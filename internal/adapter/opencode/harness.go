package opencode

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/adapter/bridge"
	"github.com/beeemT/substrate/internal/config"
)

const (
	// defaultBinary is the opencode binary name resolved via PATH.
	defaultBinary = "opencode"
	// defaultHostname is the default bind address for opencode serve.
	defaultHostname = "127.0.0.1"
	// healthCheckAttempts is the number of health check retries.
	healthCheckAttempts = 10
	// healthCheckInterval is the delay between health check retries.
	healthCheckInterval = 500 * time.Millisecond
)

// serverURLPattern matches the "Server running on http://..." line from stdout.
var serverURLPattern = regexp.MustCompile(`Server running on (http://[^\s]+)`)

// foremanMCPBridgeName is the filename stem used to locate the foreman MCP bridge.
const foremanMCPBridgeName = "opencode-foreman-mcp"

// resolveMCPBridgePath locates the foreman MCP bridge script relative to
// the substrate executable, using the same candidate resolution as other bridges.
// Returns empty string if not found.
func resolveMCPBridgePath() string {
	execPath, err := os.Executable()
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(execPath); err == nil {
		execPath = resolved
	}
	candidates := bridge.BridgeCandidates("", execPath, foremanMCPBridgeName)
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

// Harness implements adapter.AgentHarness and adapter.HarnessActionRunner
// for the OpenCode HTTP server (opencode serve).
type Harness struct {
	cfg           config.OpenCodeConfig
	workspaceRoot string
}

// NewHarness creates a new OpenCode harness.
func NewHarness(cfg config.OpenCodeConfig, workspaceRoot string) *Harness {
	return &Harness{
		cfg:           cfg,
		workspaceRoot: workspaceRoot,
	}
}

// Name returns the harness identifier.
func (h *Harness) Name() string { return "opencode" }

// Capabilities returns the harness capabilities.
func (h *Harness) Capabilities() adapter.HarnessCapabilities {
	return adapter.HarnessCapabilities{
		SupportsStreaming:    true,
		SupportsMessaging:    true,
		SupportsNativeResume: true,
		SupportedTools: []string{
			"Read", "Write", "Edit", "Bash", "Glob", "Grep",
			"mcp__substrate-foreman__ask_foreman",
		},
	}
}

// SupportsCompact reports that OpenCode supports native compaction.
func (h *Harness) SupportsCompact() bool { return true }

// ValidateReadiness checks that the opencode binary is available on PATH.
func ValidateReadiness(cfg config.OpenCodeConfig) error {
	binary := cfg.ServerPath
	if binary == "" {
		binary = defaultBinary
	}
	if _, err := exec.LookPath(binary); err != nil {
		return fmt.Errorf("opencode binary not found: %w", err)
	}
	return nil
}

// RunAction executes harness control-plane actions.
func (h *Harness) RunAction(ctx context.Context, req adapter.HarnessActionRequest) (adapter.HarnessActionResult, error) {
	switch req.Action {
	case "check_auth":
		binary := h.cfg.ServerPath
		if binary == "" {
			binary = defaultBinary
		}
		if _, err := exec.LookPath(binary); err != nil {
			return adapter.HarnessActionResult{}, fmt.Errorf("opencode binary not found: %w", err)
		}

		// Verify the binary responds to --version.
		probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		out, err := exec.CommandContext(probeCtx, binary, "--version").CombinedOutput()
		if err != nil {
			return adapter.HarnessActionResult{}, fmt.Errorf(
				"opencode --version failed: %w: %s", err, strings.TrimSpace(string(out)),
			)
		}

		return adapter.HarnessActionResult{
			Success:  true,
			Message:  "opencode binary available",
			Identity: binary,
		}, nil

	case "login_provider":
		switch req.Provider {
		case "github":
			out, err := exec.CommandContext(ctx, "gh", "auth", "token").CombinedOutput()
			if err != nil {
				return adapter.HarnessActionResult{}, fmt.Errorf("gh auth token: %w: %s", err, strings.TrimSpace(string(out)))
			}
			token := strings.TrimSpace(string(out))
			if token == "" {
				return adapter.HarnessActionResult{}, errors.New("gh auth token returned empty output")
			}
			return adapter.HarnessActionResult{
				Success:      true,
				Message:      "github login succeeded",
				Credentials:  map[string]string{"token": token},
				NeedsConfirm: true,
			}, nil

		case "sentry":
			cmd := exec.CommandContext(ctx, "sentry", "auth", "login")
			cmd.Env = config.SentryCLIEnvironment(req.Inputs["base_url"])
			cmd.Stdin = os.Stdin
			var stdout, stderr bytes.Buffer
			cmd.Stdout = io.MultiWriter(os.Stdout, &stdout)
			cmd.Stderr = io.MultiWriter(os.Stderr, &stderr)
			if err := cmd.Run(); err != nil {
				combined := strings.TrimSpace(stdout.String()) + " " + strings.TrimSpace(stderr.String())
				return adapter.HarnessActionResult{}, fmt.Errorf("sentry auth login: %w: %s", err, combined)
			}
			return adapter.HarnessActionResult{Success: true, Message: "sentry login succeeded"}, nil

		default:
			return adapter.HarnessActionResult{}, fmt.Errorf("unsupported provider %q", req.Provider)
		}

	default:
		return adapter.HarnessActionResult{}, fmt.Errorf("unsupported opencode action %q", req.Action)
	}
}

// StartSession creates a new agent session by launching the opencode serve
// child process and connecting via HTTP/SSE.
func (h *Harness) StartSession(ctx context.Context, opts adapter.SessionOpts) (_ adapter.AgentSession, err error) {
	if opts.Mode == "" {
		opts.Mode = adapter.SessionModeAgent
	}

	// Resolve binary path.
	binary := h.cfg.ServerPath
	if binary == "" {
		binary = defaultBinary
	}

	// Determine working directory.
	workDir := opts.WorktreePath
	if workDir == "" {
		workDir = h.workspaceRoot
	}

	// Build opencode serve arguments.
	hostname := h.cfg.Hostname
	if hostname == "" {
		hostname = defaultHostname
	}
	port := h.cfg.Port

	args := []string{"serve"}
	if port > 0 {
		args = append(args, "--port", strconv.Itoa(port))
	}
	args = append(args, "--hostname", hostname)

	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = workDir

	// Capture stdout to detect the server URL.
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("create stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("create stderr pipe: %w", err)
	}

	// Ensure the child process is killed if StartSession fails after this point.
	processStarted := false
	defer func() {
		if err != nil && processStarted {
			if killErr := cmd.Process.Kill(); killErr != nil {
				slog.Warn("opencode: failed to kill child process on error", "error", killErr)
			}
			cmd.Wait() //nolint:errcheck // best-effort cleanup
		}
	}()

	if err = cmd.Start(); err != nil {
		return nil, fmt.Errorf("start opencode serve: %w", err)
	}
	processStarted = true
	slog.Info("opencode: child process started", "pid", cmd.Process.Pid)

	// Drain stderr in background.
	go func() {
		scanner := bufio.NewScanner(stderrPipe)
		for scanner.Scan() {
			slog.Debug("opencode: stderr", "line", scanner.Text())
		}
	}()

	// Detect the "Server running on http://..." line from stdout.
	serverURL, err := detectServerURL(stdoutPipe, time.After(15*time.Second))
	if err != nil {
		return nil, fmt.Errorf("detect server URL: %w", err)
	}
	slog.Info("opencode: server detected", "url", serverURL)

	// Health check: retry GET /session until the server is ready.
	httpClient := &http.Client{Timeout: 10 * time.Second}
	if err := healthCheck(httpClient, serverURL); err != nil {
		return nil, fmt.Errorf("health check: %w", err)
	}

	// Create session log directory and file.
	sessionLogDir := opts.SessionLogDir
	if sessionLogDir == "" {
		sessionLogDir = filepath.Join(workDir, ".opencode", "logs")
	}
	if err = os.MkdirAll(sessionLogDir, 0o750); err != nil {
		return nil, fmt.Errorf("create session log dir: %w", err)
	}
	logPath := filepath.Join(sessionLogDir, opts.SessionID+".log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open session log: %w", err)
	}

	// Check for resume.
	openCodeSessionID := ""
	if opts.ResumeInfo != nil {
		openCodeSessionID = opts.ResumeInfo["opencode_session_id"]
	}

	// If not resuming, create a new session via POST /session.
	if openCodeSessionID == "" {
		createReq := CreateSessionRequest{Agent: h.cfg.Agent}
		if createReq.Agent == "" {
			createReq.Agent = "build"
		}
		data, marshalErr := json.Marshal(createReq)
		if marshalErr != nil {
			logFile.Close()
			return nil, fmt.Errorf("marshal create session: %w", marshalErr)
		}
		postCtx, postCancel := context.WithTimeout(ctx, 10*time.Second)
		defer postCancel()
		req, reqErr := http.NewRequestWithContext(postCtx, http.MethodPost, serverURL+"/session", bytes.NewReader(data))
		if reqErr != nil {
			logFile.Close()
			return nil, fmt.Errorf("create session request: %w", reqErr)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, postErr := httpClient.Do(req)
		if postErr != nil {
			logFile.Close()
			return nil, fmt.Errorf("create session: %w", postErr)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
			respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
			logFile.Close()
			return nil, fmt.Errorf("create session: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
		}

		var sessionResp CreateSessionResponse
		if unmarshalErr := json.NewDecoder(resp.Body).Decode(&sessionResp); unmarshalErr != nil {
			logFile.Close()
			return nil, fmt.Errorf("decode create session response: %w", unmarshalErr)
		}
		openCodeSessionID = sessionResp.ID
		if openCodeSessionID == "" {
			logFile.Close()
			return nil, errors.New("create session: server returned empty session ID")
		}
	}

	// Register the foreman MCP bridge server so the agent can invoke ask_foreman.
	mcpPath := resolveMCPBridgePath()
	if mcpPath != "" {
		mcpReq := ConnectMCPRequest{
			Transport: "stdio",
			Name:      "substrate-foreman",
			Command:   mcpPath,
		}
		mcpData, mcpErr := json.Marshal(mcpReq)
		if mcpErr == nil {
			mcpCtx, mcpCancel := context.WithTimeout(ctx, 10*time.Second)
			defer mcpCancel()
			mcpHTTPReq, mcpReqErr := http.NewRequestWithContext(mcpCtx, http.MethodPost, serverURL+"/mcp", bytes.NewReader(mcpData))
			if mcpReqErr == nil {
				mcpHTTPReq.Header.Set("Content-Type", "application/json")
				mcpResp, mcpErr := httpClient.Do(mcpHTTPReq)
				if mcpErr != nil {
					slog.Warn("opencode: failed to register foreman MCP server", "error", mcpErr)
				} else {
					mcpResp.Body.Close()
					if mcpResp.StatusCode < 200 || mcpResp.StatusCode >= 300 {
						slog.Warn("opencode: MCP registration returned non-success status", "status", mcpResp.StatusCode)
					}
				}
			} else {
				slog.Warn("opencode: failed to create MCP registration request", "error", mcpReqErr)
			}
		} else {
			slog.Warn("opencode: failed to marshal MCP request", "error", mcpErr)
		}
	}

	// Build the session struct.
	s := &session{
		id:         opts.SessionID,
		mode:       opts.Mode,
		serverURL:  serverURL,
		openCodeID: openCodeSessionID,
		httpClient: httpClient,
		events:     make(chan adapter.AgentEvent, eventChannelSize),
		logFile:    logFile,
		logPath:    logPath,
		logDir:     sessionLogDir,
		workDir:    workDir,
		cmd:        cmd,
		variant:    h.cfg.Variant,
		waitDone:   make(chan struct{}),
	}

	// Start the SSE reader goroutine.
	s.startSSEReader(ctx)

	// Send the initial prompt for agent mode.
	if opts.Mode == adapter.SessionModeAgent && opts.UserPrompt != "" {
		prompt := opts.UserPrompt
		if opts.SystemPrompt != "" {
			prompt = opts.SystemPrompt + "\n\n" + opts.UserPrompt
		}
		if sendErr := s.SendMessage(ctx, prompt); sendErr != nil {
			slog.Warn("opencode: failed to send initial prompt", "error", sendErr)
			s.Abort(ctx) //nolint:errcheck // best-effort cleanup
			return nil, fmt.Errorf("send initial prompt: %w", sendErr)
		}
	}

	return s, nil
}

// detectServerURL reads stdout until the "Server running on ..." line appears
// or the timeout fires.
func detectServerURL(stdout io.Reader, timeout <-chan time.Time) (string, error) {
	type result struct {
		url string
		err error
	}
	ch := make(chan result, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 4096), 4096)
		for scanner.Scan() {
			line := scanner.Text()
			slog.Debug("opencode: stdout", "line", line)
			if m := serverURLPattern.FindStringSubmatch(line); m != nil {
				ch <- result{url: m[1]}
				return
			}
		}
		if err := scanner.Err(); err != nil {
			ch <- result{err: fmt.Errorf("read stdout: %w", err)}
		} else {
			ch <- result{err: errors.New("server URL not found in stdout")}
		}
	}()

	select {
	case r := <-ch:
		return r.url, r.err
	case <-timeout:
		return "", errors.New("timed out waiting for opencode server to start")
	}
}

// healthCheck retries GET /session until the server responds with 200.
func healthCheck(client *http.Client, baseURL string) error {
	for i := range healthCheckAttempts {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/session", nil)
		if err != nil {
			cancel()
			return fmt.Errorf("create health check request: %w", err)
		}
		resp, err := client.Do(req)
		cancel()
		if err != nil {
			slog.Debug("opencode: health check failed", "attempt", i+1, "error", err)
			time.Sleep(healthCheckInterval)
			continue
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return nil
		}
		slog.Debug("opencode: health check unexpected status", "attempt", i+1, "status", resp.StatusCode)
		time.Sleep(healthCheckInterval)
	}
	return fmt.Errorf("server not ready after %d attempts", healthCheckAttempts)
}

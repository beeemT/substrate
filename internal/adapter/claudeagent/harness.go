// Package claudeagent implements the Claude Agent SDK harness.
// It spawns the claude-agent bridge subprocess and manages JSON-line I/O.
package claudeagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/config"
)

// Harness implements adapter.AgentHarness for the Claude Agent SDK bridge.
type Harness struct {
	cfg           config.ClaudeCodeConfig
	workspaceRoot string
}

// NewHarness creates a new Claude Agent SDK harness.
func NewHarness(cfg config.ClaudeCodeConfig, workspaceRoot string) *Harness {
	return &Harness{
		cfg:           cfg,
		workspaceRoot: workspaceRoot,
	}
}

// Name returns the harness identifier.
func (h *Harness) Name() string {
	return "claude-code"
}

// Capabilities returns the harness capabilities.
func (h *Harness) Capabilities() adapter.HarnessCapabilities {
	return adapter.HarnessCapabilities{
		SupportsStreaming:    true,
		SupportsMessaging:    true,
		SupportsNativeResume: true,
		SupportedTools: []string{
			"Read", "Write", "Edit", "Bash", "Glob", "Grep",
			"WebSearch", "WebFetch", "mcp__substrate__ask_foreman",
		},
	}
}

// ValidateReadiness verifies that the claude-agent bridge can run with the configured runtime prerequisites.
func ValidateReadiness(cfg config.ClaudeCodeConfig) error {
	_, _, err := resolveReadyBridgeRuntime(cfg)
	return err
}

func resolveReadyBridgeRuntime(cfg config.ClaudeCodeConfig) (bridgeRuntime, string, error) {
	rt, err := resolveBridgeRuntime(cfg.BridgePath)
	if err != nil {
		return bridgeRuntime{}, "", err
	}
	if !rt.NeedsBun {
		return rt, "", nil
	}

	bunPath, err := resolveBunExecutable(cfg.BunPath)
	if err != nil {
		return bridgeRuntime{}, "", err
	}
	if err := ensureBridgeDependencies(rt); err != nil {
		return bridgeRuntime{}, "", err
	}

	return rt, bunPath, nil
}

// StartSession spawns a new agent session with the given options.
func (h *Harness) StartSession(ctx context.Context, opts adapter.SessionOpts) (adapter.AgentSession, error) {
	if opts.Mode == "" {
		opts.Mode = adapter.SessionModeAgent
	}

	// Determine working directory.
	workDir := opts.WorktreePath
	if workDir == "" {
		workDir = h.workspaceRoot // foreman uses workspace root
	}

	globalDir, err := config.GlobalDir()
	if err != nil {
		return nil, fmt.Errorf("get global dir: %w", err)
	}

	// Determine session log directory.
	sessionLogDir := opts.SessionLogDir
	if sessionLogDir == "" {
		sessionLogDir = filepath.Join(globalDir, "sessions")
	}

	bridgeRt, bunPath, err := resolveReadyBridgeRuntime(h.cfg)
	if err != nil {
		return nil, err
	}
	bunCacheDir := filepath.Join(globalDir, "bun-cache")

	// Build the command.
	var cmd *exec.Cmd
	if runtime.GOOS == "darwin" {
		cmd, err = buildDarwinSandboxCmd(ctx, bridgeRt, workDir, bunPath, bunCacheDir)
	} else {
		cmd, err = buildLinuxSandboxCmd(ctx, bridgeRt, workDir, bunPath, bunCacheDir)
	}
	if err != nil {
		return nil, err
	}

	cmd.Dir = bridgeRt.launchDir(workDir)

	// Build environment.
	// Config is passed via init message, not env (avoids exposure in /proc/pid/environ).
	env := os.Environ()
	if bridgeRt.NeedsBun {
		env = append(env, "BUN_INSTALL_CACHE_DIR="+bunCacheDir)
	}
	env = append(env,
		"SUBSTRATE_WORKTREE_PATH="+workDir,
		"SUBSTRATE_SESSION_LOG_PATH="+filepath.Join(sessionLogDir, opts.SessionID+".log"),
	)
	cmd.Env = env

	// Get stdin/stdout pipes.
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("create stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("create stdout pipe: %w", err)
	}

	// Capture stderr for debugging.
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("create stderr pipe: %w", err)
	}

	// Start the subprocess.
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start bridge: %w", err)
	}

	// Create session log writer.
	sessionLogPath := filepath.Join(sessionLogDir, opts.SessionID+".log")
	if err := os.MkdirAll(sessionLogDir, 0o750); err != nil {
		cmd.Process.Kill() //nolint:errcheck // best-effort cleanup
		cmd.Wait()         //nolint:errcheck // best-effort cleanup

		return nil, fmt.Errorf("create session log dir: %w", err)
	}

	logFile, err := os.OpenFile(sessionLogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		cmd.Process.Kill() //nolint:errcheck // best-effort cleanup
		cmd.Wait()         //nolint:errcheck // best-effort cleanup

		return nil, fmt.Errorf("open session log: %w", err)
	}

	// Create the session object.
	session := &claudeAgentSession{
		id:      opts.SessionID,
		mode:    opts.Mode,
		cmd:     cmd,
		stdin:   stdin,
		stdout:  stdout,
		stderr:  stderr,
		events:  make(chan adapter.AgentEvent, 64),
		logFile: logFile,
		logPath: sessionLogPath,
		logDir:  sessionLogDir,
		workDir: workDir,
		mu:      sync.Mutex{},
		aborted: false,
	}

	// Start reading events in background.
	go session.readEvents()
	go session.readStderr()

	// Send init message unconditionally — all config goes through the message, not env.
	initMsg := bridgeInitMsg{
		Type:            "init",
		Mode:            string(opts.Mode),
		SystemPrompt:    opts.SystemPrompt,
		ResumeSessionID: opts.ResumeSessionID,
		PermissionMode:  h.cfg.PermissionMode,
		Model:           h.cfg.Model,
		MaxTurns:        h.cfg.MaxTurns,
		MaxBudgetUSD:    h.cfg.MaxBudgetUSD,
	}
	data, err := json.Marshal(initMsg)
	if err != nil {
		session.Abort(ctx) //nolint:errcheck // best-effort cleanup
		return nil, fmt.Errorf("marshal init message: %w", err)
	}
	session.mu.Lock()
	_, err = session.stdin.Write(append(data, '\n'))
	session.mu.Unlock()
	if err != nil {
		session.Abort(ctx) //nolint:errcheck // best-effort cleanup
		return nil, fmt.Errorf("send init message: %w", err)
	}

	// Send initial prompt for agent mode (foreman mode receives messages via SendMessage).
	if opts.Mode == adapter.SessionModeAgent && opts.UserPrompt != "" {
		if err := session.sendPrompt(opts.UserPrompt); err != nil {
			session.Abort(ctx) //nolint:errcheck // best-effort cleanup

			return nil, fmt.Errorf("send initial prompt: %w", err)
		}
	}

	return session, nil
}

// bridgeMsg represents a message sent to the bridge subprocess.
type bridgeMsg struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// bridgeInitMsg is sent first to configure the session.
// All config is passed here rather than via env to avoid exposure in /proc/pid/environ.
type bridgeInitMsg struct {
	Type            string  `json:"type"`
	Mode            string  `json:"mode"`
	SystemPrompt    string  `json:"system_prompt,omitempty"`
	ResumeSessionID string  `json:"resume_session_id,omitempty"`
	PermissionMode  string  `json:"permission_mode,omitempty"`
	Model           string  `json:"model,omitempty"`
	MaxTurns        int     `json:"max_turns,omitempty"`
	MaxBudgetUSD    float64 `json:"max_budget_usd,omitempty"`
}

func resolveBunExecutable(configured string) (string, error) {
	bunPath := strings.TrimSpace(configured)
	if bunPath == "" {
		bunPath = "bun"
	}

	resolved, err := exec.LookPath(bunPath)
	if err != nil {
		return "", fmt.Errorf("resolve bun %q: %w", bunPath, err)
	}

	return resolved, nil
}

type bridgeRuntime struct {
	Path     string
	NeedsBun bool
}

func (r bridgeRuntime) launchDir(workDir string) string {
	if r.NeedsBun {
		return filepath.Dir(r.Path)
	}

	return workDir
}

func resolveBridgeRuntime(configured string) (bridgeRuntime, error) {
	executablePath, err := os.Executable()
	if err != nil {
		return bridgeRuntime{}, fmt.Errorf("locate substrate executable: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(executablePath); err == nil {
		executablePath = resolved
	}

	return resolveBridgeRuntimeFrom(configured, executablePath)
}

func resolveBridgeRuntimeFrom(configured, executablePath string) (bridgeRuntime, error) {
	checked := make([]string, 0, 6)
	for _, candidate := range bridgeCandidates(strings.TrimSpace(configured), executablePath) {
		if candidate == "" {
			continue
		}
		checked = append(checked, candidate)
		info, err := os.Stat(candidate)
		if err != nil {
			continue
		}
		if info.IsDir() {
			continue
		}
		absolute, err := filepath.Abs(candidate)
		if err != nil {
			return bridgeRuntime{}, fmt.Errorf("resolve bridge path %q: %w", candidate, err)
		}

		return bridgeRuntime{
			Path:     absolute,
			NeedsBun: isBridgeScript(absolute),
		}, nil
	}

	return bridgeRuntime{}, fmt.Errorf("resolve claude-agent bridge: no bridge binary or script found; checked %s", strings.Join(dedupePaths(checked), ", "))
}

func ensureBridgeDependencies(rt bridgeRuntime) error {
	if !rt.NeedsBun {
		return nil
	}

	runtimeDir := filepath.Dir(rt.Path)
	packagePath := filepath.Join(runtimeDir, "package.json")
	if _, err := os.Stat(packagePath); err != nil {
		if os.IsNotExist(err) {
			return nil
		}

		return fmt.Errorf("check bridge package metadata: %w", err)
	}

	dependencyPath := filepath.Join(runtimeDir, "node_modules", "@anthropic-ai", "claude-agent-sdk")
	if _, err := os.Stat(dependencyPath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("claude-agent bridge dependencies missing under %s; run `bun install --cwd %s`", runtimeDir, runtimeDir)
		}

		return fmt.Errorf("check claude-agent bridge dependencies: %w", err)
	}

	return nil
}

func isBridgeScript(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".js", ".mjs", ".cjs", ".ts", ".mts", ".cts":
		return true
	default:
		return false
	}
}

func bridgeCandidates(configured, executablePath string) []string {
	execDir := filepath.Dir(executablePath)
	shareDir := filepath.Clean(filepath.Join(execDir, "..", "share", "substrate"))

	if configured != "" {
		if filepath.IsAbs(configured) {
			return []string{configured}
		}

		candidates := []string{
			filepath.Join(execDir, configured),
			filepath.Join(shareDir, configured),
		}
		if absolute, err := filepath.Abs(configured); err == nil {
			candidates = append(candidates, absolute)
		} else {
			candidates = append(candidates, configured)
		}

		return dedupePaths(candidates)
	}

	return dedupePaths([]string{
		filepath.Join(execDir, "bridge", "claude-agent-bridge"),
		filepath.Join(shareDir, "bridge", "claude-agent-bridge"),
		filepath.Join(execDir, "bridge", "claude-agent-bridge.ts"),
		filepath.Join(shareDir, "bridge", "claude-agent-bridge.ts"),
	})
}

func dedupePaths(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		if path == "" {
			continue
		}
		path = filepath.Clean(path)
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		result = append(result, path)
	}

	return result
}

// escapeSandboxPath escapes a path for use in a sandbox-exec profile.
// It replaces backslashes and double quotes to prevent profile injection.
func escapeSandboxPath(path string) string {
	path = strings.ReplaceAll(path, "\\", "\\\\")
	path = strings.ReplaceAll(path, "\"", "\\\"")

	return path
}

func (h *Harness) RunAction(ctx context.Context, req adapter.HarnessActionRequest) (adapter.HarnessActionResult, error) {
	switch req.Action {
	case "check_auth":
		bridgeRt, bunPath, err := resolveReadyBridgeRuntime(h.cfg)
		if err != nil {
			return adapter.HarnessActionResult{}, err
		}
		if bunPath != "" {
			return adapter.HarnessActionResult{
				Success:  true,
				Message:  "claude-agent bridge available",
				Identity: bridgeRt.Path + " via " + bunPath,
			}, nil
		}

		return adapter.HarnessActionResult{
			Success:  true,
			Message:  "claude-agent bridge available",
			Identity: bridgeRt.Path,
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
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			cmd.Stdout = io.MultiWriter(os.Stdout, &stdout)
			cmd.Stderr = io.MultiWriter(os.Stderr, &stderr)
			if err := cmd.Run(); err != nil {
				combined := strings.TrimSpace(strings.TrimSpace(stdout.String()) + " " + strings.TrimSpace(stderr.String()))

				return adapter.HarnessActionResult{}, fmt.Errorf("sentry auth login: %w: %s", err, combined)
			}

			return adapter.HarnessActionResult{Success: true, Message: "sentry login succeeded"}, nil
		default:
			return adapter.HarnessActionResult{}, fmt.Errorf("unsupported provider %q", req.Provider)
		}
	default:
		return adapter.HarnessActionResult{}, fmt.Errorf("unsupported claude-agent action %q", req.Action)
	}
}

// buildDarwinSandboxCmd constructs a sandbox-exec command for macOS.
func buildDarwinSandboxCmd(ctx context.Context, rt bridgeRuntime, workDir, bunPath, bunCacheDir string) (*exec.Cmd, error) {
	sessionTmpDir, err := os.MkdirTemp("", "substrate-session-*")
	if err != nil {
		return nil, fmt.Errorf("create session temp dir: %w", err)
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("get user home dir: %w", err)
	}
	claudeDir := filepath.Join(homeDir, ".claude")
	w := escapeSandboxPath(workDir)
	tmp := escapeSandboxPath(sessionTmpDir)
	cl := escapeSandboxPath(claudeDir)
	const allow = `(version 1)(allow default)(deny file-write* (subpath "/"))`
	if rt.NeedsBun {
		bc := escapeSandboxPath(bunCacheDir)
		profile := allow +
			fmt.Sprintf(`(allow file-write* (subpath "%s"))(allow file-write* (subpath "%s"))(allow file-write* (subpath "%s"))(allow file-write* (subpath "%s"))(allow file-write* (literal "/dev/null"))`,
				w, tmp, cl, bc)
		return exec.CommandContext(ctx, "sandbox-exec", "-p", profile, bunPath, "run", rt.Path), nil
	}
	profile := allow +
		fmt.Sprintf(`(allow file-write* (subpath "%s"))(allow file-write* (subpath "%s"))(allow file-write* (subpath "%s"))(allow file-write* (literal "/dev/null"))`,
			w, tmp, cl)
	return exec.CommandContext(ctx, "sandbox-exec", "-p", profile, rt.Path), nil
}

// buildLinuxSandboxCmd constructs a bubblewrap command for Linux, falling back
// to an unsandboxed invocation when bwrap is not installed.
func buildLinuxSandboxCmd(ctx context.Context, rt bridgeRuntime, workDir, bunPath, bunCacheDir string) (*exec.Cmd, error) {
	sessionTmpDir, err := os.MkdirTemp("", "substrate-session-*")
	if err != nil {
		return nil, fmt.Errorf("create session temp dir: %w", err)
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("get user home dir: %w", err)
	}
	claudeDir := filepath.Join(homeDir, ".claude")
	bwrapPath, lookErr := exec.LookPath("bwrap")
	if lookErr != nil {
		slog.Warn("bubblewrap (bwrap) not found; running bridge without sandbox", "error", lookErr)
		if rt.NeedsBun {
			return exec.CommandContext(ctx, bunPath, "run", rt.Path), nil
		}
		return exec.CommandContext(ctx, rt.Path), nil
	}
	bwrapArgs := []string{
		"--ro-bind", "/", "/",
		"--bind", workDir, workDir,
		"--bind", sessionTmpDir, sessionTmpDir,
		"--bind", claudeDir, claudeDir,
		"--dev", "/dev",
		"--proc", "/proc",
		"--unshare-pid",
		"--die-with-parent",
	}
	if rt.NeedsBun {
		bwrapArgs = append(bwrapArgs, "--bind", bunCacheDir, bunCacheDir)
		bwrapArgs = append(bwrapArgs, bunPath, "run", rt.Path)
	} else {
		bwrapArgs = append(bwrapArgs, rt.Path)
	}
	return exec.CommandContext(ctx, bwrapPath, bwrapArgs...), nil
}

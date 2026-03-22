// Package omp implements the oh-my-pi agent harness.
// It spawns the bridge subprocess and manages JSON-line I/O.
package omp

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/config"
)

// OhMyPiHarness implements adapter.AgentHarness for oh-my-pi.
type OhMyPiHarness struct {
	cfg           config.OhMyPiConfig
	workspaceRoot string
}

// NewHarness creates a new oh-my-pi harness.
func NewHarness(cfg config.OhMyPiConfig, workspaceRoot string) *OhMyPiHarness {
	return &OhMyPiHarness{
		cfg:           cfg,
		workspaceRoot: workspaceRoot,
	}
}

// Name returns the harness identifier.
func (h *OhMyPiHarness) Name() string {
	return "omp"
}

// Capabilities returns the harness capabilities.
func (h *OhMyPiHarness) Capabilities() adapter.HarnessCapabilities {
	return adapter.HarnessCapabilities{
		SupportsStreaming:    true,
		SupportsMessaging:    true,
		SupportsNativeResume: true,
		SupportedTools:       []string{"read", "grep", "find", "edit", "write", "bash", "ask_foreman"},
	}
}

// ValidateReadiness verifies that the oh-my-pi bridge can run with the configured runtime prerequisites.
func ValidateReadiness(cfg config.OhMyPiConfig) error {
	_, _, err := resolveReadyBridgeRuntime(cfg)

	return err
}

func resolveReadyBridgeRuntime(cfg config.OhMyPiConfig) (bridgeRuntime, string, error) {
	runtime, err := resolveBridgeRuntime(cfg.BridgePath)
	if err != nil {
		return bridgeRuntime{}, "", err
	}
	if !runtime.NeedsBun {
		return runtime, "", nil
	}

	bunPath, err := resolveBunExecutable(cfg.BunPath)
	if err != nil {
		return bridgeRuntime{}, "", err
	}
	if err := ensureBridgeDependencies(runtime); err != nil {
		return bridgeRuntime{}, "", err
	}

	return runtime, bunPath, nil
}

// StartSession spawns a new agent session with the given options.
func (h *OhMyPiHarness) StartSession(ctx context.Context, opts adapter.SessionOpts) (adapter.AgentSession, error) {
	if opts.Mode == "" {
		opts.Mode = adapter.SessionModeAgent
	}

	// Determine working directory
	workDir := opts.WorktreePath
	if workDir == "" {
		workDir = h.workspaceRoot // foreman uses workspace root
	}

	globalDir, err := config.GlobalDir()
	if err != nil {
		return nil, fmt.Errorf("get global dir: %w", err)
	}

	// Determine session log directory
	sessionLogDir := opts.SessionLogDir
	if sessionLogDir == "" {
		sessionLogDir = filepath.Join(globalDir, "sessions")
	}

	bridgeRuntime, bunPath, err := resolveReadyBridgeRuntime(h.cfg)
	if err != nil {
		return nil, err
	}
	bunCacheDir := filepath.Join(globalDir, "bun-cache")

	// Build the command
	var cmd *exec.Cmd

	if runtime.GOOS == "darwin" {
		// Create session temp directory atomically to prevent symlink race on predictable paths
		sessionTmpDir, err := os.MkdirTemp("", "substrate-session-*")
		if err != nil {
			return nil, fmt.Errorf("create session temp dir: %w", err)
		}

		// macOS sandbox-exec profile
		// Escape paths to prevent profile injection
		escapedWorkDir := escapeSandboxPath(workDir)
		escapedTmpDir := escapeSandboxPath(sessionTmpDir)

		homeDir, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("get user home dir: %w", err)
		}
		escapedOMPDir := escapeSandboxPath(filepath.Join(homeDir, ".omp"))
		profile := fmt.Sprintf(
			`(version 1)(allow default)(deny file-write* (subpath "/"))(allow file-write* (subpath "%s"))(allow file-write* (subpath "%s"))(allow file-write* (subpath "%s"))(allow file-write* (literal "/dev/null"))`,
			escapedWorkDir, escapedTmpDir, escapedOMPDir,
		)
		if bridgeRuntime.NeedsBun {
			escapedBunCacheDir := escapeSandboxPath(bunCacheDir)
			profile = fmt.Sprintf(
				`(version 1)(allow default)(deny file-write* (subpath "/"))(allow file-write* (subpath "%s"))(allow file-write* (subpath "%s"))(allow file-write* (subpath "%s"))(allow file-write* (subpath "%s"))(allow file-write* (literal "/dev/null"))`,
				escapedWorkDir, escapedTmpDir, escapedOMPDir, escapedBunCacheDir,
			)
			cmd = exec.CommandContext(ctx, "sandbox-exec", "-p", profile, bunPath, "run", bridgeRuntime.Path)
		} else {
			cmd = exec.CommandContext(ctx, "sandbox-exec", "-p", profile, bridgeRuntime.Path)
		}
	} else {
		// Linux: use unshare for mount namespace isolation
		// This is a simplified version; production would need more sophisticated setup
		// TODO: Implement Linux namespace isolation with unshare --mount
		if bridgeRuntime.NeedsBun {
			cmd = exec.CommandContext(ctx, bunPath, "run", bridgeRuntime.Path)
		} else {
			cmd = exec.CommandContext(ctx, bridgeRuntime.Path)
		}
	}

	cmd.Dir = bridgeRuntime.launchDir(workDir)

	// Build environment
	env := os.Environ()
	if bridgeRuntime.NeedsBun {
		env = append(env, "BUN_INSTALL_CACHE_DIR="+bunCacheDir)
	}
	env = append(env,
		"SUBSTRATE_BRIDGE_MODE="+string(opts.Mode),
		"SUBSTRATE_THINKING_LEVEL="+h.cfg.ThinkingLevel,
		"SUBSTRATE_ALLOW_PUSH="+strconv.FormatBool(opts.AllowPush),
		"SUBSTRATE_WORKTREE_PATH="+workDir,
		"SUBSTRATE_SESSION_LOG_PATH="+filepath.Join(sessionLogDir, opts.SessionID+".log"),
	)

	// Encode system prompt as base64 to avoid escaping issues
	if opts.SystemPrompt != "" {
		encoded := base64.StdEncoding.EncodeToString([]byte(opts.SystemPrompt))
		env = append(env, "SUBSTRATE_SYSTEM_PROMPT="+encoded)
	}

	if opts.ResumeSessionFile != "" {
		env = append(env, "SUBSTRATE_RESUME_SESSION_FILE="+opts.ResumeSessionFile)
	}

	cmd.Env = env

	// Get stdin/stdout pipes
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("create stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("create stdout pipe: %w", err)
	}

	// Capture stderr for debugging
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("create stderr pipe: %w", err)
	}

	// Start the subprocess
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start bridge: %w", err)
	}

	// Create session log writer
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

	// Create the session object
	session := &ohMyPiSession{
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

	// Start reading events in background
	go session.readEvents()
	go session.readStderr()

	// Send initial prompt if provided (agent mode)
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

	return bridgeRuntime{}, fmt.Errorf("resolve ohmypi bridge: no bridge binary or script found; checked %s", strings.Join(dedupePaths(checked), ", "))
}

func ensureBridgeDependencies(runtime bridgeRuntime) error {
	if !runtime.NeedsBun {
		return nil
	}

	runtimeDir := filepath.Dir(runtime.Path)
	packagePath := filepath.Join(runtimeDir, "package.json")
	if _, err := os.Stat(packagePath); err != nil {
		if os.IsNotExist(err) {
			return nil
		}

		return fmt.Errorf("check bridge package metadata: %w", err)
	}

	dependencyPath := filepath.Join(runtimeDir, "node_modules", "@oh-my-pi", "pi-coding-agent")
	if _, err := os.Stat(dependencyPath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("ohmypi source bridge dependencies missing under %s; run `bun install --cwd %s`", runtimeDir, runtimeDir)
		}

		return fmt.Errorf("check ohmypi source bridge dependencies: %w", err)
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
		filepath.Join(execDir, "bridge", "omp-bridge"),
		filepath.Join(shareDir, "bridge", "omp-bridge"),
		filepath.Join(execDir, "bridge", "omp-bridge.ts"),
		filepath.Join(shareDir, "bridge", "omp-bridge.ts"),
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
	// Replace backslashes first, then double quotes
	path = strings.ReplaceAll(path, "\\", "\\\\")
	path = strings.ReplaceAll(path, "\"", "\\\"")

	return path
}

func (h *OhMyPiHarness) RunAction(ctx context.Context, req adapter.HarnessActionRequest) (adapter.HarnessActionResult, error) {
	switch req.Action {
	case "check_auth":
		bridgeRuntime, bunPath, err := resolveReadyBridgeRuntime(h.cfg)
		if err != nil {
			return adapter.HarnessActionResult{}, err
		}
		if bunPath != "" {
			return adapter.HarnessActionResult{Success: true, Message: "ohmypi bridge available", Identity: bridgeRuntime.Path + " via " + bunPath}, nil
		}

		return adapter.HarnessActionResult{Success: true, Message: "ohmypi bridge available", Identity: bridgeRuntime.Path}, nil
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

			return adapter.HarnessActionResult{Success: true, Message: "github login succeeded", Credentials: map[string]string{"token": token}, NeedsConfirm: true}, nil
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
		return adapter.HarnessActionResult{}, fmt.Errorf("unsupported ohmypi action %q", req.Action)
	}
}

// Package omp implements the oh-my-pi agent harness.
// It spawns the bridge subprocess and manages JSON-line I/O.
package omp

import (
	"context"
	"encoding/base64"
	"fmt"
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
		SupportsStreaming: true,
		SupportsMessaging: true,
		SupportedTools:    []string{"read", "grep", "find", "edit", "write", "bash", "ask_foreman"},
	}
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

	// Determine session log directory
	sessionLogDir := opts.SessionLogDir
	if sessionLogDir == "" {
		globalDir, err := config.GlobalDir()
		if err != nil {
			return nil, fmt.Errorf("get global dir: %w", err)
		}
		sessionLogDir = filepath.Join(globalDir, "sessions")
	}

	// Build the command
	var cmd *exec.Cmd
	bunPath := h.cfg.BunPath
	if bunPath == "" {
		bunPath = "bun"
	}
	bridgePath := h.cfg.BridgePath
	if bridgePath == "" {
		bridgePath = "bridge/omp-bridge.ts"
	}

	// Create session temp directory for sandbox
	sessionTmpDir := fmt.Sprintf("/tmp/substrate-%s", opts.SessionID)

	if runtime.GOOS == "darwin" {
		// macOS sandbox-exec profile
		// Escape paths to prevent profile injection
		escapedWorkDir := escapeSandboxPath(workDir)
		escapedTmpDir := escapeSandboxPath(sessionTmpDir)
		profile := fmt.Sprintf(
			`(version 1)(allow default)(deny file-write* (subpath "/"))(allow file-write* (subpath "%s"))(allow file-write* (subpath "%s"))(allow file-write* (literal "/dev/null"))`,
			escapedWorkDir, escapedTmpDir,
		)
		cmd = exec.CommandContext(ctx, "sandbox-exec", "-p", profile, bunPath, "run", bridgePath)
	} else {
		// Linux: use unshare for mount namespace isolation
		// This is a simplified version; production would need more sophisticated setup
		// TODO: Implement Linux namespace isolation with unshare --mount
		cmd = exec.CommandContext(ctx, bunPath, "run", bridgePath)
	}

	cmd.Dir = workDir

	// Build environment
	env := os.Environ()
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
	if err := os.MkdirAll(sessionLogDir, 0o755); err != nil {
		cmd.Process.Kill()
		cmd.Wait() // reap the killed process to avoid zombie
		return nil, fmt.Errorf("create session log dir: %w", err)
	}

	logFile, err := os.OpenFile(sessionLogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		cmd.Process.Kill()
		cmd.Wait() // reap the killed process to avoid zombie
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
			session.Abort(ctx)
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

// escapeSandboxPath escapes a path for use in a sandbox-exec profile.
// It replaces backslashes and double quotes to prevent profile injection.
func escapeSandboxPath(path string) string {
	// Replace backslashes first, then double quotes
	path = strings.ReplaceAll(path, "\\", "\\\\")
	path = strings.ReplaceAll(path, "\"", "\\\"")
	return path
}

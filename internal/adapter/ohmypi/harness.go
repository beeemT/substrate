// Package omp implements the oh-my-pi agent harness.
// It spawns the bridge subprocess and manages JSON-line I/O.
package omp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/adapter/bridge"
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

func (h *OhMyPiHarness) SupportsCompact() bool { return true }

// ValidateReadiness verifies that the oh-my-pi bridge can run with the configured runtime prerequisites.
func ValidateReadiness(cfg config.OhMyPiConfig) error {
	_, _, err := resolveReadyBridgeRuntime(cfg)
	return err
}

func resolveReadyBridgeRuntime(cfg config.OhMyPiConfig) (bridge.BridgeRuntime, string, error) {
	rt, err := bridge.ResolveBridgeRuntime(cfg.BridgePath, "omp-bridge", "ohmypi bridge")
	if err != nil {
		return bridge.BridgeRuntime{}, "", err
	}
	if !rt.NeedsBun {
		return rt, "", nil
	}
	bunPath, err := bridge.ResolveBunExecutable(cfg.BunPath)
	if err != nil {
		return bridge.BridgeRuntime{}, "", err
	}
	if err := bridge.EnsureBridgeDependencies(rt, "@oh-my-pi/pi-coding-agent", "ohmypi bridge"); err != nil {
		return bridge.BridgeRuntime{}, "", err
	}
	return rt, bunPath, nil
}

// bridgeInitMsg is sent before any prompt to configure the session.
type bridgeInitMsg struct {
	Type            string `json:"type"`
	SystemPrompt    string `json:"system_prompt,omitempty"`
	AnswerTimeoutMs int64  `json:"answer_timeout_ms"`
}

// StartSession spawns a new agent session with the given options.
func (h *OhMyPiHarness) StartSession(ctx context.Context, opts adapter.SessionOpts) (adapter.AgentSession, error) {
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

	// Build the sandboxed command. OS detection is handled inside BuildSandboxCmd.
	cmd, sessionTmpDir, err := bridge.BuildSandboxCmd(ctx, bridgeRt, workDir, bridge.ResolveGitDir(workDir), bunPath, bunCacheDir, ".omp")
	if err != nil {
		return nil, err
	}

	cmd.Dir = bridgeRt.LaunchDir(workDir)

	// Build environment — adapter-specific variables stay here.
	env := os.Environ()
	if bridgeRt.NeedsBun {
		env = append(env, "BUN_INSTALL_CACHE_DIR="+bunCacheDir)
	}
	env = append(env,
		"SUBSTRATE_BRIDGE_MODE="+string(opts.Mode),
		"SUBSTRATE_THINKING_LEVEL="+h.cfg.ThinkingLevel,
		"SUBSTRATE_ALLOW_PUSH="+strconv.FormatBool(opts.AllowPush),
		"SUBSTRATE_WORKTREE_PATH="+workDir,
		"SUBSTRATE_SESSION_LOG_PATH="+filepath.Join(sessionLogDir, opts.SessionID+".log"),
	)
	if resumeFile := opts.ResumeInfo["omp_session_file"]; resumeFile != "" {
		env = append(env, "SUBSTRATE_RESUME_SESSION_FILE="+resumeFile)
	}
	cmd.Env = env

	// Get stdin/stdout/stderr pipes.
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("create stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("create stdout pipe: %w", err)
	}
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

	// Assemble the BridgeSession and wire the session-meta callback.
	bs := bridge.NewBridgeSession(opts.SessionID, opts.Mode)
	bs.Cmd = cmd
	bs.Stdin = stdin
	bs.Stdout = stdout
	bs.Stderr = stderr
	bs.LogFile = logFile
	bs.LogPath = sessionLogPath
	bs.LogDir = sessionLogDir
	bs.WorkDir = workDir
	bs.TmpDir = sessionTmpDir

	session := &ohMyPiSession{bs: bs}
	bs.ParseSessionMeta = session.sessionMetaCallback

	bs.StartReaders()

	// Always send an init message — it carries the system prompt and answer timeout.
	// Using an explicit struct write avoids exposing config in /proc/pid/environ.
	initMsg := bridgeInitMsg{
		Type:            "init",
		SystemPrompt:    opts.SystemPrompt,
		AnswerTimeoutMs: opts.AnswerTimeoutMs,
	}
	data, err := json.Marshal(initMsg)
	if err != nil {
		session.Abort(ctx) //nolint:errcheck // best-effort cleanup
		return nil, fmt.Errorf("marshal init message: %w", err)
	}
	if err := session.bs.WriteRawMsg(data); err != nil {
		session.Abort(ctx) //nolint:errcheck // best-effort cleanup
		return nil, fmt.Errorf("send init message: %w", err)
	}

	// Send initial prompt if provided (agent mode).
	if opts.Mode == adapter.SessionModeAgent && opts.UserPrompt != "" {
		if err := session.bs.SendPrompt(opts.UserPrompt); err != nil {
			session.Abort(ctx) //nolint:errcheck // best-effort cleanup
			return nil, fmt.Errorf("send initial prompt: %w", err)
		}
	}

	return session, nil
}

func (h *OhMyPiHarness) RunAction(ctx context.Context, req adapter.HarnessActionRequest) (adapter.HarnessActionResult, error) {
	switch req.Action {
	case "check_auth":
		rt, bunPath, err := resolveReadyBridgeRuntime(h.cfg)
		if err != nil {
			return adapter.HarnessActionResult{}, err
		}
		if bunPath != "" {
			return adapter.HarnessActionResult{Success: true, Message: "ohmypi bridge available", Identity: rt.Path + " via " + bunPath}, nil
		}
		return adapter.HarnessActionResult{Success: true, Message: "ohmypi bridge available", Identity: rt.Path}, nil
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

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
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/adapter/bridge"
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

func (h *Harness) SupportsCompact() bool { return true }

// ValidateReadiness verifies that the claude-agent bridge can run with the configured runtime prerequisites.
func ValidateReadiness(cfg config.ClaudeCodeConfig) error {
	_, _, err := resolveReadyBridgeRuntime(cfg)
	return err
}

func resolveReadyBridgeRuntime(cfg config.ClaudeCodeConfig) (bridge.BridgeRuntime, string, error) {
	rt, err := bridge.ResolveBridgeRuntime(cfg.BridgePath, "claude-agent-bridge", "claude-agent bridge")
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
	if err := bridge.EnsureBridgeDependencies(rt, "@anthropic-ai/claude-agent-sdk", "claude-agent bridge"); err != nil {
		return bridge.BridgeRuntime{}, "", err
	}
	return rt, bunPath, nil
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
	AnswerTimeoutMs int64   `json:"answer_timeout_ms"`
}

// StartSession spawns a new agent session with the given options.
func (h *Harness) StartSession(ctx context.Context, opts adapter.SessionOpts) (_ adapter.AgentSession, err error) {
	if opts.Mode == "" {
		opts.Mode = adapter.SessionModeAgent
	}

	// Determine working directory.
	workDir := opts.WorktreePath
	if workDir == "" {
		workDir = h.workspaceRoot
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

	cmd, sessionTmpDir, err := bridge.BuildSandboxCmd(ctx, bridgeRt, workDir, bunPath, bunCacheDir, ".claude")
	if err != nil {
		return nil, err
	}
	// Ensure the sandbox temp dir is cleaned up if StartSession fails after this point.
	defer func() {
		if err != nil {
			os.RemoveAll(sessionTmpDir) // best-effort cleanup
		}
	}()
	cmd.Dir = bridgeRt.LaunchDir(workDir)

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

	if err = cmd.Start(); err != nil {
		return nil, fmt.Errorf("start bridge: %w", err)
	}

	sessionLogPath := filepath.Join(sessionLogDir, opts.SessionID+".log")
	if err = os.MkdirAll(sessionLogDir, 0o750); err != nil {
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

	session := &claudeAgentSession{bs: bs}
	bs.ParseSessionMeta = session.sessionMetaCallback

	bs.StartReaders()

	initMsg := bridgeInitMsg{
		Type:            "init",
		Mode:            string(opts.Mode),
		SystemPrompt:    opts.SystemPrompt,
		ResumeSessionID: opts.ResumeInfo["claude_session_id"],
		PermissionMode:  h.cfg.PermissionMode,
		Model:           h.cfg.Model,
		MaxTurns:        h.cfg.MaxTurns,
		MaxBudgetUSD:    h.cfg.MaxBudgetUSD,
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

	// Send initial prompt for agent mode (foreman mode receives messages via SendMessage).
	if opts.Mode == adapter.SessionModeAgent && opts.UserPrompt != "" {
		if err := session.bs.SendPrompt(opts.UserPrompt); err != nil {
			session.Abort(ctx) //nolint:errcheck // best-effort cleanup
			return nil, fmt.Errorf("send initial prompt: %w", err)
		}
	}

	return session, nil
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

package acp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/adapter/bridge"
	"github.com/beeemT/substrate/internal/config"
)

// Verify Harness implements adapter.AgentHarness at compile time.
var _ adapter.AgentHarness = (*Harness)(nil)

// Verify Harness implements adapter.HarnessActionRunner at compile time.
var _ adapter.HarnessActionRunner = (*Harness)(nil)

type Harness struct {
	cfg           config.ACPConfig
	workspaceRoot string
	mu            sync.RWMutex
	lastInit      *initializeResponse
	compact       compactStrategy
}

func NewHarness(cfg config.ACPConfig, workspaceRoot string) *Harness {
	h := &Harness{cfg: cfg, workspaceRoot: workspaceRoot}
	h.compact = detectConfiguredCompactStrategy(cfg)
	return h
}

func (h *Harness) Name() string { return "acp" }

func (h *Harness) SupportsCompact() bool {
	h.mu.RLock()
	strategy := h.compact
	h.mu.RUnlock()
	return strategy.command != ""
}

func (h *Harness) Capabilities() adapter.HarnessCapabilities {
	return adapter.HarnessCapabilities{SupportsStreaming: true, SupportsMessaging: true, SupportsNativeResume: h.lastSupportsResume(), SupportedTools: []string{"read", "write", "bash", "ask_foreman", "acp_fs", "acp_terminal"}}
}

func (h *Harness) lastSupportsResume() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.lastInit != nil && h.lastInit.AgentCapabilities.SessionCapabilities.supportsResume()
}

func ValidateReadiness(cfg config.ACPConfig) error {
	if strings.TrimSpace(cfg.Command) == "" {
		return errors.New("adapters.acp.command is required")
	}
	if _, err := exec.LookPath(cfg.Command); err != nil {
		return fmt.Errorf("acp command %q not found: %w", cfg.Command, err)
	}
	for k := range cfg.Env {
		if strings.Contains(k, "=") || k == "" {
			return fmt.Errorf("invalid adapters.acp.env key %q", k)
		}
	}
	return nil
}

func (h *Harness) RunAction(ctx context.Context, req adapter.HarnessActionRequest) (adapter.HarnessActionResult, error) {
	switch req.Action {
	case "check_auth":
		init, err := h.initializeOnly(ctx)
		if err != nil {
			return adapter.HarnessActionResult{Success: false, Message: err.Error()}, nil
		}
		msg := "ACP agent is reachable"
		if len(init.AuthMethods) > 0 {
			msg += fmt.Sprintf("; auth methods: %s", authMethodIDs(init.AuthMethods))
		}
		return adapter.HarnessActionResult{Success: true, Message: msg, Metadata: map[string]string{"agent": init.AgentInfo.Name, "version": init.AgentInfo.Version}}, nil
	case "authenticate":
		method := req.Inputs["method_id"]
		if method == "" {
			return adapter.HarnessActionResult{Success: false, Message: "ACP authentication requires method_id"}, nil
		}
		if err := h.authenticate(ctx, method); err != nil {
			return adapter.HarnessActionResult{Success: false, Message: err.Error()}, nil
		}
		return adapter.HarnessActionResult{Success: true, Message: "ACP authentication completed"}, nil
	default:
		return adapter.HarnessActionResult{Success: false, Message: "unsupported ACP harness action: " + req.Action}, nil
	}
}

func (h *Harness) StartSession(ctx context.Context, opts adapter.SessionOpts) (adapter.AgentSession, error) {
	if opts.Mode == "" {
		opts.Mode = adapter.SessionModeAgent
	}
	root := opts.WorktreePath
	if root == "" {
		root = h.workspaceRoot
	}
	if root == "" {
		return nil, errors.New("acp session root is empty")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve acp session root: %w", err)
	}
	sessionLogDir := opts.SessionLogDir
	if sessionLogDir == "" {
		globalDir, err := config.GlobalDir()
		if err != nil {
			return nil, fmt.Errorf("get global dir: %w", err)
		}
		sessionLogDir = filepath.Join(globalDir, "sessions")
	}
	if err := os.MkdirAll(sessionLogDir, 0o750); err != nil {
		return nil, fmt.Errorf("create session log dir: %w", err)
	}
	logPath := filepath.Join(sessionLogDir, opts.SessionID+".log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open acp session log: %w", err)
	}
	cmd := exec.CommandContext(ctx, h.cfg.Command, h.cfg.Args...)
	cmd.Dir = absRoot
	cmd.Env = mergeEnv(os.Environ(), h.cfg.Env)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		logFile.Close()
		return nil, fmt.Errorf("create acp stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		logFile.Close()
		return nil, fmt.Errorf("create acp stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		logFile.Close()
		return nil, fmt.Errorf("create acp stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		logFile.Close()
		return nil, fmt.Errorf("start acp command: %w", err)
	}
	s := newSession(opts.SessionID, opts.Mode, absRoot, cmd, logFile, logPath, sessionLogDir, h.cfg)
	client := newRPCClient(stdin, stdout, stderr, s.writeProtocolLog)
	s.client = client
	s.questions = newQuestionBroker(opts.SessionID, opts.Mode, s.emit)
	s.terminals = newTerminalManager(absRoot)
	client.setHandlers(s.handleClientRequest, s.handleNotification)
	client.start()
	go s.reapProcess()
	initResp, err := h.initialize(ctx, client)
	if err != nil {
		s.Abort(ctx)
		return nil, err
	}
	s.init = initResp
	s.compactMu.Lock()
	s.compact = detectCompactStrategy(initResp, h.cfg, nil)
	s.compactMu.Unlock()
	h.mu.Lock()
	h.lastInit = &initResp
	h.compact = detectCompactStrategy(initResp, h.cfg, nil)
	h.mu.Unlock()
	mcpServers := s.buildMCPServers()
	setupResp, resumeMethod, err := s.setupACPSession(ctx, opts, mcpServers)
	if err != nil {
		s.Abort(ctx)
		return nil, err
	}
	if setupResp.SessionID != "" {
		s.acpSessionID = setupResp.SessionID
	} else if opts.ResumeInfo["acp_agent_session_id"] != "" {
		s.acpSessionID = opts.ResumeInfo["acp_agent_session_id"]
	}
	s.resumeMethod = resumeMethod
	s.configOptions = setupResp.ConfigOptions
	if err := s.applyConfiguredOptions(ctx, h.cfg, opts); err != nil {
		s.Abort(ctx)
		return nil, err
	}
	s.emit(adapter.AgentEvent{Type: "started", Timestamp: now(), Metadata: map[string]any{"acp_session_id": s.acpSessionID, "agent": initResp.AgentInfo.Name}})
	if opts.Mode == adapter.SessionModeAgent && opts.UserPrompt != "" {
		s.startPrompt(opts.UserPrompt)
	}
	return s, nil
}

func (h *Harness) initialize(ctx context.Context, client *rpcClient) (initializeResponse, error) {
	caps := clientCapabilities{}
	if boolPtrValue(h.cfg.ClientFS, true) {
		caps.FS = &fsClientCapabilities{ReadTextFile: true, WriteTextFile: true}
	}
	if boolPtrValue(h.cfg.ClientTerminal, true) {
		caps.Terminal = true
	}
	var resp initializeResponse
	req := initializeRequest{ProtocolVersion: protocolVersion, ClientCapabilities: caps, ClientInfo: implementationInfo{Name: "substrate", Title: "Substrate"}}
	if err := client.Call(ctx, "initialize", req, &resp); err != nil {
		return resp, fmt.Errorf("initialize acp agent: %w", err)
	}
	if resp.ProtocolVersion != protocolVersion {
		return resp, fmt.Errorf("unsupported acp protocol version %d", resp.ProtocolVersion)
	}
	return resp, nil
}

func (h *Harness) initializeOnly(ctx context.Context) (initializeResponse, error) {
	if err := ValidateReadiness(h.cfg); err != nil {
		return initializeResponse{}, err
	}
	cmd := exec.CommandContext(ctx, h.cfg.Command, h.cfg.Args...)
	cmd.Env = mergeEnv(os.Environ(), h.cfg.Env)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return initializeResponse{}, fmt.Errorf("create acp stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return initializeResponse{}, fmt.Errorf("create acp stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return initializeResponse{}, fmt.Errorf("create acp stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return initializeResponse{}, fmt.Errorf("start acp command: %w", err)
	}
	client := newRPCClient(stdin, stdout, stderr, nil)
	client.start()
	resp, err := h.initialize(ctx, client)
	_ = client.Notify(context.Background(), "session/cancel", sessionIDParams{})
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	_ = cmd.Wait()
	return resp, err
}

func (h *Harness) authenticate(ctx context.Context, method string) error {
	cmd := exec.CommandContext(ctx, h.cfg.Command, h.cfg.Args...)
	cmd.Env = mergeEnv(os.Environ(), h.cfg.Env)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("create acp stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("create acp stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("create acp stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start acp command: %w", err)
	}
	client := newRPCClient(stdin, stdout, stderr, nil)
	client.start()
	if _, err := h.initialize(ctx, client); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return err
	}
	err = client.Call(ctx, "authenticate", map[string]string{"methodId": method}, nil)
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	_ = cmd.Wait()
	return err
}

func boolPtrValue(v *bool, fallback bool) bool {
	if v == nil {
		return fallback
	}
	return *v
}

func mergeEnv(base []string, extra map[string]string) []string {
	out := append([]string{}, base...)
	for k, v := range extra {
		out = append(out, k+"="+v)
	}
	return out
}

func authMethodIDs(methods []authMethod) string {
	ids := make([]string, 0, len(methods))
	for _, m := range methods {
		ids = append(ids, m.ID)
	}
	return strings.Join(ids, ", ")
}

func resolveForemanMCPBridge() (string, []string) {
	execPath, err := os.Executable()
	if err != nil {
		return "", nil
	}
	for _, c := range bridge.BridgeCandidates("", execPath, "opencode-foreman-mcp") {
		if c == "" {
			continue
		}
		if st, err := os.Stat(c); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return c, nil
		}
	}
	rootCandidates := []string{
		filepath.Join(filepath.Dir(execPath), "bridge", "opencode-foreman-mcp", "index.ts"),
		filepath.Join("bridge", "opencode-foreman-mcp", "index.ts"),
	}
	bun, err := exec.LookPath("bun")
	if err != nil {
		return "", nil
	}
	for _, c := range rootCandidates {
		if _, err := os.Stat(c); err == nil {
			return bun, []string{c}
		}
	}
	return "", nil
}

func (s *Session) buildMCPServers() []mcpServer {
	if s.mode == adapter.SessionModeForeman {
		return nil
	}
	cmd, args := resolveForemanMCPBridge()
	if cmd == "" {
		slog.Warn("acp: foreman MCP bridge not found; ask_foreman unavailable")
		return nil
	}
	fs, err := startForemanSocket(s.questions)
	if err != nil {
		slog.Warn("acp: failed to start foreman MCP socket", "error", err)
		return nil
	}
	s.foremanSocket = fs
	return []mcpServer{{Name: "substrate-foreman", Command: cmd, Args: args, Env: []envVar{fs.env()}}}
}

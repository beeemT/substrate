package claudecode

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/config"
)

// Harness implements adapter.AgentHarness for Claude Code.
type Harness struct {
	cfg config.ClaudeCodeConfig
}

func NewHarness(cfg config.ClaudeCodeConfig) *Harness {
	return &Harness{cfg: cfg}
}

func (h *Harness) Name() string { return "claude-code" }

func (h *Harness) Capabilities() adapter.HarnessCapabilities {
	return adapter.HarnessCapabilities{
		SupportsStreaming: true,
		SupportsMessaging: false,
		SupportedTools:    []string{"Bash", "Edit", "Read", "Glob", "Grep"},
	}
}

func (h *Harness) StartSession(ctx context.Context, opts adapter.SessionOpts) (adapter.AgentSession, error) {
	if opts.Mode == "" {
		opts.Mode = adapter.SessionModeAgent
	}
	if opts.WorktreePath == "" {
		return nil, fmt.Errorf("claude-code requires worktree path")
	}
	binary := h.cfg.BinaryPath
	if binary == "" {
		binary = "claude"
	}
	args := h.buildArgs(opts)
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = opts.WorktreePath

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("create stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("create stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start claude code: %w", err)
	}

	session := &session{
		id:        opts.SessionID,
		cmd:       cmd,
		events:    make(chan adapter.AgentEvent, 256),
		stdout:    stdout,
		stderr:    stderr,
		logPath:   sessionLogPath(opts),
		completed: make(chan error, 1),
	}
	if err := session.openLogFile(); err != nil {
		_ = cmd.Process.Kill()
		_, _ = io.Copy(io.Discard, stdout)
		_, _ = io.Copy(io.Discard, stderr)
		return nil, err
	}
	go session.waitProcess()
	go session.readStdout()
	go session.readStderr()
	return session, nil
}

func (h *Harness) buildArgs(opts adapter.SessionOpts) []string {
	prompt := opts.UserPrompt
	if strings.TrimSpace(opts.SystemPrompt) != "" {
		prompt = opts.SystemPrompt + "\n\n" + prompt
	}
	args := []string{"-p", "--output-format", "stream-json"}
	if h.cfg.Model != "" {
		args = append(args, "--model", h.cfg.Model)
	}
	if h.cfg.PermissionMode != "" {
		args = append(args, "--permission-mode", h.cfg.PermissionMode)
	}
	if h.cfg.MaxTurns > 0 {
		args = append(args, "--max-turns", fmt.Sprintf("%d", h.cfg.MaxTurns))
	}
	if h.cfg.MaxBudgetUSD > 0 {
		args = append(args, "--max-budget-usd", fmt.Sprintf("%.2f", h.cfg.MaxBudgetUSD))
	}
	if opts.Mode == adapter.SessionModeForeman {
		args = append(args, "--tools", "Read,Grep,Glob")
	}
	if strings.TrimSpace(prompt) != "" {
		args = append(args, prompt)
	}
	return args
}

func (h *Harness) RunAction(ctx context.Context, req adapter.HarnessActionRequest) (adapter.HarnessActionResult, error) {
	switch req.Action {
	case "check_auth":
		binary := h.cfg.BinaryPath
		if binary == "" {
			binary = "claude"
		}
		if _, err := exec.LookPath(binary); err != nil {
			return adapter.HarnessActionResult{}, err
		}
		return adapter.HarnessActionResult{Success: true, Message: "claude binary available", Identity: binary}, nil
	case "login_provider":
		if req.Provider != "github" {
			return adapter.HarnessActionResult{}, fmt.Errorf("unsupported provider %q", req.Provider)
		}
		out, err := exec.CommandContext(ctx, "gh", "auth", "token").CombinedOutput()
		if err != nil {
			return adapter.HarnessActionResult{}, fmt.Errorf("gh auth token: %w: %s", err, strings.TrimSpace(string(out)))
		}
		token := strings.TrimSpace(string(out))
		if token == "" {
			return adapter.HarnessActionResult{}, fmt.Errorf("gh auth token returned empty output")
		}
		return adapter.HarnessActionResult{Success: true, Message: "github login succeeded", Credentials: map[string]string{"token": token}, NeedsConfirm: true}, nil
	default:
		return adapter.HarnessActionResult{}, fmt.Errorf("unsupported claude-code action %q", req.Action)
	}
}

type session struct {
	id        string
	cmd       *exec.Cmd
	stdout    io.Reader
	stderr    io.Reader
	events    chan adapter.AgentEvent
	logPath   string
	logFile   *os.File
	mu        sync.Mutex
	aborted   bool
	closeOnce sync.Once
	completed chan error
}

func (s *session) ID() string                        { return s.id }
func (s *session) Events() <-chan adapter.AgentEvent { return s.events }
func (s *session) SendMessage(ctx context.Context, msg string) error {
	return fmt.Errorf("claude-code harness does not support SendMessage")
}

func (s *session) Wait(ctx context.Context) error {
	select {
	case <-ctx.Done():
		_ = s.Abort(ctx)
		return ctx.Err()
	case err := <-s.completed:
		return err
	}
}

func (s *session) Abort(ctx context.Context) error {
	s.mu.Lock()
	if s.aborted {
		s.mu.Unlock()
		return nil
	}
	s.aborted = true
	s.mu.Unlock()
	if s.cmd.Process == nil {
		return nil
	}
	if err := s.cmd.Process.Signal(os.Interrupt); err != nil {
		slog.Debug("claude-code interrupt failed", "err", err)
	}
	select {
	case err := <-s.completed:
		return err
	case <-time.After(5 * time.Second):
		_ = s.cmd.Process.Kill()
		return nil
	case <-ctx.Done():
		_ = s.cmd.Process.Kill()
		return ctx.Err()
	}
}

func (s *session) waitProcess() {
	err := s.cmd.Wait()
	s.mu.Lock()
	aborted := s.aborted
	if s.logFile != nil {
		_ = s.logFile.Close()
		s.logFile = nil
	}
	s.mu.Unlock()
	s.closeOnce.Do(func() { close(s.events) })
	if aborted {
		s.completed <- nil
		return
	}
	if err != nil {
		s.completed <- fmt.Errorf("claude code exited: %w", err)
		return
	}
	s.completed <- nil
}

func (s *session) openLogFile() error {
	if s.logPath == "" {
		return nil
	}
	if err := os.MkdirAll(filepathDir(s.logPath), 0o755); err != nil {
		return fmt.Errorf("create session log dir: %w", err)
	}
	f, err := os.OpenFile(s.logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open session log: %w", err)
	}
	s.logFile = f
	return nil
}

func (s *session) readStdout() {
	scanner := bufio.NewScanner(s.stdout)
	scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		s.writeLogLine(line)
		evt, ok := mapClaudeEvent(line)
		if !ok {
			continue
		}
		select {
		case s.events <- evt:
		default:
			slog.Warn("claude-code event channel full", "type", evt.Type)
		}
	}
	if err := scanner.Err(); err != nil {
		slog.Warn("reading claude-code stdout", "err", err)
	}
}

func (s *session) readStderr() {
	scanner := bufio.NewScanner(s.stderr)
	for scanner.Scan() {
		slog.Debug("claude-code stderr", "line", scanner.Text())
	}
}

func (s *session) writeLogLine(line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.logFile == nil {
		return
	}
	_, _ = s.logFile.WriteString(line + "\n")
}

func mapClaudeEvent(line string) (adapter.AgentEvent, bool) {
	var raw map[string]any
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return adapter.AgentEvent{}, false
	}
	typeName, _ := raw["type"].(string)
	switch typeName {
	case "assistant":
		if text := extractClaudeText(raw); text != "" {
			return adapter.AgentEvent{Type: "text_delta", Timestamp: time.Now(), Payload: text}, true
		}
	case "result":
		summary := extractClaudeText(raw)
		if summary == "" {
			summary, _ = raw["result"].(string)
		}
		return adapter.AgentEvent{Type: "done", Timestamp: time.Now(), Payload: summary}, true
	case "error":
		msg, _ := raw["message"].(string)
		if msg == "" {
			msg = line
		}
		return adapter.AgentEvent{Type: "error", Timestamp: time.Now(), Payload: msg}, true
	}
	return adapter.AgentEvent{}, false
}

func extractClaudeText(raw map[string]any) string {
	if text, ok := raw["text"].(string); ok && text != "" {
		return text
	}
	if content, ok := raw["content"].([]any); ok {
		var parts []string
		for _, item := range content {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if text, ok := m["text"].(string); ok && text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

func sessionLogPath(opts adapter.SessionOpts) string {
	if opts.SessionLogDir == "" {
		return ""
	}
	return filepathJoin(opts.SessionLogDir, opts.SessionID+".log")
}

func filepathJoin(elem ...string) string {
	return strings.Join(elem, string(os.PathSeparator))
}

func filepathDir(path string) string {
	idx := strings.LastIndex(path, string(os.PathSeparator))
	if idx <= 0 {
		return "."
	}
	return path[:idx]
}

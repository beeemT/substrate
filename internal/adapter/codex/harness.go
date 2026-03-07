package codex

import (
	"bufio"
	"context"
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

// Harness implements adapter.AgentHarness for Codex CLI.
type Harness struct {
	cfg config.CodexConfig
}

func NewHarness(cfg config.CodexConfig) *Harness {
	return &Harness{cfg: cfg}
}

func (h *Harness) Name() string { return "codex" }

func (h *Harness) Capabilities() adapter.HarnessCapabilities {
	return adapter.HarnessCapabilities{
		SupportsStreaming: true,
		SupportsMessaging: false,
		SupportedTools:    []string{"sandboxed-cli"},
	}
}

func (h *Harness) StartSession(ctx context.Context, opts adapter.SessionOpts) (adapter.AgentSession, error) {
	if opts.Mode == "" {
		opts.Mode = adapter.SessionModeAgent
	}
	if opts.WorktreePath == "" {
		return nil, fmt.Errorf("codex requires worktree path")
	}
	binary := h.cfg.BinaryPath
	if binary == "" {
		binary = "codex"
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
		return nil, fmt.Errorf("start codex: %w", err)
	}

	session := &session{
		id:        opts.SessionID,
		cmd:       cmd,
		stdout:    stdout,
		stderr:    stderr,
		events:    make(chan adapter.AgentEvent, 256),
		logPath:   sessionLogPath(opts),
		completed: make(chan error, 1),
	}
	if err := session.openLogFile(); err != nil {
		_ = cmd.Process.Kill()
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
	args := []string{"-w", opts.WorktreePath}
	if h.cfg.Model != "" {
		args = append(args, "-m", h.cfg.Model)
	}
	if h.cfg.ApprovalMode != "" {
		args = append(args, "--approval-mode", h.cfg.ApprovalMode)
	}
	if h.cfg.FullAuto {
		args = append(args, "--full-auto")
	}
	if h.cfg.Quiet {
		args = append(args, "-q")
	}
	if strings.TrimSpace(prompt) != "" {
		args = append(args, prompt)
	}
	return args
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
	return fmt.Errorf("codex harness does not support SendMessage")
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
		slog.Debug("codex interrupt failed", "err", err)
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
		s.completed <- fmt.Errorf("codex exited: %w", err)
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
		evt := adapter.AgentEvent{Type: "text_delta", Timestamp: time.Now(), Payload: line}
		select {
		case s.events <- evt:
		default:
			slog.Warn("codex event channel full", "type", evt.Type)
		}
	}
	if err := scanner.Err(); err != nil {
		slog.Warn("reading codex stdout", "err", err)
	}
}

func (s *session) readStderr() {
	scanner := bufio.NewScanner(s.stderr)
	for scanner.Scan() {
		line := scanner.Text()
		s.writeLogLine(line)
		slog.Debug("codex stderr", "line", line)
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

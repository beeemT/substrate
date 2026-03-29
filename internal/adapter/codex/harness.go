package codex

import (
	"bufio"
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
	"strings"
	"sync"
	"time"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/config"
)

// jsonFlag is the canonical --json flag name for `codex exec`.
// --experimental-json is an alias; we use the canonical form.
const (
	jsonFlag    = "--json"
	codexBinary = "codex"
)

// Harness implements adapter.AgentHarness for Codex CLI.
type Harness struct {
	cfg config.CodexConfig
}

func NewHarness(cfg config.CodexConfig) *Harness {
	return &Harness{cfg: cfg}
}

func (h *Harness) Name() string { return codexBinary }

func (h *Harness) Capabilities() adapter.HarnessCapabilities {
	return adapter.HarnessCapabilities{
		SupportsStreaming:    true,
		SupportsMessaging:    true,
		SupportsNativeResume: true,
		SupportedTools:       []string{"sandboxed-cli"},
	}
}

func (h *Harness) SupportsCompact() bool { return false }

func (h *Harness) StartSession(ctx context.Context, opts adapter.SessionOpts) (adapter.AgentSession, error) {
	if opts.Mode == "" {
		opts.Mode = adapter.SessionModeAgent
	}
	if opts.WorktreePath == "" {
		return nil, errors.New("codex requires worktree path")
	}
	binary := h.cfg.BinaryPath
	if binary == "" {
		binary = codexBinary
	}

	prompt := buildPrompt(opts.SystemPrompt, opts.UserPrompt)
	args := buildArgs(opts, h.cfg)
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = opts.WorktreePath

	stdoutR, stderrR, err := startWithInput(cmd, prompt)
	if err != nil {
		return nil, fmt.Errorf("start codex: %w", err)
	}

	s := &session{
		id:            opts.SessionID,
		binary:        binary,
		harnessConfig: h.cfg,
		cmd:           cmd,
		events:        make(chan adapter.AgentEvent, 256),
		logPath:       sessionLogPath(opts),
		completed:     make(chan error, 1),
		state:         sessionRunning,
		lastText:      make(map[string]string),
	}
	if err := s.openLogFile(); err != nil {
		if killErr := cmd.Process.Kill(); killErr != nil {
			slog.Warn("failed to kill codex process after log open error", "error", killErr)
		}
		return nil, err
	}
	s.launchProcess(cmd, stdoutR, stderrR)

	return s, nil
}

// buildPrompt combines system and user prompts with a blank-line separator when
// both are non-empty.
func buildPrompt(system, user string) string {
	sys := strings.TrimSpace(system)
	usr := strings.TrimSpace(user)
	switch {
	case sys != "" && usr != "":
		return sys + "\n\n" + usr
	case sys != "":
		return sys
	default:
		return usr
	}
}

// buildArgs constructs the argument list for `codex exec --json ...`.
// The prompt is NOT included here; it is delivered via stdin in StartSession.
//
// Approval-mode mapping (verified against codex-rs/protocol/src/config_types.rs):
//
//	FullAuto=true || ApprovalMode="full-auto" → --full-auto
//	ApprovalMode="auto-edit"                 → --sandbox workspace-write
//	ApprovalMode="suggest" or empty          → (no sandbox flag; read-only is default)
//
// Quiet is silently ignored — there is no equivalent in JSON mode.
func buildArgs(opts adapter.SessionOpts, cfg config.CodexConfig) []string {
	args := []string{"exec", jsonFlag}

	if opts.WorktreePath != "" {
		args = append(args, "--cd", opts.WorktreePath)
	}
	if cfg.Model != "" {
		args = append(args, "-m", cfg.Model)
	}

	switch {
	case cfg.FullAuto || cfg.ApprovalMode == "full-auto":
		args = append(args, "--full-auto")
	case cfg.ApprovalMode == "auto-edit":
		args = append(args, "--sandbox", "workspace-write")
	default:
		// "suggest" and empty both use the default read-only sandbox; no flag needed.
	}

	if cfg.Quiet {
		slog.Debug("codex: Quiet flag is ignored in JSON mode")
	}

	// Resume an existing thread when a thread ID is provided.
	if threadID := opts.ResumeInfo["codex_thread_id"]; threadID != "" {
		args = append(args, "resume", threadID)
	}

	return args
}

func (h *Harness) RunAction(ctx context.Context, req adapter.HarnessActionRequest) (adapter.HarnessActionResult, error) {
	switch req.Action {
	case "check_auth":
		binary := h.cfg.BinaryPath
		if binary == "" {
			binary = codexBinary
		}
		if _, err := exec.LookPath(binary); err != nil {
			return adapter.HarnessActionResult{}, err
		}

		// Verify the `exec` subcommand is available. Old codex builds predate it.
		probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		out, err := exec.CommandContext(probeCtx, binary, "exec", "--help").CombinedOutput()
		if err != nil {
			return adapter.HarnessActionResult{}, fmt.Errorf(
				"codex binary found but 'exec' subcommand unavailable (upgrade codex): %w: %s",
				err, strings.TrimSpace(string(out)),
			)
		}

		return adapter.HarnessActionResult{Success: true, Message: "codex binary available", Identity: binary}, nil
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
		return adapter.HarnessActionResult{}, fmt.Errorf("unsupported codex action %q", req.Action)
	}
}

// sessionState tracks the lifecycle phase of a codex session.
// codex exec is single-turn; we manage multi-turn externally via thread resume.
type sessionState int

const (
	// sessionRunning: a codex exec process is active.
	sessionRunning sessionState = iota
	// sessionTurnDone: the process exited cleanly after turn.completed.
	// SendMessage may start a new resume process.
	sessionTurnDone
	// sessionAborted: the session was terminated via Abort or a fatal error.
	sessionAborted
)

type session struct {
	id            string
	binary        string
	harnessConfig config.CodexConfig

	mu             sync.Mutex
	state          sessionState
	threadID       string            // set on thread.started; required for resume
	lastText       map[string]string // item_id → text already streamed (for delta tracking)
	cmd            *exec.Cmd         // current running process (guarded by mu)
	logFile        *os.File          // guarded by mu
	closeOnce      sync.Once
	pendingTurnErr error

	events    chan adapter.AgentEvent
	logPath   string
	completed chan error // receives exactly once: when the session is aborted or fails fatally
}

func (s *session) ID() string                        { return s.id }
func (s *session) Events() <-chan adapter.AgentEvent { return s.events }

func (s *session) Steer(_ context.Context, _ string) error {
	return adapter.ErrSteerNotSupported
}

func (s *session) SendAnswer(_ context.Context, _ string) error {
	return adapter.ErrSendAnswerNotSupported
}

// Wait blocks until the session is aborted or a fatal error occurs.
//
// Note: unlike single-turn harnesses, Wait does NOT return when a turn
// completes — it returns only when the session is explicitly Abort()ed or
// hits an unrecoverable error. Callers that need to react to turn completion
// should listen for the "done" event on Events().
func (s *session) Compact(_ context.Context) error {
	return adapter.ErrCompactNotSupported
}

func (s *session) Wait(ctx context.Context) error {
	select {
	case <-ctx.Done():
		if abortErr := s.Abort(ctx); abortErr != nil {
			slog.Warn("failed to abort codex session on context cancel", "error", abortErr)
		}

		return ctx.Err()
	case err := <-s.completed:
		return err
	}
}

// SendMessage starts a new `codex exec resume <threadID>` process and delivers
// msg as stdin. It returns an error if the session is still mid-turn or has
// been aborted.
func (s *session) SendMessage(ctx context.Context, msg string) error {
	s.mu.Lock()
	state := s.state
	threadID := s.threadID
	s.mu.Unlock()

	switch state {
	case sessionRunning:
		return errors.New("codex session: cannot send message while a turn is in progress")
	case sessionAborted:
		return errors.New("codex session: session has been aborted")
	}
	// state == sessionTurnDone
	if threadID == "" {
		return errors.New("codex session: no thread ID received; cannot resume")
	}

	// Build resume invocation.
	workDir := s.getWorkDir()
	resumeOpts := adapter.SessionOpts{
		WorktreePath: workDir,
		ResumeInfo:   map[string]string{"codex_thread_id": threadID},
	}
	args := buildArgs(resumeOpts, s.harnessConfig)
	cmd := exec.CommandContext(ctx, s.binary, args...)
	cmd.Dir = workDir

	stdoutR, stderrR, err := startWithInput(cmd, msg)
	if err != nil {
		return fmt.Errorf("resume codex: %w", err)
	}

	s.mu.Lock()
	s.cmd = cmd
	s.state = sessionRunning
	s.mu.Unlock()

	if err := s.openLogFile(); err != nil {
		if killErr := cmd.Process.Kill(); killErr != nil {
			slog.Warn("failed to kill codex process after log open error", "error", killErr)
		}
		return err
	}
	s.launchProcess(cmd, stdoutR, stderrR)

	return nil
}

// getWorkDir returns the working directory for resume processes.
// It reads cmd.Dir under mu.
func (s *session) getWorkDir() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmd != nil {
		return s.cmd.Dir
	}
	return ""
}

func (s *session) Abort(ctx context.Context) error {
	s.mu.Lock()
	if s.state == sessionAborted {
		s.mu.Unlock()

		return nil
	}
	s.state = sessionAborted
	cmd := s.cmd
	s.mu.Unlock()

	if cmd == nil || cmd.Process == nil {
		s.terminateSession(nil)

		return nil
	}
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		slog.Debug("codex interrupt failed", "err", err)
		// Signal failed — the process is already gone. Terminate directly;
		// terminateSession is idempotent via closeOnce so calling it here
		// is safe even if waitProcess calls it concurrently. Avoid reading
		// cmd.ProcessState, which races with the cmd.Wait() in waitProcess.
		s.terminateSession(nil)
		return nil
	}
	select {
	case <-s.completed:
		return nil
	case <-time.After(5 * time.Second):
		if killErr := cmd.Process.Kill(); killErr != nil {
			slog.Warn("failed to kill codex process on abort timeout", "error", killErr)
		}

		return nil
	case <-ctx.Done():
		if killErr := cmd.Process.Kill(); killErr != nil {
			slog.Warn("failed to kill codex process on context cancel", "error", killErr)
		}

		return ctx.Err()
	}
}

func (s *session) ResumeInfo() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.threadID == "" {
		return nil
	}
	return map[string]string{
		"codex_thread_id": s.threadID,
	}
}

// waitProcess waits for the given cmd to exit and transitions session state.
// Each turn spawns its own goroutine running waitProcess(cmd).
func (s *session) waitProcess(cmd *exec.Cmd, ioWg *sync.WaitGroup) {
	ioWg.Wait() // drain all stdout/stderr before reaping the process
	err := cmd.Wait()

	s.mu.Lock()
	if s.cmd != cmd {
		// A newer turn has already started and owns s.cmd.
		// Do not interfere with the new turn's state or close the session.
		s.mu.Unlock()
		return
	}
	currentState := s.state
	pendingErr := s.pendingTurnErr
	s.pendingTurnErr = nil
	s.mu.Unlock()

	switch currentState {
	case sessionAborted:
		// Abort called: close the events channel and deliver nil on completed.
		s.terminateSession(nil)
	case sessionRunning:
		if err != nil {
			// Process exited non-zero without us aborting: use the pending turn
			// error if available (preserves the original turn.failed message);
			// otherwise fall back to the raw process exit error.
			if pendingErr != nil {
				s.terminateSession(pendingErr)
			} else {
				s.terminateSession(fmt.Errorf("codex exited: %w", err))
			}
		} else {
			// Clean exit without turn.completed already transitioning state means
			// the process exited before we saw that event — treat as turn done.
			// (The JSONL parser sets sessionTurnDone on turn.completed; if it got
			// there first, this branch becomes a no-op on the already-done state.)
			s.mu.Lock()
			if s.state == sessionRunning {
				s.state = sessionTurnDone
			}
			s.mu.Unlock()
		}
	case sessionTurnDone:
		// Normal: process exited after turn.completed was already processed.
		// State is already sessionTurnDone; nothing to do.
	}
}

// terminateSession closes the events channel and sends err on completed exactly once.
func (s *session) terminateSession(err error) {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		if s.logFile != nil {
			if closeErr := s.logFile.Close(); closeErr != nil {
				slog.Warn("failed to close session log file", "error", closeErr)
			}
			s.logFile = nil
		}
		s.mu.Unlock()
		close(s.events)
		s.completed <- err
	})
}

// startWithInput wires up stdin/stdout/stderr pipes, starts cmd, writes input to
// stdin, then closes stdin so the child sees EOF. On success cmd is running and
// stdoutR/stderrR are open for reading. On any error all resources are cleaned up
// and cmd is killed if it was started.
func startWithInput(cmd *exec.Cmd, input string) (stdoutR, stderrR *os.File, err error) {
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		return nil, nil, fmt.Errorf("create stdin pipe: %w", err)
	}
	var stdoutRead, stdoutWrite *os.File
	stdoutRead, stdoutWrite, err = os.Pipe()
	if err != nil {
		if err := stdinR.Close(); err != nil {
			slog.Warn("failed to close stdin read end during cleanup", "error", err)
		}
		if err := stdinW.Close(); err != nil {
			slog.Warn("failed to close stdin write end during cleanup", "error", err)
		}
		return nil, nil, fmt.Errorf("create stdout pipe: %w", err)
	}
	var stderrRead, stderrWrite *os.File
	stderrRead, stderrWrite, err = os.Pipe()
	if err != nil {
		if err := stdinR.Close(); err != nil {
			slog.Warn("failed to close stdin read end during cleanup", "error", err)
		}
		if err := stdinW.Close(); err != nil {
			slog.Warn("failed to close stdin write end during cleanup", "error", err)
		}
		if err := stdoutRead.Close(); err != nil {
			slog.Warn("failed to close stdout read end during cleanup", "error", err)
		}
		if err := stdoutWrite.Close(); err != nil {
			slog.Warn("failed to close stdout write end during cleanup", "error", err)
		}
		return nil, nil, fmt.Errorf("create stderr pipe: %w", err)
	}
	cmd.Stdin = stdinR
	cmd.Stdout = stdoutWrite
	cmd.Stderr = stderrWrite
	if err = cmd.Start(); err != nil {
		if err := stdinR.Close(); err != nil {
			slog.Warn("failed to close stdin read end during cleanup", "error", err)
		}
		if err := stdinW.Close(); err != nil {
			slog.Warn("failed to close stdin write end during cleanup", "error", err)
		}
		if err := stdoutRead.Close(); err != nil {
			slog.Warn("failed to close stdout read end during cleanup", "error", err)
		}
		if err := stdoutWrite.Close(); err != nil {
			slog.Warn("failed to close stdout write end during cleanup", "error", err)
		}
		if err := stderrRead.Close(); err != nil {
			slog.Warn("failed to close stderr read end during cleanup", "error", err)
		}
		if err := stderrWrite.Close(); err != nil {
			slog.Warn("failed to close stderr write end during cleanup", "error", err)
		}
		return nil, nil, fmt.Errorf("start process: %w", err)
	}
	// Close the parent's copies of the child-facing ends so the read ends
	// see EOF exactly when the child exits.
	if closeErr := stdinR.Close(); closeErr != nil {
		slog.Warn("failed to close stdin read end after process start", "error", closeErr)
	}
	if closeErr := stdoutWrite.Close(); closeErr != nil {
		slog.Warn("failed to close stdout write end after process start", "error", closeErr)
	}
	if closeErr := stderrWrite.Close(); closeErr != nil {
		slog.Warn("failed to close stderr write end after process start", "error", closeErr)
	}
	if _, err = io.WriteString(stdinW, input); err != nil {
		if err := cmd.Process.Kill(); err != nil {
			slog.Warn("failed to kill process during cleanup", "error", err)
		}
		if err := stdoutRead.Close(); err != nil {
			slog.Warn("failed to close stdout read end during cleanup", "error", err)
		}
		if err := stderrRead.Close(); err != nil {
			slog.Warn("failed to close stderr read end during cleanup", "error", err)
		}
		return nil, nil, fmt.Errorf("write stdin: %w", err)
	}
	if err = stdinW.Close(); err != nil {
		if err := cmd.Process.Kill(); err != nil {
			slog.Warn("failed to kill process during cleanup", "error", err)
		}
		if err := stdoutRead.Close(); err != nil {
			slog.Warn("failed to close stdout read end during cleanup", "error", err)
		}
		if err := stderrRead.Close(); err != nil {
			slog.Warn("failed to close stderr read end during cleanup", "error", err)
		}
		return nil, nil, fmt.Errorf("close stdin: %w", err)
	}
	return stdoutRead, stderrRead, nil
}

// launchProcess starts the stdout/stderr reader goroutines and the process
// reaper for cmd. Call after cmd has been started via startWithInput.
func (s *session) launchProcess(cmd *exec.Cmd, stdoutR, stderrR *os.File) {
	var ioWg sync.WaitGroup
	ioWg.Add(2)
	go func() { defer ioWg.Done(); s.readStdout(stdoutR) }()
	go func() { defer ioWg.Done(); s.readStderr(stderrR) }()
	go s.waitProcess(cmd, &ioWg)
}

// ---------------------------------------------------------------------------
// JSONL event parser
// ---------------------------------------------------------------------------

// The structs below mirror the codex-rs exec event schema
// (codex-rs/exec/src/exec_events.rs). They are unexported — callers interact
// only via adapter.AgentEvent.

type codexEvent struct {
	Type string `json:"type"`
	// thread.started
	ThreadID string `json:"thread_id,omitempty"`
	// turn.completed
	Usage *codexUsage `json:"usage,omitempty"`
	// turn.failed / error
	Message string `json:"message,omitempty"`
	Error   *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
	// item.started / item.updated / item.completed
	Item *codexItem `json:"item,omitempty"`
}

type codexUsage struct {
	InputTokens       int64 `json:"input_tokens"`
	CachedInputTokens int64 `json:"cached_input_tokens"`
	OutputTokens      int64 `json:"output_tokens"`
}

type codexItem struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	// agent_message / reasoning
	Text string `json:"text,omitempty"`
	// command_execution
	Command          string `json:"command,omitempty"`
	AggregatedOutput string `json:"aggregated_output,omitempty"`
	ExitCode         *int   `json:"exit_code,omitempty"`
	Status           string `json:"status,omitempty"`
	// file_change
	Changes []codexFileChange `json:"changes,omitempty"`
	// mcp_tool_call
	Server string `json:"server,omitempty"`
	Tool   string `json:"tool,omitempty"`
	// mcp error field (reuses top-level Error struct shape)
	McpError *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
	// web_search
	Query string `json:"query,omitempty"`
	// error item
	// (text field already covers message for error items via AgentMessageItem pattern;
	//  ErrorItem has a "message" field — we capture it separately)
	ItemMessage string `json:"message,omitempty"`
}

type codexFileChange struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
}

func (s *session) readStdout(r io.ReadCloser) {
	defer r.Close()
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		s.writeLogLine(line)
		if evt, ok := s.mapCodexEvent(line); ok {
			select {
			case s.events <- evt:
			default:
				slog.Warn("codex event channel full", "type", evt.Type) //nolint:gosec // G706: evt.Type is a string constant from our event schema, not user input
			}
		}
	}
	if err := scanner.Err(); err != nil {
		slog.Warn("reading codex stdout", "err", err)
	}
}

// mapCodexEvent parses one JSONL line and maps it to an adapter.AgentEvent.
// Returns (event, true) when an event should be emitted, (zero, false) otherwise.
// Unknown event types are skipped — forward-compatibility for new codex versions.
func (s *session) mapCodexEvent(line string) (adapter.AgentEvent, bool) {
	var raw codexEvent
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		slog.Debug("codex: unparseable JSONL line", "line", line, "err", err)

		return adapter.AgentEvent{}, false
	}

	now := time.Now()

	switch raw.Type {
	case "thread.started":
		s.mu.Lock()
		s.threadID = raw.ThreadID
		s.mu.Unlock()

		return adapter.AgentEvent{
			Type:      "started",
			Timestamp: now,
			Metadata:  map[string]any{"codex_thread_id": raw.ThreadID},
		}, true

	case "turn.started":
		return adapter.AgentEvent{}, false // no-op

	case "item.started":
		if raw.Item != nil {
			s.mu.Lock()
			s.lastText[raw.Item.ID] = ""
			s.mu.Unlock()
		}

		return adapter.AgentEvent{}, false

	case "item.updated":
		if raw.Item == nil || raw.Item.Type != "agent_message" {
			return adapter.AgentEvent{}, false
		}
		return s.streamDelta(raw.Item, now)

	case "item.completed":
		if raw.Item == nil {
			return adapter.AgentEvent{}, false
		}
		return s.mapItemCompleted(raw.Item, now)

	case "turn.completed":
		// Mark turn as done so SendMessage can start a resume.
		s.mu.Lock()
		if s.state == sessionRunning {
			s.state = sessionTurnDone
		}
		s.mu.Unlock()

		meta := map[string]any{}
		if raw.Usage != nil {
			meta["input_tokens"] = raw.Usage.InputTokens
			meta["cached_input_tokens"] = raw.Usage.CachedInputTokens
			meta["output_tokens"] = raw.Usage.OutputTokens
		}

		return adapter.AgentEvent{Type: "done", Timestamp: now, Metadata: meta}, true

	case "turn.failed":
		msg := ""
		if raw.Error != nil {
			msg = raw.Error.Message
		}

		s.mu.Lock()
		if s.state == sessionRunning {
			s.pendingTurnErr = fmt.Errorf("turn failed: %s", msg)
		}
		s.mu.Unlock()

		return adapter.AgentEvent{Type: "error", Timestamp: now, Payload: msg}, true

	case "error":
		return adapter.AgentEvent{Type: "error", Timestamp: now, Payload: raw.Message}, true

	default:
		slog.Debug("codex: unknown event type", "type", raw.Type)

		return adapter.AgentEvent{}, false
	}
}

// streamDelta emits an incremental text_delta for an agent_message item by
// computing the new suffix since the last observed text.
func (s *session) streamDelta(item *codexItem, now time.Time) (adapter.AgentEvent, bool) {
	s.mu.Lock()
	prev := s.lastText[item.ID]
	delta := ""
	if len(item.Text) > len(prev) {
		delta = item.Text[len(prev):]
		s.lastText[item.ID] = item.Text
	}
	s.mu.Unlock()

	if delta == "" {
		return adapter.AgentEvent{}, false
	}

	return adapter.AgentEvent{
		Type:      "text_delta",
		Timestamp: now,
		Payload:   delta,
		Metadata:  map[string]any{"item_id": item.ID},
	}, true
}

// mapItemCompleted translates a completed item into one adapter.AgentEvent.
func (s *session) mapItemCompleted(item *codexItem, now time.Time) (adapter.AgentEvent, bool) {
	switch item.Type {
	case "agent_message":
		// Flush any text not yet streamed.
		s.mu.Lock()
		prev := s.lastText[item.ID]
		remainder := ""
		if len(item.Text) > len(prev) {
			remainder = item.Text[len(prev):]
		}
		delete(s.lastText, item.ID)
		s.mu.Unlock()

		if remainder == "" {
			return adapter.AgentEvent{}, false
		}

		return adapter.AgentEvent{
			Type:      "text_delta",
			Timestamp: now,
			Payload:   remainder,
			Metadata:  map[string]any{"item_id": item.ID},
		}, true

	case "command_execution":
		meta := map[string]any{
			"output": item.AggregatedOutput,
			"status": item.Status,
		}
		if item.ExitCode != nil {
			meta["exit_code"] = *item.ExitCode
		}

		return adapter.AgentEvent{
			Type:      "tool_result",
			Timestamp: now,
			Payload:   item.Command,
			Metadata:  meta,
		}, true

	case "file_change":
		// Encode the change list into a JSON array for the Metadata field.
		type fileChangeEntry struct {
			Path string `json:"path"`
			Kind string `json:"kind"`
		}
		entries := make([]fileChangeEntry, len(item.Changes))
		for i, c := range item.Changes {
			entries[i] = fileChangeEntry(c)
		}
		changesJSON, _ := json.Marshal(entries)
		firstPath := ""
		if len(item.Changes) > 0 {
			firstPath = item.Changes[0].Path
		}

		return adapter.AgentEvent{
			Type:      "tool_result",
			Timestamp: now,
			Payload:   firstPath,
			Metadata:  map[string]any{"changes": string(changesJSON), "status": item.Status},
		}, true

	case "mcp_tool_call":
		meta := map[string]any{"status": item.Status}
		if item.McpError != nil {
			meta["error"] = item.McpError.Message
		}

		return adapter.AgentEvent{
			Type:      "tool_result",
			Timestamp: now,
			Payload:   item.Server + "." + item.Tool,
			Metadata:  meta,
		}, true

	case "web_search":
		return adapter.AgentEvent{
			Type:      "tool_result",
			Timestamp: now,
			Payload:   item.Query,
			Metadata:  map[string]any{"item_id": item.ID},
		}, true

	case "error":
		msg := item.ItemMessage
		if msg == "" {
			msg = item.Text
		}

		return adapter.AgentEvent{Type: "error", Timestamp: now, Payload: msg}, true

	default:
		// reasoning, todo_list, collab_tool_call — ignored
		slog.Debug("codex: ignoring completed item", "type", item.Type)

		return adapter.AgentEvent{}, false
	}
}

func (s *session) readStderr(r io.ReadCloser) {
	defer r.Close()
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		s.writeLogLine(line)
		slog.Debug("codex stderr", "line", line) //nolint:gosec // G706: log message is static, taint analysis false positive
	}
}

func (s *session) writeLogLine(line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.logFile == nil {
		return
	}
	if _, writeErr := s.logFile.WriteString(line + "\n"); writeErr != nil {
		slog.Warn("failed to write session log line", "error", writeErr)
	}
}

func (s *session) openLogFile() error {
	if s.logPath == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.logPath), 0o750); err != nil {
		return fmt.Errorf("create session log dir: %w", err)
	}
	f, err := os.OpenFile(s.logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open session log: %w", err)
	}
	s.mu.Lock()
	old := s.logFile
	s.logFile = f
	s.mu.Unlock()
	if old != nil {
		if closeErr := old.Close(); closeErr != nil {
			slog.Warn("failed to close previous session log file", "error", closeErr)
		}
	}
	return nil
}

func sessionLogPath(opts adapter.SessionOpts) string {
	if opts.SessionLogDir == "" {
		return ""
	}

	return filepath.Join(opts.SessionLogDir, opts.SessionID+".log")
}

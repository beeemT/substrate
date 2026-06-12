package acp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/adapter/bridge"
	"github.com/beeemT/substrate/internal/config"
)

// Verify Session implements adapter.AgentSession at compile time.
var _ adapter.AgentSession = (*Session)(nil)

const acpLogMaxBytes int64 = 10 * 1024 * 1024

type Session struct {
	id          string
	mode        adapter.SessionMode
	root        string
	cmd         *exec.Cmd
	client      *rpcClient
	events      chan adapter.AgentEvent
	done        chan struct{}
	doneOnce    sync.Once
	doneErr     error
	processDone chan struct{}
	finished    atomic.Bool // set true when finish is called
	emitMu      sync.Mutex  // guards finished check and channel send to prevent send-on-closed

	logMu    sync.Mutex
	logFile  *os.File
	logPath  string
	logDir   string
	logBytes int64
	segment  int

	init          initializeResponse
	acpSessionID  string
	resumeMethod  string
	configOptions []configOption
	compact       compactStrategy
	compactMu     sync.Mutex // protects compact field

	promptMu                sync.Mutex
	promptActive            bool
	sessionContext          string
	sessionContextDelivered bool
	foremanText             string
	steerCancel             chan struct{} // closed to signal current prompt to abort for steering
	steerDone               chan struct{} // closed when steer-aborter prompt has finished

	questions      *questionBroker
	terminals      *terminalManager
	questionSocket *questionSocket
	closeOnce      sync.Once
	traceClose     func()           // closes the raw protocol-trace file, if any
	acpCfg         config.ACPConfig // stored for compact detection in handleNotification
}

// makeOpenSteerCancel returns a new open (never-closed) channel for the initial steerCancel.
// This prevents the watch goroutine from blocking on a nil channel receive when a
// prompt is started without a preceding Steer call.
func makeOpenSteerCancel() chan struct{} { return make(chan struct{}) }

func newSession(id string, mode adapter.SessionMode, root string, cmd *exec.Cmd, logFile *os.File, logPath, logDir string, acpCfg config.ACPConfig) *Session {
	s := &Session{
		id: id, mode: mode, root: root, cmd: cmd,
		logFile: logFile, logPath: logPath, logDir: logDir,
		events: make(chan adapter.AgentEvent, 256),
		done:   make(chan struct{}),
		// Initialize steerCancel to an open channel so runPrompt's watch goroutine
		// never blocks on a nil channel receive when no Steer call preceded the prompt.
		steerCancel: makeOpenSteerCancel(),
		acpCfg:      acpCfg,
		processDone: make(chan struct{}),
	}
	return s
}

func (s *Session) ID() string                        { return s.id }
func (s *Session) Done() <-chan struct{}             { return s.done }
func (s *Session) Events() <-chan adapter.AgentEvent { return s.events }

func (s *Session) Wait(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.done:
		return s.doneErr
	}
}

func (s *Session) SendMessage(ctx context.Context, msg string) error {
	return s.runPromptWithInputLog(ctx, msg, "message", true, true)
}

func (s *Session) Steer(ctx context.Context, msg string) error {
	s.promptMu.Lock()
	var prevSteerDone chan struct{}
	active := s.promptActive
	if active {
		// Capture the old steerDone before replacing it. The old prompt's runPrompt
		// will close this channel when its defer runs after the cancellation.
		prevSteerDone = s.steerDone
		// Signal the old prompt to abort. Its watch goroutine will receive on
		// isSteerAborted (the closed steerCancel), cancel mergedCtx, and runPrompt
		// will return without closing steerDone (since wasSteerCancelled=true).
		close(s.steerCancel) // signal old prompt to abort
		// Create a new steerCancel for the new prompt (always non-nil).
		s.steerCancel = make(chan struct{})
		// Create a new steerDone for the new prompt. The new prompt's runPrompt
		// will close this channel when it finishes (unless cancelled by steering).
		s.steerDone = make(chan struct{})
	}
	s.promptMu.Unlock()
	// Wait for the old prompt's runPrompt to fully exit (its defer must close
	// isSteerDone and set promptActive=false) before starting a new runPrompt,
	// otherwise the new runPrompt would find promptActive==true and return an error.
	// prevSteerDone is nil if there was no active prompt, so this select is a no-op.
	if prevSteerDone != nil {
		<-prevSteerDone
	}
	return s.runPrompt(ctx, "Steering update from operator:\n\n"+msg, true)
}

func (s *Session) SendAnswer(_ context.Context, answer string) error {
	if err := s.questions.answer(answer); err != nil {
		return err
	}
	s.writeCanonicalInputLog("answer", answer)
	return nil
}

func (s *Session) Compact(ctx context.Context) error {
	s.compactMu.Lock()
	cmd := s.compact.command
	s.compactMu.Unlock()
	if cmd == "" {
		return adapter.ErrCompactNotSupported
	}
	return s.runPrompt(ctx, "/"+cmd, false)
}

func (s *Session) Abort(ctx context.Context) error {
	var outErr error
	// Skip the cancel/close notifications when the rpc client is already
	// closed — typically because the agent exited on its own and the stdout
	// reader hit EOF. The cancel/close are best-effort by design; if the
	// client is gone there's nothing to notify, and the writes only produce
	// noisy "acp rpc client closed" errors up the stack.
	if s.acpSessionID != "" && !s.client.Closed() {
		if err := s.client.Notify(ctx, "session/cancel", sessionIDParams{SessionID: s.acpSessionID}); err != nil {
			outErr = errors.Join(outErr, fmt.Errorf("cancel acp session: %w", err))
		}
		if s.init.AgentCapabilities.SessionCapabilities.supportsClose() {
			// Use a short timeout for graceful shutdown; it's best-effort.
			closeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			if err := s.client.Call(closeCtx, "session/close", sessionIDParams{SessionID: s.acpSessionID}, nil); err != nil {
				outErr = errors.Join(outErr, fmt.Errorf("close acp session: %w", err))
			}
		}
	}
	s.cleanup()
	if err := s.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		outErr = errors.Join(outErr, fmt.Errorf("kill acp process: %w", err))
	}
	s.finish(context.Canceled)
	return outErr
}

func (s *Session) ResumeInfo() map[string]string {
	if s.acpSessionID == "" {
		return nil
	}
	info := map[string]string{"acp_agent_session_id": s.acpSessionID, "acp_protocol_version": "1", "acp_resume_method": s.resumeMethod}
	if s.init.AgentInfo.Name != "" {
		info["acp_agent_name"] = s.init.AgentInfo.Name
	}
	if s.init.AgentInfo.Version != "" {
		info["acp_agent_version"] = s.init.AgentInfo.Version
	}
	return info
}

func newSessionCreateParams(root string, mcpServers []mcpServer, cfg config.ACPConfig) sessionCreateParams {
	params := sessionCreateParams{CWD: root, MCPServers: mcpServers}
	if !isKiroACPCommand(cfg) {
		params.Agent = cfg.Agent
		params.RegistryID = cfg.RegistryID
	}
	return params
}

func (s *Session) setupACPSession(ctx context.Context, opts adapter.SessionOpts, mcpServers []mcpServer) (sessionResponse, string, error) {
	if mcpServers == nil {
		mcpServers = []mcpServer{}
	}
	params := newSessionCreateParams(s.root, mcpServers, s.acpCfg)
	if existing := opts.ResumeInfo["acp_agent_session_id"]; existing != "" {
		params.SessionID = existing
		if s.init.AgentCapabilities.SessionCapabilities.supportsResume() {
			var resp sessionResponse
			if err := s.client.Call(ctx, "session/resume", params, &resp); err != nil {
				slog.Warn("failed to resume ACP session; starting a new session", "session_id", s.id, "acp_session_id", existing, "error", err)
				params.SessionID = ""
			} else {
				return resp, "resume", nil
			}
		} else if s.init.AgentCapabilities.LoadSession {
			var resp sessionResponse
			if err := s.client.Call(ctx, "session/load", params, &resp); err != nil {
				slog.Warn("failed to load ACP session; starting a new session", "session_id", s.id, "acp_session_id", existing, "error", err)
				params.SessionID = ""
			} else {
				return resp, "load", nil
			}
		}
		// Resume info present but agent doesn't support resume or load.
		// Clear the stale session ID before creating a new session.
		params.SessionID = ""
	}
	var resp sessionResponse
	if err := s.client.Call(ctx, "session/new", params, &resp); err != nil {
		return resp, "", fmt.Errorf("create acp session: %w", err)
	}
	return resp, "new", nil
}

func (s *Session) applyConfiguredOptions(ctx context.Context, cfg config.ACPConfig, opts adapter.SessionOpts) error {
	model := cfg.Model
	if opts.Model != nil {
		model = *opts.Model
	}
	requests := []struct{ category, value string }{{"model", model}, {"mode", cfg.Mode}, {"thought_level", cfg.ThoughtLevel}}
	for _, req := range requests {
		if req.value == "" {
			continue
		}
		opt := findConfigOption(s.configOptions, req.category)
		if opt.ID != "" {
			var resp setConfigOptionResponse
			if err := s.client.Call(ctx, "session/set_config_option", setConfigOptionParams{SessionID: s.acpSessionID, ConfigID: opt.ID, Value: req.value}, &resp); err != nil {
				return fmt.Errorf("set acp config option %s: %w", req.category, err)
			}
			s.configOptions = resp.ConfigOptions
			continue
		}
		if req.category == "mode" && s.init.AgentCapabilities.SessionCapabilities.supportsSetMode() {
			if err := s.client.Call(ctx, "session/set_mode", setModeParams{SessionID: s.acpSessionID, ModeID: req.value}, nil); err != nil {
				return fmt.Errorf("set acp mode: %w", err)
			}
		}
	}
	return nil
}

func findConfigOption(options []configOption, category string) configOption {
	for _, opt := range options {
		if opt.Category == category || opt.ID == category {
			return opt
		}
	}
	return configOption{}
}

func (s *Session) startPrompt(text string) {
	go func() {
		if err := s.runPromptWithInputLog(context.Background(), text, "prompt", true, true); err != nil {
			slog.Warn("acp: initial prompt failed", "error", err)
		}
	}()
}

func (s *Session) runPrompt(ctx context.Context, text string, finishOnDone bool) error {
	return s.runPromptWithInputLog(ctx, text, "", false, finishOnDone)
}

func (s *Session) runPromptWithInputLog(ctx context.Context, text, inputKind string, consumeSessionContext, finishOnDone bool) error {
	s.promptMu.Lock()
	if s.promptActive {
		s.promptMu.Unlock()
		return errors.New("acp prompt already active")
	}
	sessionContext := ""
	if consumeSessionContext && !s.sessionContextDelivered {
		sessionContext = s.sessionContext
		s.sessionContextDelivered = true
	}
	promptText := text
	if consumeSessionContext {
		promptText = combineSessionContextAndPrompt(sessionContext, text)
	}
	s.promptActive = true
	// Capture the steer channels. steerCancel is always non-nil (initialized in newSession).
	// steerDone may be nil if this is the first prompt (not started via Steer).
	isSteerAborted := s.steerCancel
	isSteerDone := s.steerDone
	s.promptMu.Unlock()
	// Track whether this prompt was cancelled due to steering so we know whether to
	// close isSteerDone in the defer. If mergedCtx is cancelled (steering cancelled us),
	// the defer must close isSteerDone so that the previous Steer call (which waits
	// on prevSteerDone = the old isSteerDone) unblocks. Each prompt owns its channel
	// exclusively, so double-close is impossible.
	wasSteerCancelled := atomic.Bool{}
	wasSteerCancelled.Store(false)
	defer func() {
		s.promptMu.Lock()
		s.promptActive = false
		if isSteerDone != nil {
			close(isSteerDone) // signal that this prompt has finished (normal or steer-cancelled)
		}
		s.promptMu.Unlock()
	}()
	// Merge context with steerCancel to abort when steering cancels this prompt.
	mergedCtx, cancelMerge := context.WithCancel(ctx)
	defer cancelMerge()
	go func() {
		select {
		case <-mergedCtx.Done():
			// Context cancelled, don't need to watch steerCancel.
			return
		case <-isSteerAborted:
			wasSteerCancelled.Store(true)
			cancelMerge() // abort the prompt's Call
		}
	}()
	if inputKind != "" {
		s.writeCanonicalInputLog("session_context", sessionContext)
		s.writeCanonicalInputLog(inputKind, text)
	}
	var resp promptResponse
	err := s.client.Call(mergedCtx, "session/prompt", promptParams{SessionID: s.acpSessionID, Prompt: []contentBlock{{Type: "text", Text: promptText}}}, &resp)
	if err != nil {
		// If cancelled due to steering, don't finalize the session.
		if wasSteerCancelled.Load() {
			s.emit(adapter.AgentEvent{Type: "error", Timestamp: now(), Payload: "ACP prompt cancelled for steering", Metadata: map[string]any{"stop_reason": "steer_cancel"}})
			return nil
		}
		s.emit(adapter.AgentEvent{Type: "error", Timestamp: now(), Payload: err.Error()})
		s.finish(err)
		return err
	}
	if s.mode == adapter.SessionModeForeman {
		s.emit(adapter.AgentEvent{Type: "foreman_proposed", Timestamp: now(), Payload: s.foremanText, Metadata: map[string]any{"uncertain": false}})
		s.foremanText = ""
	} else if resp.StopReason == "end_turn" || resp.StopReason == "" {
		s.emit(adapter.AgentEvent{Type: "done", Timestamp: now(), Metadata: map[string]any{"stop_reason": resp.StopReason}})
		if finishOnDone {
			s.finish(nil)
		}
	} else if resp.StopReason == "cancelled" {
		// Cancelled response from ACP (not from steering) - finalize the session.
		err := context.Canceled
		s.emit(adapter.AgentEvent{Type: "error", Timestamp: now(), Payload: "ACP prompt cancelled", Metadata: map[string]any{"stop_reason": resp.StopReason}})
		s.finish(err)
		return err
	} else {
		err := fmt.Errorf("acp prompt stopped: %s", resp.StopReason)
		s.emit(adapter.AgentEvent{Type: "error", Timestamp: now(), Payload: err.Error(), Metadata: map[string]any{"stop_reason": resp.StopReason}})
		s.finish(err)
		return err
	}
	return nil
}

func (s *Session) emit(evt adapter.AgentEvent) {
	s.emitMu.Lock()
	defer s.emitMu.Unlock()
	// Check finished under lock to prevent race with finish().
	if s.finished.Load() {
		return
	}
	if evt.Timestamp.IsZero() {
		evt.Timestamp = now()
	}
	if s.mode == adapter.SessionModeForeman {
		if evt.Type == "text_delta" {
			s.foremanText += evt.Payload
			return
		}
		if evt.Type != "foreman_proposed" && evt.Type != "done" && evt.Type != "error" && evt.Type != "question" {
			return
		}
	}
	// Terminal events use blocking sends per project convention. The caller
	// (e.g. Wait) is responsible for not calling emit after finish.
	if evt.Type == "done" || evt.Type == "error" || evt.Type == "question" || evt.Type == "foreman_proposed" {
		s.events <- evt
		return
	}
	// Non-terminal events: drop if session finished or channel full.
	select {
	case s.events <- evt:
	case <-s.done:
		slog.Debug("acp: dropping event because session finished", "type", evt.Type)
	default:
		slog.Warn("acp: dropping non-terminal event because buffer is full", "type", evt.Type)
	}
}

func (s *Session) finish(err error) {
	s.emitMu.Lock()
	defer s.emitMu.Unlock()
	s.doneOnce.Do(func() {
		s.finished.Store(true)
		s.doneErr = err
		s.cleanup()
		// Guarantee the rpc client is closed before we signal done. closeWithError
		// is idempotent via closeOnce, so this races safely against readStdout
		// hitting EOF first. Without this, callers that synchronize on Done()
		// (e.g. Abort) can observe s.done closed while s.client.Closed() is
		// still false, and the best-effort cancel/close block in Abort would
		// race against the in-flight close and return a spurious "EOF" error.
		s.client.closeWithError(nil)
		close(s.events)
		close(s.done)
	})
}

func (s *Session) cleanup() {
	s.closeOnce.Do(func() {
		if s.traceClose != nil {
			s.traceClose()
		}
		if s.questions != nil {
			s.questions.cancelAll()
		}
		if s.terminals != nil {
			s.terminals.cleanup()
		}
		if s.questionSocket != nil {
			s.questionSocket.close()
		}
		if s.cmd != nil && s.cmd.Process != nil {
			if err := s.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				slog.Debug("acp: process kill during cleanup failed", "error", err)
			}
		}
		s.logMu.Lock()
		if s.logFile != nil {
			if err := s.logFile.Close(); err != nil {
				slog.Warn("acp: close log failed", "error", err)
			}
			s.logFile = nil
			compressedPath := fmt.Sprintf("%s.%d.gz", s.logPath, time.Now().Unix())
			if err := bridge.CompressFile(s.logPath, compressedPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				slog.Warn("acp: compress log failed", "error", err)
			}
			bridge.CleanupOldSegments(s.logDir, s.id)
		}
		s.logMu.Unlock()
	})
}

func (s *Session) reapProcess() {
	err := s.cmd.Wait()
	close(s.processDone)
	select {
	case <-s.done:
		return // Already finished (e.g., from Abort or session cancellation).
	default:
	}
	if err != nil {
		s.emit(adapter.AgentEvent{Type: "error", Timestamp: now(), Payload: err.Error()})
		s.finish(err)
	} else {
		// Process exited cleanly. Signal completion to unblock Wait().
		s.finish(nil)
	}
}

func combineSessionContextAndPrompt(contextText, prompt string) string {
	contextText = strings.TrimSpace(contextText)
	prompt = strings.TrimSpace(prompt)
	switch {
	case contextText != "" && prompt != "":
		return contextText + "\n\n" + prompt
	case contextText != "":
		return contextText
	default:
		return prompt
	}
}

func (s *Session) writeCanonicalInputLog(inputKind, text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	record := struct {
		Type  string `json:"type"`
		Event struct {
			Type      string `json:"type"`
			InputKind string `json:"input_kind"`
			Text      string `json:"text"`
		} `json:"event"`
	}{
		Type: "event",
	}
	record.Event.Type = "input"
	record.Event.InputKind = inputKind
	record.Event.Text = text
	data, err := json.Marshal(record)
	if err != nil {
		slog.Warn("acp: marshal input log failed", "error", err)
		return
	}
	line := append(data, '\n')
	s.logMu.Lock()
	defer s.logMu.Unlock()
	if s.logFile == nil {
		return
	}
	if s.logBytes+int64(len(line)) > acpLogMaxBytes {
		s.rotateLogLocked()
	}
	if _, err := s.logFile.Write(line); err != nil {
		slog.Warn("acp: write input log failed", "error", err)
	} else {
		s.logBytes += int64(len(line))
	}
}

func (s *Session) writeLogEvent(evt adapter.AgentEvent) {
	data, err := json.Marshal(struct {
		Type  string             `json:"type"`
		Event adapter.AgentEvent `json:"event"`
	}{Type: "event", Event: evt})
	if err != nil {
		slog.Warn("acp: marshal log event failed", "error", err)
		return
	}
	line := append(data, '\n')
	s.logMu.Lock()
	defer s.logMu.Unlock()
	if s.logFile == nil {
		return
	}
	if s.logBytes+int64(len(line)) > acpLogMaxBytes {
		s.rotateLogLocked()
	}
	if _, err := s.logFile.Write(line); err != nil {
		slog.Warn("acp: write log event failed", "error", err)
	} else {
		s.logBytes += int64(len(line))
	}
}

func (s *Session) rotateLogLocked() {
	if s.logFile == nil {
		return
	}
	if err := s.logFile.Close(); err != nil {
		slog.Warn("acp: close log segment failed", "error", err)
	}
	segPath := fmt.Sprintf("%s.%d", s.logPath, s.segment)
	s.segment++
	if err := os.Rename(s.logPath, segPath); err != nil {
		slog.Warn("acp: rotate log failed", "error", err)
		return
	}
	go func() {
		if err := bridge.CompressFile(segPath, segPath+".gz"); err != nil {
			slog.Warn("acp: compress rotated log failed", "error", err)
		}
	}()
	f, err := os.OpenFile(s.logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		slog.Warn("acp: reopen log failed", "error", err)
		s.logFile = nil
		return
	}
	s.logFile = f
	s.logBytes = 0
}

func now() time.Time { return time.Now() }

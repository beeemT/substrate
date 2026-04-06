package opencode

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/beeemT/substrate/internal/adapter"
)

const (
	// sseReadBufferSize is the buffer size for reading SSE lines.
	sseReadBufferSize = 64 * 1024
	// eventChannelSize is the buffer size for the events channel.
	eventChannelSize = 256
)

// session implements adapter.AgentSession for an OpenCode HTTP session.
type session struct {
	id         string
	mode       adapter.SessionMode
	serverURL  string // e.g. http://127.0.0.1:4096
	openCodeID string // opencode session ID
	httpClient *http.Client
	events     chan adapter.AgentEvent
	logFile    *os.File
	logPath    string
	logDir     string
	workDir    string
	cmd        *exec.Cmd // opencode serve child process

	mu        sync.Mutex
	aborted   bool
	waitOnce  sync.Once
	waitDone  chan struct{}
	waitErr   error
	closeOnce sync.Once

	// pendingQuestionID tracks the latest pending question request ID
	// from SSE events, used by SendAnswer.
	pendingQuestionID string
	variant           string
}

func (s *session) ID() string { return s.id }

func (s *session) Events() <-chan adapter.AgentEvent { return s.events }

// Wait blocks until the session ends (done, error, abort) or context cancel.
func (s *session) Wait(ctx context.Context) error {
	select {
	case <-ctx.Done():
		if abortErr := s.Abort(context.Background()); abortErr != nil {
			slog.Warn("opencode: failed to abort on context cancel", "error", abortErr)
		}
		return ctx.Err()
	case <-s.waitDone:
		return s.waitErr
	}
}

// SendMessage sends a message to the running agent via POST /session/:id/message.
func (s *session) SendMessage(ctx context.Context, msg string) error {
	body := SendMessageRequest{Content: msg}
	body.Variant = s.variant

	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.serverURL+"/session/"+s.openCodeID+"/message",
		bytes.NewReader(data),
	)
	if err != nil {
		return fmt.Errorf("create message request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send message: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("send message: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	return nil
}

// Steer is not supported by OpenCode.
func (s *session) Steer(_ context.Context, _ string) error {
	return adapter.ErrSteerNotSupported
}

// SendAnswer sends an answer to resolve a pending question via POST /question/:requestID/reply.
func (s *session) SendAnswer(ctx context.Context, answer string) error {
	s.mu.Lock()
	requestID := s.pendingQuestionID
	s.mu.Unlock()

	if requestID == "" {
		return errors.New("opencode: no pending question to answer")
	}

	body := QuestionReplyRequest{Content: answer}
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal answer: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.serverURL+"/question/"+requestID+"/reply",
		bytes.NewReader(data),
	)
	if err != nil {
		return fmt.Errorf("create answer request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send answer: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("send answer: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	// Clear the pending question ID after a successful reply.
	s.mu.Lock()
	s.pendingQuestionID = ""
	s.mu.Unlock()

	return nil
}

// Abort terminates the session: sends abort to the server, kills the child
// process, and closes all resources.
func (s *session) Abort(ctx context.Context) error {
	s.mu.Lock()
	if s.aborted {
		s.mu.Unlock()
		return nil
	}
	s.aborted = true
	s.mu.Unlock()

	// Best-effort: tell the server to abort the session.
	if s.openCodeID != "" {
		abortCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(abortCtx, http.MethodPost,
			s.serverURL+"/session/"+s.openCodeID+"/abort", nil)
		if err == nil {
			resp, err := s.httpClient.Do(req)
			if err != nil {
				slog.Debug("opencode: abort request failed", "error", err)
			} else {
				resp.Body.Close()
			}
		}
	}

	s.closeResources()
	return nil
}

// ResumeInfo returns the opencode session ID for fork/resume, or nil if not set.
func (s *session) ResumeInfo() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.openCodeID == "" {
		return nil
	}
	return map[string]string{
		"opencode_session_id": s.openCodeID,
	}
}

// Compact requests context compaction via POST /session/:id/summarize.
func (s *session) Compact(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.serverURL+"/session/"+s.openCodeID+"/summarize", nil)
	if err != nil {
		return fmt.Errorf("create compact request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("compact session: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("compact session: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	return nil
}

// setQuestionID stores the latest pending question request ID from SSE events.
// Called from the SSE reader goroutine.
func (s *session) setQuestionID(id string) {
	s.mu.Lock()
	s.pendingQuestionID = id
	s.mu.Unlock()
}

// setOpenCodeID stores the opencode session ID received from session.created.
// Called from the SSE reader goroutine.
func (s *session) setOpenCodeID(id string) {
	s.mu.Lock()
	s.openCodeID = id
	s.mu.Unlock()
}

// finishSession closes the waitDone channel with the given error exactly once.
// Called from the SSE reader goroutine when the SSE stream ends.
func (s *session) finishSession(err error) {
	s.waitOnce.Do(func() {
		s.waitErr = err
		close(s.waitDone)
	})
}

// startSSEReader connects to GET /event, reads SSE lines, translates them
// to AgentEvents, and writes them to the events channel and log file.
// On disconnect, it closes the events channel and finishes the session.
func (s *session) startSSEReader(ctx context.Context) {
	go func() {
		defer close(s.events)
		lastPartText := make(map[string]string)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.serverURL+"/event", nil)
		if err != nil {
			slog.Error("opencode: create SSE request", "error", err)
			s.closeResources()
			s.finishSession(fmt.Errorf("create SSE request: %w", err))
			return
		}
		req.Header.Set("Accept", "text/event-stream")

		resp, err := s.httpClient.Do(req)
		if err != nil {
			slog.Error("opencode: SSE connection failed", "error", err)
			s.closeResources()
			s.finishSession(fmt.Errorf("SSE connection: %w", err))
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			slog.Error("opencode: SSE unexpected status", "status", resp.StatusCode)
			s.closeResources()
			s.finishSession(fmt.Errorf("SSE status: %d", resp.StatusCode))
			return
		}

		// Read SSE lines. SSE spec: lines starting with "data:" contain payload.
		// Empty lines separate events.
		reader := newSSELineReader(resp.Body)

		for {
			line, err := reader.ReadSSELine()
			if err != nil {
				if !errors.Is(err, io.EOF) {
					slog.Debug("opencode: SSE read ended", "error", err)
				}
				break
			}

			// Skip non-data lines (event:, id:, comments, empty lines).
			if !strings.HasPrefix(line, "data:") {
				continue
			}

			// Extract the payload after "data:".
			data := strings.TrimPrefix(line, "data:")
			data = strings.TrimSpace(data)
			if data == "" {
				continue
			}

			// Parse and translate the SSE event.
			events := mapSSEEvent(json.RawMessage(data), lastPartText)

			// Track question.asked request IDs and session.created IDs.
			for _, evt := range events {
				if evt.Type == "question" {
					if reqID, ok := evt.Metadata["request_id"].(string); ok && reqID != "" {
						s.setQuestionID(reqID)
					}
				}
				if evt.Type == "started" {
					if ocID, ok := evt.Metadata["opencode_session_id"].(string); ok && ocID != "" {
						s.setOpenCodeID(ocID)
					}
				}
				s.writeLogEvent(evt)
			}

			// Send events to the channel.
			for _, evt := range events {
				select {
				case s.events <- evt:
				default:
					slog.Warn("opencode: event channel full", "type", evt.Type)
				}
			}

			// If we got a done or error event, finish the session.
			for _, evt := range events {
				if evt.Type == "done" || evt.Type == "error" {
					s.closeResources()
					s.finishSession(nil)
					return
				}
			}
		}

		// SSE stream ended without an explicit done/error — session is done.
		s.closeResources()
		s.finishSession(nil)
	}()
}

// writeLogEvent writes an AgentEvent to the session log file in JSON-line format.
func (s *session) writeLogEvent(evt adapter.AgentEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.logFile == nil {
		return
	}

	logEntry := struct {
		Type  string             `json:"type"`
		Event adapter.AgentEvent `json:"event"`
	}{
		Type:  "event",
		Event: evt,
	}

	data, err := json.Marshal(logEntry)
	if err != nil {
		slog.Warn("opencode: failed to marshal log event", "error", err)
		return
	}
	if _, err := s.logFile.Write(append(data, '\n')); err != nil {
		slog.Warn("opencode: failed to write log event", "error", err)
	}
}

// closeResources cleans up the log file, child process, and events channel.
// Called exactly once via closeOnce.
func (s *session) closeResources() {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		if s.logFile != nil {
			if err := s.logFile.Close(); err != nil {
				slog.Warn("opencode: failed to close log file", "error", err)
			}
			s.logFile = nil
		}
		s.mu.Unlock()

		if s.cmd != nil && s.cmd.Process != nil {
			if err := s.cmd.Process.Kill(); err != nil {
				slog.Debug("opencode: failed to kill child process", "error", err)
			}
		}

	})
}

// sseLineReader wraps an io.Reader to read SSE-style lines,
// handling the buffered reading of the SSE stream.
type sseLineReader struct {
	r *bufio.Reader
}

func newSSELineReader(r io.Reader) *sseLineReader {
	return &sseLineReader{r: bufio.NewReaderSize(r, sseReadBufferSize)}
}

// ReadSSELine reads the next non-empty content line from the SSE stream.
// Returns io.EOF when the stream ends.
func (lr *sseLineReader) ReadSSELine() (string, error) {
	for {
		line, err := lr.r.ReadString('\n')
		if err != nil && err != io.EOF {
			return "", err
		}
		line = strings.TrimRight(line, "\r\n")
		if line != "" || err == io.EOF {
			return line, err
		}
		// Empty line (SSE event separator) — skip and continue.
	}
}

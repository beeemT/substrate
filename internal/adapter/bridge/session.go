// Package bridge provides shared infrastructure for JSON-line bridge harnesses.
// Both the ohmypi and claudeagent adapters embed BridgeSession to avoid
// duplicating subprocess lifecycle, event reading, and log rotation logic.
package bridge

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/beeemT/substrate/internal/adapter"
)

// BridgeMsg represents a message sent to a bridge subprocess.
type BridgeMsg struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// SessionMetaFunc is called when readEvents encounters a top-level
// session_meta event. The implementer extracts adapter-specific fields
// (e.g. session ID, session file) from the raw JSON line.
type SessionMetaFunc func(line []byte)

// BridgeSession implements the core subprocess lifecycle, event reading,
// and log rotation shared by all bridge-based harnesses.
//
// Adapter-specific harnesses embed this struct and add their own fields
// (e.g. claudeSessionID, ompSessionFile) and accessor methods.
type BridgeSession struct {
	ID      string
	Mode    adapter.SessionMode
	Cmd     *exec.Cmd
	Stdin   io.WriteCloser
	Stdout  io.Reader
	Stderr  io.Reader
	Events  chan adapter.AgentEvent
	LogFile *os.File
	LogPath string
	LogDir  string
	WorkDir string

	// TmpDir is the sandbox temp directory created for this session.
	// The session owner is responsible for calling os.RemoveAll on cleanup.
	TmpDir string

	// ParseSessionMeta is called by readEvents for each session_meta line.
	ParseSessionMeta SessionMetaFunc

	mu        sync.Mutex
	aborted   bool
	waitOnce  sync.Once
	waitDone  chan error
	readDone  chan struct{}
	closeOnce sync.Once
}

// NewBridgeSession creates a BridgeSession with initialized channels.
func NewBridgeSession(id string, mode adapter.SessionMode) *BridgeSession {
	return &BridgeSession{
		ID:       id,
		Mode:     mode,
		Events:   make(chan adapter.AgentEvent, 64),
		readDone: make(chan struct{}),
	}
}

// closeEvents safely closes the events channel exactly once.
func (s *BridgeSession) CloseEvents() {
	s.closeOnce.Do(func() {
		close(s.Events)
	})
}

// startProcessReaper ensures exactly one goroutine calls cmd.Wait().
// Returns a buffered channel that receives the process exit error.
func (s *BridgeSession) startProcessReaper() <-chan error {
	s.waitOnce.Do(func() {
		ch := make(chan error, 1)
		go func() { ch <- s.Cmd.Wait() }()
		s.waitDone = ch
	})
	return s.waitDone
}

// Wait blocks until the session completes (done or error).
func (s *BridgeSession) Wait(ctx context.Context) error {
	select {
	case <-ctx.Done():
		s.Abort(ctx) //nolint:errcheck // best-effort cleanup
		return ctx.Err()
	case err := <-s.startProcessReaper():
		// Wait for readEvents to finish draining before closing events channel.
		<-s.readDone

		s.mu.Lock()
		defer s.mu.Unlock()

		if s.LogFile != nil {
			s.LogFile.Close()
			s.LogFile = nil

			compressedPath := s.LogPath + "." + strconv.FormatInt(time.Now().Unix(), 10) + ".gz"
			go compressFile(s.LogPath, compressedPath) //nolint:errcheck // best-effort async compression
		}

		s.CloseEvents()

		// Clean up sandbox temp directory.
		if s.TmpDir != "" {
			os.RemoveAll(s.TmpDir) //nolint:errcheck // best-effort cleanup
		}

		if err != nil {
			if s.aborted {
				return nil // Graceful abort, not an error.
			}
			return fmt.Errorf("bridge subprocess exited: %w", err)
		}
		return nil
	}
}

// Events returns a channel emitting agent events.
func (s *BridgeSession) EventsChan() <-chan adapter.AgentEvent {
	return s.Events
}

// SendMessage sends a message to the running agent.
func (s *BridgeSession) SendMessage(_ context.Context, msg string) error {
	return s.SendBridgeMsg("message", msg)
}

// Steer sends a steering prompt that interrupts the agent's active streaming turn.
func (s *BridgeSession) Steer(_ context.Context, msg string) error {
	return s.SendBridgeMsg("steer", msg)
}

// SendAnswer sends an answer to resolve a pending ask_foreman tool call.
func (s *BridgeSession) SendAnswer(_ context.Context, answer string) error {
	return s.SendBridgeMsg("answer", answer)
}

// SendPrompt sends a prompt message to the bridge.
func (s *BridgeSession) SendPrompt(text string) error {
	return s.SendBridgeMsg("prompt", text)
}

// WriteRawMsg writes a pre-marshaled JSON line to the bridge subprocess.
// Use for protocol messages that don't fit the simple {type, text} shape
// (e.g. bridgeInitMsg with many fields). Callers marshal data themselves.
func (s *BridgeSession) WriteRawMsg(data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.Stdin.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write raw message: %w", err)
	}
	return nil
}

// SendBridgeMsg marshals and sends a typed message to the bridge subprocess.
// This is the single shared write path — all send methods delegate here.
func (s *BridgeSession) SendBridgeMsg(msgType, text string) error {
	msg := BridgeMsg{Type: msgType, Text: text}
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal %s message: %w", msgType, err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := s.Stdin.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write %s: %w", msgType, err)
	}
	return nil
}

// Abort terminates the agent session gracefully.
func (s *BridgeSession) Abort(_ context.Context) error {
	s.mu.Lock()
	if s.aborted {
		s.mu.Unlock()
		return nil
	}
	s.aborted = true

	// Send abort message to bridge under the same lock.
	msg := BridgeMsg{Type: "abort"}
	data, err := json.Marshal(msg)
	if err != nil {
		s.mu.Unlock()
		return fmt.Errorf("marshal abort message: %w", err)
	}
	if _, writeErr := s.Stdin.Write(append(data, '\n')); writeErr != nil {
		slog.Debug("failed to send abort message", "err", writeErr)
	}
	s.Stdin.Close() //nolint:errcheck // best-effort cleanup
	s.mu.Unlock()

	// Give the subprocess time to exit gracefully.
	timeout := time.After(5 * time.Second)

	select {
	case <-timeout:
		if s.Cmd.Process != nil {
			slog.Warn("bridge subprocess did not exit gracefully, sending SIGKILL")
			s.Cmd.Process.Kill() //nolint:errcheck // best-effort cleanup
		}
	case <-s.startProcessReaper():
		// Subprocess exited gracefully.
	}

	// Wait for readEvents to finish draining.
	<-s.readDone

	// Close the log file.
	s.mu.Lock()
	if s.LogFile != nil {
		s.LogFile.Close()
		s.LogFile = nil
	}
	s.mu.Unlock()

	// Clean up sandbox temp directory.
	if s.TmpDir != "" {
		os.RemoveAll(s.TmpDir) //nolint:errcheck // best-effort cleanup
	}

	return nil
}

// StartReaders launches background goroutines for reading events and stderr.
func (s *BridgeSession) StartReaders() {
	go s.readEvents()
	go s.readStderr()
}

// readEvents reads JSON-line events from stdout and emits them to the events channel.
func (s *BridgeSession) readEvents() {
	scanner := bufio.NewScanner(s.Stdout)
	scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024) // 10MB max line size

	for scanner.Scan() {
		line := scanner.Bytes()

		// Write to session log with timestamp.
		s.mu.Lock()
		if s.LogFile != nil {
			timestamp := time.Now().UTC().Format(time.RFC3339Nano)
			logEntry := fmt.Sprintf("%s %s\n", timestamp, string(line))
			if _, err := s.LogFile.WriteString(logEntry); err != nil {
				slog.Warn("failed to write to session log", "err", err)
			}
			if info, err := s.LogFile.Stat(); err == nil {
				if info.Size() >= 10*1024*1024 {
					s.rotateLogLocked()
				}
			}
		}
		s.mu.Unlock()

		// Parse the outer envelope.
		var rawEvent struct {
			Type  string          `json:"type"`
			Event json.RawMessage `json:"event"`
		}
		if err := json.Unmarshal(line, &rawEvent); err != nil {
			slog.Warn("failed to parse bridge event", "line", string(line), "err", err) //nolint:gosec // G706: log message is static, taint analysis false positive
			continue
		}

		// Handle session_meta separately (not wrapped in {type:"event",event:{...}}).
		if rawEvent.Type == "session_meta" {
			if s.ParseSessionMeta != nil {
				s.ParseSessionMeta(line)
			}
			continue
		}

		// Map the event.
		event, err := MapBridgeEvent(rawEvent)
		if err != nil {
			slog.Warn("failed to map bridge event", "err", err)
			continue
		}

		if event != nil {
			select {
			case s.Events <- *event:
			default:
				slog.Warn("event channel full, dropping event", "type", event.Type)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		slog.Error("error reading bridge stdout", "err", err)
	}

	// Signal that all events have been drained.
	s.CloseEvents()
	close(s.readDone)
}

// readStderr reads stderr and logs it for debugging.
func (s *BridgeSession) readStderr() {
	scanner := bufio.NewScanner(s.Stderr)
	for scanner.Scan() {
		slog.Debug("bridge stderr", "line", scanner.Text()) //nolint:gosec // G706: log message is static, taint analysis false positive
	}
}

// CloseLogAndCompress closes the log file and starts async compression.
// Callers must hold s.mu.
func (s *BridgeSession) CloseLogAndCompress() {
	if s.LogFile == nil {
		return
	}
	s.LogFile.Close()
	s.LogFile = nil
	compressedPath := s.LogPath + "." + strconv.FormatInt(time.Now().Unix(), 10) + ".gz"
	go compressFile(s.LogPath, compressedPath) //nolint:errcheck // best-effort async compression
}

// MapBridgeEvent maps a bridge event envelope to an adapter.AgentEvent.
func MapBridgeEvent(raw struct {
	Type  string          `json:"type"`
	Event json.RawMessage `json:"event"`
},
) (*adapter.AgentEvent, error) {
	if raw.Type != "event" || raw.Event == nil {
		return nil, nil
	}

	var eventMap map[string]any
	if err := json.Unmarshal(raw.Event, &eventMap); err != nil {
		return nil, fmt.Errorf("parse event payload: %w", err)
	}

	eventType, ok := eventMap["type"].(string)
	if !ok {
		return nil, errors.New("missing event type")
	}

	switch eventType {
	case "input":
		text, _ := eventMap["text"].(string)
		inputKind, _ := eventMap["input_kind"].(string)
		return &adapter.AgentEvent{
			Type:      "input",
			Timestamp: time.Now(),
			Payload:   text,
			Metadata:  map[string]any{"input_kind": inputKind},
		}, nil

	case "assistant_output":
		text, ok := eventMap["text"].(string)
		if !ok {
			return nil, errors.New("missing assistant_output text")
		}
		return &adapter.AgentEvent{
			Type:      "text_delta",
			Timestamp: time.Now(),
			Payload:   text,
		}, nil

	case "thinking_output":
		text, _ := eventMap["text"].(string)
		return &adapter.AgentEvent{
			Type:      "text_delta",
			Timestamp: time.Now(),
			Payload:   text,
			Metadata:  map[string]any{"thinking": true},
		}, nil

	case "tool_start":
		toolName, _ := eventMap["tool"].(string)
		text, _ := eventMap["text"].(string)
		intent, _ := eventMap["intent"].(string)
		return &adapter.AgentEvent{
			Type:      "tool_start",
			Timestamp: time.Now(),
			Payload:   text,
			Metadata:  map[string]any{"tool": toolName, "intent": intent},
		}, nil

	case "tool_output":
		toolName, _ := eventMap["tool"].(string)
		text, _ := eventMap["text"].(string)
		return &adapter.AgentEvent{
			Type:      "tool_output",
			Timestamp: time.Now(),
			Payload:   text,
			Metadata:  map[string]any{"tool": toolName},
		}, nil

	case "tool_result":
		toolName, _ := eventMap["tool"].(string)
		text, _ := eventMap["text"].(string)
		isError, _ := eventMap["is_error"].(bool)
		return &adapter.AgentEvent{
			Type:      "tool_result",
			Timestamp: time.Now(),
			Payload:   text,
			Metadata:  map[string]any{"tool": toolName, "is_error": isError},
		}, nil

	case "question":
		question, ok := eventMap["question"].(string)
		if !ok {
			return nil, errors.New("missing question text")
		}
		ctx, _ := eventMap["context"].(string)
		return &adapter.AgentEvent{
			Type:      "question",
			Timestamp: time.Now(),
			Payload:   question,
			Metadata:  map[string]any{"context": ctx},
		}, nil

	case "foreman_proposed":
		text, ok := eventMap["text"].(string)
		if !ok {
			return nil, errors.New("missing foreman_proposed text")
		}
		uncertain, _ := eventMap["uncertain"].(bool)
		return &adapter.AgentEvent{
			Type:      "foreman_proposed",
			Timestamp: time.Now(),
			Payload:   text,
			Metadata:  map[string]any{"uncertain": uncertain},
		}, nil

	case "lifecycle":
		stage, _ := eventMap["stage"].(string)
		summary, _ := eventMap["summary"].(string)
		message, _ := eventMap["message"].(string)
		switch stage {
		case "started":
			return &adapter.AgentEvent{
				Type:      "started",
				Timestamp: time.Now(),
				Payload:   FirstNonEmpty(message, summary),
			}, nil
		case "completed":
			return &adapter.AgentEvent{
				Type:      "done",
				Timestamp: time.Now(),
				Payload:   summary,
			}, nil
		case "failed":
			return &adapter.AgentEvent{
				Type:      "error",
				Timestamp: time.Now(),
				Payload:   FirstNonEmpty(message, summary, "unknown error"),
			}, nil
		case "retry_wait":
			msg, _ := eventMap["message"].(string)
			return &adapter.AgentEvent{
				Type:      "retry_wait",
				Timestamp: time.Now(),
				Payload:   msg,
			}, nil
		case "retry_resumed":
			return &adapter.AgentEvent{
				Type:      "retry_resumed",
				Timestamp: time.Now(),
			}, nil
		default:
			return nil, nil
		}

	default:
		return nil, nil
	}
}

// FirstNonEmpty returns the first non-empty string from the provided values.
func FirstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// rotateLogLocked performs log rotation.
// Must be called with s.mu held.
func (s *BridgeSession) rotateLogLocked() {
	if s.LogFile == nil {
		return
	}

	s.LogFile.Close()
	s.LogFile = nil

	ts := strconv.FormatInt(time.Now().Unix(), 10)
	rotateSrc := s.LogPath + "." + ts + ".rotating"
	compressedPath := s.LogPath + "." + ts + ".gz"
	if err := os.Rename(s.LogPath, rotateSrc); err != nil {
		slog.Warn("failed to rename log segment for rotation", "err", err)
		rotateSrc = ""
	}

	newFile, err := os.OpenFile(s.LogPath, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		slog.Error("failed to open new log file after rotation", "err", err)
		return
	}
	s.LogFile = newFile

	if rotateSrc == "" {
		return
	}

	logDir := s.LogDir
	sessionID := s.ID
	go func() {
		if err := compressFile(rotateSrc, compressedPath); err != nil {
			slog.Warn("failed to compress log segment", "err", err)
			return
		}
		CleanupOldSegments(logDir, sessionID)
	}()
}

// compressFile compresses src to dst using gzip, then removes src.
// No defers are used to prevent double-close on the happy path.
// On failure, dst is removed to avoid leaving incomplete output on disk.
func compressFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}

	dstFile, err := os.Create(dst)
	if err != nil {
		srcFile.Close()
		return err
	}

	gzWriter := gzip.NewWriter(dstFile)
	_, err = io.Copy(gzWriter, srcFile)
	if closeErr := gzWriter.Close(); err == nil {
		err = closeErr
	}
	if closeErr := dstFile.Close(); err == nil {
		err = closeErr
	}
	srcFile.Close()

	if err != nil {
		os.Remove(dst)
		return err
	}

	if err := os.Remove(src); err != nil {
		slog.Warn("failed to remove log file after compression", "path", src, "err", err)
	}
	return nil
}

// CleanupOldSegments removes old compressed segments, keeping max 5.
func CleanupOldSegments(logDir, sessionID string) {
	pattern := filepath.Join(logDir, sessionID+"*.log.*.gz")
	files, err := filepath.Glob(pattern)
	if err != nil {
		return
	}

	sort.Slice(files, func(i, j int) bool {
		fi, err := os.Stat(files[i])
		if err != nil {
			return false
		}
		fj, err := os.Stat(files[j])
		if err != nil {
			return false
		}
		return fi.ModTime().Before(fj.ModTime())
	})

	if len(files) > 5 {
		for _, f := range files[:len(files)-5] {
			if err := os.Remove(f); err != nil {
				slog.Warn("failed to remove old log segment", "file", f, "err", err)
			}
		}
	}
}

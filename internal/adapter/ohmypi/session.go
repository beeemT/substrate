package omp

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
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

// ohMyPiSession implements adapter.AgentSession for oh-my-pi.
type ohMyPiSession struct {
	id      string
	mode    adapter.SessionMode
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  io.Reader
	stderr  io.Reader
	events  chan adapter.AgentEvent
	logFile *os.File
	logPath string
	logDir  string
	workDir string

	mu        sync.Mutex
	aborted   bool
	closeOnce sync.Once // ensures events channel is only closed once
}

// closeEvents safely closes the events channel exactly once.
func (s *ohMyPiSession) closeEvents() {
	s.closeOnce.Do(func() {
		close(s.events)
	})
}

// ID returns the session identifier.
func (s *ohMyPiSession) ID() string {
	return s.id
}

// Wait blocks until the session completes (done or error).
func (s *ohMyPiSession) Wait(ctx context.Context) error {
	// Wait for the subprocess to exit
	done := make(chan error, 1)
	go func() {
		done <- s.cmd.Wait()
	}()

	select {
	case <-ctx.Done():
		// Context cancelled, abort the session
		s.Abort(ctx)
		return ctx.Err()
	case err := <-done:
		// Subprocess exited
		s.mu.Lock()
		defer s.mu.Unlock()

		// Close the log file
		if s.logFile != nil {
			s.logFile.Close()
			s.logFile = nil

			// Compress the final log segment
			compressedPath := s.logPath + "." + strconv.FormatInt(time.Now().Unix(), 10) + ".gz"
			go compressFile(s.logPath, compressedPath)
		}

		s.closeEvents()

		if err != nil {
			// Check if it was aborted
			if s.aborted {
				return nil // Graceful abort, not an error
			}
			return fmt.Errorf("bridge subprocess exited: %w", err)
		}

		return nil
	}
}

// Events returns a channel emitting agent events.
func (s *ohMyPiSession) Events() <-chan adapter.AgentEvent {
	return s.events
}

// SendMessage sends a message to the running agent.
// Used for foreman iteration and critique feedback.
func (s *ohMyPiSession) SendMessage(ctx context.Context, msg string) error {
	return s.sendMessage(msg)
}

// Abort terminates the agent session gracefully.
// Returns an error if the session cannot be aborted.
func (s *ohMyPiSession) Abort(ctx context.Context) error {
	s.mu.Lock()
	if s.aborted {
		s.mu.Unlock()
		return nil // Already aborted
	}
	s.aborted = true
	s.mu.Unlock()

	// Send abort message to bridge
	msg := bridgeMsg{Type: "abort"}
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal abort message: %w", err)
	}

	if _, err := s.stdin.Write(append(data, '\n')); err != nil {
		slog.Debug("failed to send abort message", "err", err)
	}

	// Give the subprocess time to exit gracefully
	timeout := time.After(5 * time.Second)
	done := make(chan error, 1)
	go func() {
		done <- s.cmd.Wait()
	}()

	select {
	case <-timeout:
		// Timeout, force kill
		if s.cmd.Process != nil {
			slog.Warn("bridge subprocess did not exit gracefully, sending SIGKILL")
			s.cmd.Process.Kill()
		}
	case <-done:
		// Subprocess exited gracefully
	}

	// Close stdin
	s.stdin.Close()

	// Close the log file
	s.mu.Lock()
	if s.logFile != nil {
		s.logFile.Close()
		s.logFile = nil
	}
	s.mu.Unlock()

	s.closeEvents()

	return nil
}

// sendPrompt sends a prompt message to the bridge.
func (s *ohMyPiSession) sendPrompt(text string) error {
	msg := bridgeMsg{Type: "prompt", Text: text}
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal prompt message: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := s.stdin.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write prompt: %w", err)
	}

	return nil
}

// sendMessage sends a message to the bridge.
func (s *ohMyPiSession) sendMessage(text string) error {
	msg := bridgeMsg{Type: "message", Text: text}
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := s.stdin.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write message: %w", err)
	}

	return nil
}

// SendAnswer sends an answer to resolve a pending ask_foreman tool call.
func (s *ohMyPiSession) SendAnswer(ctx context.Context, answer string) error {
	msg := bridgeMsg{Type: "answer", Text: answer}
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal answer message: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := s.stdin.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write answer: %w", err)
	}

	return nil
}

// readEvents reads JSON-line events from stdout and emits them to the events channel.
func (s *ohMyPiSession) readEvents() {
	scanner := bufio.NewScanner(s.stdout)
	scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024) // 10MB max line size

	for scanner.Scan() {
		line := scanner.Bytes()

		// Write to session log with timestamp
		s.mu.Lock()
		if s.logFile != nil {
			timestamp := time.Now().UTC().Format(time.RFC3339Nano)
			logEntry := fmt.Sprintf("%s %s\n", timestamp, string(line))
			if _, err := s.logFile.WriteString(logEntry); err != nil {
				slog.Warn("failed to write to session log", "err", err)
			}
			// Check for log rotation
			if info, err := s.logFile.Stat(); err == nil {
				if info.Size() >= 10*1024*1024 {
					s.rotateLogLocked()
				}
			}
		}
		s.mu.Unlock()

		// Parse the event
		var rawEvent struct {
			Type  string          `json:"type"`
			Event json.RawMessage `json:"event"`
		}
		if err := json.Unmarshal(line, &rawEvent); err != nil {
			slog.Warn("failed to parse bridge event", "line", string(line), "err", err)
			continue
		}

		// Map the event
		event, err := s.mapBridgeEvent(rawEvent)
		if err != nil {
			slog.Warn("failed to map bridge event", "err", err)
			continue
		}

		if event != nil {
			select {
			case s.events <- *event:
				// Event sent
			default:
				// Channel full, skip (should be rare)
				slog.Warn("event channel full, dropping event", "type", event.Type)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		slog.Error("error reading bridge stdout", "err", err)
	}
}

// mapBridgeEvent maps a bridge event to an adapter.AgentEvent.
func (s *ohMyPiSession) mapBridgeEvent(raw struct {
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
		return nil, fmt.Errorf("missing event type")
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
			return nil, fmt.Errorf("missing assistant_output text")
		}
		return &adapter.AgentEvent{
			Type:      "text_delta",
			Timestamp: time.Now(),
			Payload:   text,
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
			return nil, fmt.Errorf("missing question text")
		}
		context, _ := eventMap["context"].(string)
		return &adapter.AgentEvent{
			Type:      "question",
			Timestamp: time.Now(),
			Payload:   question,
			Metadata:  map[string]any{"context": context},
		}, nil

	case "foreman_proposed":
		text, ok := eventMap["text"].(string)
		if !ok {
			return nil, fmt.Errorf("missing foreman_proposed text")
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
				Payload:   firstNonEmpty(message, summary),
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
				Payload:   firstNonEmpty(message, summary, "unknown error"),
			}, nil
		default:
			return nil, nil
		}

	default:
		return nil, nil
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// readStderr reads stderr and logs it for debugging.
func (s *ohMyPiSession) readStderr() {
	scanner := bufio.NewScanner(s.stderr)
	for scanner.Scan() {
		slog.Debug("bridge stderr", "line", scanner.Text())
	}
}

// checkLogRotation checks if log rotation is needed.
// Deprecated: rotation is now handled inline in readEvents.
func (s *ohMyPiSession) checkLogRotation() {}

// rotateLogLocked performs log rotation.
// Must be called with s.mu held.
// The file swap (close old, open new) happens under the lock.
// Compression and segment cleanup are launched asynchronously so the lock
// is not held during I/O-intensive operations, which would block Abort.
func (s *ohMyPiSession) rotateLogLocked() {
	if s.logFile == nil {
		return
	}

	// Close the current log file before rotating.
	s.logFile.Close()
	s.logFile = nil

	// Rename the closed file to a stable temp name so the new active log
	// can be opened at s.logPath immediately (still under lock).
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	rotateSrc := s.logPath + "." + ts + ".rotating"
	compressedPath := s.logPath + "." + ts + ".gz"
	if err := os.Rename(s.logPath, rotateSrc); err != nil {
		slog.Warn("failed to rename log segment for rotation", "err", err)
		rotateSrc = "" // skip async compress; can't safely rename
	}

	// Open fresh log file (still under lock so writers don't see a nil logFile).
	newFile, err := os.OpenFile(s.logPath, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		slog.Error("failed to open new log file after rotation", "err", err)
		return
	}
	s.logFile = newFile

	if rotateSrc == "" {
		return // rename failed; skip compression
	}

	// Compress and clean up old segment outside the lock.
	logDir := s.logDir
	sessionID := s.id
	go func() {
		if err := compressFile(rotateSrc, compressedPath); err != nil {
			slog.Warn("failed to compress log segment", "err", err)
			return
		}
		cleanupOldSegments(logDir, sessionID)
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
	srcFile.Close() // read-only; ignore close error

	if err != nil {
		os.Remove(dst) // remove incomplete output
		return err
	}

	if err := os.Remove(src); err != nil {
		slog.Warn("failed to remove log file after compression", "path", src, "err", err)
	}
	return nil
}

// cleanupOldSegments removes old compressed segments, keeping max 5.
func cleanupOldSegments(logDir, sessionID string) {
	pattern := filepath.Join(logDir, sessionID+"*.log.*.gz")
	files, err := filepath.Glob(pattern)
	if err != nil {
		return
	}

	// Sort by modification time (oldest first)
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

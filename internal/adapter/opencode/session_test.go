package opencode

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/sessionlog"
)

func TestSession_ID(t *testing.T) {
	s := &session{
		id:     "test-session-id",
		events: make(chan adapter.AgentEvent, 1),
	}
	if got := s.ID(); got != "test-session-id" {
		t.Errorf("ID() = %q, want %q", got, "test-session-id")
	}
}

func TestSession_Events(t *testing.T) {
	s := &session{
		id:     "test-id",
		events: make(chan adapter.AgentEvent, 1),
	}
	ch := s.Events()
	if ch == nil {
		t.Fatal("Events() returned nil channel")
	}
	// Verify it's the underlying channel by sending a probe event.
	probe := adapter.AgentEvent{Type: "probe"}
	s.events <- probe
	got := <-ch
	if got.Type != "probe" {
		t.Errorf("received event Type = %q, want %q", got.Type, "probe")
	}
}

func TestSessionEmitEventBlocksTerminalEventsWhenChannelFull(t *testing.T) {
	t.Parallel()

	s := &session{events: make(chan adapter.AgentEvent, 1)}
	s.events <- adapter.AgentEvent{Type: "text_delta", Payload: "filler"}
	sent := make(chan struct{})
	go func() {
		defer close(sent)
		s.emitEvent(adapter.AgentEvent{Type: "question", Payload: "need input"})
	}()

	select {
	case <-sent:
		t.Fatal("terminal event send completed while channel was full")
	case <-time.After(10 * time.Millisecond):
	}

	<-s.events
	select {
	case <-sent:
	case <-time.After(time.Second):
		t.Fatal("terminal event send did not complete after draining channel")
	}
	if got := <-s.events; got.Type != "question" {
		t.Fatalf("event type = %q, want question", got.Type)
	}
}

func TestSession_SteerNotSupported(t *testing.T) {
	s := &session{
		id:     "test-id",
		events: make(chan adapter.AgentEvent, 1),
	}
	err := s.Steer(context.Background(), "some direction")
	if !errors.Is(err, adapter.ErrSteerNotSupported) {
		t.Errorf("Steer() error = %v, want ErrSteerNotSupported", err)
	}
}

func TestSession_SendAnswer_NoPendingQuestion(t *testing.T) {
	s := &session{
		id:                "test-id",
		events:            make(chan adapter.AgentEvent, 1),
		pendingQuestionID: "",
	}
	err := s.SendAnswer(context.Background(), "my answer")
	if err == nil {
		t.Fatal("expected error when no pending question, got nil")
	}
	if !contains(err.Error(), "no pending question") {
		t.Errorf("error should mention 'no pending question', got: %v", err)
	}
}

func TestSession_ResumeInfo_Empty(t *testing.T) {
	s := &session{
		id:         "test-id",
		events:     make(chan adapter.AgentEvent, 1),
		openCodeID: "",
	}
	info := s.ResumeInfo()
	if info != nil {
		t.Errorf("ResumeInfo() = %v, want nil", info)
	}
}

func TestSession_ResumeInfo_Set(t *testing.T) {
	s := &session{
		id:         "test-id",
		events:     make(chan adapter.AgentEvent, 1),
		openCodeID: "",
	}
	s.setOpenCodeID("oc-session-abc")
	info := s.ResumeInfo()
	if info == nil {
		t.Fatal("ResumeInfo() returned nil after setting openCodeID")
	}
	if got := info["opencode_session_id"]; got != "oc-session-abc" {
		t.Errorf("ResumeInfo()[\"opencode_session_id\"] = %q, want %q", got, "oc-session-abc")
	}
}

func TestSession_Wait_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	s := &session{
		id:       "test-id",
		events:   make(chan adapter.AgentEvent, 1),
		waitDone: make(chan struct{}),
	}
	err := s.Wait(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Wait() error = %v, want context.Canceled", err)
	}
}

// contains is a simple helper to avoid importing strings for one use.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestSessionWriteInputLogSeparatesSessionContextAndPrompt(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "session.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open log: %v", err)
	}
	s := &session{logFile: logFile}
	s.writeInputLog("session_context", "system")
	s.writeInputLog("prompt", "user")
	if err := logFile.Close(); err != nil {
		t.Fatalf("close log: %v", err)
	}
	entries, err := sessionlog.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %+v, want 2", entries)
	}
	if entries[0].InputKind != "session_context" || entries[0].Text != "system" {
		t.Fatalf("first entry = %+v, want session context", entries[0])
	}
	if entries[1].InputKind != "prompt" || entries[1].Text != "user" {
		t.Fatalf("second entry = %+v, want prompt", entries[1])
	}
}

func TestSession_VariantCarried(t *testing.T) {
	// Variant set on session is exposed through the struct field.
	s := &session{
		id:      "test-id",
		events:  make(chan adapter.AgentEvent, 1),
		variant: "high",
	}
	if s.variant != "high" {
		t.Errorf("variant = %q, want %q", s.variant, "high")
	}
}

func TestSession_VariantEmpty(t *testing.T) {
	// Empty variant leaves the field at zero value; SendMessage will omit it via omitempty.
	s := &session{
		id:     "test-id",
		events: make(chan adapter.AgentEvent, 1),
	}
	if s.variant != "" {
		t.Errorf("variant = %q, want empty string", s.variant)
	}
}

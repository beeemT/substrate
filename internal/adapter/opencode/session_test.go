package opencode

import (
	"context"
	"errors"
	"testing"

	"github.com/beeemT/substrate/internal/adapter"
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

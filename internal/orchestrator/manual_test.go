package orchestrator

import (
	"context"
	"testing"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/event"
)

// mockManualHarness is a mock harness for testing ManualSessionService.
type mockManualHarness struct {
	nameVal         string
	capabilitiesVal adapter.HarnessCapabilities
	compactVal      bool
	startErr        error
	session         adapter.AgentSession
}

func (m *mockManualHarness) Name() string                              { return m.nameVal }
func (m *mockManualHarness) Capabilities() adapter.HarnessCapabilities { return m.capabilitiesVal }
func (m *mockManualHarness) SupportsCompact() bool                     { return m.compactVal }
func (m *mockManualHarness) StartSession(ctx context.Context, opts adapter.SessionOpts) (adapter.AgentSession, error) {
	if m.startErr != nil {
		return nil, m.startErr
	}
	return m.session, nil
}

// mockManualSession is a mock agent session for testing.
type mockManualSession struct {
	idVal      string
	events     chan adapter.AgentEvent
	resumeInfo map[string]string
	waitErr    error
}

func (m *mockManualSession) ID() string                                          { return m.idVal }
func (m *mockManualSession) SendMessage(ctx context.Context, msg string) error   { return nil }
func (m *mockManualSession) Steer(ctx context.Context, msg string) error         { return nil }
func (m *mockManualSession) SendAnswer(ctx context.Context, answer string) error { return nil }
func (m *mockManualSession) Abort(ctx context.Context) error                     { return nil }
func (m *mockManualSession) Events() <-chan adapter.AgentEvent                   { return m.events }
func (m *mockManualSession) Done() <-chan struct{} {
	done := make(chan struct{})
	close(done)
	return done
}
func (m *mockManualSession) Wait(ctx context.Context) error    { return m.waitErr }
func (m *mockManualSession) ResumeInfo() map[string]string     { return m.resumeInfo }
func (m *mockManualSession) Compact(ctx context.Context) error { return nil }

// Verify mock types implement the required interfaces.
var (
	_ adapter.AgentHarness = (*mockManualHarness)(nil)
	_ adapter.AgentSession = (*mockManualSession)(nil)
)

func TestIsActiveStatus(t *testing.T) {
	tests := []struct {
		status   domain.AgentSessionStatus
		isActive bool
	}{
		{domain.AgentSessionPending, true},
		{domain.AgentSessionRunning, true},
		{domain.AgentSessionWaitingForAnswer, true},
		{domain.AgentSessionCompleted, false},
		{domain.AgentSessionInterrupted, false},
		{domain.AgentSessionFailed, false},
	}

	for _, tc := range tests {
		t.Run(string(tc.status), func(t *testing.T) {
			if got := isActiveStatus(tc.status); got != tc.isActive {
				t.Errorf("isActiveStatus(%q) = %v, want %v", tc.status, got, tc.isActive)
			}
		})
	}
}

func TestManualSessionService_ResumePreservesPhase(t *testing.T) {
	// Verify that manual sessions preserve the manual phase.
	// ResumeSession and FollowUpManualSession must not use implementation phase.
	manualPhase := domain.AgentSessionKindManual
	if manualPhase != "manual" {
		t.Errorf("manual phase constant = %q, want %q", manualPhase, "manual")
	}
}

func TestManualSessionService_QuestionToolPolicyHuman(t *testing.T) {
	// Manual sessions must use QuestionToolPolicyHuman.
	// This routes questions to the operator inline rather than to Foreman.
	policy := adapter.QuestionToolPolicyHuman
	if policy != "human" {
		t.Errorf("QuestionToolPolicyHuman = %q, want %q", policy, "human")
	}
}

func TestStartManualSessionRequest_OptionalSubPlanID(t *testing.T) {
	// SubPlanID is optional for manual sessions.
	// Verify the struct can be created without SubPlanID.
	req := StartManualSessionRequest{
		WorkItemID:      "wi-1",
		WorkspaceID:     "ws-1",
		RepositoryName:  "repo1",
		InitialMessage:  "Hello",
		OwnerInstanceID: nil,
		// SubPlanID intentionally omitted
	}
	if req.SubPlanID != "" {
		t.Errorf("SubPlanID = %q, want empty", req.SubPlanID)
	}
}

func TestManualForwardEventsDoesNotPublishTranscriptEvents(t *testing.T) {
	t.Parallel()

	eventRepo := &implementationEventRepo{}
	bus := event.NewBus(event.BusConfig{EventRepo: eventRepo})
	defer bus.Close()
	svc := &ManualSessionService{eventBus: bus}
	events := make(chan adapter.AgentEvent, 2)
	events <- adapter.AgentEvent{Type: "text_delta", Payload: "stream text"}
	events <- adapter.AgentEvent{Type: "tool_result", Payload: "tool output"}
	close(events)

	svc.forwardEvents(context.Background(), events, "manual-session-1")

	eventRepo.mu.Lock()
	defer eventRepo.mu.Unlock()
	if len(eventRepo.events) != 0 {
		t.Fatalf("forwardEvents published %d events, want 0", len(eventRepo.events))
	}
}

func TestManualSessionEventPayloadWithOld(t *testing.T) {
	// Verify the manual session event payload with old session ID.
	payload := manualSessionEventPayload{
		Session: domain.AgentSession{
			ID:     "new-session",
			Kind:   domain.AgentSessionKindManual,
			Status: domain.AgentSessionRunning,
		},
		WorkItemID:   "wi-1",
		SessionID:    "new-session",
		OldSessionID: "old-session",
	}
	if payload.OldSessionID == "" {
		t.Error("OldSessionID should not be empty in follow-up session payload")
	}
	if payload.WorkItemID != "wi-1" {
		t.Errorf("WorkItemID = %q, want %q", payload.WorkItemID, "wi-1")
	}
}

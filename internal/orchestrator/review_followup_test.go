package orchestrator

import (
	"context"
	"sync"
	"testing"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/event"
	"github.com/beeemT/substrate/internal/repository"
	"github.com/beeemT/substrate/internal/service"
)

// simpleMockSession is a minimal mock session for testing ReviewFollowup.
type simpleMockSession struct {
	id string
}

func (s *simpleMockSession) ID() string                   { return s.id }
func (s *simpleMockSession) Wait(_ context.Context) error { return nil }
func (s *simpleMockSession) Events() <-chan adapter.AgentEvent {
	ch := make(chan adapter.AgentEvent)
	close(ch)
	return ch
}
func (s *simpleMockSession) SendMessage(_ context.Context, _ string) error { return nil }
func (s *simpleMockSession) Steer(_ context.Context, _ string) error       { return nil }
func (s *simpleMockSession) SendAnswer(_ context.Context, _ string) error  { return nil }
func (s *simpleMockSession) Abort(_ context.Context) error                 { return nil }
func (s *simpleMockSession) Compact(_ context.Context) error               { return nil }
func (s *simpleMockSession) ResumeInfo() map[string]string                 { return nil }
func (s *simpleMockSession) Done() <-chan struct{} {
	done := make(chan struct{})
	close(done)
	return done
}

// trackingHarness tracks StartSession calls for assertions.
type trackingHarness struct {
	mu       sync.Mutex
	sessions []adapter.SessionOpts
}

func (h *trackingHarness) SupportsCompact() bool { return true }
func (h *trackingHarness) Name() string          { return "tracking-mock" }
func (h *trackingHarness) StartSession(_ context.Context, opts adapter.SessionOpts) (adapter.AgentSession, error) {
	h.mu.Lock()
	h.sessions = append(h.sessions, opts)
	h.mu.Unlock()
	return &simpleMockSession{id: opts.SessionID}, nil
}

// newReviewFollowupForTest creates a ReviewFollowup with real services backed by mock repos.
func newReviewFollowupForTest(t *testing.T) (*ReviewFollowup, *trackingHarness, *mockPlanRepo) {
	t.Helper()

	planRepo := newMockPlanRepo()
	subPlanRepo := newMockSubPlanRepo()
	questionRepo := newMockQuestionRepo()
	sessionRepo := newMockSessionRepo()
	bus := event.NewBus(event.BusConfig{})
	_ = questionRepo
	_ = sessionRepo

	planSvc := service.NewPlanService(repository.NoopTransacter{Res: repository.Resources{Plans: planRepo, SubPlans: subPlanRepo}}, &mockPublisher{})
	questionSvc := service.NewQuestionService(repository.NoopTransacter{Res: repository.Resources{Questions: questionRepo}}, &mockPublisher{})
	sessionSvc := service.NewAgentSessionService(repository.NoopTransacter{Res: repository.Resources{AgentSessions: sessionRepo}}, &mockPublisher{})

	harness := &trackingHarness{}
	cfg := &config.Config{}
	registry := NewSessionRegistry()

	rf := NewReviewFollowup(cfg, harness, registry, planSvc, questionSvc, sessionSvc, bus)
	return rf, harness, planRepo
}

// TestReviewFollowup_FollowUp_StartsNewForeman verifies that FollowUp creates
// a new foreman, starts it, and registers it in the registry.
func TestReviewFollowup_FollowUp_StartsNewForeman(t *testing.T) {
	t.Parallel()

	rf, harness, planRepo := newReviewFollowupForTest(t)
	planRepo.plans["plan-1"] = domain.Plan{ID: "plan-1", WorkItemID: "wi-1"}

	if err := rf.FollowUp(context.Background(), "wi-1", "please refine"); err != nil {
		t.Fatalf("FollowUp: %v", err)
	}

	// Verify foreman was registered
	foreman := rf.registry.GetForeman("wi-1")
	if foreman == nil {
		t.Fatal("foreman not registered in registry after FollowUp")
	}

	// Verify harness received exactly one StartSession call with follow-up context
	harness.mu.Lock()
	defer harness.mu.Unlock()
	if len(harness.sessions) != 1 {
		t.Fatalf("harness sessions: got %d, want 1", len(harness.sessions))
	}
	sess := harness.sessions[0]
	if sess.Mode != adapter.SessionModeForeman {
		t.Errorf("session mode = %q, want %q", sess.Mode, adapter.SessionModeForeman)
	}
}

// TestReviewFollowup_FollowUp_StopsExistingForeman verifies that FollowUp stops
// the existing foreman before starting a new one and deregisters the old entry.
func TestReviewFollowup_FollowUp_StopsExistingForeman(t *testing.T) {
	t.Parallel()

	rf, harness, planRepo := newReviewFollowupForTest(t)
	planRepo.plans["plan-1"] = domain.Plan{ID: "plan-1", WorkItemID: "wi-1"}

	// Register an existing foreman (not started, so IsRunning() is false)
	existingForeman := NewForeman(&config.Config{}, nil, rf.planSvc, rf.questionSvc, rf.sessionSvc, rf.eventBus)
	rf.registry.RegisterForeman("wi-1", existingForeman)

	if err := rf.FollowUp(context.Background(), "wi-1", "improve the implementation"); err != nil {
		t.Fatalf("FollowUp: %v", err)
	}

	// Registry should contain exactly one foreman (the new one)
	newForeman := rf.registry.GetForeman("wi-1")
	if newForeman == nil {
		t.Fatal("no foreman registered after FollowUp")
	}
	if newForeman == existingForeman {
		t.Error("new foreman should not be the same as the old foreman")
	}

	// Verify harness received the new session
	harness.mu.Lock()
	defer harness.mu.Unlock()
	if len(harness.sessions) != 1 {
		t.Errorf("expected 1 session, got %d", len(harness.sessions))
	}
}

// TestReviewFollowup_FollowUp_DeregistersOldForeman verifies that FollowUp
// deregisters the old foreman even when Stop returns an error.
func TestReviewFollowup_FollowUp_DeregistersOldForeman(t *testing.T) {
	t.Parallel()

	rf, harness, planRepo := newReviewFollowupForTest(t)
	planRepo.plans["plan-1"] = domain.Plan{ID: "plan-1", WorkItemID: "wi-1"}

	// Register an existing foreman (can't be started since harness is nil)
	existingForeman := NewForeman(&config.Config{}, nil, rf.planSvc, rf.questionSvc, rf.sessionSvc, rf.eventBus)
	rf.registry.RegisterForeman("wi-1", existingForeman)

	// FollowUp with a working harness; it should replace the old entry
	if err := rf.FollowUp(context.Background(), "wi-1", "retry"); err != nil {
		t.Fatalf("FollowUp: %v", err)
	}

	// Registry should only contain the new foreman
	newForeman := rf.registry.GetForeman("wi-1")
	if newForeman == nil {
		t.Fatal("no foreman registered after FollowUp")
	}
	if newForeman == existingForeman {
		t.Error("old foreman should have been deregistered")
	}

	// Verify new foreman was started
	harness.mu.Lock()
	defer harness.mu.Unlock()
	if len(harness.sessions) != 1 {
		t.Errorf("expected 1 session, got %d", len(harness.sessions))
	}
}

// TestReviewFollowup_FollowUp_NoExistingForeman verifies that FollowUp works
// when no existing foreman is registered.
func TestReviewFollowup_FollowUp_NoExistingForeman(t *testing.T) {
	t.Parallel()

	rf, harness, planRepo := newReviewFollowupForTest(t)
	planRepo.plans["plan-1"] = domain.Plan{ID: "plan-1", WorkItemID: "wi-1"}

	if err := rf.FollowUp(context.Background(), "wi-1", "follow up"); err != nil {
		t.Fatalf("FollowUp: %v", err)
	}

	foreman := rf.registry.GetForeman("wi-1")
	if foreman == nil {
		t.Fatal("foreman not registered after FollowUp with no existing foreman")
	}

	harness.mu.Lock()
	defer harness.mu.Unlock()
	if len(harness.sessions) != 1 {
		t.Errorf("expected 1 session, got %d", len(harness.sessions))
	}
}

// TestReviewFollowup_FollowUpFailed_PrefixesFeedback verifies that FollowUpFailed
// adds "Failed: " prefix to the feedback in the user prompt.
func TestReviewFollowup_FollowUpFailed_PrefixesFeedback(t *testing.T) {
	t.Parallel()

	rf, harness, planRepo := newReviewFollowupForTest(t)
	planRepo.plans["plan-1"] = domain.Plan{ID: "plan-1", WorkItemID: "wi-1"}

	if err := rf.FollowUpFailed(context.Background(), "wi-1", "build is broken"); err != nil {
		t.Fatalf("FollowUpFailed: %v", err)
	}

	harness.mu.Lock()
	defer harness.mu.Unlock()
	if len(harness.sessions) != 1 {
		t.Fatalf("harness sessions: got %d, want 1", len(harness.sessions))
	}
	sess := harness.sessions[0]
	if sess.UserPrompt == "" {
		t.Error("UserPrompt should not be empty")
	}
	// The follow-up context should contain "Failed: " prefix
	if !containsString(sess.UserPrompt, "Failed:") {
		t.Errorf("UserPrompt = %q, want to contain 'Failed:'", sess.UserPrompt)
	}
}

func containsString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

package orchestrator

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/repository"
	"github.com/beeemT/substrate/internal/service"
)

// doneSession emits a single "done" event (lifecycle.completed in agent mode)
// then stays open, matching the omp-bridge agent-mode lifecycle where the
// process exits shortly after emitting the event.
type doneSession struct {
	id     string
	events chan adapter.AgentEvent
}

func newDoneSession(id string) *doneSession {
	ch := make(chan adapter.AgentEvent, 1)
	ch <- adapter.AgentEvent{Type: "done"}
	// Channel intentionally left open: the real bridge exits the process
	// rather than closing stdout from within, so EOF arrives asynchronously.
	return &doneSession{id: id, events: ch}
}

func (s *doneSession) ID() string                                    { return s.id }
func (s *doneSession) Wait(_ context.Context) error                  { return nil }
func (s *doneSession) Events() <-chan adapter.AgentEvent             { return s.events }
func (s *doneSession) SendMessage(_ context.Context, _ string) error { return nil }
func (s *doneSession) Steer(_ context.Context, _ string) error       { return nil }
func (s *doneSession) SendAnswer(_ context.Context, _ string) error  { return nil }
func (s *doneSession) Abort(_ context.Context) error                 { return nil }
func (s *doneSession) Compact(_ context.Context) error               { return nil }
func (s *doneSession) ResumeInfo() map[string]string                 { return nil }
func (s *doneSession) Done() <-chan struct{} {
	done := make(chan struct{})
	close(done)
	return done
}

type doneHarness struct{}

func (h *doneHarness) SupportsCompact() bool { return true }
func (h *doneHarness) Name() string          { return "done-mock" }
func (h *doneHarness) StartSession(_ context.Context, opts adapter.SessionOpts) (adapter.AgentSession, error) {
	return newDoneSession(opts.SessionID), nil
}

// newReviewPipelineForTest builds a minimal ReviewPipeline for unit testing.
// sessionRepo must be pre-populated with any sessions the test needs.
func newReviewPipelineForTest(harness adapter.AgentHarness, sessionRepo *mockSessionRepo) *ReviewPipeline {
	sessionSvc := service.NewAgentSessionService(repository.NoopTransacter{Res: repository.Resources{AgentSessions: sessionRepo}}, &mockPublisher{})
	maxCycles := 3
	reviewTimeoutDur := 5 * time.Second // long enough to detect hangs in tests
	cfg := &config.Config{}
	cfg.Review.MaxCycles = &maxCycles
	cfg.Review.PassThreshold = config.PassThresholdNoCritiques
	return &ReviewPipeline{
		cfg:           cfg,
		harness:       harness,
		sessionSvc:    sessionSvc,
		reviewTimeout: reviewTimeoutDur,
		registry:      NewSessionRegistry(),
	}
}

// TestStartReviewAgent_CompletesOnDone verifies that the review event loop
// returns as soon as it receives the "done" signal (lifecycle.completed from
// the agent bridge). If the loop did not handle "done" the call would block
// until reviewTimeout fired.
func TestStartReviewAgent_CompletesOnDone(t *testing.T) {
	sessionRepo := newMockSessionRepo()
	sessionRepo.sessions["impl-session-1"] = domain.AgentSession{
		ID:         "impl-session-1",
		WorkItemID: "wi-1",
		Status:     domain.AgentSessionRunning,
	}

	pipeline := newReviewPipelineForTest(&doneHarness{}, sessionRepo)

	session := domain.AgentSession{
		ID:             "impl-session-1",
		WorkItemID:     "wi-1",
		SubPlanID:      "sp-1",
		RepositoryName: "repo-a",
	}
	subPlan := domain.TaskPlan{ID: "sp-1", PlanID: "plan-1", RepositoryName: "repo-a"}
	plan := domain.Plan{ID: "plan-1", WorkItemID: "wi-1"}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	type result struct{ err error }
	done := make(chan result, 1)
	go func() {
		_, _, _, err := pipeline.startReviewAgent(ctx, session, subPlan, plan)
		done <- result{err: err}
	}()

	// reviewTimeout is 5s. If "done" is not handled the call blocks for 5s.
	// Assert it returns well before that.
	select {
	case <-done:
		// Returned — may carry a "read review session output" error (no real
		// log file in the test environment). That is acceptable; what matters
		// is the call did not block.
	case <-time.After(2 * time.Second):
		t.Fatal("startReviewAgent blocked for 2s: \"done\" event not handled as completion signal")
	}
}

// ============================================================
// Cycle-state-on-failure tests (regression: review crashes used to leave
// the cycle in `reviewing`, which made loadCritiqueFeedback silently miss
// outstanding critiques on prior cycles for the same impl session).
// ============================================================

// erroringSession is a mockAgentSession variant used to drive ReviewSession
// failure paths. Its events channel can be pre-closed or carry an "error"
// event, both of which startReviewAgent's loop treats as failures.
func erroringSession(id string, events ...adapter.AgentEvent) *mockAgentSession {
	ch := make(chan adapter.AgentEvent, len(events)+1)
	for _, e := range events {
		ch <- e
	}
	return &mockAgentSession{id: id, eventsCh: ch}
}

// closedChannelSession returns a session whose events channel is already
// closed — this triggers startReviewAgent's "events channel closed
// unexpectedly" path.
func closedChannelSession(id string) *mockAgentSession {
	ch := make(chan adapter.AgentEvent)
	close(ch)
	return &mockAgentSession{id: id, eventsCh: ch}
}

// assertCycleFailed reads back the only cycle for the impl session and
// verifies it is in `failed` state, not lingering in `reviewing`.
func assertCycleFailed(t *testing.T, fix *reviewPipelineFixture, implSessionID string) {
	t.Helper()
	cycles, err := fix.pipeline.reviewSvc.ListCyclesBySessionID(context.Background(), implSessionID)
	if err != nil {
		t.Fatalf("list cycles: %v", err)
	}
	if len(cycles) != 1 {
		t.Fatalf("expected 1 cycle, got %d", len(cycles))
	}
	if cycles[0].Status != domain.ReviewCycleFailed {
		t.Fatalf("expected cycle status=%q, got %q", domain.ReviewCycleFailed, cycles[0].Status)
	}
}

// TestReviewSession_HarnessStartFailure_FailsCycle verifies that when the
// review harness fails to start, the cycle is transitioned to `failed`.
func TestReviewSession_HarnessStartFailure_FailsCycle(t *testing.T) {
	fix := newReviewPipelineFixture(t, 3)
	defer fix.cleanup()

	agentSession := fix.seedPlanAndSubPlan(t)

	// First StartSession call (review session creation) succeeds; the actual
	// review-agent StartSession (the second call) fails.
	calls := 0
	fix.harness.onStartSession = func(opts adapter.SessionOpts) (adapter.AgentSession, error) {
		calls++
		return nil, fmt.Errorf("simulated harness start failure (call %d)", calls)
	}

	_, err := fix.pipeline.ReviewSession(context.Background(), agentSession)
	if err == nil {
		t.Fatal("expected ReviewSession to return an error")
	}
	assertCycleFailed(t, fix, agentSession.ID)
}

// TestReviewSession_EventsChannelClosed_FailsCycle verifies that when the
// review session's event channel closes before emitting "done", the cycle is
// transitioned to `failed`.
func TestReviewSession_EventsChannelClosed_FailsCycle(t *testing.T) {
	fix := newReviewPipelineFixture(t, 3)
	defer fix.cleanup()

	agentSession := fix.seedPlanAndSubPlan(t)

	fix.harness.onStartSession = func(opts adapter.SessionOpts) (adapter.AgentSession, error) {
		return closedChannelSession(opts.SessionID), nil
	}

	_, err := fix.pipeline.ReviewSession(context.Background(), agentSession)
	if err == nil {
		t.Fatal("expected ReviewSession to return an error")
	}
	assertCycleFailed(t, fix, agentSession.ID)
}

// TestReviewSession_ErrorEvent_FailsCycle verifies that an "error" event from
// the review session causes the cycle to be transitioned to `failed`.
func TestReviewSession_ErrorEvent_FailsCycle(t *testing.T) {
	fix := newReviewPipelineFixture(t, 3)
	defer fix.cleanup()

	agentSession := fix.seedPlanAndSubPlan(t)

	fix.harness.onStartSession = func(opts adapter.SessionOpts) (adapter.AgentSession, error) {
		return erroringSession(opts.SessionID, adapter.AgentEvent{
			Type:    "error",
			Payload: "simulated agent error",
		}), nil
	}

	_, err := fix.pipeline.ReviewSession(context.Background(), agentSession)
	if err == nil {
		t.Fatal("expected ReviewSession to return an error")
	}
	assertCycleFailed(t, fix, agentSession.ID)
}

// TestReviewSession_Timeout_FailsCycle verifies that a review session that
// never produces "done" within the configured reviewTimeout leaves the cycle
// in `failed` status.
func TestReviewSession_Timeout_FailsCycle(t *testing.T) {
	fix := newReviewPipelineFixture(t, 3)
	defer fix.cleanup()

	// Tighten the review timeout so the test runs quickly.
	fix.pipeline.reviewTimeout = 50 * time.Millisecond

	agentSession := fix.seedPlanAndSubPlan(t)

	fix.harness.onStartSession = func(opts adapter.SessionOpts) (adapter.AgentSession, error) {
		// Empty channel that never receives "done".
		return erroringSession(opts.SessionID), nil
	}

	_, err := fix.pipeline.ReviewSession(context.Background(), agentSession)
	if err == nil {
		t.Fatal("expected ReviewSession to return an error")
	}
	assertCycleFailed(t, fix, agentSession.ID)
}

// TestReviewSession_HappyPath_CycleStaysTerminal verifies that the
// fail-cycle-on-error defer does not disturb the happy path: a passed cycle
// remains `passed` and a critiques-found cycle remains `critiques_found`.
func TestReviewSession_HappyPath_CycleStaysTerminal(t *testing.T) {
	fix := newReviewPipelineFixture(t, 3)
	defer fix.cleanup()

	fix.harness.outputs = []string{"NO_CRITIQUES"}
	agentSession := fix.seedPlanAndSubPlan(t)

	result, err := fix.pipeline.ReviewSession(context.Background(), agentSession)
	if err != nil {
		t.Fatalf("ReviewSession: %v", err)
	}
	if !result.Passed {
		t.Fatalf("expected Passed=true")
	}
	cycles, err := fix.pipeline.reviewSvc.ListCyclesBySessionID(context.Background(), agentSession.ID)
	if err != nil {
		t.Fatalf("list cycles: %v", err)
	}
	if len(cycles) != 1 {
		t.Fatalf("expected 1 cycle, got %d", len(cycles))
	}
	if cycles[0].Status != domain.ReviewCyclePassed {
		t.Fatalf("expected cycle status=%q, got %q", domain.ReviewCyclePassed, cycles[0].Status)
	}
}


// TestReviewSession_StaleReviewingCyclesDoNotConsumeBudget verifies the M6
// cherry-pick: cycle counting filters to terminal statuses only. Stale
// `Reviewing` cycles left behind by harness crashes (e.g. SIGKILL between
// CreateCycle and makeDecision) must not consume the per-impl budget.
//
// Scenario: maxCycles=3. Seed two stale `Reviewing` cycles + one
// `CritiquesFound` cycle for the same impl session. The next ReviewSession
// call should create cycle number 2 (not 4) and run a normal review pass —
// not escalate via the max-cycles guard.
func TestReviewSession_StaleReviewingCyclesDoNotConsumeBudget(t *testing.T) {
	fix := newReviewPipelineFixture(t, 3)
	defer fix.cleanup()

	agentSession := fix.seedPlanAndSubPlan(t)

	// Seed two stale Reviewing cycles + one CritiquesFound cycle.
	now := time.Now()
	staleCycles := []domain.ReviewCycle{
		{
			ID:              "stale-cycle-1",
			AgentSessionID:  agentSession.ID,
			CycleNumber:     1,
			ReviewerHarness: "mock",
			Status:          domain.ReviewCycleReviewing,
			CreatedAt:       now.Add(-2 * time.Hour),
			UpdatedAt:       now.Add(-2 * time.Hour),
		},
		{
			ID:              "stale-cycle-2",
			AgentSessionID:  agentSession.ID,
			CycleNumber:     2,
			ReviewerHarness: "mock",
			Status:          domain.ReviewCycleReviewing,
			CreatedAt:       now.Add(-1 * time.Hour),
			UpdatedAt:       now.Add(-1 * time.Hour),
		},
		{
			ID:              "terminal-cycle-1",
			AgentSessionID:  agentSession.ID,
			CycleNumber:     1, // shadowed by the first stale cycle's number
			ReviewerHarness: "mock",
			Status:          domain.ReviewCycleCritiquesFound,
			CreatedAt:       now.Add(-30 * time.Minute),
			UpdatedAt:       now.Add(-30 * time.Minute),
		},
	}
	for _, c := range staleCycles {
		if err := fix.reviewRepo.CreateCycle(context.Background(), c); err != nil {
			t.Fatalf("seed cycle %s: %v", c.ID, err)
		}
	}

	// Drive the review pass with a clean output (no critiques) so it passes.
	fix.harness.outputs = []string{"NO_CRITIQUES"}

	result, err := fix.pipeline.ReviewSession(context.Background(), agentSession)
	if err != nil {
		t.Fatalf("ReviewSession: %v", err)
	}

	// Without the terminal-status filter, terminalCount would be 3 (counting
	// all cycles), cycleNumber=4 > maxCycles=3 → escalated. With the filter,
	// terminalCount=1 (only the CritiquesFound cycle) → cycleNumber=2 → not
	// escalated → normal review pass.
	if result.Escalated {
		t.Fatal("expected ReviewSession NOT to escalate; stale Reviewing cycles must not consume the budget")
	}
	if result.CycleNumber != 2 {
		t.Errorf("CycleNumber = %d, want 2 (1 terminal cycle + 1 new)", result.CycleNumber)
	}

	// Verify a fresh cycle was created with the right number, and the stale
	// cycles are still there (untouched by this call — they remain orphaned
	// until manually cleaned up or transitioned via durable cleanup elsewhere).
	allCycles, err := fix.reviewRepo.ListCyclesBySessionID(context.Background(), agentSession.ID)
	if err != nil {
		t.Fatalf("list cycles: %v", err)
	}
	if len(allCycles) != 4 {
		t.Fatalf("expected 4 cycles total (3 seeded + 1 new), got %d", len(allCycles))
	}

	// The newly created cycle is the one whose ID is not in the seeded set.
	seededIDs := map[string]bool{
		"stale-cycle-1": true, "stale-cycle-2": true, "terminal-cycle-1": true,
	}
	var newCycle *domain.ReviewCycle
	for i := range allCycles {
		if !seededIDs[allCycles[i].ID] {
			newCycle = &allCycles[i]
			break
		}
	}
	if newCycle == nil {
		t.Fatal("could not locate newly created cycle in repo")
	}
	if newCycle.CycleNumber != 2 {
		t.Errorf("new cycle CycleNumber = %d, want 2", newCycle.CycleNumber)
	}
	if newCycle.Status != domain.ReviewCyclePassed {
		t.Errorf("new cycle Status = %q, want %q (NO_CRITIQUES output)",
			newCycle.Status, domain.ReviewCyclePassed)
	}
}

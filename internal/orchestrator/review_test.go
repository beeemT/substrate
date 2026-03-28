package orchestrator

import (
	"context"
	"testing"
	"time"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/repository"
	"github.com/beeemT/substrate/internal/service"
)

// foremanProposedSession emits a single foreman_proposed event then stays open,
// matching the omp-bridge foreman-mode lifecycle (no process exit).
type foremanProposedSession struct {
	id     string
	events chan adapter.AgentEvent
}

func newForemanProposedSession(id, output string) *foremanProposedSession {
	ch := make(chan adapter.AgentEvent, 1)
	ch <- adapter.AgentEvent{Type: "foreman_proposed", Payload: output}
	// Intentionally NOT closed: bridge stays alive after foreman_proposed.
	return &foremanProposedSession{id: id, events: ch}
}

func (s *foremanProposedSession) ID() string                                    { return s.id }
func (s *foremanProposedSession) Wait(_ context.Context) error                  { return nil }
func (s *foremanProposedSession) Events() <-chan adapter.AgentEvent             { return s.events }
func (s *foremanProposedSession) SendMessage(_ context.Context, _ string) error { return nil }
func (s *foremanProposedSession) Steer(_ context.Context, _ string) error       { return nil }
func (s *foremanProposedSession) Abort(_ context.Context) error                 { return nil }

type foremanProposedHarness struct{ output string }

func (h *foremanProposedHarness) Name() string { return "foreman-proposed-mock" }
func (h *foremanProposedHarness) StartSession(_ context.Context, opts adapter.SessionOpts) (adapter.AgentSession, error) {
	return newForemanProposedSession(opts.SessionID, h.output), nil
}

// newReviewPipelineForTest builds a minimal ReviewPipeline for unit testing.
// sessionRepo must be pre-populated with any sessions the test needs.
func newReviewPipelineForTest(harness adapter.AgentHarness, sessionRepo *mockSessionRepo) *ReviewPipeline {
	sessionSvc := service.NewTaskService(repository.NoopTransacter{Res: repository.Resources{Tasks: sessionRepo}})
	maxCycles := 3
	maxParseRetries := 2
	reviewTimeoutDur := 5 * time.Second // long enough to detect hangs in tests
	cfg := &config.Config{}
	cfg.Review.MaxCycles = &maxCycles
	cfg.Review.PassThreshold = config.PassThresholdNoCritiques
	cfg.Plan.MaxParseRetries = &maxParseRetries
	return &ReviewPipeline{
		cfg:           cfg,
		harness:       harness,
		sessionSvc:    sessionSvc,
		reviewTimeout: reviewTimeoutDur,
	}
}

// TestStartReviewAgent_CompletesOnForemanProposed verifies that the review event
// loop treats "foreman_proposed" as the session-done signal. Previously the loop
// only handled "done" (lifecycle.completed), so it would block until reviewTimeout
// when the omp-bridge ran in foreman mode (which emits foreman_proposed, not done).
func TestStartReviewAgent_CompletesOnForemanProposed(t *testing.T) {
	sessionRepo := newMockSessionRepo()
	// Seed a running implementation session so Create/Start on the review
	// session have a valid WorkItemID to reference.
	sessionRepo.sessions["impl-session-1"] = domain.Task{
		ID:         "impl-session-1",
		WorkItemID: "wi-1",
		Status:     domain.AgentSessionRunning,
	}

	pipeline := newReviewPipelineForTest(
		&foremanProposedHarness{output: "NO_CRITIQUES"},
		sessionRepo,
	)

	session := domain.Task{
		ID:             "impl-session-1",
		WorkItemID:     "wi-1",
		SubPlanID:      "sp-1",
		RepositoryName: "repo-a",
	}
	subPlan := domain.TaskPlan{ID: "sp-1", PlanID: "plan-1", RepositoryName: "repo-a"}
	plan := domain.Plan{ID: "plan-1", WorkItemID: "wi-1"}
	cycle := domain.ReviewCycle{ID: "cycle-1"}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	type result struct {
		err error
	}
	done := make(chan result, 1)
	go func() {
		_, _, _, err := pipeline.startReviewAgent(ctx, session, subPlan, plan, cycle)
		done <- result{err: err}
	}()

	// The pipeline's reviewTimeout is 5s. If foreman_proposed is not handled,
	// the call blocks for 5s. Assert it returns well before that.
	select {
	case res := <-done:
		// The call returned. It may have an error (log file not found in test
		// environment is fine); what matters is it did NOT block.
		if res.err != nil && res.err == context.DeadlineExceeded {
			t.Fatalf("startReviewAgent returned context.DeadlineExceeded: foreman_proposed not handled")
		}
		// A "read review session output" error is expected (no real log file).
		// Any other error is also acceptable as long as we didn't time out.
	case <-time.After(2 * time.Second):
		t.Fatal("startReviewAgent blocked for 2s: foreman_proposed not handled as completion signal")
	}
}

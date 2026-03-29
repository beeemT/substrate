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
func (s *doneSession) ResumeInfo() map[string]string                { return nil }

type doneHarness struct{}

func (h *doneHarness) Name() string { return "done-mock" }
func (h *doneHarness) StartSession(_ context.Context, opts adapter.SessionOpts) (adapter.AgentSession, error) {
	return newDoneSession(opts.SessionID), nil
}

// newReviewPipelineForTest builds a minimal ReviewPipeline for unit testing.
// sessionRepo must be pre-populated with any sessions the test needs.
func newReviewPipelineForTest(harness adapter.AgentHarness, sessionRepo *mockSessionRepo) *ReviewPipeline {
	sessionSvc := service.NewTaskService(repository.NoopTransacter{Res: repository.Resources{Tasks: sessionRepo}})
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
	}
}

// TestStartReviewAgent_CompletesOnDone verifies that the review event loop
// returns as soon as it receives the "done" signal (lifecycle.completed from
// the agent bridge). If the loop did not handle "done" the call would block
// until reviewTimeout fired.
func TestStartReviewAgent_CompletesOnDone(t *testing.T) {
	sessionRepo := newMockSessionRepo()
	sessionRepo.sessions["impl-session-1"] = domain.Task{
		ID:         "impl-session-1",
		WorkItemID: "wi-1",
		Status:     domain.AgentSessionRunning,
	}

	pipeline := newReviewPipelineForTest(&doneHarness{}, sessionRepo)

	session := domain.Task{
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

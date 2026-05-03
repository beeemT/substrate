package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/event"
	"github.com/beeemT/substrate/internal/repository"
)


// waitForEvent receives an event from ch or fails t on timeout.
func waitForEvent(t *testing.T, ch <-chan domain.SystemEvent, timeout time.Duration) domain.SystemEvent {
	select {
	case evt := <-ch:
		return evt
	case <-time.After(timeout):
		t.Fatalf("timeout after %v waiting for event", timeout)
		return domain.SystemEvent{} // unreachable
	}
}

func TestEmit(t *testing.T) {
	t.Run("nil bus does not panic", func(t *testing.T) {
		// Should not panic when bus is nil
		Emit(nil, domain.SystemEvent{
			ID:        domain.NewID(),
			EventType: string(domain.EventAgentTaskCompleted),
		})
	})

	t.Run("emits event asynchronously", func(t *testing.T) {
		// Create a real event bus with mock repo
		repo := &mockEventRepoForEmit{events: []domain.SystemEvent{}}
		bus := event.NewBus(event.BusConfig{EventRepo: repo})

		sub, err := bus.Subscribe("test-subscriber", string(domain.EventAgentTaskCompleted))
		if err != nil {
			t.Fatalf("Subscribe: %v", err)
		}
		// Note: no Unsubscribe method on Subscriber in current event.Bus implementation

		// Emit event
		Emit(bus, domain.SystemEvent{
			ID:        domain.NewID(),
			EventType: string(domain.EventAgentTaskCompleted),
		})

		// Wait for async emission and receive
		select {
		case <-sub.C:
			// Success
		case <-time.After(2 * time.Second):
			t.Error("timeout waiting for event")
		}

		// Note: no Close method on Bus in current implementation
	})
}

func TestTaskService_EmitsEvents(t *testing.T) {
	t.Run("Create emits EventAgentTaskStarted", func(t *testing.T) {
		repo := NewMockSessionRepository()
		bus := event.NewBus(event.BusConfig{EventRepo: &mockEventRepoForEmit{events: []domain.SystemEvent{}}})
		svc := NewTaskService(repository.NoopTransacter{Res: repository.Resources{Tasks: repo}}, bus)

		sub, err := bus.Subscribe("test", string(domain.EventAgentTaskStarted))
		if err != nil {
			t.Fatalf("Subscribe: %v", err)
		}
		// Note: no Unsubscribe method on Subscriber in current event.Bus implementation

		task := domain.Task{
			ID:             "task-1",
			WorkItemID:     "wi-1",
			SubPlanID:      "sp-1",
			WorkspaceID:    "ws-1",
			Phase:          domain.TaskPhaseImplementation,
			RepositoryName: "repo1",
			HarnessName:    "omp",
			Status:         domain.AgentSessionPending,
		}

		if err := svc.Create(context.Background(), task); err != nil {
			t.Fatalf("Create: %v", err)
		}

		select {
		case evt := <-sub.C:
			if evt.EventType != string(domain.EventAgentTaskStarted) {
				t.Errorf("event type = %q, want %q", evt.EventType, domain.EventAgentTaskStarted)
			}
			t.Logf("received event: %s", evt.ID)
		case <-time.After(2 * time.Second):
			t.Error("timeout waiting for EventAgentTaskStarted")
		}
	})

	t.Run("Complete emits EventAgentTaskCompleted", func(t *testing.T) {
		repo := NewMockSessionRepository()
		bus := event.NewBus(event.BusConfig{EventRepo: &mockEventRepoForEmit{events: []domain.SystemEvent{}}})
		svc := NewTaskService(repository.NoopTransacter{Res: repository.Resources{Tasks: repo}}, bus)

		sub, _ := bus.Subscribe("test", string(domain.EventAgentTaskCompleted))
		// Note: no Unsubscribe method on Subscriber in current event.Bus implementation

		task := domain.Task{
			ID:             "task-complete-test",
			WorkItemID:     "wi-1",
			SubPlanID:      "sp-1",
			WorkspaceID:    "ws-1",
			Phase:          domain.TaskPhaseImplementation,
			RepositoryName: "repo1",
			HarnessName:    "omp",
			Status:         domain.AgentSessionPending,
		}
		if err := svc.Create(context.Background(), task); err != nil {
			t.Fatalf("Create: %v", err)
		}

		// Transition to running first
		if err := svc.Start(context.Background(), "task-complete-test"); err != nil {
			t.Fatalf("Start: %v", err)
		}

		// Complete the task
		if err := svc.Complete(context.Background(), "task-complete-test"); err != nil {
			t.Fatalf("Complete: %v", err)
		}

		select {
		case evt := <-sub.C:
			if evt.EventType != string(domain.EventAgentTaskCompleted) {
				t.Errorf("event type = %q, want %q", evt.EventType, domain.EventAgentTaskCompleted)
			}
		case <-time.After(2 * time.Second):
			t.Error("timeout waiting for EventAgentTaskCompleted")
		}
	})

	t.Run("Fail emits EventAgentTaskFailed", func(t *testing.T) {
		repo := NewMockSessionRepository()
		bus := event.NewBus(event.BusConfig{EventRepo: &mockEventRepoForEmit{events: []domain.SystemEvent{}}})
		svc := NewTaskService(repository.NoopTransacter{Res: repository.Resources{Tasks: repo}}, bus)

		sub, _ := bus.Subscribe("test", string(domain.EventAgentTaskFailed))
		// Note: no Unsubscribe method on Subscriber in current event.Bus implementation

		task := domain.Task{
			ID:             "task-fail-test",
			WorkItemID:     "wi-1",
			SubPlanID:      "sp-1",
			WorkspaceID:    "ws-1",
			Phase:          domain.TaskPhaseImplementation,
			RepositoryName: "repo1",
			HarnessName:    "omp",
			Status:         domain.AgentSessionPending,
		}
		if err := svc.Create(context.Background(), task); err != nil {
			t.Fatalf("Create: %v", err)
		}

		exitCode := 1
		if err := svc.Fail(context.Background(), "task-fail-test", &exitCode); err != nil {
			t.Fatalf("Fail: %v", err)
		}

		select {
		case evt := <-sub.C:
			if evt.EventType != string(domain.EventAgentTaskFailed) {
				t.Errorf("event type = %q, want %q", evt.EventType, domain.EventAgentTaskFailed)
			}
		case <-time.After(2 * time.Second):
			t.Error("timeout waiting for EventAgentTaskFailed")
		}
	})

	t.Run("Interrupt emits EventAgentTaskInterrupted", func(t *testing.T) {
		repo := NewMockSessionRepository()
		bus := event.NewBus(event.BusConfig{EventRepo: &mockEventRepoForEmit{events: []domain.SystemEvent{}}})
		svc := NewTaskService(repository.NoopTransacter{Res: repository.Resources{Tasks: repo}}, bus)

		sub, _ := bus.Subscribe("test", string(domain.EventAgentTaskInterrupted))
		// Note: no Unsubscribe method on Subscriber in current event.Bus implementation

		task := domain.Task{
			ID:             "task-interrupt-test",
			WorkItemID:     "wi-1",
			SubPlanID:      "sp-1",
			WorkspaceID:    "ws-1",
			Phase:          domain.TaskPhaseImplementation,
			RepositoryName: "repo1",
			HarnessName:    "omp",
			Status:         domain.AgentSessionPending,
		}
		if err := svc.Create(context.Background(), task); err != nil {
			t.Fatalf("Create: %v", err)
		}

		// Transition to running first
		if err := svc.Start(context.Background(), "task-interrupt-test"); err != nil {
			t.Fatalf("Start: %v", err)
		}

		if err := svc.Interrupt(context.Background(), "task-interrupt-test"); err != nil {
			t.Fatalf("Interrupt: %v", err)
		}

		select {
		case evt := <-sub.C:
			if evt.EventType != string(domain.EventAgentTaskInterrupted) {
				t.Errorf("event type = %q, want %q", evt.EventType, domain.EventAgentTaskInterrupted)
			}
		case <-time.After(2 * time.Second):
			t.Error("timeout waiting for EventAgentTaskInterrupted")
		}
	})

	t.Run("nil bus does not panic", func(t *testing.T) {
		repo := NewMockSessionRepository()
		svc := NewTaskService(repository.NoopTransacter{Res: repository.Resources{Tasks: repo}}, nil)

		// Should not panic
		task := domain.Task{
			ID:             "task-nil-bus-test",
			WorkItemID:     "wi-1",
			SubPlanID:      "sp-1",
			WorkspaceID:    "ws-1",
			Phase:          domain.TaskPhaseImplementation,
			RepositoryName: "repo1",
			HarnessName:    "omp",
			Status:         domain.AgentSessionPending,
		}
		if err := svc.Create(context.Background(), task); err != nil {
			t.Fatalf("Create with nil bus: %v", err)
		}
	})
}

// mockEventRepoForEmit implements repository.EventRepository for testing
type mockEventRepoForEmit struct {
	events []domain.SystemEvent
	mu     sync.Mutex
}

func (m *mockEventRepoForEmit) Create(ctx context.Context, event domain.SystemEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, event)
	return nil
}

func (m *mockEventRepoForEmit) ListByType(ctx context.Context, eventType string, limit int) ([]domain.SystemEvent, error) {
	return m.events, nil
}

func (m *mockEventRepoForEmit) ListByWorkspaceID(ctx context.Context, workspaceID string, limit int) ([]domain.SystemEvent, error) {
	return m.events, nil
}

func TestSessionService_EmitsEvents(t *testing.T) {
	t.Run("Transition emits work_item event", func(t *testing.T) {
		repo := NewMockWorkItemRepository()
		bus := event.NewBus(event.BusConfig{EventRepo: &mockEventRepoForEmit{events: []domain.SystemEvent{}}})
		svc := NewSessionService(repository.NoopTransacter{Res: repository.Resources{Sessions: repo}}, bus)

		sub, _ := bus.Subscribe("test", string(domain.EventWorkItemPlanning))

		// Create an ingested item first
		item := domain.Session{
			ID:          "wi-transition-test",
			WorkspaceID: "ws-1",
			Title:       "Test Item",
			Source:      "manual",
			State:       domain.SessionIngested,
		}
		if err := svc.Create(context.Background(), item); err != nil {
			t.Fatalf("Create: %v", err)
		}

		// Transition to planning
		if err := svc.Transition(context.Background(), "wi-transition-test", domain.SessionPlanning); err != nil {
			t.Fatalf("Transition: %v", err)
		}

		select {
		case evt := <-sub.C:
			if evt.EventType != string(domain.EventWorkItemPlanning) {
				t.Errorf("event type = %q, want %q", evt.EventType, domain.EventWorkItemPlanning)
			}
		case <-time.After(2 * time.Second):
			t.Error("timeout waiting for EventWorkItemPlanning")
		}
	})

	t.Run("Transition to SessionImplementing emits EventWorkItemImplementing", func(t *testing.T) {
		repo := NewMockWorkItemRepository()
		bus := event.NewBus(event.BusConfig{EventRepo: &mockEventRepoForEmit{events: []domain.SystemEvent{}}})
		svc := NewSessionService(repository.NoopTransacter{Res: repository.Resources{Sessions: repo}}, bus)

		sub, _ := bus.Subscribe("test", string(domain.EventWorkItemImplementing))

		// Start with ingested and transition through the state machine
		item := domain.Session{
			ID:          "wi-impl-test",
			WorkspaceID: "ws-1",
			Title:       "Test Item",
			Source:      "manual",
			State:       domain.SessionIngested,
		}
		if err := svc.Create(context.Background(), item); err != nil {
			t.Fatalf("Create: %v", err)
		}
		// Transition: ingested -> planning -> approved -> implementing
		if err := svc.Transition(context.Background(), "wi-impl-test", domain.SessionPlanning); err != nil {
			t.Fatalf("Transition to planning: %v", err)
		}
		if err := svc.Transition(context.Background(), "wi-impl-test", domain.SessionPlanReview); err != nil {
			t.Fatalf("Transition to plan_review: %v", err)
		}
		if err := svc.Transition(context.Background(), "wi-impl-test", domain.SessionApproved); err != nil {
			t.Fatalf("Transition to approved: %v", err)
		}
		if err := svc.Transition(context.Background(), "wi-impl-test", domain.SessionImplementing); err != nil {
			t.Fatalf("Transition to implementing: %v", err)
		}

		select {
		case evt := <-sub.C:
			if evt.EventType != string(domain.EventWorkItemImplementing) {
				t.Errorf("event type = %q, want %q", evt.EventType, domain.EventWorkItemImplementing)
			}
		case <-time.After(2 * time.Second):
			t.Error("timeout waiting for EventWorkItemImplementing")
		}
	})

	t.Run("Transition to SessionCompleted emits EventWorkItemCompleted", func(t *testing.T) {
		repo := NewMockWorkItemRepository()
		bus := event.NewBus(event.BusConfig{EventRepo: &mockEventRepoForEmit{events: []domain.SystemEvent{}}})
		svc := NewSessionService(repository.NoopTransacter{Res: repository.Resources{Sessions: repo}}, bus)

		sub, _ := bus.Subscribe("test", string(domain.EventWorkItemCompleted))

		// Start with ingested and transition through the state machine
		item := domain.Session{
			ID:          "wi-complete-test",
			WorkspaceID: "ws-1",
			Title:       "Test Item",
			Source:      "manual",
			State:       domain.SessionIngested,
		}
		if err := svc.Create(context.Background(), item); err != nil {
			t.Fatalf("Create: %v", err)
		}
		// Transition: ingested -> planning -> plan_review -> approved -> implementing -> reviewing -> completed
		if err := svc.Transition(context.Background(), "wi-complete-test", domain.SessionPlanning); err != nil {
			t.Fatalf("Transition to planning: %v", err)
		}
		if err := svc.Transition(context.Background(), "wi-complete-test", domain.SessionPlanReview); err != nil {
			t.Fatalf("Transition to plan_review: %v", err)
		}
		if err := svc.Transition(context.Background(), "wi-complete-test", domain.SessionApproved); err != nil {
			t.Fatalf("Transition to approved: %v", err)
		}
		if err := svc.Transition(context.Background(), "wi-complete-test", domain.SessionImplementing); err != nil {
			t.Fatalf("Transition to implementing: %v", err)
		}
		if err := svc.Transition(context.Background(), "wi-complete-test", domain.SessionReviewing); err != nil {
			t.Fatalf("Transition to reviewing: %v", err)
		}
		if err := svc.Transition(context.Background(), "wi-complete-test", domain.SessionCompleted); err != nil {
			t.Fatalf("Transition to completed: %v", err)
		}

		select {
		case evt := <-sub.C:
			if evt.EventType != string(domain.EventWorkItemCompleted) {
				t.Errorf("event type = %q, want %q", evt.EventType, domain.EventWorkItemCompleted)
			}
		case <-time.After(2 * time.Second):
			t.Error("timeout waiting for EventWorkItemCompleted")
		}
	})

	t.Run("nil bus does not panic", func(t *testing.T) {
		repo := NewMockWorkItemRepository()
		svc := NewSessionService(repository.NoopTransacter{Res: repository.Resources{Sessions: repo}}, nil)

		item := domain.Session{
			ID:          "wi-nil-bus-test",
			WorkspaceID: "ws-1",
			Title:       "Test Item",
			Source:      "manual",
			State:       domain.SessionIngested,
		}
		if err := svc.Create(context.Background(), item); err != nil {
			t.Fatalf("Create: %v", err)
		}

		// Should not panic
		if err := svc.Transition(context.Background(), "wi-nil-bus-test", domain.SessionPlanning); err != nil {
			t.Fatalf("Transition with nil bus: %v", err)
		}
	})
}

func TestPlanService_EmitsEvents(t *testing.T) {
	t.Run("SubmitForReview emits EventPlanSubmittedForReview", func(t *testing.T) {
		repo := NewMockPlanRepository()
		subPlanRepo := NewMockSubPlanRepository()
		bus := event.NewBus(event.BusConfig{EventRepo: &mockEventRepoForEmit{events: []domain.SystemEvent{}}})
		svc := NewPlanService(repository.NoopTransacter{Res: repository.Resources{Plans: repo, SubPlans: subPlanRepo}}, bus)

		sub, _ := bus.Subscribe("test", string(domain.EventPlanSubmittedForReview))

		// Create a draft plan first
		plan := domain.Plan{
			ID:         "plan-submit-test",
			WorkItemID: "wi-1",
			Status:     domain.PlanDraft,
			Version:    1,
		}
		if err := repo.Create(context.Background(), plan); err != nil {
			t.Fatalf("Create plan: %v", err)
		}

		if err := svc.SubmitForReview(context.Background(), "plan-submit-test"); err != nil {
			t.Fatalf("SubmitForReview: %v", err)
		}

		select {
		case evt := <-sub.C:
			if evt.EventType != string(domain.EventPlanSubmittedForReview) {
				t.Errorf("event type = %q, want %q", evt.EventType, domain.EventPlanSubmittedForReview)
			}
		case <-time.After(2 * time.Second):
			t.Error("timeout waiting for EventPlanSubmittedForReview")
		}
	})

	t.Run("ApprovePlan emits EventPlanApproved", func(t *testing.T) {
		repo := NewMockPlanRepository()
		subPlanRepo := NewMockSubPlanRepository()
		bus := event.NewBus(event.BusConfig{EventRepo: &mockEventRepoForEmit{events: []domain.SystemEvent{}}})
		svc := NewPlanService(repository.NoopTransacter{Res: repository.Resources{Plans: repo, SubPlans: subPlanRepo}}, bus)

		sub, _ := bus.Subscribe("test", string(domain.EventPlanApproved))

		// Create a pending_review plan first
		plan := domain.Plan{
			ID:         "plan-approve-test",
			WorkItemID: "wi-1",
			Status:     domain.PlanPendingReview,
			Version:    1,
		}
		if err := repo.Create(context.Background(), plan); err != nil {
			t.Fatalf("Create plan: %v", err)
		}

		if err := svc.ApprovePlan(context.Background(), "plan-approve-test"); err != nil {
			t.Fatalf("ApprovePlan: %v", err)
		}

		select {
		case evt := <-sub.C:
			if evt.EventType != string(domain.EventPlanApproved) {
				t.Errorf("event type = %q, want %q", evt.EventType, domain.EventPlanApproved)
			}
		case <-time.After(2 * time.Second):
			t.Error("timeout waiting for EventPlanApproved")
		}
	})

	t.Run("RejectPlan emits EventPlanRejected", func(t *testing.T) {
		repo := NewMockPlanRepository()
		subPlanRepo := NewMockSubPlanRepository()
		bus := event.NewBus(event.BusConfig{EventRepo: &mockEventRepoForEmit{events: []domain.SystemEvent{}}})
		svc := NewPlanService(repository.NoopTransacter{Res: repository.Resources{Plans: repo, SubPlans: subPlanRepo}}, bus)

		sub, _ := bus.Subscribe("test", string(domain.EventPlanRejected))

		// Create a pending_review plan first
		plan := domain.Plan{
			ID:         "plan-reject-test",
			WorkItemID: "wi-1",
			Status:     domain.PlanPendingReview,
			Version:    1,
		}
		if err := repo.Create(context.Background(), plan); err != nil {
			t.Fatalf("Create plan: %v", err)
		}

		if err := svc.RejectPlan(context.Background(), "plan-reject-test"); err != nil {
			t.Fatalf("RejectPlan: %v", err)
		}

		select {
		case evt := <-sub.C:
			if evt.EventType != string(domain.EventPlanRejected) {
				t.Errorf("event type = %q, want %q", evt.EventType, domain.EventPlanRejected)
			}
		case <-time.After(2 * time.Second):
			t.Error("timeout waiting for EventPlanRejected")
		}
	})

	t.Run("TransitionSubPlan emits EventSubPlanStatusChanged", func(t *testing.T) {
		repo := NewMockPlanRepository()
		subPlanRepo := NewMockSubPlanRepository()
		bus := event.NewBus(event.BusConfig{EventRepo: &mockEventRepoForEmit{events: []domain.SystemEvent{}}})
		svc := NewPlanService(repository.NoopTransacter{Res: repository.Resources{Plans: repo, SubPlans: subPlanRepo}}, bus)

		sub, _ := bus.Subscribe("test", string(domain.EventSubPlanStatusChanged))

		// Create a pending sub-plan first
		subPlan := domain.TaskPlan{
			ID:     "subplan-transition-test",
			PlanID: "plan-1",
			Status: domain.SubPlanPending,
		}
		if err := subPlanRepo.Create(context.Background(), subPlan); err != nil {
			t.Fatalf("Create sub-plan: %v", err)
		}

		if err := svc.TransitionSubPlan(context.Background(), "subplan-transition-test", domain.SubPlanInProgress); err != nil {
			t.Fatalf("TransitionSubPlan: %v", err)
		}

		select {
		case evt := <-sub.C:
			if evt.EventType != string(domain.EventSubPlanStatusChanged) {
				t.Errorf("event type = %q, want %q", evt.EventType, domain.EventSubPlanStatusChanged)
			}
		case <-time.After(2 * time.Second):
			t.Error("timeout waiting for EventSubPlanStatusChanged")
		}
	})

	t.Run("nil bus does not panic", func(t *testing.T) {
		repo := NewMockPlanRepository()
		subPlanRepo := NewMockSubPlanRepository()
		svc := NewPlanService(repository.NoopTransacter{Res: repository.Resources{Plans: repo, SubPlans: subPlanRepo}}, nil)

		// Should not panic
		plan := domain.Plan{
			ID:         "plan-nil-bus-test",
			WorkItemID: "wi-1",
			Status:     domain.PlanPendingReview,
			Version:    1,
		}
		if err := repo.Create(context.Background(), plan); err != nil {
			t.Fatalf("Create plan: %v", err)
		}

		if err := svc.ApprovePlan(context.Background(), "plan-nil-bus-test"); err != nil {
			t.Fatalf("ApprovePlan with nil bus: %v", err)
		}
	})
}

package service

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/event"
	"github.com/beeemT/substrate/internal/repository"
)

func TestEmit(t *testing.T) {
	t.Run("emits event before returning", func(t *testing.T) {
		// Create a real event bus with mock repo
		repo := &mockEventRepoForEmit{events: []domain.SystemEvent{}}
		bus := event.NewBus(event.BusConfig{EventRepo: repo})

		sub, err := bus.Subscribe("test-subscriber", string(domain.EventAgentSessionCompleted))
		if err != nil {
			t.Fatalf("Subscribe: %v", err)
		}
		defer bus.Unsubscribe(sub.ID)

		// Emit event
		Emit(bus, domain.SystemEvent{
			ID:        domain.NewID(),
			EventType: string(domain.EventAgentSessionCompleted),
		})

		repo.mu.Lock()
		persisted := len(repo.events)
		repo.mu.Unlock()
		if persisted != 1 {
			t.Fatalf("persisted events = %d, want 1 before Emit returns", persisted)
		}

		// Emit returns only after the event is persisted and dispatched.
		select {
		case <-sub.C:
			// Success
		case <-time.After(2 * time.Second):
			t.Error("timeout waiting for event")
		}

		defer bus.Close()
	})
}

func TestAgentSessionService_EmitsEvents(t *testing.T) {
	t.Run("Start emits EventAgentSessionStarted", func(t *testing.T) {
		repo := NewMockSessionRepository()
		bus := event.NewBus(event.BusConfig{EventRepo: &mockEventRepoForEmit{events: []domain.SystemEvent{}}})
		svc := NewAgentSessionService(repository.NoopTransacter{Res: repository.Resources{AgentSessions: repo}}, bus)

		sub, err := bus.Subscribe("test", string(domain.EventAgentSessionStarted))
		if err != nil {
			t.Fatalf("Subscribe: %v", err)
		}
		defer bus.Unsubscribe(sub.ID)

		session := domain.AgentSession{
			ID:             "session-1",
			WorkItemID:     "wi-1",
			SubPlanID:      "sp-1",
			WorkspaceID:    "ws-1",
			Kind: domain.AgentSessionKindImplementation,
			RepositoryName: "repo1",
			HarnessName:    "omp",
			Status:         domain.AgentSessionPending,
		}
		if err := svc.Create(context.Background(), session); err != nil {
			t.Fatalf("Create: %v", err)
		}

		// Event fires on Start, not Create
		if err := svc.Start(context.Background(), "session-1"); err != nil {
			t.Fatalf("Start: %v", err)
		}

		select {
		case evt := <-sub.C:
			if evt.EventType != string(domain.EventAgentSessionStarted) {
				t.Errorf("event type = %q, want %q", evt.EventType, domain.EventAgentSessionStarted)
			}
			t.Logf("received event: %s", evt.ID)
		case <-time.After(2 * time.Second):
			t.Error("timeout waiting for EventAgentSessionStarted")
		}
	})

	t.Run("Complete emits EventAgentSessionCompleted", func(t *testing.T) {
		repo := NewMockSessionRepository()
		bus := event.NewBus(event.BusConfig{EventRepo: &mockEventRepoForEmit{events: []domain.SystemEvent{}}})
		svc := NewAgentSessionService(repository.NoopTransacter{Res: repository.Resources{AgentSessions: repo}}, bus)

		sub, _ := bus.Subscribe("test", string(domain.EventAgentSessionCompleted))
		defer bus.Unsubscribe(sub.ID)

		session := domain.AgentSession{
			ID:             "session-complete-test",
			WorkItemID:     "wi-1",
			SubPlanID:      "sp-1",
			WorkspaceID:    "ws-1",
			Kind: domain.AgentSessionKindImplementation,
			RepositoryName: "repo1",
			HarnessName:    "omp",
			Status:         domain.AgentSessionPending,
		}
		if err := svc.Create(context.Background(), session); err != nil {
			t.Fatalf("Create: %v", err)
		}

		// Transition to running first
		if err := svc.Start(context.Background(), "session-complete-test"); err != nil {
			t.Fatalf("Start: %v", err)
		}

		// Complete the session
		if err := svc.Complete(context.Background(), "session-complete-test"); err != nil {
			t.Fatalf("Complete: %v", err)
		}

		select {
		case evt := <-sub.C:
			if evt.EventType != string(domain.EventAgentSessionCompleted) {
				t.Errorf("event type = %q, want %q", evt.EventType, domain.EventAgentSessionCompleted)
			}
		case <-time.After(2 * time.Second):
			t.Error("timeout waiting for EventAgentSessionCompleted")
		}
	})

	t.Run("Fail emits EventAgentSessionFailed", func(t *testing.T) {
		repo := NewMockSessionRepository()
		bus := event.NewBus(event.BusConfig{EventRepo: &mockEventRepoForEmit{events: []domain.SystemEvent{}}})
		svc := NewAgentSessionService(repository.NoopTransacter{Res: repository.Resources{AgentSessions: repo}}, bus)

		sub, _ := bus.Subscribe("test", string(domain.EventAgentSessionFailed))
		defer bus.Unsubscribe(sub.ID)

		session := domain.AgentSession{
			ID:             "session-fail-test",
			WorkItemID:     "wi-1",
			SubPlanID:      "sp-1",
			WorkspaceID:    "ws-1",
			Kind: domain.AgentSessionKindImplementation,
			RepositoryName: "repo1",
			HarnessName:    "omp",
			Status:         domain.AgentSessionPending,
		}
		if err := svc.Create(context.Background(), session); err != nil {
			t.Fatalf("Create: %v", err)
		}

		exitCode := 1
		if err := svc.Fail(context.Background(), "session-fail-test", &exitCode); err != nil {
			t.Fatalf("Fail: %v", err)
		}

		select {
		case evt := <-sub.C:
			if evt.EventType != string(domain.EventAgentSessionFailed) {
				t.Errorf("event type = %q, want %q", evt.EventType, domain.EventAgentSessionFailed)
			}
		case <-time.After(2 * time.Second):
			t.Error("timeout waiting for EventAgentSessionFailed")
		}
	})

	t.Run("Interrupt emits EventAgentSessionInterrupted", func(t *testing.T) {
		repo := NewMockSessionRepository()
		bus := event.NewBus(event.BusConfig{EventRepo: &mockEventRepoForEmit{events: []domain.SystemEvent{}}})
		svc := NewAgentSessionService(repository.NoopTransacter{Res: repository.Resources{AgentSessions: repo}}, bus)

		sub, _ := bus.Subscribe("test", string(domain.EventAgentSessionInterrupted))
		defer bus.Unsubscribe(sub.ID)

		session := domain.AgentSession{
			ID:             "session-interrupt-test",
			WorkItemID:     "wi-1",
			SubPlanID:      "sp-1",
			WorkspaceID:    "ws-1",
			Kind: domain.AgentSessionKindImplementation,
			RepositoryName: "repo1",
			HarnessName:    "omp",
			Status:         domain.AgentSessionPending,
		}
		if err := svc.Create(context.Background(), session); err != nil {
			t.Fatalf("Create: %v", err)
		}

		// Transition to running first
		if err := svc.Start(context.Background(), "session-interrupt-test"); err != nil {
			t.Fatalf("Start: %v", err)
		}

		if err := svc.Interrupt(context.Background(), "session-interrupt-test"); err != nil {
			t.Fatalf("Interrupt: %v", err)
		}

		select {
		case evt := <-sub.C:
			if evt.EventType != string(domain.EventAgentSessionInterrupted) {
				t.Errorf("event type = %q, want %q", evt.EventType, domain.EventAgentSessionInterrupted)
			}
		case <-time.After(2 * time.Second):
			t.Error("timeout waiting for EventAgentSessionInterrupted")
		}
	})

	t.Run("nil bus does not panic", func(t *testing.T) {
		repo := NewMockSessionRepository()
		svc := NewAgentSessionService(repository.NoopTransacter{Res: repository.Resources{AgentSessions: repo}}, newTestBus())

		// Should not panic
		session := domain.AgentSession{
			ID:             "session-nil-bus-test",
			WorkItemID:     "wi-1",
			SubPlanID:      "sp-1",
			WorkspaceID:    "ws-1",
			Kind: domain.AgentSessionKindImplementation,
			RepositoryName: "repo1",
			HarnessName:    "omp",
			Status:         domain.AgentSessionPending,
		}
		if err := svc.Create(context.Background(), session); err != nil {
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
		svc := NewSessionService(repository.NoopTransacter{Res: repository.Resources{Sessions: repo}}, newTestBus())

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

	// Note: Archive/Unarchive don't emit events, so no nil-bus test needed for them.
	// They call s.transacter.Transact directly and don't use the Emit helper.
}

func TestPlanService_EmitsEvents(t *testing.T) {
	t.Run("SubmitForReview emits EventPlanSubmitted", func(t *testing.T) {
		repo := NewMockPlanRepository()
		subPlanRepo := NewMockSubPlanRepository()
		sessionRepo := NewMockWorkItemRepository()
		bus := event.NewBus(event.BusConfig{EventRepo: &mockEventRepoForEmit{events: []domain.SystemEvent{}}})
		svc := NewPlanService(repository.NoopTransacter{Res: repository.Resources{Plans: repo, SubPlans: subPlanRepo, Sessions: sessionRepo}}, bus)

		sub, _ := bus.Subscribe("test", string(domain.EventPlanSubmitted))

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

		// Create work item so SubmitForReview can load it for WorkspaceID
		sessionRepo.Create(context.Background(), domain.Session{ID: "wi-1", WorkspaceID: "ws-1"})

		if err := svc.SubmitForReview(context.Background(), "plan-submit-test"); err != nil {
			t.Fatalf("SubmitForReview: %v", err)
		}

		select {
		case evt := <-sub.C:
			if evt.EventType != string(domain.EventPlanSubmitted) {
				t.Errorf("event type = %q, want %q", evt.EventType, domain.EventPlanSubmitted)
			}
		case <-time.After(2 * time.Second):
			t.Error("timeout waiting for EventPlanSubmitted")
		}
	})

	t.Run("SubmitForReview emits EventPlanStatusChanged", func(t *testing.T) {
		repo := NewMockPlanRepository()
		subPlanRepo := NewMockSubPlanRepository()
		sessionRepo := NewMockWorkItemRepository()
		bus := event.NewBus(event.BusConfig{EventRepo: &mockEventRepoForEmit{events: []domain.SystemEvent{}}})
		svc := NewPlanService(repository.NoopTransacter{Res: repository.Resources{Plans: repo, SubPlans: subPlanRepo, Sessions: sessionRepo}}, bus)

		sub, _ := bus.Subscribe("test", string(domain.EventPlanStatusChanged))

		// Create a draft plan first
		plan := domain.Plan{
			ID:         "plan-status-test",
			WorkItemID: "wi-1",
			Status:     domain.PlanDraft,
			Version:    1,
		}
		if err := repo.Create(context.Background(), plan); err != nil {
			t.Fatalf("Create plan: %v", err)
		}

		// Create work item so SubmitForReview can load it for WorkspaceID
		sessionRepo.Create(context.Background(), domain.Session{ID: "wi-1", WorkspaceID: "ws-1"})

		if err := svc.SubmitForReview(context.Background(), "plan-status-test"); err != nil {
			t.Fatalf("SubmitForReview: %v", err)
		}

		select {
		case evt := <-sub.C:
			if evt.EventType != string(domain.EventPlanStatusChanged) {
				t.Errorf("event type = %q, want %q", evt.EventType, domain.EventPlanStatusChanged)
			}
			// Verify payload contains the correct status transition
			var payload struct {
				From string `json:"from"`
				To   string `json:"to"`
			}
			if err := json.Unmarshal([]byte(evt.Payload), &payload); err != nil {
				t.Fatalf("failed to unmarshal payload: %v", err)
			}
			if payload.From != string(domain.PlanDraft) {
				t.Errorf("From = %q, want %q", payload.From, domain.PlanDraft)
			}
			if payload.To != string(domain.PlanPendingReview) {
				t.Errorf("To = %q, want %q", payload.To, domain.PlanPendingReview)
			}
		case <-time.After(2 * time.Second):
			t.Error("timeout waiting for EventPlanStatusChanged")
		}
	})

	t.Run("ApprovePlan emits EventPlanApproved", func(t *testing.T) {
		repo := NewMockPlanRepository()
		subPlanRepo := NewMockSubPlanRepository()
		sessionRepo := NewMockWorkItemRepository()
		bus := event.NewBus(event.BusConfig{EventRepo: &mockEventRepoForEmit{events: []domain.SystemEvent{}}})
		svc := NewPlanService(repository.NoopTransacter{Res: repository.Resources{Plans: repo, SubPlans: subPlanRepo, Sessions: sessionRepo}}, bus)

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

		// Create work item so ApprovePlan can load it for WorkspaceID
		sessionRepo.Create(context.Background(), domain.Session{ID: "wi-1", WorkspaceID: "ws-1"})

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
		sessionRepo := NewMockWorkItemRepository()
		bus := event.NewBus(event.BusConfig{EventRepo: &mockEventRepoForEmit{events: []domain.SystemEvent{}}})
		svc := NewPlanService(repository.NoopTransacter{Res: repository.Resources{Plans: repo, SubPlans: subPlanRepo, Sessions: sessionRepo}}, bus)

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

	t.Run("TransitionSubPlan emits EventSubPlanStarted", func(t *testing.T) {
		repo := NewMockPlanRepository()
		subPlanRepo := NewMockSubPlanRepository()
		sessionRepo := NewMockWorkItemRepository()
		bus := event.NewBus(event.BusConfig{EventRepo: &mockEventRepoForEmit{events: []domain.SystemEvent{}}})
		svc := NewPlanService(repository.NoopTransacter{Res: repository.Resources{Plans: repo, SubPlans: subPlanRepo, Sessions: sessionRepo}}, bus)

		sub, _ := bus.Subscribe("test", string(domain.EventSubPlanStarted))

		// Create a plan and work item so TransitionSubPlan can load them
		plan := domain.Plan{
			ID:         "plan-1",
			WorkItemID: "wi-1",
			Status:     domain.PlanApproved,
		}
		if err := repo.Create(context.Background(), plan); err != nil {
			t.Fatalf("Create plan: %v", err)
		}
		workItem := domain.Session{
			ID:          "wi-1",
			WorkspaceID: "ws-1",
			State:       domain.SessionImplementing,
		}
		if err := sessionRepo.Create(context.Background(), workItem); err != nil {
			t.Fatalf("Create work item: %v", err)
		}

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
			if evt.EventType != string(domain.EventSubPlanStarted) {
				t.Errorf("event type = %q, want %q", evt.EventType, domain.EventSubPlanStarted)
			}
		case <-time.After(2 * time.Second):
			t.Error("timeout waiting for EventSubPlanStarted")
		}
	})

	t.Run("TransitionSubPlan emits EventSubPlanCompleted", func(t *testing.T) {
		repo := NewMockPlanRepository()
		subPlanRepo := NewMockSubPlanRepository()
		sessionRepo := NewMockWorkItemRepository()
		bus := event.NewBus(event.BusConfig{EventRepo: &mockEventRepoForEmit{events: []domain.SystemEvent{}}})
		svc := NewPlanService(repository.NoopTransacter{Res: repository.Resources{Plans: repo, SubPlans: subPlanRepo, Sessions: sessionRepo}}, bus)

		sub, _ := bus.Subscribe("test", string(domain.EventSubPlanCompleted))

		// Create a plan and work item so TransitionSubPlan can load them
		plan := domain.Plan{
			ID:         "plan-completed-test",
			WorkItemID: "wi-completed-test",
			Status:     domain.PlanApproved,
		}
		if err := repo.Create(context.Background(), plan); err != nil {
			t.Fatalf("Create plan: %v", err)
		}
		workItem := domain.Session{
			ID:          "wi-completed-test",
			WorkspaceID: "ws-1",
			State:       domain.SessionImplementing,
		}
		if err := sessionRepo.Create(context.Background(), workItem); err != nil {
			t.Fatalf("Create work item: %v", err)
		}

		// Create an in-progress sub-plan first
		subPlan := domain.TaskPlan{
			ID:     "subplan-completed-test",
			PlanID: "plan-completed-test",
			Status: domain.SubPlanInProgress,
		}
		if err := subPlanRepo.Create(context.Background(), subPlan); err != nil {
			t.Fatalf("Create sub-plan: %v", err)
		}

		if err := svc.TransitionSubPlan(context.Background(), "subplan-completed-test", domain.SubPlanCompleted); err != nil {
			t.Fatalf("TransitionSubPlan: %v", err)
		}

		select {
		case evt := <-sub.C:
			if evt.EventType != string(domain.EventSubPlanCompleted) {
				t.Errorf("event type = %q, want %q", evt.EventType, domain.EventSubPlanCompleted)
			}
		case <-time.After(2 * time.Second):
			t.Error("timeout waiting for EventSubPlanCompleted")
		}
	})

	t.Run("TransitionSubPlan emits EventSubPlanFailed", func(t *testing.T) {
		repo := NewMockPlanRepository()
		subPlanRepo := NewMockSubPlanRepository()
		sessionRepo := NewMockWorkItemRepository()
		bus := event.NewBus(event.BusConfig{EventRepo: &mockEventRepoForEmit{events: []domain.SystemEvent{}}})
		svc := NewPlanService(repository.NoopTransacter{Res: repository.Resources{Plans: repo, SubPlans: subPlanRepo, Sessions: sessionRepo}}, bus)

		sub, _ := bus.Subscribe("test", string(domain.EventSubPlanFailed))

		// Create a plan and work item so TransitionSubPlan can load them
		plan := domain.Plan{
			ID:         "plan-failed-test",
			WorkItemID: "wi-failed-test",
			Status:     domain.PlanApproved,
		}
		if err := repo.Create(context.Background(), plan); err != nil {
			t.Fatalf("Create plan: %v", err)
		}
		workItem := domain.Session{
			ID:          "wi-failed-test",
			WorkspaceID: "ws-1",
			State:       domain.SessionImplementing,
		}
		if err := sessionRepo.Create(context.Background(), workItem); err != nil {
			t.Fatalf("Create work item: %v", err)
		}

		// Create an in-progress sub-plan first
		subPlan := domain.TaskPlan{
			ID:     "subplan-failed-test",
			PlanID: "plan-failed-test",
			Status: domain.SubPlanInProgress,
		}
		if err := subPlanRepo.Create(context.Background(), subPlan); err != nil {
			t.Fatalf("Create sub-plan: %v", err)
		}

		if err := svc.TransitionSubPlan(context.Background(), "subplan-failed-test", domain.SubPlanFailed); err != nil {
			t.Fatalf("TransitionSubPlan: %v", err)
		}

		select {
		case evt := <-sub.C:
			if evt.EventType != string(domain.EventSubPlanFailed) {
				t.Errorf("event type = %q, want %q", evt.EventType, domain.EventSubPlanFailed)
			}
		case <-time.After(2 * time.Second):
			t.Error("timeout waiting for EventSubPlanFailed")
		}
	})

	t.Run("nil bus does not panic", func(t *testing.T) {
		repo := NewMockPlanRepository()
		subPlanRepo := NewMockSubPlanRepository()
		sessionRepo := NewMockWorkItemRepository()
		svc := NewPlanService(repository.NoopTransacter{Res: repository.Resources{Plans: repo, SubPlans: subPlanRepo, Sessions: sessionRepo}}, newTestBus())

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

		// Create work item so ApprovePlan can load WorkspaceID
		sessionRepo.Create(context.Background(), domain.Session{ID: "wi-1", WorkspaceID: "ws-1"})

		if err := svc.ApprovePlan(context.Background(), "plan-nil-bus-test"); err != nil {
			t.Fatalf("ApprovePlan with nil bus: %v", err)
		}
	})
}

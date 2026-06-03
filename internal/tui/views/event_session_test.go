package views

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/event"
	"github.com/beeemT/substrate/internal/repository"
	"github.com/beeemT/substrate/internal/service"
)

// TestDomainEventMsg_AgentSessionStarted verifies that the DomainEventMsg handler
// correctly extracts the work item ID and triggers session/task loading commands.
func TestDomainEventMsg_AgentSessionStarted(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	bus := event.NewBus(event.BusConfig{})
	t.Cleanup(func() { bus.Close() })

	// Create mock repositories
	taskRepo := &mockTaskRepoForSession{tasks: make(map[string]domain.AgentSession)}
	sessionRepo := &mockSessionRepoForSession{workItems: make(map[string]domain.Session)}

	// Create services with mock repositories
	taskSvc := service.NewAgentSessionService(repository.NoopTransacter{
		Res: repository.Resources{AgentSessions: taskRepo},
	}, bus)
	sessionSvc := service.NewSessionService(repository.NoopTransacter{
		Res: repository.Resources{Sessions: sessionRepo},
	}, bus)

	// Create App with services
	app := newTestApp(Services{
		WorkspaceID:   "ws-integration",
		WorkspaceName: "integration-test",
		Bus:           bus,
		Task:          taskSvc,
		Session:       sessionSvc,
		Settings:      newTestSettingsService(),
	})

	// Set up event subscription
	sub, err := bus.Subscribe(
		"tui:ws-integration",
		string(domain.EventAgentSessionStarted),
	)
	if err != nil {
		t.Fatalf("failed to subscribe: %v", err)
	}
	app.busSub = sub
	app.eventConsumer = NewEventConsumer(app, sub)

	// Create a session in the mock repo
	now := time.Now()
	session := domain.AgentSession{
		ID:          "session-1",
		WorkItemID:  "wi-1",
		WorkspaceID: "ws-integration",
		Kind:        domain.AgentSessionKindPlanning,
		Status:      domain.AgentSessionRunning,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	taskRepo.tasks["session-1"] = session
	sessionRepo.workItems["wi-1"] = domain.Session{
		ID:          "wi-1",
		WorkspaceID: "ws-integration",
		Title:       "Test Work Item",
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	// Create event payload matching what AgentSessionService.Start emits
	payload, _ := json.Marshal(map[string]any{
		"session":          session,
		"work_item_id":     "wi-1",
		"agent_session_id": "session-1",
	})

	// Publish EventAgentSessionStarted
	bus.Publish(context.Background(), domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   string(domain.EventAgentSessionStarted),
		WorkspaceID: "ws-integration",
		Payload:     string(payload),
		CreatedAt:   time.Now(),
	})

	// Advance the bridge to get DomainEventMsg
	bridgeCmd := app.eventConsumer.BridgeCmd()
	domMsg, ok := bridgeCmd().(DomainEventMsg)
	if !ok {
		t.Fatalf("expected DomainEventMsg from bridge, got %T", bridgeCmd())
	}

	// Verify the event has correct workspace ID
	if domMsg.Event.WorkspaceID != "ws-integration" {
		t.Errorf("WorkspaceID = %q, want %q", domMsg.Event.WorkspaceID, "ws-integration")
	}

	// Verify extractWorkItemID works
	workItemID := extractWorkItemID(domMsg.Event.Payload)
	if workItemID != "wi-1" {
		t.Errorf("extractWorkItemID = %q, want %q", workItemID, "wi-1")
	}

	// Process through App.Update - this should append LoadSessionCmd and LoadTasksForSessionCmd
	updatedApp, cmd := app.Update(domMsg)
	app = updatedApp.(*App)

	// Verify commands were returned
	if cmd == nil {
		t.Fatal("expected non-nil command from App.Update for DomainEventMsg")
	}

	// Execute the commands and verify they return the expected messages
	cmds := []tea.Cmd{cmd}

	// Execute all returned commands
	for len(cmds) > 0 {
		var nextCmds []tea.Cmd
		for _, c := range cmds {
			if c != nil {
				if msg := c(); msg != nil {
					switch m := msg.(type) {
					case SessionLoadedMsg:
						if m.WorkItem.ID != "wi-1" {
							t.Errorf("SessionLoadedMsg.WorkItem.ID = %q, want %q", m.WorkItem.ID, "wi-1")
						}
					case TasksForSessionLoadedMsg:
						if len(m.Sessions) != 1 {
							t.Errorf("TasksForSessionLoadedMsg.Sessions length = %d, want 1", len(m.Sessions))
						}
						if len(m.Sessions) > 0 && m.Sessions[0].ID != "session-1" {
							t.Errorf("TasksForSessionLoadedMsg.Sessions[0].ID = %q, want %q", m.Sessions[0].ID, "session-1")
						}
					case DomainEventMsg:
						// BridgeCmd - should be re-scheduled
						nextCmds = append(nextCmds, app.eventConsumer.BridgeCmd())
					}
				}
			}
		}
		cmds = nextCmds
	}
}

func TestDomainEventMsg_AgentSessionStartedAppliesTypedPayload(t *testing.T) {
	now := time.Now()
	task := domain.AgentSession{
		ID:          "impl-session-1",
		WorkItemID:  "wi-1",
		WorkspaceID: "ws-integration",
		Kind:        domain.AgentSessionKindImplementation,
		Status:      domain.AgentSessionRunning,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	payload, err := json.Marshal(map[string]any{
		"session":          task,
		"work_item_id":     "wi-1",
		"agent_session_id": task.ID,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	app := newTestApp(Services{WorkspaceID: "ws-integration", WorkspaceName: "integration-test", Settings: newTestSettingsService()})
	app.busSub = &event.Subscriber{}
	app.eventConsumer = NewEventConsumer(app, app.busSub)
	updated, cmd := app.Update(DomainEventMsg{Event: domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   string(domain.EventAgentSessionStarted),
		WorkspaceID: "ws-integration",
		Payload:     string(payload),
		CreatedAt:   now,
	}})
	app = updated.(*App)

	typed := findBatchMsg[TaskStartedMsg](t, cmd)
	updated, _ = app.Update(typed)
	app = updated.(*App)
	if len(app.sessions) != 1 {
		t.Fatalf("sessions length = %d, want 1", len(app.sessions))
	}
	if got := app.sessions[0]; got.ID != task.ID || got.Kind != domain.AgentSessionKindImplementation || got.Status != domain.AgentSessionRunning {
		t.Fatalf("session = %+v, want implementation running task %+v", got, task)
	}
}

func TestDomainEventMsg_ImplementationStartedReloadsWorkItemAndTasks(t *testing.T) {
	now := time.Now()
	workItem := domain.Session{ID: "wi-1", WorkspaceID: "ws-integration", Title: "Implement me", State: domain.SessionImplementing, CreatedAt: now, UpdatedAt: now}
	task := domain.AgentSession{ID: "impl-session-1", WorkItemID: "wi-1", WorkspaceID: "ws-integration", Kind: domain.AgentSessionKindImplementation, Status: domain.AgentSessionRunning, CreatedAt: now, UpdatedAt: now}
	taskRepo := &mockTaskRepoForSession{tasks: map[string]domain.AgentSession{task.ID: task}}
	sessionRepo := &mockSessionRepoForSession{workItems: map[string]domain.Session{workItem.ID: workItem}}
	bus := event.NewBus(event.BusConfig{})
	t.Cleanup(func() { bus.Close() })
	app := newTestApp(Services{
		WorkspaceID:   "ws-integration",
		WorkspaceName: "integration-test",
		Task: service.NewAgentSessionService(repository.NoopTransacter{
			Res: repository.Resources{AgentSessions: taskRepo},
		}, bus),
		Session: service.NewSessionService(repository.NoopTransacter{
			Res: repository.Resources{Sessions: sessionRepo},
		}, bus),
		Plan: service.NewPlanService(repository.NoopTransacter{
			Res: repository.Resources{Plans: &mockPlanRepo{}, SubPlans: &mockSubPlanRepo{}},
		}, bus),
		Settings: newTestSettingsService(),
	})
	app.busSub = &event.Subscriber{}
	app.eventConsumer = NewEventConsumer(app, app.busSub)
	// Use EventWorkItemImplementing with full work item payload
	payload, err := json.Marshal(map[string]any{"work_item_id": "wi-1", "workspace_id": "ws-integration", "session": workItem})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	_, cmd := app.Update(DomainEventMsg{Event: domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   string(domain.EventWorkItemImplementing),
		WorkspaceID: "ws-integration",
		Payload:     string(payload),
		CreatedAt:   now,
	}})

	foundSession := false
	foundTyped := false
	for _, msg := range drainBatchMsgs(t, cmd) {
		switch m := msg.(type) {
		case WorkItemUpdatedMsg:
			foundTyped = m.WorkItemID == "wi-1" && m.Session.State == domain.SessionImplementing
		case SessionLoadedMsg:
			foundSession = m.WorkItem.ID == "wi-1" && m.WorkItem.State == domain.SessionImplementing
		}
	}
	if !foundTyped {
		t.Fatal("EventWorkItemImplementing was not decoded into WorkItemUpdatedMsg")
	}
	if !foundSession {
		t.Fatal("WorkItemUpdatedMsg did not schedule work item reload")
	}
}

type mockPlanRepo struct{}

func (r *mockPlanRepo) Get(_ context.Context, _ string) (domain.Plan, error) {
	return domain.Plan{}, repository.ErrNotFound
}

func (r *mockPlanRepo) GetByWorkItemID(_ context.Context, _ string) (domain.Plan, error) {
	return domain.Plan{}, repository.ErrNotFound
}
func (r *mockPlanRepo) List(_ context.Context) ([]domain.Plan, error)        { return nil, nil }
func (r *mockPlanRepo) Create(_ context.Context, _ domain.Plan) error        { return nil }
func (r *mockPlanRepo) Update(_ context.Context, _ domain.Plan) error        { return nil }
func (r *mockPlanRepo) Delete(_ context.Context, _ string) error             { return nil }
func (r *mockPlanRepo) AppendFAQ(_ context.Context, _ domain.FAQEntry) error { return nil }

type mockSubPlanRepo struct{}

func (r *mockSubPlanRepo) Get(_ context.Context, _ string) (domain.TaskPlan, error) {
	return domain.TaskPlan{}, repository.ErrNotFound
}

func (r *mockSubPlanRepo) GetForUpdate(_ context.Context, _ string) (domain.TaskPlan, error) {
	return domain.TaskPlan{}, repository.ErrNotFound
}

func (r *mockSubPlanRepo) ListByPlanID(_ context.Context, _ string) ([]domain.TaskPlan, error) {
	return nil, nil
}
func (r *mockSubPlanRepo) Create(_ context.Context, _ domain.TaskPlan) error { return nil }
func (r *mockSubPlanRepo) Update(_ context.Context, _ domain.TaskPlan) error { return nil }
func (r *mockSubPlanRepo) Delete(_ context.Context, _ string) error          { return nil }

func findBatchMsg[T tea.Msg](t *testing.T, cmd tea.Cmd) T {
	t.Helper()
	if cmd == nil {
		var zero T
		t.Fatalf("nil command; want %T", zero)
		return zero
	}
	msg := cmd()
	if typed, ok := msg.(T); ok {
		return typed
	}
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		var zero T
		t.Fatalf("command returned %T; want %T", msg, zero)
		return zero
	}
	for i, sub := range batch {
		if i == 0 || sub == nil {
			continue // first command is the event bridge and waits for the next event
		}
		if subMsg := sub(); subMsg != nil {
			if typed, ok := subMsg.(T); ok {
				return typed
			}
		}
	}
	var zero T
	t.Fatalf("batch did not contain %T", zero)
	return zero
}

func drainBatchMsgs(t *testing.T, cmd tea.Cmd) []tea.Msg {
	t.Helper()
	if cmd == nil {
		return nil
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		if msg == nil {
			return nil
		}
		return []tea.Msg{msg}
	}
	msgs := make([]tea.Msg, 0, len(batch))
	for i, sub := range batch {
		if i == 0 || sub == nil {
			continue // first command is the event bridge and waits for the next event
		}
		if subMsg := sub(); subMsg != nil {
			msgs = append(msgs, subMsg)
		}
	}
	return msgs
}

// mockTaskRepoForSession implements repository.AgentSessionRepository for testing.
type mockTaskRepoForSession struct {
	tasks map[string]domain.AgentSession
}

func (r *mockTaskRepoForSession) Get(ctx context.Context, id string) (domain.AgentSession, error) {
	if t, ok := r.tasks[id]; ok {
		return t, nil
	}
	return domain.AgentSession{}, nil
}

func (r *mockTaskRepoForSession) ListByWorkItemID(ctx context.Context, workItemID string) ([]domain.AgentSession, error) {
	var result []domain.AgentSession
	for _, t := range r.tasks {
		if t.WorkItemID == workItemID {
			result = append(result, t)
		}
	}
	return result, nil
}

func (r *mockTaskRepoForSession) ListBySubPlanID(ctx context.Context, subPlanID string) ([]domain.AgentSession, error) {
	return nil, nil
}

func (r *mockTaskRepoForSession) ListByWorkspaceID(ctx context.Context, workspaceID string) ([]domain.AgentSession, error) {
	return nil, nil
}

func (r *mockTaskRepoForSession) ListActiveChildrenByParentID(ctx context.Context, parentID string) ([]domain.AgentSession, error) {
	return nil, nil
}

func (r *mockTaskRepoForSession) ListByOwnerInstanceID(ctx context.Context, instanceID string) ([]domain.AgentSession, error) {
	return nil, nil
}

func (r *mockTaskRepoForSession) SearchHistory(ctx context.Context, filter domain.SessionHistoryFilter) ([]domain.SessionHistoryEntry, error) {
	return nil, nil
}

func (r *mockTaskRepoForSession) Create(ctx context.Context, s domain.AgentSession) error {
	r.tasks[s.ID] = s
	return nil
}

func (r *mockTaskRepoForSession) Update(ctx context.Context, s domain.AgentSession) error {
	r.tasks[s.ID] = s
	return nil
}

func (r *mockTaskRepoForSession) Delete(ctx context.Context, id string) error {
	delete(r.tasks, id)
	return nil
}

func (r *mockTaskRepoForSession) UpdateResumeInfo(ctx context.Context, id string, info map[string]string) error {
	return nil
}

func (r *mockTaskRepoForSession) UpdateOwnerInstance(ctx context.Context, id string, instanceID string) error {
	return nil
}

func (r *mockTaskRepoForSession) UpdatePID(ctx context.Context, id string, pid int) error {
	return nil
}

// mockSessionRepoForSession implements repository.SessionRepository for testing.
type mockSessionRepoForSession struct {
	workItems map[string]domain.Session
}

func (r *mockSessionRepoForSession) Get(ctx context.Context, id string) (domain.Session, error) {
	if w, ok := r.workItems[id]; ok {
		return w, nil
	}
	return domain.Session{}, nil
}

func (r *mockSessionRepoForSession) List(ctx context.Context, filter repository.SessionFilter) ([]domain.Session, error) {
	return nil, nil
}

func (r *mockSessionRepoForSession) Create(ctx context.Context, item domain.Session) error {
	r.workItems[item.ID] = item
	return nil
}

func (r *mockSessionRepoForSession) Update(ctx context.Context, item domain.Session) error {
	r.workItems[item.ID] = item
	return nil
}

func (r *mockSessionRepoForSession) Delete(ctx context.Context, id string) error {
	delete(r.workItems, id)
	return nil
}

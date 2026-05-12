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
	taskRepo := &mockTaskRepoForSession{tasks: make(map[string]domain.Task)}
	sessionRepo := &mockSessionRepoForSession{workItems: make(map[string]domain.Session)}

	// Create services with mock repositories
	taskSvc := service.NewTaskService(repository.NoopTransacter{
		Res: repository.Resources{Tasks: taskRepo},
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
		Settings:      &SettingsService{},
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
	session := domain.Task{
		ID:          "session-1",
		WorkItemID:  "wi-1",
		WorkspaceID: "ws-integration",
		Phase:       domain.TaskPhasePlanning,
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

	// Create event payload matching what TaskService.Start emits
	payload, _ := json.Marshal(map[string]any{
		"session":      session,
		"work_item_id": "wi-1",
		"session_id":   "session-1",
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

// mockTaskRepoForSession implements repository.TaskRepository for testing.
type mockTaskRepoForSession struct {
	tasks map[string]domain.Task
}

func (r *mockTaskRepoForSession) Get(ctx context.Context, id string) (domain.Task, error) {
	if t, ok := r.tasks[id]; ok {
		return t, nil
	}
	return domain.Task{}, nil
}

func (r *mockTaskRepoForSession) ListByWorkItemID(ctx context.Context, workItemID string) ([]domain.Task, error) {
	var result []domain.Task
	for _, t := range r.tasks {
		if t.WorkItemID == workItemID {
			result = append(result, t)
		}
	}
	return result, nil
}

func (r *mockTaskRepoForSession) ListBySubPlanID(ctx context.Context, subPlanID string) ([]domain.Task, error) {
	return nil, nil
}

func (r *mockTaskRepoForSession) ListByWorkspaceID(ctx context.Context, workspaceID string) ([]domain.Task, error) {
	return nil, nil
}

func (r *mockTaskRepoForSession) ListByOwnerInstanceID(ctx context.Context, instanceID string) ([]domain.Task, error) {
	return nil, nil
}

func (r *mockTaskRepoForSession) SearchHistory(ctx context.Context, filter domain.SessionHistoryFilter) ([]domain.SessionHistoryEntry, error) {
	return nil, nil
}

func (r *mockTaskRepoForSession) Create(ctx context.Context, s domain.Task) error {
	r.tasks[s.ID] = s
	return nil
}

func (r *mockTaskRepoForSession) Update(ctx context.Context, s domain.Task) error {
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

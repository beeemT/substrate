package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/event"
	"github.com/beeemT/substrate/internal/repository"
)

type mockEventRepoForPlanning struct{}

func (m *mockEventRepoForPlanning) Create(ctx context.Context, event domain.SystemEvent) error {
	return nil
}

func (m *mockEventRepoForPlanning) ListByType(ctx context.Context, eventType string, limit int) ([]domain.SystemEvent, error) {
	return nil, nil
}

func (m *mockEventRepoForPlanning) ListByWorkspaceID(ctx context.Context, workspaceID string, limit int) ([]domain.SystemEvent, error) {
	return nil, nil
}

func TestStartPlanning_EmitsEvent(t *testing.T) {
	repo := NewMockWorkItemRepository()
	bus := event.NewBus(event.BusConfig{EventRepo: &mockEventRepoForPlanning{}})
	svc := NewSessionService(repository.NoopTransacter{Res: repository.Resources{Sessions: repo}}, bus)

	sub, err := bus.Subscribe("test", string(domain.EventWorkItemPlanning))
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer bus.Unsubscribe(sub.ID)

	item := domain.Session{
		ID:          "wi-planning-test",
		WorkspaceID: "ws-test",
		Title:       "Test Item",
		State:       domain.SessionIngested,
	}
	if err := repo.Create(context.Background(), item); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := svc.StartPlanning(context.Background(), "wi-planning-test"); err != nil {
		t.Fatalf("StartPlanning: %v", err)
	}

	select {
	case evt := <-sub.C:
		if evt.EventType != string(domain.EventWorkItemPlanning) {
			t.Errorf("event type = %q, want %q", evt.EventType, domain.EventWorkItemPlanning)
		}
		t.Logf("received event: %s", evt.ID)
	case <-time.After(2 * time.Second):
		t.Error("timeout waiting for EventWorkItemPlanning")
	}
}

func TestAgentSessionService_Start_EmitsEventWithCorrectPayload(t *testing.T) {
	repo := NewMockSessionRepository()
	bus := event.NewBus(event.BusConfig{EventRepo: &mockEventRepoForPlanning{}})
	svc := NewAgentSessionService(repository.NoopTransacter{Res: repository.Resources{AgentSessions: repo}}, bus)

	sub, err := bus.Subscribe("test", string(domain.EventAgentSessionStarted))
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer bus.Unsubscribe(sub.ID)

	session := domain.AgentSession{
		ID:             "session-payload-test",
		WorkItemID:     "wi-payload-test",
		SubPlanID:      "sp-test",
		WorkspaceID:    "ws-test",
		Phase:          domain.AgentSessionPhasePlanning,
		RepositoryName: "repo1",
		HarnessName:    "omp",
		Status:         domain.AgentSessionPending,
	}
	if err := repo.Create(context.Background(), session); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := svc.Start(context.Background(), "session-payload-test"); err != nil {
		t.Fatalf("Start: %v", err)
	}

	select {
	case evt := <-sub.C:
		var p struct {
			SessionID  string `json:"agent_session_id"`
			WorkItemID string `json:"work_item_id"`
		}
		if err := json.Unmarshal([]byte(evt.Payload), &p); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}
		if p.SessionID != "session-payload-test" {
			t.Errorf("session_id = %q, want %q", p.SessionID, "session-payload-test")
		}
		if p.WorkItemID != "wi-payload-test" {
			t.Errorf("work_item_id = %q, want %q", p.WorkItemID, "wi-payload-test")
		}
	case <-time.After(2 * time.Second):
		t.Error("timeout waiting for EventAgentSessionStarted")
	}
}

func TestAgentSessionService_Start_WithNilBus(t *testing.T) {
	repo := NewMockSessionRepository()
	svc := NewAgentSessionService(repository.NoopTransacter{Res: repository.Resources{AgentSessions: repo}}, newTestBus())

	session := domain.AgentSession{
		ID:             "session-nil-test",
		WorkItemID:     "wi-test",
		SubPlanID:      "sp-test",
		WorkspaceID:    "ws-test",
		Phase:          domain.AgentSessionPhasePlanning,
		RepositoryName: "repo1",
		HarnessName:    "omp",
		Status:         domain.AgentSessionPending,
	}
	if err := repo.Create(context.Background(), session); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := svc.Start(context.Background(), "session-nil-test"); err != nil {
		t.Fatalf("Start: %v", err)
	}
}

func TestSessionService_Transition_EmitsEventWithPayload(t *testing.T) {
	repo := NewMockWorkItemRepository()
	bus := event.NewBus(event.BusConfig{EventRepo: &mockEventRepoForPlanning{}})
	svc := NewSessionService(repository.NoopTransacter{Res: repository.Resources{Sessions: repo}}, bus)

	sub, err := bus.Subscribe("test", string(domain.EventWorkItemPlanning))
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer bus.Unsubscribe(sub.ID)

	item := domain.Session{
		ID:          "wi-transition-test",
		WorkspaceID: "ws-test",
		Title:       "Test Item",
		State:       domain.SessionIngested,
	}
	if err := repo.Create(context.Background(), item); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := svc.Transition(context.Background(), "wi-transition-test", domain.SessionPlanning); err != nil {
		t.Fatalf("Transition: %v", err)
	}

	select {
	case evt := <-sub.C:
		if evt.EventType != string(domain.EventWorkItemPlanning) {
			t.Errorf("event type = %q, want %q", evt.EventType, domain.EventWorkItemPlanning)
		}
		var p struct {
			WorkItemID string `json:"work_item_id"`
		}
		if err := json.Unmarshal([]byte(evt.Payload), &p); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}
		if p.WorkItemID != "wi-transition-test" {
			t.Errorf("work_item_id = %q, want %q", p.WorkItemID, "wi-transition-test")
		}
	case <-time.After(2 * time.Second):
		t.Error("timeout waiting for EventWorkItemPlanning")
	}
}

func TestSessionService_StartPlanning_WithNilBus(t *testing.T) {
	repo := NewMockWorkItemRepository()
	svc := NewSessionService(repository.NoopTransacter{Res: repository.Resources{Sessions: repo}}, newTestBus())

	item := domain.Session{
		ID:          "wi-nil-test",
		WorkspaceID: "ws-test",
		Title:       "Test Item",
		State:       domain.SessionIngested,
	}
	if err := repo.Create(context.Background(), item); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := svc.StartPlanning(context.Background(), "wi-nil-test"); err != nil {
		t.Fatalf("StartPlanning: %v", err)
	}
}

func TestSessionService_StartPlanning_AlreadyPlanning_Rollback(t *testing.T) {
	repo := NewMockWorkItemRepository()
	bus := event.NewBus(event.BusConfig{EventRepo: &mockEventRepoForPlanning{}})
	svc := NewSessionService(repository.NoopTransacter{Res: repository.Resources{Sessions: repo}}, bus)

	sub, err := bus.Subscribe("test", string(domain.EventWorkItemIngested), string(domain.EventWorkItemPlanning))
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer bus.Unsubscribe(sub.ID)

	item := domain.Session{
		ID:          "wi-rollback-test",
		WorkspaceID: "ws-test",
		Title:       "Test Item",
		State:       domain.SessionPlanning,
	}
	if err := repo.Create(context.Background(), item); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := svc.StartPlanning(context.Background(), "wi-rollback-test"); err != nil {
		t.Fatalf("StartPlanning: %v", err)
	}

	var gotIngested, gotPlanning bool
	for i := 0; i < 2; i++ {
		select {
		case evt := <-sub.C:
			switch evt.EventType {
			case string(domain.EventWorkItemIngested):
				gotIngested = true
			case string(domain.EventWorkItemPlanning):
				gotPlanning = true
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timeout waiting for event %d", i)
		}
	}

	if !gotIngested {
		t.Error("did not receive EventWorkItemIngested")
	}
	if !gotPlanning {
		t.Error("did not receive EventWorkItemPlanning")
	}
}

func TestAgentSessionService_Start_PayloadHasFlatFields(t *testing.T) {
	repo := NewMockSessionRepository()
	bus := event.NewBus(event.BusConfig{EventRepo: &mockEventRepoForPlanning{}})
	svc := NewAgentSessionService(repository.NoopTransacter{Res: repository.Resources{AgentSessions: repo}}, bus)

	sub, err := bus.Subscribe("test", string(domain.EventAgentSessionStarted))
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer bus.Unsubscribe(sub.ID)

	session := domain.AgentSession{
		ID:             "session-flat-test",
		WorkItemID:     "wi-flat-test",
		SubPlanID:      "sp-test",
		WorkspaceID:    "ws-test",
		Phase:          domain.AgentSessionPhasePlanning,
		RepositoryName: "repo1",
		HarnessName:    "omp",
		Status:         domain.AgentSessionPending,
	}
	if err := repo.Create(context.Background(), session); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := svc.Start(context.Background(), "session-flat-test"); err != nil {
		t.Fatalf("Start: %v", err)
	}

	select {
	case evt := <-sub.C:
		var p struct {
			SessionID  string `json:"agent_session_id"`
			WorkItemID string `json:"work_item_id"`
		}
		if err := json.Unmarshal([]byte(evt.Payload), &p); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}
		if p.SessionID == "" {
			t.Error("session_id is empty")
		}
		if p.WorkItemID == "" {
			t.Error("work_item_id is empty")
		}
		if p.SessionID != "session-flat-test" {
			t.Errorf("session_id = %q, want %q", p.SessionID, "session-flat-test")
		}
		if p.WorkItemID != "wi-flat-test" {
			t.Errorf("work_item_id = %q, want %q", p.WorkItemID, "wi-flat-test")
		}
	case <-time.After(2 * time.Second):
		t.Error("timeout waiting for EventAgentSessionStarted")
	}
}

func TestEmitStateChange_NilBus(t *testing.T) {
	repo := NewMockWorkItemRepository()
	svc := NewSessionService(repository.NoopTransacter{Res: repository.Resources{Sessions: repo}}, newTestBus())

	item := domain.Session{
		ID:          "wi-nil-bus-test",
		WorkspaceID: "ws-test",
		Title:       "Test Item",
		State:       domain.SessionIngested,
	}
	if err := repo.Create(context.Background(), item); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := svc.Transition(context.Background(), "wi-nil-bus-test", domain.SessionPlanning); err != nil {
		t.Fatalf("Transition: %v", err)
	}
}

func TestAgentSessionService_Start_WithRunningStatus(t *testing.T) {
	repo := NewMockSessionRepository()
	bus := event.NewBus(event.BusConfig{EventRepo: &mockEventRepoForPlanning{}})
	svc := NewAgentSessionService(repository.NoopTransacter{Res: repository.Resources{AgentSessions: repo}}, bus)

	sub, err := bus.Subscribe("test", string(domain.EventAgentSessionStarted))
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer bus.Unsubscribe(sub.ID)

	session := domain.AgentSession{
		ID:             "session-status-test",
		WorkItemID:     "wi-status-test",
		SubPlanID:      "sp-test",
		WorkspaceID:    "ws-test",
		Phase:          domain.AgentSessionPhasePlanning,
		RepositoryName: "repo1",
		HarnessName:    "omp",
		Status:         domain.AgentSessionPending,
	}
	if err := repo.Create(context.Background(), session); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := svc.Start(context.Background(), "session-status-test"); err != nil {
		t.Fatalf("Start: %v", err)
	}

	select {
	case evt := <-sub.C:
		var p struct {
			Session struct {
				Status string `json:"Status"`
			} `json:"session"`
		}
		if err := json.Unmarshal([]byte(evt.Payload), &p); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}
		if p.Session.Status != "running" {
			t.Errorf("session status = %q, want %q", p.Session.Status, "running")
		}
	case <-time.After(2 * time.Second):
		t.Error("timeout waiting for EventAgentSessionStarted")
	}
}

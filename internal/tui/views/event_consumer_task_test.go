package views

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/event"
)

func TestEventConsumer_toMsg_AgentSessionStarted(t *testing.T) {
	app := &App{}
	sub := &event.Subscriber{}
	ec := NewEventConsumer(app, sub)

	// Real payload from the database
	now := time.Now()
	startedAt := time.Now()
	task := domain.AgentSession{
		ID:          "01KR0WZPRFCRY356KBNH45ANKT",
		WorkItemID:  "01KR0NAP6ZW1AAZJGSN6DE4AEE",
		WorkspaceID: "01KP3EBN5HTYJ7RN86VQQ5EZQP",
		Phase:       domain.AgentSessionPhasePlanning,
		HarnessName: "omp",
		Status:      domain.AgentSessionRunning,
		StartedAt:   &startedAt,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	payload, _ := json.Marshal(map[string]any{
		"session":          task,
		"work_item_id":     task.WorkItemID,
		"agent_session_id": task.ID,
	})
	evt := domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   string(domain.EventAgentSessionStarted),
		WorkspaceID: task.WorkspaceID,
		Payload:     string(payload),
		CreatedAt:   now,
	}

	msg := ec.toMsg(evt)

	if msg == nil {
		t.Fatal("expected non-nil message")
	}
	typed, ok := msg.(TaskStartedMsg)
	if !ok {
		t.Fatalf("expected TaskStartedMsg, got %T", msg)
	}
	if typed.WorkItemID != task.WorkItemID {
		t.Errorf("WorkItemID = %q, want %q", typed.WorkItemID, task.WorkItemID)
	}
	if typed.Task.ID != task.ID {
		t.Errorf("Task.ID = %q, want %q", typed.Task.ID, task.ID)
	}
	if typed.Task.Phase != domain.AgentSessionPhasePlanning {
		t.Errorf("Task.Phase = %q, want %q", typed.Task.Phase, domain.AgentSessionPhasePlanning)
	}
	if typed.Task.Status != domain.AgentSessionRunning {
		t.Errorf("Task.Status = %q, want %q", typed.Task.Status, domain.AgentSessionRunning)
	}
}

func TestEventConsumer_toMsg_AgentSessionUpdated(t *testing.T) {
	app := &App{}
	sub := &event.Subscriber{}
	ec := NewEventConsumer(app, sub)

	now := time.Now()
	startedAt := time.Now()
	completedAt := time.Now()
	task := domain.AgentSession{
		ID:          "01KR0NBYZVA3NNQNT6AEKFMDBV",
		WorkItemID:  "01KR0NAP6ZW1AAZJGSN6DE4AEE",
		WorkspaceID: "01KP3EBN5HTYJ7RN86VQQ5EZQP",
		Phase:       domain.AgentSessionPhasePlanning,
		HarnessName: "omp",
		Status:      domain.AgentSessionFailed,
		StartedAt:   &startedAt,
		CompletedAt: &completedAt,
		ExitCode:    intPtr(1),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	payload, _ := json.Marshal(map[string]any{
		"session":          task,
		"work_item_id":     task.WorkItemID,
		"agent_session_id": task.ID,
	})
	evt := domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   string(domain.EventAgentSessionFailed),
		WorkspaceID: task.WorkspaceID,
		Payload:     string(payload),
		CreatedAt:   now,
	}

	msg := ec.toMsg(evt)

	if msg == nil {
		t.Fatal("expected non-nil message")
	}
	typed, ok := msg.(TaskUpdatedMsg)
	if !ok {
		t.Fatalf("expected TaskUpdatedMsg, got %T", msg)
	}
	if typed.WorkItemID != task.WorkItemID {
		t.Errorf("WorkItemID = %q, want %q", typed.WorkItemID, task.WorkItemID)
	}
	if typed.Task.ID != task.ID {
		t.Errorf("Task.ID = %q, want %q", typed.Task.ID, task.ID)
	}
	if typed.Task.Status != domain.AgentSessionFailed {
		t.Errorf("Task.Status = %q, want %q", typed.Task.Status, domain.AgentSessionFailed)
	}
}

func intPtr(i int) *int {
	return &i
}

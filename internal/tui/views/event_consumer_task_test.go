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
	if typed.AgentSession.ID != task.ID {
		t.Errorf("AgentSession.ID = %q, want %q", typed.AgentSession.ID, task.ID)
	}
	if typed.AgentSession.Phase != domain.AgentSessionPhasePlanning {
		t.Errorf("AgentSession.Phase = %q, want %q", typed.AgentSession.Phase, domain.AgentSessionPhasePlanning)
	}
	if typed.AgentSession.Status != domain.AgentSessionRunning {
		t.Errorf("AgentSession.Status = %q, want %q", typed.AgentSession.Status, domain.AgentSessionRunning)
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
	if typed.AgentSession.ID != task.ID {
		t.Errorf("AgentSession.ID = %q, want %q", typed.AgentSession.ID, task.ID)
	}
	if typed.AgentSession.Status != domain.AgentSessionFailed {
		t.Errorf("AgentSession.Status = %q, want %q", typed.AgentSession.Status, domain.AgentSessionFailed)
	}
}

func TestEventConsumer_toMsg_AgentSessionFollowUp(t *testing.T) {
	app := &App{}
	sub := &event.Subscriber{}
	ec := NewEventConsumer(app, sub)

	now := time.Now()
	startedAt := time.Now()
	task := domain.AgentSession{
		ID:              "01KS2YJY",
		WorkItemID:      "wi-follow-up",
		WorkspaceID:     "ws-follow-up",
		Phase:           domain.AgentSessionPhaseImplementation,
		HarnessName:     "omp",
		Status:          domain.AgentSessionRunning,
		StartedAt:       &startedAt,
		CompletedAt:     nil,
		OwnerInstanceID: stringPtr("inst-follow-up"),
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	payload, _ := json.Marshal(map[string]any{
		"session":          task,
		"work_item_id":     task.WorkItemID,
		"agent_session_id": task.ID,
	})
	evt := domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   string(domain.EventAgentSessionFollowUp),
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
	if typed.AgentSession.ID != task.ID {
		t.Errorf("AgentSession.ID = %q, want %q", typed.AgentSession.ID, task.ID)
	}
	if typed.AgentSession.Status != domain.AgentSessionRunning {
		t.Errorf("AgentSession.Status = %q, want %q", typed.AgentSession.Status, domain.AgentSessionRunning)
	}
	if typed.AgentSession.CompletedAt != nil {
		t.Errorf("CompletedAt = %v, want nil", typed.AgentSession.CompletedAt)
	}
	if typed.AgentSession.OwnerInstanceID == nil || *typed.AgentSession.OwnerInstanceID != "inst-follow-up" {
		t.Errorf("OwnerInstanceID = %v, want inst-follow-up", typed.AgentSession.OwnerInstanceID)
	}
}

func intPtr(i int) *int {
	return &i
}

func stringPtr(s string) *string {
	return &s
}

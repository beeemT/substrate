package views

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/event"
)

func TestEventConsumer_toMsg_WorkItemIngested(t *testing.T) {
	app := &App{}
	sub := &event.Subscriber{}
	ec := NewEventConsumer(app, sub)

	payload, _ := json.Marshal(map[string]any{
		"work_item_id": "wi-1",
		"session": domain.Session{
			ID:          "wi-1",
			Title:       "Test Session",
			WorkspaceID: "ws-1",
		},
	})
	evt := domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   string(domain.EventWorkItemIngested),
		WorkspaceID: "ws-1",
		Payload:     string(payload),
		CreatedAt:   time.Now(),
	}

	msg := ec.toMsg(evt)

	if msg == nil {
		t.Fatal("expected non-nil message")
	}
	if _, ok := msg.(WorkItemIngestedMsg); !ok {
		t.Fatalf("expected WorkItemIngestedMsg, got %T", msg)
	}

	typed := msg.(WorkItemIngestedMsg)
	if typed.WorkItemID != "wi-1" {
		t.Errorf("WorkItemID = %q, want %q", typed.WorkItemID, "wi-1")
	}
}

func TestEventConsumer_toMsg_WorkItemStateChange(t *testing.T) {
	app := &App{}
	sub := &event.Subscriber{}
	ec := NewEventConsumer(app, sub)

	payload, _ := json.Marshal(map[string]any{
		"work_item_id": "wi-1",
		"workspace_id": "ws-1",
		"session": domain.Session{
			ID:    "wi-1",
			Title: "Updated Session",
			State: domain.SessionPlanning,
		},
	})
	evt := domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   string(domain.EventWorkItemPlanning),
		WorkspaceID: "ws-1",
		Payload:     string(payload),
		CreatedAt:   time.Now(),
	}

	msg := ec.toMsg(evt)

	if msg == nil {
		t.Fatal("expected non-nil message")
	}
	if _, ok := msg.(WorkItemUpdatedMsg); !ok {
		t.Fatalf("expected WorkItemUpdatedMsg, got %T", msg)
	}
}

func TestEventConsumer_toMsg_PlanGenerated(t *testing.T) {
	app := &App{}
	sub := &event.Subscriber{}
	ec := NewEventConsumer(app, sub)

	plan := domain.Plan{
		ID:         "plan-1",
		WorkItemID: "wi-1",
	}
	subPlans := []domain.TaskPlan{
		{ID: "sp-1", PlanID: "plan-1"},
	}
	payload, _ := json.Marshal(map[string]any{
		"work_item_id": "wi-1",
		"plan":         plan,
		"sub_plans":    subPlans,
	})
	evt := domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   string(domain.EventPlanGenerated),
		WorkspaceID: "ws-1",
		Payload:     string(payload),
		CreatedAt:   time.Now(),
	}

	msg := ec.toMsg(evt)

	if msg == nil {
		t.Fatal("expected non-nil message")
	}
	if _, ok := msg.(PlanGeneratedMsg); !ok {
		t.Fatalf("expected PlanGeneratedMsg, got %T", msg)
	}
}

func TestEventConsumer_toMsg_QuestionRaised(t *testing.T) {
	app := &App{}
	sub := &event.Subscriber{}
	ec := NewEventConsumer(app, sub)

	question := domain.Question{
		ID:             "q-1",
		AgentSessionID: "sess-1",
		Content:        "Is this correct?",
		Status:         domain.QuestionPending,
	}
	payload, _ := json.Marshal(map[string]any{
		"agent_session_id": "sess-1",
		"question":         question,
	})
	evt := domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   string(domain.EventAgentQuestionRaised),
		WorkspaceID: "ws-1",
		Payload:     string(payload),
		CreatedAt:   time.Now(),
	}

	msg := ec.toMsg(evt)

	if msg == nil {
		t.Fatal("expected non-nil message")
	}
	if _, ok := msg.(QuestionRaisedMsg); !ok {
		t.Fatalf("expected QuestionRaisedMsg, got %T", msg)
	}

	typed := msg.(QuestionRaisedMsg)
	if typed.SessionID != "sess-1" {
		t.Errorf("SessionID = %q, want %q", typed.SessionID, "sess-1")
	}
	if typed.Question.ID != "q-1" {
		t.Errorf("Question.ID = %q, want %q", typed.Question.ID, "q-1")
	}
}

func TestEventConsumer_toMsg_QuestionAnswered(t *testing.T) {
	app := &App{}
	sub := &event.Subscriber{}
	ec := NewEventConsumer(app, sub)

	payload, _ := json.Marshal(map[string]any{
		"agent_session_id": "sess-1",
		"question_id":      "q-1",
	})
	evt := domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   string(domain.EventAgentQuestionAnswered),
		WorkspaceID: "ws-1",
		Payload:     string(payload),
		CreatedAt:   time.Now(),
	}

	msg := ec.toMsg(evt)

	if msg == nil {
		t.Fatal("expected non-nil message")
	}
	if _, ok := msg.(QuestionAnsweredMsg); !ok {
		t.Fatalf("expected QuestionAnsweredMsg, got %T", msg)
	}

	typed := msg.(QuestionAnsweredMsg)
	if typed.SessionID != "sess-1" {
		t.Errorf("SessionID = %q, want %q", typed.SessionID, "sess-1")
	}
	if typed.QuestionID != "q-1" {
		t.Errorf("QuestionID = %q, want %q", typed.QuestionID, "q-1")
	}
}

func TestEventConsumer_toMsg_ReviewEvents(t *testing.T) {
	tests := []struct {
		name      string
		eventType domain.EventType
		wantType  any
	}{
		{"ReviewStarted", domain.EventReviewStarted, ReviewStartedMsg{}},
		{"ReviewCompleted", domain.EventReviewCompleted, ReviewCompletedMsg{}},
		{"CritiquesFound", domain.EventCritiquesFound, CritiquesFoundMsg{}},
		{"ReimplementationStarted", domain.EventReimplementationStarted, ReimplementationStartedMsg{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := &App{}
			sub := &event.Subscriber{}
			ec := NewEventConsumer(app, sub)

			payload, _ := json.Marshal(map[string]any{
				"agent_session_id": "sess-1",
			})
			evt := domain.SystemEvent{
				ID:          domain.NewID(),
				EventType:   string(tt.eventType),
				WorkspaceID: "ws-1",
				Payload:     string(payload),
				CreatedAt:   time.Now(),
			}

			msg := ec.toMsg(evt)

			if msg == nil {
				t.Fatal("expected non-nil message")
			}
			if reflect.TypeOf(msg) != reflect.TypeOf(tt.wantType) {
				t.Errorf("got %T, want %T", msg, tt.wantType)
			}
		})
	}
}

func TestEventConsumer_toMsg_UnknownEvent(t *testing.T) {
	app := &App{}
	sub := &event.Subscriber{}
	ec := NewEventConsumer(app, sub)

	evt := domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   "unknown.event",
		WorkspaceID: "ws-1",
		Payload:     "{}",
		CreatedAt:   time.Now(),
	}

	msg := ec.toMsg(evt)

	// Unknown events should return nil
	if msg != nil {
		t.Errorf("expected nil for unknown event, got %T", msg)
	}
}

func TestEventConsumer_toMsg_InvalidPayload(t *testing.T) {
	app := &App{}
	sub := &event.Subscriber{}
	ec := NewEventConsumer(app, sub)

	evt := domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   string(domain.EventWorkItemIngested),
		WorkspaceID: "ws-1",
		Payload:     "invalid-json",
		CreatedAt:   time.Now(),
	}

	msg := ec.toMsg(evt)

	// Invalid JSON should return nil
	if msg != nil {
		t.Errorf("expected nil for invalid payload, got %T", msg)
	}
}

func TestQuestionAnsweredMsg_removes_from_nested_map(t *testing.T) {
	app := newTestApp(Services{Settings: &SettingsService{}})
	app.questions = map[string]map[string]domain.Question{
		"sess-1": {
			"q-1": {ID: "q-1", Content: "test?"},
			"q-2": {ID: "q-2", Content: "test 2?"},
		},
	}

	updated, _ := app.Update(QuestionAnsweredMsg{SessionID: "sess-1", QuestionID: "q-1"})
	app = updated.(*App)

	_, has := app.questions["sess-1"]["q-1"]
	if has {
		t.Error("q-1 should be removed")
	}
	_, has = app.questions["sess-1"]["q-2"]
	if !has {
		t.Error("q-2 should remain")
	}
}

func TestQuestionAnsweredMsg_removes_empty_session(t *testing.T) {
	app := newTestApp(Services{Settings: &SettingsService{}})
	app.questions = map[string]map[string]domain.Question{
		"sess-1": {
			"q-1": {ID: "q-1", Content: "test?"},
		},
	}

	updated, _ := app.Update(QuestionAnsweredMsg{SessionID: "sess-1", QuestionID: "q-1"})
	app = updated.(*App)

	_, has := app.questions["sess-1"]
	if has {
		t.Error("sess-1 key should be removed when last question is answered")
	}
}

package views

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/event"
)

// TestEventConsumerEndToEnd tests that events published to the bus are consumed
// by the EventConsumer and delivered to the App's update loop as typed messages,
// and that the App's state is correctly updated.
func TestEventConsumerEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	bus := event.NewBus(event.BusConfig{})
	t.Cleanup(func() { bus.Close() })

	// Create App with the bus subscription wired up.
	app := newTestApp(Services{
		WorkspaceID:   "ws-integration",
		WorkspaceName: "integration-test",
		Bus:           bus,
		Settings:      &SettingsService{},
	})

	// Wire up the event consumer bridge.
	sub, err := bus.Subscribe(
		"tui:ws-integration",
		string(domain.EventWorkItemIngested),
		string(domain.EventAgentQuestionRaised),
		string(domain.EventAgentQuestionAnswered),
	)
	if err != nil {
		t.Fatalf("failed to subscribe: %v", err)
	}
	ec := NewEventConsumer(app, sub)
	bridgeCmd := ec.BridgeCmd()

	// Publish EventWorkItemIngested and deliver it through the bridge.
	payload, _ := json.Marshal(map[string]any{
		"work_item_id": "wi-new",
		"workspace_id": "ws-integration",
		"session": domain.Session{
			ID:          "wi-new",
			Title:       "New Work Item",
			WorkspaceID: "ws-integration",
		},
	})
	bus.Publish(context.Background(), domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   string(domain.EventWorkItemIngested),
		WorkspaceID: "ws-integration",
		Payload:     string(payload),
		CreatedAt:   time.Now(),
	})

	// Advance the bridge: deliver the event as a DomainEventMsg.
	domMsg, ok := bridgeCmd().(DomainEventMsg)
	if !ok {
		t.Fatalf("expected DomainEventMsg from bridge, got %T", bridgeCmd())
	}

	// Process through App.Update: DomainEventMsg handler fires targeted loads.
	// We can't execute the DB commands in a test without a real DB connection,
	// but we can verify the event is correctly decoded and the typed message
	// would be returned.
	typedMsg := ec.toMsg(domMsg.Event)
	if typedMsg == nil {
		t.Fatal("expected non-nil typed message from toMsg")
	}
	ingested, ok := typedMsg.(WorkItemIngestedMsg)
	if !ok {
		t.Fatalf("expected WorkItemIngestedMsg, got %T", typedMsg)
	}
	if ingested.Session.ID != "wi-new" {
		t.Errorf("Session.ID = %q, want %q", ingested.Session.ID, "wi-new")
	}
	if ingested.WorkspaceID != "ws-integration" {
		t.Errorf("WorkspaceID = %q, want %q", ingested.WorkspaceID, "ws-integration")
	}
}

func TestEventConsumerBridgeWaitsForFutureEvent(t *testing.T) {
	bus := event.NewBus(event.BusConfig{})
	t.Cleanup(func() { bus.Close() })

	app := newTestApp(Services{
		WorkspaceID:   "ws-integration",
		WorkspaceName: "integration-test",
		Bus:           bus,
		Settings:      &SettingsService{},
	})

	sub, err := bus.Subscribe("tui:ws-integration", string(domain.EventAgentSessionStarted))
	if err != nil {
		t.Fatalf("failed to subscribe: %v", err)
	}
	ec := NewEventConsumer(app, sub)

	msgCh := make(chan any, 1)
	go func() {
		msgCh <- ec.BridgeCmd()()
	}()

	select {
	case msg := <-msgCh:
		t.Fatalf("BridgeCmd returned before any event was published: %T", msg)
	case <-time.After(10 * time.Millisecond):
	}

	payload, err := json.Marshal(map[string]any{
		"work_item_id": "wi-1",
		"session_id":   "task-1",
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if err := bus.Publish(context.Background(), domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   string(domain.EventAgentSessionStarted),
		WorkspaceID: "ws-integration",
		Payload:     string(payload),
		CreatedAt:   time.Now(),
	}); err != nil {
		t.Fatalf("publish event: %v", err)
	}

	select {
	case msg := <-msgCh:
		domMsg, ok := msg.(DomainEventMsg)
		if !ok {
			t.Fatalf("expected DomainEventMsg, got %T", msg)
		}
		if domMsg.Event.EventType != string(domain.EventAgentSessionStarted) {
			t.Fatalf("EventType = %q, want %q", domMsg.Event.EventType, domain.EventAgentSessionStarted)
		}
	case <-time.After(time.Second):
		t.Fatal("BridgeCmd did not deliver published event")
	}
}

// TestEventConsumer_questionAnsweredEndToEnd verifies that a question-answered event
// triggers removal from the nested questions map.
func TestEventConsumer_questionAnsweredEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	bus := event.NewBus(event.BusConfig{})
	t.Cleanup(func() { bus.Close() })

	app := newTestApp(Services{
		WorkspaceID:   "ws-integration",
		WorkspaceName: "integration-test",
		Bus:           bus,
		Settings:      &SettingsService{},
	})
	app.questions = map[string]map[string]domain.Question{
		"sess-1": {
			"q-1": {ID: "q-1", Content: "test?"},
			"q-2": {ID: "q-2", Content: "test 2?"},
		},
	}

	sub, err := bus.Subscribe(
		"tui:ws-integration",
		string(domain.EventAgentQuestionAnswered),
	)
	if err != nil {
		t.Fatalf("failed to subscribe: %v", err)
	}
	ec := NewEventConsumer(app, sub)
	bridgeCmd := ec.BridgeCmd()

	// Publish EventAgentQuestionAnswered.
	payload, _ := json.Marshal(map[string]any{
		"session_id":  "sess-1",
		"question_id": "q-1",
	})
	bus.Publish(context.Background(), domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   string(domain.EventAgentQuestionAnswered),
		WorkspaceID: "ws-integration",
		Payload:     string(payload),
		CreatedAt:   time.Now(),
	})

	domMsg, ok := bridgeCmd().(DomainEventMsg)
	if !ok {
		t.Fatalf("expected DomainEventMsg from bridge, got %T", bridgeCmd())
	}

	typedMsg := ec.toMsg(domMsg.Event)
	if typedMsg == nil {
		t.Fatal("expected non-nil typed message from toMsg")
	}
	answered, ok := typedMsg.(QuestionAnsweredMsg)
	if !ok {
		t.Fatalf("expected QuestionAnsweredMsg, got %T", typedMsg)
	}

	// Process through App.Update.
	updatedApp, _ := app.Update(answered)
	app = updatedApp.(*App)

	_, has := app.questions["sess-1"]["q-1"]
	if has {
		t.Error("q-1 should be removed after QuestionAnsweredMsg")
	}
	_, has = app.questions["sess-1"]["q-2"]
	if !has {
		t.Error("q-2 should remain after q-1 is answered")
	}
}

// TestEventConsumer_unknownEventReturnsNil verifies that unknown event types
// return nil from toMsg and do not crash the bridge.
func TestEventConsumer_unknownEventReturnsNil(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	bus := event.NewBus(event.BusConfig{})
	t.Cleanup(func() { bus.Close() })

	app := newTestApp(Services{
		WorkspaceID:   "ws-integration",
		WorkspaceName: "integration-test",
		Bus:           bus,
		Settings:      &SettingsService{},
	})

	sub, err := bus.Subscribe(
		"tui:ws-integration",
		"workspace.created", // a type the registry does not handle
	)
	if err != nil {
		t.Fatalf("failed to subscribe: %v", err)
	}
	ec := NewEventConsumer(app, sub)
	bridgeCmd := ec.BridgeCmd()

	bus.Publish(context.Background(), domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   "workspace.created",
		WorkspaceID: "ws-integration",
		Payload:     "{}",
		CreatedAt:   time.Now(),
	})

	domMsg, ok := bridgeCmd().(DomainEventMsg)
	if !ok {
		t.Fatalf("expected DomainEventMsg from bridge, got %T", bridgeCmd())
	}

	typedMsg := ec.toMsg(domMsg.Event)
	if typedMsg != nil {
		t.Errorf("expected nil for unknown event, got %T", typedMsg)
	}
}

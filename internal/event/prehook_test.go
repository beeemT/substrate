package event

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/beeemT/substrate/internal/domain"
)

// Test that pre-hook events are NOT persisted when a pre-hook rejects.
func TestBus_PreHookEvent_NotPersistedOnReject(t *testing.T) {
	repo := &mockEventRepo{}
	bus := NewBus(BusConfig{EventRepo: repo})
	defer bus.Close()

	// Register a pre-hook that always rejects
	bus.RegisterPreHook(HookConfig{Name: "reject"}, func(_ context.Context, _ domain.SystemEvent) error {
		return errors.New("rejected")
	})

	// WorktreeCreating is a pre-hook event by default
	event := domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   string(domain.EventWorktreeCreating),
		WorkspaceID: "ws-123",
		Payload:     `{}`,
		CreatedAt:   domain.Now(),
	}

	err := bus.Publish(context.Background(), event)
	if err == nil {
		t.Fatal("expected error from pre-hook rejection")
	}

	// Event should NOT be persisted
	repo.mu.Lock()
	persisted := len(repo.events)
	repo.mu.Unlock()
	if persisted != 0 {
		t.Errorf("event was persisted despite pre-hook rejection; got %d events", persisted)
	}
}

// Test that regular events ARE persisted even when a pre-hook rejects.
func TestBus_RegularEvent_PersistedOnReject(t *testing.T) {
	repo := &mockEventRepo{}
	bus := NewBus(BusConfig{EventRepo: repo})
	defer bus.Close()

	sub, err := bus.Subscribe("sub-1")
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}
	defer bus.Unsubscribe("sub-1")

	// Register a pre-hook that always rejects
	bus.RegisterPreHook(HookConfig{Name: "reject"}, func(_ context.Context, _ domain.SystemEvent) error {
		return errors.New("rejected")
	})

	// test.event is NOT a pre-hook event (it's a regular event)
	event := domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   "test.event",
		WorkspaceID: "ws-123",
		Payload:     `{}`,
		CreatedAt:   domain.Now(),
	}

	err = bus.Publish(context.Background(), event)
	if err == nil {
		t.Fatal("expected error from pre-hook rejection")
	}

	// Event SHOULD be persisted (it represents a fact that already happened)
	repo.mu.Lock()
	persisted := len(repo.events)
	repo.mu.Unlock()
	if persisted != 1 {
		t.Errorf("regular event should be persisted even on pre-hook rejection; got %d events", persisted)
	}

	// Subscriber should NOT receive the event (dispatch was aborted)
	select {
	case <-sub.C:
		t.Error("subscriber should not receive event when pre-hook rejects")
	default:
		// expected
	}
}

// Test that pre-hook events ARE persisted when all pre-hooks pass.
func TestBus_PreHookEvent_PersistedOnSuccess(t *testing.T) {
	repo := &mockEventRepo{}
	bus := NewBus(BusConfig{EventRepo: repo})
	defer bus.Close()

	sub, err := bus.Subscribe("sub-1", string(domain.EventWorktreeCreating))
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}
	defer bus.Unsubscribe("sub-1")

	// Register a pre-hook that passes
	bus.RegisterPreHook(HookConfig{Name: "pass"}, func(_ context.Context, _ domain.SystemEvent) error {
		return nil
	})

	// WorktreeCreating is a pre-hook event
	event := domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   string(domain.EventWorktreeCreating),
		WorkspaceID: "ws-123",
		Payload:     `{}`,
		CreatedAt:   domain.Now(),
	}

	err = bus.Publish(context.Background(), event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Event should be persisted
	repo.mu.Lock()
	persisted := len(repo.events)
	repo.mu.Unlock()
	if persisted != 1 {
		t.Errorf("event should be persisted after pre-hooks pass; got %d events", persisted)
	}

	// Subscriber should receive the event
	select {
	case got := <-sub.C:
		if got.ID != event.ID {
			t.Errorf("got event ID %q, want %q", got.ID, event.ID)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("timed out waiting for event")
	}
}

// Test IsPreHookEvent reports correctly.
func TestBus_IsPreHookEvent(t *testing.T) {
	bus := NewBus(BusConfig{})
	defer bus.Close()

	// WorktreeCreating is a pre-hook event by default
	if !bus.IsPreHookEvent(string(domain.EventWorktreeCreating)) {
		t.Error("WorktreeCreating should be a pre-hook event")
	}

	// WorktreeCreated is NOT a pre-hook event
	if bus.IsPreHookEvent(string(domain.EventWorktreeCreated)) {
		t.Error("WorktreeCreated should NOT be a pre-hook event")
	}

	// Random event type is not a pre-hook event
	if bus.IsPreHookEvent("random.event") {
		t.Error("random.event should NOT be a pre-hook event")
	}
}

// Test RegisterPreHookType adds event types to the pre-hook types set.
func TestBus_RegisterPreHookType(t *testing.T) {
	bus := NewBus(BusConfig{})
	defer bus.Close()

	// Initially not a pre-hook event
	if bus.IsPreHookEvent("custom.prehook") {
		t.Error("custom.prehook should not be a pre-hook event initially")
	}

	// Register as pre-hook event
	bus.RegisterPreHookType("custom.prehook")

	if !bus.IsPreHookEvent("custom.prehook") {
		t.Error("custom.prehook should now be a pre-hook event")
	}

	// WorktreeCreating should still be a pre-hook event
	if !bus.IsPreHookEvent(string(domain.EventWorktreeCreating)) {
		t.Error("WorktreeCreating should still be a pre-hook event")
	}
}

package event

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/beeemT/substrate/internal/domain"
)

// TestBus_PreHook_AbortDispatch verifies that a pre-hook returning an error
// aborts dispatch (subscriber does not receive the event).
func TestBus_PreHook_AbortDispatch(t *testing.T) {
	repo := &mockEventRepo{}
	bus := NewBus(BusConfig{EventRepo: repo})
	defer bus.Close()

	sub, err := bus.Subscribe("sub-1")
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}
	defer bus.Unsubscribe("sub-1")

	bus.RegisterPreHook(HookConfig{Name: "reject"}, func(_ context.Context, _ domain.SystemEvent) error {
		return errors.New("rejected")
	})

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

	// Event IS persisted (it represents a fact that already happened)
	repo.mu.Lock()
	persisted := len(repo.events)
	repo.mu.Unlock()
	if persisted != 1 {
		t.Errorf("event should be persisted on pre-hook rejection; got %d events", persisted)
	}

	// Subscriber should NOT receive the event (dispatch was aborted)
	select {
	case <-sub.C:
		t.Error("subscriber should not receive event when pre-hook rejects")
	default:
		// expected
	}
}

// TestBus_PreHook_CallOrder verifies pre-hooks are called in registration order.
func TestBus_PreHook_CallOrder(t *testing.T) {
	bus := NewBus(BusConfig{})
	defer bus.Close()

	var order []string
	var mu sync.Mutex
	var callCh chan string

	bus.RegisterPreHook(HookConfig{Name: "first"}, func(_ context.Context, _ domain.SystemEvent) error {
		mu.Lock()
		defer mu.Unlock()
		order = append(order, "first")
		select {
		case callCh <- "first":
		default:
		}
		return nil
	})
	bus.RegisterPreHook(HookConfig{Name: "second"}, func(_ context.Context, _ domain.SystemEvent) error {
		mu.Lock()
		order = append(order, "second")
		mu.Unlock()
		return nil
	})

	callCh = make(chan string, 2)
	evt := domain.SystemEvent{ID: domain.NewID(), EventType: "test"}
	if err := bus.Publish(context.Background(), evt); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	// Wait for first hook to complete
	<-callCh

	mu.Lock()
	defer mu.Unlock()
	if len(order) < 2 {
		t.Fatalf("expected at least 2 hooks called, got %d: %v", len(order), order)
	}
	if order[0] != "first" || order[1] != "second" {
		t.Errorf("call order = %v, want [first, second]", order)
	}
}

// TestBus_PreHook_Concurrent verifies pre-hooks run synchronously (not concurrently).
func TestBus_PreHook_Concurrent(t *testing.T) {
	bus := NewBus(BusConfig{})
	defer bus.Close()

	var active atomic.Int32
	var maxConcurrent atomic.Int32

	for i := 0; i < 5; i++ {
		bus.RegisterPreHook(HookConfig{Name: "check"}, func(_ context.Context, _ domain.SystemEvent) error {
			current := active.Add(1)
			for current > maxConcurrent.Load() {
				maxConcurrent.Store(current)
			}
			time.Sleep(10 * time.Millisecond)
			active.Add(-1)
			return nil
		})
	}

	evt := domain.SystemEvent{ID: domain.NewID(), EventType: "test"}
	if err := bus.Publish(context.Background(), evt); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	if maxConcurrent.Load() != 1 {
		t.Errorf("max concurrent pre-hooks = %d, want 1 (hooks should be serial, not parallel)", maxConcurrent.Load())
	}
}

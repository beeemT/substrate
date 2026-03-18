package event

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/repository"
)

// mockEventRepo is a mock EventRepository for testing.
type mockEventRepo struct {
	events []domain.SystemEvent
	mu     sync.Mutex
	err    error
}

func (m *mockEventRepo) Create(_ context.Context, e domain.SystemEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	m.events = append(m.events, e)
	return nil
}

func (m *mockEventRepo) ListByType(_ context.Context, _ string, _ int) ([]domain.SystemEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.events, nil
}

func (m *mockEventRepo) ListByWorkspaceID(_ context.Context, _ string, _ int) ([]domain.SystemEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.events, nil
}

func newTestEvent() domain.SystemEvent {
	return domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   "test.event",
		WorkspaceID: "ws-123",
		Payload:     `{"test": true}`,
		CreatedAt:   domain.Now(),
	}
}

func TestBus_Publish_DispatchesToSubscribers(t *testing.T) {
	repo := &mockEventRepo{}
	bus := NewBus(BusConfig{EventRepo: repo})
	defer bus.Close()

	sub, err := bus.Subscribe("sub-1", "test.event")
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}
	defer bus.Unsubscribe("sub-1")

	event := newTestEvent()
	ctx := context.Background()

	if err := bus.Publish(ctx, event); err != nil {
		t.Fatalf("Publish failed: %v", err)
	}

	select {
	case got := <-sub.C:
		if got.ID != event.ID {
			t.Errorf("got event ID %q, want %q", got.ID, event.ID)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("timed out waiting for event")
	}

	// Verify event was persisted
	if len(repo.events) != 1 {
		t.Errorf("repo has %d events, want 1", len(repo.events))
	}
}

func TestBus_Publish_TopicFiltering(t *testing.T) {
	bus := NewBus(BusConfig{})
	defer bus.Close()

	subAll, err := bus.Subscribe("sub-all")
	if err != nil {
		t.Fatalf("Subscribe sub-all failed: %v", err)
	}
	subTest, err := bus.Subscribe("sub-test", "test.event")
	if err != nil {
		t.Fatalf("Subscribe sub-test failed: %v", err)
	}
	subOther, err := bus.Subscribe("sub-other", "other.event")
	if err != nil {
		t.Fatalf("Subscribe sub-other failed: %v", err)
	}
	defer bus.Unsubscribe("sub-all")
	defer bus.Unsubscribe("sub-test")
	defer bus.Unsubscribe("sub-other")

	event := newTestEvent()
	event.EventType = "test.event"
	ctx := context.Background()

	if err := bus.Publish(ctx, event); err != nil {
		t.Fatalf("Publish failed: %v", err)
	}

	// sub-all should receive
	select {
	case <-subAll.C:
		// expected
	case <-time.After(100 * time.Millisecond):
		t.Error("sub-all did not receive event")
	}

	// sub-test should receive
	select {
	case <-subTest.C:
		// expected
	case <-time.After(100 * time.Millisecond):
		t.Error("sub-test did not receive event")
	}

	// sub-other should NOT receive
	select {
	case <-subOther.C:
		t.Error("sub-other should not have received event")
	default:
		// expected
	}
}

func TestBus_PreHook_Aborts(t *testing.T) {
	bus := NewBus(BusConfig{})
	defer bus.Close()

	sub, err := bus.Subscribe("sub-1")
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}
	defer bus.Unsubscribe("sub-1")

	// Register pre-hook that aborts
	var hookCalled int32
	bus.RegisterPreHook(HookConfig{Name: "abort"}, func(_ context.Context, _ domain.SystemEvent) error {
		atomic.AddInt32(&hookCalled, 1)

		return errors.New("abort")
	})
	event := newTestEvent()
	ctx := context.Background()

	err = bus.Publish(ctx, event)
	if err == nil {
		t.Fatal("Publish should have returned error")
	}

	if atomic.LoadInt32(&hookCalled) != 1 {
		t.Error("pre-hook was not called")
	}

	// Subscriber should NOT receive the event
	select {
	case <-sub.C:
		t.Error("subscriber should not have received event after pre-hook abort")
	default:
		// expected
	}
}

func TestBus_PreHook_AbortPreventsDelivery(t *testing.T) {
	bus := NewBus(BusConfig{})
	defer bus.Close()

	sub, err := bus.Subscribe("sub-1", "test.event")
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}
	defer bus.Unsubscribe("sub-1")

	// Pre-hook that aborts
	bus.RegisterPreHook(HookConfig{Name: "abort"}, func(_ context.Context, _ domain.SystemEvent) error {
		return errors.New("abort")
	})

	event := newTestEvent()
	ctx := context.Background()

	err = bus.Publish(ctx, event)
	if err == nil {
		t.Fatal("expected error from pre-hook abort")
	}

	// Verify subscriber did NOT receive the event
	select {
	case <-sub.C:
		t.Error("subscriber received event despite pre-hook abort")
	default:
		// expected - no event
	}
}

func TestBus_PreHook_Timeout(t *testing.T) {
	bus := NewBus(BusConfig{})
	defer bus.Close()

	// Pre-hook that sleeps for 5s with 100ms timeout
	bus.RegisterPreHook(HookConfig{Name: "slow", Timeout: 100 * time.Millisecond}, func(_ context.Context, _ domain.SystemEvent) error {
		time.Sleep(5 * time.Second) // will exceed timeout
		return nil
	})

	event := newTestEvent()
	ctx := context.Background()

	start := time.Now()
	err := bus.Publish(ctx, event)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error")
	}

	// Should timeout around 100ms, not 5s
	if elapsed > 500*time.Millisecond {
		t.Errorf("timeout took %v, should be around 100ms", elapsed)
	}

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected DeadlineExceeded, got: %v", err)
	}
}

func TestBus_PreHook_ExecutesInOrder(t *testing.T) {
	bus := NewBus(BusConfig{})
	defer bus.Close()

	var order []string
	var mu sync.Mutex

	bus.RegisterPreHook(HookConfig{Name: "first"}, func(_ context.Context, _ domain.SystemEvent) error {
		mu.Lock()
		order = append(order, "first")
		mu.Unlock()
		return nil
	})

	bus.RegisterPreHook(HookConfig{Name: "second"}, func(_ context.Context, _ domain.SystemEvent) error {
		mu.Lock()
		order = append(order, "second")
		mu.Unlock()
		return nil
	})

	event := newTestEvent()
	ctx := context.Background()

	if err := bus.Publish(ctx, event); err != nil {
		t.Fatalf("Publish failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(order) != 2 || order[0] != "first" || order[1] != "second" {
		t.Errorf("hook order = %v, want [first, second]", order)
	}
}

func TestBus_PostHook_Async(t *testing.T) {
	bus := NewBus(BusConfig{})
	defer bus.Close()

	var postHookCalled int32
	var wg sync.WaitGroup
	wg.Add(1)

	bus.RegisterPostHook(HookConfig{Name: "post"}, func(_ context.Context, _ domain.SystemEvent) error {
		atomic.AddInt32(&postHookCalled, 1)
		wg.Done()
		return nil
	})

	event := newTestEvent()
	ctx := context.Background()

	if err := bus.Publish(ctx, event); err != nil {
		t.Fatalf("Publish failed: %v", err)
	}

	// Post-hook runs async, wait for it
	wg.Wait()

	if atomic.LoadInt32(&postHookCalled) != 1 {
		t.Error("post-hook was not called")
	}
}

func TestBus_PostHook_Timeout(t *testing.T) {
	bus := NewBus(BusConfig{})
	defer bus.Close()

	// Post-hook that sleeps with short timeout - should not block Publish
	bus.RegisterPostHook(HookConfig{Name: "slow-post", Timeout: 100 * time.Millisecond}, func(_ context.Context, _ domain.SystemEvent) error {
		time.Sleep(5 * time.Second) // will timeout
		return nil
	})

	event := newTestEvent()
	ctx := context.Background()

	// Publish should return quickly (post-hooks are async)
	start := time.Now()
	if err := bus.Publish(ctx, event); err != nil {
		t.Fatalf("Publish failed: %v", err)
	}
	elapsed := time.Since(start)

	// Publish should return immediately, not wait for post-hook
	if elapsed > 100*time.Millisecond {
		t.Errorf("Publish took %v, should return immediately", elapsed)
	}
}

func TestBus_Concurrent_100Goroutines(t *testing.T) {
	bus := NewBus(BusConfig{})
	defer bus.Close()

	// Create subscribers
	numSubscribers := 10
	subs := make([]*Subscriber, numSubscribers)
	for i := range subs {
		sub, err := bus.Subscribe(fmt.Sprintf("sub-%d", i))
		if err != nil {
			t.Fatalf("Subscribe failed: %v", err)
		}
		subs[i] = sub
	}

	// 100 goroutines publishing events
	numPublishers := 100
	eventsPerPublisher := 10
	var wg sync.WaitGroup
	var retryErrors int32
	var otherErrors int32

	ctx := context.Background()
	for publisherID := range make([]struct{}, numPublishers) {
		wg.Add(1)
		go func(publisherID int) {
			defer wg.Done()
			for seq := range make([]struct{}, eventsPerPublisher) {
				event := domain.SystemEvent{
					ID:          fmt.Sprintf("pub-%d-evt-%d", publisherID, seq),
					EventType:   "concurrent.test",
					WorkspaceID: "ws-concurrent",
					Payload:     fmt.Sprintf(`{"publisher": %d, "seq": %d}`, publisherID, seq),
					CreatedAt:   domain.Now(),
				}
				if err := bus.Publish(ctx, event); err != nil {
					if errors.Is(err, ErrRetryLater) {
						atomic.AddInt32(&retryErrors, 1)
					} else {
						atomic.AddInt32(&otherErrors, 1)
					}
				}
			}
		}(publisherID)
	}

	wg.Wait()

	// Retry errors are expected under high concurrency (subscriber buffers full)
	t.Logf("%d retry errors (expected under high load)", retryErrors)

	// Other errors are unexpected
	if otherErrors > 0 {
		t.Errorf("%d unexpected publish errors occurred", otherErrors)
	}

	// Collect received events from subscribers
	totalReceived := 0
	for _, sub := range subs {
		for {
			select {
			case <-sub.C:
				totalReceived++
			default:
				goto nextSubscriber
			}
		}
	nextSubscriber:
	}

	// This test mainly checks for races, not delivery guarantees
	t.Logf("received %d events", totalReceived)
}

func TestBus_Close(t *testing.T) {
	bus := NewBus(BusConfig{})

	sub, err := bus.Subscribe("sub-1")
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}
	defer bus.Unsubscribe("sub-1")

	if err := bus.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Second close should be idempotent
	if err := bus.Close(); err != nil {
		t.Fatalf("Second Close failed: %v", err)
	}

	// Publishing to closed bus should fail
	event := newTestEvent()
	ctx := context.Background()
	if err := bus.Publish(ctx, event); err == nil {
		t.Error("Publish to closed bus should fail")
	}

	// Subscriber channel should be closed
	_, ok := <-sub.C
	if ok {
		t.Error("subscriber channel should be closed")
	}
}

func TestBus_SubscriberCount(t *testing.T) {
	bus := NewBus(BusConfig{})
	defer bus.Close()

	if count := bus.SubscriberCount(); count != 0 {
		t.Errorf("initial count = %d, want 0", count)
	}

	if _, err := bus.Subscribe("sub-1"); err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}
	if count := bus.SubscriberCount(); count != 1 {
		t.Errorf("count after subscribe = %d, want 1", count)
	}

	if _, err := bus.Subscribe("sub-2"); err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}
	if count := bus.SubscriberCount(); count != 2 {
		t.Errorf("count after second subscribe = %d, want 2", count)
	}

	bus.Unsubscribe("sub-1")
	if count := bus.SubscriberCount(); count != 1 {
		t.Errorf("count after unsubscribe = %d, want 1", count)
	}
}

func TestBus_PersistenceFailure(t *testing.T) {
	repo := &mockEventRepo{err: errors.New("db error")}
	bus := NewBus(BusConfig{EventRepo: repo})
	defer bus.Close()

	sub, err := bus.Subscribe("sub-1")
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}
	defer bus.Unsubscribe("sub-1")

	event := newTestEvent()
	ctx := context.Background()

	err = bus.Publish(ctx, event)
	if err == nil {
		t.Fatal("Publish should fail when persistence fails")
	}

	// Subscriber should NOT receive event
	select {
	case <-sub.C:
		t.Error("subscriber should not receive event when persistence fails")
	default:
		// expected
	}
}

func TestBus_NoRepository(t *testing.T) {
	// Bus can work without a repository (for testing or special cases)
	bus := NewBus(BusConfig{})
	defer bus.Close()

	sub, err := bus.Subscribe("sub-1")
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}
	defer bus.Unsubscribe("sub-1")

	event := newTestEvent()
	ctx := context.Background()

	if err := bus.Publish(ctx, event); err != nil {
		t.Fatalf("Publish failed: %v", err)
	}

	select {
	case got := <-sub.C:
		if got.ID != event.ID {
			t.Errorf("got event ID %q, want %q", got.ID, event.ID)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("timed out waiting for event")
	}
}

func TestBus_SubscribeAfterClose(t *testing.T) {
	bus := NewBus(BusConfig{})

	// Close the bus first
	if err := bus.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Attempting to subscribe to a closed bus should return ErrBusClosed
	sub, err := bus.Subscribe("sub-1")
	if err == nil {
		bus.Unsubscribe("sub-1")
		t.Fatal("Subscribe should fail on closed bus")
	}
	if sub != nil {
		t.Error("Subscribe should return nil subscriber on closed bus")
	}
	if !errors.Is(err, ErrBusClosed) {
		t.Errorf("expected ErrBusClosed, got: %v", err)
	}
}

func TestBus_RetryLater(t *testing.T) {
	bus := NewBus(BusConfig{})
	defer bus.Close()

	// Create a subscriber with a very small buffer
	sub, err := bus.Subscribe("sub-1")
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}
	defer bus.Unsubscribe("sub-1")

	// Fill the subscriber's buffer (100 capacity)
	for fillID := range make([]struct{}, 100) {
		event := domain.SystemEvent{
			ID:          fmt.Sprintf("fill-%d", fillID),
			EventType:   "test.event",
			WorkspaceID: "ws-123",
			Payload:     "fill",
			CreatedAt:   domain.Now(),
		}
		if err := bus.Publish(context.Background(), event); err != nil {
			t.Fatalf("Publish %d failed: %v", fillID, err)
		}
	}

	// Next publish should return ErrRetryLater because buffer is full
	event := domain.SystemEvent{
		ID:          "overflow",
		EventType:   "test.event",
		WorkspaceID: "ws-123",
		Payload:     "overflow",
		CreatedAt:   domain.Now(),
	}
	err = bus.Publish(context.Background(), event)
	if err == nil {
		t.Fatal("Publish should fail when buffer is full")
	}
	if !errors.Is(err, ErrRetryLater) {
		t.Errorf("expected ErrRetryLater, got: %v", err)
	}

	// Drain one event from buffer
	<-sub.C

	// Now publish should succeed
	err = bus.Publish(context.Background(), event)
	if err != nil {
		t.Fatalf("Publish should succeed after draining buffer: %v", err)
	}
}

// Ensure mockEventRepo implements repository.EventRepository
var _ repository.EventRepository = (*mockEventRepo)(nil)

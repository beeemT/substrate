package event

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/repository"
)

// Verify Bus implements Publisher at compile time.
var _ Publisher = &Bus{}

// DefaultHookTimeout is the default timeout for hook execution.
const DefaultHookTimeout = 30 * time.Second

// ErrBusClosed is returned when attempting to subscribe to a closed bus.
var ErrBusClosed = error(busClosed{})

type busClosed struct{}

func (busClosed) Error() string { return "event bus is closed" }

// ErrRetryLater is returned when dispatch fails due to slow subscribers.
// The publisher should retry publishing the event after a short delay.
var ErrRetryLater = error(retryLater{})

type retryLater struct{}

func (retryLater) Error() string { return "subscriber buffer full, retry later" }

// PreHook is a synchronous hook called before event dispatch.
// If it returns an error, the event is aborted and not dispatched to subscribers.
type PreHook func(ctx context.Context, event domain.SystemEvent) error

// PostHook is an asynchronous hook called after event dispatch.
// Errors are logged but do not affect event delivery.
type PostHook func(ctx context.Context, event domain.SystemEvent) error

// HookConfig configures a hook's behavior.
type HookConfig struct {
	Name    string
	Timeout time.Duration // 0 means DefaultHookTimeout
}

// DropHandler is called when an event is dropped due to a slow subscriber.
// The handler typically logs a warning or enqueues a toast notification.
type DropHandler func(subscriberID string, event domain.SystemEvent)

// Publisher is the interface for publishing events.
type Publisher interface {
	Publish(ctx context.Context, event domain.SystemEvent) error
}

// BusOption configures the event bus.
type BusOption func(*Bus)

// WithDropHandler returns a BusOption that sets the drop handler.
// When set, dropped events call the handler instead of returning ErrRetryLater.
func WithDropHandler(h DropHandler) BusOption {
	return func(b *Bus) { b.onDrop = h }
}

// Subscriber receives events matching their subscribed topics.
type Subscriber struct {
	ID     string
	Topics map[string]bool // event types to subscribe to (empty = all)
	C      chan domain.SystemEvent
}

// Bus implements a channel-based pub/sub system with topic routing,
// synchronous pre-hooks, and asynchronous post-hooks.
//
// Transaction boundary: the bus persists events outside the caller's transaction.
// This is intentional — if event persistence were wrapped in the caller's transaction
// and that transaction rolled back, we would have already dispatched the event
// (triggering side effects like adapter calls, worktree operations, etc.) but lost
// the record of it. The bus is the source of truth for what was dispatched; callers
// must treat bus.Publish() as idempotent if they need at-least-once delivery guarantees.
//
// For synchronous validation that can abort an operation before it proceeds, use the
// worktree.HookRegistry instead.
type Bus struct {
	mu          sync.RWMutex
	subscribers map[string]*Subscriber // subscriber ID -> subscriber
	preHooks    []preHookEntry
	postHooks   []postHookEntry
	eventRepo   repository.EventRepository
	onDrop      DropHandler // called when subscriber buffer is full; nil returns ErrRetryLater
	closed      bool
	publishMu   sync.Mutex // serializes Publish so subscribers see events in sequence order
}

type preHookEntry struct {
	config HookConfig
	hook   PreHook
}

type postHookEntry struct {
	config HookConfig
	hook   PostHook
}

// BusConfig configures the event bus.
type BusConfig struct {
	EventRepo repository.EventRepository
}

// NewBus creates a new event bus with the given options.
func NewBus(cfg BusConfig, opts ...BusOption) *Bus {
	b := &Bus{
		subscribers: make(map[string]*Subscriber),
		preHooks:    make([]preHookEntry, 0),
		postHooks:   make([]postHookEntry, 0),
		eventRepo:   cfg.EventRepo,
	}
	for _, opt := range opts {
		opt(b)
	}
	return b
}

// IsPreHookEvent reports whether the given event type is a pre-hook event.
//
// Deprecated: pre-hook event types were removed in the async event-bus migration.
// This method always returns false. Hooks now run after persistence and cannot
// abort database writes — use worktree.HookRegistry for synchronous validation.
func (b *Bus) IsPreHookEvent(_ string) bool {
	return false
}

// RegisterPreHookType is a no-op.
//
// Deprecated: pre-hook event types were removed in the async event-bus migration.
// This method does nothing. For synchronous validation that runs before state
// transitions, use worktree.HookRegistry instead.
func (b *Bus) RegisterPreHookType(_ string) {}

// Subscribe creates a new subscriber for the given topics.
// If no topics are specified, the subscriber receives all events.
// Returns ErrBusClosed if the bus has been closed.
func (b *Bus) Subscribe(id string, topics ...string) (*Subscriber, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return nil, ErrBusClosed
	}

	topicSet := make(map[string]bool)
	for _, t := range topics {
		topicSet[t] = true
	}

	sub := &Subscriber{
		ID:     id,
		Topics: topicSet,
		C:      make(chan domain.SystemEvent, 500), // buffered to avoid blocking
	}

	// Close existing subscriber channel if present to prevent goroutine leak
	if existing, ok := b.subscribers[id]; ok {
		close(existing.C)
	}
	b.subscribers[id] = sub
	return sub, nil
}

// Unsubscribe removes a subscriber.
func (b *Bus) Unsubscribe(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if sub, ok := b.subscribers[id]; ok {
		close(sub.C)
		delete(b.subscribers, id)
	}
}

// RegisterPreHook registers a synchronous pre-hook.
// Pre-hooks are called in registration order, after persistence but before dispatch.
//
// For pre-creation validation (e.g., blocking worktree creation), use
// worktree.HookRegistry instead — it can abort the operation before persistence.
//
// If a pre-hook returns an error, dispatch is aborted but the event
// remains persisted (it already happened).
//
// Note: When a pre-hook times out, the goroutine running the hook continues
// executing if the hook function does not respect context cancellation.
// Go cannot forcefully kill goroutines. Hook implementations should check
// ctx.Done() and return promptly to avoid goroutine leaks.
func (b *Bus) RegisterPreHook(config HookConfig, hook PreHook) {
	if hook == nil {
		panic("event: RegisterPreHook called with nil hook")
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	if config.Timeout == 0 {
		config.Timeout = DefaultHookTimeout
	}
	b.preHooks = append(b.preHooks, preHookEntry{config: config, hook: hook})
}

// RegisterPostHook registers an asynchronous post-hook.
// Post-hooks are called after event dispatch with the configured timeout.
func (b *Bus) RegisterPostHook(config HookConfig, hook PostHook) {
	if hook == nil {
		panic("event: RegisterPostHook called with nil hook")
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	if config.Timeout == 0 {
		config.Timeout = DefaultHookTimeout
	}
	b.postHooks = append(b.postHooks, postHookEntry{config: config, hook: hook})
}

// Publish persists the event and dispatches it to matching subscribers.
//
//   - Persist event to repository
//   - Run pre-hooks synchronously (can abort dispatch but not persistence)
//   - Dispatch to matching subscribers
//   - Run post-hooks asynchronously
//
// Concurrency: Publish holds publishMu for the entire persist+pre-hook+dispatch
// flow. This serializes concurrent Publish calls so that subscribers cannot
// observe sequence N+1 before sequence N. Post-hooks run asynchronously after
// the lock is released; their execution order is best-effort and does not
// affect event delivery ordering.
func (b *Bus) Publish(ctx context.Context, event domain.SystemEvent) error {
	b.mu.RLock()
	closed := b.closed
	b.mu.RUnlock()

	if closed {
		return errors.New("event bus is closed")
	}

	// Serialize the entire persist+pre-hook+dispatch pipeline so subscribers
	// observe events in the same order the repository assigns sequences.
	// The dedicated mutex keeps Subscribe/Unsubscribe/RegisterHook (which take
	// b.mu) responsive while a long-running publish is in flight.
	b.publishMu.Lock()
	defer b.publishMu.Unlock()

	// Persist event first - it represents a fact that already occurred.
	// The repository returns the same event with its daemon-local monotonic
	// sequence assigned in the persistence transaction.
	if b.eventRepo != nil {
		persisted, err := b.eventRepo.Create(ctx, event)
		if err != nil {
			return fmt.Errorf("persist event: %w", err)
		}
		event = persisted
	}

	// Run pre-hooks synchronously (can abort dispatch but not persistence)
	if err := b.runPreHooks(ctx, event); err != nil {
		return fmt.Errorf("pre-hook aborted: %w", err)
	}

	// Dispatch to matching subscribers
	if err := b.dispatch(event); err != nil {
		return err
	}

	// Run post-hooks asynchronously
	go b.runPostHooks(event)

	return nil
}

func (b *Bus) runPreHooks(ctx context.Context, event domain.SystemEvent) error {
	b.mu.RLock()
	hooks := make([]preHookEntry, len(b.preHooks))
	copy(hooks, b.preHooks)
	b.mu.RUnlock()

	for _, entry := range hooks {
		timeout := entry.config.Timeout
		hookCtx, cancel := context.WithTimeout(ctx, timeout)

		// Run hook in goroutine to enforce timeout
		resultCh := make(chan error, 1)
		go func() {
			defer func() {
				if r := recover(); r != nil {
					resultCh <- fmt.Errorf("pre-hook %q panicked: %v", entry.config.Name, r)
				}
			}()
			resultCh <- entry.hook(hookCtx, event)
		}()
		select {
		case <-hookCtx.Done():
			cancel()
			return fmt.Errorf("pre-hook %q: %w", entry.config.Name, hookCtx.Err())
		case err := <-resultCh:
			cancel()
			if err != nil {
				return fmt.Errorf("pre-hook %q: %w", entry.config.Name, err)
			}
		}
	}
	return nil
}

// dispatch sends the event to all matching subscribers.
// Returns ErrRetryLater if any subscriber's buffer is full and no onDrop handler is set.
// If onDrop is set, calls the handler asynchronously for dropped events and continues.
func (b *Bus) dispatch(event domain.SystemEvent) error {
	b.mu.RLock()
	defer b.mu.RUnlock()

	for _, sub := range b.subscribers {
		// Check if subscriber wants this event type
		if len(sub.Topics) > 0 && !sub.Topics[event.EventType] {
			continue
		}

		// Non-blocking send; if buffer is full, handle via onDrop or return error
		select {
		case sub.C <- event:
			// delivered
			// no-op
		default:
			if b.onDrop != nil {
				// Invoke handler asynchronously to avoid blocking the bus
				go func(subID string, ev domain.SystemEvent) {
					defer func() {
						if r := recover(); r != nil {
							slog.Error("drop handler panicked", "subscriber", subID, "panic", r)
						}
					}()
					b.onDrop(subID, ev)
				}(sub.ID, event)
			} else {
				return ErrRetryLater
			}
		}
	}
	return nil
}

func (b *Bus) runPostHooks(event domain.SystemEvent) {
	b.mu.RLock()
	hooks := make([]postHookEntry, len(b.postHooks))
	copy(hooks, b.postHooks)
	b.mu.RUnlock()

	var wg sync.WaitGroup
	for _, entry := range hooks {
		wg.Add(1)
		go func(e postHookEntry) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					slog.Error("post-hook panicked", "hook", e.config.Name, "panic", r)
				}
			}()
			ctx, cancel := context.WithTimeout(context.Background(), e.config.Timeout)
			defer cancel()

			// Post-hook errors are logged at warn level.
			if hookErr := e.hook(ctx, event); hookErr != nil {
				slog.Warn("post-hook failed", "hook", e.config.Name, "error", hookErr)
			}
		}(entry)
	}
	wg.Wait()
}

// Close shuts down the bus, closing all subscriber channels.
func (b *Bus) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return nil
	}
	b.closed = true

	for id, sub := range b.subscribers {
		close(sub.C)
		delete(b.subscribers, id)
	}
	return nil
}

// SubscriberCount returns the number of active subscribers.
func (b *Bus) SubscriberCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subscribers)
}

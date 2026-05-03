package worktree

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestHookRegistry_Empty verifies an empty registry passes without error.
func TestHookRegistry_Empty(t *testing.T) {
	registry := NewHookRegistry()
	err := registry.Run(context.Background(), CheckoutRequest{
		WorkspaceID: "ws-1",
		WorkItemID:  "wi-1",
	})
	if err != nil {
		t.Errorf("empty registry should pass; got %v", err)
	}
}

// TestHookRegistry_FirstErrorStops verifies that the first hook error stops execution.
func TestHookRegistry_FirstErrorStops(t *testing.T) {
	registry := NewHookRegistry()

	var called int32
	registry.Register(HookConfig{Name: "first"}, func(_ context.Context, _ CheckoutRequest) error {
		atomic.AddInt32(&called, 1)
		return errors.New("first rejected")
	})
	registry.Register(HookConfig{Name: "second"}, func(_ context.Context, _ CheckoutRequest) error {
		atomic.AddInt32(&called, 1)
		return errors.New("second rejected")
	})

	err := registry.Run(context.Background(), CheckoutRequest{})
	if err == nil {
		t.Fatal("expected error from first hook")
	}
	if called != 1 {
		t.Errorf("only first hook should be called; got %d calls", called)
	}
}

// TestHookRegistry_CallOrder verifies hooks run in registration order.
func TestHookRegistry_CallOrder(t *testing.T) {
	registry := NewHookRegistry()

	var order []string
	var mu sync.Mutex

	registry.Register(HookConfig{Name: "first"}, func(_ context.Context, _ CheckoutRequest) error {
		mu.Lock()
		order = append(order, "first")
		mu.Unlock()
		return nil
	})
	registry.Register(HookConfig{Name: "second"}, func(_ context.Context, _ CheckoutRequest) error {
		mu.Lock()
		order = append(order, "second")
		mu.Unlock()
		return nil
	})

	err := registry.Run(context.Background(), CheckoutRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(order) != 2 {
		t.Fatalf("expected 2 hooks called, got %d: %v", len(order), order)
	}
	if order[0] != "first" || order[1] != "second" {
		t.Errorf("call order = %v, want [first, second]", order)
	}
}

// TestHookRegistry_Timeout verifies slow hooks are cancelled by timeout.
func TestHookRegistry_Timeout(t *testing.T) {
	registry := NewHookRegistry()

	registry.Register(HookConfig{Name: "slow", Timeout: 50 * time.Millisecond}, func(ctx context.Context, _ CheckoutRequest) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
			return nil
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := registry.Run(ctx, CheckoutRequest{})
	if err == nil {
		t.Error("expected error from hook timeout")
	}
}

// TestHookRegistry_PassRequest verifies the request context is passed to hooks.
func TestHookRegistry_PassRequest(t *testing.T) {
	registry := NewHookRegistry()

	want := CheckoutRequest{
		WorkspaceID:   "ws-test",
		WorkItemID:    "wi-42",
		Repository:    "my-repo",
		Branch:        "feat/test",
		WorkItemTitle: "Test work item",
	}

	var got CheckoutRequest
	registry.Register(HookConfig{Name: "check"}, func(_ context.Context, req CheckoutRequest) error {
		got = req
		return nil
	})

	err := registry.Run(context.Background(), want)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got != want {
		t.Errorf("got request %+v, want %+v", got, want)
	}
}

// TestHookRegistry_Concurrent verifies hooks run synchronously (not concurrently).
func TestHookRegistry_Concurrent(t *testing.T) {
	registry := NewHookRegistry()

	var active atomic.Int32
	var maxConcurrent atomic.Int32

	for i := 0; i < 5; i++ {
		registry.Register(HookConfig{Name: "check"}, func(_ context.Context, _ CheckoutRequest) error {
			current := active.Add(1)
			if current > maxConcurrent.Load() {
				maxConcurrent.Store(current)
			}
			time.Sleep(10 * time.Millisecond)
			active.Add(-1)
			return nil
		})
	}

	err := registry.Run(context.Background(), CheckoutRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if maxConcurrent.Load() != 1 {
		t.Errorf("max concurrent hooks = %d, want 1 (hooks should be serial, not parallel)", maxConcurrent.Load())
	}
}

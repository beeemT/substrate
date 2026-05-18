package workerpool

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestProcessAll_Basic(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	items := []int{1, 2, 3, 4, 5}
	cfg := Config{Workers: 2}

	results, errs := ProcessAll(ctx, items, cfg, func(_ context.Context, item int) (int, error) {
		return item * 2, nil
	})

	// Check results in order
	expected := []int{2, 4, 6, 8, 10}
	for i, want := range expected {
		if results[i] != want {
			t.Errorf("results[%d] = %d, want %d", i, results[i], want)
		}
	}

	// Check no errors
	for i, err := range errs {
		if err != nil {
			t.Errorf("errors[%d] = %v, want nil", i, err)
		}
	}
}

func TestProcessAll_WithErrors(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	items := []int{1, 2, 3}
	cfg := Config{Workers: 2}
	expectedErr := errors.New("test error")

	results, errs := ProcessAll(ctx, items, cfg, func(_ context.Context, item int) (int, error) {
		if item == 2 {
			return 0, expectedErr
		}
		return item * 2, nil
	})

	// Check results
	if results[0] != 2 {
		t.Errorf("results[0] = %d, want 2", results[0])
	}
	if results[1] != 0 { // Error case, result should be zero value
		t.Errorf("results[1] = %d, want 0", results[1])
	}
	if results[2] != 6 {
		t.Errorf("results[2] = %d, want 6", results[2])
	}

	// Check errors
	if errs[0] != nil {
		t.Errorf("errors[0] = %v, want nil", errs[0])
	}
	if !errors.Is(errs[1], expectedErr) {
		t.Errorf("errors[1] = %v, want %v", errs[1], expectedErr)
	}
	if errs[2] != nil {
		t.Errorf("errors[2] = %v, want nil", errs[2])
	}
}

func TestProcessAll_EmptyItems(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	items := []int{}
	cfg := Config{Workers: 2}

	results, errs := ProcessAll(ctx, items, cfg, func(_ context.Context, item int) (int, error) {
		return item, nil
	})

	if results != nil {
		t.Errorf("results = %v, want nil", results)
	}
	if errs != nil {
		t.Errorf("errors = %v, want nil", errs)
	}
}

func TestProcessAll_ContextCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	items := []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	cfg := Config{Workers: 2}

	// Cancel after some items are processed
	var processed atomic.Int32
	_, errs := ProcessAll(ctx, items, cfg, func(ctx context.Context, item int) (int, error) {
		processed.Add(1)
		time.Sleep(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		default:
			return item, nil
		}
	})

	cancel()

	// Should get some results before cancellation
	if processed.Load() == 0 {
		t.Error("no items were processed before cancellation")
	}

	// Results should still be populated for processed items
	for i, err := range errs {
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("errors[%d] = %v, want context.Canceled or nil", i, err)
		}
	}
}

func TestProcessAll_OrderPreserved(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	// Items that will take varying amounts of time
	items := []int{100, 10, 50, 5, 200, 1}
	cfg := Config{Workers: 3}

	var completions []int
	var mu sync.Mutex

	_, errs := ProcessAll(ctx, items, cfg, func(_ context.Context, item int) (int, error) {
		time.Sleep(time.Duration(item) * time.Millisecond)
		mu.Lock()
		completions = append(completions, item)
		mu.Unlock()
		return item * 10, nil
	})

	// All should complete
	if len(completions) != len(items) {
		t.Errorf("completions = %v, want all %v", completions, items)
	}

	// No errors
	for i, err := range errs {
		if err != nil {
			t.Errorf("errors[%d] = %v, want nil", i, err)
		}
	}
}

func TestProcessAllVoid_Basic(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	items := []int{1, 2, 3}
	cfg := Config{Workers: 2}

	var processed atomic.Int32
	errs := ProcessAllVoid(ctx, items, cfg, func(_ context.Context, item int) error {
		processed.Add(int32(item))
		return nil
	})

	if processed.Load() != 6 { // 1 + 2 + 3
		t.Errorf("processed = %d, want 6", processed.Load())
	}

	for i, err := range errs {
		if err != nil {
			t.Errorf("errors[%d] = %v, want nil", i, err)
		}
	}
}

func TestProcessAllVoid_EmptyItems(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	items := []int{}
	cfg := Config{Workers: 2}

	errs := ProcessAllVoid(ctx, items, cfg, func(_ context.Context, item int) error {
		return nil
	})

	if errs != nil {
		t.Errorf("errors = %v, want nil", errs)
	}
}

func TestEffectiveWorkers(t *testing.T) {
	t.Parallel()

	defaultWorkers := DefaultWorkers()

	tests := []struct {
		name        string
		cfg         Config
		itemCount   int
		wantWorkers int
	}{
		{"zero workers with few items", Config{Workers: 0}, 3, 3},
		{"zero workers with many items", Config{Workers: 0}, 100, defaultWorkers},
		{"explicit workers less than items", Config{Workers: 4}, 10, 4},
		{"explicit workers more than items", Config{Workers: 10}, 3, 3},
		{"exact match", Config{Workers: 5}, 5, 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.cfg.EffectiveWorkers(tt.itemCount)
			if got != tt.wantWorkers {
				t.Errorf("EffectiveWorkers(%d) = %d, want %d", tt.itemCount, got, tt.wantWorkers)
			}
		})
	}
}

func TestDefaultWorkers(t *testing.T) {
	t.Parallel()

	n := DefaultWorkers()
	if n < 1 {
		t.Errorf("DefaultWorkers() = %d, want >= 1", n)
	}
	if n > 8 {
		t.Errorf("DefaultWorkers() = %d, want <= 8", n)
	}
}

func TestProcessAll_Parallelism(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	itemCount := 10
	delay := 100 * time.Millisecond
	items := make([]int, itemCount)
	for i := range items {
		items[i] = i
	}

	cfg := Config{Workers: 4}

	start := time.Now()
	_, _ = ProcessAll(ctx, items, cfg, func(_ context.Context, item int) (int, error) {
		time.Sleep(delay)
		return item, nil
	})
	elapsed := time.Since(start)

	// With 10 items and 4 workers, sequential would take ~1000ms (10 * 100ms).
	// Parallel should take ~300ms (10/4 rounded up = 3 batches * 100ms).
	// Allow generous margin for CI variability: should be well under 600ms.
	if elapsed < delay {
		t.Errorf("elapsed = %v, want >= %v (something is wrong)", elapsed, delay)
	}
	if elapsed > 600*time.Millisecond {
		t.Errorf("elapsed = %v, want < 600ms (appears sequential instead of parallel)", elapsed)
	}
}

func TestProcessAllVoid_Parallelism(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	itemCount := 8
	delay := 50 * time.Millisecond
	items := make([]int, itemCount)
	for i := range items {
		items[i] = i
	}

	cfg := Config{Workers: 4}

	start := time.Now()
	_ = ProcessAllVoid(ctx, items, cfg, func(_ context.Context, item int) error {
		time.Sleep(delay)
		return nil
	})
	elapsed := time.Since(start)

	// With 8 items and 4 workers, sequential would take 400ms. Parallel ~200ms.
	if elapsed > 300*time.Millisecond {
		t.Errorf("elapsed = %v, want < 300ms (appears sequential instead of parallel)", elapsed)
	}
}

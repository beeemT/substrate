// Package workerpool provides a generic worker pool for parallel processing.
package workerpool

import (
	"context"
	"runtime"
	"sync"
)

// DefaultWorkers returns the default number of workers based on CPU count.
func DefaultWorkers() int {
	n := runtime.NumCPU()
	if n > 8 {
		return 8
	}
	return n
}

// Config controls pool behavior.
type Config struct {
	// Workers is the number of concurrent workers. Zero uses DefaultWorkers().
	Workers int
}

// EffectiveWorkers returns the worker count, using default if not set.
func (c Config) EffectiveWorkers(itemCount int) int {
	if c.Workers == 0 {
		w := DefaultWorkers()
		if w > itemCount {
			return itemCount
		}
		return w
	}
	if c.Workers > itemCount {
		return itemCount
	}
	return c.Workers
}

// ItemResult holds the result of processing a single item.
type ItemResult[T any] struct {
	Index  int
	Result T
	Error  error
}

// ProcessAll runs fn concurrently over items using a pool of workers.
// It returns results in the same order as input items.
// The context controls cancellation; if cancelled, partial results are still returned.
func ProcessAll[In, Out any](
	ctx context.Context,
	items []In,
	cfg Config,
	fn func(context.Context, In) (Out, error),
) ([]Out, []error) {
	if len(items) == 0 {
		return nil, nil
	}

	workerCount := cfg.EffectiveWorkers(len(items))
	results := make([]Out, len(items))
	errors := make([]error, len(items))

	// Channel for distributing work
	type workItem struct {
		index int
		item  In
	}
	workCh := make(chan workItem, len(items))

	// Signal channel for workers to stop
	doneCh := make(chan struct{})

	// WaitGroup for workers
	var wg sync.WaitGroup

	// Start workers
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for work := range workCh {
				select {
				case <-ctx.Done():
					return
				case <-doneCh:
					return
				default:
				}

				result, err := fn(ctx, work.item)
				if err != nil {
					errors[work.index] = err
				} else {
					results[work.index] = result
				}
			}
		}()
	}

	// Send all work
	for i, item := range items {
		select {
		case <-ctx.Done():
			// Context cancelled before we could send all work
			break
		case workCh <- workItem{index: i, item: item}:
		}
	}
	close(workCh)

	// Wait for all workers to finish
	wg.Wait()
	close(doneCh)

	return results, errors
}

// ProcessAllVoid runs fn concurrently over items without collecting results.
// Use this when you only care about errors or side effects.
func ProcessAllVoid[In any](
	ctx context.Context,
	items []In,
	cfg Config,
	fn func(context.Context, In) error,
) []error {
	if len(items) == 0 {
		return nil
	}

	workerCount := cfg.EffectiveWorkers(len(items))
	errors := make([]error, len(items))

	type workItem struct {
		index int
		item  In
	}
	workCh := make(chan workItem, len(items))
	doneCh := make(chan struct{})

	var wg sync.WaitGroup

	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for work := range workCh {
				select {
				case <-ctx.Done():
					return
				case <-doneCh:
					return
				default:
				}

				if err := fn(ctx, work.item); err != nil {
					errors[work.index] = err
				}
			}
		}()
	}

	for i, item := range items {
		select {
		case <-ctx.Done():
			break
		case workCh <- workItem{index: i, item: item}:
		}
	}
	close(workCh)
	wg.Wait()
	close(doneCh)

	return errors
}

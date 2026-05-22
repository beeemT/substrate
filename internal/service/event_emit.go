package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/event"
)

// emitTimeout is the timeout for async event emission.
const emitTimeout = 5 * time.Second

// Emit publishes an event after the caller's state change has committed.
// It blocks until the event is persisted and dispatched so callers cannot return
// a durable state transition while the matching event is still only queued in a
// goroutine that may be lost during shutdown or service graph rebuilds.
func Emit(bus event.Publisher, evt domain.SystemEvent) {
	ctx, cancel := context.WithTimeout(context.Background(), emitTimeout)
	defer cancel()
	if err := bus.Publish(ctx, evt); err != nil {
		slog.Error("failed to emit event",
			slog.String("event_type", evt.EventType),
			slog.String("error", err.Error()),
		)
	}
}

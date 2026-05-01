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

// Emit emits an event asynchronously if the bus is not nil.
// This is a shared helper to reduce boilerplate across services.
func Emit(bus *event.Bus, evt domain.SystemEvent) {
	if bus == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), emitTimeout)
		defer cancel()
		if err := bus.Publish(ctx, evt); err != nil {
			slog.Error("failed to emit event",
				slog.String("event_type", evt.EventType),
				slog.String("error", err.Error()),
			)
		}
	}()
}

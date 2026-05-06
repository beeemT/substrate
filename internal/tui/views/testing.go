package views

import (
	"context"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/event"
)

// noopPublisher implements event.Publisher for tests that don't need actual publishing.
type noopPublisher struct{}

func (noopPublisher) Publish(_ context.Context, _ domain.SystemEvent) error {
	return nil
}

// MockBus creates a real event.Bus for tests that need event publishing.
func MockBus() *event.Bus {
	return event.NewBus(event.BusConfig{})
}

// NewNoopPublisher returns a no-op publisher for tests.
func NewNoopPublisher() event.Publisher {
	return &noopPublisher{}
}

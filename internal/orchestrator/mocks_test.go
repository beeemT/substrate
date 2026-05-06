package orchestrator

import (
	"context"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/event"
)

// mockPublisher implements event.Publisher for testing.
type mockPublisher struct {
	Published []domain.SystemEvent
	Err       error
}

func (m *mockPublisher) Publish(_ context.Context, evt domain.SystemEvent) error {
	m.Published = append(m.Published, evt)
	return m.Err
}

// Ensure event.Bus implements event.Publisher for tests that need the real bus.
var _ event.Publisher = (*event.Bus)(nil)

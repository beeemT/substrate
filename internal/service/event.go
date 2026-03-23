package service

import (
	"context"

	"github.com/beeemT/go-atomic"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/repository"
)

// EventService provides business logic for system events.
type EventService struct {
	transacter atomic.Transacter[repository.Resources]
}

// NewEventService creates a new EventService.
func NewEventService(transacter atomic.Transacter[repository.Resources]) *EventService {
	return &EventService{transacter: transacter}
}

// Create persists a system event.
func (s *EventService) Create(ctx context.Context, e domain.SystemEvent) error {
	return s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		return res.Events.Create(ctx, e)
	})
}

// ListByType retrieves events by type.
func (s *EventService) ListByType(ctx context.Context, eventType string, limit int) ([]domain.SystemEvent, error) {
	var result []domain.SystemEvent
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		events, err := res.Events.ListByType(ctx, eventType, limit)
		if err != nil {
			return err
		}
		result = events
		return nil
	})
	return result, err
}

// ListByWorkspaceID retrieves events by workspace ID.
func (s *EventService) ListByWorkspaceID(ctx context.Context, workspaceID string, limit int) ([]domain.SystemEvent, error) {
	var result []domain.SystemEvent
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		events, err := res.Events.ListByWorkspaceID(ctx, workspaceID, limit)
		if err != nil {
			return err
		}
		result = events
		return nil
	})
	return result, err
}

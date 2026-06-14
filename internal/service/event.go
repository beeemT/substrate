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

// Create persists a system event and returns it with its assigned monotonic sequence.
func (s *EventService) Create(ctx context.Context, e domain.SystemEvent) (domain.SystemEvent, error) {
	var result domain.SystemEvent
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		created, err := res.Events.Create(ctx, e)
		if err != nil {
			return err
		}
		result = created
		return nil
	})
	return result, err
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

// ListByWorkspaceIDAfterSequence retrieves events for replay in ascending sequence order.
func (s *EventService) ListByWorkspaceIDAfterSequence(ctx context.Context, workspaceID string, afterSequence uint64, limit int) ([]domain.SystemEvent, error) {
	var result []domain.SystemEvent
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		events, err := res.Events.ListByWorkspaceIDAfterSequence(ctx, workspaceID, afterSequence, limit)
		if err != nil {
			return err
		}
		result = events
		return nil
	})
	return result, err
}

// LatestSequence returns the current replay cursor for a workspace.
func (s *EventService) LatestSequence(ctx context.Context, workspaceID string) (uint64, error) {
	var result uint64
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		sequence, err := res.Events.LatestSequence(ctx, workspaceID)
		if err != nil {
			return err
		}
		result = sequence
		return nil
	})
	return result, err
}

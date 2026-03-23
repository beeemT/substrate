package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/beeemT/go-atomic"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/repository"
)

// InstanceService provides business logic for substrate instances.
type InstanceService struct {
	transacter atomic.Transacter[repository.Resources]
}

// NewInstanceService creates a new InstanceService.
func NewInstanceService(transacter atomic.Transacter[repository.Resources]) *InstanceService {
	return &InstanceService{transacter: transacter}
}

// Get retrieves an instance by ID.
func (s *InstanceService) Get(ctx context.Context, id string) (domain.SubstrateInstance, error) {
	var result domain.SubstrateInstance
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		inst, err := res.Instances.Get(ctx, id)
		if err != nil {
			return newNotFoundError("instance", id)
		}
		result = inst
		return nil
	})
	return result, err
}

// ListByWorkspaceID retrieves all instances for a workspace.
func (s *InstanceService) ListByWorkspaceID(ctx context.Context, workspaceID string) ([]domain.SubstrateInstance, error) {
	var result []domain.SubstrateInstance
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		var err error
		result, err = res.Instances.ListByWorkspaceID(ctx, workspaceID)
		return err
	})
	return result, err
}

// Create creates a new instance record.
func (s *InstanceService) Create(ctx context.Context, inst domain.SubstrateInstance) error {
	// Set timestamps
	now := time.Now()
	inst.StartedAt = now
	inst.LastHeartbeat = now

	return s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		return res.Instances.Create(ctx, inst)
	})
}

// UpdateHeartbeat updates the last heartbeat timestamp for an instance.
func (s *InstanceService) UpdateHeartbeat(ctx context.Context, id string) error {
	return s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		inst, err := res.Instances.Get(ctx, id)
		if err != nil {
			return newNotFoundError("instance", id)
		}

		inst.LastHeartbeat = time.Now()

		return res.Instances.Update(ctx, inst)
	})
}

// Delete removes an instance record (on clean shutdown).
func (s *InstanceService) Delete(ctx context.Context, id string) error {
	return s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		_, err := res.Instances.Get(ctx, id)
		if err != nil {
			return newNotFoundError("instance", id)
		}

		return res.Instances.Delete(ctx, id)
	})
}

// IsAlive checks if an instance is alive based on heartbeat.
// An instance is considered alive if its last heartbeat is within the threshold.
func (s *InstanceService) IsAlive(ctx context.Context, id string, threshold time.Duration) (bool, error) {
	var alive bool
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		inst, err := res.Instances.Get(ctx, id)
		if err != nil {
			return newNotFoundError("instance", id)
		}
		alive = time.Since(inst.LastHeartbeat) <= threshold
		return nil
	})
	return alive, err
}

// FindStaleInstances finds all instances with stale heartbeats for a workspace.
func (s *InstanceService) FindStaleInstances(ctx context.Context, workspaceID string, threshold time.Duration) ([]domain.SubstrateInstance, error) {
	var stale []domain.SubstrateInstance
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		instances, err := res.Instances.ListByWorkspaceID(ctx, workspaceID)
		if err != nil {
			return err
		}

		for _, inst := range instances {
			if time.Since(inst.LastHeartbeat) > threshold {
				stale = append(stale, inst)
			}
		}
		return nil
	})
	return stale, err
}

// CleanupStaleInstances removes all stale instance records for a workspace.
// Returns the count of successfully deleted instances and an error if any deletions failed.
// If some deletions fail, the error will be a joined error containing all individual errors.
func (s *InstanceService) CleanupStaleInstances(ctx context.Context, workspaceID string, threshold time.Duration) (int, error) {
	stale, err := s.FindStaleInstances(ctx, workspaceID, threshold)
	if err != nil {
		return 0, err
	}

	var (
		count     int
		deleteErr error
	)
	for _, inst := range stale {
		err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
			return res.Instances.Delete(ctx, inst.ID)
		})
		if err != nil {
			deleteErr = errors.Join(deleteErr, fmt.Errorf("delete instance %s: %w", inst.ID, err))
			continue
		}
		count++
	}

	return count, deleteErr
}

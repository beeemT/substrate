package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/repository"
)

// InstanceService provides business logic for substrate instances.
type InstanceService struct {
	repo repository.InstanceRepository
}

// NewInstanceService creates a new InstanceService.
func NewInstanceService(repo repository.InstanceRepository) *InstanceService {
	return &InstanceService{repo: repo}
}

// Get retrieves an instance by ID.
func (s *InstanceService) Get(ctx context.Context, id string) (domain.SubstrateInstance, error) {
	inst, err := s.repo.Get(ctx, id)
	if err != nil {
		return domain.SubstrateInstance{}, newNotFoundError("instance", id)
	}

	return inst, nil
}

// ListByWorkspaceID retrieves all instances for a workspace.
func (s *InstanceService) ListByWorkspaceID(ctx context.Context, workspaceID string) ([]domain.SubstrateInstance, error) {
	return s.repo.ListByWorkspaceID(ctx, workspaceID)
}

// Create creates a new instance record.
func (s *InstanceService) Create(ctx context.Context, inst domain.SubstrateInstance) error {
	// Set timestamps
	now := time.Now()
	inst.StartedAt = now
	inst.LastHeartbeat = now

	return s.repo.Create(ctx, inst)
}

// UpdateHeartbeat updates the last heartbeat timestamp for an instance.
func (s *InstanceService) UpdateHeartbeat(ctx context.Context, id string) error {
	inst, err := s.repo.Get(ctx, id)
	if err != nil {
		return newNotFoundError("instance", id)
	}

	inst.LastHeartbeat = time.Now()

	return s.repo.Update(ctx, inst)
}

// Delete removes an instance record (on clean shutdown).
func (s *InstanceService) Delete(ctx context.Context, id string) error {
	_, err := s.repo.Get(ctx, id)
	if err != nil {
		return newNotFoundError("instance", id)
	}

	return s.repo.Delete(ctx, id)
}

// IsAlive checks if an instance is alive based on heartbeat.
// An instance is considered alive if its last heartbeat is within the threshold.
func (s *InstanceService) IsAlive(ctx context.Context, id string, threshold time.Duration) (bool, error) {
	inst, err := s.repo.Get(ctx, id)
	if err != nil {
		return false, newNotFoundError("instance", id)
	}

	return time.Since(inst.LastHeartbeat) <= threshold, nil
}

// FindStaleInstances finds all instances with stale heartbeats for a workspace.
func (s *InstanceService) FindStaleInstances(ctx context.Context, workspaceID string, threshold time.Duration) ([]domain.SubstrateInstance, error) {
	instances, err := s.repo.ListByWorkspaceID(ctx, workspaceID)
	if err != nil {
		return nil, err
	}

	var stale []domain.SubstrateInstance
	for _, inst := range instances {
		if time.Since(inst.LastHeartbeat) > threshold {
			stale = append(stale, inst)
		}
	}

	return stale, nil
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
		if err := s.repo.Delete(ctx, inst.ID); err != nil {
			deleteErr = errors.Join(deleteErr, fmt.Errorf("delete instance %s: %w", inst.ID, err))

			continue
		}
		count++
	}

	return count, deleteErr
}

package service

import (
	"context"
	"slices"
	"time"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/repository"
)

// WorkspaceService provides business logic for workspaces.
type WorkspaceService struct {
	repo repository.WorkspaceRepository
}

// NewWorkspaceService creates a new WorkspaceService.
func NewWorkspaceService(repo repository.WorkspaceRepository) *WorkspaceService {
	return &WorkspaceService{repo: repo}
}

// Workspace state transitions
var validWorkspaceTransitions = map[domain.WorkspaceStatus][]domain.WorkspaceStatus{
	domain.WorkspaceCreating: {domain.WorkspaceReady, domain.WorkspaceError},
	domain.WorkspaceReady:    {domain.WorkspaceArchived},
	domain.WorkspaceArchived: {},                      // Terminal state
	domain.WorkspaceError:    {domain.WorkspaceReady}, // Can recover from error
}

func canTransitionWorkspace(from, to domain.WorkspaceStatus) bool {
	allowed, exists := validWorkspaceTransitions[from]
	if !exists {
		return false
	}
	return slices.Contains(allowed, to)
}

// Get retrieves a workspace by ID.
func (s *WorkspaceService) Get(ctx context.Context, id string) (domain.Workspace, error) {
	ws, err := s.repo.Get(ctx, id)
	if err != nil {
		return domain.Workspace{}, newNotFoundError("workspace", id)
	}

	return ws, nil
}

// Create creates a new workspace in creating status.
func (s *WorkspaceService) Create(ctx context.Context, ws domain.Workspace) error {
	// Set initial status if not set
	if ws.Status == "" {
		ws.Status = domain.WorkspaceCreating
	}
	// Set timestamps
	now := time.Now()
	ws.CreatedAt = now
	ws.UpdatedAt = now

	return s.repo.Create(ctx, ws)
}

// Transition transitions a workspace to a new status.
func (s *WorkspaceService) Transition(ctx context.Context, id string, to domain.WorkspaceStatus) error {
	ws, err := s.repo.Get(ctx, id)
	if err != nil {
		return newNotFoundError("workspace", id)
	}

	if !canTransitionWorkspace(ws.Status, to) {
		return newInvalidTransitionError(
			workspaceStatusName(ws.Status),
			workspaceStatusName(to),
			"workspace",
		)
	}

	ws.Status = to
	ws.UpdatedAt = time.Now()

	return s.repo.Update(ctx, ws)
}

// MarkReady transitions a workspace from creating to ready.
func (s *WorkspaceService) MarkReady(ctx context.Context, id string) error {
	return s.Transition(ctx, id, domain.WorkspaceReady)
}

// MarkError transitions a workspace from creating to error.
func (s *WorkspaceService) MarkError(ctx context.Context, id string) error {
	return s.Transition(ctx, id, domain.WorkspaceError)
}

// Archive transitions a workspace from ready to archived.
func (s *WorkspaceService) Archive(ctx context.Context, id string) error {
	return s.Transition(ctx, id, domain.WorkspaceArchived)
}

// Recover transitions a workspace from error to ready.
func (s *WorkspaceService) Recover(ctx context.Context, id string) error {
	return s.Transition(ctx, id, domain.WorkspaceReady)
}

// Update updates a workspace's mutable fields.
func (s *WorkspaceService) Update(ctx context.Context, ws domain.Workspace) error {
	existing, err := s.repo.Get(ctx, ws.ID)
	if err != nil {
		return newNotFoundError("workspace", ws.ID)
	}

	// Preserve immutable fields
	ws.ID = existing.ID
	ws.CreatedAt = existing.CreatedAt
	ws.Status = existing.Status // Status changes must go through Transition
	ws.UpdatedAt = time.Now()

	return s.repo.Update(ctx, ws)
}

// Delete deletes a workspace.
func (s *WorkspaceService) Delete(ctx context.Context, id string) error {
	_, err := s.repo.Get(ctx, id)
	if err != nil {
		return newNotFoundError("workspace", id)
	}

	return s.repo.Delete(ctx, id)
}

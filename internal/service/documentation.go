package service

import (
	"context"
	"time"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/repository"
)

// DocumentationService provides business logic for documentation sources.
type DocumentationService struct {
	repo repository.DocumentationRepository
}

// NewDocumentationService creates a new DocumentationService.
func NewDocumentationService(repo repository.DocumentationRepository) *DocumentationService {
	return &DocumentationService{repo: repo}
}

// Get retrieves a documentation source by ID.
func (s *DocumentationService) Get(ctx context.Context, id string) (domain.DocumentationSource, error) {
	ds, err := s.repo.Get(ctx, id)
	if err != nil {
		return domain.DocumentationSource{}, newNotFoundError("documentation source", id)
	}
	return ds, nil
}

// ListByWorkspaceID retrieves all documentation sources for a workspace.
func (s *DocumentationService) ListByWorkspaceID(ctx context.Context, workspaceID string) ([]domain.DocumentationSource, error) {
	return s.repo.ListByWorkspaceID(ctx, workspaceID)
}

// Create creates a new documentation source.
func (s *DocumentationService) Create(ctx context.Context, ds domain.DocumentationSource) error {
	// Set timestamps
	now := time.Now()
	ds.CreatedAt = now

	// Validate type
	if ds.Type != domain.DocSourceRepoEmbedded && ds.Type != domain.DocSourceDedicatedRepo {
		return newInvalidInputError("invalid documentation source type", "type")
	}

	return s.repo.Create(ctx, ds)
}

// Update updates a documentation source.
func (s *DocumentationService) Update(ctx context.Context, ds domain.DocumentationSource) error {
	existing, err := s.repo.Get(ctx, ds.ID)
	if err != nil {
		return newNotFoundError("documentation source", ds.ID)
	}

	// Preserve immutable fields
	ds.ID = existing.ID
	ds.WorkspaceID = existing.WorkspaceID
	ds.CreatedAt = existing.CreatedAt

	return s.repo.Update(ctx, ds)
}

// UpdateLastSynced updates the last synced timestamp for a documentation source.
func (s *DocumentationService) UpdateLastSynced(ctx context.Context, id string) error {
	ds, err := s.repo.Get(ctx, id)
	if err != nil {
		return newNotFoundError("documentation source", id)
	}

	now := time.Now()
	ds.LastSyncedAt = &now

	return s.repo.Update(ctx, ds)
}

// Delete deletes a documentation source.
func (s *DocumentationService) Delete(ctx context.Context, id string) error {
	_, err := s.repo.Get(ctx, id)
	if err != nil {
		return newNotFoundError("documentation source", id)
	}
	return s.repo.Delete(ctx, id)
}

// MarkStale marks documentation sources as stale based on changed file paths.
// This is a placeholder - the actual staleness detection logic will be implemented
// in Phase 5 (Documentation Source System).
func (s *DocumentationService) MarkStale(ctx context.Context, workspaceID string, changedPaths []string) ([]domain.DocumentationSource, error) {
	// This will be implemented in Phase 5 with proper path-to-doc mapping rules
	// For now, return an empty list
	return []domain.DocumentationSource{}, nil
}

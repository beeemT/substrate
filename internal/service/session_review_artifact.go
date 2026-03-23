package service

import (
	"context"

	"github.com/beeemT/go-atomic"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/repository"
)

// SessionReviewArtifactService provides business logic for session review artifacts.
type SessionReviewArtifactService struct {
	transacter atomic.Transacter[repository.Resources]
}

// NewSessionReviewArtifactService creates a new SessionReviewArtifactService.
func NewSessionReviewArtifactService(transacter atomic.Transacter[repository.Resources]) *SessionReviewArtifactService {
	return &SessionReviewArtifactService{transacter: transacter}
}

// Upsert creates or updates a session review artifact.
func (s *SessionReviewArtifactService) Upsert(ctx context.Context, link domain.SessionReviewArtifact) error {
	return s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		return res.SessionReviewArtifacts.Upsert(ctx, link)
	})
}

// ListByWorkItemID retrieves session review artifacts by work item ID.
func (s *SessionReviewArtifactService) ListByWorkItemID(ctx context.Context, workItemID string) ([]domain.SessionReviewArtifact, error) {
	var result []domain.SessionReviewArtifact
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		artifacts, err := res.SessionReviewArtifacts.ListByWorkItemID(ctx, workItemID)
		if err != nil {
			return err
		}
		result = artifacts
		return nil
	})
	return result, err
}

// ListByWorkspaceID retrieves session review artifacts by workspace ID.
func (s *SessionReviewArtifactService) ListByWorkspaceID(ctx context.Context, workspaceID string) ([]domain.SessionReviewArtifact, error) {
	var result []domain.SessionReviewArtifact
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		artifacts, err := res.SessionReviewArtifacts.ListByWorkspaceID(ctx, workspaceID)
		if err != nil {
			return err
		}
		result = artifacts
		return nil
	})
	return result, err
}

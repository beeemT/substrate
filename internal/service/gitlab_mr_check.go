package service

import (
	"context"

	"github.com/beeemT/go-atomic"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/repository"
)

// GitlabMRCheckService provides business logic for GitLab MR pipeline jobs.
type GitlabMRCheckService struct {
	transacter atomic.Transacter[repository.Resources]
}

// NewGitlabMRCheckService creates a new GitlabMRCheckService.
func NewGitlabMRCheckService(transacter atomic.Transacter[repository.Resources]) *GitlabMRCheckService {
	return &GitlabMRCheckService{transacter: transacter}
}

// Upsert creates or updates a GitLab MR pipeline job.
func (s *GitlabMRCheckService) Upsert(ctx context.Context, check domain.GitlabMRCheck) error {
	return s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		return res.GitlabMRChecks.Upsert(ctx, check)
	})
}

// ListByMRID retrieves GitLab MR pipeline jobs by MR ID.
func (s *GitlabMRCheckService) ListByMRID(ctx context.Context, mrID string) ([]domain.GitlabMRCheck, error) {
	var result []domain.GitlabMRCheck
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		checks, err := res.GitlabMRChecks.ListByMRID(ctx, mrID)
		if err != nil {
			return err
		}
		result = checks
		return nil
	})
	return result, err
}

// DeleteByMRID deletes all pipeline jobs for a GitLab MR.
func (s *GitlabMRCheckService) DeleteByMRID(ctx context.Context, mrID string) error {
	return s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		return res.GitlabMRChecks.DeleteByMRID(ctx, mrID)
	})
}

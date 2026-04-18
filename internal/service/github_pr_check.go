package service

import (
	"context"

	"github.com/beeemT/go-atomic"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/repository"
)

// GithubPRCheckService provides business logic for GitHub PR check runs.
type GithubPRCheckService struct {
	transacter atomic.Transacter[repository.Resources]
}

// NewGithubPRCheckService creates a new GithubPRCheckService.
func NewGithubPRCheckService(transacter atomic.Transacter[repository.Resources]) *GithubPRCheckService {
	return &GithubPRCheckService{transacter: transacter}
}

// Upsert creates or updates a GitHub PR check run.
func (s *GithubPRCheckService) Upsert(ctx context.Context, check domain.GithubPRCheck) error {
	return s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		return res.GithubPRChecks.Upsert(ctx, check)
	})
}

// ListByPRID retrieves GitHub PR check runs by PR ID.
func (s *GithubPRCheckService) ListByPRID(ctx context.Context, prID string) ([]domain.GithubPRCheck, error) {
	var result []domain.GithubPRCheck
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		checks, err := res.GithubPRChecks.ListByPRID(ctx, prID)
		if err != nil {
			return err
		}
		result = checks
		return nil
	})
	return result, err
}

// DeleteByPRID deletes all check runs for a GitHub PR.
func (s *GithubPRCheckService) DeleteByPRID(ctx context.Context, prID string) error {
	return s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		return res.GithubPRChecks.DeleteByPRID(ctx, prID)
	})
}

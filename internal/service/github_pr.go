package service

import (
	"context"

	"github.com/beeemT/go-atomic"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/repository"
)

// GithubPRService provides business logic for GitHub pull requests.
type GithubPRService struct {
	transacter atomic.Transacter[repository.Resources]
}

// NewGithubPRService creates a new GithubPRService.
func NewGithubPRService(transacter atomic.Transacter[repository.Resources]) *GithubPRService {
	return &GithubPRService{transacter: transacter}
}

// Upsert creates or updates a GitHub pull request.
func (s *GithubPRService) Upsert(ctx context.Context, pr domain.GithubPullRequest) error {
	return s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		return res.GithubPRs.Upsert(ctx, pr)
	})
}

// Get retrieves a GitHub pull request by ID.
func (s *GithubPRService) Get(ctx context.Context, id string) (domain.GithubPullRequest, error) {
	var result domain.GithubPullRequest
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		pr, err := res.GithubPRs.Get(ctx, id)
		if err != nil {
			return err
		}
		result = pr
		return nil
	})
	return result, err
}

// GetByNumber retrieves a GitHub pull request by owner, repo, and number.
func (s *GithubPRService) GetByNumber(ctx context.Context, owner, repo string, number int) (domain.GithubPullRequest, error) {
	var result domain.GithubPullRequest
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		pr, err := res.GithubPRs.GetByNumber(ctx, owner, repo, number)
		if err != nil {
			return err
		}
		result = pr
		return nil
	})
	return result, err
}

// ListByWorkspaceID retrieves GitHub pull requests by workspace ID.
func (s *GithubPRService) ListByWorkspaceID(ctx context.Context, workspaceID string) ([]domain.GithubPullRequest, error) {
	var result []domain.GithubPullRequest
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		prs, err := res.GithubPRs.ListByWorkspaceID(ctx, workspaceID)
		if err != nil {
			return err
		}
		result = prs
		return nil
	})
	return result, err
}

// ListNonTerminal retrieves non-terminal GitHub pull requests by workspace ID.
func (s *GithubPRService) ListNonTerminal(ctx context.Context, workspaceID string) ([]domain.GithubPullRequest, error) {
	var result []domain.GithubPullRequest
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		prs, err := res.GithubPRs.ListNonTerminal(ctx, workspaceID)
		if err != nil {
			return err
		}
		result = prs
		return nil
	})
	return result, err
}

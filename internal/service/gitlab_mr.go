package service

import (
	"context"

	"github.com/beeemT/go-atomic"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/repository"
)

// GitlabMRService provides business logic for GitLab merge requests.
type GitlabMRService struct {
	transacter atomic.Transacter[repository.Resources]
}

// NewGitlabMRService creates a new GitlabMRService.
func NewGitlabMRService(transacter atomic.Transacter[repository.Resources]) *GitlabMRService {
	return &GitlabMRService{transacter: transacter}
}

// Upsert creates or updates a GitLab merge request.
func (s *GitlabMRService) Upsert(ctx context.Context, mr domain.GitlabMergeRequest) error {
	return s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		return res.GitlabMRs.Upsert(ctx, mr)
	})
}

// Get retrieves a GitLab merge request by ID.
func (s *GitlabMRService) Get(ctx context.Context, id string) (domain.GitlabMergeRequest, error) {
	var result domain.GitlabMergeRequest
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		mr, err := res.GitlabMRs.Get(ctx, id)
		if err != nil {
			return err
		}
		result = mr
		return nil
	})
	return result, err
}

// GetByIID retrieves a GitLab merge request by project path and IID.
func (s *GitlabMRService) GetByIID(ctx context.Context, projectPath string, iid int) (domain.GitlabMergeRequest, error) {
	var result domain.GitlabMergeRequest
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		mr, err := res.GitlabMRs.GetByIID(ctx, projectPath, iid)
		if err != nil {
			return err
		}
		result = mr
		return nil
	})
	return result, err
}

// ListByWorkspaceID retrieves GitLab merge requests by workspace ID.
func (s *GitlabMRService) ListByWorkspaceID(ctx context.Context, workspaceID string) ([]domain.GitlabMergeRequest, error) {
	var result []domain.GitlabMergeRequest
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		mrs, err := res.GitlabMRs.ListByWorkspaceID(ctx, workspaceID)
		if err != nil {
			return err
		}
		result = mrs
		return nil
	})
	return result, err
}

// ListNonTerminal retrieves non-terminal GitLab merge requests by workspace ID.
func (s *GitlabMRService) ListNonTerminal(ctx context.Context, workspaceID string) ([]domain.GitlabMergeRequest, error) {
	var result []domain.GitlabMergeRequest
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		mrs, err := res.GitlabMRs.ListNonTerminal(ctx, workspaceID)
		if err != nil {
			return err
		}
		result = mrs
		return nil
	})
	return result, err
}

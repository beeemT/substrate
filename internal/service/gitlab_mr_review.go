package service

import (
	"context"

	"github.com/beeemT/go-atomic"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/repository"
)

// GitlabMRReviewService provides business logic for GitLab MR reviews.
type GitlabMRReviewService struct {
	transacter atomic.Transacter[repository.Resources]
}

// NewGitlabMRReviewService creates a new GitlabMRReviewService.
func NewGitlabMRReviewService(transacter atomic.Transacter[repository.Resources]) *GitlabMRReviewService {
	return &GitlabMRReviewService{transacter: transacter}
}

// Upsert creates or updates a GitLab MR review.
func (s *GitlabMRReviewService) Upsert(ctx context.Context, review domain.GitlabMRReview) error {
	return s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		return res.GitlabMRReviews.Upsert(ctx, review)
	})
}

// ListByMRID retrieves GitLab MR reviews by MR ID.
func (s *GitlabMRReviewService) ListByMRID(ctx context.Context, mrID string) ([]domain.GitlabMRReview, error) {
	var result []domain.GitlabMRReview
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		reviews, err := res.GitlabMRReviews.ListByMRID(ctx, mrID)
		if err != nil {
			return err
		}
		result = reviews
		return nil
	})
	return result, err
}

// DeleteByMRID deletes all reviews for a GitLab MR.
func (s *GitlabMRReviewService) DeleteByMRID(ctx context.Context, mrID string) error {
	return s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		return res.GitlabMRReviews.DeleteByMRID(ctx, mrID)
	})
}

package service

import (
	"context"

	"github.com/beeemT/go-atomic"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/repository"
)

// GithubPRReviewService provides business logic for GitHub PR reviews.
type GithubPRReviewService struct {
	transacter atomic.Transacter[repository.Resources]
}

// NewGithubPRReviewService creates a new GithubPRReviewService.
func NewGithubPRReviewService(transacter atomic.Transacter[repository.Resources]) *GithubPRReviewService {
	return &GithubPRReviewService{transacter: transacter}
}

// Upsert creates or updates a GitHub PR review.
func (s *GithubPRReviewService) Upsert(ctx context.Context, review domain.GithubPRReview) error {
	return s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		return res.GithubPRReviews.Upsert(ctx, review)
	})
}

// ListByPRID retrieves GitHub PR reviews by PR ID.
func (s *GithubPRReviewService) ListByPRID(ctx context.Context, prID string) ([]domain.GithubPRReview, error) {
	var result []domain.GithubPRReview
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		reviews, err := res.GithubPRReviews.ListByPRID(ctx, prID)
		if err != nil {
			return err
		}
		result = reviews
		return nil
	})
	return result, err
}

// DeleteByPRID deletes all reviews for a GitHub PR.
func (s *GithubPRReviewService) DeleteByPRID(ctx context.Context, prID string) error {
	return s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		return res.GithubPRReviews.DeleteByPRID(ctx, prID)
	})
}

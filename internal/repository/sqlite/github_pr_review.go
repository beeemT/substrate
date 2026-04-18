package sqlite

import (
	"context"
	"fmt"

	"github.com/beeemT/go-atomic/generic"
	"github.com/beeemT/substrate/internal/domain"
)

type githubPRReviewRow struct {
	ID            string `db:"id"`
	PRID          string `db:"pr_id"`
	ReviewerLogin string `db:"reviewer_login"`
	State         string `db:"state"`
	SubmittedAt   string `db:"submitted_at"`
	CreatedAt     string `db:"created_at"`
	UpdatedAt     string `db:"updated_at"`
}

func (r *githubPRReviewRow) toDomain() (domain.GithubPRReview, error) {
	submittedAt, err := parseTime(r.SubmittedAt)
	if err != nil {
		return domain.GithubPRReview{}, fmt.Errorf("submitted_at: %w", err)
	}
	createdAt, err := parseTime(r.CreatedAt)
	if err != nil {
		return domain.GithubPRReview{}, fmt.Errorf("created_at: %w", err)
	}
	updatedAt, err := parseTime(r.UpdatedAt)
	if err != nil {
		return domain.GithubPRReview{}, fmt.Errorf("updated_at: %w", err)
	}
	return domain.GithubPRReview{
		ID:            r.ID,
		PRID:          r.PRID,
		ReviewerLogin: r.ReviewerLogin,
		State:         r.State,
		SubmittedAt:   submittedAt,
		CreatedAt:     createdAt,
		UpdatedAt:     updatedAt,
	}, nil
}

func rowFromGithubPRReview(review domain.GithubPRReview) githubPRReviewRow {
	return githubPRReviewRow{
		ID:            review.ID,
		PRID:          review.PRID,
		ReviewerLogin: review.ReviewerLogin,
		State:         review.State,
		SubmittedAt:   formatTime(review.SubmittedAt),
		CreatedAt:     formatTime(review.CreatedAt),
		UpdatedAt:     formatTime(review.UpdatedAt),
	}
}

// GithubPRReviewRepo implements repository.GithubPRReviewRepository using SQLite.
type GithubPRReviewRepo struct{ remote generic.SQLXRemote }

func NewGithubPRReviewRepo(remote generic.SQLXRemote) GithubPRReviewRepo {
	return GithubPRReviewRepo{remote: remote}
}

func (r GithubPRReviewRepo) Upsert(ctx context.Context, review domain.GithubPRReview) error {
	row := rowFromGithubPRReview(review)
	_, err := r.remote.NamedExecContext(ctx,
		`INSERT INTO github_pr_reviews (id, pr_id, reviewer_login, state, submitted_at, created_at, updated_at)
		 VALUES (:id, :pr_id, :reviewer_login, :state, :submitted_at, :created_at, :updated_at)
		 ON CONFLICT(pr_id, reviewer_login) DO UPDATE SET
		   state = excluded.state,
		   submitted_at = excluded.submitted_at,
		   updated_at = excluded.updated_at`, row)
	if err != nil {
		return fmt.Errorf("upsert github pr review %s: %w", review.ID, err)
	}
	return nil
}

func (r GithubPRReviewRepo) ListByPRID(ctx context.Context, prID string) ([]domain.GithubPRReview, error) {
	var rows []githubPRReviewRow
	if err := r.remote.SelectContext(ctx, &rows,
		`SELECT * FROM github_pr_reviews WHERE pr_id = ? ORDER BY submitted_at DESC`, prID); err != nil {
		return nil, fmt.Errorf("list github pr reviews for pr %s: %w", prID, err)
	}
	reviews := make([]domain.GithubPRReview, len(rows))
	for i := range rows {
		review, err := rows[i].toDomain()
		if err != nil {
			return nil, fmt.Errorf("convert github pr review: %w", err)
		}
		reviews[i] = review
	}
	return reviews, nil
}

func (r GithubPRReviewRepo) DeleteByPRID(ctx context.Context, prID string) error {
	_, err := r.remote.NamedExecContext(ctx, `DELETE FROM github_pr_reviews WHERE pr_id = :pr_id`, map[string]any{"pr_id": prID})
	if err != nil {
		return fmt.Errorf("delete github pr reviews for pr %s: %w", prID, err)
	}
	return nil
}

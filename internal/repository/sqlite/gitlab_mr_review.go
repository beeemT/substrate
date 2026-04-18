package sqlite

import (
	"context"
	"fmt"

	"github.com/beeemT/go-atomic/generic"
	"github.com/beeemT/substrate/internal/domain"
)

type gitlabMRReviewRow struct {
	ID            string `db:"id"`
	MRID          string `db:"mr_id"`
	ReviewerLogin string `db:"reviewer_login"`
	State         string `db:"state"`
	SubmittedAt   string `db:"submitted_at"`
	CreatedAt     string `db:"created_at"`
	UpdatedAt     string `db:"updated_at"`
}

func (r *gitlabMRReviewRow) toDomain() (domain.GitlabMRReview, error) {
	submittedAt, err := parseTime(r.SubmittedAt)
	if err != nil {
		return domain.GitlabMRReview{}, fmt.Errorf("submitted_at: %w", err)
	}
	createdAt, err := parseTime(r.CreatedAt)
	if err != nil {
		return domain.GitlabMRReview{}, fmt.Errorf("created_at: %w", err)
	}
	updatedAt, err := parseTime(r.UpdatedAt)
	if err != nil {
		return domain.GitlabMRReview{}, fmt.Errorf("updated_at: %w", err)
	}
	return domain.GitlabMRReview{
		ID:            r.ID,
		MRID:          r.MRID,
		ReviewerLogin: r.ReviewerLogin,
		State:         r.State,
		SubmittedAt:   submittedAt,
		CreatedAt:     createdAt,
		UpdatedAt:     updatedAt,
	}, nil
}

func rowFromGitlabMRReview(review domain.GitlabMRReview) gitlabMRReviewRow {
	return gitlabMRReviewRow{
		ID:            review.ID,
		MRID:          review.MRID,
		ReviewerLogin: review.ReviewerLogin,
		State:         review.State,
		SubmittedAt:   formatTime(review.SubmittedAt),
		CreatedAt:     formatTime(review.CreatedAt),
		UpdatedAt:     formatTime(review.UpdatedAt),
	}
}

// GitlabMRReviewRepo implements repository.GitlabMRReviewRepository using SQLite.
type GitlabMRReviewRepo struct{ remote generic.SQLXRemote }

func NewGitlabMRReviewRepo(remote generic.SQLXRemote) GitlabMRReviewRepo {
	return GitlabMRReviewRepo{remote: remote}
}

func (r GitlabMRReviewRepo) Upsert(ctx context.Context, review domain.GitlabMRReview) error {
	row := rowFromGitlabMRReview(review)
	_, err := r.remote.NamedExecContext(ctx,
		`INSERT INTO gitlab_mr_reviews (id, mr_id, reviewer_login, state, submitted_at, created_at, updated_at)
		 VALUES (:id, :mr_id, :reviewer_login, :state, :submitted_at, :created_at, :updated_at)
		 ON CONFLICT(mr_id, reviewer_login) DO UPDATE SET
		   state = excluded.state,
		   submitted_at = excluded.submitted_at,
		   updated_at = excluded.updated_at`, row)
	if err != nil {
		return fmt.Errorf("upsert gitlab mr review %s: %w", review.ID, err)
	}
	return nil
}

func (r GitlabMRReviewRepo) ListByMRID(ctx context.Context, mrID string) ([]domain.GitlabMRReview, error) {
	var rows []gitlabMRReviewRow
	if err := r.remote.SelectContext(ctx, &rows,
		`SELECT * FROM gitlab_mr_reviews WHERE mr_id = ? ORDER BY submitted_at DESC`, mrID); err != nil {
		return nil, fmt.Errorf("list gitlab mr reviews for mr %s: %w", mrID, err)
	}
	reviews := make([]domain.GitlabMRReview, len(rows))
	for i := range rows {
		review, err := rows[i].toDomain()
		if err != nil {
			return nil, fmt.Errorf("convert gitlab mr review: %w", err)
		}
		reviews[i] = review
	}
	return reviews, nil
}

func (r GitlabMRReviewRepo) DeleteByMRID(ctx context.Context, mrID string) error {
	_, err := r.remote.NamedExecContext(ctx, `DELETE FROM gitlab_mr_reviews WHERE mr_id = :mr_id`, map[string]any{"mr_id": mrID})
	if err != nil {
		return fmt.Errorf("delete gitlab mr reviews for mr %s: %w", mrID, err)
	}
	return nil
}

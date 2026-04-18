package sqlite

import (
	"context"
	"fmt"

	"github.com/beeemT/go-atomic/generic"
	"github.com/beeemT/substrate/internal/domain"
)

type githubPRCheckRow struct {
	ID         string `db:"id"`
	PRID       string `db:"pr_id"`
	Name       string `db:"name"`
	Status     string `db:"status"`
	Conclusion string `db:"conclusion"`
	CreatedAt  string `db:"created_at"`
	UpdatedAt  string `db:"updated_at"`
}

func (r *githubPRCheckRow) toDomain() (domain.GithubPRCheck, error) {
	createdAt, err := parseTime(r.CreatedAt)
	if err != nil {
		return domain.GithubPRCheck{}, fmt.Errorf("created_at: %w", err)
	}
	updatedAt, err := parseTime(r.UpdatedAt)
	if err != nil {
		return domain.GithubPRCheck{}, fmt.Errorf("updated_at: %w", err)
	}
	return domain.GithubPRCheck{
		ID:         r.ID,
		PRID:       r.PRID,
		Name:       r.Name,
		Status:     r.Status,
		Conclusion: r.Conclusion,
		CreatedAt:  createdAt,
		UpdatedAt:  updatedAt,
	}, nil
}

func rowFromGithubPRCheck(check domain.GithubPRCheck) githubPRCheckRow {
	return githubPRCheckRow{
		ID:         check.ID,
		PRID:       check.PRID,
		Name:       check.Name,
		Status:     check.Status,
		Conclusion: check.Conclusion,
		CreatedAt:  formatTime(check.CreatedAt),
		UpdatedAt:  formatTime(check.UpdatedAt),
	}
}

// GithubPRCheckRepo implements repository.GithubPRCheckRepository using SQLite.
type GithubPRCheckRepo struct{ remote generic.SQLXRemote }

func NewGithubPRCheckRepo(remote generic.SQLXRemote) GithubPRCheckRepo {
	return GithubPRCheckRepo{remote: remote}
}

func (r GithubPRCheckRepo) Upsert(ctx context.Context, check domain.GithubPRCheck) error {
	row := rowFromGithubPRCheck(check)
	_, err := r.remote.NamedExecContext(ctx,
		`INSERT INTO github_pr_checks (id, pr_id, name, status, conclusion, created_at, updated_at)
		 VALUES (:id, :pr_id, :name, :status, :conclusion, :created_at, :updated_at)
		 ON CONFLICT(pr_id, name) DO UPDATE SET
		   status = excluded.status,
		   conclusion = excluded.conclusion,
		   updated_at = excluded.updated_at`, row)
	if err != nil {
		return fmt.Errorf("upsert github pr check %s: %w", check.ID, err)
	}
	return nil
}

func (r GithubPRCheckRepo) ListByPRID(ctx context.Context, prID string) ([]domain.GithubPRCheck, error) {
	var rows []githubPRCheckRow
	if err := r.remote.SelectContext(ctx, &rows,
		`SELECT * FROM github_pr_checks WHERE pr_id = ? ORDER BY name`, prID); err != nil {
		return nil, fmt.Errorf("list github pr checks for pr %s: %w", prID, err)
	}
	checks := make([]domain.GithubPRCheck, len(rows))
	for i := range rows {
		check, err := rows[i].toDomain()
		if err != nil {
			return nil, fmt.Errorf("convert github pr check: %w", err)
		}
		checks[i] = check
	}
	return checks, nil
}

func (r GithubPRCheckRepo) DeleteByPRID(ctx context.Context, prID string) error {
	_, err := r.remote.NamedExecContext(ctx, `DELETE FROM github_pr_checks WHERE pr_id = :pr_id`, map[string]any{"pr_id": prID})
	if err != nil {
		return fmt.Errorf("delete github pr checks for pr %s: %w", prID, err)
	}
	return nil
}

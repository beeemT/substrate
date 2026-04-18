package sqlite

import (
	"context"
	"fmt"

	"github.com/beeemT/go-atomic/generic"
	"github.com/beeemT/substrate/internal/domain"
)

type gitlabMRCheckRow struct {
	ID         string `db:"id"`
	MRID       string `db:"mr_id"`
	Name       string `db:"name"`
	Status     string `db:"status"`
	Conclusion string `db:"conclusion"`
	CreatedAt  string `db:"created_at"`
	UpdatedAt  string `db:"updated_at"`
}

func (r *gitlabMRCheckRow) toDomain() (domain.GitlabMRCheck, error) {
	createdAt, err := parseTime(r.CreatedAt)
	if err != nil {
		return domain.GitlabMRCheck{}, fmt.Errorf("created_at: %w", err)
	}
	updatedAt, err := parseTime(r.UpdatedAt)
	if err != nil {
		return domain.GitlabMRCheck{}, fmt.Errorf("updated_at: %w", err)
	}
	return domain.GitlabMRCheck{
		ID:         r.ID,
		MRID:       r.MRID,
		Name:       r.Name,
		Status:     r.Status,
		Conclusion: r.Conclusion,
		CreatedAt:  createdAt,
		UpdatedAt:  updatedAt,
	}, nil
}

func rowFromGitlabMRCheck(check domain.GitlabMRCheck) gitlabMRCheckRow {
	return gitlabMRCheckRow{
		ID:         check.ID,
		MRID:       check.MRID,
		Name:       check.Name,
		Status:     check.Status,
		Conclusion: check.Conclusion,
		CreatedAt:  formatTime(check.CreatedAt),
		UpdatedAt:  formatTime(check.UpdatedAt),
	}
}

// GitlabMRCheckRepo implements repository.GitlabMRCheckRepository using SQLite.
type GitlabMRCheckRepo struct{ remote generic.SQLXRemote }

func NewGitlabMRCheckRepo(remote generic.SQLXRemote) GitlabMRCheckRepo {
	return GitlabMRCheckRepo{remote: remote}
}

func (r GitlabMRCheckRepo) Upsert(ctx context.Context, check domain.GitlabMRCheck) error {
	row := rowFromGitlabMRCheck(check)
	_, err := r.remote.NamedExecContext(ctx,
		`INSERT INTO gitlab_mr_checks (id, mr_id, name, status, conclusion, created_at, updated_at)
		 VALUES (:id, :mr_id, :name, :status, :conclusion, :created_at, :updated_at)
		 ON CONFLICT(mr_id, name) DO UPDATE SET
		   status = excluded.status,
		   conclusion = excluded.conclusion,
		   updated_at = excluded.updated_at`, row)
	if err != nil {
		return fmt.Errorf("upsert gitlab mr check %s: %w", check.ID, err)
	}
	return nil
}

func (r GitlabMRCheckRepo) ListByMRID(ctx context.Context, mrID string) ([]domain.GitlabMRCheck, error) {
	var rows []gitlabMRCheckRow
	if err := r.remote.SelectContext(ctx, &rows,
		`SELECT * FROM gitlab_mr_checks WHERE mr_id = ? ORDER BY name`, mrID); err != nil {
		return nil, fmt.Errorf("list gitlab mr checks for mr %s: %w", mrID, err)
	}
	checks := make([]domain.GitlabMRCheck, len(rows))
	for i := range rows {
		check, err := rows[i].toDomain()
		if err != nil {
			return nil, fmt.Errorf("convert gitlab mr check: %w", err)
		}
		checks[i] = check
	}
	return checks, nil
}

func (r GitlabMRCheckRepo) DeleteByMRID(ctx context.Context, mrID string) error {
	_, err := r.remote.NamedExecContext(ctx, `DELETE FROM gitlab_mr_checks WHERE mr_id = :mr_id`, map[string]any{"mr_id": mrID})
	if err != nil {
		return fmt.Errorf("delete gitlab mr checks for mr %s: %w", mrID, err)
	}
	return nil
}

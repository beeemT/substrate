package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/beeemT/go-atomic/generic"
	"github.com/beeemT/substrate/internal/domain"
)

type subPlanRow struct {
	ID        string `db:"id"`
	PlanID    string `db:"plan_id"`
	RepoName  string `db:"repo_name"`
	Content   string `db:"content"`
	ExecOrder int    `db:"exec_order"`
	Status    string `db:"status"`
	CreatedAt string `db:"created_at"`
	UpdatedAt string `db:"updated_at"`
}

func (r *subPlanRow) toDomain() (domain.SubPlan, error) {
	createdAt, err := parseTime(r.CreatedAt)
	if err != nil {
		return domain.SubPlan{}, fmt.Errorf("created_at: %w", err)
	}
	updatedAt, err := parseTime(r.UpdatedAt)
	if err != nil {
		return domain.SubPlan{}, fmt.Errorf("updated_at: %w", err)
	}
	return domain.SubPlan{
		ID:             r.ID,
		PlanID:         r.PlanID,
		RepositoryName: r.RepoName,
		Content:        r.Content,
		Order:          r.ExecOrder,
		Status:         domain.SubPlanStatus(r.Status),
		CreatedAt:      createdAt,
		UpdatedAt:      updatedAt,
	}, nil
}

func rowFromSubPlan(sp domain.SubPlan) subPlanRow {
	return subPlanRow{
		ID:        sp.ID,
		PlanID:    sp.PlanID,
		RepoName:  sp.RepositoryName,
		Content:   sp.Content,
		ExecOrder: sp.Order,
		Status:    string(sp.Status),
		CreatedAt: formatTime(sp.CreatedAt),
		UpdatedAt: formatTime(sp.UpdatedAt),
	}
}

// SubPlanRepo implements repository.SubPlanRepository using SQLite.
type SubPlanRepo struct{ remote generic.SQLXRemote }

func NewSubPlanRepo(remote generic.SQLXRemote) SubPlanRepo {
	return SubPlanRepo{remote: remote}
}

func (r SubPlanRepo) Get(ctx context.Context, id string) (domain.SubPlan, error) {
	var row subPlanRow
	if err := r.remote.GetContext(ctx, &row, `SELECT * FROM sub_plans WHERE id = ?`, id); err != nil {
		return domain.SubPlan{}, fmt.Errorf("get sub-plan %s: %w", id, err)
	}
	return row.toDomain()
}

func (r SubPlanRepo) ListByPlanID(ctx context.Context, planID string) ([]domain.SubPlan, error) {
	var rows []subPlanRow
	if err := r.remote.SelectContext(ctx, &rows, `SELECT * FROM sub_plans WHERE plan_id = ? ORDER BY exec_order`, planID); err != nil {
		return nil, fmt.Errorf("list sub-plans for plan %s: %w", planID, err)
	}
	sps := make([]domain.SubPlan, len(rows))
	for i := range rows {
		sp, err := rows[i].toDomain()
		if err != nil {
			return nil, fmt.Errorf("convert sub-plan: %w", err)
		}
		sps[i] = sp
	}
	return sps, nil
}

func (r SubPlanRepo) Create(ctx context.Context, sp domain.SubPlan) error {
	row := rowFromSubPlan(sp)
	_, err := r.remote.NamedExecContext(ctx,
		`INSERT INTO sub_plans (id, plan_id, repo_name, content, exec_order, status, created_at, updated_at)
		 VALUES (:id, :plan_id, :repo_name, :content, :exec_order, :status, :created_at, :updated_at)`, row)
	if err != nil {
		return fmt.Errorf("create sub-plan %s: %w", sp.ID, err)
	}
	return nil
}

func (r SubPlanRepo) Update(ctx context.Context, sp domain.SubPlan) error {
	row := rowFromSubPlan(sp)
	res, err := r.remote.NamedExecContext(ctx,
		`UPDATE sub_plans SET plan_id = :plan_id, repo_name = :repo_name, content = :content,
		 exec_order = :exec_order, status = :status, updated_at = :updated_at WHERE id = :id`, row)
	if err != nil {
		return fmt.Errorf("update sub-plan %s: %w", sp.ID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update sub-plan %s: get rows affected: %w", sp.ID, err)
	}
	if n == 0 {
		return fmt.Errorf("update sub-plan %s: %w", sp.ID, sql.ErrNoRows)
	}
	return nil
}

func (r SubPlanRepo) Delete(ctx context.Context, id string) error {
	_, err := r.remote.NamedExecContext(ctx, `DELETE FROM sub_plans WHERE id = :id`, map[string]any{"id": id})
	if err != nil {
		return fmt.Errorf("delete sub-plan %s: %w", id, err)
	}
	return nil
}

package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/beeemT/go-atomic/generic"
	"github.com/beeemT/substrate/internal/domain"
)

type planRow struct {
	ID               string `db:"id"`
	WorkItemID       string `db:"work_item_id"`
	OrchestratorPlan string `db:"orchestrator_plan"`
	Status           string `db:"status"`
	Version          int    `db:"version"`
	FAQ              string `db:"faq"`
	CreatedAt        string `db:"created_at"`
	UpdatedAt        string `db:"updated_at"`
}

func (r *planRow) toDomain() (domain.Plan, error) {
	createdAt, err := parseTime(r.CreatedAt)
	if err != nil {
		return domain.Plan{}, fmt.Errorf("created_at: %w", err)
	}
	updatedAt, err := parseTime(r.UpdatedAt)
	if err != nil {
		return domain.Plan{}, fmt.Errorf("updated_at: %w", err)
	}
	// Parse FAQ from JSON
	var faq []domain.FAQEntry
	if r.FAQ != "" {
		if err := json.Unmarshal([]byte(r.FAQ), &faq); err != nil {
			faq = []domain.FAQEntry{} // Default to empty on parse error
		}
	}
	return domain.Plan{
		ID:               r.ID,
		WorkItemID:       r.WorkItemID,
		OrchestratorPlan: r.OrchestratorPlan,
		Status:           domain.PlanStatus(r.Status),
		Version:          r.Version,
		FAQ:              faq,
		CreatedAt:        createdAt,
		UpdatedAt:        updatedAt,
	}, nil
}

func rowFromPlan(p domain.Plan) planRow {
	faqJSON, err := json.Marshal(p.FAQ)
	if err != nil {
		faqJSON = []byte("[]") // Default to empty array
	}
	return planRow{
		ID:               p.ID,
		WorkItemID:       p.WorkItemID,
		OrchestratorPlan: p.OrchestratorPlan,
		Status:           string(p.Status),
		Version:          p.Version,
		FAQ:              string(faqJSON),
		CreatedAt:        formatTime(p.CreatedAt),
		UpdatedAt:        formatTime(p.UpdatedAt),
	}
}

// PlanRepo implements repository.PlanRepository using SQLite.
type PlanRepo struct{ remote generic.SQLXRemote }

func NewPlanRepo(remote generic.SQLXRemote) PlanRepo {
	return PlanRepo{remote: remote}
}

func (r PlanRepo) Get(ctx context.Context, id string) (domain.Plan, error) {
	var row planRow
	if err := r.remote.GetContext(ctx, &row, `SELECT * FROM plans WHERE id = ?`, id); err != nil {
		return domain.Plan{}, fmt.Errorf("get plan %s: %w", id, err)
	}
	return row.toDomain()
}

func (r PlanRepo) GetByWorkItemID(ctx context.Context, workItemID string) (domain.Plan, error) {
	var row planRow
	if err := r.remote.GetContext(ctx, &row, `SELECT * FROM plans WHERE work_item_id = ?`, workItemID); err != nil {
		return domain.Plan{}, fmt.Errorf("get plan by work item %s: %w", workItemID, err)
	}
	return row.toDomain()
}

func (r PlanRepo) Create(ctx context.Context, p domain.Plan) error {
	row := rowFromPlan(p)
	_, err := r.remote.NamedExecContext(ctx,
		`INSERT INTO plans (id, work_item_id, orchestrator_plan, status, version, faq, created_at, updated_at)
		 VALUES (:id, :work_item_id, :orchestrator_plan, :status, :version, :faq, :created_at, :updated_at)`, row)
	if err != nil {
		return fmt.Errorf("create plan %s: %w", p.ID, err)
	}
	return nil
}

func (r PlanRepo) Update(ctx context.Context, p domain.Plan) error {
	row := rowFromPlan(p)
	res, err := r.remote.NamedExecContext(ctx,
		`UPDATE plans SET work_item_id = :work_item_id, orchestrator_plan = :orchestrator_plan,
		 status = :status, version = :version, faq = :faq, updated_at = :updated_at WHERE id = :id`, row)
	if err != nil {
		return fmt.Errorf("update plan %s: %w", p.ID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update plan %s: get rows affected: %w", p.ID, err)
	}
	if n == 0 {
		return fmt.Errorf("update plan %s: %w", p.ID, sql.ErrNoRows)
	}
	return nil
}

func (r PlanRepo) Delete(ctx context.Context, id string) error {
	_, err := r.remote.NamedExecContext(ctx, `DELETE FROM plans WHERE id = :id`, map[string]any{"id": id})
	if err != nil {
		return fmt.Errorf("delete plan %s: %w", id, err)
	}
	return nil
}

// AppendFAQ adds a new FAQ entry to the plan's FAQ list.
func (r PlanRepo) AppendFAQ(ctx context.Context, entry domain.FAQEntry) error {
	// Get current plan
	plan, err := r.Get(ctx, entry.PlanID)
	if err != nil {
		return fmt.Errorf("get plan: %w", err)
	}

	// Append entry to FAQ
	plan.FAQ = append(plan.FAQ, entry)

	// Update plan
	return r.Update(ctx, plan)
}

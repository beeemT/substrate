package sqlite

import (
	"context"

	"github.com/beeemT/go-atomic/generic"
	goatomicsqlx "github.com/beeemT/go-atomic/generic/sqlx"
	"github.com/beeemT/substrate/internal/repository"
	"github.com/jmoiron/sqlx"
)

// PlanTransacter wraps the generic.Transacter to provide plan+sub-plan
// operations within a single database transaction.
type PlanTransacter struct {
	transacter generic.Transacter[generic.SQLXRemote, Resources]
}

// NewPlanTransacter creates a PlanTransacter backed by the given sqlx.DB.
func NewPlanTransacter(db *sqlx.DB) PlanTransacter {
	executer := goatomicsqlx.NewExecuter(db)
	return PlanTransacter{
		transacter: generic.NewTransacter[generic.SQLXRemote, Resources](executer, ResourcesFactory),
	}
}

// TransactPlanRepos runs fn with plan and sub-plan repos bound to the same database transaction.
func (t PlanTransacter) TransactPlanRepos(ctx context.Context, fn func(ctx context.Context, planRepo repository.PlanRepository, subPlanRepo repository.TaskPlanRepository) error) error {
	return t.transacter.Transact(ctx, func(ctx context.Context, res Resources) error {
		return fn(ctx, res.Plans, res.SubPlans)
	})
}

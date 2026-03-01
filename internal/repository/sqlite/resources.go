package sqlite

import (
	"context"

	"github.com/beeemT/go-atomic/generic"
)

// Resources groups all transaction-bound repos. Every field is bound to the
// same *sqlx.Tx when created via ResourcesFactory inside a Transact call.
type Resources struct {
	WorkItems  WorkItemRepo
	Plans      PlanRepo
	SubPlans   SubPlanRepo
	Workspaces WorkspaceRepo
	Sessions   SessionRepo
	Reviews    ReviewRepo
	Questions  QuestionRepo
	Events     EventRepo
	Instances  InstanceRepo
}

// ResourcesFactory creates a Resources from a transaction handle.
// It is passed to generic.NewTransacter to bind all repos to the same transaction.
func ResourcesFactory(
	_ context.Context,
	_ *generic.Transacter[generic.SQLXRemote, Resources],
	tx generic.SQLXRemote,
) (Resources, error) {
	return Resources{
		WorkItems:  NewWorkItemRepo(tx),
		Plans:      NewPlanRepo(tx),
		SubPlans:   NewSubPlanRepo(tx),
		Workspaces: NewWorkspaceRepo(tx),
		Sessions:   NewSessionRepo(tx),
		Reviews:    NewReviewRepo(tx),
		Questions:  NewQuestionRepo(tx),
		Events:     NewEventRepo(tx),
		Instances:  NewInstanceRepo(tx),
	}, nil
}

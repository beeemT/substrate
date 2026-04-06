package sqlite

import (
	"context"

	"github.com/beeemT/go-atomic/generic"
	"github.com/beeemT/substrate/internal/repository"
)

// ResourcesFactory creates transaction-bound repositories from a transaction handle.
func ResourcesFactory(
	_ context.Context,
	_ *generic.Transacter[generic.SQLXRemote, repository.Resources],
	tx generic.SQLXRemote,
) (repository.Resources, error) {
	return repository.Resources{
		Sessions:               NewSessionRepo(tx),
		Plans:                  NewPlanRepo(tx),
		SubPlans:               NewSubPlanRepo(tx),
		Workspaces:             NewWorkspaceRepo(tx),
		NewSessionFilters:      NewSessionFilterRepo(tx),
		NewSessionFilterLocks:  NewSessionFilterLockRepo(tx),
		Tasks:                  NewTaskRepo(tx),
		Reviews:                NewReviewRepo(tx),
		Questions:              NewQuestionRepo(tx),
		Events:                 NewEventRepo(tx),
		Instances:              NewInstanceRepo(tx),
		GithubPRs:              NewGithubPRRepo(tx),
		GitlabMRs:              NewGitlabMRRepo(tx),
		SessionReviewArtifacts: NewSessionReviewArtifactRepo(tx),
	}, nil
}

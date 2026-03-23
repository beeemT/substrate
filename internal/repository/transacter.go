package repository

import (
	"context"

	"github.com/beeemT/go-atomic"
)

// Resources groups all transaction-bound repositories. Every field is bound
// to the same database transaction when created inside a Transact call.
type Resources struct {
	Sessions               SessionRepository
	Plans                  PlanRepository
	SubPlans               TaskPlanRepository
	Workspaces             WorkspaceRepository
	Tasks                  TaskRepository
	Reviews                ReviewRepository
	Questions              QuestionRepository
	Events                 EventRepository
	Instances              InstanceRepository
	GithubPRs              GithubPullRequestRepository
	GitlabMRs              GitlabMergeRequestRepository
	SessionReviewArtifacts SessionReviewArtifactRepository
}

// NoopTransacter calls fn directly with the stored Resources without
// transaction semantics. For tests only.
type NoopTransacter struct {
	Res Resources
}

var _ atomic.Transacter[Resources] = NoopTransacter{}

func (t NoopTransacter) Transact(ctx context.Context, fn func(context.Context, Resources) error) error {
	return fn(ctx, t.Res)
}

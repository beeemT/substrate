package sqlite

import "github.com/beeemT/substrate/internal/repository"

// Compile-time interface compliance checks.
var (
	_ repository.SessionRepository               = SessionRepo{}
	_ repository.PlanRepository                  = PlanRepo{}
	_ repository.TaskPlanRepository              = SubPlanRepo{}
	_ repository.WorkspaceRepository             = WorkspaceRepo{}
	_ repository.NewSessionFilterRepository      = SessionFilterRepo{}
	_ repository.NewSessionFilterLockRepository  = SessionFilterLockRepo{}
	_ repository.TaskRepository                  = TaskRepo{}
	_ repository.ReviewRepository                = ReviewRepo{}
	_ repository.QuestionRepository              = QuestionRepo{}
	_ repository.EventRepository                 = EventRepo{}
	_ repository.InstanceRepository              = InstanceRepo{}
	_ repository.GithubPullRequestRepository     = GithubPRRepo{}
	_ repository.GitlabMergeRequestRepository    = GitlabMRRepo{}
	_ repository.SessionReviewArtifactRepository = SessionReviewArtifactRepo{}
)

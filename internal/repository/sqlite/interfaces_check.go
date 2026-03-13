package sqlite

import "github.com/beeemT/substrate/internal/repository"

// Compile-time interface compliance checks.
var (
	_ repository.SessionRepository   = SessionRepo{}
	_ repository.PlanRepository      = PlanRepo{}
	_ repository.TaskPlanRepository  = SubPlanRepo{}
	_ repository.WorkspaceRepository = WorkspaceRepo{}
	_ repository.TaskRepository      = TaskRepo{}
	_ repository.ReviewRepository    = ReviewRepo{}
	_ repository.QuestionRepository  = QuestionRepo{}
	_ repository.EventRepository     = EventRepo{}
	_ repository.InstanceRepository  = InstanceRepo{}
)

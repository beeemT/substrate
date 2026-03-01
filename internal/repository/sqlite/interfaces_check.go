package sqlite

import "github.com/beeemT/substrate/internal/repository"

// Compile-time interface compliance checks.
var (
	_ repository.WorkItemRepository  = WorkItemRepo{}
	_ repository.PlanRepository      = PlanRepo{}
	_ repository.SubPlanRepository   = SubPlanRepo{}
	_ repository.WorkspaceRepository = WorkspaceRepo{}
	_ repository.SessionRepository   = SessionRepo{}
	_ repository.ReviewRepository    = ReviewRepo{}
	_ repository.QuestionRepository  = QuestionRepo{}
	_ repository.EventRepository     = EventRepo{}
	_ repository.InstanceRepository  = InstanceRepo{}
)

package repository

import (
	"context"

	"github.com/beeemT/substrate/internal/domain"
)

// ErrNotFound is returned when an entity is not found.
var ErrNotFound = error(notFound{})

type notFound struct{}

func (notFound) Error() string { return "not found" }

// SessionFilter constrains session repository List results.
type SessionFilter struct {
	WorkspaceID   *string
	ExternalID    *string
	State         *domain.SessionState
	Source        *string
	Limit, Offset int
}

// SessionRepository provides CRUD for root sessions.
type SessionRepository interface {
	Get(ctx context.Context, id string) (domain.Session, error)
	List(ctx context.Context, filter SessionFilter) ([]domain.Session, error)
	Create(ctx context.Context, item domain.Session) error
	Update(ctx context.Context, item domain.Session) error
	Delete(ctx context.Context, id string) error
}

// PlanRepository provides CRUD for plans.
type PlanRepository interface {
	Get(ctx context.Context, id string) (domain.Plan, error)
	GetByWorkItemID(ctx context.Context, workItemID string) (domain.Plan, error)
	Create(ctx context.Context, plan domain.Plan) error
	Update(ctx context.Context, plan domain.Plan) error
	Delete(ctx context.Context, id string) error
	AppendFAQ(ctx context.Context, entry domain.FAQEntry) error
}

// TaskPlanRepository provides CRUD for task plans.
type TaskPlanRepository interface {
	Get(ctx context.Context, id string) (domain.TaskPlan, error)
	ListByPlanID(ctx context.Context, planID string) ([]domain.TaskPlan, error)
	Create(ctx context.Context, sp domain.TaskPlan) error
	Update(ctx context.Context, sp domain.TaskPlan) error
	Delete(ctx context.Context, id string) error
}

// WorkspaceRepository provides CRUD for workspaces.
type WorkspaceRepository interface {
	Get(ctx context.Context, id string) (domain.Workspace, error)
	Create(ctx context.Context, ws domain.Workspace) error
	Update(ctx context.Context, ws domain.Workspace) error
	Delete(ctx context.Context, id string) error
}

// TaskRepository provides CRUD for repo-scoped tasks.
type TaskRepository interface {
	Get(ctx context.Context, id string) (domain.Task, error)
	ListBySubPlanID(ctx context.Context, subPlanID string) ([]domain.Task, error)
	ListByWorkspaceID(ctx context.Context, workspaceID string) ([]domain.Task, error)
	ListByOwnerInstanceID(ctx context.Context, instanceID string) ([]domain.Task, error)
	SearchHistory(ctx context.Context, filter domain.SessionHistoryFilter) ([]domain.SessionHistoryEntry, error)
	Create(ctx context.Context, s domain.Task) error
	Update(ctx context.Context, s domain.Task) error
	Delete(ctx context.Context, id string) error
}

// ReviewRepository provides CRUD for review cycles and critiques.
type ReviewRepository interface {
	GetCycle(ctx context.Context, id string) (domain.ReviewCycle, error)
	ListCyclesBySessionID(ctx context.Context, sessionID string) ([]domain.ReviewCycle, error)
	CreateCycle(ctx context.Context, rc domain.ReviewCycle) error
	UpdateCycle(ctx context.Context, rc domain.ReviewCycle) error

	GetCritique(ctx context.Context, id string) (domain.Critique, error)
	ListCritiquesByReviewCycleID(ctx context.Context, cycleID string) ([]domain.Critique, error)
	CreateCritique(ctx context.Context, c domain.Critique) error
	UpdateCritique(ctx context.Context, c domain.Critique) error
}

// QuestionRepository provides CRUD for questions.
type QuestionRepository interface {
	Get(ctx context.Context, id string) (domain.Question, error)
	ListBySessionID(ctx context.Context, sessionID string) ([]domain.Question, error)
	Create(ctx context.Context, q domain.Question) error
	Update(ctx context.Context, q domain.Question) error
	UpdateProposedAnswer(ctx context.Context, id, proposedAnswer string) error
}

// EventRepository provides persistence for system events.
type EventRepository interface {
	Create(ctx context.Context, e domain.SystemEvent) error
	ListByType(ctx context.Context, eventType string, limit int) ([]domain.SystemEvent, error)
	ListByWorkspaceID(ctx context.Context, workspaceID string, limit int) ([]domain.SystemEvent, error)
}

// InstanceRepository provides CRUD for substrate instances.
type InstanceRepository interface {
	Get(ctx context.Context, id string) (domain.SubstrateInstance, error)
	ListByWorkspaceID(ctx context.Context, workspaceID string) ([]domain.SubstrateInstance, error)
	Create(ctx context.Context, inst domain.SubstrateInstance) error
	Update(ctx context.Context, inst domain.SubstrateInstance) error
	Delete(ctx context.Context, id string) error
}

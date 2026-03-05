package repository

import (
	"context"

	"github.com/beeemT/substrate/internal/domain"
)

// ErrNotFound is returned when an entity is not found.
var ErrNotFound = error(notFound{})

type notFound struct{}

func (notFound) Error() string { return "not found" }

// WorkItemFilter constrains WorkItemRepository.List results.
type WorkItemFilter struct {
	WorkspaceID   *string
	State         *domain.WorkItemState
	Source        *string
	Limit, Offset int
}

// WorkItemRepository provides CRUD for work items.
type WorkItemRepository interface {
	Get(ctx context.Context, id string) (domain.WorkItem, error)
	List(ctx context.Context, filter WorkItemFilter) ([]domain.WorkItem, error)
	Create(ctx context.Context, item domain.WorkItem) error
	Update(ctx context.Context, item domain.WorkItem) error
	Delete(ctx context.Context, id string) error
}

// PlanRepository provides CRUD for plans.
type PlanRepository interface {
	Get(ctx context.Context, id string) (domain.Plan, error)
	GetByWorkItemID(ctx context.Context, workItemID string) (domain.Plan, error)
	Create(ctx context.Context, plan domain.Plan) error
	Update(ctx context.Context, plan domain.Plan) error
	Delete(ctx context.Context, id string) error
	// AppendFAQ adds a new FAQ entry to the plan's FAQ list.
	AppendFAQ(ctx context.Context, entry domain.FAQEntry) error
}

// SubPlanRepository provides CRUD for sub-plans.
type SubPlanRepository interface {
	Get(ctx context.Context, id string) (domain.SubPlan, error)
	ListByPlanID(ctx context.Context, planID string) ([]domain.SubPlan, error)
	Create(ctx context.Context, sp domain.SubPlan) error
	Update(ctx context.Context, sp domain.SubPlan) error
	Delete(ctx context.Context, id string) error
}

// WorkspaceRepository provides CRUD for workspaces.
type WorkspaceRepository interface {
	Get(ctx context.Context, id string) (domain.Workspace, error)
	Create(ctx context.Context, ws domain.Workspace) error
	Update(ctx context.Context, ws domain.Workspace) error
	Delete(ctx context.Context, id string) error
}

// SessionRepository provides CRUD for agent sessions.
type SessionRepository interface {
	Get(ctx context.Context, id string) (domain.AgentSession, error)
	ListBySubPlanID(ctx context.Context, subPlanID string) ([]domain.AgentSession, error)
	ListByWorkspaceID(ctx context.Context, workspaceID string) ([]domain.AgentSession, error)
	ListByOwnerInstanceID(ctx context.Context, instanceID string) ([]domain.AgentSession, error)
	Create(ctx context.Context, s domain.AgentSession) error
	Update(ctx context.Context, s domain.AgentSession) error
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

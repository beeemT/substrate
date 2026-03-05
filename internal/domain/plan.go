package domain

import "time"

// Plan is the orchestration plan for a work item.
type Plan struct {
	ID               string
	WorkItemID       string
	Status           PlanStatus
	OrchestratorPlan string
	Version          int
	FAQ              []FAQEntry
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// FAQEntry represents a question-answer pair from the foreman.
type FAQEntry struct {
	ID             string
	PlanID         string
	AgentSessionID string
	RepoName       string
	Question       string
	Answer         string
	AnsweredBy     string
	CreatedAt      time.Time
}

// PlanStatus represents the lifecycle state of a plan.
type PlanStatus string

const (
	PlanDraft         PlanStatus = "draft"
	PlanPendingReview PlanStatus = "pending_review"
	PlanApproved      PlanStatus = "approved"
	PlanRejected      PlanStatus = "rejected"
)

// SubPlan is a single repository's portion of the plan.
type SubPlan struct {
	ID             string
	PlanID         string
	RepositoryName string
	Content        string
	Order          int
	Status         SubPlanStatus
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// SubPlanStatus represents the lifecycle state of a sub-plan.
type SubPlanStatus string

const (
	SubPlanPending    SubPlanStatus = "pending"
	SubPlanInProgress SubPlanStatus = "in_progress"
	SubPlanCompleted  SubPlanStatus = "completed"
	SubPlanFailed     SubPlanStatus = "failed"
)

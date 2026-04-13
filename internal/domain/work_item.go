package domain

import "time"

// Session is the root aggregate representing an external ticket.
type Session struct {
	ID            string
	WorkspaceID   string
	ExternalID    string
	Source        string
	Title         string
	Description   string
	Labels        []string
	AssigneeID    string
	State         SessionState
	Metadata      map[string]any
	ExtraContext  string
	SourceScope   SelectionScope
	SourceItemIDs []string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// SessionState represents the lifecycle state of a work item.
type SessionState string

const (
	SessionIngested     SessionState = "ingested"
	SessionPlanning     SessionState = "planning"
	SessionPlanReview   SessionState = "plan_review"
	SessionApproved     SessionState = "approved"
	SessionImplementing SessionState = "implementing"
	SessionReviewing    SessionState = "reviewing"
	SessionCompleted    SessionState = "completed"
	SessionFailed       SessionState = "failed"
)

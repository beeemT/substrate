package domain

import "time"

// WorkItem is the root aggregate representing an external ticket.
type WorkItem struct {
	ID            string
	WorkspaceID   string
	ExternalID    string
	Source        string
	Title         string
	Description   string
	Labels        []string
	AssigneeID    string
	State         WorkItemState
	Metadata      map[string]any
	SourceScope   SelectionScope
	SourceItemIDs []string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// WorkItemState represents the lifecycle state of a work item.
type WorkItemState string

const (
	WorkItemIngested     WorkItemState = "ingested"
	WorkItemPlanning     WorkItemState = "planning"
	WorkItemPlanReview   WorkItemState = "plan_review"
	WorkItemApproved     WorkItemState = "approved"
	WorkItemImplementing WorkItemState = "implementing"
	WorkItemReviewing    WorkItemState = "reviewing"
	WorkItemCompleted    WorkItemState = "completed"
	WorkItemFailed       WorkItemState = "failed"
)

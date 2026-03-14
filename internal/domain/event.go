package domain

import "time"

// EventType is a string type for event type constants.
type EventType string

// SystemEvent is a persisted system event for audit and replay.
type SystemEvent struct {
	ID          string
	EventType   string
	WorkspaceID string
	Payload     string
	CreatedAt   time.Time
}

// Event type constants for system events.
// These are used for routing and persistence.
//
// Pre-hook events run hooks BEFORE persistence. If any hook rejects,
// the event is not persisted and the operation should be aborted.
const (
	// WorktreeCreating is a pre-hook event emitted before git-work checkout.
	// Adapters can abort by returning an error.
	EventWorktreeCreating EventType = "worktree.creating"

	// WorktreeCreated is a post-hook event emitted after git-work checkout succeeds.
	EventWorktreeCreated EventType = "worktree.created"
)

// Regular events - persisted first, then dispatched to subscribers
const (
	// Work item lifecycle events
	EventWorkItemIngested     EventType = "work_item.ingested"
	EventWorkItemPlanning     EventType = "work_item.planning"
	EventWorkItemPlanReview   EventType = "work_item.plan_review"
	EventWorkItemApproved     EventType = "work_item.approved"
	EventWorkItemImplementing EventType = "work_item.implementing"
	EventWorkItemReviewing    EventType = "work_item.reviewing"
	EventWorkItemCompleted    EventType = "work_item.completed"
	EventWorkItemFailed       EventType = "work_item.failed"

	// Workspace events
	EventWorkspaceCreated EventType = "workspace.created"

	// Plan events
	EventPlanGenerated          EventType = "plan.generated"
	EventPlanSubmittedForReview EventType = "plan.submitted_for_review"
	EventPlanApproved           EventType = "plan.approved"
	EventPlanRejected           EventType = "plan.rejected"
	EventPlanRevised            EventType = "plan.revised"
	EventPlanFailed             EventType = "plan.failed"

	// Implementation events
	EventImplementationStarted EventType = "work_item.implementation_started"

	// Worktree events
	EventWorktreeRemoved EventType = "worktree.removed"

	// Agent session events
	EventAgentSessionStarted     EventType = "agent_session.started"
	EventAgentSessionCompleted   EventType = "agent_session.completed"
	EventAgentSessionFailed      EventType = "agent_session.failed"
	EventAgentSessionInterrupted EventType = "agent_session.interrupted"
	EventAgentSessionResumed     EventType = "agent_session.resumed"

	// Question events
	EventAgentQuestionRaised   EventType = "agent_question.raised"
	EventAgentQuestionAnswered EventType = "agent_question.answered"

	// Review events
	EventReviewStarted           EventType = "review.started"
	EventReviewCompleted         EventType = "review.completed"
	EventCritiquesFound          EventType = "review.critiques_found"
	EventReimplementationStarted EventType = "reimplementation.started"
	EventReviewArtifactRecorded  EventType = "review.artifact_recorded"
)

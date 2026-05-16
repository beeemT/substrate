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
// Hooks are run synchronously before event dispatch but AFTER persistence.
// For pre-creation validation (e.g., blocking worktree creation), use
// the worktree.HookRegistry in the orchestrator instead.
const (
	// WorktreeCreating is emitted before git-work checkout.
	// For validation that can abort creation, use worktree.HookRegistry.
	EventWorktreeCreating EventType = "worktree.creating"

	// WorktreeCreated is emitted after git-work checkout succeeds.
	EventWorktreeCreated EventType = "worktree.created"

	// WorktreeReused is emitted when an existing worktree is reused
	// during differential re-implementation.
	EventWorktreeReused EventType = "worktree.reused"
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
	EventWorkItemMerged       EventType = "work_item.merged"
	EventWorkItemArchived     EventType = "work_item.archived"

	// Workspace events
	EventWorkspaceCreated       EventType = "workspace.created"
	EventWorkspaceStatusChanged EventType = "workspace.status_changed"

	// Plan events
	EventPlanGenerated     EventType = "plan.generated"
	EventPlanSubmitted     EventType = "plan.submitted"
	EventPlanApproved      EventType = "plan.approved"
	EventPlanRejected      EventType = "plan.rejected"
	EventPlanRevised       EventType = "plan.revised"
	EventPlanFailed        EventType = "plan.failed"
	EventPlanSuperseded    EventType = "plan.superseded"
	EventPlanStatusChanged EventType = "plan.status_changed"

	// Sub-plan state events
	EventSubPlanStarted   EventType = "subplan.started"
	EventSubPlanCompleted EventType = "subplan.completed"
	EventSubPlanFailed    EventType = "subplan.failed"

	// Repo lifecycle event consumed by GitHub/glab lifecycle adapters.
	EventSubPlanPRReady EventType = "subplan.pr_ready"

	// Deprecated: use EventSubPlanStarted, EventSubPlanCompleted, or EventSubPlanFailed
	EventSubPlanStatusChanged EventType = "subplan.status_changed"

	// Worktree events
	EventWorktreeRemoved EventType = "worktree.removed"

	// Agent task events (lifecycle of individual agent sessions within a work item)
	EventAgentSessionStarted     EventType = "agent_session.started"
	EventAgentSessionCompleted   EventType = "agent_session.completed"
	EventAgentSessionFailed      EventType = "agent_session.failed"
	EventAgentSessionInterrupted EventType = "agent_session.interrupted"

	// Agent session events (resumption lifecycle)
	EventAgentSessionResumed          EventType = "agent_session.resumed"
	EventAgentSessionFollowUp         EventType = "agent_session.follow_up"
	EventAgentSessionWaitingForAnswer EventType = "agent_session.waiting_for_answer"

	// Question events
	EventAgentQuestionRaised   EventType = "agent_question.raised"
	EventAgentQuestionAnswered EventType = "agent_question.answered"
	EventQuestionStatusChanged EventType = "question.status_changed"

	// Review events
	EventReviewStarted            EventType = "review.started"
	EventReviewCompleted          EventType = "review.completed"
	EventCritiquesFound           EventType = "review.critiques_found"
	EventReimplementationStarted  EventType = "reimplementation.started"
	EventReviewArtifactRecorded   EventType = "review.artifact_recorded"
	EventReviewCycleStatusChanged EventType = "review_cycle.status_changed"
	EventCritiqueStatusChanged    EventType = "critique.status_changed"

	EventPRReviewStateChanged EventType = "pr.review_state_changed"
	EventPRCIFailed           EventType = "pr.ci_failed"
	EventPRMerged             EventType = "pr.merged"

	// Adapter error events
	EventAdapterError EventType = "adapter.error"
)

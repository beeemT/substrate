package domain

// TrackerState represents the state of a work item in an external tracker.
// This is distinct from WorkItemState which is Substrate's internal state machine.
// Adapters map TrackerState to their native states (e.g., Linear workflow states).
type TrackerState string

const (
	TrackerStateTodo       TrackerState = "todo"
	TrackerStateInProgress TrackerState = "in_progress"
	TrackerStateInReview   TrackerState = "in_review"
	TrackerStateDone       TrackerState = "done"
)

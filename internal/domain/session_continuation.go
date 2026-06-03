package domain

import "time"

// AgentSessionContinuationStatus records progress for work that must happen
// after an agent session reaches a terminal harness state.
type AgentSessionContinuationStatus string

const (
	AgentSessionContinuationPending    AgentSessionContinuationStatus = "pending"
	AgentSessionContinuationRunning    AgentSessionContinuationStatus = "running"
	AgentSessionContinuationCompleted  AgentSessionContinuationStatus = "completed"
	AgentSessionContinuationFailed     AgentSessionContinuationStatus = "failed"
	AgentSessionContinuationSkipped    AgentSessionContinuationStatus = "skipped"
	AgentSessionContinuationSuperseded AgentSessionContinuationStatus = "superseded"
)

// AgentSessionContinuation is durable state for kind-specific continuation work
// that follows a completed agent session, such as implementation review and
// work-item aggregation.
type AgentSessionContinuation struct {
	ID             string
	AgentSessionID string
	WorkItemID     string
	SubPlanID      string
	Kind           string
	Status         AgentSessionContinuationStatus
	Attempt        int
	LastError      string
	StartedAt      *time.Time
	CompletedAt    *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func IsTerminalAgentSessionContinuationStatus(status AgentSessionContinuationStatus) bool {
	switch status {
	case AgentSessionContinuationCompleted, AgentSessionContinuationSkipped, AgentSessionContinuationSuperseded:
		return true
	default:
		return false
	}
}

func CanTransitionAgentSessionContinuation(from, to AgentSessionContinuationStatus) bool {
	if from == to {
		return true
	}
	switch from {
	case AgentSessionContinuationPending:
		return to == AgentSessionContinuationRunning || to == AgentSessionContinuationSkipped || to == AgentSessionContinuationSuperseded
	case AgentSessionContinuationRunning:
		return to == AgentSessionContinuationCompleted || to == AgentSessionContinuationFailed || to == AgentSessionContinuationSkipped || to == AgentSessionContinuationSuperseded
	case AgentSessionContinuationFailed:
		return to == AgentSessionContinuationRunning || to == AgentSessionContinuationSuperseded
	default:
		return false
	}
}

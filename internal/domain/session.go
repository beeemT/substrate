package domain

import "time"

// AgentSession is a single agent harness invocation against one sub-plan.
type AgentSession struct {
	ID              string
	WorkspaceID     string
	SubPlanID       string
	RepositoryName  string
	WorktreePath    string
	HarnessName     string
	Status          AgentSessionStatus
	PID             *int
	StartedAt       *time.Time
	CompletedAt     *time.Time
	ShutdownAt      *time.Time
	ExitCode        *int
	OwnerInstanceID *string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// SessionHistoryEntry is one searchable session-history result enriched with workspace and work-item metadata.
type SessionHistoryEntry struct {
	SessionID          string
	WorkspaceID        string
	WorkspaceName      string
	WorkItemID         string
	WorkItemExternalID string
	WorkItemTitle      string
	WorkItemState      WorkItemState
	RepositoryName     string
	HarnessName        string
	Status             AgentSessionStatus
	CreatedAt          time.Time
	UpdatedAt          time.Time
	CompletedAt        *time.Time
}

// SessionHistoryFilter constrains session-history search results.
type SessionHistoryFilter struct {
	WorkspaceID *string
	Search      string
	Limit       int
	Offset      int
}

// AgentSessionStatus represents the lifecycle state of an agent session.
type AgentSessionStatus string

const (
	AgentSessionPending          AgentSessionStatus = "pending"
	AgentSessionRunning          AgentSessionStatus = "running"
	AgentSessionWaitingForAnswer AgentSessionStatus = "waiting_for_answer"
	AgentSessionCompleted        AgentSessionStatus = "completed"
	AgentSessionInterrupted      AgentSessionStatus = "interrupted"
	AgentSessionFailed           AgentSessionStatus = "failed"
)

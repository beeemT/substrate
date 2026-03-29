package domain

import "time"

// TaskPhase identifies the kind of child agent session being tracked.
type TaskPhase string

const (
	TaskPhasePlanning       TaskPhase = "planning"
	TaskPhaseImplementation TaskPhase = "implementation"
	TaskPhaseReview         TaskPhase = "review"
)

// Task is a single child agent session for a work item.
type Task struct {
	ID              string
	WorkItemID      string
	WorkspaceID     string
	Phase           TaskPhase
	SubPlanID       string
	RepositoryName  string
	WorktreePath    string
	HarnessName     string
	Status          TaskStatus
	PID             *int
	StartedAt       *time.Time
	CompletedAt     *time.Time
	ShutdownAt      *time.Time
	ExitCode        *int
	OwnerInstanceID *string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	ResumeInfo      map[string]string
}

// SessionHistoryEntry is one searchable root-session result.
//
// The work item is the primary session identity shown in session history. When the
// work item has child tasks, SessionID/RepositoryName/HarnessName/Status describe
// the latest contributing task for preview purposes.
type SessionHistoryEntry struct {
	SessionID          string
	WorkspaceID        string
	WorkspaceName      string
	WorkItemID         string
	WorkItemExternalID string
	WorkItemTitle      string
	WorkItemState      SessionState
	RepositoryName     string
	HarnessName        string
	Status             TaskStatus
	AgentSessionCount  int
	HasOpenQuestion    bool
	HasInterrupted     bool
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

// TaskStatus represents the lifecycle state of an agent session.
type TaskStatus string

const (
	AgentSessionPending          TaskStatus = "pending"
	AgentSessionRunning          TaskStatus = "running"
	AgentSessionWaitingForAnswer TaskStatus = "waiting_for_answer"
	AgentSessionCompleted        TaskStatus = "completed"
	AgentSessionInterrupted      TaskStatus = "interrupted"
	AgentSessionFailed           TaskStatus = "failed"
)

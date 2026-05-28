package domain

import "time"

// AgentSessionKind identifies the kind of child agent session being tracked.
type AgentSessionKind string

const (
	AgentSessionKindPlanning       AgentSessionKind = "planning"
	AgentSessionKindImplementation AgentSessionKind = "implementation"
	AgentSessionKindReview         AgentSessionKind = "review"
	AgentSessionKindManual         AgentSessionKind = "manual"
	AgentSessionKindForeman        AgentSessionKind = "foreman"
)

// AgentSession is a single child agent session for a work item.
type AgentSession struct {
	ID              string
	WorkItemID      string
	WorkspaceID     string
	Kind            AgentSessionKind
	SubPlanID       string
	PlanID          string // Plan produced by this planning session (empty for non-planning sessions).
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
	ResumeInfo      map[string]string
	// ParentAgentSessionID links this session to its parent in the agent-session
	// graph (parent -> child). Populated for retries, resumes, follow-ups, reviews
	// of implementations, and reimplementations after review critique. Empty for
	// the first session in a chain. A leaf session is one with no children.
	ParentAgentSessionID string
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
	Status             AgentSessionStatus
	AgentSessionCount  int
	HasOpenQuestion    bool
	HasInterrupted     bool
	CreatedAt          time.Time
	UpdatedAt          time.Time
	CompletedAt        *time.Time
	PreviousState      SessionState
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

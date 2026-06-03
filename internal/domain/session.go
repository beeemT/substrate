package domain

import "time"

// LeafAgentSessions returns the leaf nodes of the agent-session graph defined
// by AgentSession.ParentAgentSessionID. A leaf is a session that no other
// non-manual session in the input slice points to as its parent.
//
// Manual sessions are excluded entirely. They are user-driven side
// conversations outside the orchestrator's implementation/review chain and must
// not influence work-item-level retry, question, or interruption projections.
func LeafAgentSessions(sessions []AgentSession) []AgentSession {
	if len(sessions) == 0 {
		return nil
	}

	filtered := make([]AgentSession, 0, len(sessions))
	for i := range sessions {
		if sessions[i].Kind == AgentSessionKindManual {
			continue
		}
		filtered = append(filtered, sessions[i])
	}
	if len(filtered) == 0 {
		return nil
	}

	hasChild := make(map[string]bool, len(filtered))
	for i := range filtered {
		if pid := filtered[i].ParentAgentSessionID; pid != "" {
			hasChild[pid] = true
		}
	}

	type groupKey struct {
		kind           AgentSessionKind
		subPlanID      string
		repositoryName string
	}
	type groupInfo struct {
		leaves  []AgentSession
		anyEdge bool
	}

	groups := make(map[groupKey]*groupInfo)
	for i := range filtered {
		s := filtered[i]
		k := groupKey{kind: s.Kind, subPlanID: s.SubPlanID, repositoryName: s.RepositoryName}
		g := groups[k]
		if g == nil {
			g = &groupInfo{}
			groups[k] = g
		}
		if s.ParentAgentSessionID != "" || hasChild[s.ID] {
			g.anyEdge = true
		}
		if hasChild[s.ID] {
			continue
		}
		g.leaves = append(g.leaves, s)
	}

	leaves := make([]AgentSession, 0, len(filtered))
	for _, g := range groups {
		if len(g.leaves) == 0 {
			continue
		}
		if g.anyEdge {
			leaves = append(leaves, g.leaves...)
			continue
		}
		latest := g.leaves[0]
		for i := 1; i < len(g.leaves); i++ {
			if leafAgentSessionIsNewer(g.leaves[i], latest) {
				latest = g.leaves[i]
			}
		}
		leaves = append(leaves, latest)
	}
	return leaves
}

// FindLeafAgentSessionByID returns the current graph leaf with id, if id is a
// leaf in sessions according to LeafAgentSessions.
func FindLeafAgentSessionByID(sessions []AgentSession, id string) (AgentSession, bool) {
	for _, leaf := range LeafAgentSessions(sessions) {
		if leaf.ID == id {
			return leaf, true
		}
	}
	return AgentSession{}, false
}

// IsLeafAgentSessionID reports whether id is a current agent-session graph leaf.
func IsLeafAgentSessionID(sessions []AgentSession, id string) bool {
	_, ok := FindLeafAgentSessionByID(sessions, id)
	return ok
}

// RetryableAgentSessionLeaves returns current non-manual failed leaves.
func RetryableAgentSessionLeaves(sessions []AgentSession) []AgentSession {
	return agentSessionLeavesWithStatus(sessions, AgentSessionFailed)
}

// ResumableAgentSessionLeaves returns current non-manual interrupted leaves.
func ResumableAgentSessionLeaves(sessions []AgentSession) []AgentSession {
	return agentSessionLeavesWithStatus(sessions, AgentSessionInterrupted)
}

func agentSessionLeavesWithStatus(sessions []AgentSession, status AgentSessionStatus) []AgentSession {
	leaves := LeafAgentSessions(sessions)
	if len(leaves) == 0 {
		return nil
	}
	eligible := make([]AgentSession, 0, len(leaves))
	for i := range leaves {
		if leaves[i].Status == status {
			eligible = append(eligible, leaves[i])
		}
	}
	return eligible
}

func leafAgentSessionIsNewer(a, b AgentSession) bool {
	if !a.CreatedAt.Equal(b.CreatedAt) {
		return a.CreatedAt.After(b.CreatedAt)
	}
	if !a.UpdatedAt.Equal(b.UpdatedAt) {
		return a.UpdatedAt.After(b.UpdatedAt)
	}
	return a.ID > b.ID
}

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

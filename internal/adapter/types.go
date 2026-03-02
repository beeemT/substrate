package adapter

import (
	"time"

	"github.com/beeemT/substrate/internal/domain"
)

// AdapterCapabilities describes what an adapter can do.
type AdapterCapabilities struct {
	CanWatch     bool                    // Supports Watch for reactive auto-assignment
	CanBrowse    bool                    // Supports ListSelectable/Resolve for interactive selection
	CanMutate    bool                    // Supports UpdateState/AddComment
	BrowseScopes []domain.SelectionScope // Available scopes for browsing
}

// ListOpts controls ListSelectable behavior.
type ListOpts struct {
	WorkspaceID string
	Scope       domain.SelectionScope
	TeamID      string // Optional: filter by team (for Linear)
	Search      string // Optional: fuzzy search query
	Limit       int    // Optional: max results (0 = default)
	Offset      int    // Optional: pagination offset
}

// ListResult contains paginated results from ListSelectable.
type ListResult struct {
	Items      []ListItem
	TotalCount int
	HasMore    bool
}

// ListItem represents a single selectable item in browse results.
type ListItem struct {
	ID          string
	Title       string
	Description string
	State       string
	Labels      []string
	ParentRef   *ParentRef // Optional: parent project/initiative reference
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// ParentRef references a parent entity (project or initiative).
type ParentRef struct {
	ID    string
	Type  string // "project" or "initiative"
	Title string
}

// Selection represents a user's selection from ListSelectable results.
type Selection struct {
	Scope    domain.SelectionScope
	ItemIDs  []string       // For issues/projects: one or more selected IDs
	Manual   *ManualInput   // For manual scope: user-provided input
	Metadata map[string]any // Scope-specific metadata
}

// ManualInput contains user-provided work item data for manual adapter.
type ManualInput struct {
	Title       string
	Description string
}

// WorkItemFilter constrains Watch event filtering.
type WorkItemFilter struct {
	WorkspaceID string
	TeamID      string
	States      []string // Filter by external tracker states
	Labels      []string // Filter by labels
}

// WorkItemEvent represents a change detected by Watch.
type WorkItemEvent struct {
	Type      string // "created", "updated", "deleted"
	WorkItem  domain.WorkItem
	Timestamp time.Time
}

// SessionMode determines the behavior of an agent session.
type SessionMode string

const (
	// SessionModeAgent is a coding sub-agent with full tool set.
	SessionModeAgent SessionMode = "agent"
	// SessionModeForeman is a question-answering session with read-only tools.
	SessionModeForeman SessionMode = "foreman"
)

// SessionOpts configures a new agent session.
type SessionOpts struct {
	SessionID            string      // Substrate-generated ULID; used for DB record and session directory
	Mode                 SessionMode // Agent or Foreman; defaults to Agent
	WorkspaceID          string
	SubPlanID            string
	Repository           string
	WorktreePath         string // Empty for foreman sessions (uses workspace root)
	DraftPath            string // Absolute path to plan-draft.md; set for planning sessions
	CrossRepoPlan        string // Full cross-repo orchestration plan
	DocumentationContext string // Concatenated documentation for the session
	SystemPrompt         string
	UserPrompt           string
	SessionLogDir        string // Directory for session output logs
	CommitConfig         CommitConfig
	AllowPush            bool // Whether agent is allowed to push to remote
}

// CommitConfig contains commit strategy settings.
type CommitConfig struct {
	Strategy        string // "granular", "semi-regular", "single"
	MessageFormat   string // "ai-generated", "conventional", "custom"
	MessageTemplate string // Required when MessageFormat = "custom"
}

// AgentEvent represents an event from a running agent session.
type AgentEvent struct {
	Type      string // "started", "text_delta", "tool_start", "tool_end", "done", "error", "question"
	Timestamp time.Time
	Payload   string // JSON or text payload depending on type
	Metadata  map[string]any
}

// HarnessCapabilities describes what an agent harness supports.
type HarnessCapabilities struct {
	SupportsStreaming bool     // Supports real-time event streaming
	SupportsMessaging bool     // Supports SendMessage for iteration
	SupportedTools    []string // List of supported tool names
}

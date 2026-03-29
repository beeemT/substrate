package adapter

import (
	"time"

	"github.com/beeemT/substrate/internal/domain"
)

// BrowseFilterCapabilities describes which shared browse filters a provider/scope can honor.
type BrowseFilterCapabilities struct {
	Views          []string
	States         []string
	SupportsLabels bool
	SupportsSearch bool
	SupportsCursor bool
	SupportsOffset bool
	SupportsOwner  bool
	SupportsRepo   bool
	SupportsGroup  bool
	SupportsTeam   bool
}

// AdapterCapabilities describes what an adapter can do.
type AdapterCapabilities struct {
	CanWatch      bool                                               // Supports Watch for reactive auto-assignment
	CanBrowse     bool                                               // Supports ListSelectable/Resolve for interactive selection
	CanMutate     bool                                               // Supports UpdateState/AddComment
	BrowseScopes  []domain.SelectionScope                            // Available scopes for browsing
	BrowseFilters map[domain.SelectionScope]BrowseFilterCapabilities // Available filters by scope
}

// ListOpts controls ListSelectable behavior.
type ListOpts struct {
	WorkspaceID string
	Provider    string
	Scope       domain.SelectionScope
	TeamID      string // Optional: filter by team (for Linear)
	Search      string // Optional: server-side search query
	Limit       int    // Optional: max results (0 = default)
	Offset      int    // Optional: pagination offset
	View        string // Optional: assigned_to_me, created_by_me, mentioned, subscribed, all
	State       string // Optional: provider-native state filter
	Owner       string // Optional: GitHub owner filter
	Repo        string // Optional: GitHub repo or GitLab project-path filter
	Group       string // Optional: GitLab group filter
	Labels      []string
	Metadata    map[string]any
	Cursor      string
	HasMoreHint bool
	Sort        string
	Direction   string
}

// ListResult contains paginated results from ListSelectable.
type ListResult struct {
	Items      []ListItem
	TotalCount int
	HasMore    bool
	NextCursor string
}

// ListItem represents a single selectable item in browse results.
type ListItem struct {
	ID           string
	Title        string
	Description  string
	State        string
	Labels       []string
	Provider     string
	Identifier   string
	ContainerRef string
	URL          string
	Metadata     map[string]any
	ParentRef    *ParentRef // Optional: parent project/initiative reference
	CreatedAt    time.Time
	UpdatedAt    time.Time
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
	WorkItem  domain.Session
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
	AllowPush            bool              // Whether agent is allowed to push to remote
	ResumeFromSessionID  string            // Substrate session ID; harness resumes if it can
	ResumeInfo           map[string]string // Resolved resume data; harness reads its own keys
	// AnswerTimeoutMs controls how long the bridge waits for a foreman answer before
	// falling back to a placeholder. 0 means no timeout (wait indefinitely).
	AnswerTimeoutMs int64
}

// CommitConfig contains commit strategy settings.
type CommitConfig struct {
	Strategy        string // "granular", "semi-regular", "single"
	MessageFormat   string // "ai-generated", "conventional", "custom"
	MessageTemplate string // Required when MessageFormat = "custom"
}

// AgentEvent represents an event from a running agent session.
type AgentEvent struct {
	Type string // e.g. started, input, text_delta, tool_start, tool_output,
	// tool_result, done, error, question, foreman_proposed, retry_wait, retry_resumed, retry_exhausted
	Timestamp time.Time
	Payload   string // text payload for the event type
	Metadata  map[string]any
}

// HarnessCapabilities describes what an agent harness supports.
type HarnessCapabilities struct {
	SupportsStreaming    bool     // Supports real-time event streaming
	SupportsMessaging    bool     // Supports SendMessage for iteration
	SupportsNativeResume bool     // Supports resuming completed sessions natively
	SupportedTools       []string // List of supported tool names
}

// HarnessActionRequest describes a short-lived control-plane action executed by a harness.
type HarnessActionRequest struct {
	Action      string            // e.g. "login_provider", "check_auth"
	Provider    string            // github, gitlab, linear, etc.
	HarnessName string            // selected harness name
	Inputs      map[string]string // optional scoped inputs / env-like values
}

// HarnessActionResult is the structured result of a harness-side action.
type HarnessActionResult struct {
	Success      bool
	Message      string
	Identity     string
	Credentials  map[string]string // redacted only in UI; caller decides persistence
	Metadata     map[string]string
	NeedsConfirm bool
}

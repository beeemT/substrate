package views

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/gitwork"
	"github.com/beeemT/substrate/internal/sessionlog"
)

// --- DB polling / data loading ---

// SessionsLoadedMsg is sent when the session list is refreshed.
type SessionsLoadedMsg struct {
	WorkspaceID string
	Items       []domain.Session
}

// TasksLoadedMsg is sent when tasks for a workspace are refreshed.
type TasksLoadedMsg struct {
	WorkspaceID string
	Sessions    []domain.Task
}

// SessionHistoryLoadedMsg is sent when a session-history search completes.
type SessionHistoryLoadedMsg struct {
	Filter  domain.SessionHistoryFilter
	Entries []domain.SessionHistoryEntry
}

// PlanLoadedMsg is sent when a plan is loaded.
type PlanLoadedMsg struct {
	WorkItemID string
	Plan       *domain.Plan // nil if not found
	SubPlans   []domain.TaskPlan
}

// QuestionsLoadedMsg is sent when questions for a session are loaded.
type QuestionsLoadedMsg struct {
	SessionID string
	Questions []domain.Question
}

// ReviewsLoadedMsg is sent when review cycles for a session are loaded.
type ReviewsLoadedMsg struct {
	SessionID string
	Cycles    []domain.ReviewCycle
	Critiques map[string][]domain.Critique // keyed by ReviewCycleID
}

// PollTickMsg triggers DB state refresh every 2s.
type PollTickMsg time.Time

// HeartbeatTickMsg triggers instance heartbeat update every 5s.
type HeartbeatTickMsg time.Time

// --- Session log tailing ---

// SessionLogLinesMsg delivers new log lines from a tailed session log.
type SessionLogLinesMsg struct {
	SessionID  string
	Entries    []sessionlog.Entry
	NextOffset int64
}

// SessionInteractionLoadedMsg delivers the parsed interaction log for one session.
type SessionInteractionLoadedMsg struct {
	SessionID string
	Entries   []sessionlog.Entry
}

// --- User actions ---

// SelectSessionMsg selects a session in the sidebar.
type SelectSessionMsg struct{ WorkItemID string }

// PlanApproveMsg fires when the user presses [a] in plan review.
type PlanApproveMsg struct {
	PlanID     string
	WorkItemID string
}

// PlanApprovedMsg is sent after ApprovePlanCmd succeeds.
// It signals the plan is persisted as approved, so RunImplementationCmd and
// StartForemanCmd are only dispatched after the DB write commits — not concurrently.
type PlanApprovedMsg struct {
	PlanID     string
	WorkItemID string
}

// PlanRequestChangesMsg fires when user submits feedback with [c].
type PlanRequestChangesMsg struct {
	PlanID   string
	Feedback string
}

// AnswerQuestionMsg fires when the human approves a foreman answer.
type AnswerQuestionMsg struct {
	QuestionID string
	Answer     string
	AnsweredBy string // "human" or "foreman"
}

// SendToForemanMsg fires when the human sends a message to foreman (iterating on answer).
type SendToForemanMsg struct {
	QuestionID string
	Message    string
}

// SkipQuestionMsg fires when the human presses Esc to skip a question.
type SkipQuestionMsg struct{ QuestionID string }

// ResumeSessionMsg fires when the user presses [r] on interrupted.
type ResumeSessionMsg struct {
	SubPlanID    string
	OldSessionID string
}

// RestartPlanMsg fires when the user presses [r] on an interrupted planning task.
type RestartPlanMsg struct{ WorkItemID string }

// SessionResumedMsg is returned by ResumeSessionCmd after the interrupted session
// has been replaced by a new running session.
type SessionResumedMsg struct{ Message string }

// PlanningRestartedMsg is returned by RestartPlanningCmd after the planning
// pipeline has been re-launched from scratch.
type PlanningRestartedMsg struct{ Message string }

// QuitRequestMsg fires when an OS signal (SIGTERM) requests a graceful quit.
type QuitRequestMsg struct{}

// QuitConfirmedMsg fires when the user confirms the quit dialog. The handler
// tears down all running pipelines and agent sessions before exiting.
type QuitConfirmedMsg struct{}

// AbandonSessionMsg fires when the user confirms abandonment.
type AbandonSessionMsg struct{ SessionID string }

// WorkspaceInitMsg fires when user confirms workspace initialization.
type WorkspaceInitMsg struct{ Dir string }

// WorkspaceCancelMsg fires when user cancels workspace initialization (triggers exit).
type WorkspaceCancelMsg struct{}

// ReimplementMsg fires when user triggers re-implementation from review.
type ReimplementMsg struct{ WorkItemID string }

// RetryFailedMsg fires when user retries a failed work item from the overview.
type RetryFailedMsg struct{ WorkItemID string }

// StartPlanMsg fires when the user presses Enter on a work item in the ready-to-plan state.
type StartPlanMsg struct{ WorkItemID string }

// OverrideAcceptMsg fires when user overrides and accepts critiques.
type OverrideAcceptMsg struct{ WorkItemID string }

// NewSessionBrowseMsg fires when user selects browsed items and starts a session.
type NewSessionBrowseMsg struct {
	Adapter      adapter.WorkItemAdapter
	Selection    adapter.Selection
	ExtraContext string
}

// NewSessionManualMsg fires when user submits the manual session form.
type NewSessionManualMsg struct {
	Adapter adapter.WorkItemAdapter
	Title   string
	Desc    string
}

// LoadNewSessionFiltersMsg requests loading saved New Session Filters for the active workspace.
type LoadNewSessionFiltersMsg struct{ WorkspaceID string }

// NewSessionFiltersLoadedMsg delivers saved New Session Filters for the workspace.
type NewSessionFiltersLoadedMsg struct {
	WorkspaceID string
	Filters     []domain.NewSessionFilter
}

// SaveNewSessionFilterMsg requests persisting the current New Session Filter criteria.
type SaveNewSessionFilterMsg struct {
	WorkspaceID string
	Provider    string
	Name        string
	Criteria    domain.NewSessionFilterCriteria
}

// NewSessionFilterSavedMsg acknowledges a persisted New Session Filter.
type NewSessionFilterSavedMsg struct {
	Filter  domain.NewSessionFilter
	Message string
}

// DeleteNewSessionFilterMsg requests deleting a saved New Session Filter by ID.
type DeleteNewSessionFilterMsg struct {
	WorkspaceID string
	FilterID    string
}

// NewSessionFilterDeletedMsg acknowledges deletion of a saved New Session Filter.
type NewSessionFilterDeletedMsg struct {
	FilterID string
	Message  string
}

// StartNewSessionAutonomousModeMsg requests starting autonomous mode from selected New Session Filters.
type StartNewSessionAutonomousModeMsg struct {
	SelectedFilterIDs []string
}

// StopNewSessionAutonomousModeMsg requests stopping autonomous mode.
type StopNewSessionAutonomousModeMsg struct{ Runtime *NewSessionAutonomousRuntime }

// NewSessionAutonomousStartedMsg reports autonomous mode startup with runtime handles.
type NewSessionAutonomousStartedMsg struct {
	Runtime *NewSessionAutonomousRuntime
	Events  <-chan tea.Msg
	Message string
}

// NewSessionAutonomousStoppedMsg reports autonomous mode shutdown.
type NewSessionAutonomousStoppedMsg struct{ Message string }

// NewSessionAutonomousStatusMsg delivers runtime status/warning updates.
type NewSessionAutonomousStatusMsg struct {
	Level   string // info, warning, error
	Message string
}

// NewSessionAutonomousDetectedWorkItemMsg is emitted when autonomous mode detects a matching created work item.
type NewSessionAutonomousDetectedWorkItemMsg struct {
	Adapter  adapter.WorkItemAdapter
	FilterID string
	WorkItem domain.Session
}

// SessionHistorySearchRequestedMsg requests a session-history refresh for the active overlay filter.
type SessionHistorySearchRequestedMsg struct{}

// OpenSessionHistoryMsg requests opening the selected session-history entry in the main content area.
type OpenSessionHistoryMsg struct {
	Entry domain.SessionHistoryEntry
}

// --- Error / status ---

// ErrMsg wraps an error for display in the TUI.
type ErrMsg struct{ Err error }

// AdapterErrorMsg reports an adapter handler failure after exhausting retries.
// The TUI displays this as a warning toast.
type AdapterErrorMsg struct {
	Adapter   string // adapter name (e.g. "github", "gitlab")
	EventType string // original event type that failed
	Err       error  // underlying error
	Retries   int    // number of retries attempted
}

// ActionDoneMsg is a generic success acknowledgement.
type ActionDoneMsg struct{ Message string }

// StartupWarningsMsg delivers adapter initialisation warnings that should be
// displayed as toasts when the TUI first appears.
type StartupWarningsMsg struct{ Warnings []string }

// OpenExternalURLMsg requests opening a durable external artifact URL in the system browser.
type OpenExternalURLMsg struct{ URL string }

// DeleteSessionMsg fires when the user confirms session deletion.
type DeleteSessionMsg struct{ SessionID string }

// SessionDeletedMsg is sent after a session and its related records are removed.
type SessionDeletedMsg struct {
	SessionID string
	TaskIDs   []string
	Message   string
	Warning   string
}

// SessionCreatedMsg is sent after the new-session flow persists a work item.
type SessionCreatedMsg struct {
	Session domain.Session
	Message string
}

// SessionDuplicatePromptMsg is sent when new-session creation resolves to an existing work item.
type SessionDuplicatePromptMsg struct {
	RequestedSession domain.Session
	ExistingSession  domain.Session
}

// SessionDuplicateAction identifies how the user resolved a duplicate work-item prompt.
type SessionDuplicateAction string

const (
	SessionDuplicateCancel        SessionDuplicateAction = "cancel"
	SessionDuplicateOpenExisting  SessionDuplicateAction = "open_existing"
	SessionDuplicateCreateSession SessionDuplicateAction = "create_session"
)

// SessionDuplicateActionMsg resolves the duplicate-session prompt.
type SessionDuplicateActionMsg struct{ Action SessionDuplicateAction }

// --- Log toasts ---

// LogToastMsg is sent when a slog warn/error entry should be shown as a toast.
type LogToastMsg struct {
	Level   string // "WARN" or "ERROR"
	Message string
}

// --- Overlay control ---

// ShowNewSessionMsg opens the New Session overlay.
type ShowNewSessionMsg struct{}

// ShowSettingsMsg opens the Settings page.
type ShowSettingsMsg struct{}

// CloseOverlayMsg closes the active overlay.
type CloseOverlayMsg struct{}

// OpenSourceItemsOverlayMsg opens the source items overlay for multi-select browsing.
type OpenSourceItemsOverlayMsg struct {
	Items []domain.SourceSummary
}

// openSourceItemURLsMsg is an internal message emitted by the source items overlay
// when the user confirms opening one or more source item URLs.
type openSourceItemURLsMsg struct{ URLs []string }

// ShowAddRepoMsg opens the Add Repository overlay.
type ShowAddRepoMsg struct{}

// RepoListLoadedMsg carries fetched repos from repo sources.
type RepoListLoadedMsg struct {
	RequestID int
	Repos     []adapter.RepoItem
	HasMore   bool
	Errs      []error
}

// AddRepoCloneMsg fires when user confirms cloning a repo.
type AddRepoCloneMsg struct {
	Repo     adapter.RepoItem
	CloneDir string // workspaceDir
	CloneURL string // resolved clone URL (URL or SSHURL fallback)
}

// RepoClonedMsg fires when git-work clone completes.
type RepoClonedMsg struct {
	RepoPath string
	Err      error
}

// --- Workspace Init ---

// WorkspaceHealthCheckMsg carries the result of a workspace scan during init.
type WorkspaceHealthCheckMsg struct {
	Check domain.WorkspaceHealthCheck
	Error error
}

// WorkspaceInitDoneMsg is sent after workspace metadata is persisted.
type WorkspaceInitDoneMsg struct {
	WorkspaceID   string
	WorkspaceName string
	WorkspaceDir  string
}

// WorkspaceServicesReloadedMsg is sent after the app rebuilds services for a newly initialized workspace.
type WorkspaceServicesReloadedMsg struct {
	Reload  viewsServicesReload
	Message string
}

// PlanEditedMsg is sent when the user edits a full plan document in $EDITOR and saves.
type PlanEditedMsg struct {
	PlanID     string
	WorkItemID string
	NewContent string
}

// PlanSavedMsg is sent after a reviewed plan document is parsed and persisted.
type PlanSavedMsg struct {
	WorkItemID string
	Plan       domain.Plan
	SubPlans   []domain.TaskPlan
	Message    string
}

// ConfirmAbandonMsg requests a confirmation dialog before abandoning a session.
type ConfirmAbandonMsg struct{ SessionID string }

// ConfirmDeleteSessionMsg requests a confirmation dialog before deleting a session.
type ConfirmDeleteSessionMsg struct{ SessionID string }

// ConfirmOverrideAcceptMsg requests a confirmation dialog before overriding review acceptance.
type ConfirmOverrideAcceptMsg struct{ WorkItemID string }

// LiveInstancesLoadedMsg carries the set of currently-alive instance IDs.
type LiveInstancesLoadedMsg struct {
	// AliveIDs is the set of instance IDs whose heartbeat is within the staleness threshold.
	AliveIDs map[string]bool
}

// ImplementationCompleteMsg is sent when RunImplementationCmd finishes successfully.
// SessionIDs holds the IDs of completed implementation sessions that need review.
type ImplementationCompleteMsg struct {
	PlanID     string
	WorkItemID string
	SessionIDs []string // completed session IDs; may be empty on partial failure
}

// ForemanReplyMsg delivers a refreshed Foreman proposal after a human follow-up message.
// The TUI should update the QuestionModel with the new proposal without clearing the question.
type ForemanReplyMsg struct {
	QuestionID  string
	NewProposal string
	Uncertain   bool
}

// SteerSessionMsg fires when the user submits a steering prompt for a running session.
type SteerSessionMsg struct {
	SessionID string
	Message   string
}

// SteerSessionSentMsg confirms a steering message was delivered to the agent.
type SteerSessionSentMsg struct {
	SessionID string
}

// FollowUpSessionMsg fires when the user submits a follow-up prompt for a completed session.
type FollowUpSessionMsg struct {
	TaskID   string
	Feedback string
}

// FollowUpSessionSentMsg confirms a follow-up session was started.
type FollowUpSessionSentMsg struct {
	TaskID string
}

// FollowUpFailedSessionMsg fires when the user submits a follow-up prompt for a failed session.
type FollowUpFailedSessionMsg struct {
	TaskID   string
	Feedback string
}

// FollowUpFailedSessionSentMsg confirms a follow-up session was started for a failed task.
type FollowUpFailedSessionSentMsg struct {
	TaskID string
}

// FollowUpPlanMsg requests a follow-up re-planning for a completed work item.
type FollowUpPlanMsg struct {
	WorkItemID string
	Feedback   string
}

// FollowUpPlanResultMsg is the result of a follow-up planning attempt.
type FollowUpPlanResultMsg struct {
	WorkItemID string
	Err        error
}

// InspectPlanMsg requests loading a plan by ID for read-only inspection.
type InspectPlanMsg struct{ PlanID string }

// InspectPlanLoadedMsg delivers a composed plan document for overlay display.
type InspectPlanLoadedMsg struct {
	PlanID   string
	Document string
	// Err is non-nil when the plan could not be loaded; the overlay must close and surface the error.
	Err error
}

// --- Repo Manager ---

// repoKind classifies a repository found in the workspace.
type repoKind int

const (
	repoKindGitWork  repoKind = iota // has .bare/ layout, fully git-work managed
	repoKindPlainGit                 // has .git entry, not yet initialized with git-work
)

// managedRepo is a repository discovered in the workspace directory.
type managedRepo struct {
	Path string   // absolute path to the repo directory
	Name string   // filepath.Base(Path)
	Kind repoKind // classification
}

// ManagedReposLoadedMsg is sent when the repo manager scans the workspace.
type ManagedReposLoadedMsg struct {
	Repos []managedRepo
	Err   error
}

// WorktreesLoadedMsg delivers worktrees for a git-work repository.
// RequestID guards against stale responses when list selection changes quickly.
type WorktreesLoadedMsg struct {
	RequestID int
	RepoPath  string
	Worktrees []gitwork.Worktree
	Err       error
}

// RepoRemovedMsg is sent after RemoveRepoCmd completes (success or failure).
type RepoRemovedMsg struct {
	RepoPath string
	Err      error
}

// RepoInitializedMsg is sent after InitRepoCmd completes (success or failure).
type RepoInitializedMsg struct {
	RepoPath string
	Err      error
}

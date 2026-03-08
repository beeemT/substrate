package views

import (
	"time"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/domain"
)

// --- DB polling / data loading ---

// WorkItemsLoadedMsg is sent when the work item list is refreshed.
type WorkItemsLoadedMsg struct{ Items []domain.WorkItem }

// SessionsLoadedMsg is sent when sessions for a workspace are refreshed.
type SessionsLoadedMsg struct{ Sessions []domain.AgentSession }

// SessionHistoryLoadedMsg is sent when a session-history search completes.
type SessionHistoryLoadedMsg struct {
	Filter  domain.SessionHistoryFilter
	Entries []domain.SessionHistoryEntry
}

// PlanLoadedMsg is sent when a plan is loaded.
type PlanLoadedMsg struct {
	WorkItemID string
	Plan       *domain.Plan // nil if not found
	SubPlans   []domain.SubPlan
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
	Lines      []string
	NextOffset int64
}

// SessionInteractionLoadedMsg delivers the parsed interaction log for one session.
type SessionInteractionLoadedMsg struct {
	SessionID string
	Lines     []string
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

// PlanRejectMsg fires when user confirms rejection.
type PlanRejectMsg struct {
	PlanID     string
	Reason     string
	WorkItemID string
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

// AbandonSessionMsg fires when the user confirms abandonment.
type AbandonSessionMsg struct{ SessionID string }

// WorkspaceInitMsg fires when user confirms workspace initialization.
type WorkspaceInitMsg struct{ Dir string }

// WorkspaceCancelMsg fires when user cancels workspace initialization (triggers exit).
type WorkspaceCancelMsg struct{}

// ReimplementMsg fires when user triggers re-implementation from review.
type ReimplementMsg struct{ WorkItemID string }

// StartPlanMsg fires when the user presses Enter on a work item in the ready-to-plan state.
type StartPlanMsg struct{ WorkItemID string }

// OverrideAcceptMsg fires when user overrides and accepts critiques.
type OverrideAcceptMsg struct{ WorkItemID string }

// NewSessionBrowseMsg fires when user selects browsed items and starts a session.
type NewSessionBrowseMsg struct {
	Adapter   adapter.WorkItemAdapter
	Selection adapter.Selection
}

// NewSessionManualMsg fires when user submits the manual session form.
type NewSessionManualMsg struct {
	Adapter adapter.WorkItemAdapter
	Title   string
	Desc    string
}

// --- Error / status ---

// ErrMsg wraps an error for display in the TUI.
type ErrMsg struct{ Err error }

// ActionDoneMsg is a generic success acknowledgement.
type ActionDoneMsg struct{ Message string }

// --- Overlay control ---

// ShowNewSessionMsg opens the New Session overlay.
type ShowNewSessionMsg struct{}

// ShowSettingsMsg opens the Settings page.
type ShowSettingsMsg struct{}

// CloseOverlayMsg closes the active overlay.
type CloseOverlayMsg struct{}

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

// PlanEditedMsg is sent when the user edits a plan in $EDITOR and saves.
type PlanEditedMsg struct {
	PlanID     string
	NewContent string
}

// ConfirmAbandonMsg requests a confirmation dialog before abandoning a session.
type ConfirmAbandonMsg struct{ SessionID string }

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

// ReviewCompleteMsg is sent when RunReviewSessionCmd finishes.
// The TUI uses ReviewSessionID to tail/display the review agent's log.
type ReviewCompleteMsg struct {
	ImplSessionID   string // implementation session that was reviewed
	ReviewSessionID string // review agent session whose log to display
}

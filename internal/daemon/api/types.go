// Package api contains daemon transport DTOs.
package api

import (
	"time"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/logic"
	"github.com/beeemT/substrate/internal/logic/readmodel"
	"github.com/beeemT/substrate/internal/sessionlog"
)

type HealthRequest struct{}

type HealthResponse struct {
	Ready             bool   `json:"ready"`
	Version           string `json:"version"`
	BuildSHA          string `json:"build_sha"`
	SchemaVersion     uint32 `json:"schema_version"`
	WorkspaceID       string `json:"workspace_id"`
	RebuildInProgress bool   `json:"rebuild_in_progress"`
	UptimeSeconds     int64  `json:"uptime_seconds"`
}

type InfoRequest struct{}

type InfoResponse struct {
	Version     string `json:"version"`
	BuildSHA    string `json:"build_sha"`
	WorkspaceID string `json:"workspace_id"`
}

type DisconnectRequest struct{}
type DisconnectResponse struct{}

type ShutdownRequest struct{}

type GetAccessTokenRequest struct{}

type GetAccessTokenResponse struct {
	Token string `json:"token"`
}

type RotateAccessTokenRequest struct{}

type RotateAccessTokenResponse struct {
	Token string `json:"token"`
}
type ShutdownResponse struct{}

type ListSessionsRequest struct {
	WorkspaceID string `json:"workspace_id"`
}

type ListAgentSessionsRequest struct {
	WorkspaceID string `json:"workspace_id"`
	WorkItemID  string `json:"work_item_id"`
}

type ListAgentSessionsResponse struct {
	AgentSessions []domain.AgentSession `json:"agent_sessions"`
}

type SearchHistoryRequest struct {
	Filter domain.SessionHistoryFilter `json:"filter"`
}

type SearchHistoryResponse struct {
	Entries []domain.SessionHistoryEntry `json:"entries"`
}

type GetInteractionRequest struct {
	AgentSessionID string `json:"agent_session_id"`
}

type GetInteractionResponse struct {
	Entries []sessionlog.Entry `json:"entries"`
}

type ListSessionsResponse struct {
	Sessions []domain.Session `json:"sessions"`
}

type GetSessionRequest struct {
	SessionID string `json:"session_id"`
}

type SearchSessionHistoryRequest struct {
	Filter domain.SessionHistoryFilter `json:"filter"`
}

type SearchSessionHistoryResponse struct {
	Entries []domain.SessionHistoryEntry `json:"entries"`
}

type ArchiveSessionRequest struct {
	SessionID      string `json:"session_id"`
	IdempotencyKey string `json:"idempotency_key"`
}

type DeleteSessionRequest struct {
	SessionID      string `json:"session_id"`
	IdempotencyKey string `json:"idempotency_key"`
}
type UnarchiveSessionRequest struct {
	SessionID      string `json:"session_id"`
	IdempotencyKey string `json:"idempotency_key"`
}

type OverrideAcceptRequest struct {
	WorkItemID     string `json:"work_item_id"`
	IdempotencyKey string `json:"idempotency_key"`
}

type FailReviewRequest struct {
	WorkItemID     string `json:"work_item_id"`
	IdempotencyKey string `json:"idempotency_key"`
}

type GetSessionResponse struct {
	Session domain.Session `json:"session"`
}

type GetInitialSnapshotRequest struct {
	WorkspaceID string `json:"workspace_id"`
}

type GetInitialSnapshotResponse struct {
	Snapshot logic.InitialSnapshot `json:"snapshot"`
}

type ApprovePlanRequest struct {
	PlanID         string `json:"plan_id"`
	WorkItemID     string `json:"work_item_id"`
	IdempotencyKey string `json:"idempotency_key"`
}

type RequestPlanChangesRequest struct {
	WorkItemID     string `json:"work_item_id"`
	PlanID         string `json:"plan_id"`
	Feedback       string `json:"feedback"`
	IdempotencyKey string `json:"idempotency_key"`
}

type SaveReviewedPlanRequest struct {
	PlanID         string `json:"plan_id"`
	Content        string `json:"content"`
	IdempotencyKey string `json:"idempotency_key"`
}

type SaveReviewedPlanResponse struct {
	Plan     domain.Plan       `json:"plan"`
	SubPlans []domain.TaskPlan `json:"sub_plans"`
	Message  string            `json:"message"`
}

type RunImplementationRequest struct {
	PlanID         string `json:"plan_id"`
	IdempotencyKey string `json:"idempotency_key"`
}

type StartPlanningRequest struct {
	WorkItemID     string `json:"work_item_id"`
	IdempotencyKey string `json:"idempotency_key"`
}

type RestartPlanningRequest struct {
	WorkItemID     string `json:"work_item_id"`
	Prompt         string `json:"prompt"`
	IdempotencyKey string `json:"idempotency_key"`
}

type FollowUpPlanRequest struct {
	WorkItemID     string `json:"work_item_id"`
	Feedback       string `json:"feedback"`
	IdempotencyKey string `json:"idempotency_key"`
}

type FinalizeSessionRequest struct {
	WorkItemID     string `json:"work_item_id"`
	IdempotencyKey string `json:"idempotency_key"`
}

type RetryFailedSessionRequest struct {
	PlanID         string `json:"plan_id"`
	WorkItemID     string `json:"work_item_id"`
	IdempotencyKey string `json:"idempotency_key"`
}

type ResumeAllForSessionRequest struct {
	WorkItemID     string `json:"work_item_id"`
	InstanceID     string `json:"instance_id"`
	IdempotencyKey string `json:"idempotency_key"`
}

type RetryAgentSessionRequest struct {
	AgentSessionID string `json:"agent_session_id"`
	InstanceID     string `json:"instance_id"`
	IdempotencyKey string `json:"idempotency_key"`
}

type CancelPipelineRequest struct {
	WorkItemID     string `json:"work_item_id"`
	AgentSessionID string `json:"agent_session_id"`
	IdempotencyKey string `json:"idempotency_key"`
}

type FollowUpAgentSessionRequest struct {
	AgentSessionID string `json:"agent_session_id"`
	Feedback       string `json:"feedback"`
	InstanceID     string `json:"instance_id"`
	IdempotencyKey string `json:"idempotency_key"`
}

type SteerSessionRequest struct {
	AgentSessionID string `json:"agent_session_id"`
	Message        string `json:"message"`
	IdempotencyKey string `json:"idempotency_key"`
}

type AnswerQuestionRequest struct {
	QuestionID     string `json:"question_id"`
	Answer         string `json:"answer"`
	AnsweredBy     string `json:"answered_by"`
	IdempotencyKey string `json:"idempotency_key"`
}

type SkipQuestionRequest struct {
	QuestionID     string `json:"question_id"`
	IdempotencyKey string `json:"idempotency_key"`
}

type ActionResultResponse struct {
	Message string `json:"message"`
}

type OperationResponse struct {
	Operation logic.Operation `json:"operation"`
}

type SubscribeEventsRequest struct {
	WorkspaceID           string   `json:"workspace_id"`
	AfterSequence         uint64   `json:"after_sequence"`
	ReplayWindow          int      `json:"replay_window"`
	EventTypes            []string `json:"event_types"`
	IncludeSnapshotMarker bool     `json:"include_snapshot_marker"`
}

type EventBatch struct {
	Events         []SystemEventEnvelope `json:"events"`
	LatestSequence uint64                `json:"latest_sequence"`
	CaughtUp       bool                  `json:"caught_up"`
}

type TailAgentSessionLogRequest struct {
	AgentSessionID string `json:"agent_session_id"`
	Since          int64  `json:"since"`
}

type SnapshotAgentSessionLogRequest struct {
	AgentSessionID string `json:"agent_session_id"`
}

type SessionLogBatch struct {
	AgentSessionID string   `json:"agent_session_id"`
	EntriesJSON    []string `json:"entries_json"`
	NextOffset     int64    `json:"next_offset"`
	Final          bool     `json:"final"`
}

type SessionLogSnapshot struct {
	AgentSessionID string   `json:"agent_session_id"`
	EntriesJSON    []string `json:"entries_json"`
	NextOffset     int64    `json:"next_offset"`
}

type TailAppLogRequest struct {
	Since int64 `json:"since"`
}

type AppLogBatch struct {
	EntriesJSON []string `json:"entries_json"`
	NextOffset  int64    `json:"next_offset"`
}

type SnapshotAppLogRequest struct{}

type AppLogSnapshot struct {
	EntriesJSON []string `json:"entries_json"`
	NextOffset  int64    `json:"next_offset"`
}

type GetSessionOverviewRequest struct {
	WorkspaceID string `json:"workspace_id"`
	SessionID   string `json:"session_id"`
}

type GetSessionOverviewResponse struct {
	Overview readmodel.SessionOverview `json:"overview"`
}

type GetSidebarRequest struct {
	WorkspaceID string `json:"workspace_id"`
}
type GetPlanRequest struct {
	WorkspaceID string `json:"workspace_id"`
	SessionID   string `json:"session_id"`
}

type PlanView struct {
	PayloadJSON string `json:"payload_json"`
}

type GetArtifactsRequest struct {
	WorkspaceID string `json:"workspace_id"`
	SessionID   string `json:"session_id"`
}

type ArtifactsView struct {
	PayloadJSON string `json:"payload_json"`
}

type GetSidebarResponse struct {
	Entries []readmodel.SidebarEntry `json:"entries"`
}

type GetAvailableActionsRequest struct {
	WorkspaceID string `json:"workspace_id"`
	SessionID   string `json:"session_id"`
}

type GetAvailableActionsResponse struct {
	Actions []readmodel.ActionKind `json:"actions"`
}

type RuntimeContext struct {
	WorkspaceID   string `json:"workspace_id"`
	WorkspaceName string `json:"workspace_name"`
	WorkspaceDir  string `json:"workspace_dir"`
	InstanceID    string `json:"instance_id"`
}

type GetRuntimeContextRequest struct{}

type Workspace struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Dir  string `json:"dir"`
}

type InitializeWorkspaceRequest struct {
	Dir  string `json:"dir"`
	Name string `json:"name"`
}

type WorkspaceHealth struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

type HealthCheckWorkspaceRequest struct {
	Dir string `json:"dir"`
}

type ListManagedReposRequest struct {
	WorkspaceDir string `json:"workspace_dir"`
}

type ListManagedReposResponse struct {
	ReposJSON []string `json:"repos_json"`
}

type ListWorktreesRequest struct {
	RepoPath string `json:"repo_path"`
}

type ListWorktreesResponse struct {
	WorktreesJSON []string `json:"worktrees_json"`
}

type CloneRepoRequest struct {
	CloneURL       string `json:"clone_url"`
	CloneDir       string `json:"clone_dir"`
	IdempotencyKey string `json:"idempotency_key"`
}

type CloneRepoResponse struct {
	RepoPath string `json:"repo_path"`
}

type InitRepoRequest struct {
	RepoPath       string `json:"repo_path"`
	IdempotencyKey string `json:"idempotency_key"`
}

type InitRepoResponse struct {
	RepoPath string `json:"repo_path"`
}

type RemoveRepoRequest struct {
	RepoPath       string `json:"repo_path"`
	IdempotencyKey string `json:"idempotency_key"`
}

type RemoveRepoResponse struct{}

type GetSettingsRequest struct{}

type GetSettingsResponse struct {
	RawYAML      string   `json:"raw_yaml"`
	ActiveDaemon string   `json:"active_daemon"`
	DaemonsJSON  []string `json:"daemons_json"`
}

type SaveSettingsRequest struct {
	RawYAML        string `json:"raw_yaml"`
	IdempotencyKey string `json:"idempotency_key"`
}

type SaveSettingsResponse struct {
	Message string `json:"message"`
}

type ProviderStatus struct {
	Title       string `json:"title"`
	Configured  bool   `json:"configured"`
	Connected   bool   `json:"connected"`
	AuthSource  string `json:"auth_source"`
	Description string `json:"description"`
	LastError   string `json:"last_error"`
}

type TestProviderRequest struct {
	Provider string `json:"provider"`
	RawYAML  string `json:"raw_yaml"`
}

type LoginProviderRequest struct {
	Provider string `json:"provider"`
	Harness  string `json:"harness"`
	RawYAML  string `json:"raw_yaml"`
}

type LoginProviderResponse struct {
	Message string `json:"message"`
	Dirty   bool   `json:"dirty"`
	RawYAML string `json:"raw_yaml"`
}

type RefreshProviderDiagnosticsRequest struct {
	RawYAML string `json:"raw_yaml"`
}

type RefreshProviderDiagnosticsResponse struct {
	RawYAML        string                    `json:"raw_yaml"`
	HarnessWarning string                    `json:"harness_warning"`
	Providers      map[string]ProviderStatus `json:"providers"`
}

type SystemEventEnvelope struct {
	Sequence    uint64    `json:"sequence"`
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	Type        string    `json:"type"`
	CreatedAt   time.Time `json:"created_at"`
	PayloadJSON string    `json:"payload_json"`
}

// StartAutonomousModeRequest asks the daemon to start autonomous mode for the
// supplied New Session Filter IDs. The owning TUI hands off watch-stream and
// filter-lock responsibilities to the daemon in remote mode; the daemon keeps
// them alive for the duration of the controlling session.
type StartAutonomousModeRequest struct {
	WorkspaceID       string   `json:"workspace_id"`
	InstanceID        string   `json:"instance_id"`
	SelectedFilterIDs []string `json:"selected_filter_ids"`
	IdempotencyKey    string   `json:"idempotency_key"`
}

// StopAutonomousModeRequest asks the daemon to stop any running autonomous
// mode for the controlling instance. With an empty InstanceID the daemon
// stops every active run (operator override).
type StopAutonomousModeRequest struct {
	InstanceID     string `json:"instance_id"`
	IdempotencyKey string `json:"idempotency_key"`
}

// GetAutonomousModeStatusRequest reports the autonomous-mode state owned by
// the daemon. When InstanceID is empty the daemon reports a single combined
// view of all running instances.
type GetAutonomousModeStatusRequest struct {
	InstanceID string `json:"instance_id"`
}

type AutonomousStatusEntry struct {
	Kind      string    `json:"kind"`
	Level     string    `json:"level"`
	Message   string    `json:"message"`
	Payload   string    `json:"payload,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// AutonomousModeRun summarises one autonomous-mode instance owned by the
// daemon. The current state, active filters, recent status events, and
// timestamps let a TUI (or a follow-up operator) reconstruct the on-screen
// status without owning provider watch streams or filter locks.
type AutonomousModeRun struct {
	InstanceID       string              `json:"instance_id"`
	WorkspaceID      string              `json:"workspace_id"`
	Running          bool                `json:"running"`
	StartedAt        time.Time           `json:"started_at"`
	StoppedAt        time.Time           `json:"stopped_at"`
	StopReason       string              `json:"stop_reason"`
	ActiveFilterIDs  []string            `json:"active_filter_ids"`
	ActiveByProvider map[string][]string `json:"active_by_provider"`
	RecentStatusJSON []string            `json:"recent_status_json"`
	LastDetectedJSON []string            `json:"last_detected_json"`
}

// AutonomousModeStatusResponse is the wire payload returned from
// GetAutonomousModeStatus. Runs carries every autonomous-mode instance the
// daemon is currently tracking.
type AutonomousModeStatusResponse struct {
	Running     bool                `json:"running"`
	Runs        []AutonomousModeRun `json:"runs"`
	ActiveCount int                 `json:"active_count"`
}

// AutonomousModeStatusEventType is the SystemEvent type used to publish
// autonomous-mode status changes (start, stop, status, detected work item).
// The event payload is a JSON object with at least an `instance_id` field
// plus the type-specific fields listed below.
const (
	AutonomousModeEventStarted  = "autonomous_mode.started"
	AutonomousModeEventStopped  = "autonomous_mode.stopped"
	AutonomousModeEventStatus   = "autonomous_mode.status"
	AutonomousModeEventDetected = "autonomous_mode.detected"
)

func EventEnvelope(event domain.SystemEvent) SystemEventEnvelope {
	return SystemEventEnvelope{
		Sequence:    event.Sequence,
		ID:          event.ID,
		WorkspaceID: event.WorkspaceID,
		Type:        event.EventType,
		CreatedAt:   event.CreatedAt,
		PayloadJSON: event.Payload,
	}
}

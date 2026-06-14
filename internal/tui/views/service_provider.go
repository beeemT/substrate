package views

import (
	"context"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/app"
	daemonapi "github.com/beeemT/substrate/internal/daemon/api"
	"github.com/beeemT/substrate/internal/event"
	"github.com/beeemT/substrate/internal/gitwork"
	"github.com/beeemT/substrate/internal/logic"
	"github.com/beeemT/substrate/internal/orchestrator"
	"github.com/beeemT/substrate/internal/service"
)

type SessionLogStream interface {
	RecvMsg(any) error
}

type SessionLogClient interface {
	SnapshotAgentSessionLog(context.Context, string) (*daemonapi.SessionLogSnapshot, error)
	TailAgentSessionLog(context.Context, daemonapi.TailAgentSessionLogRequest) (SessionLogStream, error)
}

type EventStreamClient interface {
	SubscribeEvents(context.Context, daemonapi.SubscribeEventsRequest) (SessionLogStream, error)
}

type SettingsClient interface {
	GetSettings(context.Context) (*daemonapi.GetSettingsResponse, error)
	SaveSettings(context.Context, string, string) (*daemonapi.SaveSettingsResponse, error)
	TestProvider(context.Context, string, string) (*daemonapi.ProviderStatus, error)
	LoginProvider(context.Context, string, string, string) (*daemonapi.LoginProviderResponse, error)
	RefreshProviderDiagnostics(context.Context, string) (*daemonapi.RefreshProviderDiagnosticsResponse, error)
}
type AutonomousClient interface {
	StartAutonomousMode(context.Context, daemonapi.StartAutonomousModeRequest) (*daemonapi.AutonomousModeRun, error)
	StopAutonomousMode(context.Context, daemonapi.StopAutonomousModeRequest) (*daemonapi.AutonomousModeStatusResponse, error)
	GetAutonomousModeStatus(context.Context, string) (*daemonapi.AutonomousModeStatusResponse, error)
}
type WorkspaceClient interface {
	InitializeWorkspace(context.Context, string, string) (*daemonapi.Workspace, error)
	HealthCheckWorkspace(context.Context, string) (*daemonapi.WorkspaceHealth, error)
	ListManagedRepos(context.Context, string) (*daemonapi.ListManagedReposResponse, error)
	ListWorktrees(context.Context, string) (*daemonapi.ListWorktreesResponse, error)
	CloneRepo(context.Context, string, string) (*daemonapi.CloneRepoResponse, error)
	InitRepo(context.Context, string) (*daemonapi.InitRepoResponse, error)
	RemoveRepo(context.Context, string) (*daemonapi.RemoveRepoResponse, error)
}

// ServiceProvider provides access to the current service graph.
// Implementations guarantee that returned pointers are the current
// ones — callers must not cache them across reloads.
type ServiceProvider interface {
	// Internal: returns the underlying Services struct

	// Logic exposes product-shaped actions and read models.
	EventClient() EventStreamClient
	LogClient() SessionLogClient
	AutonomousClient() AutonomousClient
	WorkspaceClient() WorkspaceClient
	Logic() logic.Client
	GetServices() *Services

	// Close shuts down the service graph: stops foremen, aborts sessions,
	// stops refresh goroutines, and closes the event bus.
	// Safe to call even if Init has not been called.
	Close(ctx context.Context)

	// Domain services
	Session() *service.SessionService
	Plan() *service.PlanService
	Task() *service.AgentSessionService
	Continuation() *service.AgentSessionContinuationService
	Question() *service.QuestionService
	Instance() *service.InstanceService
	Workspace() *service.WorkspaceService
	Review() *service.ReviewService
	Events() *service.EventService
	GithubPRs() *service.GithubPRService
	GitlabMRs() *service.GitlabMRService
	SessionArtifacts() *service.SessionReviewArtifactService
	GithubPRReviews() *service.GithubPRReviewService
	GitlabMRReviews() *service.GitlabMRReviewService
	GithubPRChecks() *service.GithubPRCheckService
	GitlabMRChecks() *service.GitlabMRCheckService
	NewSessionFilters() *service.SessionFilterService
	NewSessionFilterLocks() *service.SessionFilterLockService
	Settings() SettingsService

	// Orchestration
	Planning() *orchestrator.PlanningService
	Implementation() *orchestrator.ImplementationService
	ReviewPipeline() *orchestrator.ReviewPipeline
	// AnswerRouter routes human answers based on question phase.
	AnswerRouter() orchestrator.AnswerRouter
	// ReviewFollowup owns Foreman lifecycle for follow-up sessions.
	ReviewFollowup() *orchestrator.ReviewFollowup
	SessionRegistry() orchestrator.SessionRegistry
	// Manual returns the manual session service.
	Manual() *orchestrator.ManualSessionService

	// Infrastructure / derived state rebuilt with services
	Bus() *event.Bus
	GitClient() *gitwork.Client
	Adapters() []adapter.WorkItemAdapter
	RepoSources() []adapter.RepoSource
	Harnesses() app.AgentHarnesses
	ReviewComments() *adapter.ReviewCommentDispatcher
	StartupWarnings() []string
}

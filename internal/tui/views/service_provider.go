package views

import (
	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/app"
	"github.com/beeemT/substrate/internal/event"
	"github.com/beeemT/substrate/internal/gitwork"
	"github.com/beeemT/substrate/internal/orchestrator"
	"github.com/beeemT/substrate/internal/service"
)

// ServiceProvider provides access to the current service graph.
// Implementations guarantee that returned pointers are the current
// ones — callers must not cache them across reloads.
type ServiceProvider interface {
	// Internal: returns the underlying Services struct
	GetServices() *Services

	// Domain services
	Session() *service.SessionService
	Plan() *service.PlanService
	Task() *service.TaskService
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
	Settings() *SettingsService

	// Orchestration
	Planning() *orchestrator.PlanningService
	Implementation() *orchestrator.ImplementationService
	ReviewPipeline() *orchestrator.ReviewPipeline
	Resumption() *orchestrator.Resumption
	Foreman() *orchestrator.Foreman
	SessionRegistry() *orchestrator.SessionRegistry

	// Infrastructure / derived state rebuilt with services
	Bus() *event.Bus
	GitClient() *gitwork.Client
	Adapters() []adapter.WorkItemAdapter
	RepoSources() []adapter.RepoSource
	Harnesses() app.AgentHarnesses
	ReviewComments() *adapter.ReviewCommentDispatcher
	StartupWarnings() []string
}

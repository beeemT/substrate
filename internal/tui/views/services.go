package views

import (
	"context"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/app"
	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/event"
	"github.com/beeemT/substrate/internal/gitwork"
	"github.com/beeemT/substrate/internal/orchestrator"
	"github.com/beeemT/substrate/internal/service"
	"github.com/beeemT/substrate/internal/tuilog"
)

// Services aggregates all dependencies needed by the TUI.
type Services struct {
	Session               *service.SessionService
	Plan                  *service.PlanService
	Task                  *service.AgentSessionService
	Question              *service.QuestionService
	Instance              *service.InstanceService
	Workspace             *service.WorkspaceService
	Review                *service.ReviewService
	Events                *service.EventService
	GithubPRs             *service.GithubPRService
	GitlabMRs             *service.GitlabMRService
	SessionArtifacts      *service.SessionReviewArtifactService
	GithubPRReviews       *service.GithubPRReviewService
	GitlabMRReviews       *service.GitlabMRReviewService
	GithubPRChecks        *service.GithubPRCheckService
	GitlabMRChecks        *service.GitlabMRCheckService
	NewSessionFilters     *service.SessionFilterService
	NewSessionFilterLocks *service.SessionFilterLockService
	// Orchestration pipelines backed by the configured agent harnesses.
	Planning       *orchestrator.PlanningService
	Implementation *orchestrator.ImplementationService
	ReviewPipeline *orchestrator.ReviewPipeline
	Resumption     *orchestrator.Resumption
	// AnswerRouter routes human answers and skips based on question phase.
	AnswerRouter orchestrator.AnswerRouter
	// ReviewFollowup owns Foreman lifecycle for follow-up sessions.
	ReviewFollowup  *orchestrator.ReviewFollowup
	SessionRegistry orchestrator.SessionRegistry
	// QuestionRouter is the single stage-aware routing point for normalized agent questions.
	QuestionRouter *orchestrator.QuestionRouter
	// Manual is the manual agent session orchestration service.
	Manual         *orchestrator.ManualSessionService
	Cfg            *config.Config
	Adapters       []adapter.WorkItemAdapter
	RepoSources    []adapter.RepoSource
	Harnesses      app.AgentHarnesses
	Settings       SettingsService
	GitClient      *gitwork.Client
	Bus            *event.Bus
	ReviewComments *adapter.ReviewCommentDispatcher
	// StartupWarnings collects adapter initialisation warnings to surface
	// as toasts when the TUI starts.
	StartupWarnings []string
	// LogStore holds all captured slog entries for the logs overlay.
	LogStore *tuilog.Store
	// LogToasts delivers slog warn/error entries for toast display.
	LogToasts <-chan tuilog.ToastEntry
	// Instance identity
	InstanceID    string
	WorkspaceID   string
	WorkspaceDir  string
	WorkspaceName string
	// RefreshStoppers holds cancel functions for PR/MR refresh goroutines.
	// Call these before Rebuild to prevent orphaned goroutines.
	RefreshStoppers []func()
}

func (s Services) ForemanHarness() adapter.AgentHarness {
	if s.Harnesses.Foreman != nil {
		return s.Harnesses.Foreman
	}

	return nil
}

// Close shuts down all services and resources owned by the service graph.
// It stops all foremen, aborts all sessions, calls refresh goroutine stoppers,
// and closes the event bus.
func (s Services) Close(ctx context.Context) {
	// Close session registry: stops all foremen and aborts all sessions.
	if s.SessionRegistry != nil {
		s.SessionRegistry.Close(ctx)
	}

	// Stop all PR/MR refresh goroutines.
	for _, stop := range s.RefreshStoppers {
		if stop != nil {
			stop()
		}
	}

	// Close the event bus.
	if s.Bus != nil {
		s.Bus.Close()
	}
}

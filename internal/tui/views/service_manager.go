package views

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	atomic "github.com/beeemT/go-atomic"
	"github.com/beeemT/substrate/internal/adapter"
	githubadapter "github.com/beeemT/substrate/internal/adapter/github"
	"github.com/beeemT/substrate/internal/app"
	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/event"
	"github.com/beeemT/substrate/internal/gitwork"
	"github.com/beeemT/substrate/internal/orchestrator"
	"github.com/beeemT/substrate/internal/repository"
	"github.com/beeemT/substrate/internal/service"
	"github.com/beeemT/substrate/internal/worktree"
)

// Verify ServiceManager implements ServiceProvider at compile time.
var _ ServiceProvider = (*ServiceManager)(nil)

// ServiceManager owns the complete service graph lifecycle.
// It is the single place responsible for building and rebuilding services.
type ServiceManager struct {
	transacter atomic.Transacter[repository.Resources]
	eventRepo  repository.EventRepository
	mu         sync.RWMutex
	services   *Services // nil until Init() or after first Rebuild()
}

// NewServiceManager creates a new ServiceManager.
func NewServiceManager(
	transacter atomic.Transacter[repository.Resources],
	eventRepo repository.EventRepository,
) *ServiceManager {
	return &ServiceManager{
		transacter: transacter,
		eventRepo:  eventRepo,
	}
}

// Init builds the initial service graph.
func (sm *ServiceManager) Init(ctx context.Context, cfg *config.Config) error {
	return sm.InitWithServices(ctx, cfg, Services{})
}

// InitWithServices builds the initial service graph from known runtime context.
// Use this when startup has already resolved workspace/instance identity so the
// service graph does not need to be built once globally and then rebuilt for the workspace.
func (sm *ServiceManager) InitWithServices(ctx context.Context, cfg *config.Config, current Services) error {
	return sm.initWithOptions(ctx, cfg, current, serviceBuildOptions{})
}

// InitWithDeferredIntegrations builds a usable initial service graph without
// external provider initialization. Call Rebuild later to install the full graph.
func (sm *ServiceManager) InitWithDeferredIntegrations(ctx context.Context, cfg *config.Config, current Services) error {
	return sm.initWithOptions(ctx, cfg, current, serviceBuildOptions{deferIntegrations: true})
}

type serviceBuildOptions struct {
	deferIntegrations bool
}

func (sm *ServiceManager) initWithOptions(ctx context.Context, cfg *config.Config, current Services, opts serviceBuildOptions) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	settingsSvc := current.Settings
	if settingsSvc == nil {
		settingsSvc = NewSettingsService(sm.transacter, config.OSKeychainStore{}, sm)
	}
	current.Settings = settingsSvc
	svcs, err := sm.buildServicesWithOptions(ctx, cfg, current, opts)
	if err != nil {
		return err
	}
	sm.services = svcs
	return nil
}

// Rebuild tears down the current graph and builds a new one.
// Called by SettingsService when config changes.
func (sm *ServiceManager) Rebuild(ctx context.Context, cfg *config.Config, current Services) (*Services, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Stop old refresh goroutines BEFORE building new ones.
	// The old goroutines hold a reference to the old bus — if we build new services
	// first and then close the old ones, we would close the NEW bus instead of the old
	// one (because Close() closes the bus owned by Services). Stopping first ensures
	// the old goroutines exit cleanly before we touch anything new.
	if sm.services != nil {
		for _, stop := range sm.services.RefreshStoppers {
			if stop != nil {
				stop()
			}
		}
	}

	svcs, err := sm.buildServices(ctx, cfg, current)
	if err != nil {
		return nil, err
	}

	// Tear down old services (foremen, sessions) and close the old bus.
	// Refresh stoppers were already called above so they are no-ops here.
	if sm.services != nil {
		sm.services.Close(context.WithoutCancel(ctx))
	}

	sm.services = svcs
	return svcs, nil
}

// GetServices returns the current service graph.
// Safe to call concurrently.
func (sm *ServiceManager) GetServices() *Services {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.services
}

// Close shuts down the service graph: stops foremen, aborts sessions,
// stops refresh goroutines, and closes the event bus.
func (sm *ServiceManager) Close(ctx context.Context) {
	sm.mu.RLock()
	services := sm.services
	sm.mu.RUnlock()
	if services != nil {
		services.Close(ctx)
	}
}

// InitWorkspace rebuilds services for a new workspace.
// This is called when the user initializes a new workspace.
func (sm *ServiceManager) InitWorkspace(ctx context.Context, cfg *config.Config, current Services, workspaceID, workspaceName, workspaceDir string) (*Services, error) {
	current.WorkspaceID = workspaceID
	current.WorkspaceName = workspaceName
	current.WorkspaceDir = workspaceDir
	return sm.Rebuild(ctx, cfg, current)
}

// buildServices constructs the complete service graph.
func (sm *ServiceManager) buildServices(ctx context.Context, cfg *config.Config, current Services) (*Services, error) {
	return sm.buildServicesWithOptions(ctx, cfg, current, serviceBuildOptions{})
}

func (sm *ServiceManager) buildServicesWithOptions(ctx context.Context, cfg *config.Config, current Services, opts serviceBuildOptions) (*Services, error) {
	// Preserve settings service reference if already created
	settingsSvc := current.Settings
	if settingsSvc == nil {
		settingsSvc = NewSettingsService(sm.transacter, config.OSKeychainStore{}, sm)
	}

	// 1. Create bus (shared singleton — passed to services, orchestrators, adapters)
	bus := event.NewBus(event.BusConfig{EventRepo: sm.eventRepo}, event.WithDropHandler(
		func(subscriberID string, evt domain.SystemEvent) {
			slog.Warn("event dropped: slow subscriber",
				"subscriber", subscriberID,
				"event_type", evt.EventType,
				"workspace_id", evt.WorkspaceID,
			)
		},
	))

	// 2. Create hook registry for pre-checkout validation
	hookRegistry := worktree.NewHookRegistry()

	// 2. Create services with shared bus
	workItemSvc := service.NewSessionService(sm.transacter, bus)
	workspaceSvc := service.NewWorkspaceService(sm.transacter, bus)
	sessionSvc := service.NewAgentSessionService(sm.transacter, bus)
	continuationSvc := service.NewAgentSessionContinuationService(sm.transacter)
	planSvc := service.NewPlanService(sm.transacter, bus)
	questionSvc := service.NewQuestionService(sm.transacter, bus)
	instanceSvc := service.NewInstanceService(sm.transacter)
	reviewSvc := service.NewReviewService(sm.transacter, bus)
	eventSvc := service.NewEventService(sm.transacter)
	ghPRSvc := service.NewGithubPRService(sm.transacter)
	glMRSvc := service.NewGitlabMRService(sm.transacter)
	sessionArtifactSvc := service.NewSessionReviewArtifactService(sm.transacter)
	ghPRReviewSvc := service.NewGithubPRReviewService(sm.transacter)
	glMRReviewSvc := service.NewGitlabMRReviewService(sm.transacter)
	ghPRCheckSvc := service.NewGithubPRCheckService(sm.transacter)
	glMRCheckSvc := service.NewGitlabMRCheckService(sm.transacter)
	newSessionFilterSvc := service.NewSessionFilterService(sm.transacter)
	newSessionFilterLockSvc := service.NewSessionFilterLockService(sm.transacter)

	gitClient := current.GitClient
	if gitClient == nil {
		gitClient = gitwork.NewClient("")
	}

	repos := adapter.ReviewArtifactRepos{
		Events:           eventSvc,
		GithubPRs:        ghPRSvc,
		GitlabMRs:        glMRSvc,
		SessionArtifacts: sessionArtifactSvc,
		Sessions:         workItemSvc,
		GithubPRReviews:  ghPRReviewSvc,
		GitlabMRReviews:  glMRReviewSvc,
		GithubPRChecks:   ghPRCheckSvc,
		GitlabMRChecks:   glMRCheckSvc,
		Bus:              bus,
	}
	var githubAdapter *githubadapter.GithubAdapter
	var githubWarning string
	if !opts.deferIntegrations {
		github, warning := app.BuildGithubAdapter(ctx, cfg, repos)
		githubAdapter = github
		githubWarning = warning
	}

	var adapters []adapter.WorkItemAdapter
	var adapterWarnings []string
	if current.WorkspaceID != "" {
		if opts.deferIntegrations {
			adapters = app.BuildManualWorkItemAdapters(current.WorkspaceID, workItemSvc)
		} else {
			adapters, adapterWarnings = app.BuildWorkItemAdapters(cfg, current.WorkspaceID, workItemSvc, githubAdapter)
		}
	}
	if githubWarning != "" {
		adapterWarnings = append(adapterWarnings, githubWarning)
	}
	var repoLifecycleAdapters []adapter.RepoLifecycleAdapter
	var repoSources []adapter.RepoSource
	if !opts.deferIntegrations {
		repoLifecycleAdapters = app.BuildRepoLifecycleAdapters(ctx, cfg, current.WorkspaceDir, repos, githubAdapter)
		repoSources = app.BuildRepoSources(ctx, cfg)
	}

	// Wire adapters to bus
	for _, workItemAdapter := range adapters {
		sub, subErr := bus.Subscribe("work-item-adapter:"+workItemAdapter.Name(), string(domain.EventPlanApproved), string(domain.EventWorkItemCompleted), string(domain.EventPRMerged))
		if subErr != nil {
			return nil, fmt.Errorf("subscribe work item adapter %s: %w", workItemAdapter.Name(), subErr)
		}
		go wireAdapterToBus(workItemAdapter, sub.C, bus)
	}
	for _, lifecycleAdapter := range repoLifecycleAdapters {
		sub, subErr := bus.Subscribe("repo-lifecycle-adapter:"+lifecycleAdapter.Name(), string(domain.EventWorktreeCreated), string(domain.EventWorktreeReused), string(domain.EventWorkItemCompleted), string(domain.EventSubPlanPRReady), string(domain.EventPRMerged), string(domain.EventPlanApproved))
		if subErr != nil {
			return nil, fmt.Errorf("subscribe repo lifecycle adapter %s: %w", lifecycleAdapter.Name(), subErr)
		}
		go wireAdapterToBus(lifecycleAdapter, sub.C, bus)
	}

	// Start PR/MR state refresh for lifecycle adapters.
	// Collect stop functions to cancel the refresh loops on next Rebuild.
	var refreshStoppers []func()
	for _, la := range repoLifecycleAdapters {
		type prRefresher interface {
			StartPRRefresh(ctx context.Context, workspaceID string) func()
		}
		type mrRefresher interface {
			StartMRRefresh(ctx context.Context, workspaceID string) func()
		}
		if r, ok := la.(prRefresher); ok && current.WorkspaceID != "" {
			stop := r.StartPRRefresh(ctx, current.WorkspaceID)
			if stop != nil {
				refreshStoppers = append(refreshStoppers, stop)
			}
		}
		slog.Debug("service_manager: mrRefresher check",
			"adapter_name", la.Name(),
			"workspace_id", current.WorkspaceID,
		)
		if r, ok := la.(mrRefresher); ok && current.WorkspaceID != "" {
			stop := r.StartMRRefresh(ctx, current.WorkspaceID)
			if stop != nil {
				refreshStoppers = append(refreshStoppers, stop)
			}
		}
	}

	// Start GitLab Work Item status refresh for work item adapters.
	for _, workItemAdapter := range adapters {
		type statusRefresher interface {
			StartStatusRefresh(ctx context.Context, workspaceID string) func()
		}
		if r, ok := workItemAdapter.(statusRefresher); ok && current.WorkspaceID != "" {
			r.StartStatusRefresh(ctx, current.WorkspaceID)
		}
	}

	// Build orchestrators
	discoverer := orchestrator.NewDiscoverer(gitClient, cfg)
	harnesses, err := app.BuildAgentHarnesses(cfg, current.WorkspaceDir)
	if err != nil {
		return nil, fmt.Errorf("building agent harnesses: %w", err)
	}

	planningCfg := orchestrator.PlanningConfigFromConfig(cfg)
	registry := orchestrator.NewSessionRegistry()

	var planningSvc *orchestrator.PlanningService
	if harnesses.Planning != nil {
		planningSvc, err = orchestrator.NewPlanningService(planningCfg, discoverer, gitClient, harnesses.Planning, planSvc, workItemSvc, sessionSvc, bus, workspaceSvc, registry, questionSvc, cfg)
		if err != nil {
			return nil, fmt.Errorf("build planning service: %w", err)
		}
	}

	var reviewPipeline *orchestrator.ReviewPipeline
	if harnesses.Review != nil {
		reviewPipeline = orchestrator.NewReviewPipeline(cfg, harnesses.Review, reviewSvc, sessionSvc, planSvc, workItemSvc, bus, registry)
	}

	var implSvc *orchestrator.ImplementationService
	if harnesses.Implementation != nil {
		implSvc = orchestrator.NewImplementationService(cfg, harnesses.Implementation, gitClient, bus, planSvc, workItemSvc, sessionSvc, continuationSvc, workspaceSvc, registry, reviewPipeline, harnesses.Foreman, questionSvc, reviewSvc, hookRegistry)
		implSvc.SetPlanningService(planningSvc)
	}
	if implSvc != nil && current.WorkspaceID != "" && serviceManagerHasContinuationRepository(sm.transacter) {
		recoveryCtx := context.WithoutCancel(ctx)
		go func(workspaceID string) {
			result, recoverErr := implSvc.RecoverContinuationsForWorkspace(recoveryCtx, workspaceID)
			if recoverErr != nil {
				slog.Error("recover implementation continuations failed", "workspace_id", workspaceID, "error", recoverErr)
				return
			}
			if result.Recovered > 0 || len(result.Skipped) > 0 {
				slog.Debug("implementation continuation recovery completed", "workspace_id", workspaceID, "recovered", result.Recovered, "skipped", len(result.Skipped))
			}
		}(current.WorkspaceID)
	}

	// Build QuestionRouter for stage-aware question routing.
	// Foreman is looked up dynamically per question via registry.
	questionRouter := orchestrator.NewQuestionRouter(questionSvc, sessionSvc, registry, bus)

	// Build AnswerRouter for stateless question routing
	var answerRouter orchestrator.AnswerRouter
	if harnesses.Foreman != nil || harnesses.Implementation != nil || harnesses.Planning != nil {
		answerRouter = orchestrator.NewAnswerRouter(registry, questionSvc, sessionSvc, bus)
	}

	// Build ReviewFollowup for foreman lifecycle during follow-up sessions
	var reviewFollowup *orchestrator.ReviewFollowup
	if harnesses.Foreman != nil {
		reviewFollowup = orchestrator.NewReviewFollowup(cfg, harnesses.Foreman, registry, planSvc, questionSvc, sessionSvc, workItemSvc, bus)
	}

	// Build ManualSessionService with default agent harness
	var manualSvc *orchestrator.ManualSessionService
	var defaultHarness adapter.AgentHarness
	if harnesses.Planning != nil {
		defaultHarness = harnesses.Planning
	} else if harnesses.Implementation != nil {
		defaultHarness = harnesses.Implementation
	} else if harnesses.Review != nil {
		defaultHarness = harnesses.Review
	} else if harnesses.Foreman != nil {
		defaultHarness = harnesses.Foreman
	} else if harnesses.Resume != nil {
		defaultHarness = harnesses.Resume
	}
	if defaultHarness != nil {
		manualSvc = orchestrator.NewManualSessionService(cfg, defaultHarness, gitClient, sessionSvc, workItemSvc, workspaceSvc, registry, questionRouter, bus)
	}

	var reviewCommentDispatcher *adapter.ReviewCommentDispatcher
	if opts.deferIntegrations {
		reviewCommentDispatcher = adapter.NewReviewCommentDispatcher(nil)
	} else {
		reviewCommentDispatcher = app.BuildReviewCommentFetcher(cfg, current.WorkspaceDir, githubAdapter)
	}

	return &Services{
		Settings:              settingsSvc,
		RefreshStoppers:       refreshStoppers,
		Session:               workItemSvc,
		Continuation:          continuationSvc,
		Plan:                  planSvc,
		Task:                  sessionSvc,
		Question:              questionSvc,
		Instance:              instanceSvc,
		Workspace:             workspaceSvc,
		Review:                reviewSvc,
		Events:                eventSvc,
		GithubPRs:             ghPRSvc,
		GitlabMRs:             glMRSvc,
		SessionArtifacts:      sessionArtifactSvc,
		GithubPRReviews:       ghPRReviewSvc,
		GitlabMRReviews:       glMRReviewSvc,
		GithubPRChecks:        ghPRCheckSvc,
		GitlabMRChecks:        glMRCheckSvc,
		NewSessionFilters:     newSessionFilterSvc,
		NewSessionFilterLocks: newSessionFilterLockSvc,
		Planning:              planningSvc,
		Implementation:        implSvc,
		ReviewPipeline:        reviewPipeline,
		AnswerRouter:          answerRouter,
		ReviewFollowup:        reviewFollowup,
		SessionRegistry:       registry,
		QuestionRouter:        questionRouter,
		Manual:                manualSvc,

		Cfg:             cfg,
		Adapters:        adapters,
		RepoSources:     repoSources,
		Harnesses:       harnesses,
		GitClient:       gitClient,
		Bus:             bus,
		ReviewComments:  reviewCommentDispatcher,
		StartupWarnings: adapterWarnings,
		InstanceID:      current.InstanceID,
		WorkspaceID:     current.WorkspaceID,
		WorkspaceDir:    current.WorkspaceDir,
		WorkspaceName:   current.WorkspaceName,
	}, nil
}

func serviceManagerHasContinuationRepository(transacter atomic.Transacter[repository.Resources]) bool {
	if noop, ok := transacter.(repository.NoopTransacter); ok {
		return noop.Res.AgentSessionContinuations != nil
	}
	return true
}

// wireAdapterToBus bridges an adapter to the event bus with retry logic.
// Errors are published to the bus as EventAdapterError events for the TUI to consume.
func wireAdapterToBus[T any](adapterInstance T, events <-chan domain.SystemEvent, bus *event.Bus) {
	switch a := any(adapterInstance).(type) {
	case adapter.WorkItemAdapter:
		for evt := range events {
			var lastErr error
			for attempt := range 3 {
				if err := a.OnEvent(context.Background(), evt); err != nil {
					lastErr = err
					if attempt < 2 {
						if errors.As(lastErr, new(*adapter.PermissionError)) {
							break
						}
						time.Sleep(time.Duration(attempt+1) * time.Second)
					}
					continue
				}
				lastErr = nil
				break
			}
			if lastErr != nil {
				errPayload := fmt.Sprintf(`{"adapter":%q,"event_type":%q,"error":%q}`, a.Name(), evt.EventType, lastErr.Error())
				if pubErr := bus.Publish(context.Background(), domain.SystemEvent{
					ID:          domain.NewID(),
					EventType:   string(domain.EventAdapterError),
					WorkspaceID: evt.WorkspaceID,
					Payload:     errPayload,
					CreatedAt:   time.Now(),
				}); pubErr != nil {
					slog.Warn("failed to publish adapter error event", "error", pubErr)
				}
			}
		}
	case adapter.RepoLifecycleAdapter:
		for evt := range events {
			var lastErr error
			for attempt := range 3 {
				if err := a.OnEvent(context.Background(), evt); err != nil {
					lastErr = err
					if attempt < 2 {
						if errors.As(lastErr, new(*adapter.PermissionError)) {
							break
						}
						time.Sleep(time.Duration(attempt+1) * time.Second)
					}
					continue
				}
				lastErr = nil
				break
			}
			if lastErr != nil {
				errPayload := fmt.Sprintf(`{"adapter":%q,"event_type":%q,"error":%q}`, a.Name(), evt.EventType, lastErr.Error())
				if pubErr := bus.Publish(context.Background(), domain.SystemEvent{
					ID:          domain.NewID(),
					EventType:   string(domain.EventAdapterError),
					WorkspaceID: evt.WorkspaceID,
					Payload:     errPayload,
					CreatedAt:   time.Now(),
				}); pubErr != nil {
					slog.Warn("failed to publish adapter error event", "error", pubErr)
				}
			}
		}
	}
}

// ServiceProvider interface implementation — each method delegates to GetServices().

func (sm *ServiceManager) Session() *service.SessionService {
	if s := sm.GetServices(); s != nil {
		return s.Session
	}
	return nil
}

func (sm *ServiceManager) Plan() *service.PlanService {
	if s := sm.GetServices(); s != nil {
		return s.Plan
	}
	return nil
}

func (sm *ServiceManager) Task() *service.AgentSessionService {
	if s := sm.GetServices(); s != nil {
		return s.Task
	}
	return nil
}

func (sm *ServiceManager) Continuation() *service.AgentSessionContinuationService {
	if s := sm.GetServices(); s != nil {
		return s.Continuation
	}
	return nil
}

func (sm *ServiceManager) Question() *service.QuestionService {
	if s := sm.GetServices(); s != nil {
		return s.Question
	}
	return nil
}

func (sm *ServiceManager) Instance() *service.InstanceService {
	if s := sm.GetServices(); s != nil {
		return s.Instance
	}
	return nil
}

func (sm *ServiceManager) Workspace() *service.WorkspaceService {
	if s := sm.GetServices(); s != nil {
		return s.Workspace
	}
	return nil
}

func (sm *ServiceManager) Review() *service.ReviewService {
	if s := sm.GetServices(); s != nil {
		return s.Review
	}
	return nil
}

func (sm *ServiceManager) Events() *service.EventService {
	if s := sm.GetServices(); s != nil {
		return s.Events
	}
	return nil
}

func (sm *ServiceManager) GithubPRs() *service.GithubPRService {
	if s := sm.GetServices(); s != nil {
		return s.GithubPRs
	}
	return nil
}

func (sm *ServiceManager) GitlabMRs() *service.GitlabMRService {
	if s := sm.GetServices(); s != nil {
		return s.GitlabMRs
	}
	return nil
}

func (sm *ServiceManager) SessionArtifacts() *service.SessionReviewArtifactService {
	if s := sm.GetServices(); s != nil {
		return s.SessionArtifacts
	}
	return nil
}

func (sm *ServiceManager) GithubPRReviews() *service.GithubPRReviewService {
	if s := sm.GetServices(); s != nil {
		return s.GithubPRReviews
	}
	return nil
}

func (sm *ServiceManager) GitlabMRReviews() *service.GitlabMRReviewService {
	if s := sm.GetServices(); s != nil {
		return s.GitlabMRReviews
	}
	return nil
}

func (sm *ServiceManager) GithubPRChecks() *service.GithubPRCheckService {
	if s := sm.GetServices(); s != nil {
		return s.GithubPRChecks
	}
	return nil
}

func (sm *ServiceManager) GitlabMRChecks() *service.GitlabMRCheckService {
	if s := sm.GetServices(); s != nil {
		return s.GitlabMRChecks
	}
	return nil
}

func (sm *ServiceManager) NewSessionFilters() *service.SessionFilterService {
	if s := sm.GetServices(); s != nil {
		return s.NewSessionFilters
	}
	return nil
}

func (sm *ServiceManager) NewSessionFilterLocks() *service.SessionFilterLockService {
	if s := sm.GetServices(); s != nil {
		return s.NewSessionFilterLocks
	}
	return nil
}

func (sm *ServiceManager) Settings() SettingsService {
	if s := sm.GetServices(); s != nil {
		return s.Settings
	}
	return nil
}

func (sm *ServiceManager) Planning() *orchestrator.PlanningService {
	if s := sm.GetServices(); s != nil {
		return s.Planning
	}
	return nil
}

func (sm *ServiceManager) Implementation() *orchestrator.ImplementationService {
	if s := sm.GetServices(); s != nil {
		return s.Implementation
	}
	return nil
}

func (sm *ServiceManager) ReviewPipeline() *orchestrator.ReviewPipeline {
	if s := sm.GetServices(); s != nil {
		return s.ReviewPipeline
	}
	return nil
}

func (sm *ServiceManager) AnswerRouter() orchestrator.AnswerRouter {
	if s := sm.GetServices(); s != nil {
		return s.AnswerRouter
	}
	return nil
}

func (sm *ServiceManager) ReviewFollowup() *orchestrator.ReviewFollowup {
	if s := sm.GetServices(); s != nil {
		return s.ReviewFollowup
	}
	return nil
}

func (sm *ServiceManager) SessionRegistry() orchestrator.SessionRegistry {
	if s := sm.GetServices(); s != nil {
		return s.SessionRegistry
	}
	return nil
}

func (sm *ServiceManager) Bus() *event.Bus {
	if s := sm.GetServices(); s != nil {
		return s.Bus
	}
	return nil
}

func (sm *ServiceManager) GitClient() *gitwork.Client {
	if s := sm.GetServices(); s != nil {
		return s.GitClient
	}
	return nil
}

func (sm *ServiceManager) Adapters() []adapter.WorkItemAdapter {
	if s := sm.GetServices(); s != nil {
		return s.Adapters
	}
	return nil
}

func (sm *ServiceManager) RepoSources() []adapter.RepoSource {
	if s := sm.GetServices(); s != nil {
		return s.RepoSources
	}
	return nil
}

func (sm *ServiceManager) Harnesses() app.AgentHarnesses {
	if s := sm.GetServices(); s != nil {
		return s.Harnesses
	}
	return app.AgentHarnesses{}
}

func (sm *ServiceManager) ReviewComments() *adapter.ReviewCommentDispatcher {
	if s := sm.GetServices(); s != nil {
		return s.ReviewComments
	}
	return nil
}

func (sm *ServiceManager) StartupWarnings() []string {
	if s := sm.GetServices(); s != nil {
		return s.StartupWarnings
	}
	return nil
}

func (sm *ServiceManager) Manual() *orchestrator.ManualSessionService {
	if s := sm.GetServices(); s != nil {
		return s.Manual
	}
	return nil
}

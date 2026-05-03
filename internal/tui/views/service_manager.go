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
	sm.mu.Lock()
	defer sm.mu.Unlock()
	svcs, err := sm.buildServices(ctx, cfg, Services{})
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

	svcs, err := sm.buildServices(ctx, cfg, current)
	if err != nil {
		return nil, err
	}

	// Tear down old services only after new ones are built successfully.
	if sm.services != nil && sm.services.Bus != nil {
		sm.services.Bus.Close()
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

// Close shuts down the service graph.
func (sm *ServiceManager) Close() {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.services != nil && sm.services.Bus != nil {
		sm.services.Bus.Close()
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

	// 1. Create hook registry for pre-checkout validation
	hookRegistry := worktree.NewHookRegistry()

	// 2. Create services with shared bus
	workItemSvc := service.NewSessionService(sm.transacter, bus)
	workspaceSvc := service.NewWorkspaceService(sm.transacter)
	sessionSvc := service.NewTaskService(sm.transacter, bus)
	planSvc := service.NewPlanService(sm.transacter, bus)
	questionSvc := service.NewQuestionService(sm.transacter)
	instanceSvc := service.NewInstanceService(sm.transacter)
	reviewSvc := service.NewReviewService(sm.transacter)
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
	githubAdapter, githubWarning := app.BuildGithubAdapter(ctx, cfg, repos)

	var adapters []adapter.WorkItemAdapter
	var adapterWarnings []string
	if current.WorkspaceID != "" {
		adapters, adapterWarnings = app.BuildWorkItemAdapters(cfg, current.WorkspaceID, workItemSvc, githubAdapter)
	}
	if githubWarning != "" {
		adapterWarnings = append(adapterWarnings, githubWarning)
	}
	repoLifecycleAdapters := app.BuildRepoLifecycleAdapters(ctx, cfg, current.WorkspaceDir, repos, githubAdapter)

	// Wire adapters to bus
	for _, workItemAdapter := range adapters {
		sub, subErr := bus.Subscribe("work-item-adapter:"+workItemAdapter.Name(), string(domain.EventPlanApproved), string(domain.EventWorkItemCompleted), string(domain.EventPRMerged))
		if subErr != nil {
			return nil, fmt.Errorf("subscribe work item adapter %s: %w", workItemAdapter.Name(), subErr)
		}
		go wireAdapterToBus(workItemAdapter, sub.C, bus)
	}
	for _, lifecycleAdapter := range repoLifecycleAdapters {
		sub, subErr := bus.Subscribe("repo-lifecycle-adapter:"+lifecycleAdapter.Name(), string(domain.EventWorktreeCreated), string(domain.EventWorktreeReused), string(domain.EventWorkItemCompleted), string(domain.EventPRMerged), string(domain.EventPlanApproved))
		if subErr != nil {
			return nil, fmt.Errorf("subscribe repo lifecycle adapter %s: %w", lifecycleAdapter.Name(), subErr)
		}
		go wireAdapterToBus(lifecycleAdapter, sub.C, bus)
	}

	// Start PR/MR state refresh for lifecycle adapters.
	for _, la := range repoLifecycleAdapters {
		type prRefresher interface {
			StartPRRefresh(ctx context.Context, workspaceID string)
		}
		type mrRefresher interface {
			StartMRRefresh(ctx context.Context, workspaceID string)
		}
		if r, ok := la.(prRefresher); ok && current.WorkspaceID != "" {
			r.StartPRRefresh(ctx, current.WorkspaceID)
		}
		if r, ok := la.(mrRefresher); ok && current.WorkspaceID != "" {
			r.StartMRRefresh(ctx, current.WorkspaceID)
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

	var foreman *orchestrator.Foreman
	if harnesses.Foreman != nil {
		foreman = orchestrator.NewForeman(cfg, harnesses.Foreman, planSvc, questionSvc, sessionSvc, bus)
	}

	var implSvc *orchestrator.ImplementationService
	if harnesses.Implementation != nil {
		implSvc = orchestrator.NewImplementationService(cfg, harnesses.Implementation, gitClient, bus, planSvc, workItemSvc, sessionSvc, workspaceSvc, registry, reviewPipeline, foreman, questionSvc, reviewSvc, hookRegistry)
	}

	var resumption *orchestrator.Resumption
	if harnesses.Resume != nil {
		resumption = orchestrator.NewResumption(harnesses.Resume, sessionSvc, planSvc, bus, registry)
	}

	reviewCommentDispatcher := app.BuildReviewCommentFetcher(cfg, current.WorkspaceDir, githubAdapter)

	return &Services{
		Session:               workItemSvc,
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
		Resumption:            resumption,
		Foreman:               foreman,
		SessionRegistry:       registry,
		Cfg:                   cfg,
		Adapters:              adapters,
		Harnesses:             harnesses,
		GitClient:             gitClient,
		Bus:                   bus,
		ReviewComments:        reviewCommentDispatcher,
		StartupWarnings:       adapterWarnings,
		InstanceID:            current.InstanceID,
		WorkspaceID:           current.WorkspaceID,
		WorkspaceDir:          current.WorkspaceDir,
		WorkspaceName:         current.WorkspaceName,
	}, nil
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

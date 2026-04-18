// Package main is the entry point for the Substrate CLI.
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"os"
	"strings"
	"time"

	atomic "github.com/beeemT/go-atomic"
	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/app"
	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/event"
	"github.com/beeemT/substrate/internal/gitwork"
	"github.com/beeemT/substrate/internal/orchestrator"
	"github.com/beeemT/substrate/internal/repository"
	"github.com/beeemT/substrate/internal/repository/sqlite"
	"github.com/beeemT/substrate/internal/service"
	"github.com/beeemT/substrate/internal/tui/views"
	"github.com/beeemT/substrate/internal/tuilog"
	"github.com/beeemT/substrate/migrations"
)

func printUsage() {
	fmt.Println("substrate - AI-powered work item orchestration")
	fmt.Println("")
	fmt.Println("Usage:")
	fmt.Println("  substrate           Start the TUI")
	fmt.Println("  substrate --help    Show help")
	fmt.Println("  substrate --version Show version")
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

var Version = "dev"

type workspaceContext struct {
	ID   string
	Name string
	Dir  string
}

type coreServices struct {
	workItem             *service.SessionService
	plan                 *service.PlanService
	workspace            *service.WorkspaceService
	task                 *service.TaskService
	question             *service.QuestionService
	instance             *service.InstanceService
	review               *service.ReviewService
	event                *service.EventService
	githubPR             *service.GithubPRService
	gitlabMR             *service.GitlabMRService
	sessionArtifact      *service.SessionReviewArtifactService
	ghPRReview           *service.GithubPRReviewService
	glMRReview           *service.GitlabMRReviewService
	ghPRCheck            *service.GithubPRCheckService
	glMRCheck            *service.GitlabMRCheckService
	newSessionFilter     *service.SessionFilterService
	newSessionFilterLock *service.SessionFilterLockService
	settings             *views.SettingsService
}

type adapterSetup struct {
	workItem      []adapter.WorkItemAdapter
	repoLifecycle []adapter.RepoLifecycleAdapter
	repoSources   []adapter.RepoSource
	warnings      []string
	adapterErrors chan views.AdapterErrorMsg
}

type orchestrationRuntime struct {
	gitClient      *gitwork.Client
	harnesses      app.AgentHarnesses
	planning       *orchestrator.PlanningService
	reviewPipeline *orchestrator.ReviewPipeline
	foreman        *orchestrator.Foreman
	implementation *orchestrator.ImplementationService
	resumption     *orchestrator.Resumption
	registry       *orchestrator.SessionRegistry
}

func run() error {
	if handleCLIArgs(os.Args[1:]) {
		return nil
	}

	logStore, logToasts := initTUILogging()

	cfg, err := loadRuntimeConfig()
	if err != nil {
		return err
	}

	ctx := context.Background()
	db, err := openDatabase(ctx)
	if err != nil {
		return err
	}
	defer db.Close()

	remote := dbRemote{db}
	eventRepo := sqlite.NewEventRepo(remote)
	transacter := sqlite.NewTransacter(db)
	services := buildCoreServices(transacter, eventRepo)

	bus := event.NewBus(event.BusConfig{EventRepo: eventRepo})
	workspace, err := detectWorkspace(ctx, services.workspace)
	if err != nil {
		return err
	}

	instanceID := registerInstance(ctx, services.instance, workspace.ID)

	if err := config.LoadSecrets(cfg, config.OSKeychainStore{}); err != nil {
		return fmt.Errorf("load config secrets: %w", err)
	}

	adapters, err := buildAdapterSetup(ctx, cfg, workspace, services, bus)
	if err != nil {
		return err
	}

	runtime, err := buildOrchestrationRuntime(cfg, workspace.Dir, services, bus)
	if err != nil {
		return err
	}

	settingsData, err := services.settings.Snapshot(cfg)
	if err != nil {
		return fmt.Errorf("load settings snapshot: %w", err)
	}

	return views.RunTUI(views.Services{
		Session:               services.workItem,
		Plan:                  services.plan,
		Task:                  services.task,
		Question:              services.question,
		Instance:              services.instance,
		Workspace:             services.workspace,
		Review:                services.review,
		Events:                services.event,
		GithubPRs:             services.githubPR,
		GitlabMRs:             services.gitlabMR,
		SessionArtifacts:      services.sessionArtifact,
		GithubPRReviews:       services.ghPRReview,
		GitlabMRReviews:       services.glMRReview,
		GithubPRChecks:        services.ghPRCheck,
		GitlabMRChecks:        services.glMRCheck,
		NewSessionFilters:     services.newSessionFilter,
		NewSessionFilterLocks: services.newSessionFilterLock,
		Cfg:                   cfg,
		Adapters:              adapters.workItem,
		RepoSources:           adapters.repoSources,
		Harnesses:             runtime.harnesses,
		Settings:              services.settings,
		SettingsData:          settingsData,
		GitClient:             runtime.gitClient,
		Bus:                   bus,
		AdapterErrors:         adapters.adapterErrors,
		StartupWarnings:       adapters.warnings,
		LogStore:              logStore,
		LogToasts:             logToasts,
		InstanceID:            instanceID,
		WorkspaceID:           workspace.ID,
		WorkspaceName:         workspace.Name,
		WorkspaceDir:          workspace.Dir,
		Planning:              runtime.planning,
		Implementation:        runtime.implementation,
		ReviewPipeline:        runtime.reviewPipeline,
		Resumption:            runtime.resumption,
		Foreman:               runtime.foreman,
		SessionRegistry:       runtime.registry,
	})
}

func handleCLIArgs(args []string) bool {
	if len(args) == 0 {
		return false
	}

	switch args[0] {
	case "--help", "-h", "help":
		printUsage()
		return true
	case "--version", "-v", "version":
		fmt.Println(Version)
		return true
	default:
		return false
	}
}

func initTUILogging() (*tuilog.Store, chan tuilog.ToastEntry) {
	// Install a TUI-aware slog handler that captures log entries into an
	// in-memory buffer and sends warn/error entries to a channel for toast
	// display. This replaces the default text handler, which would write to
	// stderr and corrupt the bubbletea rendering.
	logStore := tuilog.NewStore()
	logToasts := make(chan tuilog.ToastEntry, 64)
	slog.SetDefault(slog.New(tuilog.NewHandler(logStore, logToasts)))

	return logStore, logToasts
}

func loadRuntimeConfig() (*config.Config, error) {
	globalDir, err := config.GlobalDir()
	if err != nil {
		return nil, fmt.Errorf("getting global directory: %w", err)
	}

	if err := os.MkdirAll(globalDir, 0o750); err != nil {
		return nil, fmt.Errorf("creating global directory: %w", err)
	}

	cfgPath, err := config.ConfigPath()
	if err != nil {
		return nil, fmt.Errorf("getting config path: %w", err)
	}

	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		if err := initializeGlobalConfig(cfgPath); err != nil {
			return nil, fmt.Errorf("initializing global config: %w", err)
		}
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}

	if err := ensureSessionsDir(); err != nil {
		return nil, err
	}

	return cfg, nil
}

func ensureSessionsDir() error {
	// Ensure sessions directory exists. The TUI tails logs from this location at runtime.
	sessionsDir, err := config.SessionsDir()
	if err != nil {
		return fmt.Errorf("getting sessions directory: %w", err)
	}
	if err := os.MkdirAll(sessionsDir, 0o750); err != nil {
		return fmt.Errorf("creating sessions directory: %w", err)
	}

	return nil
}

func openDatabase(ctx context.Context) (*sqlx.DB, error) {
	dbPath, err := config.GlobalDBPath()
	if err != nil {
		return nil, fmt.Errorf("getting database path: %w", err)
	}

	db, err := sqlx.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA foreign_keys=ON",
		"PRAGMA busy_timeout=5000",
	} {
		if _, err := db.ExecContext(ctx, pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("set pragma: %w", err)
		}
	}

	if err := repository.Migrate(ctx, db, migrations.FS); err != nil {
		db.Close()
		return nil, fmt.Errorf("running migrations: %w", err)
	}

	// Harden database file permissions — the file may have been created with
	// the process umask (often 0644); restrict to owner-only access.
	// Must run after migrations so the file exists on first launch.
	if err := os.Chmod(dbPath, 0o600); err != nil {
		slog.Warn("failed to set database permissions", "path", dbPath, "err", err)
	}

	return db, nil
}

func buildCoreServices(
	transacter atomic.Transacter[repository.Resources],
	eventRepo repository.EventRepository,
) coreServices {
	workItemSvc := service.NewSessionService(transacter)
	planSvc := service.NewPlanService(transacter)
	workspaceSvc := service.NewWorkspaceService(transacter)
	taskSvc := service.NewTaskService(transacter)
	questionSvc := service.NewQuestionService(transacter)
	instanceSvc := service.NewInstanceService(transacter)
	reviewSvc := service.NewReviewService(transacter)
	eventSvc := service.NewEventService(transacter)
	ghPRSvc := service.NewGithubPRService(transacter)
	glMRSvc := service.NewGitlabMRService(transacter)
	sessionArtifactSvc := service.NewSessionReviewArtifactService(transacter)
	ghPRReviewSvc := service.NewGithubPRReviewService(transacter)
	glMRReviewSvc := service.NewGitlabMRReviewService(transacter)
	ghPRCheckSvc := service.NewGithubPRCheckService(transacter)
	glMRCheckSvc := service.NewGitlabMRCheckService(transacter)
	newSessionFilterSvc := service.NewSessionFilterService(transacter)
	newSessionFilterLockSvc := service.NewSessionFilterLockService(transacter)

	return coreServices{
		workItem:             workItemSvc,
		plan:                 planSvc,
		workspace:            workspaceSvc,
		task:                 taskSvc,
		question:             questionSvc,
		instance:             instanceSvc,
		review:               reviewSvc,
		event:                eventSvc,
		githubPR:             ghPRSvc,
		gitlabMR:             glMRSvc,
		sessionArtifact:      sessionArtifactSvc,
		ghPRReview:           ghPRReviewSvc,
		glMRReview:           glMRReviewSvc,
		ghPRCheck:            ghPRCheckSvc,
		glMRCheck:            glMRCheckSvc,
		newSessionFilter:     newSessionFilterSvc,
		newSessionFilterLock: newSessionFilterLockSvc,
		settings: views.NewSettingsService(
			transacter, planSvc, eventRepo, config.OSKeychainStore{},
		),
	}
}

func detectWorkspace(ctx context.Context, workspaceSvc *service.WorkspaceService) (workspaceContext, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return workspaceContext{}, fmt.Errorf("getting working directory: %w", err)
	}

	wsDir, wsFile, wsErr := gitwork.FindWorkspace(cwd)
	if wsErr != nil {
		if gitwork.IsNotInWorkspace(wsErr) {
			return workspaceContext{}, nil
		}
		return workspaceContext{}, fmt.Errorf("detecting workspace: %w", wsErr)
	}

	workspace := workspaceContext{
		Dir:  wsDir,
		Name: wsFile.Name,
	}

	ws, err := workspaceSvc.Get(ctx, wsFile.ID)
	if err != nil {
		slog.Warn("workspace file found but not in DB; will prompt init", "id", wsFile.ID, "err", err)
		return workspace, nil
	}

	workspace.ID = ws.ID
	workspace.Name = ws.Name

	return workspace, nil
}

func registerInstance(ctx context.Context, instanceSvc *service.InstanceService, workspaceID string) string {
	if workspaceID == "" {
		return ""
	}

	host, _ := os.Hostname()
	inst := domain.SubstrateInstance{
		ID:          domain.NewID(),
		WorkspaceID: workspaceID,
		PID:         os.Getpid(),
		Hostname:    host,
	}

	if err := instanceSvc.Create(ctx, inst); err != nil {
		slog.Warn("failed to register instance", "err", err)
		return ""
	}

	return inst.ID
}

func buildAdapterSetup(
	ctx context.Context,
	cfg *config.Config,
	workspace workspaceContext,
	services coreServices,
	bus *event.Bus,
) (adapterSetup, error) {
	var workItemAdapters []adapter.WorkItemAdapter
	var adapterWarnings []string
	if workspace.ID != "" {
		workItemAdapters, adapterWarnings = app.BuildWorkItemAdapters(cfg, workspace.ID, services.workItem)
	}

	repoLifecycleAdapters := app.BuildRepoLifecycleAdapters(
		ctx,
		cfg,
		workspace.Dir,
		adapter.ReviewArtifactRepos{
			Events:           services.event,
			GithubPRs:        services.githubPR,
			GitlabMRs:        services.gitlabMR,
			SessionArtifacts: services.sessionArtifact,
			Sessions:         services.workItem,
			GithubPRReviews:  services.ghPRReview,
			GitlabMRReviews:  services.glMRReview,
			GithubPRChecks:   services.ghPRCheck,
			GitlabMRChecks:   services.glMRCheck,
			Bus:              bus,
		},
	)
	repoSources := app.BuildRepoSources(ctx, cfg)

	adapterErrors := make(chan views.AdapterErrorMsg, 16)

	if err := subscribeWorkItemAdapters(bus, workItemAdapters, adapterErrors); err != nil {
		return adapterSetup{}, err
	}
	if err := subscribeRepoLifecycleAdapters(bus, repoLifecycleAdapters, adapterErrors); err != nil {
		return adapterSetup{}, err
	}

	startRepoLifecycleRefresh(ctx, repoLifecycleAdapters, workspace.ID)

	return adapterSetup{
		workItem:      workItemAdapters,
		repoLifecycle: repoLifecycleAdapters,
		repoSources:   repoSources,
		warnings:      adapterWarnings,
		adapterErrors: adapterErrors,
	}, nil
}

func subscribeWorkItemAdapters(
	bus *event.Bus,
	adapters []adapter.WorkItemAdapter,
	adapterErrors chan<- views.AdapterErrorMsg,
) error {
	return subscribeAdapters(
		bus,
		"work-item-adapter",
		[]domain.EventType{domain.EventPlanApproved, domain.EventWorkItemCompleted, domain.EventPRMerged},
		adapters,
		adapterErrors,
	)
}

func subscribeRepoLifecycleAdapters(
	bus *event.Bus,
	adapters []adapter.RepoLifecycleAdapter,
	adapterErrors chan<- views.AdapterErrorMsg,
) error {
	return subscribeAdapters(
		bus,
		"repo-lifecycle-adapter",
		[]domain.EventType{
			domain.EventWorktreeCreated,
			domain.EventWorktreeReused,
			domain.EventWorkItemCompleted,
			domain.EventPRMerged,
		},
		adapters,
		adapterErrors,
	)
}

type eventDrivenAdapter interface {
	Name() string
	OnEvent(context.Context, domain.SystemEvent) error
}

func subscribeAdapters[T eventDrivenAdapter](
	bus *event.Bus,
	subscriberPrefix string,
	eventTypes []domain.EventType,
	adapters []T,
	adapterErrors chan<- views.AdapterErrorMsg,
) error {
	for _, candidate := range adapters {
		sub, err := bus.Subscribe(
			subscriberPrefix+":"+candidate.Name(),
			eventTypeStrings(eventTypes)...,
		)
		if err != nil {
			return fmt.Errorf("subscribe %s %s: %w", subscriberPrefix, candidate.Name(), err)
		}

		go runAdapterEventLoop(candidate, bus, sub.C, adapterErrors)
	}

	return nil
}

func eventTypeStrings(eventTypes []domain.EventType) []string {
	values := make([]string, 0, len(eventTypes))
	for _, eventType := range eventTypes {
		values = append(values, string(eventType))
	}

	return values
}

func runAdapterEventLoop[T eventDrivenAdapter](
	adapterInstance T,
	bus *event.Bus,
	events <-chan domain.SystemEvent,
	adapterErrors chan<- views.AdapterErrorMsg,
) {
	for evt := range events {
		if err := handleAdapterEvent(adapterInstance, evt); err != nil {
			slog.Warn(
				"adapter event failed after retries",
				"adapter", adapterInstance.Name(),
				"event", evt.EventType,
				"err", err,
				"retries", 2,
			)
			publishAdapterError(context.Background(), bus, adapterInstance.Name(), evt, err)
			reportAdapterError(adapterErrors, adapterInstance.Name(), evt.EventType, err)
		}
	}
}

func handleAdapterEvent[T eventDrivenAdapter](adapterInstance T, evt domain.SystemEvent) error {
	var lastErr error
	for attempt := range 3 {
		if err := adapterInstance.OnEvent(context.Background(), evt); err != nil {
			lastErr = err
			if attempt < 2 {
				// PermissionError is permanent; retrying will not help.
				if errors.As(lastErr, new(*adapter.PermissionError)) {
					break
				}
				time.Sleep(time.Duration(attempt+1) * time.Second)
			}
			continue
		}
		return nil
	}

	return lastErr
}

func publishAdapterError(ctx context.Context, bus *event.Bus, adapterName string, evt domain.SystemEvent, err error) {
	errPayload := fmt.Sprintf(`{"adapter":%q,"event_type":%q,"error":%q}`, adapterName, evt.EventType, err.Error())
	if pubErr := bus.Publish(ctx, domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   string(domain.EventAdapterError),
		WorkspaceID: evt.WorkspaceID,
		Payload:     errPayload,
		CreatedAt:   time.Now(),
	}); pubErr != nil {
		slog.Warn("failed to publish adapter error event", "adapter", adapterName, "err", pubErr)
	}
}

func reportAdapterError(
	adapterErrors chan<- views.AdapterErrorMsg,
	adapterName, eventType string,
	err error,
) {
	select {
	case adapterErrors <- views.AdapterErrorMsg{
		Adapter:   adapterName,
		EventType: eventType,
		Err:       err,
		Retries:   2,
	}:
	default:
	}
}

func startRepoLifecycleRefresh(
	ctx context.Context,
	adapters []adapter.RepoLifecycleAdapter,
	workspaceID string,
) {
	if workspaceID == "" {
		return
	}

	type prRefresher interface {
		StartPRRefresh(ctx context.Context, workspaceID string)
	}
	type mrRefresher interface {
		StartMRRefresh(ctx context.Context, workspaceID string)
	}

	for _, lifecycleAdapter := range adapters {
		if refresher, ok := lifecycleAdapter.(prRefresher); ok {
			refresher.StartPRRefresh(ctx, workspaceID)
		}
		if refresher, ok := lifecycleAdapter.(mrRefresher); ok {
			refresher.StartMRRefresh(ctx, workspaceID)
		}
	}
}

func buildOrchestrationRuntime(
	cfg *config.Config,
	workspaceDir string,
	services coreServices,
	bus *event.Bus,
) (orchestrationRuntime, error) {
	gitClient := gitwork.NewClient("")
	discoverer := orchestrator.NewDiscoverer(gitClient, cfg)
	harnesses, err := app.BuildAgentHarnesses(cfg, workspaceDir)
	if err != nil {
		return orchestrationRuntime{}, fmt.Errorf("building agent harnesses: %w", err)
	}

	planningCfg := orchestrator.PlanningConfigFromConfig(cfg)
	registry := orchestrator.NewSessionRegistry()
	var planningSvc *orchestrator.PlanningService
	if harnesses.Planning != nil {
		planningSvc, err = orchestrator.NewPlanningService(
			planningCfg, discoverer, gitClient, harnesses.Planning,
			services.plan, services.workItem, services.task, services.event, services.workspace, registry, cfg,
		)
		if err != nil {
			slog.Warn("failed to build planning service; planning unavailable", "err", err)
		}
	}

	var reviewPipeline *orchestrator.ReviewPipeline
	if harnesses.Review != nil {
		reviewPipeline = orchestrator.NewReviewPipeline(
			cfg, harnesses.Review, services.review, services.task, services.plan, services.workItem,
			bus, registry,
		)
	}

	var foreman *orchestrator.Foreman
	if harnesses.Foreman != nil {
		foreman = orchestrator.NewForeman(
			cfg, harnesses.Foreman, services.plan, services.question, services.task, bus,
		)
	}

	var implementationSvc *orchestrator.ImplementationService
	if harnesses.Implementation != nil {
		implementationSvc = orchestrator.NewImplementationService(
			cfg, harnesses.Implementation, gitClient, bus,
			services.plan, services.workItem, services.task, services.workspace, registry,
			reviewPipeline,
			foreman, services.question,
			services.review,
		)
	}

	var resumption *orchestrator.Resumption
	if harnesses.Resume != nil {
		resumption = orchestrator.NewResumption(
			harnesses.Resume, services.task, services.plan, bus, registry,
		)
	}

	return orchestrationRuntime{
		gitClient:      gitClient,
		harnesses:      harnesses,
		planning:       planningSvc,
		reviewPipeline: reviewPipeline,
		foreman:        foreman,
		implementation: implementationSvc,
		resumption:     resumption,
		registry:       registry,
	}, nil
}

// initializeGlobalConfig creates the default config.yaml file.
func initializeGlobalConfig(cfgPath string) error {
	defaultConfig := strings.Join([]string{
		"# Substrate Configuration",
		"# This file was auto-generated with default values.",
		"# All settings have sensible defaults - customize as needed.",
		"#",
		"# commit:",
		"#   strategy: semi-regular",
		"#   message_format: ai-generated",
		"#   message_template: \"feat({{.Scope}}): {{.Description}}\"",
		"#",
		"# plan:",
		"#   max_parse_retries: 2",
		"#",
		"# review:",
		"#   pass_threshold: minor_ok",
		"#   max_cycles: 3",
		"#",
		"# harness:",
		"#   default: ohmypi",
		"#   phase:",
		"#     planning: ohmypi",
		"#     implementation: ohmypi",
		"#     review: ohmypi",
		"#     foreman: ohmypi",
		"#",
		"# foreman:",
		"#   question_timeout: \"0\"",
		"#",
		"# adapters:",
		"#   claude_code:",
		"#     binary_path: claude",
		"#     model: sonnet",
		"#   codex:",
		"#     binary_path: codex",
		"#     model: o4",
		"#     reasoning_effort: high",
		"#   ohmypi:",
		"#     thinking_level: high",
		"#     bun_path: /opt/homebrew/bin/bun",
		"#     bridge_path: /custom/path/to/omp-bridge",
		"",
	}, "\n")
	if err := os.WriteFile(cfgPath, []byte(defaultConfig), 0o600); err != nil {
		return fmt.Errorf("writing default config: %w", err)
	}

	fmt.Printf("substrate: created default config at %s\n", cfgPath)

	return nil
}

// dbRemote wraps *sqlx.DB to implement generic.SQLXRemote.
// The repos use generic.SQLXRemote (designed for *sqlx.Tx usage), but for the
// TUI's direct reads/writes outside explicit transactions we delegate all methods
// to *sqlx.DB, which supports them identically except NamedStmtContext (a Tx-only
// method in sqlx that the repos never actually call).
type dbRemote struct{ db *sqlx.DB }

func (r dbRemote) BindNamed(query string, arg any) (string, []any, error) {
	return r.db.BindNamed(query, arg)
}
func (r dbRemote) DriverName() string { return r.db.DriverName() }
func (r dbRemote) GetContext(ctx context.Context, dest any, query string, args ...any) error {
	return r.db.GetContext(ctx, dest, query, args...)
}

func (r dbRemote) MustExecContext(ctx context.Context, query string, args ...any) sql.Result {
	return r.db.MustExecContext(ctx, query, args...)
}

func (r dbRemote) NamedExecContext(ctx context.Context, query string, arg any) (sql.Result, error) {
	return r.db.NamedExecContext(ctx, query, arg)
}

func (r dbRemote) NamedQuery(query string, arg any) (*sqlx.Rows, error) {
	return r.db.NamedQuery(query, arg)
}

// NamedStmtContext returns stmt unchanged. *sqlx.DB has no transaction to bind
// the stmt to; the repos never call this method in practice.
func (r dbRemote) NamedStmtContext(_ context.Context, stmt *sqlx.NamedStmt) *sqlx.NamedStmt {
	return stmt
}

func (r dbRemote) PrepareNamedContext(ctx context.Context, query string) (*sqlx.NamedStmt, error) {
	return r.db.PrepareNamedContext(ctx, query)
}

func (r dbRemote) PreparexContext(ctx context.Context, query string) (*sqlx.Stmt, error) {
	return r.db.PreparexContext(ctx, query)
}

func (r dbRemote) QueryRowxContext(ctx context.Context, query string, args ...any) *sqlx.Row {
	return r.db.QueryRowxContext(ctx, query, args...)
}

func (r dbRemote) QueryxContext(ctx context.Context, query string, args ...any) (*sqlx.Rows, error) {
	return r.db.QueryxContext(ctx, query, args...)
}
func (r dbRemote) Rebind(query string) string { return r.db.Rebind(query) }
func (r dbRemote) SelectContext(ctx context.Context, dest any, query string, args ...any) error {
	return r.db.SelectContext(ctx, dest, query, args...)
}

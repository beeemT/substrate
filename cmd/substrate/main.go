// Package main is the entry point for the Substrate CLI.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"log/slog"
	"os"
	"strings"

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

func run() error {
	args := os.Args[1:]
	if len(args) > 0 {
		switch args[0] {
		case "--help", "-h", "help":
			printUsage()
			return nil
		case "--version", "-v", "version":
			fmt.Println(Version)
			return nil
		}
	}

	// Get paths from config package (respects SUBSTRATE_HOME)
	globalDir, err := config.GlobalDir()
	if err != nil {
		return fmt.Errorf("getting global directory: %w", err)
	}

	// Ensure global directory exists
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		return fmt.Errorf("creating global directory: %w", err)
	}

	cfgPath, err := config.ConfigPath()
	if err != nil {
		return fmt.Errorf("getting config path: %w", err)
	}

	// Global self-initialization: create default config if not exists
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		if err := initializeGlobalConfig(cfgPath); err != nil {
			return fmt.Errorf("initializing global config: %w", err)
		}
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Ensure sessions directory exists.
	sessionsDir, err := config.SessionsDir()
	if err != nil {
		return fmt.Errorf("getting sessions directory: %w", err)
	}
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		return fmt.Errorf("creating sessions directory: %w", err)
	}
	_ = sessionsDir // used at runtime by TUI log tailing

	// Open database.
	dbPath, err := config.GlobalDBPath()
	if err != nil {
		return fmt.Errorf("getting database path: %w", err)
	}
	db, err := sqlx.Open("sqlite", dbPath)
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer db.Close()

	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA foreign_keys=ON",
		"PRAGMA busy_timeout=5000",
	} {
		if _, err := db.Exec(pragma); err != nil {
			return fmt.Errorf("set pragma: %w", err)
		}
	}

	ctx := context.Background()
	if err := repository.Migrate(ctx, db, migrations.FS); err != nil {
		return fmt.Errorf("running migrations: %w", err)
	}

	// Build repositories.
	remote := dbRemote{db}
	workItemRepo := sqlite.NewSessionRepo(remote)
	planRepo := sqlite.NewPlanRepo(remote)
	subPlanRepo := sqlite.NewSubPlanRepo(remote)
	workspaceRepo := sqlite.NewWorkspaceRepo(remote)
	sessionRepo := sqlite.NewTaskRepo(remote)
	questionRepo := sqlite.NewQuestionRepo(remote)
	instanceRepo := sqlite.NewInstanceRepo(remote)
	reviews := sqlite.NewReviewRepo(remote)
	eventRepo := sqlite.NewEventRepo(remote)

	// Build services.
	workItemSvc := service.NewSessionService(workItemRepo)
	planSvc := service.NewPlanService(planRepo, subPlanRepo)
	workspaceSvc := service.NewWorkspaceService(workspaceRepo)
	sessionSvc := service.NewTaskService(sessionRepo)
	questionSvc := service.NewQuestionService(questionRepo)
	instanceSvc := service.NewInstanceService(instanceRepo)
	reviewSvc := service.NewReviewService(reviews)

	settingsSvc := views.NewSettingsService(
		workItemRepo, planRepo, subPlanRepo, workspaceRepo, sessionRepo, questionRepo, instanceRepo, reviews, eventRepo, config.OSKeychainStore{},
	)

	// Build event bus.
	bus := event.NewBus(event.BusConfig{EventRepo: eventRepo})
	// Detect workspace from cwd.
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting working directory: %w", err)
	}
	var workspaceID, workspaceName, workspaceDir string
	wsDir, wsFile, wsErr := gitwork.FindWorkspace(cwd)
	if wsErr == nil {
		workspaceDir = wsDir
		workspaceName = wsFile.Name
		ws, getErr := workspaceSvc.Get(context.Background(), wsFile.ID)
		if getErr != nil {
			slog.Warn("workspace file found but not in DB; will prompt init", "id", wsFile.ID, "err", getErr)
		} else {
			workspaceID = ws.ID
			workspaceName = ws.Name
		}
	} else if !gitwork.IsNotInWorkspace(wsErr) {
		return fmt.Errorf("detecting workspace: %w", wsErr)
	}

	// Register instance if workspace is known.
	instanceID := ""
	if workspaceID != "" {
		host, _ := os.Hostname()
		inst := domain.SubstrateInstance{
			ID:          domain.NewID(),
			WorkspaceID: workspaceID,
			PID:         os.Getpid(),
			Hostname:    host,
		}
		if err := instanceSvc.Create(context.Background(), inst); err != nil {
			slog.Warn("failed to register instance", "err", err)
		} else {
			instanceID = inst.ID
		}
	}

	if err := config.LoadSecrets(cfg, config.OSKeychainStore{}); err != nil {
		return fmt.Errorf("load config secrets: %w", err)
	}

	// Build adapters.
	var adapters []adapter.WorkItemAdapter
	if workspaceID != "" {
		adapters = app.BuildWorkItemAdapters(cfg, workspaceID, workItemRepo)
	}
	repoLifecycleAdapters := app.BuildRepoLifecycleAdapters(ctx, cfg, workspaceDir, eventRepo)
	for _, workItemAdapter := range adapters {
		sub, subErr := bus.Subscribe("work-item-adapter:" + workItemAdapter.Name())
		if subErr != nil {
			return fmt.Errorf("subscribe work item adapter %s: %w", workItemAdapter.Name(), subErr)
		}
		go func(a adapter.WorkItemAdapter, events <-chan domain.SystemEvent) {
			for evt := range events {
				if err := a.OnEvent(context.Background(), evt); err != nil {
					slog.Warn("work item adapter event handler failed", "adapter", a.Name(), "event", evt.EventType, "err", err)
				}
			}
		}(workItemAdapter, sub.C)
	}
	for _, lifecycleAdapter := range repoLifecycleAdapters {
		sub, subErr := bus.Subscribe("repo-lifecycle-adapter:"+lifecycleAdapter.Name(), string(domain.EventWorktreeCreated), string(domain.EventWorkItemCompleted))
		if subErr != nil {
			return fmt.Errorf("subscribe repo lifecycle adapter %s: %w", lifecycleAdapter.Name(), subErr)
		}
		go func(a adapter.RepoLifecycleAdapter, events <-chan domain.SystemEvent) {
			for evt := range events {
				if err := a.OnEvent(context.Background(), evt); err != nil {
					slog.Warn("repo lifecycle adapter event handler failed", "adapter", a.Name(), "event", evt.EventType, "err", err)
				}
			}
		}(lifecycleAdapter, sub.C)
	}

	// Build gitwork client.
	gitClient := gitwork.NewClient("")

	// Build orchestration services.
	// planningSvc may be nil if template compilation fails (extremely unlikely).
	discoverer := orchestrator.NewDiscoverer(gitClient, cfg)
	harnesses, err := app.BuildAgentHarnesses(cfg, workspaceDir)
	if err != nil {
		return fmt.Errorf("building agent harnesses: %w", err)
	}
	planningCfg := orchestrator.PlanningConfigFromConfig(cfg)
	var planningSvc *orchestrator.PlanningService
	if harnesses.Planning != nil {
		planningSvc, err = orchestrator.NewPlanningService(
			planningCfg, discoverer, gitClient, harnesses.Planning,
			planSvc, workItemSvc, sessionSvc, planRepo, subPlanRepo, eventRepo, workspaceSvc, cfg,
		)
		if err != nil {
			slog.Warn("failed to build planning service; planning unavailable", "err", err)
		}
	}
	var implSvc *orchestrator.ImplementationService
	if harnesses.Implementation != nil {
		implSvc = orchestrator.NewImplementationService(
			cfg, harnesses.Implementation, gitClient, bus,
			planSvc, workItemSvc, sessionSvc, subPlanRepo, sessionRepo, eventRepo, workspaceSvc,
		)
	}
	var reviewPipeline *orchestrator.ReviewPipeline
	if harnesses.Review != nil {
		reviewPipeline = orchestrator.NewReviewPipeline(
			cfg, harnesses.Review, reviewSvc, sessionSvc, planSvc, workItemSvc,
			sessionRepo, planRepo, bus,
		)
	}
	var resumption *orchestrator.Resumption
	if harnesses.Resume != nil {
		resumption = orchestrator.NewResumption(
			harnesses.Resume, sessionSvc, planSvc, sessionRepo, bus,
		)
	}
	var foreman *orchestrator.Foreman
	if harnesses.Foreman != nil {
		foreman = orchestrator.NewForeman(
			cfg, harnesses.Foreman, planSvc, questionSvc, sessionSvc, planRepo, bus,
		)
	}
	settingsData, err := settingsSvc.Snapshot(cfg)
	if err != nil {
		return fmt.Errorf("load settings snapshot: %w", err)
	}

	svcs := views.Services{
		Session:        workItemSvc,
		Plan:           planSvc,
		TaskPlan:       subPlanRepo,
		Task:           sessionSvc,
		Question:       questionSvc,
		Instance:       instanceSvc,
		Workspace:      workspaceSvc,
		Review:         reviewSvc,
		Events:         eventRepo,
		Cfg:            cfg,
		Adapters:       adapters,
		Harnesses:      harnesses,
		Settings:       settingsSvc,
		SettingsData:   settingsData,
		GitClient:      gitClient,
		Bus:            bus,
		InstanceID:     instanceID,
		WorkspaceID:    workspaceID,
		WorkspaceName:  workspaceName,
		WorkspaceDir:   workspaceDir,
		Planning:       planningSvc,
		Implementation: implSvc,
		ReviewPipeline: reviewPipeline,
		Resumption:     resumption,
		Foreman:        foreman,
	}

	return views.RunTUI(svcs)
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
		"#     permission_mode: auto",
		"#     max_turns: 50",
		"#     max_budget_usd: 10.0",
		"#   codex:",
		"#     binary_path: codex",
		"#     model: o4",
		"#     approval_mode: full-auto",
		"#     full_auto: false",
		"#     quiet: false",
		"#   ohmypi:",
		"#     thinking_level: high",
		"#     bun_path: /opt/homebrew/bin/bun",
		"#     bridge_path: /custom/path/to/omp-bridge",
		"",
	}, "\n")
	if err := os.WriteFile(cfgPath, []byte(defaultConfig), 0o644); err != nil {
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

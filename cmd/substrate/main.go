// Package main is the entry point for the Substrate CLI.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"log/slog"
	"os"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"

	"github.com/beeemT/substrate/internal/adapter"
	omp "github.com/beeemT/substrate/internal/adapter/ohmypi"
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

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
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
	workItemRepo := sqlite.NewWorkItemRepo(remote)
	planRepo := sqlite.NewPlanRepo(remote)
	subPlanRepo := sqlite.NewSubPlanRepo(remote)
	workspaceRepo := sqlite.NewWorkspaceRepo(remote)
	sessionRepo := sqlite.NewSessionRepo(remote)
	questionRepo := sqlite.NewQuestionRepo(remote)
	instanceRepo := sqlite.NewInstanceRepo(remote)
	reviews := sqlite.NewReviewRepo(remote)
	eventRepo := sqlite.NewEventRepo(remote)

	// Build services.
	workItemSvc := service.NewWorkItemService(workItemRepo)
	planSvc := service.NewPlanService(planRepo, subPlanRepo)
	workspaceSvc := service.NewWorkspaceService(workspaceRepo)
	sessionSvc := service.NewSessionService(sessionRepo)
	questionSvc := service.NewQuestionService(questionRepo)
	instanceSvc := service.NewInstanceService(instanceRepo)
	reviewSvc := service.NewReviewService(reviews)

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
		ws, getErr := workspaceSvc.Get(context.Background(), wsFile.ID)
		if getErr != nil {
			slog.Warn("workspace file found but not in DB; will prompt init", "id", wsFile.ID, "err", getErr)
		} else {
			workspaceID = ws.ID
			workspaceName = ws.Name
			workspaceDir = wsDir
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

	// Build adapters.
	var adapters []adapter.WorkItemAdapter
	if workspaceID != "" {
		adapters = app.BuildWorkItemAdapters(cfg, workspaceID, workItemRepo)
	}
	repoLifecycleAdapters := app.BuildRepoLifecycleAdapters(ctx, cfg, workspaceDir)
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
	harness := omp.NewHarness(cfg.Adapters.OhMyPi, workspaceDir)
	planningCfg := orchestrator.PlanningConfigFromConfig(cfg)
	planningSvc, err := orchestrator.NewPlanningService(
		planningCfg, discoverer, gitClient, harness,
		planSvc, workItemSvc, planRepo, subPlanRepo, eventRepo, workspaceSvc, cfg,
	)
	if err != nil {
		slog.Warn("failed to build planning service; planning unavailable", "err", err)
	}
	implSvc := orchestrator.NewImplementationService(
		cfg, harness, gitClient, bus,
		planSvc, workItemSvc, sessionSvc, subPlanRepo, sessionRepo, eventRepo, workspaceSvc,
	)
	reviewPipeline := orchestrator.NewReviewPipeline(
		cfg, harness, reviewSvc, sessionSvc, planSvc, workItemSvc,
		sessionRepo, planRepo, bus,
	)
	resumption := orchestrator.NewResumption(
		harness, sessionSvc, planSvc, sessionRepo, bus,
	)
	foreman := orchestrator.NewForeman(
		cfg, harness, planSvc, questionSvc, sessionSvc, planRepo, bus,
	)

	svcs := views.Services{
		WorkItem:       workItemSvc,
		Plan:           planSvc,
		SubPlan:        subPlanRepo,
		Session:        sessionSvc,
		Question:       questionSvc,
		Instance:       instanceSvc,
		Workspace:      workspaceSvc,
		Review:         reviewSvc,
		Cfg:            cfg,
		Adapters:       adapters,
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

// initializeGlobalConfig creates the default config file.
func initializeGlobalConfig(cfgPath string) error {
	defaultConfig := `# Substrate Configuration
# This file was auto-generated with default values.
# All settings have sensible defaults - customize as needed.

# Commit behavior for agent sessions
[commit]
# strategy: granular (every change), semi-regular (logical chunks), single (one commit)
# strategy = "semi-regular"

# message_format: ai-generated, conventional, custom
# message_format = "ai-generated"

# message_template: required when message_format = "custom"
# message_template = "feat({{.Scope}}): {{.Description}}"

# Planning pipeline settings
[plan]
# max_parse_retries: number of correction attempts when plan parsing fails
# max_parse_retries = 2

# Review pipeline settings
[review]
# pass_threshold: nit_only, minor_ok, no_critiques
# pass_threshold = "minor_ok"

# max_cycles: maximum review->re-implement iterations before escalation
# max_cycles = 3

# Foreman (question-answering) settings
[foreman]
# enabled: whether to use foreman for agent questions
# enabled = true

# question_timeout: duration string (e.g., "5m") or "0" for unlimited
# question_timeout = "0"

# Oh-my-pi adapter settings
[adapters.ohmypi]
# bun_path: path to bun executable (defaults to "bun" in PATH)
# bridge_path: path to omp-bridge.ts (defaults to bundled bridge)
# thinking_level: thinking level for all sessions
`

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

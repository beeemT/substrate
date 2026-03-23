//go:build integration && e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"

	"github.com/beeemT/go-atomic/generic"
	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/event"
	"github.com/beeemT/substrate/internal/gitwork"
	"github.com/beeemT/substrate/internal/orchestrator"
	repocore "github.com/beeemT/substrate/internal/repository"
	reposqlite "github.com/beeemT/substrate/internal/repository/sqlite"
	"github.com/beeemT/substrate/internal/service"
	"github.com/beeemT/substrate/migrations"
)

// ---------------------------------------------------------------------------
// dbRemote: adapt *sqlx.DB to generic.SQLXRemote
//
// *sqlx.DB is missing NamedStmtContext, which is present on *sqlx.Tx.
// The SQLite repository implementations never call NamedStmtContext, so a
// pass-through stub is sufficient for testing purposes.
// ---------------------------------------------------------------------------

// dbRemote wraps *sqlx.DB and satisfies generic.SQLXRemote.
type dbRemote struct{ *sqlx.DB }

// newDBRemote creates a dbRemote from a *sqlx.DB.
func newDBRemote(db *sqlx.DB) *dbRemote { return &dbRemote{db} }

// NamedStmtContext is the only method on generic.SQLXRemote that *sqlx.DB
// lacks. It is never called by the SQLite repository implementations.
func (d *dbRemote) NamedStmtContext(_ context.Context, stmt *sqlx.NamedStmt) *sqlx.NamedStmt {
	return stmt
}

// Compile-time check.
var _ generic.SQLXRemote = (*dbRemote)(nil)

// ---------------------------------------------------------------------------
// testEnv: full wired environment for e2e tests
// ---------------------------------------------------------------------------

// testEnv holds all infrastructure and services for e2e tests.
type testEnv struct {
	// Infrastructure
	db           *sqlx.DB
	bus          *event.Bus
	gitClient    *gitwork.Client
	cfg          *config.Config
	mockHarness  *e2eMockHarness
	workspaceDir string // temp dir acting as workspace root
	sessionsDir  string // $SUBSTRATE_HOME/sessions

	// Repos
	workItemRepo  reposqlite.SessionRepo
	planRepo      reposqlite.PlanRepo
	subPlanRepo   reposqlite.SubPlanRepo
	workspaceRepo reposqlite.WorkspaceRepo
	sessionRepo   reposqlite.TaskRepo
	reviewRepo    reposqlite.ReviewRepo
	questionRepo  reposqlite.QuestionRepo
	eventRepo     reposqlite.EventRepo
	instanceRepo  reposqlite.InstanceRepo

	// Services
	workItemSvc  *service.SessionService
	planSvc      *service.PlanService
	sessionSvc   *service.TaskService
	reviewSvc    *service.ReviewService
	workspaceSvc *service.WorkspaceService
	questionSvc  *service.QuestionService

	// Orchestration
	plannerSvc     *orchestrator.PlanningService
	implSvc        *orchestrator.ImplementationService
	reviewPipeline *orchestrator.ReviewPipeline
	resumption     *orchestrator.Resumption
}

// newTestEnv creates and wires all infrastructure for an e2e test.
// It sets SUBSTRATE_HOME to an isolated temp dir so session logs do not
// bleed into ~/.substrate during test runs.
func newTestEnv(t *testing.T) *testEnv {
	t.Helper()

	// Isolate SUBSTRATE_HOME so session logs go to a temp dir.
	substrateHome := t.TempDir()
	t.Setenv("SUBSTRATE_HOME", substrateHome)

	sessionsDir := filepath.Join(substrateHome, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatalf("create sessions dir: %v", err)
	}

	workspaceDir := t.TempDir()

	// --- Database ---
	// ImplementationService spawns goroutines that open new connections from the
	// pool. SQLite :memory: gives each new connection its own empty database,
	// causing "no such table" errors in goroutines. Use a file-based database in
	// the test's temp dir so all connections share the same schema and data.
	dbPath := filepath.Join(substrateHome, "substrate.db")
	db, err := sqlx.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// SQLite pragmas are per-connection. With a file-based DB and multiple
	// goroutines, SetMaxOpenConns(1) ensures all operations share one connection
	// so WAL/busy_timeout apply consistently and no SQLITE_BUSY errors occur.
	db.SetMaxOpenConns(1)
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA foreign_keys=ON",
		"PRAGMA busy_timeout=5000",
	} {
		if _, err := db.Exec(pragma); err != nil {
			t.Fatalf("set pragma %s: %v", pragma, err)
		}
	}
	if err := repocore.Migrate(context.Background(), db, migrations.FS); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// --- Repos ---
	// *sqlx.DB does not implement generic.SQLXRemote directly (missing
	// NamedStmtContext).  Wrap it with dbRemote which adds the stub method.
	dbR := newDBRemote(db)
	workItemRepo := reposqlite.NewSessionRepo(dbR)
	planRepo := reposqlite.NewPlanRepo(dbR)
	subPlanRepo := reposqlite.NewSubPlanRepo(dbR)
	workspaceRepo := reposqlite.NewWorkspaceRepo(dbR)
	sessionRepo := reposqlite.NewTaskRepo(dbR)
	reviewRepo := reposqlite.NewReviewRepo(dbR)
	questionRepo := reposqlite.NewQuestionRepo(dbR)
	eventRepo := reposqlite.NewEventRepo(dbR)
	instanceRepo := reposqlite.NewInstanceRepo(dbR)

	// --- Services ---
	workItemSvc := service.NewSessionService(workItemRepo)
	planSvc := service.NewPlanService(planRepo, subPlanRepo, reposqlite.NewPlanTransacter(db))
	sessionSvc := service.NewTaskService(sessionRepo)
	reviewSvc := service.NewReviewService(reviewRepo)
	workspaceSvc := service.NewWorkspaceService(workspaceRepo)
	questionSvc := service.NewQuestionService(questionRepo)

	// --- Event bus ---
	bus := event.NewBus(event.BusConfig{EventRepo: eventRepo})

	// --- Config ---
	cfg := defaultTestConfig()

	// --- Mock harness ---
	mockHarness := &e2eMockHarness{sessionsDir: sessionsDir}

	// --- git-work client ---
	gitClient := gitwork.NewClient("")

	// --- Discoverer ---
	discoverer := orchestrator.NewDiscoverer(gitClient, cfg)

	// --- Planning service ---
	plannerSvc, err := orchestrator.NewPlanningService(
		orchestrator.PlanningConfigFromConfig(cfg),
		discoverer,
		gitClient,
		mockHarness,
		planSvc,
		workItemSvc,
		sessionSvc,
		eventRepo,
		workspaceSvc,
		nil, // registry
		cfg,
	)
	if err != nil {
		t.Fatalf("create planning service: %v", err)
	}

	// --- Implementation service ---
	implSvc := orchestrator.NewImplementationService(
		cfg,
		mockHarness,
		gitClient,
		bus,
		planSvc,
		workItemSvc,
		sessionSvc,
		subPlanRepo,
		sessionRepo,
		eventRepo,
		workspaceSvc,
	)

	// --- Review pipeline ---
	reviewPipeline := orchestrator.NewReviewPipeline(
		cfg,
		mockHarness,
		reviewSvc,
		sessionSvc,
		planSvc,
		workItemSvc,
		sessionRepo,
		planRepo,
		bus,
	)

	// --- Resumption ---
	resumption := orchestrator.NewResumption(
		mockHarness,
		sessionSvc,
		planSvc,
		sessionRepo,
		bus,
	)

	_ = questionSvc  // used by Foreman; not exercised in these tests
	_ = instanceRepo // used by instance manager; not exercised directly

	return &testEnv{
		db:           db,
		bus:          bus,
		gitClient:    gitClient,
		cfg:          cfg,
		mockHarness:  mockHarness,
		workspaceDir: workspaceDir,
		sessionsDir:  sessionsDir,

		workItemRepo:  workItemRepo,
		planRepo:      planRepo,
		subPlanRepo:   subPlanRepo,
		workspaceRepo: workspaceRepo,
		sessionRepo:   sessionRepo,
		reviewRepo:    reviewRepo,
		questionRepo:  questionRepo,
		eventRepo:     eventRepo,
		instanceRepo:  instanceRepo,

		workItemSvc:  workItemSvc,
		planSvc:      planSvc,
		sessionSvc:   sessionSvc,
		reviewSvc:    reviewSvc,
		workspaceSvc: workspaceSvc,
		questionSvc:  questionSvc,

		plannerSvc:     plannerSvc,
		implSvc:        implSvc,
		reviewPipeline: reviewPipeline,
		resumption:     resumption,
	}
}

// defaultTestConfig returns a minimal config for tests.
func defaultTestConfig() *config.Config {
	maxRetries := 2
	maxCycles := 2
	return &config.Config{
		Plan:   config.PlanConfig{MaxParseRetries: &maxRetries},
		Review: config.ReviewConfig{PassThreshold: config.PassThresholdMinorOK, MaxCycles: &maxCycles},
		Commit: config.CommitConfig{Strategy: config.CommitStrategySemiRegular, MessageFormat: config.CommitMessageAIGenerated},
	}
}

// ---------------------------------------------------------------------------
// DB helpers
// ---------------------------------------------------------------------------

// createWorkspace inserts a Workspace row in the DB and returns it.
func (env *testEnv) createWorkspace(t *testing.T, ctx context.Context) domain.Workspace {
	t.Helper()
	ws := domain.Workspace{
		ID:        domain.NewID(),
		Name:      "test-workspace",
		RootPath:  env.workspaceDir,
		Status:    domain.WorkspaceReady,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := env.workspaceRepo.Create(ctx, ws); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	return ws
}

// createWorkItem inserts a WorkItem in ingested state and returns it.
func (env *testEnv) createWorkItem(t *testing.T, ctx context.Context, wsID string) domain.Session {
	t.Helper()
	item := domain.Session{
		ID:          domain.NewID(),
		WorkspaceID: wsID,
		ExternalID:  "MAN-1",
		Source:      "manual",
		Title:       "Implement test feature",
		Description: "E2E test work item for workflow validation.",
		State:       domain.SessionIngested,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := env.workItemSvc.Create(ctx, item); err != nil {
		t.Fatalf("create work item: %v", err)
	}
	return item
}

// requireWorkItemState asserts the work item is in the expected state.
func (env *testEnv) requireWorkItemState(t *testing.T, ctx context.Context, id string, want domain.SessionState) {
	t.Helper()
	item, err := env.workItemSvc.Get(ctx, id)
	if err != nil {
		t.Fatalf("get work item: %v", err)
	}
	if item.State != want {
		t.Fatalf("work item state = %s, want %s", item.State, want)
	}
}

// approvePlan transitions both the work item and the plan to approved state.
// Called after plannerSvc.Plan(): work item is in PlanReview, plan is in PlanDraft.
// The plan must be submitted for review before it can be approved.
func (env *testEnv) approvePlan(t *testing.T, ctx context.Context, workItemID, planID string) {
	t.Helper()
	if err := env.workItemSvc.ApprovePlan(ctx, workItemID); err != nil {
		t.Fatalf("approve work item: %v", err)
	}
	// plannerSvc.Plan creates the plan in Draft status; submit before approve.
	if err := env.planSvc.SubmitForReview(ctx, planID); err != nil {
		t.Fatalf("submit plan for review: %v", err)
	}
	if err := env.planSvc.ApprovePlan(ctx, planID); err != nil {
		t.Fatalf("approve plan: %v", err)
	}
}

// createApprovedPlan creates a plan through proper service transitions:
// draft → pending_review → approved.  Returns the plan with its assigned ID.
// planSvc.CreatePlan only accepts draft status — callers must not bypass this.
func (env *testEnv) createApprovedPlan(t *testing.T, ctx context.Context, workItemID, orcPlan string) domain.Plan {
	t.Helper()
	plan := domain.Plan{
		ID:               domain.NewID(),
		WorkItemID:       workItemID,
		Status:           domain.PlanDraft,
		OrchestratorPlan: orcPlan,
		Version:          1,
	}
	if err := env.planSvc.CreatePlan(ctx, plan); err != nil {
		t.Fatalf("create plan draft: %v", err)
	}
	if err := env.planSvc.SubmitForReview(ctx, plan.ID); err != nil {
		t.Fatalf("submit plan for review: %v", err)
	}
	if err := env.planSvc.ApprovePlan(ctx, plan.ID); err != nil {
		t.Fatalf("approve plan: %v", err)
	}
	plan.Status = domain.PlanApproved
	return plan
}

// reviewAllSessions fetches all completed sessions for the workspace and runs
// the review pipeline on each one. Returns all ReviewResult values.
func (env *testEnv) reviewAllSessions(t *testing.T, ctx context.Context, wsID string) []*orchestrator.ReviewResult {
	t.Helper()
	sessions, err := env.sessionRepo.ListByWorkspaceID(ctx, wsID)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	var results []*orchestrator.ReviewResult
	for _, sess := range sessions {
		if sess.Status != domain.AgentSessionCompleted {
			continue
		}
		result, err := env.reviewPipeline.ReviewSession(ctx, sess)
		if err != nil {
			t.Fatalf("review session %s: %v", sess.ID, err)
		}
		results = append(results, result)
	}
	return results
}

// ---------------------------------------------------------------------------
// git-work helpers
// ---------------------------------------------------------------------------

// skipIfGitWorkNotInstalled skips the test when the git-work CLI is not available.
func skipIfGitWorkNotInstalled(t *testing.T) {
	t.Helper()
	if err := gitwork.NewClient("").CheckInstalled(); err != nil {
		t.Skipf("git-work not installed: %v", err)
	}
}

// createGitWorkRepo initialises a git-work repository under workspaceDir/name.
// Returns the repository root path and registers t.Cleanup to remove it.
func createGitWorkRepo(t *testing.T, workspaceDir, name string) string {
	t.Helper()

	repoDir := filepath.Join(workspaceDir, name)
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", repoDir, err)
	}

	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("cmd %v: %v\n%s", args, err, out)
		}
	}

	run(repoDir, "git", "init")
	run(repoDir, "git", "config", "user.email", "e2e@substrate.test")
	run(repoDir, "git", "config", "user.name", "E2E Test")

	readme := filepath.Join(repoDir, "README.md")
	if err := os.WriteFile(readme, []byte("# "+name+"\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	run(repoDir, "git", "add", "README.md")
	// Disable GPG signing to avoid 1Password prompts in CI/local.
	run(repoDir, "git", "-c", "commit.gpgsign=false", "commit", "-m", "Initial commit")
	run(repoDir, "git-work", "init")

	t.Cleanup(func() { _ = os.RemoveAll(repoDir) })
	return repoDir
}

// ---------------------------------------------------------------------------
// Plan content helpers
// ---------------------------------------------------------------------------

// validPlan returns a syntactically valid substrate-plan markdown with all
// given repos in a single parallel execution group (wave 0).
func validPlan(repos ...string) string {
	var sb strings.Builder
	sb.WriteString("```substrate-plan\nexecution_groups:\n  - [")
	for i, r := range repos {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(r)
	}
	sb.WriteString("]\n```\n\n## Orchestration\n\nAll repos execute in parallel.\n\n")
	for _, r := range repos {
		fmt.Fprintf(&sb, "## SubPlan: %s\n\nImplement changes in %s.\n\n", r, r)
	}
	return sb.String()
}

// validPlanWaves returns a valid plan with two execution groups:
// firstWave runs first; secondWave runs after firstWave completes.
func validPlanWaves(firstWave, secondWave []string) string {
	var sb strings.Builder
	sb.WriteString("```substrate-plan\nexecution_groups:\n")
	writeGroup := func(group []string) {
		sb.WriteString("  - [")
		for i, r := range group {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(r)
		}
		sb.WriteString("]\n")
	}
	writeGroup(firstWave)
	writeGroup(secondWave)
	sb.WriteString("```\n\n## Orchestration\n\nTwo-wave execution.\n\n")
	for _, r := range append(firstWave, secondWave...) {
		fmt.Fprintf(&sb, "## SubPlan: %s\n\nImplement changes in %s.\n\n", r, r)
	}
	return sb.String()
}

// ---------------------------------------------------------------------------
// Session log helpers
// ---------------------------------------------------------------------------

// writeProgressLog writes content lines as JSONL progress events to path.
// Format: {"type":"event","event":{"type":"progress","text":"<line>"}}
// This matches the format expected by ReviewPipeline.readSessionOutputFromLog.
func writeProgressLog(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create log dir: %w", err)
	}
	var sb strings.Builder
	for _, line := range strings.Split(content, "\n") {
		escaped, err := json.Marshal(line)
		if err != nil {
			return fmt.Errorf("marshal line: %w", err)
		}
		fmt.Fprintf(&sb, `{"type":"event","event":{"type":"progress","text":%s}}`, string(escaped))
		sb.WriteString("\n")
	}
	return os.WriteFile(path, []byte(sb.String()), 0o644)
}

// ---------------------------------------------------------------------------
// e2eMockHarness — configurable mock AgentHarness
// ---------------------------------------------------------------------------

// sessionSpec describes the behaviour of one mock agent session.
type sessionSpec struct {
	// planContent is written to opts.DraftPath when non-empty (planning sessions).
	planContent string
	// reviewOutput is written to the session log file when non-empty (review sessions).
	reviewOutput string
	// waitErr is returned by the session's Wait() method.
	waitErr error
	// startErr, if set, causes StartSession to return (nil, startErr) immediately.
	// Use this to simulate harness startup failures (network errors, etc.).
	startErr error
}

// e2eMockHarness is a FIFO-queued mock AgentHarness.
// Call Enqueue* methods to configure responses before running orchestration.
type e2eMockHarness struct {
	mu          sync.Mutex
	sessionsDir string
	queue       []sessionSpec
	idx         int
}

func (h *e2eMockHarness) Name() string { return "e2e-mock" }

// EnqueueSpec appends one or more session specs to the FIFO queue.
func (h *e2eMockHarness) EnqueueSpec(specs ...sessionSpec) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.queue = append(h.queue, specs...)
}

// EnqueuePlanning queues a session that writes planContent to DraftPath.
func (h *e2eMockHarness) EnqueuePlanning(planContent string) {
	h.EnqueueSpec(sessionSpec{planContent: planContent})
}

// EnqueueImplSuccess queues a successful implementation session.
func (h *e2eMockHarness) EnqueueImplSuccess() {
	h.EnqueueSpec(sessionSpec{})
}

// EnqueueReview queues a review session with the given output text.
func (h *e2eMockHarness) EnqueueReview(output string) {
	h.EnqueueSpec(sessionSpec{reviewOutput: output})
}

// EnqueueError queues a session that fails at startup (StartSession returns err).
// Use this to simulate network-level failures before the session begins.
func (h *e2eMockHarness) EnqueueError(err error) {
	h.EnqueueSpec(sessionSpec{startErr: err})
}

// StartSession pops the next spec from the FIFO queue (or uses an empty spec
// when the queue is exhausted) and returns a configured e2eSession.
func (h *e2eMockHarness) StartSession(_ context.Context, opts adapter.SessionOpts) (adapter.AgentSession, error) {
	h.mu.Lock()
	var spec sessionSpec
	if h.idx < len(h.queue) {
		spec = h.queue[h.idx]
		h.idx++
	}
	h.mu.Unlock()

	// Return startup error immediately (simulates network/auth failures).
	if spec.startErr != nil {
		return nil, spec.startErr
	}

	// Write plan draft (planning sessions set DraftPath).
	if spec.planContent != "" && opts.DraftPath != "" {
		if err := os.MkdirAll(filepath.Dir(opts.DraftPath), 0o755); err != nil {
			return nil, fmt.Errorf("create draft dir: %w", err)
		}
		if err := os.WriteFile(opts.DraftPath, []byte(spec.planContent), 0o644); err != nil {
			return nil, fmt.Errorf("write plan draft: %w", err)
		}
	}

	// Write session log (review sessions, foreman sessions).
	if spec.reviewOutput != "" {
		logPath := filepath.Join(h.sessionsDir, opts.SessionID+".log")
		if err := writeProgressLog(logPath, spec.reviewOutput); err != nil {
			return nil, fmt.Errorf("write session log: %w", err)
		}
	}

	// Pre-buffer a "done" event for sessions that are polled for events
	// (planning's waitForDraftOrCompletion, review's startReviewAgent).
	// Implementation sessions don't need pre-buffered events: Wait() closes
	// the channel and forwardEvents goroutine exits via context cancellation.
	var preloaded []adapter.AgentEvent
	if opts.DraftPath != "" || opts.Mode == adapter.SessionModeForeman {
		preloaded = []adapter.AgentEvent{{Type: "done", Timestamp: time.Now()}}
	}

	return newE2ESession(opts.SessionID, spec.waitErr, preloaded...), nil
}

// ---------------------------------------------------------------------------
// e2eSession — mock adapter.AgentSession
// ---------------------------------------------------------------------------

type e2eSession struct {
	id        string
	eventsCh  chan adapter.AgentEvent
	waitErr   error
	closeOnce sync.Once
}

func newE2ESession(id string, waitErr error, events ...adapter.AgentEvent) *e2eSession {
	ch := make(chan adapter.AgentEvent, len(events)+1)
	for _, e := range events {
		ch <- e
	}
	return &e2eSession{id: id, eventsCh: ch, waitErr: waitErr}
}

func (s *e2eSession) ID() string                        { return s.id }
func (s *e2eSession) Events() <-chan adapter.AgentEvent { return s.eventsCh }

// Wait closes the events channel and returns the configured error.
// Idempotent via sync.Once — safe to call from forwardEvents goroutines.
func (s *e2eSession) Wait(_ context.Context) error {
	s.closeOnce.Do(func() { close(s.eventsCh) })
	return s.waitErr
}

// SendMessage is a no-op (correction loops succeed without messaging).
func (s *e2eSession) SendMessage(_ context.Context, _ string) error { return nil }

// Abort closes the events channel. Idempotent via sync.Once.
func (s *e2eSession) Abort(_ context.Context) error {
	s.closeOnce.Do(func() { close(s.eventsCh) })
	return nil
}

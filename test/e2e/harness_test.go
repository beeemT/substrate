//go:build integration && e2e

package e2e_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
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
	"github.com/beeemT/substrate/internal/app"
	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/event"
	"github.com/beeemT/substrate/internal/gitwork"
	"github.com/beeemT/substrate/internal/orchestrator"
	repocore "github.com/beeemT/substrate/internal/repository"
	reposqlite "github.com/beeemT/substrate/internal/repository/sqlite"
	"github.com/beeemT/substrate/internal/service"
	"github.com/beeemT/substrate/internal/worktree"
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
	sessionRepo   reposqlite.AgentSessionRepo
	reviewRepo    reposqlite.ReviewRepo
	questionRepo  reposqlite.QuestionRepo
	eventRepo     reposqlite.EventRepo
	instanceRepo  reposqlite.InstanceRepo

	// Services
	workItemSvc  *service.SessionService
	planSvc      *service.PlanService
	sessionSvc   *service.AgentSessionService
	reviewSvc    *service.ReviewService
	workspaceSvc *service.WorkspaceService
	questionSvc  *service.QuestionService

	// Orchestration
	plannerSvc     *orchestrator.PlanningService
	implSvc        *orchestrator.ImplementationService
	reviewPipeline *orchestrator.ReviewPipeline
}

// newTestEnv creates and wires all infrastructure for an e2e test with the default mock harness.
func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	return newTestEnvWithAgentHarnesses(t, nil, nil, nil)
}

// newTestEnvWithAgentHarnesses wires e2e infrastructure with explicit product harnesses.
func newTestEnvWithAgentHarnesses(t *testing.T, planningHarness, implementationHarness, reviewHarness adapter.AgentHarness) *testEnv {
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
	dbPath := filepath.Join(substrateHome, "substrate.db")
	db, err := sqlx.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Logf("close db: %v", err)
		}
	})

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
	dbR := newDBRemote(db)
	workItemRepo := reposqlite.NewSessionRepo(dbR)
	planRepo := reposqlite.NewPlanRepo(dbR)
	subPlanRepo := reposqlite.NewSubPlanRepo(dbR)
	workspaceRepo := reposqlite.NewWorkspaceRepo(dbR)
	sessionRepo := reposqlite.NewAgentSessionRepo(dbR)
	reviewRepo := reposqlite.NewReviewRepo(dbR)
	questionRepo := reposqlite.NewQuestionRepo(dbR)
	eventRepo := reposqlite.NewEventRepo(dbR)
	instanceRepo := reposqlite.NewInstanceRepo(dbR)

	// --- Event bus ---
	bus := event.NewBus(event.BusConfig{EventRepo: eventRepo})

	// --- Services ---
	transacter := reposqlite.NewTransacter(db)
	workItemSvc := service.NewSessionService(transacter, bus)
	planSvc := service.NewPlanService(transacter, bus)
	sessionSvc := service.NewAgentSessionService(transacter, bus)
	continuationSvc := service.NewAgentSessionContinuationService(transacter)
	reviewSvc := service.NewReviewService(transacter, bus)
	workspaceSvc := service.NewWorkspaceService(transacter, bus)
	questionSvc := service.NewQuestionService(transacter, bus)
	eventSvc := service.NewEventService(transacter)

	// --- Config ---
	cfg := defaultTestConfig()

	// --- Harnesses ---
	mockHarness := &e2eMockHarness{sessionsDir: sessionsDir}
	if planningHarness == nil {
		planningHarness = mockHarness
	}
	if implementationHarness == nil {
		implementationHarness = mockHarness
	}
	if reviewHarness == nil {
		reviewHarness = mockHarness
	}

	// --- git-work client ---
	gitClient := gitwork.NewClient("")

	// --- Discoverer ---
	discoverer := orchestrator.NewDiscoverer(gitClient)
	registry := orchestrator.NewSessionRegistry()

	// --- Planning service ---
	plannerSvc, err := orchestrator.NewPlanningService(
		orchestrator.PlanningConfigFromConfig(cfg),
		discoverer,
		gitClient,
		planningHarness,
		planSvc,
		workItemSvc,
		sessionSvc,
		bus,
		workspaceSvc,
		registry,
		questionSvc,
		cfg,
	)
	if err != nil {
		t.Fatalf("create planning service: %v", err)
	}

	// --- Implementation service ---
	implSvc := orchestrator.NewImplementationService(
		cfg,
		implementationHarness,
		gitClient,
		bus,
		planSvc,
		workItemSvc,
		sessionSvc,
		continuationSvc,
		workspaceSvc,
		registry,
		nil, // reviewPipeline
		nil, // foreman
		nil, // questionSvc
		nil, // reviewSvc
		worktree.NewHookRegistry(),
	)

	// --- Review pipeline ---
	reviewPipeline := orchestrator.NewReviewPipeline(
		cfg,
		reviewHarness,
		reviewSvc,
		sessionSvc,
		planSvc,
		workItemSvc,
		bus,
		registry,
	)

	_ = eventSvc    // constructed to keep service wiring explicit in e2e tests
	_ = questionSvc // used by Foreman; not exercised in these tests
	_ = instanceRepo

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
	plan, err := env.planSvc.GetPlan(ctx, planID)
	if err != nil {
		t.Fatalf("get plan before approval: %v", err)
	}
	if plan.Status == domain.PlanDraft {
		if err := env.planSvc.SubmitForReview(ctx, planID); err != nil {
			t.Fatalf("submit plan for review: %v", err)
		}
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
		if sess.Kind != domain.AgentSessionKindImplementation || sess.Status != domain.AgentSessionCompleted {
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
	run(repoDir, "git", "branch", "-M", "main")
	run(repoDir, "git", "config", "user.email", "e2e@substrate.test")
	run(repoDir, "git", "config", "user.name", "E2E Test")

	readme := filepath.Join(repoDir, "README.md")
	if err := os.WriteFile(readme, []byte("# "+name+"\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	run(repoDir, "git", "add", "README.md")
	// Disable GPG signing to avoid 1Password prompts in CI/local.
	run(repoDir, "git", "-c", "commit.gpgsign=false", "commit", "-m", "Initial commit")
	remoteDir := filepath.Join(workspaceDir, name+"-origin.git")
	run(workspaceDir, "git", "init", "--bare", remoteDir)
	run(repoDir, "git", "remote", "add", "origin", remoteDir)
	run(repoDir, "git", "-c", "commit.gpgsign=false", "push", "-u", "origin", "main")
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
		writeValidSubPlan(&sb, r)
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
		writeValidSubPlan(&sb, r)
	}
	return sb.String()
}

func writeValidSubPlan(sb *strings.Builder, repo string) {
	fmt.Fprintf(sb, "## SubPlan: %s\n", repo)
	sb.WriteString("### Goal\n")
	fmt.Fprintf(sb, "Implement deterministic test changes in %s.\n\n", repo)
	sb.WriteString("### Scope\n")
	fmt.Fprintf(sb, "- Repository %s test fixture files.\n\n", repo)
	sb.WriteString("### Changes\n")
	sb.WriteString("1. Apply the requested product workflow changes.\n")
	sb.WriteString("2. Preserve existing behavior for unaffected code paths.\n")
	sb.WriteString("3. Add or refresh focused validation.\n\n")
	sb.WriteString("### Validation\n")
	sb.WriteString("- go test ./...\n\n")
	sb.WriteString("### Risks\n")
	sb.WriteString("- Keep the workflow deterministic and isolated from external services.\n\n")
}

// ---------------------------------------------------------------------------
// Session log helpers
// ---------------------------------------------------------------------------

// writeProgressLog writes content as a canonical assistant_output JSONL event.
// ReviewPipeline.readSessionOutputFromLog flattens assistant output entries.
func writeProgressLog(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create log dir: %w", err)
	}
	escaped, err := json.Marshal(content)
	if err != nil {
		return fmt.Errorf("marshal content: %w", err)
	}
	entry := fmt.Sprintf(`{"type":"event","event":{"type":"assistant_output","text":%s}}`+"\n", string(escaped))
	return os.WriteFile(path, []byte(entry), 0o644)
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

func (h *e2eMockHarness) SupportsCompact() bool { return false }

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
	if opts.DraftPath != "" || opts.Mode == adapter.SessionModeForeman || spec.reviewOutput != "" {
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
	doneCh    chan struct{}
	waitErr   error
	closeOnce sync.Once
}

func newE2ESession(id string, waitErr error, events ...adapter.AgentEvent) *e2eSession {
	ch := make(chan adapter.AgentEvent, len(events)+1)
	for _, e := range events {
		ch <- e
	}
	return &e2eSession{id: id, eventsCh: ch, doneCh: make(chan struct{}), waitErr: waitErr}
}

func (s *e2eSession) ID() string                        { return s.id }
func (s *e2eSession) Events() <-chan adapter.AgentEvent { return s.eventsCh }
func (s *e2eSession) Done() <-chan struct{}             { return s.doneCh }

// Wait closes the events channel and returns the configured error.
// Idempotent via sync.Once — safe to call from forwardEvents goroutines.
func (s *e2eSession) Wait(_ context.Context) error {
	s.closeOnce.Do(func() {
		close(s.eventsCh)
		close(s.doneCh)
	})
	return s.waitErr
}

// SendMessage is a no-op (correction loops succeed without messaging).
func (s *e2eSession) SendMessage(_ context.Context, _ string) error { return nil }

// Abort closes the events channel. Idempotent via sync.Once.
func (s *e2eSession) Abort(_ context.Context) error {
	s.closeOnce.Do(func() {
		close(s.eventsCh)
		close(s.doneCh)
	})
	return nil
}

func (s *e2eSession) Steer(_ context.Context, _ string) error { return adapter.ErrSteerNotSupported }

func (s *e2eSession) SendAnswer(_ context.Context, _ string) error {
	return adapter.ErrSendAnswerNotSupported
}

func (s *e2eSession) ResumeInfo() map[string]string { return nil }

func (s *e2eSession) Compact(_ context.Context) error {
	return adapter.ErrCompactNotSupported
}

type e2eHarnessCase struct {
	name string
	cfg  func(t *testing.T, plan string, workspaceRoot string) *config.Config
}

func buildE2EProductHarnesses(t *testing.T, cfg *config.Config, workspaceRoot string) app.AgentHarnesses {
	t.Helper()
	harnesses, err := app.BuildAgentHarnesses(cfg, workspaceRoot)
	if err != nil {
		t.Fatalf("BuildAgentHarnesses: %v", err)
	}
	return harnesses
}

func e2eProductHarnessCases() []e2eHarnessCase {
	return []e2eHarnessCase{
		{name: "ohmypi", cfg: func(t *testing.T, plan, _ string) *config.Config {
			cfg := defaultTestConfig()
			cfg.Harness.Default = config.HarnessOhMyPi
			cfg.Adapters.OhMyPi.BridgePath = writeE2EHelperWrapper(t, "omp-bridge", "GO_WANT_E2E_BRIDGE")
			t.Setenv("E2E_PRODUCT_PLAN", plan)
			return cfg
		}},
		{name: "claudeagent", cfg: func(t *testing.T, plan, _ string) *config.Config {
			cfg := defaultTestConfig()
			cfg.Harness.Default = config.HarnessClaudeCode
			cfg.Adapters.ClaudeCode.BridgePath = writeE2EHelperWrapper(t, "claude-bridge", "GO_WANT_E2E_BRIDGE")
			t.Setenv("E2E_PRODUCT_PLAN", plan)
			return cfg
		}},
		{name: "codex", cfg: func(t *testing.T, plan, _ string) *config.Config {
			cfg := defaultTestConfig()
			cfg.Harness.Default = config.HarnessCodex
			cfg.Adapters.Codex.BinaryPath = writeE2EHelperWrapper(t, "codex", "GO_WANT_E2E_CODEX")
			t.Setenv("E2E_PRODUCT_PLAN", plan)
			return cfg
		}},
		{name: "opencode", cfg: func(t *testing.T, plan, _ string) *config.Config {
			cfg := defaultTestConfig()
			cfg.Harness.Default = config.HarnessOpenCode
			cfg.Adapters.OpenCode.BinaryPath = writeE2EHelperWrapper(t, "opencode", "GO_WANT_E2E_OPENCODE")
			t.Setenv("E2E_PRODUCT_PLAN", plan)
			return cfg
		}},
		{name: "acp", cfg: func(t *testing.T, plan, _ string) *config.Config {
			cfg := defaultTestConfig()
			cfg.Harness.Default = config.HarnessACP
			cfg.Adapters.ACP.Command = writeE2EHelperWrapper(t, "acp", "GO_WANT_E2E_ACP")
			cfg.Adapters.ACP.Args = []string{}
			cfg.Adapters.ACP.Env = map[string]string{"E2E_PRODUCT_PLAN": plan}
			return cfg
		}},
	}
}

func writeE2EHelperWrapper(t *testing.T, name, envKey string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	content := fmt.Sprintf("#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo '%s fake'; exit 0; fi\n%s=1 exec %q -test.run=TestHelperE2EProductHarness -- \"$@\"\n", name, envKey, os.Args[0])
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write helper wrapper: %v", err)
	}
	return path
}

func TestHelperE2EProductHarness(t *testing.T) {
	switch {
	case os.Getenv("GO_WANT_E2E_BRIDGE") == "1":
		runE2EBridgeHelper()
	case os.Getenv("GO_WANT_E2E_CODEX") == "1":
		runE2ECodexHelper()
	case os.Getenv("GO_WANT_E2E_ACP") == "1":
		runE2EACPHelper()
	case os.Getenv("GO_WANT_E2E_OPENCODE") == "1":
		runE2EOpenCodeHelper()
		os.Exit(0)
	}
}

func runE2EBridgeHelper() {
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var msg map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}
		typ, _ := msg["type"].(string)
		text, _ := msg["text"].(string)
		switch typ {
		case "prompt", "message", "steer":
			writeE2EPlanFromPrompt(text)
			writeE2EJSONLine(map[string]any{"type": "event", "event": map[string]any{"type": "assistant_output", "text": "NO_CRITIQUES"}})
			writeE2EJSONLine(map[string]any{"type": "event", "event": map[string]any{"type": "lifecycle", "stage": "completed", "summary": "done"}})
			os.Exit(0)
		case "compact":
			writeE2EJSONLine(map[string]any{"type": "event", "event": map[string]any{"type": "lifecycle", "stage": "compaction_end", "message": "done"}})
		case "abort":
			os.Exit(0)
		}
	}
	os.Exit(0)
}

func runE2ECodexHelper() {
	if len(os.Args) > 2 && os.Args[1] == "--" && len(os.Args) > 3 && os.Args[2] == "exec" && os.Args[3] == "--help" {
		fmt.Println("usage: codex exec --json")
		os.Exit(0)
	}
	promptBytes, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read stdin: %v\n", err)
		os.Exit(1)
	}
	writeE2EPlanFromPrompt(string(promptBytes))
	writeE2EJSONLine(map[string]any{"type": "thread.started", "thread_id": "e2e-thread"})
	writeE2EJSONLine(map[string]any{"type": "item.completed", "item": map[string]any{"id": "m1", "type": "agent_message", "text": "NO_CRITIQUES"}})
	writeE2EJSONLine(map[string]any{"type": "turn.completed", "usage": map[string]any{"input_tokens": 1, "cached_input_tokens": 0, "output_tokens": 1}})
	os.Exit(0)
}

func runE2EACPHelper() {
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var msg map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}
		method, _ := msg["method"].(string)
		id := msg["id"]
		switch method {
		case "initialize":
			writeE2EJSONLine(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"protocolVersion": 1, "agentInfo": map[string]any{"name": "E2E ACP", "version": "test"}, "agentCapabilities": map[string]any{"loadSession": true, "sessionCapabilities": map[string]any{"close": map[string]any{}}}}})
		case "session/new", "session/load", "session/resume":
			writeE2EJSONLine(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"sessionId": "acp-e2e"}})
		case "session/prompt":
			text := e2eACPPromptText(msg)
			writeE2EPlanFromPrompt(text)
			writeE2EJSONLine(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": "acp-e2e", "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "NO_CRITIQUES"}}}})
			writeE2EJSONLine(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"stopReason": "end_turn"}})
			os.Exit(0)
		case "session/cancel", "session/close":
			writeE2EJSONLine(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{}})
		}
	}
	os.Exit(0)
}

func runE2EOpenCodeHelper() {
	helper := &e2eOpenCodeHelper{events: make(chan string, 16)}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintf(os.Stderr, "listen: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Server running on http://%s\n", listener.Addr().String())
	if err := http.Serve(listener, helper); err != nil {
		fmt.Fprintf(os.Stderr, "serve: %v\n", err)
		os.Exit(1)
	}
}

type e2eOpenCodeHelper struct {
	events chan string
}

func (h *e2eOpenCodeHelper) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/session":
		w.WriteHeader(http.StatusOK)
	case r.Method == http.MethodPost && r.URL.Path == "/session":
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"oc-e2e"}`)
	case r.Method == http.MethodGet && r.URL.Path == "/event":
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		for {
			select {
			case event := <-h.events:
				fmt.Fprintf(w, "data: %s\n\n", event)
				if flusher != nil {
					flusher.Flush()
				}
			case <-r.Context().Done():
				return
			}
		}
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/session/") && strings.HasSuffix(r.URL.Path, "/message"):
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read body", http.StatusBadRequest)
			return
		}
		writeE2EPlanFromPrompt(string(body))
		h.events <- `{"type":"message.updated","sessionID":"oc-e2e","message":{"id":"m1","parts":[{"type":"text","text":"NO_CRITIQUES"}]}}`
		h.events <- `{"type":"session.completed","sessionID":"oc-e2e"}`
		w.WriteHeader(http.StatusOK)
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/abort"):
		h.events <- `{"type":"session.aborted","sessionID":"oc-e2e"}`
		w.WriteHeader(http.StatusOK)
	default:
		http.NotFound(w, r)
	}
}

func e2eACPPromptText(msg map[string]any) string {
	params, _ := msg["params"].(map[string]any)
	blocks, _ := params["prompt"].([]any)
	if len(blocks) == 0 {
		return ""
	}
	block, _ := blocks[0].(map[string]any)
	text, _ := block["text"].(string)
	return text
}

func writeE2EPlanFromPrompt(prompt string) {
	plan := os.Getenv("E2E_PRODUCT_PLAN")
	if plan == "" {
		return
	}
	path := extractE2EDraftPath(prompt)
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "create draft dir: %v\n", err)
		return
	}
	if err := os.WriteFile(path, []byte(plan), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write draft: %v\n", err)
	}
}

func extractE2EDraftPath(prompt string) string {
	for _, marker := range []string{"progressively to ", "draft update to ", "plan to "} {
		idx := strings.Index(prompt, marker)
		if idx < 0 {
			continue
		}
		rest := prompt[idx+len(marker):]
		rest = strings.Trim(rest, "` \n\t")
		end := strings.IndexAny(rest, " \n\t")
		if end > 0 {
			return strings.TrimSuffix(strings.Trim(rest[:end], "`"), ".")
		}
		return strings.TrimSuffix(strings.Trim(rest, "`"), ".")
	}
	return ""
}

func writeE2EJSONLine(v any) {
	data, err := json.Marshal(v)
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal helper json: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(data))
}

package orchestrator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/event"
	"github.com/beeemT/substrate/internal/repository"
	"github.com/beeemT/substrate/internal/service"
)

// ============================================================
// Mock: adapter.AgentSession
// ============================================================

type mockAgentSession struct {
	id       string
	eventsCh chan adapter.AgentEvent
	mu       sync.Mutex
	messages []string
	aborted  bool
	abortErr error
}

func newMockSession(id string, events ...adapter.AgentEvent) *mockAgentSession {
	ch := make(chan adapter.AgentEvent, len(events)+10)
	for _, e := range events {
		ch <- e
	}

	return &mockAgentSession{id: id, eventsCh: ch}
}

func (s *mockAgentSession) ID() string { return s.id }
func (s *mockAgentSession) Wait(ctx context.Context) error {
	<-ctx.Done()

	return ctx.Err()
}
func (s *mockAgentSession) Events() <-chan adapter.AgentEvent { return s.eventsCh }
func (s *mockAgentSession) SendMessage(_ context.Context, msg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, msg)

	return nil
}

func (s *mockAgentSession) Abort(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.aborted = true

	return s.abortErr
}
func (s *mockAgentSession) Steer(_ context.Context, _ string) error      { return nil }
func (s *mockAgentSession) SendAnswer(_ context.Context, _ string) error { return nil }
func (s *mockAgentSession) Compact(_ context.Context) error { return nil }
func (s *mockAgentSession) ResumeInfo() map[string]string                { return nil }

// ============================================================
// Mock: adapter.AgentHarness
// ============================================================

// mockAgentHarness returns sessions pre-loaded with test output written to session log files.
type mockAgentHarness struct {
	sessionsDir string
	// outputs is a FIFO queue. Each StartSession call pops one output string.
	// If the queue is empty, "NO_CRITIQUES" is used.
	outputs []string
	idx     int
	mu      sync.Mutex
}

func (h *mockAgentHarness) SupportsCompact() bool { return true }
func (h *mockAgentHarness) Name() string { return "mock" }

func (h *mockAgentHarness) StartSession(_ context.Context, opts adapter.SessionOpts) (adapter.AgentSession, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	output := "NO_CRITIQUES"
	if h.idx < len(h.outputs) {
		output = h.outputs[h.idx]
		h.idx++
	}

	if err := writeTestSessionLog(h.sessionsDir, opts.SessionID, output); err != nil {
		return nil, fmt.Errorf("write test session log: %w", err)
	}

	// Pre-buffer a "done" event so startReviewAgent returns immediately.
	return newMockSession(opts.SessionID, adapter.AgentEvent{Type: "done"}), nil
}

// writeTestSessionLog writes output in the canonical session-log event format.
func writeTestSessionLog(sessionsDir, sessionID, output string) error {
	logPath := filepath.Join(sessionsDir, sessionID+".log")
	entry := fmt.Sprintf(`{"type":"event","event":{"type":"assistant_output","text":%q}}`+"\n", output)

	return os.WriteFile(logPath, []byte(entry), 0o644)
}

// ============================================================
// Mock repositories
// ============================================================

type mockPlanRepo struct {
	mu       sync.Mutex
	plans    map[string]domain.Plan
	faqAdded []domain.FAQEntry
}

func newMockPlanRepo() *mockPlanRepo {
	return &mockPlanRepo{plans: make(map[string]domain.Plan)}
}

func (r *mockPlanRepo) Get(_ context.Context, id string) (domain.Plan, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p, ok := r.plans[id]; ok {
		return p, nil
	}

	return domain.Plan{}, repository.ErrNotFound
}

func (r *mockPlanRepo) GetByWorkItemID(_ context.Context, _ string) (domain.Plan, error) {
	return domain.Plan{}, repository.ErrNotFound
}

func (r *mockPlanRepo) Create(_ context.Context, plan domain.Plan) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.plans[plan.ID] = plan

	return nil
}

func (r *mockPlanRepo) Update(_ context.Context, plan domain.Plan) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.plans[plan.ID] = plan

	return nil
}

func (r *mockPlanRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.plans, id)

	return nil
}

func (r *mockPlanRepo) AppendFAQ(_ context.Context, entry domain.FAQEntry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.faqAdded = append(r.faqAdded, entry)

	return nil
}

type mockSubPlanRepo struct {
	mu       sync.Mutex
	subPlans map[string]domain.TaskPlan
}

func newMockSubPlanRepo() *mockSubPlanRepo {
	return &mockSubPlanRepo{subPlans: make(map[string]domain.TaskPlan)}
}

func (r *mockSubPlanRepo) Get(_ context.Context, id string) (domain.TaskPlan, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if sp, ok := r.subPlans[id]; ok {
		return sp, nil
	}

	return domain.TaskPlan{}, repository.ErrNotFound
}

func (r *mockSubPlanRepo) ListByPlanID(_ context.Context, planID string) ([]domain.TaskPlan, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var result []domain.TaskPlan
	for _, sp := range r.subPlans {
		if sp.PlanID == planID {
			result = append(result, sp)
		}
	}

	return result, nil
}

func (r *mockSubPlanRepo) Create(_ context.Context, sp domain.TaskPlan) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.subPlans[sp.ID] = sp

	return nil
}

func (r *mockSubPlanRepo) Update(_ context.Context, sp domain.TaskPlan) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.subPlans[sp.ID] = sp

	return nil
}

func (r *mockSubPlanRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.subPlans, id)

	return nil
}

type mockSessionRepo struct {
	mu              sync.Mutex
	sessions        map[string]domain.Task
	searchHistory   []domain.SessionHistoryEntry
	updateErr       error
	updateErrStatus domain.TaskStatus
	deleteErr       error
	updateHook      func(context.Context, domain.Task) error
	deleteHook      func(context.Context, string) error
}

func newMockSessionRepo() *mockSessionRepo {
	return &mockSessionRepo{sessions: make(map[string]domain.Task)}
}

func (r *mockSessionRepo) Get(_ context.Context, id string) (domain.Task, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s, ok := r.sessions[id]; ok {
		return s, nil
	}

	return domain.Task{}, repository.ErrNotFound
}

func (r *mockSessionRepo) ListByWorkItemID(_ context.Context, workItemID string) ([]domain.Task, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var result []domain.Task
	for _, s := range r.sessions {
		if s.WorkItemID == workItemID {
			result = append(result, s)
		}
	}

	return result, nil
}

func (r *mockSessionRepo) ListBySubPlanID(_ context.Context, subPlanID string) ([]domain.Task, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var result []domain.Task
	for _, s := range r.sessions {
		if s.SubPlanID == subPlanID {
			result = append(result, s)
		}
	}

	return result, nil
}

func (r *mockSessionRepo) ListByWorkspaceID(_ context.Context, workspaceID string) ([]domain.Task, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var result []domain.Task
	for _, s := range r.sessions {
		if s.WorkspaceID == workspaceID {
			result = append(result, s)
		}
	}

	return result, nil
}

func (r *mockSessionRepo) ListByOwnerInstanceID(_ context.Context, instanceID string) ([]domain.Task, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var result []domain.Task
	for _, s := range r.sessions {
		if s.OwnerInstanceID != nil && *s.OwnerInstanceID == instanceID {
			result = append(result, s)
		}
	}

	return result, nil
}

func (r *mockSessionRepo) SearchHistory(_ context.Context, _ domain.SessionHistoryFilter) ([]domain.SessionHistoryEntry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entries := make([]domain.SessionHistoryEntry, len(r.searchHistory))
	copy(entries, r.searchHistory)

	return entries, nil
}

func (r *mockSessionRepo) Create(_ context.Context, s domain.Task) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessions[s.ID] = s

	return nil
}

func (r *mockSessionRepo) Update(ctx context.Context, s domain.Task) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.updateHook != nil {
		if err := r.updateHook(ctx, s); err != nil {
			return err
		}
	}
	if r.updateErr != nil && (r.updateErrStatus == "" || s.Status == r.updateErrStatus) {
		return r.updateErr
	}
	r.sessions[s.ID] = s

	return nil
}

func (r *mockSessionRepo) Delete(ctx context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.deleteHook != nil {
		if err := r.deleteHook(ctx, id); err != nil {
			return err
		}
	}
	if r.deleteErr != nil {
		return r.deleteErr
	}
	delete(r.sessions, id)

	return nil
}

type mockReviewRepo struct {
	mu        sync.Mutex
	cycles    map[string]domain.ReviewCycle
	critiques map[string]domain.Critique
	bySessID  map[string][]string // sessionID → []cycleID
	byCycleID map[string][]string // cycleID → []critiqueID
}

func newMockReviewRepo() *mockReviewRepo {
	return &mockReviewRepo{
		cycles:    make(map[string]domain.ReviewCycle),
		critiques: make(map[string]domain.Critique),
		bySessID:  make(map[string][]string),
		byCycleID: make(map[string][]string),
	}
}

func (r *mockReviewRepo) GetCycle(_ context.Context, id string) (domain.ReviewCycle, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok := r.cycles[id]; ok {
		return c, nil
	}

	return domain.ReviewCycle{}, repository.ErrNotFound
}

func (r *mockReviewRepo) ListCyclesBySessionID(_ context.Context, sessionID string) ([]domain.ReviewCycle, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var result []domain.ReviewCycle
	for _, id := range r.bySessID[sessionID] {
		if c, ok := r.cycles[id]; ok {
			result = append(result, c)
		}
	}

	return result, nil
}

func (r *mockReviewRepo) CreateCycle(_ context.Context, rc domain.ReviewCycle) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cycles[rc.ID] = rc
	r.bySessID[rc.AgentSessionID] = append(r.bySessID[rc.AgentSessionID], rc.ID)

	return nil
}

func (r *mockReviewRepo) UpdateCycle(_ context.Context, rc domain.ReviewCycle) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.cycles[rc.ID]; !ok {
		return repository.ErrNotFound
	}
	r.cycles[rc.ID] = rc

	return nil
}

func (r *mockReviewRepo) GetCritique(_ context.Context, id string) (domain.Critique, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok := r.critiques[id]; ok {
		return c, nil
	}

	return domain.Critique{}, repository.ErrNotFound
}

func (r *mockReviewRepo) ListCritiquesByReviewCycleID(_ context.Context, cycleID string) ([]domain.Critique, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var result []domain.Critique
	for _, id := range r.byCycleID[cycleID] {
		if c, ok := r.critiques[id]; ok {
			result = append(result, c)
		}
	}

	return result, nil
}

func (r *mockReviewRepo) CreateCritique(_ context.Context, c domain.Critique) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.critiques[c.ID] = c
	r.byCycleID[c.ReviewCycleID] = append(r.byCycleID[c.ReviewCycleID], c.ID)

	return nil
}

func (r *mockReviewRepo) UpdateCritique(_ context.Context, c domain.Critique) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.critiques[c.ID]; !ok {
		return repository.ErrNotFound
	}
	r.critiques[c.ID] = c

	return nil
}

type mockQuestionRepo struct {
	mu        sync.Mutex
	questions map[string]domain.Question
}

func newMockQuestionRepo() *mockQuestionRepo {
	return &mockQuestionRepo{questions: make(map[string]domain.Question)}
}

func (r *mockQuestionRepo) Get(_ context.Context, id string) (domain.Question, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if q, ok := r.questions[id]; ok {
		return q, nil
	}

	return domain.Question{}, repository.ErrNotFound
}

func (r *mockQuestionRepo) ListBySessionID(_ context.Context, _ string) ([]domain.Question, error) {
	return nil, nil
}

func (r *mockQuestionRepo) Create(_ context.Context, q domain.Question) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.questions[q.ID] = q

	return nil
}

func (r *mockQuestionRepo) Update(_ context.Context, q domain.Question) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.questions[q.ID] = q

	return nil
}

func (r *mockQuestionRepo) UpdateProposedAnswer(_ context.Context, id, proposedAnswer string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if q, ok := r.questions[id]; ok {
		q.ProposedAnswer = proposedAnswer
		r.questions[id] = q
	}

	return nil
}

type mockWorkItemRepo struct{}

func (r *mockWorkItemRepo) Get(_ context.Context, _ string) (domain.Session, error) {
	return domain.Session{}, repository.ErrNotFound
}

func (r *mockWorkItemRepo) List(_ context.Context, _ repository.SessionFilter) ([]domain.Session, error) {
	return nil, nil
}
func (r *mockWorkItemRepo) Create(_ context.Context, _ domain.Session) error { return nil }
func (r *mockWorkItemRepo) Update(_ context.Context, _ domain.Session) error { return nil }
func (r *mockWorkItemRepo) Delete(_ context.Context, _ string) error         { return nil }

// ============================================================
// Test helpers
// ============================================================

// testReviewConfig returns a minimal config for review pipeline tests.
func testReviewConfig(maxCycles int) *config.Config {
	cfg := &config.Config{}
	cfg.Review.MaxCycles = ptrInt(maxCycles)
	cfg.Review.PassThreshold = config.PassThresholdMinorOK // majors trigger re-impl
	cfg.Plan.MaxParseRetries = ptrInt(2)
	cfg.Foreman.QuestionTimeout = "5s"

	return cfg
}

// reviewPipelineFixture holds everything needed to run ReviewSession tests.
type reviewPipelineFixture struct {
	pipeline    *ReviewPipeline
	reviewRepo  *mockReviewRepo
	planRepo    *mockPlanRepo
	subPlanRepo *mockSubPlanRepo
	harness     *mockAgentHarness
	sessionsDir string // temp sessions dir (SUBSTRATE_HOME/sessions)
	cleanup     func()
}

// newReviewPipelineFixture wires up a ReviewPipeline with test doubles.
// It sets SUBSTRATE_HOME to a temp dir so readSessionOutputFromLog finds log files.
func newReviewPipelineFixture(t *testing.T, maxCycles int) *reviewPipelineFixture {
	t.Helper()

	tmpDir := t.TempDir()
	sessionsDir := filepath.Join(tmpDir, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatalf("create sessions dir: %v", err)
	}

	// Set SUBSTRATE_HOME so config.GlobalDir() returns tmpDir.
	t.Setenv("SUBSTRATE_HOME", tmpDir)
	cleanup := func() {}

	reviewRepo := newMockReviewRepo()
	planRepo := newMockPlanRepo()
	subPlanRepo := newMockSubPlanRepo()
	sessionRepo := newMockSessionRepo()
	questionRepo := newMockQuestionRepo()
	workItemRepo := &mockWorkItemRepo{}
	harness := &mockAgentHarness{sessionsDir: sessionsDir}

	cfg := testReviewConfig(maxCycles)
	reviewSvc := service.NewReviewService(repository.NoopTransacter{Res: repository.Resources{Reviews: reviewRepo}})
	planSvc := service.NewPlanService(repository.NoopTransacter{Res: repository.Resources{Plans: planRepo, SubPlans: subPlanRepo}})
	sessionSvc := service.NewTaskService(repository.NoopTransacter{Res: repository.Resources{Tasks: sessionRepo}})
	workItemSvc := service.NewSessionService(repository.NoopTransacter{Res: repository.Resources{Sessions: workItemRepo}})
	bus := event.NewBus(event.BusConfig{}) // nil EventRepo → no persistence, OK for tests
	_ = questionRepo

	pipeline := NewReviewPipeline(cfg, harness, reviewSvc, sessionSvc, planSvc, workItemSvc, bus, nil)

	return &reviewPipelineFixture{
		pipeline:    pipeline,
		reviewRepo:  reviewRepo,
		planRepo:    planRepo,
		subPlanRepo: subPlanRepo,
		harness:     harness,
		sessionsDir: sessionsDir,
		cleanup:     cleanup,
	}
}

// seedPlanAndSubPlan creates a plan+subplan in the fixture's repos and returns
// a domain.Task whose SubPlanID points to the sub-plan.
func (f *reviewPipelineFixture) seedPlanAndSubPlan(t *testing.T) domain.Task {
	t.Helper()

	planID := "plan-1"
	subPlanID := "sub-plan-1"

	plan := domain.Plan{ID: planID, WorkItemID: "wi-1", OrchestratorPlan: "test plan"}
	if err := f.planRepo.Create(context.Background(), plan); err != nil {
		t.Fatalf("create plan: %v", err)
	}

	subPlan := domain.TaskPlan{ID: subPlanID, PlanID: planID, Content: "test sub-plan", Order: 0}
	if err := f.subPlanRepo.Create(context.Background(), subPlan); err != nil {
		t.Fatalf("create sub-plan: %v", err)
	}

	return domain.Task{
		ID:             "session-1",
		WorkItemID:     "wi-1",
		WorkspaceID:    "ws-1",
		Phase:          domain.TaskPhaseImplementation,
		SubPlanID:      subPlanID,
		RepositoryName: "repo-a",
		HarnessName:    "mock",
		Status:         domain.AgentSessionCompleted,
	}
}

// twoMajorCritiquesOutput returns output text that parses to 2 major critiques.
func twoMajorCritiquesOutput() string {
	return strings.Join([]string{
		"CRITIQUE",
		"File: general",
		"Severity: major",
		"Description: First major issue requiring fix",
		"END_CRITIQUE",
		"CRITIQUE",
		"File: main.go",
		"Severity: major",
		"Description: Second major issue requiring fix",
		"END_CRITIQUE",
	}, "\n")
}

// ============================================================
// Phase 9 tests: Foreman — question resolution
// ============================================================

// TestWaitForAnswer_HighConfidence verifies that a foreman_proposed event with
// uncertain=false is treated as a confident answer (no human escalation).
func TestWaitForAnswer_HighConfidence(t *testing.T) {
	cfg := testReviewConfig(3)
	f := &Foreman{cfg: cfg}

	// Build a mock session that immediately emits foreman_proposed with uncertain=false.
	sess := newMockSession("foreman-session-1",
		adapter.AgentEvent{
			Type:     "foreman_proposed",
			Payload:  "The answer is 42.",
			Metadata: map[string]any{"uncertain": false},
		},
	)

	ctx := context.Background()
	answer, uncertain, err := f.waitForAnswer(ctx, sess)
	if err != nil {
		t.Fatalf("waitForAnswer returned error: %v", err)
	}
	if uncertain {
		t.Error("expected uncertain=false for high-confidence answer, got uncertain=true")
	}
	if answer != "The answer is 42." {
		t.Errorf("expected answer %q, got %q", "The answer is 42.", answer)
	}
}

// TestWaitForAnswer_Uncertain verifies that a foreman_proposed event with
// uncertain=true triggers escalation to human (uncertain=true returned).
func TestWaitForAnswer_Uncertain(t *testing.T) {
	cfg := testReviewConfig(3)
	f := &Foreman{cfg: cfg}

	sess := newMockSession("foreman-session-2",
		adapter.AgentEvent{
			Type:     "foreman_proposed",
			Payload:  "I'm not sure but maybe...",
			Metadata: map[string]any{"uncertain": true},
		},
	)

	ctx := context.Background()
	answer, uncertain, err := f.waitForAnswer(ctx, sess)
	if err != nil {
		t.Fatalf("waitForAnswer returned error: %v", err)
	}
	if !uncertain {
		t.Error("expected uncertain=true for uncertain answer, got uncertain=false")
	}
	if answer == "" {
		t.Error("expected non-empty proposed answer even when uncertain")
	}
}

// TestWaitForAnswer_IgnoresNonForemanEvents verifies that text_delta and other
// events are skipped; only foreman_proposed is acted on.
func TestWaitForAnswer_IgnoresNonForemanEvents(t *testing.T) {
	cfg := testReviewConfig(3)
	f := &Foreman{cfg: cfg}

	sess := newMockSession("foreman-session-3",
		adapter.AgentEvent{Type: "text_delta", Payload: "thinking..."},
		adapter.AgentEvent{Type: "text_delta", Payload: "still thinking..."},
		adapter.AgentEvent{
			Type:     "foreman_proposed",
			Payload:  "Final answer.",
			Metadata: map[string]any{"uncertain": false},
		},
	)

	ctx := context.Background()
	answer, uncertain, err := f.waitForAnswer(ctx, sess)
	if err != nil {
		t.Fatalf("waitForAnswer returned error: %v", err)
	}
	if uncertain {
		t.Error("expected uncertain=false")
	}
	if answer != "Final answer." {
		t.Errorf("expected %q, got %q", "Final Answer.", answer)
	}
}

// ============================================================
// Phase 9 tests: ReviewPipeline — review cycle behavior
// ============================================================

// TestReviewSession_NoCritiques_Passes verifies that when the review agent outputs
// NO_CRITIQUES the session is marked as passed on the first cycle.
func TestReviewSession_NoCritiques_Passes(t *testing.T) {
	fix := newReviewPipelineFixture(t, 3)
	defer fix.cleanup()

	fix.harness.outputs = []string{"NO_CRITIQUES"}
	agentSession := fix.seedPlanAndSubPlan(t)

	result, err := fix.pipeline.ReviewSession(context.Background(), agentSession)
	if err != nil {
		t.Fatalf("ReviewSession: %v", err)
	}
	if !result.Passed {
		t.Errorf("expected Passed=true, got Passed=false (NeedsReimpl=%v, Escalated=%v)", result.NeedsReimpl, result.Escalated)
	}
	if result.CycleNumber != 1 {
		t.Errorf("expected CycleNumber=1, got %d", result.CycleNumber)
	}
}

// TestReviewSession_MajorCritiques_NeedsReimpl verifies that 2 major critiques
// in the review output cause NeedsReimpl=true on the first cycle.
func TestReviewSession_MajorCritiques_NeedsReimpl(t *testing.T) {
	fix := newReviewPipelineFixture(t, 3)
	defer fix.cleanup()

	fix.harness.outputs = []string{twoMajorCritiquesOutput()}
	agentSession := fix.seedPlanAndSubPlan(t)

	result, err := fix.pipeline.ReviewSession(context.Background(), agentSession)
	if err != nil {
		t.Fatalf("ReviewSession: %v", err)
	}
	if result.Passed {
		t.Error("expected Passed=false with major critiques")
	}
	if !result.NeedsReimpl {
		t.Error("expected NeedsReimpl=true with major critiques")
	}
	if result.Escalated {
		t.Error("expected Escalated=false on first cycle with majors")
	}
	if len(result.Critiques) != 2 {
		t.Errorf("expected 2 critiques, got %d", len(result.Critiques))
	}
}

// TestReviewSession_RoundOneFailsRoundTwoPasses tests the canonical Phase 9 scenario:
// cycle 1 → 2 major critiques (NeedsReimpl) → re-implement → cycle 2 → NO_CRITIQUES (Passed).
func TestReviewSession_RoundOneFailsRoundTwoPasses(t *testing.T) {
	fix := newReviewPipelineFixture(t, 3)
	defer fix.cleanup()

	agentSession := fix.seedPlanAndSubPlan(t)

	// Pre-populate: round 1 fails, round 2 passes.
	fix.harness.outputs = []string{twoMajorCritiquesOutput(), "NO_CRITIQUES"}

	// Round 1: 2 major critiques.
	round1, err := fix.pipeline.ReviewSession(context.Background(), agentSession)
	if err != nil {
		t.Fatalf("ReviewSession round 1: %v", err)
	}
	if round1.Passed {
		t.Fatal("round 1: expected Passed=false")
	}
	if !round1.NeedsReimpl {
		t.Fatal("round 1: expected NeedsReimpl=true")
	}

	// Round 2: NO_CRITIQUES after re-implementation.
	round2, err := fix.pipeline.ReviewSession(context.Background(), agentSession)
	if err != nil {
		t.Fatalf("ReviewSession round 2: %v", err)
	}
	if !round2.Passed {
		t.Fatalf("round 2: expected Passed=true, got NeedsReimpl=%v Escalated=%v", round2.NeedsReimpl, round2.Escalated)
	}
	if round2.CycleNumber != 2 {
		t.Errorf("round 2: expected CycleNumber=2, got %d", round2.CycleNumber)
	}
}

// TestReviewSession_ThreeRoundsOfMajors_Escalates tests that after hitting
// the cycle limit with persistent major critiques, the pipeline escalates.
// Gate requirement: "3 rounds of majors → escalated".
func TestReviewSession_ThreeRoundsOfMajors_Escalates(t *testing.T) {
	// maxCycles=3: after 3 real cycles the 4th call exceeds the limit.
	fix := newReviewPipelineFixture(t, 3)
	defer fix.cleanup()

	agentSession := fix.seedPlanAndSubPlan(t)
	majors := twoMajorCritiquesOutput()

	// Pre-populate: 3 rounds of major critiques.
	fix.harness.outputs = []string{majors, majors, majors}

	for round := 1; round <= 3; round++ {
		result, err := fix.pipeline.ReviewSession(context.Background(), agentSession)
		if err != nil {
			t.Fatalf("ReviewSession round %d: %v", round, err)
		}
		if result.Passed {
			t.Fatalf("round %d: expected Passed=false with major critiques", round)
		}
		if !result.NeedsReimpl {
			t.Fatalf("round %d: expected NeedsReimpl=true", round)
		}
		if result.Escalated {
			t.Fatalf("round %d: expected Escalated=false (cycle limit not yet reached)", round)
		}
	}

	// 4th call: cycleNumber=4 > maxCycles=3 → Escalated.
	// No harness output needed; escalation should occur before a new session is started.
	result, err := fix.pipeline.ReviewSession(context.Background(), agentSession)
	if err != nil {
		t.Fatalf("ReviewSession round 4 (escalation): %v", err)
	}
	if !result.Escalated {
		t.Errorf("expected Escalated=true after 3 failed cycles, got Passed=%v NeedsReimpl=%v", result.Passed, result.NeedsReimpl)
	}
}

// TestResolveEscalated_AppendsFAQ verifies that human-answered escalated questions
// are appended to the plan's FAQ, not just persisted on the question row.
func TestResolveEscalated_AppendsFAQ(t *testing.T) {
	planRepo := newMockPlanRepo()
	questionRepo := newMockQuestionRepo()
	sessionRepo := newMockSessionRepo()

	planRepo.plans["plan-1"] = domain.Plan{ID: "plan-1", WorkItemID: "wi-1"}
	questionRepo.questions["q-1"] = domain.Question{
		ID:             "q-1",
		AgentSessionID: "sess-1",
		Content:        "Which pattern should I use?",
		Status:         domain.QuestionEscalated,
	}
	sessionRepo.sessions["sess-1"] = domain.Task{
		ID:             "sess-1",
		RepositoryName: "repo-a",
	}

	planSvc := service.NewPlanService(repository.NoopTransacter{Res: repository.Resources{Plans: planRepo}})
	questionSvc := service.NewQuestionService(repository.NoopTransacter{Res: repository.Resources{Questions: questionRepo}})
	sessionSvc := service.NewTaskService(repository.NoopTransacter{Res: repository.Resources{Tasks: sessionRepo}})

	answerCh := make(chan string, 1)
	f := &Foreman{
		cfg:         testReviewConfig(3),
		planSvc:     planSvc,
		questionSvc: questionSvc,
		sessionSvc:  sessionSvc,
		escalatedChs: map[string]escalatedEntry{
			"q-1": {answerCh: answerCh, agentSessionID: "sess-1"},
		},
		planID: "plan-1",
	}

	err := f.ResolveEscalated(context.Background(), "q-1", "Use the repository pattern.")
	if err != nil {
		t.Fatalf("ResolveEscalated: %v", err)
	}

	if len(planRepo.faqAdded) != 1 {
		t.Fatalf("expected 1 FAQ entry, got %d", len(planRepo.faqAdded))
	}

	entry := planRepo.faqAdded[0]
	if entry.PlanID != "plan-1" {
		t.Errorf("expected PlanID %q, got %q", "plan-1", entry.PlanID)
	}
	if entry.AnsweredBy != "human" {
		t.Errorf("expected AnsweredBy %q, got %q", "human", entry.AnsweredBy)
	}
	if entry.Question != "Which pattern should I use?" {
		t.Errorf("expected Question %q, got %q", "Which pattern should I use?", entry.Question)
	}
	if entry.Answer != "Use the repository pattern." {
		t.Errorf("expected Answer %q, got %q", "Use the repository pattern.", entry.Answer)
	}
	if entry.RepoName != "repo-a" {
		t.Errorf("expected RepoName %q, got %q", "repo-a", entry.RepoName)
	}
	if entry.AgentSessionID != "sess-1" {
		t.Errorf("expected AgentSessionID %q, got %q", "sess-1", entry.AgentSessionID)
	}
}

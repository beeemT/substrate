package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/event"
	"github.com/beeemT/substrate/internal/repository"
	"github.com/beeemT/substrate/internal/service"
)

// ============================================================
// Mock: InstanceRepository
// ============================================================

type mockInstanceRepo struct {
	mu          sync.Mutex
	instances   map[string]domain.SubstrateInstance
	byWorkspace map[string][]string // workspaceID → []instanceID
}

func newMockInstanceRepo() *mockInstanceRepo {
	return &mockInstanceRepo{
		instances:   make(map[string]domain.SubstrateInstance),
		byWorkspace: make(map[string][]string),
	}
}

func (r *mockInstanceRepo) Get(_ context.Context, id string) (domain.SubstrateInstance, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if inst, ok := r.instances[id]; ok {
		return inst, nil
	}

	return domain.SubstrateInstance{}, repository.ErrNotFound
}

func (r *mockInstanceRepo) ListByWorkspaceID(_ context.Context, workspaceID string) ([]domain.SubstrateInstance, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ids := r.byWorkspace[workspaceID]
	result := make([]domain.SubstrateInstance, 0, len(ids))
	for _, id := range ids {
		if inst, ok := r.instances[id]; ok {
			result = append(result, inst)
		}
	}

	return result, nil
}

func (r *mockInstanceRepo) Create(_ context.Context, inst domain.SubstrateInstance) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.instances[inst.ID] = inst
	r.byWorkspace[inst.WorkspaceID] = append(r.byWorkspace[inst.WorkspaceID], inst.ID)

	return nil
}

func (r *mockInstanceRepo) Update(_ context.Context, inst domain.SubstrateInstance) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.instances[inst.ID]; !ok {
		return repository.ErrNotFound
	}
	r.instances[inst.ID] = inst

	return nil
}

func (r *mockInstanceRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	inst, ok := r.instances[id]
	if !ok {
		return repository.ErrNotFound
	}
	// Remove from workspace index.
	ids := r.byWorkspace[inst.WorkspaceID]
	filtered := ids[:0]
	for _, existing := range ids {
		if existing != id {
			filtered = append(filtered, existing)
		}
	}
	r.byWorkspace[inst.WorkspaceID] = filtered
	delete(r.instances, id)

	return nil
}

// ============================================================
// captureHarness: records SessionOpts passed to StartSession
// ============================================================

type captureHarness struct {
	sessionsDir string
	mu          sync.Mutex
	captured    []adapter.SessionOpts
	returnErr   error
	abortErr    error
	lastSession *mockAgentSession
}

func (h *captureHarness) Name() string { return "capture" }

func (h *captureHarness) StartSession(_ context.Context, opts adapter.SessionOpts) (adapter.AgentSession, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.returnErr != nil {
		return nil, h.returnErr
	}
	h.captured = append(h.captured, opts)
	// Write a minimal log so downstream reads don't fail.
	if h.sessionsDir != "" {
		logPath := filepath.Join(h.sessionsDir, opts.SessionID+".log")
		_ = os.WriteFile(logPath, []byte(`{"type":"event","event":{"type":"progress","text":"ok"}}`+"\n"), 0o644)
	}
	session := newMockSession(opts.SessionID, adapter.AgentEvent{Type: "done"})
	session.abortErr = h.abortErr
	h.lastSession = session

	return session, nil
}

// lastOpts returns the SessionOpts from the most recent StartSession call.
func (h *captureHarness) lastOpts() adapter.SessionOpts {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.captured) == 0 {
		return adapter.SessionOpts{}
	}

	return h.captured[len(h.captured)-1]
}

// ============================================================
// Phase 9b test fixture
// ============================================================

type phase9bFixture struct {
	instanceRepo *mockInstanceRepo
	sessionRepo  *mockSessionRepo // reused from phase9_test.go
	subPlanRepo  *mockSubPlanRepo
	planRepo     *mockPlanRepo
	instanceSvc  *service.InstanceService
	sessionSvc   *service.TaskService
	planSvc      *service.PlanService
	bus          *event.Bus
	workspaceID  string
}

func newPhase9bFixture() *phase9bFixture {
	instanceRepo := newMockInstanceRepo()
	sessionRepo := newMockSessionRepo()
	subPlanRepo := newMockSubPlanRepo()
	planRepo := newMockPlanRepo()

	// Seed a sub-plan so ResumeSession can look it up.
	subPlanRepo.subPlans["sub-plan-1"] = domain.TaskPlan{
		ID:      "sub-plan-1",
		PlanID:  "plan-1",
		Content: "Implement the auth module",
		Order:   0,
	}
	planRepo.plans["plan-1"] = domain.Plan{ID: "plan-1", WorkItemID: "wi-1"}

	planSvc := service.NewPlanService(repository.NoopTransacter{Res: repository.Resources{Plans: planRepo, SubPlans: subPlanRepo}})

	return &phase9bFixture{
		instanceRepo: instanceRepo,
		sessionRepo:  sessionRepo,
		subPlanRepo:  subPlanRepo,
		planRepo:     planRepo,
		instanceSvc:  service.NewInstanceService(repository.NoopTransacter{Res: repository.Resources{Instances: instanceRepo}}),
		sessionSvc:   service.NewTaskService(repository.NoopTransacter{Res: repository.Resources{Tasks: sessionRepo}}),
		planSvc:      planSvc,
		bus:          event.NewBus(event.BusConfig{}),
		workspaceID:  "ws-test",
	}
}

// seedRunningSession inserts a running session owned by ownerInstanceID.
func (f *phase9bFixture) seedRunningSession(sessionID, ownerInstanceID string) {
	owner := ownerInstanceID
	f.sessionRepo.sessions[sessionID] = domain.Task{
		ID:              sessionID,
		WorkItemID:      "wi-1",
		WorkspaceID:     f.workspaceID,
		Phase:           domain.TaskPhaseImplementation,
		SubPlanID:       "sub-plan-1",
		RepositoryName:  "repo-a",
		WorktreePath:    "/tmp/worktrees/repo-a",
		HarnessName:     "mock",
		Status:          domain.AgentSessionRunning,
		OwnerInstanceID: &owner,
	}
}

// seedInterruptedSession inserts an interrupted session.
func (f *phase9bFixture) seedInterruptedSession(sessionID string) {
	f.sessionRepo.sessions[sessionID] = domain.Task{
		ID:             sessionID,
		WorkItemID:     "wi-1",
		WorkspaceID:    f.workspaceID,
		Phase:          domain.TaskPhaseImplementation,
		SubPlanID:      "sub-plan-1",
		RepositoryName: "repo-a",
		WorktreePath:   "/tmp/worktrees/repo-a",
		HarnessName:    "mock",
		Status:         domain.AgentSessionInterrupted,
	}
}

// seedInterruptedSessionWithResumeInfo inserts an interrupted session with ResumeInfo.
func (f *phase9bFixture) seedInterruptedSessionWithResumeInfo(sessionID string, resumeInfo map[string]string) {
	f.sessionRepo.sessions[sessionID] = domain.Task{
		ID:             sessionID,
		WorkItemID:     "wi-1",
		WorkspaceID:    f.workspaceID,
		Phase:          domain.TaskPhaseImplementation,
		SubPlanID:      "sub-plan-1",
		RepositoryName: "repo-a",
		WorktreePath:   "/tmp/worktrees/repo-a",
		HarnessName:    "mock",
		Status:         domain.AgentSessionInterrupted,
		ResumeInfo:     resumeInfo,
	}
}

func (f *phase9bFixture) getSessionStatus(id string) domain.TaskStatus {
	f.sessionRepo.mu.Lock()
	defer f.sessionRepo.mu.Unlock()

	return f.sessionRepo.sessions[id].Status
}

// ============================================================
// Resumption tests
// ============================================================

// TestAbandonSession_TransitionsToFailed verifies the abandon path.
func TestAbandonSession_TransitionsToFailed(t *testing.T) {
	fix := newPhase9bFixture()
	fix.seedInterruptedSession("sess-abandoned")

	r := NewResumption(&captureHarness{sessionsDir: t.TempDir()}, fix.sessionSvc, fix.planSvc, fix.bus, nil)

	if err := r.AbandonSession(context.Background(), "sess-abandoned"); err != nil {
		t.Fatalf("AbandonSession: %v", err)
	}
	if got := fix.getSessionStatus("sess-abandoned"); got != domain.AgentSessionFailed {
		t.Errorf("expected failed, got %q", got)
	}
}

// TestAbandonSession_RejectsNonInterrupted ensures only interrupted sessions
// can be abandoned.
func TestAbandonSession_RejectsNonInterrupted(t *testing.T) {
	fix := newPhase9bFixture()
	fix.seedRunningSession("sess-running", "inst-x")

	r := NewResumption(&captureHarness{sessionsDir: t.TempDir()}, fix.sessionSvc, fix.planSvc, fix.bus, nil)

	if err := r.AbandonSession(context.Background(), "sess-running"); err == nil {
		t.Error("expected error when abandoning a non-interrupted session, got nil")
	}
}

// TestResumeSession_StartsNewSessionWithLogContext is the core Phase 9b resume test:
// a new session is created in the same worktree, the system prompt contains the
// last lines of the interrupted session's log plus the sub-plan content, and
// EventAgentSessionResumed is published.
func TestResumeSession_StartsNewSessionWithLogContext(t *testing.T) {
	fix := newPhase9bFixture()
	ctx := context.Background()

	// Set SUBSTRATE_HOME to a temp dir so readLastNLines finds the log.
	tmpDir := t.TempDir()
	sessionsDir := filepath.Join(tmpDir, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatalf("create sessions dir: %v", err)
	}
	t.Setenv("SUBSTRATE_HOME", tmpDir)

	intID := "sess-interrupted"
	fix.seedInterruptedSession(intID)

	// Write a log file with recognisable content.
	logContent := strings.Join([]string{
		`{"type":"event","event":{"type":"assistant_output","text":"started working"}}`,
		`{"type":"event","event":{"type":"assistant_output","text":"wrote main.go"}}`,
		`{"type":"event","event":{"type":"assistant_output","text":"all tests passing"}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(sessionsDir, intID+".log"), []byte(logContent), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}

	// Subscribe to the bus to capture AgentSessionResumed events.
	sub, err := fix.bus.Subscribe("test-resume-sub", string(domain.EventAgentSessionResumed))
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer fix.bus.Unsubscribe(sub.ID)

	harness := &captureHarness{sessionsDir: sessionsDir}
	resumption := NewResumption(harness, fix.sessionSvc, fix.planSvc, fix.bus, nil)

	interrupted := fix.sessionRepo.sessions[intID]
	result, err := resumption.ResumeSession(ctx, interrupted, "inst-new")
	if err != nil {
		t.Fatalf("ResumeSession: %v", err)
	}

	// New session must be linked to the same sub-plan and be running.
	if result.NewSession.SubPlanID != "sub-plan-1" {
		t.Errorf("expected SubPlanID=sub-plan-1, got %q", result.NewSession.SubPlanID)
	}
	if got := fix.getSessionStatus(result.NewSession.ID); got != domain.AgentSessionRunning {
		t.Errorf("new session: expected running, got %q", got)
	}

	// Old session must be transitioned to failed (superseded by the new session).
	if got := fix.getSessionStatus(intID); got != domain.AgentSessionFailed {
		t.Errorf("old session: expected failed, got %q", got)
	}

	// System prompt must contain the log content and the sub-plan.
	opts := harness.lastOpts()
	if !strings.Contains(opts.SystemPrompt, "wrote main.go") {
		t.Errorf("system prompt missing log context; got:\n%s", opts.SystemPrompt)
	}
	if !strings.Contains(opts.SystemPrompt, "Implement the auth module") {
		t.Errorf("system prompt missing sub-plan content; got:\n%s", opts.SystemPrompt)
	}

	// EventAgentSessionResumed must be published with both session IDs.
	select {
	case evt := <-sub.C:
		if !strings.Contains(evt.Payload, result.NewSession.ID) {
			t.Errorf("event payload missing new session ID; payload: %s", evt.Payload)
		}
		if !strings.Contains(evt.Payload, intID) {
			t.Errorf("event payload missing old session ID; payload: %s", evt.Payload)
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("timed out waiting for EventAgentSessionResumed")
	}
}

// TestResumeSession_OldSessionTransitionsToFailed verifies that
// ResumeSession transitions the superseded interrupted session to failed.
func TestResumeSession_OldSessionTransitionsToFailed(t *testing.T) {
	fix := newPhase9bFixture()
	ctx := context.Background()

	tmpDir := t.TempDir()
	sessionsDir := filepath.Join(tmpDir, "sessions")
	_ = os.MkdirAll(sessionsDir, 0o755)
	t.Setenv("SUBSTRATE_HOME", tmpDir)

	fix.seedInterruptedSession("sess-int2")
	// No log file — readLastNLines falls back to "unavailable" gracefully.

	harness := &captureHarness{sessionsDir: sessionsDir}
	r := NewResumption(harness, fix.sessionSvc, fix.planSvc, fix.bus, nil)

	interrupted := fix.sessionRepo.sessions["sess-int2"]
	_, err := r.ResumeSession(ctx, interrupted, "inst-new2")
	if err != nil {
		t.Fatalf("ResumeSession: %v", err)
	}

	if got := fix.getSessionStatus("sess-int2"); got != domain.AgentSessionFailed {
		t.Errorf("old session must be failed after resume; got %q", got)
	}
}

func TestResumeSession_StartTransitionFailureDeletesPendingSessionWithoutStartingHarness(t *testing.T) {
	fix := newPhase9bFixture()
	ctx := context.Background()

	tmpDir := t.TempDir()
	sessionsDir := filepath.Join(tmpDir, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatalf("create sessions dir: %v", err)
	}
	t.Setenv("SUBSTRATE_HOME", tmpDir)

	fix.seedInterruptedSession("sess-int3")
	fix.sessionRepo.updateErr = repository.ErrNotFound
	fix.sessionRepo.updateErrStatus = domain.AgentSessionRunning

	harness := &captureHarness{sessionsDir: sessionsDir}
	r := NewResumption(harness, fix.sessionSvc, fix.planSvc, fix.bus, nil)
	interrupted := fix.sessionRepo.sessions["sess-int3"]
	_, err := r.ResumeSession(ctx, interrupted, "inst-new3")
	if err == nil {
		t.Fatal("expected ResumeSession to fail when transition to running fails")
	}
	if !strings.Contains(err.Error(), "transition resumed session to running") {
		t.Fatalf("expected original start-transition error, got %v", err)
	}

	if len(harness.captured) != 0 {
		t.Fatalf("expected no harness start, got %d", len(harness.captured))
	}
	for id, session := range fix.sessionRepo.sessions {
		if id == "sess-int3" {
			continue
		}
		if session.Status == domain.AgentSessionPending {
			t.Fatalf("unexpected pending resumed session left behind: %+v", session)
		}
	}
	if got := fix.getSessionStatus("sess-int3"); got != domain.AgentSessionInterrupted {
		t.Errorf("old session must remain interrupted; got %q", got)
	}
}

func TestResumeSession_StartTransitionFailureCleansPendingSessionAfterCancellation(t *testing.T) {
	fix := newPhase9bFixture()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tmpDir := t.TempDir()
	sessionsDir := filepath.Join(tmpDir, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatalf("create sessions dir: %v", err)
	}
	t.Setenv("SUBSTRATE_HOME", tmpDir)

	fix.seedInterruptedSession("sess-int4")
	fix.sessionRepo.updateHook = func(_ context.Context, session domain.Task) error {
		if session.Status == domain.AgentSessionRunning {
			cancel()
		}

		return nil
	}
	fix.sessionRepo.updateErr = repository.ErrNotFound
	fix.sessionRepo.updateErrStatus = domain.AgentSessionRunning
	fix.sessionRepo.deleteHook = func(ctx context.Context, _ string) error {
		return ctx.Err()
	}

	harness := &captureHarness{sessionsDir: sessionsDir}
	r := NewResumption(harness, fix.sessionSvc, fix.planSvc, fix.bus, nil)

	interrupted := fix.sessionRepo.sessions["sess-int4"]
	_, err := r.ResumeSession(ctx, interrupted, "inst-new4")
	if err == nil {
		t.Fatal("expected ResumeSession to fail when transition to running fails")
	}
	if !strings.Contains(err.Error(), "transition resumed session to running") {
		t.Fatalf("expected original start-transition error, got %v", err)
	}
	if len(harness.captured) != 0 {
		t.Fatalf("expected no harness start, got %d", len(harness.captured))
	}
	for id, session := range fix.sessionRepo.sessions {
		if id == "sess-int4" {
			continue
		}
		if session.Status == domain.AgentSessionPending {
			t.Fatalf("unexpected pending resumed session left behind after cancellation: %+v", session)
		}
	}
	if got := fix.getSessionStatus("sess-int4"); got != domain.AgentSessionInterrupted {
		t.Errorf("old session must remain interrupted; got %q", got)
	}
}

// TestResumeSession_WithResumeInfo verifies that when the interrupted session has
// ResumeInfo, the harness receives ResumeFromSessionID and ResumeInfo in opts,
// UserPrompt is empty (harness resumes natively), and an orientation message
// is sent via SendMessage.
func TestResumeSession_WithResumeInfo(t *testing.T) {
	fix := newPhase9bFixture()
	ctx := context.Background()

	tmpDir := t.TempDir()
	sessionsDir := filepath.Join(tmpDir, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatalf("create sessions dir: %v", err)
	}
	t.Setenv("SUBSTRATE_HOME", tmpDir)

	intID := "sess-int-resume"
	fix.seedInterruptedSessionWithResumeInfo(intID, map[string]string{
		"omp_session_file": "/tmp/sessions/prev.json",
		"omp_session_id":   "prev-session-id",
	})

	harness := &captureHarness{sessionsDir: sessionsDir}
	resumption := NewResumption(harness, fix.sessionSvc, fix.planSvc, fix.bus, nil)

	interrupted := fix.sessionRepo.sessions[intID]
	result, err := resumption.ResumeSession(ctx, interrupted, "inst-resume")
	if err != nil {
		t.Fatalf("ResumeSession: %v", err)
	}

	// New session must be running.
	if got := fix.getSessionStatus(result.NewSession.ID); got != domain.AgentSessionRunning {
		t.Errorf("new session: expected running, got %q", got)
	}

	// Old session must be failed (superseded).
	if got := fix.getSessionStatus(intID); got != domain.AgentSessionFailed {
		t.Errorf("old session: expected failed, got %q", got)
	}

	// Harness must receive resume opts.
	opts := harness.lastOpts()
	if opts.ResumeFromSessionID != intID {
		t.Errorf("expected ResumeFromSessionID=%q, got %q", intID, opts.ResumeFromSessionID)
	}
	if len(opts.ResumeInfo) != 2 {
		t.Errorf("expected 2 ResumeInfo entries, got %d", len(opts.ResumeInfo))
	}
	if opts.ResumeInfo["omp_session_file"] != "/tmp/sessions/prev.json" {
		t.Errorf("ResumeInfo[omp_session_file] mismatch: %q", opts.ResumeInfo["omp_session_file"])
	}
	// UserPrompt must be empty when resuming natively.
	if opts.UserPrompt != "" {
		t.Errorf("expected empty UserPrompt when resuming natively, got %q", opts.UserPrompt)
	}

	// Orientation message must be sent via SendMessage.
	sess := harness.lastSession
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if len(sess.messages) != 1 {
		t.Fatalf("expected 1 SendMessage call, got %d", len(sess.messages))
	}
	if !strings.Contains(sess.messages[0], "interrupted") {
		t.Errorf("orientation message missing 'interrupted'; got: %q", sess.messages[0])
	}
}

// TestResumeSession_WithoutResumeInfo verifies the fallback path: when the
// interrupted session has no ResumeInfo, UserPrompt is set with orientation
// and no SendMessage is called.
func TestResumeSession_WithoutResumeInfo(t *testing.T) {
	fix := newPhase9bFixture()
	ctx := context.Background()

	tmpDir := t.TempDir()
	sessionsDir := filepath.Join(tmpDir, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatalf("create sessions dir: %v", err)
	}
	t.Setenv("SUBSTRATE_HOME", tmpDir)

	intID := "sess-int-no-resume"
	fix.seedInterruptedSession(intID)

	harness := &captureHarness{sessionsDir: sessionsDir}
	resumption := NewResumption(harness, fix.sessionSvc, fix.planSvc, fix.bus, nil)

	interrupted := fix.sessionRepo.sessions[intID]
	result, err := resumption.ResumeSession(ctx, interrupted, "inst-no-resume")
	if err != nil {
		t.Fatalf("ResumeSession: %v", err)
	}

	if got := fix.getSessionStatus(result.NewSession.ID); got != domain.AgentSessionRunning {
		t.Errorf("new session: expected running, got %q", got)
	}

	// No resume opts should be set.
	opts := harness.lastOpts()
	if opts.ResumeFromSessionID != "" {
		t.Errorf("expected empty ResumeFromSessionID, got %q", opts.ResumeFromSessionID)
	}
	if len(opts.ResumeInfo) != 0 {
		t.Errorf("expected no ResumeInfo, got %d entries", len(opts.ResumeInfo))
	}
	// UserPrompt must be set with orientation (fallback path).
	if opts.UserPrompt == "" {
		t.Error("expected non-empty UserPrompt in fallback path")
	}
	if !strings.Contains(opts.UserPrompt, "continuing work") {
		t.Errorf("UserPrompt missing orientation text; got: %q", opts.UserPrompt)
	}

	// No SendMessage should be called in fallback path.
	sess := harness.lastSession
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if len(sess.messages) != 0 {
		t.Errorf("expected 0 SendMessage calls in fallback path, got %d", len(sess.messages))
	}
}

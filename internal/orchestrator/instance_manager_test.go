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

	planSvc := service.NewPlanService(planRepo, subPlanRepo, service.NoopPlanTransacter{PlanRepo: planRepo, SubPlanRepo: subPlanRepo})

	return &phase9bFixture{
		instanceRepo: instanceRepo,
		sessionRepo:  sessionRepo,
		subPlanRepo:  subPlanRepo,
		planRepo:     planRepo,
		instanceSvc:  service.NewInstanceService(instanceRepo),
		sessionSvc:   service.NewTaskService(sessionRepo),
		planSvc:      planSvc,
		bus:          event.NewBus(event.BusConfig{}),
		workspaceID:  "ws-test",
	}
}

// seedStaleInstance inserts an instance whose heartbeat exceeds the stale threshold.
func (f *phase9bFixture) seedStaleInstance(id string) {
	inst := domain.SubstrateInstance{
		ID:            id,
		WorkspaceID:   f.workspaceID,
		PID:           99999,
		Hostname:      "dead-host",
		LastHeartbeat: time.Now().Add(-30 * time.Second),
		StartedAt:     time.Now().Add(-60 * time.Second),
	}
	f.instanceRepo.instances[id] = inst
	f.instanceRepo.byWorkspace[f.workspaceID] = append(f.instanceRepo.byWorkspace[f.workspaceID], id)
}

// seedLiveInstance inserts an instance with a fresh heartbeat.
func (f *phase9bFixture) seedLiveInstance(id string) {
	inst := domain.SubstrateInstance{
		ID:            id,
		WorkspaceID:   f.workspaceID,
		PID:           12345,
		Hostname:      "live-host",
		LastHeartbeat: time.Now(),
		StartedAt:     time.Now().Add(-10 * time.Second),
	}
	f.instanceRepo.instances[id] = inst
	f.instanceRepo.byWorkspace[f.workspaceID] = append(f.instanceRepo.byWorkspace[f.workspaceID], id)
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

func (f *phase9bFixture) getSessionStatus(id string) domain.TaskStatus {
	f.sessionRepo.mu.Lock()
	defer f.sessionRepo.mu.Unlock()

	return f.sessionRepo.sessions[id].Status
}

func (f *phase9bFixture) instanceExists(id string) bool {
	f.instanceRepo.mu.Lock()
	defer f.instanceRepo.mu.Unlock()
	_, ok := f.instanceRepo.instances[id]

	return ok
}

// ============================================================
// InstanceManager tests
// ============================================================

// TestReconcile_MarksOrphanedRunningSessionInterrupted verifies that a running
// session owned by a stale instance is transitioned to interrupted.
func TestReconcile_MarksOrphanedRunningSessionInterrupted(t *testing.T) {
	fix := newPhase9bFixture()
	fix.seedStaleInstance("inst-stale")
	fix.seedRunningSession("sess-orphaned", "inst-stale")

	mgr := NewInstanceManager(fix.instanceSvc, fix.sessionSvc, fix.bus)
	mgr.workspaceID = fix.workspaceID
	mgr.instanceID = "inst-new" // current instance; not in DB for this test

	if err := mgr.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if got := fix.getSessionStatus("sess-orphaned"); got != domain.AgentSessionInterrupted {
		t.Errorf("expected interrupted, got %q", got)
	}
}

// TestReconcile_SkipsSessionOwnedByLiveInstance ensures that a running session
// owned by a live (recently-heartbeating) instance is NOT interrupted.
func TestReconcile_SkipsSessionOwnedByLiveInstance(t *testing.T) {
	fix := newPhase9bFixture()
	fix.seedLiveInstance("inst-live")
	fix.seedRunningSession("sess-live", "inst-live")

	mgr := NewInstanceManager(fix.instanceSvc, fix.sessionSvc, fix.bus)
	mgr.workspaceID = fix.workspaceID
	mgr.instanceID = "inst-new"

	if err := mgr.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if got := fix.getSessionStatus("sess-live"); got != domain.AgentSessionRunning {
		t.Errorf("expected session to remain running, got %q", got)
	}
}

// TestGracefulShutdown_InterruptsOwnedSessionsAndDeletesInstance verifies the
// full clean-exit sequence: owned sessions → interrupted, instance row deleted.
func TestGracefulShutdown_InterruptsOwnedSessionsAndDeletesInstance(t *testing.T) {
	fix := newPhase9bFixture()
	ctx := context.Background()

	// Manually set up the manager without going through Start (avoids goroutine).
	instID := "inst-current"
	fix.instanceRepo.instances[instID] = domain.SubstrateInstance{
		ID:            instID,
		WorkspaceID:   fix.workspaceID,
		LastHeartbeat: time.Now(),
		StartedAt:     time.Now(),
	}
	fix.instanceRepo.byWorkspace[fix.workspaceID] = append(fix.instanceRepo.byWorkspace[fix.workspaceID], instID)

	fix.seedRunningSession("sess-a", instID)
	fix.seedRunningSession("sess-b", instID)

	mgr := NewInstanceManager(fix.instanceSvc, fix.sessionSvc, fix.bus)
	mgr.instanceID = instID
	mgr.workspaceID = fix.workspaceID
	// stopCh is already initialised by NewInstanceManager; no goroutine running.

	if err := mgr.GracefulShutdown(ctx); err != nil {
		t.Fatalf("GracefulShutdown: %v", err)
	}

	if fix.instanceExists(instID) {
		t.Error("expected instance row to be deleted after GracefulShutdown")
	}
	for _, sid := range []string{"sess-a", "sess-b"} {
		if got := fix.getSessionStatus(sid); got != domain.AgentSessionInterrupted {
			t.Errorf("session %s: expected interrupted, got %q", sid, got)
		}
	}
}

// TestStart_RegistersInstanceAndReconciles is an integration-style test confirming
// that Start() registers the current process, reconciles orphaned sessions, and
// launches the heartbeat goroutine (which we stop immediately for the test).
func TestStart_RegistersInstanceAndReconciles(t *testing.T) {
	fix := newPhase9bFixture()
	fix.seedStaleInstance("inst-stale")
	fix.seedRunningSession("sess-to-reconcile", "inst-stale")

	mgr := NewInstanceManager(fix.instanceSvc, fix.sessionSvc, fix.bus)

	if err := mgr.Start(context.Background(), fix.workspaceID); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Stop the heartbeat goroutine so the test exits cleanly.
	close(mgr.stopCh)
	mgr.wg.Wait()

	if !fix.instanceExists(mgr.InstanceID()) {
		t.Error("expected instance row to be present after Start")
	}
	if got := fix.getSessionStatus("sess-to-reconcile"); got != domain.AgentSessionInterrupted {
		t.Errorf("expected orphaned session reconciled to interrupted, got %q", got)
	}
}

// ============================================================
// Resumption tests
// ============================================================

// TestAbandonSession_TransitionsToFailed verifies the abandon path.
func TestAbandonSession_TransitionsToFailed(t *testing.T) {
	fix := newPhase9bFixture()
	fix.seedInterruptedSession("sess-abandoned")

	r := NewResumption(&captureHarness{sessionsDir: t.TempDir()}, fix.sessionSvc, fix.planSvc, fix.sessionRepo, fix.bus, nil)

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

	r := NewResumption(&captureHarness{sessionsDir: t.TempDir()}, fix.sessionSvc, fix.planSvc, fix.sessionRepo, fix.bus, nil)

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
	resumption := NewResumption(harness, fix.sessionSvc, fix.planSvc, fix.sessionRepo, fix.bus, nil)

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

	// Old session must remain as interrupted (not touched beyond owner update).
	if got := fix.getSessionStatus(intID); got != domain.AgentSessionInterrupted {
		t.Errorf("old session: expected interrupted, got %q", got)
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

// TestResumeSession_OldSessionRemainInterrupted is an explicit guard that
// ResumeSession never changes the interrupted session's status field.
func TestResumeSession_OldSessionRemainInterrupted(t *testing.T) {
	fix := newPhase9bFixture()
	ctx := context.Background()

	tmpDir := t.TempDir()
	sessionsDir := filepath.Join(tmpDir, "sessions")
	_ = os.MkdirAll(sessionsDir, 0o755)
	t.Setenv("SUBSTRATE_HOME", tmpDir)

	fix.seedInterruptedSession("sess-int2")
	// No log file — readLastNLines falls back to "unavailable" gracefully.

	harness := &captureHarness{sessionsDir: sessionsDir}
	r := NewResumption(harness, fix.sessionSvc, fix.planSvc, fix.sessionRepo, fix.bus, nil)

	interrupted := fix.sessionRepo.sessions["sess-int2"]
	_, err := r.ResumeSession(ctx, interrupted, "inst-new2")
	if err != nil {
		t.Fatalf("ResumeSession: %v", err)
	}

	if got := fix.getSessionStatus("sess-int2"); got != domain.AgentSessionInterrupted {
		t.Errorf("old session must remain interrupted; got %q", got)
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
	r := NewResumption(harness, fix.sessionSvc, fix.planSvc, fix.sessionRepo, fix.bus, nil)

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
	r := NewResumption(harness, fix.sessionSvc, fix.planSvc, fix.sessionRepo, fix.bus, nil)

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

package orchestrator

import (
	"context"
	"fmt"
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

func (h *captureHarness) SupportsCompact() bool { return true }
func (h *captureHarness) Name() string          { return "capture" }

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

type phase9bWorkItemRepo struct {
	items map[string]domain.Session
}

func newPhase9bWorkItemRepo() *phase9bWorkItemRepo {
	return &phase9bWorkItemRepo{items: make(map[string]domain.Session)}
}

func (r *phase9bWorkItemRepo) Get(_ context.Context, id string) (domain.Session, error) {
	item, ok := r.items[id]
	if !ok {
		return domain.Session{}, repository.ErrNotFound
	}
	return item, nil
}

func (r *phase9bWorkItemRepo) List(_ context.Context, filter repository.SessionFilter) ([]domain.Session, error) {
	items := make([]domain.Session, 0, len(r.items))
	for _, item := range r.items {
		if filter.WorkspaceID != nil && item.WorkspaceID != *filter.WorkspaceID {
			continue
		}
		if filter.ExternalID != nil && item.ExternalID != *filter.ExternalID {
			continue
		}
		if filter.State != nil && item.State != *filter.State {
			continue
		}
		if filter.Source != nil && item.Source != *filter.Source {
			continue
		}
		items = append(items, item)
	}
	return items, nil
}

func (r *phase9bWorkItemRepo) Create(_ context.Context, item domain.Session) error {
	r.items[item.ID] = item
	return nil
}

func (r *phase9bWorkItemRepo) Update(_ context.Context, item domain.Session) error {
	if _, ok := r.items[item.ID]; !ok {
		return repository.ErrNotFound
	}
	r.items[item.ID] = item
	return nil
}

func (r *phase9bWorkItemRepo) Delete(_ context.Context, id string) error {
	if _, ok := r.items[id]; !ok {
		return repository.ErrNotFound
	}
	delete(r.items, id)
	return nil
}

type phase9bFixture struct {
	instanceRepo *mockInstanceRepo
	sessionRepo  *mockSessionRepo // reused from phase9_test.go
	workItemRepo *phase9bWorkItemRepo
	subPlanRepo  *mockSubPlanRepo
	planRepo     *mockPlanRepo
	instanceSvc  *service.InstanceService
	sessionSvc   *service.AgentSessionService
	planSvc      *service.PlanService
	workItemSvc  *service.SessionService
	bus          *event.Bus
	workspaceID  string
	registry     SessionRegistry
}

func newPhase9bFixture() *phase9bFixture {
	instanceRepo := newMockInstanceRepo()
	sessionRepo := newMockSessionRepo()
	workItemRepo := newPhase9bWorkItemRepo()
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
	workItemRepo.items["wi-1"] = domain.Session{
		ID:          "wi-1",
		WorkspaceID: "ws-test",
		Title:       "Work item",
		Source:      "manual",
		State:       domain.SessionImplementing,
	}

	bus := event.NewBus(event.BusConfig{})
	planSvc := service.NewPlanService(repository.NoopTransacter{Res: repository.Resources{Plans: planRepo, SubPlans: subPlanRepo}}, bus)

	return &phase9bFixture{
		instanceRepo: instanceRepo,
		sessionRepo:  sessionRepo,
		workItemRepo: workItemRepo,
		subPlanRepo:  subPlanRepo,
		planRepo:     planRepo,
		instanceSvc:  service.NewInstanceService(repository.NoopTransacter{Res: repository.Resources{Instances: instanceRepo}}),
		sessionSvc:   service.NewAgentSessionService(repository.NoopTransacter{Res: repository.Resources{AgentSessions: sessionRepo}}, bus),
		planSvc:      planSvc,
		workItemSvc:  service.NewSessionService(repository.NoopTransacter{Res: repository.Resources{Sessions: workItemRepo}}, bus),
		bus:          bus,
		workspaceID:  "ws-test",
		registry:     NewSessionRegistry(),
	}
}

// seedRunningSession inserts a running session owned by ownerInstanceID.
func (f *phase9bFixture) seedRunningSession(sessionID, ownerInstanceID string) {
	owner := ownerInstanceID
	f.sessionRepo.sessions[sessionID] = domain.AgentSession{
		ID:              sessionID,
		WorkItemID:      "wi-1",
		WorkspaceID:     f.workspaceID,
		Kind: domain.AgentSessionKindImplementation,
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
	f.sessionRepo.sessions[sessionID] = domain.AgentSession{
		ID:             sessionID,
		WorkItemID:     "wi-1",
		WorkspaceID:    f.workspaceID,
		Kind: domain.AgentSessionKindImplementation,
		SubPlanID:      "sub-plan-1",
		RepositoryName: "repo-a",
		WorktreePath:   "/tmp/worktrees/repo-a",
		HarnessName:    "mock",
		Status:         domain.AgentSessionInterrupted,
	}
}

// seedInterruptedSessionWithResumeInfo inserts an interrupted session with ResumeInfo.
func (f *phase9bFixture) seedInterruptedSessionWithResumeInfo(sessionID string, resumeInfo map[string]string) {
	f.sessionRepo.sessions[sessionID] = domain.AgentSession{
		ID:             sessionID,
		WorkItemID:     "wi-1",
		WorkspaceID:    f.workspaceID,
		Kind: domain.AgentSessionKindImplementation,
		SubPlanID:      "sub-plan-1",
		RepositoryName: "repo-a",
		WorktreePath:   "/tmp/worktrees/repo-a",
		HarnessName:    "mock",
		Status:         domain.AgentSessionInterrupted,
		ResumeInfo:     resumeInfo,
	}
}

func (f *phase9bFixture) getSessionStatus(id string) domain.AgentSessionStatus {
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

	r := NewResumption(&captureHarness{sessionsDir: t.TempDir()}, fix.sessionSvc, fix.planSvc, fix.workItemSvc, fix.bus, fix.registry, nil)

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

	r := NewResumption(&captureHarness{sessionsDir: t.TempDir()}, fix.sessionSvc, fix.planSvc, fix.workItemSvc, fix.bus, fix.registry, nil)

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
	resumption := NewResumption(harness, fix.sessionSvc, fix.planSvc, fix.workItemSvc, fix.bus, fix.registry, nil)

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
	r := NewResumption(harness, fix.sessionSvc, fix.planSvc, fix.workItemSvc, fix.bus, fix.registry, nil)

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
	r := NewResumption(harness, fix.sessionSvc, fix.planSvc, fix.workItemSvc, fix.bus, fix.registry, nil)
	interrupted := fix.sessionRepo.sessions["sess-int3"]
	_, err := r.ResumeSession(ctx, interrupted, "inst-new3")
	if err == nil {
		t.Fatal("expected ResumeSession to fail when transition to running fails")
	}
	if !strings.Contains(err.Error(), "create resumed session") {
		t.Fatalf("expected resumed session error, got %v", err)
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
	fix.sessionRepo.updateHook = func(_ context.Context, session domain.AgentSession) error {
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
	r := NewResumption(harness, fix.sessionSvc, fix.planSvc, fix.workItemSvc, fix.bus, fix.registry, nil)

	interrupted := fix.sessionRepo.sessions["sess-int4"]
	_, err := r.ResumeSession(ctx, interrupted, "inst-new4")
	if err == nil {
		t.Fatal("expected ResumeSession to fail when transition to running fails")
	}
	if !strings.Contains(err.Error(), "create resumed session") {
		t.Fatalf("expected resumed session error, got %v", err)
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
	resumption := NewResumption(harness, fix.sessionSvc, fix.planSvc, fix.workItemSvc, fix.bus, fix.registry, nil)

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
	resumption := NewResumption(harness, fix.sessionSvc, fix.planSvc, fix.workItemSvc, fix.bus, fix.registry, nil)

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

// TestInterruptedSession_ResumeWithManualPrompt verifies that when resuming an
// interrupted session with an operator-supplied prompt, the prompt is delivered
// through native resume when ResumeInfo is available, or as a fallback message
// when ResumeInfo is not available.
func TestInterruptedSession_ResumeWithManualPrompt(t *testing.T) {
	ctx := context.Background()

	tmpDir := t.TempDir()
	sessionsDir := filepath.Join(tmpDir, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatalf("create sessions dir: %v", err)
	}
	t.Setenv("SUBSTRATE_HOME", tmpDir)

	const manualPrompt = "Please prioritize the authentication module first."

	// Test with resume info available (native resume path).
	{
		fix2 := newPhase9bFixture()
		intID := "sess-int-with-resume"
		fix2.seedInterruptedSessionWithResumeInfo(intID, map[string]string{"session": "native-resume-data"})

		harness := &captureHarness{sessionsDir: sessionsDir}
		resumption := NewResumption(harness, fix2.sessionSvc, fix2.planSvc, fix2.workItemSvc, fix2.bus, fix2.registry, nil)

		interrupted := fix2.sessionRepo.sessions[intID]
		result, err := resumption.ResumeSessionWithPrompt(ctx, interrupted, manualPrompt, "inst-native")
		if err != nil {
			t.Fatalf("ResumeSessionWithPrompt (native): %v", err)
		}

		if got := fix2.getSessionStatus(result.NewSession.ID); got != domain.AgentSessionRunning {
			t.Errorf("new session (native): expected running, got %q", got)
		}

		opts := harness.lastOpts()
		// Native resume: ResumeFromSessionID and ResumeInfo should be set.
		if opts.ResumeFromSessionID != interrupted.ID {
			t.Errorf("ResumeFromSessionID (native) = %q, want %q", opts.ResumeFromSessionID, interrupted.ID)
		}
		if opts.ResumeInfo == nil || opts.ResumeInfo["session"] != "native-resume-data" {
			t.Errorf("ResumeInfo (native) missing session key, got %v", opts.ResumeInfo)
		}
		// Manual prompt should be passed as UserPrompt.
		if opts.UserPrompt != manualPrompt {
			t.Errorf("UserPrompt (native) = %q, want %q", opts.UserPrompt, manualPrompt)
		}

		// SendMessage should be called with orientation.
		sess := harness.lastSession
		sess.mu.Lock()
		hasOrientation := false
		for _, msg := range sess.messages {
			if strings.Contains(msg, "previous session was interrupted") {
				hasOrientation = true
				break
			}
		}
		sess.mu.Unlock()
		if !hasOrientation {
			t.Error("native resume should send orientation via SendMessage")
		}
	}

	// Test without resume info (fallback path).
	{
		fix3 := newPhase9bFixture()
		intID := "sess-int-no-resume-prompt"
		fix3.seedInterruptedSession(intID)

		harness2 := &captureHarness{sessionsDir: sessionsDir}
		resumption2 := NewResumption(harness2, fix3.sessionSvc, fix3.planSvc, fix3.workItemSvc, fix3.bus, fix3.registry, nil)

		interrupted := fix3.sessionRepo.sessions[intID]
		result, err := resumption2.ResumeSessionWithPrompt(ctx, interrupted, manualPrompt, "inst-fallback")
		if err != nil {
			t.Fatalf("ResumeSessionWithPrompt (fallback): %v", err)
		}

		if got := fix3.getSessionStatus(result.NewSession.ID); got != domain.AgentSessionRunning {
			t.Errorf("new session (fallback): expected running, got %q", got)
		}

		opts := harness2.lastOpts()
		// Fallback path: no ResumeFromSessionID or ResumeInfo.
		if opts.ResumeFromSessionID != "" {
			t.Errorf("ResumeFromSessionID (fallback) = %q, want empty", opts.ResumeFromSessionID)
		}
		if len(opts.ResumeInfo) != 0 {
			t.Errorf("ResumeInfo (fallback) = %v, want empty", opts.ResumeInfo)
		}
		// UserPrompt should include both orientation and manual prompt.
		if opts.UserPrompt == "" {
			t.Error("UserPrompt (fallback) should be non-empty")
		}
		if !strings.Contains(opts.UserPrompt, "continuing work") {
			t.Errorf("UserPrompt (fallback) missing orientation, got: %q", opts.UserPrompt)
		}
		if !strings.Contains(opts.UserPrompt, manualPrompt) {
			t.Errorf("UserPrompt (fallback) missing manual prompt, got: %q", opts.UserPrompt)
		}

		// No SendMessage in fallback path.
		sess := harness2.lastSession
		sess.mu.Lock()
		if len(sess.messages) != 0 {
			t.Errorf("expected 0 SendMessage calls in fallback path, got %d", len(sess.messages))
		}
		sess.mu.Unlock()
	}
}

// ============================================================
// FollowUpSession / WaitAndComplete tests
// ============================================================

// seedCompletedSession inserts a completed session with a sub-plan.
func (f *phase9bFixture) seedCompletedSession(sessionID string) {
	now := time.Now()
	f.sessionRepo.sessions[sessionID] = domain.AgentSession{
		ID:             sessionID,
		WorkItemID:     "wi-1",
		WorkspaceID:    f.workspaceID,
		Kind: domain.AgentSessionKindImplementation,
		SubPlanID:      "sub-plan-1",
		RepositoryName: "repo-a",
		WorktreePath:   "/tmp/worktrees/repo-a",
		HarnessName:    "mock",
		Status:         domain.AgentSessionCompleted,
		CompletedAt:    &now,
	}
}

func TestFollowUpSession_TransitionsCompletedWorkItemToImplementing(t *testing.T) {
	fix := newPhase9bFixture()
	ctx := context.Background()

	tmpDir := t.TempDir()
	sessionsDir := filepath.Join(tmpDir, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatalf("create sessions dir: %v", err)
	}
	t.Setenv("SUBSTRATE_HOME", tmpDir)

	workItem := fix.workItemRepo.items["wi-1"]
	workItem.State = domain.SessionCompleted
	fix.workItemRepo.items["wi-1"] = workItem

	sessionID := "sess-follow-up-implementing"
	fix.seedCompletedSession(sessionID)

	harness := &captureHarness{sessionsDir: sessionsDir}
	r := NewResumption(harness, fix.sessionSvc, fix.planSvc, fix.workItemSvc, fix.bus, fix.registry, nil)

	_, err := r.FollowUpSession(ctx, fix.sessionRepo.sessions[sessionID], "please continue", "inst-1")
	if err != nil {
		t.Fatalf("FollowUpSession: %v", err)
	}

	gotWorkItem, err := fix.workItemSvc.Get(ctx, "wi-1")
	if err != nil {
		t.Fatalf("Get work item: %v", err)
	}
	if gotWorkItem.State != domain.SessionImplementing {
		t.Fatalf("work item state = %q, want %q", gotWorkItem.State, domain.SessionImplementing)
	}

	gotSession, err := fix.sessionSvc.Get(ctx, sessionID)
	if err != nil {
		t.Fatalf("Get agent session: %v", err)
	}
	if gotSession.Status != domain.AgentSessionRunning {
		t.Fatalf("agent session status = %q, want %q", gotSession.Status, domain.AgentSessionRunning)
	}
	if gotSession.CompletedAt != nil {
		t.Fatalf("agent session CompletedAt = %v, want nil", gotSession.CompletedAt)
	}
	if gotSession.OwnerInstanceID == nil || *gotSession.OwnerInstanceID != "inst-1" {
		t.Fatalf("agent session owner = %v, want inst-1", gotSession.OwnerInstanceID)
	}
}

// seedFailedSession inserts a failed session with a sub-plan.
func (f *phase9bFixture) seedFailedSession(sessionID string) {
	f.sessionRepo.sessions[sessionID] = domain.AgentSession{
		ID:             sessionID,
		WorkItemID:     "wi-1",
		WorkspaceID:    f.workspaceID,
		Kind: domain.AgentSessionKindImplementation,
		SubPlanID:      "sub-plan-1",
		RepositoryName: "repo-a",
		WorktreePath:   "/tmp/worktrees/repo-a",
		HarnessName:    "mock",
		Status:         domain.AgentSessionFailed,
	}
}

// TestFollowUpSession_WaitAndComplete_CompletesSessionInDB verifies that once
// WaitAndComplete returns the session row is transitioned to completed.
func TestFollowUpSession_WaitAndComplete_CompletesSessionInDB(t *testing.T) {
	fix := newPhase9bFixture()
	ctx := context.Background()

	tmpDir := t.TempDir()
	sessionsDir := filepath.Join(tmpDir, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatalf("create sessions dir: %v", err)
	}
	t.Setenv("SUBSTRATE_HOME", tmpDir)

	sessionID := "sess-follow-up-complete"
	fix.seedCompletedSession(sessionID)

	harness := &captureHarness{sessionsDir: sessionsDir}
	r := NewResumption(harness, fix.sessionSvc, fix.planSvc, fix.workItemSvc, fix.bus, fix.registry, nil)

	completedTask := fix.sessionRepo.sessions[sessionID]
	result, err := r.FollowUpSession(ctx, completedTask, "please also add tests", "inst-1")
	if err != nil {
		t.Fatalf("FollowUpSession: %v", err)
	}

	// WaitAndComplete should transition the session to completed.
	r.WaitAndComplete(ctx, result.Session.ID, &immediatelyCompletingSession{id: result.Session.ID})

	if got := fix.getSessionStatus(result.Session.ID); got != domain.AgentSessionCompleted {
		t.Errorf("expected completed, got %q", got)
	}
}

// TestFollowUpSession_WaitAndComplete_FailsSessionOnHarnessError verifies that when
// the harness exits with an error the session row is transitioned to failed.
func TestFollowUpSession_WaitAndComplete_FailsSessionOnHarnessError(t *testing.T) {
	fix := newPhase9bFixture()
	ctx := context.Background()

	tmpDir := t.TempDir()
	sessionsDir := filepath.Join(tmpDir, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatalf("create sessions dir: %v", err)
	}
	t.Setenv("SUBSTRATE_HOME", tmpDir)

	sessionID := "sess-follow-up-fail"
	fix.seedCompletedSession(sessionID)

	harness := &captureHarness{sessionsDir: sessionsDir}
	r := NewResumption(harness, fix.sessionSvc, fix.planSvc, fix.workItemSvc, fix.bus, fix.registry, nil)

	completedTask := fix.sessionRepo.sessions[sessionID]
	result, err := r.FollowUpSession(ctx, completedTask, "retry with extra tests", "inst-2")
	if err != nil {
		t.Fatalf("FollowUpSession: %v", err)
	}

	// Inject a session that returns an error from Wait.
	failingSession := newMockSession(result.Session.ID)
	failingSession.waitErr = fmt.Errorf("harness crashed")

	r.WaitAndComplete(ctx, result.Session.ID, failingSession)

	if got := fix.getSessionStatus(result.Session.ID); got != domain.AgentSessionFailed {
		t.Errorf("expected failed, got %q", got)
	}
}

// TestFollowUpFailedSession_WaitAndComplete_CompletesNewSessionInDB verifies that
// a follow-up on a failed task creates a new session and WaitAndComplete completes it.
func TestFollowUpFailedSession_WaitAndComplete_CompletesNewSessionInDB(t *testing.T) {
	fix := newPhase9bFixture()
	ctx := context.Background()

	tmpDir := t.TempDir()
	sessionsDir := filepath.Join(tmpDir, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatalf("create sessions dir: %v", err)
	}
	t.Setenv("SUBSTRATE_HOME", tmpDir)

	origID := "sess-failed-orig"
	fix.seedFailedSession(origID)

	harness := &captureHarness{sessionsDir: sessionsDir}
	r := NewResumption(harness, fix.sessionSvc, fix.planSvc, fix.workItemSvc, fix.bus, fix.registry, nil)

	failedTask := fix.sessionRepo.sessions[origID]
	result, err := r.FollowUpFailedSession(ctx, failedTask, "fix the compilation error", "inst-3")
	if err != nil {
		t.Fatalf("FollowUpFailedSession: %v", err)
	}

	// The new task ID is different from the original failed task.
	if result.Session.ID == origID {
		t.Error("FollowUpFailedSession must create a new task, not reuse the failed one")
	}

	// WaitAndComplete transitions the new session to completed.
	completingSession := &immediatelyCompletingSession{id: result.Session.ID}
	r.WaitAndComplete(ctx, result.Session.ID, completingSession)

	if got := fix.getSessionStatus(result.Session.ID); got != domain.AgentSessionCompleted {
		t.Errorf("expected new session to be completed, got %q", got)
	}
	// Original failed session must remain failed (audit trail).
	if got := fix.getSessionStatus(origID); got != domain.AgentSessionFailed {
		t.Errorf("original session must remain failed, got %q", got)
	}
}

// immediatelyCompletingSession is an AgentSession stub whose Wait returns nil immediately,
// simulating a session that finishes successfully without blocking.
type immediatelyCompletingSession struct {
	id string
}

func (s *immediatelyCompletingSession) ID() string                                    { return s.id }
func (s *immediatelyCompletingSession) Wait(_ context.Context) error                  { return nil }
func (s *immediatelyCompletingSession) Events() <-chan adapter.AgentEvent             { return nil }
func (s *immediatelyCompletingSession) SendMessage(_ context.Context, _ string) error { return nil }
func (s *immediatelyCompletingSession) Abort(_ context.Context) error                 { return nil }
func (s *immediatelyCompletingSession) Steer(_ context.Context, _ string) error       { return nil }
func (s *immediatelyCompletingSession) SendAnswer(_ context.Context, _ string) error  { return nil }
func (s *immediatelyCompletingSession) Compact(_ context.Context) error               { return nil }
func (s *immediatelyCompletingSession) ResumeInfo() map[string]string                 { return nil }
func (s *immediatelyCompletingSession) Done() <-chan struct{} {
	done := make(chan struct{})
	close(done)
	return done
}

// TestFollowUpSession_SetsOwnerInstance verifies reused follow-up rows are owned
// by the current process so startup reconciliation does not treat them as orphaned.
func TestFollowUpSession_SetsOwnerInstance(t *testing.T) {
	fix := newPhase9bFixture()
	ctx := context.Background()

	tmpDir := t.TempDir()
	sessionsDir := filepath.Join(tmpDir, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatalf("create sessions dir: %v", err)
	}
	t.Setenv("SUBSTRATE_HOME", tmpDir)

	sessionID := "sess-follow-up-owner"
	fix.seedCompletedSession(sessionID)

	r := NewResumption(&captureHarness{sessionsDir: sessionsDir}, fix.sessionSvc, fix.planSvc, fix.workItemSvc, fix.bus, fix.registry, nil)
	result, err := r.FollowUpSession(ctx, fix.sessionRepo.sessions[sessionID], "please also add tests", "inst-owner")
	if err != nil {
		t.Fatalf("FollowUpSession: %v", err)
	}

	got := fix.sessionRepo.sessions[result.Session.ID]
	if got.OwnerInstanceID == nil || *got.OwnerInstanceID != "inst-owner" {
		t.Fatalf("OwnerInstanceID = %v, want inst-owner", got.OwnerInstanceID)
	}
}

// TestFollowUpSession_WaitAndComplete_DrainsTerminalEvent verifies a bridge-style
// terminal done event cannot block WaitAndComplete before it marks the row completed.
func TestFollowUpSession_WaitAndComplete_DrainsTerminalEvent(t *testing.T) {
	fix := newPhase9bFixture()
	ctx := context.Background()

	sessionID := "sess-follow-up-drain"
	fix.seedCompletedSession(sessionID)
	owner := "inst-owner"
	if err := fix.sessionSvc.FollowUpRestart(ctx, sessionID, &owner); err != nil {
		t.Fatalf("FollowUpRestart: %v", err)
	}

	r := NewResumption(&captureHarness{sessionsDir: t.TempDir()}, fix.sessionSvc, fix.planSvc, fix.workItemSvc, fix.bus, fix.registry, nil)
	sess := newWaitBlockedByTerminalEventSession(sessionID)
	done := make(chan struct{})
	go func() {
		r.WaitAndComplete(ctx, sessionID, sess)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("WaitAndComplete blocked on unconsumed terminal event")
	}

	if got := fix.getSessionStatus(sessionID); got != domain.AgentSessionCompleted {
		t.Fatalf("status = %q, want %q", got, domain.AgentSessionCompleted)
	}
}

type waitBlockedByTerminalEventSession struct {
	id     string
	events chan adapter.AgentEvent
	done   chan struct{}
}

func newWaitBlockedByTerminalEventSession(id string) *waitBlockedByTerminalEventSession {
	s := &waitBlockedByTerminalEventSession{
		id:     id,
		events: make(chan adapter.AgentEvent),
		done:   make(chan struct{}),
	}
	go func() {
		s.events <- adapter.AgentEvent{Type: "done", Timestamp: time.Now()}
		close(s.events)
		close(s.done)
	}()
	return s
}

func (s *waitBlockedByTerminalEventSession) ID() string { return s.id }
func (s *waitBlockedByTerminalEventSession) Wait(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.done:
		return nil
	}
}
func (s *waitBlockedByTerminalEventSession) Events() <-chan adapter.AgentEvent { return s.events }
func (s *waitBlockedByTerminalEventSession) SendMessage(_ context.Context, _ string) error {
	return nil
}
func (s *waitBlockedByTerminalEventSession) Abort(_ context.Context) error                { return nil }
func (s *waitBlockedByTerminalEventSession) Steer(_ context.Context, _ string) error      { return nil }
func (s *waitBlockedByTerminalEventSession) SendAnswer(_ context.Context, _ string) error { return nil }
func (s *waitBlockedByTerminalEventSession) Compact(_ context.Context) error              { return nil }
func (s *waitBlockedByTerminalEventSession) ResumeInfo() map[string]string                { return nil }
func (s *waitBlockedByTerminalEventSession) Done() <-chan struct{} {
	done := make(chan struct{})
	close(done)
	return done
}

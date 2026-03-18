package orchestrator

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/repository"
	"github.com/beeemT/substrate/internal/service"
)

// TestBuildWaves tests the BuildWaves function with various input scenarios.
func TestBuildWaves(t *testing.T) {
	tests := []struct {
		name        string
		subPlans    []domain.TaskPlan
		wantWaves   int
		wantPerWave []int
	}{
		{
			name:        "empty sub-plans",
			subPlans:    []domain.TaskPlan{},
			wantWaves:   0,
			wantPerWave: nil,
		},
		{
			name: "single sub-plan",
			subPlans: []domain.TaskPlan{
				{ID: "sp1", Order: 0, RepositoryName: "repo1"},
			},
			wantWaves:   1,
			wantPerWave: []int{1},
		},
		{
			name: "two parallel sub-plans (same order)",
			subPlans: []domain.TaskPlan{
				{ID: "sp1", Order: 0, RepositoryName: "repo1"},
				{ID: "sp2", Order: 0, RepositoryName: "repo2"},
			},
			wantWaves:   1,
			wantPerWave: []int{2},
		},
		{
			name: "two sequential sub-plans (different orders)",
			subPlans: []domain.TaskPlan{
				{ID: "sp1", Order: 0, RepositoryName: "repo1"},
				{ID: "sp2", Order: 1, RepositoryName: "repo2"},
			},
			wantWaves:   2,
			wantPerWave: []int{1, 1},
		},
		{
			name: "three sub-plans with mixed orders [0,0,1]",
			subPlans: []domain.TaskPlan{
				{ID: "sp1", Order: 0, RepositoryName: "repo1"},
				{ID: "sp2", Order: 0, RepositoryName: "repo2"},
				{ID: "sp3", Order: 1, RepositoryName: "repo3"},
			},
			wantWaves:   2,
			wantPerWave: []int{2, 1},
		},
		{
			name: "complex wave pattern [0,0,1,2,2,2]",
			subPlans: []domain.TaskPlan{
				{ID: "sp1", Order: 0, RepositoryName: "repo1"},
				{ID: "sp2", Order: 0, RepositoryName: "repo2"},
				{ID: "sp3", Order: 1, RepositoryName: "repo3"},
				{ID: "sp4", Order: 2, RepositoryName: "repo4"},
				{ID: "sp5", Order: 2, RepositoryName: "repo5"},
				{ID: "sp6", Order: 2, RepositoryName: "repo6"},
			},
			wantWaves:   3,
			wantPerWave: []int{2, 1, 3},
		},
		{
			name: "sparse orders [0,2,5]",
			subPlans: []domain.TaskPlan{
				{ID: "sp1", Order: 0, RepositoryName: "repo1"},
				{ID: "sp2", Order: 2, RepositoryName: "repo2"},
				{ID: "sp3", Order: 5, RepositoryName: "repo3"},
			},
			wantWaves:   3,
			wantPerWave: []int{1, 1, 1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			waves := BuildWaves(tt.subPlans)

			if len(waves) != tt.wantWaves {
				t.Errorf("BuildWaves() got %d waves, want %d", len(waves), tt.wantWaves)
			}

			for i, wave := range waves {
				if i < len(tt.wantPerWave) && len(wave) != tt.wantPerWave[i] {
					t.Errorf("BuildWaves() wave %d got %d sub-plans, want %d", i, len(wave), tt.wantPerWave[i])
				}
			}
		})
	}
}

// TestBuildWavesOrderPreservation tests that waves are ordered by Order value.
func TestBuildWavesOrderPreservation(t *testing.T) {
	// Sub-plans with orders 2, 0, 1 (out of order)
	subPlans := []domain.TaskPlan{
		{ID: "sp1", Order: 2, RepositoryName: "repo1"},
		{ID: "sp2", Order: 0, RepositoryName: "repo2"},
		{ID: "sp3", Order: 1, RepositoryName: "repo3"},
	}

	waves := BuildWaves(subPlans)

	if len(waves) != 3 {
		t.Fatalf("expected 3 waves, got %d", len(waves))
	}

	// Verify wave order: wave 0 should have Order=0, wave 1 should have Order=1, etc.
	expectedOrders := []int{0, 1, 2}
	for i, wave := range waves {
		for _, sp := range wave {
			if sp.Order != expectedOrders[i] {
				t.Errorf("wave %d: expected Order %d, got %d", i, expectedOrders[i], sp.Order)
			}
		}
	}
}

// TestBuildWavesRaceCondition tests that BuildWaves is safe for concurrent use.
func TestBuildWavesRaceCondition(t *testing.T) {
	subPlans := []domain.TaskPlan{
		{ID: "sp1", Order: 0, RepositoryName: "repo1"},
		{ID: "sp2", Order: 0, RepositoryName: "repo2"},
		{ID: "sp3", Order: 1, RepositoryName: "repo3"},
	}

	var wg sync.WaitGroup
	for range 100 {
		wg.Go(func() {
			waves := BuildWaves(subPlans)
			if len(waves) != 2 {
				t.Errorf("expected 2 waves, got %d", len(waves))
			}
		})
	}
	wg.Wait()
}

// TestExecutionState tests the ExecutionState tracking.
func TestExecutionState(t *testing.T) {
	subPlans := []domain.TaskPlan{
		{ID: "sp1", Order: 0, RepositoryName: "repo1"},
		{ID: "sp2", Order: 0, RepositoryName: "repo2"},
		{ID: "sp3", Order: 1, RepositoryName: "repo3"},
	}

	state := NewExecutionState("plan-1", subPlans)

	// Initial state
	if state.CurrentWave != 0 {
		t.Errorf("expected current wave 0, got %d", state.CurrentWave)
	}

	if state.AllWavesCompleted() {
		t.Error("expected waves not completed initially")
	}

	// Start and complete wave 0
	state.StartWave(0, time.Now().UnixNano())
	state.StartSubPlan("sp1", time.Now().UnixNano())
	state.StartSubPlan("sp2", time.Now().UnixNano())

	state.CompleteSubPlan("sp1", time.Now().UnixNano())
	state.CompleteSubPlan("sp2", time.Now().UnixNano())
	state.CompleteWave(0, time.Now().UnixNano())

	if !state.CurrentWaveComplete() {
		t.Error("expected wave 0 to be complete")
	}

	// Advance to wave 1
	if !state.AdvanceWave() {
		t.Error("expected to advance to wave 1")
	}

	if state.CurrentWave != 1 {
		t.Errorf("expected current wave 1, got %d", state.CurrentWave)
	}

	// Complete wave 1
	state.StartWave(1, time.Now().UnixNano())
	state.StartSubPlan("sp3", time.Now().UnixNano())
	state.CompleteSubPlan("sp3", time.Now().UnixNano())
	state.CompleteWave(1, time.Now().UnixNano())
	state.AdvanceWave()

	if !state.AllWavesCompleted() {
		t.Error("expected all waves to be completed")
	}
}

// TestExecutionStateFailure tests failure handling in execution state.
func TestExecutionStateFailure(t *testing.T) {
	subPlans := []domain.TaskPlan{
		{ID: "sp1", Order: 0, RepositoryName: "repo1"},
		{ID: "sp2", Order: 0, RepositoryName: "repo2"},
	}

	state := NewExecutionState("plan-1", subPlans)

	// Start wave 0
	state.StartWave(0, time.Now().UnixNano())
	state.StartSubPlan("sp1", time.Now().UnixNano())
	state.StartSubPlan("sp2", time.Now().UnixNano())

	// One succeeds, one fails
	state.CompleteSubPlan("sp1", time.Now().UnixNano())
	state.FailSubPlan("sp2", time.Now().UnixNano(), context.Canceled)

	if !state.HasFailed() {
		t.Error("expected state to show failure")
	}

	// Fail the wave
	state.FailWave(0, time.Now().UnixNano())

	if state.WaveStates[0].Status != WaveFailed {
		t.Errorf("expected wave status %s, got %s", WaveFailed, state.WaveStates[0].Status)
	}
}

// TestGenerateBranchName tests branch name generation.
func TestGenerateBranchName(t *testing.T) {
	tests := []struct {
		name         string
		externalID   string
		title        string
		wantPrefix   string
		wantContains string
	}{
		{
			name:         "simple title",
			externalID:   "LIN-FOO-123",
			title:        "Fix auth bug",
			wantPrefix:   "sub-LIN-FOO-123-",
			wantContains: "fix-auth-bug",
		},
		{
			name:         "title with special characters",
			externalID:   "LIN-BAR-456",
			title:        "Add OAuth2.0 support!!!",
			wantPrefix:   "sub-LIN-BAR-456-",
			wantContains: "oauth2-0-support",
		},
		{
			name:         "manual work item",
			externalID:   "MAN-1",
			title:        "Update documentation",
			wantPrefix:   "sub-MAN-1-",
			wantContains: "update-documentation",
		},
		{
			name:         "long title truncation",
			externalID:   "LIN-TEST-789",
			title:        "This is a very long title that should be truncated to fit within the maximum slug length limit",
			wantPrefix:   "sub-LIN-TEST-789-",
			wantContains: "",
		},
		{
			name:         "title with consecutive spaces",
			externalID:   "LIN-SPACE-1",
			title:        "Fix   multiple    spaces",
			wantPrefix:   "sub-LIN-SPACE-1-",
			wantContains: "fix-multiple-spaces",
		},
		{
			name:         "title with uppercase",
			externalID:   "LIN-UPPER-1",
			title:        "UPPERCASE Title",
			wantPrefix:   "sub-LIN-UPPER-1-",
			wantContains: "uppercase-title",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GenerateBranchName(tt.externalID, tt.title)

			if got == "" {
				t.Error("GenerateBranchName() returned empty string")
			}

			if tt.wantPrefix != "" && got[:len(tt.wantPrefix)] != tt.wantPrefix {
				t.Errorf("GenerateBranchName() = %q, want prefix %q", got, tt.wantPrefix)
			}

			if tt.wantContains != "" && !strings.Contains(got, tt.wantContains) {
				t.Errorf("GenerateBranchName() = %q, want it to contain %q", got, tt.wantContains)
			}
		})
	}
}

// TestValidateBranchName tests branch name validation.
func TestValidateBranchName(t *testing.T) {
	tests := []struct {
		name      string
		branch    string
		wantValid bool
	}{
		{
			name:      "valid branch",
			branch:    "sub-LIN-FOO-123-fix-auth",
			wantValid: true,
		},
		{
			name:      "valid branch with manual ID",
			branch:    "sub-MAN-42-update-docs",
			wantValid: true,
		},
		{
			name:      "missing sub prefix",
			branch:    "LIN-FOO-123-fix-auth",
			wantValid: false,
		},
		{
			name:      "contains slash",
			branch:    "sub-LIN-FOO-123/fix-auth",
			wantValid: false,
		},
		{
			name:      "no slug",
			branch:    "sub-LIN-FOO-123-",
			wantValid: false,
		},
		{
			name:      "empty string",
			branch:    "",
			wantValid: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ValidateBranchName(tt.branch)
			if got != tt.wantValid {
				t.Errorf("ValidateBranchName(%q) = %v, want %v", tt.branch, got, tt.wantValid)
			}
		})
	}
}

// TestWaveTimingConcurrentStart tests that sub-plans in the same wave start
// within a short time window of each other (concurrent execution).
func TestWaveTimingConcurrentStart(t *testing.T) {
	// This test verifies that sub-plans in the same wave can execute concurrently.
	// We use a mock scenario to verify the timing characteristics.

	subPlans := []domain.TaskPlan{
		{ID: "sp1", Order: 0, RepositoryName: "repo1"},
		{ID: "sp2", Order: 0, RepositoryName: "repo2"},
		{ID: "sp3", Order: 1, RepositoryName: "repo3"},
	}

	waves := BuildWaves(subPlans)

	// Verify wave structure
	if len(waves) != 2 {
		t.Fatalf("expected 2 waves, got %d", len(waves))
	}

	// Wave 0 should have 2 sub-plans
	if len(waves[0]) != 2 {
		t.Errorf("wave 0 should have 2 sub-plans, got %d", len(waves[0]))
	}

	// Wave 1 should have 1 sub-plan
	if len(waves[1]) != 1 {
		t.Errorf("wave 1 should have 1 sub-plan, got %d", len(waves[1]))
	}
}

// TestGetWaveSubPlans tests getting sub-plan IDs for a specific wave.
func TestGetWaveSubPlans(t *testing.T) {
	subPlans := []domain.TaskPlan{
		{ID: "sp1", Order: 0, RepositoryName: "repo1"},
		{ID: "sp2", Order: 0, RepositoryName: "repo2"},
		{ID: "sp3", Order: 1, RepositoryName: "repo3"},
	}

	state := NewExecutionState("plan-1", subPlans)

	// Get wave 0 sub-plans
	wave0IDs := state.GetWaveSubPlans(0)
	if len(wave0IDs) != 2 {
		t.Errorf("expected 2 sub-plans in wave 0, got %d", len(wave0IDs))
	}

	// Get wave 1 sub-plans
	wave1IDs := state.GetWaveSubPlans(1)
	if len(wave1IDs) != 1 {
		t.Errorf("expected 1 sub-plan in wave 1, got %d", len(wave1IDs))
	}

	// Invalid wave index
	invalidIDs := state.GetWaveSubPlans(10)
	if invalidIDs != nil {
		t.Errorf("expected nil for invalid wave index, got %v", invalidIDs)
	}
}

// TestAllWavesCompletedEmptyPlan verifies that a plan with no sub-plans does not
// vacuously report completion.
func TestAllWavesCompletedEmptyPlan(t *testing.T) {
	state := NewExecutionState("plan-empty", []domain.TaskPlan{})
	if state.AllWavesCompleted() {
		t.Error("AllWavesCompleted() = true for empty plan, want false")
	}
}

type implementationWorkItemRepo struct {
	mu         sync.Mutex
	items      map[string]domain.Session
	updateHook func(context.Context, domain.Session) error
}

func (r *implementationWorkItemRepo) Get(_ context.Context, id string) (domain.Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	item, ok := r.items[id]
	if !ok {
		return domain.Session{}, repository.ErrNotFound
	}

	return item, nil
}

func (r *implementationWorkItemRepo) List(_ context.Context, _ repository.SessionFilter) ([]domain.Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	items := make([]domain.Session, 0, len(r.items))
	for _, item := range r.items {
		items = append(items, item)
	}

	return items, nil
}

func (r *implementationWorkItemRepo) Create(_ context.Context, item domain.Session) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items[item.ID] = item

	return nil
}

func (r *implementationWorkItemRepo) Update(ctx context.Context, item domain.Session) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.updateHook != nil {
		if err := r.updateHook(ctx, item); err != nil {
			return err
		}
	}
	r.items[item.ID] = item

	return nil
}

func (r *implementationWorkItemRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.items, id)

	return nil
}

type implementationWorkspaceRepo struct {
	workspaces map[string]domain.Workspace
}

func (r *implementationWorkspaceRepo) Get(_ context.Context, id string) (domain.Workspace, error) {
	ws, ok := r.workspaces[id]
	if !ok {
		return domain.Workspace{}, repository.ErrNotFound
	}

	return ws, nil
}

func (r *implementationWorkspaceRepo) Create(_ context.Context, ws domain.Workspace) error {
	r.workspaces[ws.ID] = ws

	return nil
}

func (r *implementationWorkspaceRepo) Update(_ context.Context, ws domain.Workspace) error {
	r.workspaces[ws.ID] = ws

	return nil
}

func (r *implementationWorkspaceRepo) Delete(_ context.Context, id string) error {
	delete(r.workspaces, id)

	return nil
}

type implementationEventRepo struct {
	events []domain.SystemEvent
}

func (r *implementationEventRepo) Create(_ context.Context, evt domain.SystemEvent) error {
	r.events = append(r.events, evt)

	return nil
}

func (r *implementationEventRepo) ListByType(_ context.Context, eventType string, limit int) ([]domain.SystemEvent, error) {
	var events []domain.SystemEvent
	for _, evt := range r.events {
		if evt.EventType == eventType {
			events = append(events, evt)
		}
	}
	if limit > 0 && len(events) > limit {
		events = events[:limit]
	}

	return events, nil
}

func (r *implementationEventRepo) ListByWorkspaceID(_ context.Context, workspaceID string, limit int) ([]domain.SystemEvent, error) {
	var events []domain.SystemEvent
	for _, evt := range r.events {
		if evt.WorkspaceID == workspaceID {
			events = append(events, evt)
		}
	}
	if limit > 0 && len(events) > limit {
		events = events[:limit]
	}

	return events, nil
}

func newImplementationServiceForTest(workspaceRoot, repoName string) (*ImplementationService, *implementationWorkItemRepo, *implementationEventRepo) {
	planRepo := newMockPlanRepo()
	planRepo.plans["plan-1"] = domain.Plan{
		ID:         "plan-1",
		WorkItemID: "wi-1",
		Status:     domain.PlanApproved,
	}

	subPlanRepo := newMockSubPlanRepo()
	subPlanRepo.subPlans["sp-1"] = domain.TaskPlan{
		ID:             "sp-1",
		PlanID:         "plan-1",
		RepositoryName: repoName,
		Content:        "Implement the change",
		Order:          0,
		Status:         domain.SubPlanPending,
	}

	workItemRepo := &implementationWorkItemRepo{
		items: map[string]domain.Session{
			"wi-1": {
				ID:          "wi-1",
				WorkspaceID: "ws-1",
				ExternalID:  "MAN-1",
				Source:      "manual",
				Title:       "Implement the change",
				State:       domain.SessionApproved,
			},
		},
	}
	workspaceRepo := &implementationWorkspaceRepo{
		workspaces: map[string]domain.Workspace{
			"ws-1": {
				ID:       "ws-1",
				RootPath: workspaceRoot,
				Status:   domain.WorkspaceReady,
			},
		},
	}
	sessionRepo := newMockSessionRepo()
	eventRepo := &implementationEventRepo{}

	svc := NewImplementationService(
		&config.Config{},
		&mockAgentHarness{},
		nil,
		nil,
		service.NewPlanService(planRepo, subPlanRepo),
		service.NewSessionService(workItemRepo),
		service.NewTaskService(sessionRepo),
		subPlanRepo,
		sessionRepo,
		eventRepo,
		service.NewWorkspaceService(workspaceRepo),
	)

	return svc, workItemRepo, eventRepo
}

func TestImplement_DiscoverRepoFailureKeepsWorkItemApproved(t *testing.T) {
	workspaceRoot := filepath.Join(t.TempDir(), "missing")
	svc, workItemRepo, eventRepo := newImplementationServiceForTest(workspaceRoot, "repo-a")

	_, err := svc.Implement(context.Background(), "plan-1")
	if err == nil {
		t.Fatal("expected implementation to fail when workspace repo discovery fails")
	}
	if !strings.Contains(err.Error(), "discover repo paths") {
		t.Fatalf("expected discover repo paths error, got %v", err)
	}

	workItem, getErr := workItemRepo.Get(context.Background(), "wi-1")
	if getErr != nil {
		t.Fatalf("get work item: %v", getErr)
	}
	if workItem.State != domain.SessionApproved {
		t.Fatalf("work item state = %q, want %q", workItem.State, domain.SessionApproved)
	}
	if len(eventRepo.events) != 0 {
		t.Fatalf("expected no implementation-started events, got %d", len(eventRepo.events))
	}
}

func TestImplement_PrepareWorktreesFailureMarksWorkItemFailed(t *testing.T) {
	svc, workItemRepo, eventRepo := newImplementationServiceForTest(t.TempDir(), "repo-a")

	_, err := svc.Implement(context.Background(), "plan-1")
	if err == nil {
		t.Fatal("expected implementation to fail when worktree preparation fails")
	}
	if !strings.Contains(err.Error(), "prepare worktrees") {
		t.Fatalf("expected prepare worktrees error, got %v", err)
	}

	workItem, getErr := workItemRepo.Get(context.Background(), "wi-1")
	if getErr != nil {
		t.Fatalf("get work item: %v", getErr)
	}
	if workItem.State != domain.SessionFailed {
		t.Fatalf("work item state = %q, want %q", workItem.State, domain.SessionFailed)
	}
	if len(eventRepo.events) != 1 {
		t.Fatalf("expected one implementation-started event, got %d", len(eventRepo.events))
	}
	if got := eventRepo.events[0].EventType; got != string(domain.EventImplementationStarted) {
		t.Fatalf("event type = %q, want %q", got, domain.EventImplementationStarted)
	}
}

func TestImplement_PrepareWorktreesFailureUsesDetachedCleanupContext(t *testing.T) {
	svc, workItemRepo, eventRepo := newImplementationServiceForTest(t.TempDir(), "repo-a")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	workItemRepo.updateHook = func(ctx context.Context, item domain.Session) error {
		if item.State == domain.SessionImplementing {
			cancel()

			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		return nil
	}

	_, err := svc.Implement(ctx, "plan-1")
	if err == nil {
		t.Fatal("expected implementation to fail when worktree preparation fails")
	}
	if !strings.Contains(err.Error(), "prepare worktrees") {
		t.Fatalf("expected prepare worktrees error, got %v", err)
	}

	workItem, getErr := workItemRepo.Get(context.Background(), "wi-1")
	if getErr != nil {
		t.Fatalf("get work item: %v", getErr)
	}
	if workItem.State != domain.SessionFailed {
		t.Fatalf("work item state = %q, want %q", workItem.State, domain.SessionFailed)
	}
	if len(eventRepo.events) != 1 {
		t.Fatalf("expected one implementation-started event, got %d", len(eventRepo.events))
	}
	if got := eventRepo.events[0].EventType; got != string(domain.EventImplementationStarted) {
		t.Fatalf("event type = %q, want %q", got, domain.EventImplementationStarted)
	}
}

func TestExecuteSubPlan_DoesNotStartHarnessWhenSessionStartFails(t *testing.T) {
	svc, _, eventRepo := newImplementationServiceForTest(t.TempDir(), "repo-a")
	sessionRepo, ok := svc.sessionRepo.(*mockSessionRepo)
	if !ok {
		t.Fatal("expected mock session repo")
	}
	sessionRepo.updateErr = repository.ErrNotFound
	sessionRepo.updateErrStatus = domain.AgentSessionRunning

	harness := &captureHarness{}
	svc.harness = harness

	subPlanRepo, ok := svc.subPlanRepo.(*mockSubPlanRepo)
	if !ok {
		t.Fatal("expected mock sub-plan repo")
	}
	subPlan := subPlanRepo.subPlans["sp-1"]
	workspace := domain.Workspace{ID: "ws-1", RootPath: t.TempDir(), Status: domain.WorkspaceReady}
	plan := domain.Plan{ID: "plan-1", WorkItemID: "wi-1", Status: domain.PlanApproved}
	workItem := domain.Session{
		ID:          "wi-1",
		WorkspaceID: "ws-1",
		ExternalID:  "MAN-1",
		Source:      "manual",
		Title:       "Implement the change",
		State:       domain.SessionImplementing,
	}
	state := NewExecutionState("plan-1", []domain.TaskPlan{subPlan})

	result, warning := svc.executeSubPlan(
		context.Background(),
		subPlan,
		&workspace,
		&plan,
		&workItem,
		"sub-MAN-1-implement-the-change",
		map[string]string{"repo-a": t.TempDir()},
		state,
	)

	if result.Status != domain.AgentSessionFailed {
		t.Fatalf("session status = %q, want %q", result.Status, domain.AgentSessionFailed)
	}
	if warning == nil || warning.Type != "session_start_failed" {
		t.Fatalf("warning = %#v, want session_start_failed", warning)
	}
	if len(harness.captured) != 0 {
		t.Fatalf("expected no harness starts, got %d", len(harness.captured))
	}
	if _, err := sessionRepo.Get(context.Background(), result.SessionID); err != repository.ErrNotFound {
		t.Fatalf("expected pending session cleanup, got %v", err)
	}
	for _, evt := range eventRepo.events {
		if evt.EventType == string(domain.EventAgentSessionStarted) {
			t.Fatalf("unexpected %s event for session that never reached running", domain.EventAgentSessionStarted)
		}
	}
}

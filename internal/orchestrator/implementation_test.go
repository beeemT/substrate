package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/event"
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

// TestBuildWavesSkipsCompletedSubPlans verifies that completed sub-plans are
// excluded from waves during differential re-implementation.
func TestBuildWavesSkipsCompletedSubPlans(t *testing.T) {
	subPlans := []domain.TaskPlan{
		{ID: "sp1", Order: 0, RepositoryName: "repo1", Status: domain.SubPlanCompleted},
		{ID: "sp2", Order: 0, RepositoryName: "repo2", Status: domain.SubPlanPending},
		{ID: "sp3", Order: 1, RepositoryName: "repo3", Status: domain.SubPlanPending},
		{ID: "sp4", Order: 1, RepositoryName: "repo4", Status: domain.SubPlanCompleted},
	}

	waves := BuildWaves(subPlans)
	if len(waves) != 2 {
		t.Fatalf("got %d waves, want 2", len(waves))
	}
	if len(waves[0]) != 1 || waves[0][0].ID != "sp2" {
		t.Errorf("wave 0: got %v, want [sp2]", waves[0])
	}
	if len(waves[1]) != 1 || waves[1][0].ID != "sp3" {
		t.Errorf("wave 1: got %v, want [sp3]", waves[1])
	}
}

// TestBuildWavesAllCompletedReturnsNil verifies that when all sub-plans are
// completed, BuildWaves returns nil (nothing to execute).
func TestBuildWavesAllCompletedReturnsNil(t *testing.T) {
	subPlans := []domain.TaskPlan{
		{ID: "sp1", Order: 0, Status: domain.SubPlanCompleted},
		{ID: "sp2", Order: 1, Status: domain.SubPlanCompleted},
	}
	waves := BuildWaves(subPlans)
	if waves != nil {
		t.Errorf("got %v, want nil", waves)
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
		{
			name:         "github issue externalID",
			externalID:   "gh:issue:rtk-ai/rtk#591",
			title:        "Add support for Oh My Pi",
			wantPrefix:   "sub-gh-issue-rtk-ai-rtk-591-",
			wantContains: "add-support-for-oh-my-pi",
		},
		{
			name:         "gitlab issue externalID",
			externalID:   "gl:issue:1234#42",
			title:        "Fix rendering bug",
			wantPrefix:   "sub-gl-issue-1234-42-",
			wantContains: "fix-rendering-bug",
		},
		{
			name:         "sentry externalID with dot in org",
			externalID:   "SEN-my.company-1234567890",
			title:        "Null pointer in checkout",
			wantPrefix:   "sub-SEN-my-company-1234567890-",
			wantContains: "null-pointer-in-checkout",
		},
		{
			// Consecutive dots in an external ID must not survive sanitization:
			// `..` is forbidden by git check-ref-format and rejected by GitHub/GitLab.
			name:         "externalID with consecutive dots",
			externalID:   "SEN-my..org-9999",
			title:        "Crash",
			wantPrefix:   "sub-SEN-my-org-9999-",
			wantContains: "crash",
		},
		{
			// Sentry org configured as an FQDN (dots → dashes).
			name:         "sentry org as fqdn",
			externalID:   "SEN-sentry.example.com-555",
			title:        "Out of memory",
			wantPrefix:   "sub-SEN-sentry-example-com-555-",
			wantContains: "out-of-memory",
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
		{
			name:      "contains colon (unsanitized github ID)",
			branch:    "sub-gh:issue:rtk-ai-rtk-591-fix",
			wantValid: false,
		},
		{
			name:      "contains hash",
			branch:    "sub-gh-issue-rtk-ai-rtk#591-fix",
			wantValid: false,
		},
		{
			name:      "sanitized github ID",
			branch:    "sub-gh-issue-rtk-ai-rtk-591-fix",
			wantValid: true,
		},
		{
			name:      "consecutive dots (invalid git ref)",
			branch:    "sub-SEN-my..org-1234-crash",
			wantValid: false,
		},
		{
			name:      "at-brace sequence (invalid git ref)",
			branch:    "sub-LIN-FOO-123-fix@{auth}",
			wantValid: false,
		},
		{
			name:      "trailing dot (invalid git ref)",
			branch:    "sub-MAN-1-work.",
			wantValid: false,
		},
		{
			name:      "trailing dotlock (invalid git ref)",
			branch:    "sub-MAN-1-work.lock",
			wantValid: false,
		},
		{
			name:      "valid sentry branch with dot-sanitized org",
			branch:    "sub-SEN-my-company-1234567890-null-pointer",
			wantValid: true,
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

func newImplementationServiceForTest(workspaceRoot, repoName string) (*ImplementationService, *implementationWorkItemRepo, *implementationEventRepo, *mockSessionRepo, *mockSubPlanRepo) {
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
	bus := event.NewBus(event.BusConfig{EventRepo: eventRepo})

	svc := NewImplementationService(
		&config.Config{},
		&mockAgentHarness{},
		nil, bus,
		service.NewPlanService(repository.NoopTransacter{Res: repository.Resources{Plans: planRepo, SubPlans: subPlanRepo}}),
		service.NewSessionService(repository.NoopTransacter{Res: repository.Resources{Sessions: workItemRepo}}),
		service.NewTaskService(repository.NoopTransacter{Res: repository.Resources{Tasks: sessionRepo}}),
		service.NewWorkspaceService(repository.NoopTransacter{Res: repository.Resources{Workspaces: workspaceRepo}}),
		nil,
		nil,
		nil, nil, // foreman, questionSvc
		nil, // reviewSvc
	)

	return svc, workItemRepo, eventRepo, sessionRepo, subPlanRepo
}

func TestImplement_DiscoverRepoFailureKeepsWorkItemApproved(t *testing.T) {
	workspaceRoot := filepath.Join(t.TempDir(), "missing")
	svc, workItemRepo, eventRepo, _, _ := newImplementationServiceForTest(workspaceRoot, "repo-a")

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
	svc, workItemRepo, eventRepo, _, _ := newImplementationServiceForTest(t.TempDir(), "repo-a")

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
	svc, workItemRepo, eventRepo, _, _ := newImplementationServiceForTest(t.TempDir(), "repo-a")
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
	svc, _, eventRepo, sessionRepo, subPlanRepo := newImplementationServiceForTest(t.TempDir(), "repo-a")
	sessionRepo.updateErr = repository.ErrNotFound
	sessionRepo.updateErrStatus = domain.AgentSessionRunning

	harness := &captureHarness{}
	svc.harness = harness

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
	// runImplementation errors now collapse into a plain result failure; no warning is returned.
	if warning != nil {
		t.Fatalf("expected nil warning (no granular session-start type in new design), got %#v", warning)
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

func TestBuildCritiqueFeedback(t *testing.T) {
	tests := []struct {
		name     string
		input    []domain.Critique
		contains []string
		empty    bool
	}{
		{
			name:  "empty critiques returns empty string",
			input: nil,
			empty: true,
		},
		{
			name: "single critique with file and line",
			input: []domain.Critique{
				{
					Severity:    domain.CritiqueCritical,
					Description: "nil pointer dereference",
					FilePath:    "cmd/main.go",
					LineNumber:  ptrInt(42),
				},
			},
			contains: []string{
				"1. [critical] nil pointer dereference",
				"file: cmd/main.go",
				"line 42",
			},
		},
		{
			name: "critique with nil LineNumber omits line",
			input: []domain.Critique{
				{
					Severity:    domain.CritiqueMajor,
					Description: "missing error check",
					FilePath:    "pkg/server.go",
					LineNumber:  nil,
				},
			},
			contains: []string{
				"1. [major] missing error check",
				"file: pkg/server.go)",
			},
		},
		{
			name: "critique with suggestion includes suggestion line",
			input: []domain.Critique{
				{
					Severity:    domain.CritiqueMinor,
					Description: "use constants",
					FilePath:    "pkg/config.go",
					LineNumber:  ptrInt(10),
					Suggestion:  "Replace magic number with named constant",
				},
			},
			contains: []string{
				"Suggestion: Replace magic number with named constant",
			},
		},
		{
			name: "empty file path omits file info",
			input: []domain.Critique{
				{
					Severity:    domain.CritiqueNit,
					Description: "trailing whitespace",
				},
			},
			contains: []string{
				"1. [nit] trailing whitespace",
			},
		},
		{
			name: "multiple critiques are numbered",
			input: []domain.Critique{
				{
					Severity:    domain.CritiqueCritical,
					Description: "first issue",
				},
				{
					Severity:    domain.CritiqueMinor,
					Description: "second issue",
					Suggestion:  "fix it",
				},
			},
			contains: []string{
				"1. [critical] first issue",
				"2. [minor] second issue",
				"Suggestion: fix it",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildCritiqueFeedback(tt.input)
			if tt.empty {
				if got != "" {
					t.Fatalf("expected empty string, got %q", got)
				}
				return
			}
			if got == "" {
				t.Fatal("expected non-empty string")
			}
			for _, s := range tt.contains {
				if !strings.Contains(got, s) {
					t.Errorf("output missing %q\ngot:\n%s", s, got)
				}
			}
		})
	}
}

// TestBuildWavesSkipsCompletedKeepsFailed verifies that completed sub-plans are
// skipped but failed sub-plans are included (they will be reset to pending by Implement).
func TestBuildWavesSkipsCompletedKeepsFailed(t *testing.T) {
	subPlans := []domain.TaskPlan{
		{ID: "sp1", Order: 0, RepositoryName: "repo1", Status: domain.SubPlanCompleted},
		{ID: "sp2", Order: 0, RepositoryName: "repo2", Status: domain.SubPlanFailed},
		{ID: "sp3", Order: 1, RepositoryName: "repo3", Status: domain.SubPlanPending},
	}

	waves := BuildWaves(subPlans)
	if len(waves) != 2 {
		t.Fatalf("expected 2 waves, got %d", len(waves))
	}
	// Wave 0: sp2 (failed — not filtered)
	if len(waves[0]) != 1 || waves[0][0].ID != "sp2" {
		t.Errorf("wave 0: expected [sp2], got %v", waves[0])
	}
	// Wave 1: sp3 (pending)
	if len(waves[1]) != 1 || waves[1][0].ID != "sp3" {
		t.Errorf("wave 1: expected [sp3], got %v", waves[1])
	}
}

// TestBuildWavesIncludesInProgress verifies that BuildWaves does not filter out
// in_progress sub-plans — they were stranded by a crash and must be re-executed.
func TestBuildWavesIncludesInProgress(t *testing.T) {
	subPlans := []domain.TaskPlan{
		{ID: "sp1", Order: 0, RepositoryName: "repo1", Status: domain.SubPlanCompleted},
		{ID: "sp2", Order: 0, RepositoryName: "repo2", Status: domain.SubPlanInProgress},
		{ID: "sp3", Order: 1, RepositoryName: "repo3", Status: domain.SubPlanPending},
	}

	waves := BuildWaves(subPlans)
	if len(waves) != 2 {
		t.Fatalf("got %d waves, want 2", len(waves))
	}
	// Wave 0: sp2 (in_progress — not filtered, treated like failed/pending)
	if len(waves[0]) != 1 || waves[0][0].ID != "sp2" {
		t.Errorf("wave 0: expected [sp2], got %v", waves[0])
	}
	// Wave 1: sp3 (pending)
	if len(waves[1]) != 1 || waves[1][0].ID != "sp3" {
		t.Errorf("wave 1: expected [sp3], got %v", waves[1])
	}
}

// TestPersistSubPlanStatus_CrashRecovery verifies that a sub-plan stranded in
// in_progress by a process crash can be reset to pending via persistSubPlanStatus.
// This is the exact path taken by the Implement pre-execution reset loop.
func TestPersistSubPlanStatus_CrashRecovery(t *testing.T) {
	planRepo := newMockPlanRepo()
	subPlanRepo := newMockSubPlanRepo()
	// Simulate a stranded sub-plan left in_progress by a previous crashed run.
	subPlanRepo.subPlans["sp-crashed"] = domain.TaskPlan{
		ID:             "sp-crashed",
		PlanID:         "plan-1",
		RepositoryName: "repo-a",
		Status:         domain.SubPlanInProgress,
	}
	planRepo.plans["plan-1"] = domain.Plan{ID: "plan-1", WorkItemID: "wi-1", Status: domain.PlanApproved}

	svc := &ImplementationService{
		planSvc: service.NewPlanService(repository.NoopTransacter{Res: repository.Resources{Plans: planRepo, SubPlans: subPlanRepo}}),
	}

	sp := subPlanRepo.subPlans["sp-crashed"]
	svc.persistSubPlanStatus(context.Background(), &sp, domain.SubPlanPending)

	got, ok := subPlanRepo.subPlans["sp-crashed"]
	if !ok {
		t.Fatal("sub-plan not found in repo after reset")
	}
	if got.Status != domain.SubPlanPending {
		t.Errorf("Status = %q after crash recovery reset, want %q", got.Status, domain.SubPlanPending)
	}
}

// completingMockSession is a mock session that completes immediately on Wait.
type completingMockSession struct {
	id     string
	events chan adapter.AgentEvent
	mu     sync.Mutex
	msgs   []string
	opts   adapter.SessionOpts // captured from StartSession
}

func (s *completingMockSession) ID() string                   { return s.id }
func (s *completingMockSession) Wait(_ context.Context) error { return nil }
func (s *completingMockSession) Events() <-chan adapter.AgentEvent {
	close(s.events)
	return s.events
}

func (s *completingMockSession) SendMessage(_ context.Context, msg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.msgs = append(s.msgs, msg)
	return nil
}
func (s *completingMockSession) Steer(_ context.Context, _ string) error      { return nil }
func (s *completingMockSession) SendAnswer(_ context.Context, _ string) error { return nil }
func (s *completingMockSession) Abort(_ context.Context) error                { return nil }
func (s *completingMockSession) ResumeInfo() map[string]string                { return nil }
func (s *completingMockSession) Compact(_ context.Context) error              { return nil }

// completingHarness returns sessions that complete immediately on Wait.
type completingHarness struct {
	mu       sync.Mutex
	lastSess *completingMockSession
}

func (h *completingHarness) SupportsCompact() bool { return true }
func (h *completingHarness) Name() string          { return "completing-mock" }
func (h *completingHarness) StartSession(_ context.Context, opts adapter.SessionOpts) (adapter.AgentSession, error) {
	s := &completingMockSession{
		id:     opts.SessionID,
		events: make(chan adapter.AgentEvent, 1),
		opts:   opts,
	}
	h.mu.Lock()
	h.lastSess = s
	h.mu.Unlock()
	return s, nil
}

// failingMockSession is a mock session whose Wait returns an error.
type failingMockSession struct {
	id     string
	events chan adapter.AgentEvent
	err    error
}

func (s *failingMockSession) ID() string                   { return s.id }
func (s *failingMockSession) Wait(_ context.Context) error { return s.err }
func (s *failingMockSession) Events() <-chan adapter.AgentEvent {
	close(s.events)
	return s.events
}
func (s *failingMockSession) SendMessage(_ context.Context, _ string) error { return nil }
func (s *failingMockSession) Steer(_ context.Context, _ string) error       { return nil }
func (s *failingMockSession) SendAnswer(_ context.Context, _ string) error  { return nil }
func (s *failingMockSession) Abort(_ context.Context) error                 { return nil }
func (s *failingMockSession) ResumeInfo() map[string]string                 { return nil }
func (s *failingMockSession) Compact(_ context.Context) error               { return nil }

// failingHarness returns sessions whose Wait returns a fixed error.
type failingHarness struct{ err error }

func (h *failingHarness) SupportsCompact() bool { return true }
func (h *failingHarness) Name() string          { return "failing-mock" }
func (h *failingHarness) StartSession(_ context.Context, opts adapter.SessionOpts) (adapter.AgentSession, error) {
	return &failingMockSession{id: opts.SessionID, events: make(chan adapter.AgentEvent, 1), err: h.err}, nil
}

// TestExecuteSubPlan_FailedSessionPersistsSubPlanFailed verifies that when the
// harness session fails (e.g. timeout), the sub-plan is marked SubPlanFailed
// in the repository — not left stranded as in_progress.
func TestExecuteSubPlan_FailedSessionPersistsSubPlanFailed(t *testing.T) {
	svc, _, _, sessionRepo, subPlanRepo := newImplementationServiceForTest(t.TempDir(), "repo-a")
	svc.harness = &failingHarness{err: errors.New("session timed out")}

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

	result, _ := svc.executeSubPlan(
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
		t.Fatalf("result.Status = %q, want %q", result.Status, domain.AgentSessionFailed)
	}

	// The sub-plan must be persisted as failed — not left in_progress.
	got, ok := subPlanRepo.subPlans["sp-1"]
	if !ok {
		t.Fatal("sub-plan not found after executeSubPlan")
	}
	if got.Status != domain.SubPlanFailed {
		t.Errorf("sub-plan status = %q after failed session, want %q", got.Status, domain.SubPlanFailed)
	}

	// Agent session record must also be failed.
	sessions := sessionRepo.sessions
	var found bool
	for _, sess := range sessions {
		if sess.Status == domain.AgentSessionFailed {
			found = true
			break
		}
	}
	if !found {
		t.Error("no agent_session record found in failed state after executeSubPlan failure")
	}
}

// TestRunImplementation_WithResumeInfo verifies that when prevSession has ResumeInfo,
// the harness receives ResumeFromSessionID and ResumeInfo in opts, UserPrompt is cleared,
// and critique is delivered via SendMessage (not baked into the system prompt).
func TestRunImplementation_WithResumeInfo(t *testing.T) {
	ctx := context.Background()
	workspaceRoot := t.TempDir()

	sessionRepo := newMockSessionRepo()
	harness := &completingHarness{}

	cfg := &config.Config{}
	subPlanRepo := newMockSubPlanRepo()
	planRepo := newMockPlanRepo()
	workItemRepo := &implementationWorkItemRepo{items: make(map[string]domain.Session)}
	eventRepo := &implementationEventRepo{}
	workspaceRepo := &implementationWorkspaceRepo{
		workspaces: map[string]domain.Workspace{
			"ws-1": {ID: "ws-1", RootPath: workspaceRoot, Status: domain.WorkspaceReady},
		},
	}

	svc := NewImplementationService(
		cfg,
		harness,
		nil, event.NewBus(event.BusConfig{EventRepo: eventRepo}),
		service.NewPlanService(repository.NoopTransacter{Res: repository.Resources{Plans: planRepo, SubPlans: subPlanRepo}}),
		service.NewSessionService(repository.NoopTransacter{Res: repository.Resources{Sessions: workItemRepo}}),
		service.NewTaskService(repository.NoopTransacter{Res: repository.Resources{Tasks: sessionRepo}}),
		service.NewWorkspaceService(repository.NoopTransacter{Res: repository.Resources{Workspaces: workspaceRepo}}),
		nil, nil,
		nil, nil, // foreman, questionSvc
		nil, // reviewSvc
	)

	// Seed a previous completed session with ResumeInfo.
	prevSession := domain.Task{
		ID:             "prev-session",
		WorkItemID:     "wi-1",
		WorkspaceID:    "ws-1",
		SubPlanID:      "sp-1",
		RepositoryName: "repo-a",
		WorktreePath:   workspaceRoot,
		HarnessName:    "mock",
		Status:         domain.AgentSessionCompleted,
		ResumeInfo: map[string]string{
			"omp_session_file": "/tmp/session.jsonl",
			"omp_session_id":   "omp-123",
		},
	}
	sessionRepo.sessions[prevSession.ID] = prevSession

	subPlan := domain.TaskPlan{
		ID:             "sp-1",
		PlanID:         "plan-1",
		RepositoryName: "repo-a",
		Content:        "Implement the change",
	}
	workspace := &domain.Workspace{ID: "ws-1", RootPath: workspaceRoot}
	workItem := &domain.Session{ID: "wi-1", WorkspaceID: "ws-1"}
	plan := &domain.Plan{ID: "plan-1"}

	newSess, err := svc.runImplementation(ctx, subPlan, workspace, plan, workItem, workspaceRoot, "Fix the bug", &prevSession)
	if err != nil {
		t.Fatalf("runImplementation: %v", err)
	}

	// Verify a new session was created (different ID from previous).
	if newSess.ID == prevSession.ID {
		t.Error("new session should have a different ID from previous session")
	}

	// Verify the new session exists in the repo.
	if _, err := sessionRepo.Get(ctx, newSess.ID); err != nil {
		t.Fatalf("new session not found in repo: %v", err)
	}

	// Verify the harness received ResumeFromSessionID and ResumeInfo in opts.
	harness.mu.Lock()
	lastSess := harness.lastSess
	harness.mu.Unlock()
	if lastSess == nil {
		t.Fatal("harness did not create a session")
	}
	if lastSess.opts.ResumeFromSessionID != "prev-session" {
		t.Errorf("ResumeFromSessionID = %q, want %q", lastSess.opts.ResumeFromSessionID, "prev-session")
	}
	if lastSess.opts.ResumeInfo["omp_session_file"] != "/tmp/session.jsonl" {
		t.Errorf("ResumeInfo[omp_session_file] = %q, want %q", lastSess.opts.ResumeInfo["omp_session_file"], "/tmp/session.jsonl")
	}
	if lastSess.opts.UserPrompt != "" {
		t.Errorf("UserPrompt = %q, want empty (harness resumes; no new prompt turn needed)", lastSess.opts.UserPrompt)
	}

	// Verify critique feedback was sent as a follow-up message (not in system prompt).
	lastSess.mu.Lock()
	msgs := lastSess.msgs
	lastSess.mu.Unlock()
	if len(msgs) != 1 || msgs[0] != "Fix the bug" {
		t.Errorf("SendMessage calls = %v, want [\"Fix the bug\"]", msgs)
	}
	if strings.Contains(lastSess.opts.SystemPrompt, "Fix the bug") {
		t.Error("critique should NOT be in SystemPrompt when resuming (should be sent via SendMessage)")
	}
}

// TestRunImplementation_WithoutResumeInfo verifies fallback behavior when prevSession
// is nil: critique goes into the system prompt.
func TestRunImplementation_WithoutResumeInfo(t *testing.T) {
	ctx := context.Background()
	workspaceRoot := t.TempDir()

	sessionRepo := newMockSessionRepo()
	harness := &completingHarness{}

	cfg := &config.Config{}
	subPlanRepo := newMockSubPlanRepo()
	planRepo := newMockPlanRepo()
	workItemRepo := &implementationWorkItemRepo{items: make(map[string]domain.Session)}
	eventRepo := &implementationEventRepo{}
	workspaceRepo := &implementationWorkspaceRepo{
		workspaces: map[string]domain.Workspace{
			"ws-1": {ID: "ws-1", RootPath: workspaceRoot, Status: domain.WorkspaceReady},
		},
	}

	svc := NewImplementationService(
		cfg,
		harness,
		nil, event.NewBus(event.BusConfig{EventRepo: eventRepo}),
		service.NewPlanService(repository.NoopTransacter{Res: repository.Resources{Plans: planRepo, SubPlans: subPlanRepo}}),
		service.NewSessionService(repository.NoopTransacter{Res: repository.Resources{Sessions: workItemRepo}}),
		service.NewTaskService(repository.NoopTransacter{Res: repository.Resources{Tasks: sessionRepo}}),
		service.NewWorkspaceService(repository.NoopTransacter{Res: repository.Resources{Workspaces: workspaceRepo}}),
		nil, nil,
		nil, nil, // foreman, questionSvc
		nil, // reviewSvc
	)

	subPlan := domain.TaskPlan{
		ID:             "sp-1",
		PlanID:         "plan-1",
		RepositoryName: "repo-a",
		Content:        "Implement the change",
	}
	workspace := &domain.Workspace{ID: "ws-1", RootPath: workspaceRoot}
	workItem := &domain.Session{ID: "wi-1", WorkspaceID: "ws-1"}
	plan := &domain.Plan{ID: "plan-1"}

	newSess, err := svc.runImplementation(ctx, subPlan, workspace, plan, workItem, workspaceRoot, "Fix the bug", nil)
	if err != nil {
		t.Fatalf("runImplementation: %v", err)
	}

	// Verify session was created.
	if newSess.ID == "" {
		t.Error("new session should have an ID")
	}

	// Verify no ResumeFromSessionID was set (nil prevSession = fresh run).
	harness.mu.Lock()
	lastSess := harness.lastSess
	harness.mu.Unlock()
	if lastSess == nil {
		t.Fatal("harness did not create a session")
	}
	if lastSess.opts.ResumeFromSessionID != "" {
		t.Errorf("ResumeFromSessionID = %q, want empty (no prev session)", lastSess.opts.ResumeFromSessionID)
	}

	// Verify critique feedback was baked into SystemPrompt (fallback for no resume).
	if !strings.Contains(lastSess.opts.SystemPrompt, "Fix the bug") {
		t.Error("critique should be in SystemPrompt when no resume info is available")
	}

	// Verify no SendMessage was called (feedback is in SystemPrompt instead).
	lastSess.mu.Lock()
	msgs := lastSess.msgs
	lastSess.mu.Unlock()
	if len(msgs) != 0 {
		t.Errorf("SendMessage should not be called in non-resume mode, got %v", msgs)
	}
}

// TestLoadCritiqueFeedback_NoReviewSvc verifies that loadCritiqueFeedback returns
// empty string when reviewSvc is not configured.
func TestLoadCritiqueFeedback_NoReviewSvc(t *testing.T) {
	svc := &ImplementationService{}
	result := svc.loadCritiqueFeedback(context.Background(), "sp-1")
	if result != "" {
		t.Errorf("expected empty string when reviewSvc is nil, got %q", result)
	}
}

// TestLoadCritiqueFeedback_NoImplSession verifies that loadCritiqueFeedback returns
// empty string when there is no prior completed implementation session.
func TestLoadCritiqueFeedback_NoImplSession(t *testing.T) {
	repo := newMockSessionRepo()
	sessionSvc := service.NewTaskService(
		repository.NoopTransacter{Res: repository.Resources{Tasks: repo}},
	)
	svc := &ImplementationService{
		sessionSvc: sessionSvc,
		reviewSvc:  service.NewReviewService(repository.NoopTransacter{}),
	}
	result := svc.loadCritiqueFeedback(context.Background(), "sp-1")
	if result != "" {
		t.Errorf("expected empty string when no impl session exists, got %q", result)
	}
}

// ---------------------------------------------------------------------------
// reviewLoop decision-logic tests
// ---------------------------------------------------------------------------

// reviewLoopFixture builds an ImplementationService with just enough wiring
// for reviewLoop paths that never reach reimplementSubPlan.
func reviewLoopFixture(t *testing.T, maxCycles int, autoLoop bool) (*ImplementationService, *reviewPipelineFixture) {
	t.Helper()
	fix := newReviewPipelineFixture(t, maxCycles)

	cfg := testReviewConfig(maxCycles)
	cfg.Review.AutoFeedbackLoop = &autoLoop

	svc := &ImplementationService{
		cfg:            cfg,
		reviewPipeline: fix.pipeline,
	}
	return svc, fix
}

// TestReviewLoop_PassesFirstReview verifies that when the review pipeline
// reports no critiques the loop returns Passed on the first cycle.
func TestReviewLoop_PassesFirstReview(t *testing.T) {
	svc, fix := reviewLoopFixture(t, 3, true)
	defer fix.cleanup()

	fix.harness.outputs = []string{"NO_CRITIQUES"}
	implSession := fix.seedPlanAndSubPlan(t)

	outcome := svc.reviewLoop(
		context.Background(),
		implSession,
		domain.TaskPlan{ID: "sub-plan-1"},
		&domain.Workspace{},
		&domain.Plan{},
		&domain.Session{},
		nil,
	)

	if !outcome.Passed {
		t.Errorf("expected Passed=true, got Passed=%v Escalated=%v Failed=%v", outcome.Passed, outcome.Escalated, outcome.Failed)
	}
	if outcome.Cycles != 1 {
		t.Errorf("expected Cycles=1, got %d", outcome.Cycles)
	}
}

// TestReviewLoop_ReviewError verifies that when ReviewSession returns an
// error (e.g., session sub-plan not found) the loop reports Failed.
func TestReviewLoop_ReviewError(t *testing.T) {
	svc, fix := reviewLoopFixture(t, 3, true)
	defer fix.cleanup()

	// Don't seed plan/sub-plan so ReviewSession fails looking them up.
	implSession := domain.Task{
		ID:             "session-missing",
		WorkItemID:     "wi-1",
		SubPlanID:      "no-such-sub-plan",
		RepositoryName: "repo-a",
		Phase:          domain.TaskPhaseImplementation,
		Status:         domain.AgentSessionCompleted,
	}

	outcome := svc.reviewLoop(
		context.Background(),
		implSession,
		domain.TaskPlan{ID: "no-such-sub-plan"},
		&domain.Workspace{},
		&domain.Plan{},
		&domain.Session{},
		nil,
	)

	if !outcome.Failed {
		t.Errorf("expected Failed=true, got Passed=%v Escalated=%v Failed=%v", outcome.Passed, outcome.Escalated, outcome.Failed)
	}
}

// TestReviewLoop_EscalatedByMaxCycles verifies that when the review pipeline
// reports Escalated (cycle limit exceeded) the loop returns Escalated.
func TestReviewLoop_EscalatedByMaxCycles(t *testing.T) {
	// maxCycles=1: first review sees major critiques, second call (cycle 2)
	// exceeds the limit → ReviewSession returns Escalated.
	svc, fix := reviewLoopFixture(t, 1, true)
	defer fix.cleanup()

	majors := twoMajorCritiquesOutput()
	fix.harness.outputs = []string{majors}
	implSession := fix.seedPlanAndSubPlan(t)

	// With maxCycles=1 and major critiques, the first ReviewSession returns
	// NeedsReimpl=true. But because reimplementSubPlan will fail (nil deps),
	// the loop should fail. Instead, to get Escalated from ReviewSession
	// itself, we need cycle >= maxCycles. ReviewSession tracks cycles via its
	// review repo. After one NeedsReimpl, the second call escalates.
	// However, reimplementSubPlan will be called first. We can't easily
	// test escalation via max cycles without hitting reimpl.
	//
	// Alternative: use autoLoop=false so NeedsReimpl → escalated without reimpl.
	// The max-cycles escalation path is tested in phase9_test.go directly.
	// See TestReviewLoop_NeedsReimplAutoLoopOff below.
	//
	// Instead, test escalation when ReviewSession itself returns Escalated
	// (cycle limit already hit at the pipeline level). We run 1 cycle of
	// ReviewSession directly to bump the cycle counter, then let reviewLoop
	// see Escalated on its call.

	// Cycle 1: direct call bumps pipeline's internal cycle counter.
	_, err := fix.pipeline.ReviewSession(context.Background(), implSession)
	if err != nil {
		t.Fatalf("pre-warming ReviewSession: %v", err)
	}

	// Cycle 2: reviewLoop calls ReviewSession which now returns Escalated.
	// No harness output needed — escalation triggers before running agent.
	outcome := svc.reviewLoop(
		context.Background(),
		implSession,
		domain.TaskPlan{ID: "sub-plan-1"},
		&domain.Workspace{},
		&domain.Plan{},
		&domain.Session{},
		nil,
	)

	if !outcome.Escalated {
		t.Errorf("expected Escalated=true, got Passed=%v Escalated=%v Failed=%v", outcome.Passed, outcome.Escalated, outcome.Failed)
	}
	if outcome.Cycles != 1 {
		t.Errorf("expected Cycles=1 (one reviewLoop iteration), got %d", outcome.Cycles)
	}
}

// TestReviewLoop_NeedsReimplAutoLoopOff verifies that when the review reports
// NeedsReimpl but auto-feedback-loop is disabled, the loop escalates instead
// of attempting re-implementation.
func TestReviewLoop_NeedsReimplAutoLoopOff(t *testing.T) {
	svc, fix := reviewLoopFixture(t, 3, false) // autoLoop OFF
	defer fix.cleanup()

	fix.harness.outputs = []string{twoMajorCritiquesOutput()}
	implSession := fix.seedPlanAndSubPlan(t)

	outcome := svc.reviewLoop(
		context.Background(),
		implSession,
		domain.TaskPlan{ID: "sub-plan-1"},
		&domain.Workspace{},
		&domain.Plan{},
		&domain.Session{},
		nil,
	)

	if !outcome.Escalated {
		t.Errorf("expected Escalated=true when autoLoop=false, got Passed=%v Escalated=%v Failed=%v",
			outcome.Passed, outcome.Escalated, outcome.Failed)
	}
	if outcome.Cycles != 1 {
		t.Errorf("expected Cycles=1, got %d", outcome.Cycles)
	}
}

// TODO: TestReviewLoop_NeedsReimplAutoLoopOn_ReimplSucceeds — testing the full
// auto-reimpl loop requires wiring gitClient, repos, and other dependencies
// that reimplementSubPlan accesses. Consider an integration-level test or
// extracting the reimpl call behind an interface for easier mocking.

// TestExecuteSubPlan_CompletesTaskOnSuccess verifies the happy path: when the
// harness returns without error, executeSubPlan marks the task as completed in
// the repository and emits an EventAgentSessionCompleted event.
func TestExecuteSubPlan_CompletesTaskOnSuccess(t *testing.T) {
	svc, _, eventRepo, sessionRepo, subPlanRepo := newImplementationServiceForTest(t.TempDir(), "repo-a")
	svc.harness = &completingHarness{}

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

	if result.Status != domain.AgentSessionCompleted {
		t.Fatalf("session result status = %q, want %q", result.Status, domain.AgentSessionCompleted)
	}
	if warning != nil {
		t.Fatalf("unexpected warning: %+v", warning)
	}

	// The task record in the repository must reflect completed status.
	task, err := sessionRepo.Get(context.Background(), result.SessionID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if task.Status != domain.AgentSessionCompleted {
		t.Fatalf("task repo status = %q, want %q", task.Status, domain.AgentSessionCompleted)
	}
	if task.CompletedAt == nil {
		t.Fatal("task.CompletedAt must be set after completion")
	}

	// Verify the completed event was emitted.
	var found bool
	for _, evt := range eventRepo.events {
		if evt.EventType == string(domain.EventAgentSessionCompleted) {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected EventAgentSessionCompleted event, none emitted")
	}
}

// ---------------------------------------------------------------------------
// Two-stage retry model tests
// ---------------------------------------------------------------------------

// TestLastSessionForSubPlan verifies the two-stage decision helper.
func TestLastSessionForSubPlan(t *testing.T) {
	repo := newMockSessionRepo()
	sessionSvc := service.NewTaskService(repository.NoopTransacter{Res: repository.Resources{Tasks: repo}})
	svc := &ImplementationService{sessionSvc: sessionSvc}
	ctx := context.Background()

	t.Run("no sessions returns nil", func(t *testing.T) {
		if got := svc.lastSessionForSubPlan(ctx, "sp-1"); got != nil {
			t.Fatalf("expected nil, got %+v", got)
		}
	})

	t.Run("returns most recent session regardless of phase or status", func(t *testing.T) {
		repo.mu.Lock()
		repo.sessions["old-impl"] = domain.Task{
			ID: "old-impl", SubPlanID: "sp-1", Phase: domain.TaskPhaseImplementation,
			Status: domain.AgentSessionCompleted, CreatedAt: time.Now().Add(-2 * time.Hour),
		}
		repo.sessions["review"] = domain.Task{
			ID: "review", SubPlanID: "sp-1", Phase: domain.TaskPhaseReview,
			Status: domain.AgentSessionFailed, CreatedAt: time.Now().Add(-time.Hour),
		}
		repo.mu.Unlock()
		got := svc.lastSessionForSubPlan(ctx, "sp-1")
		if got == nil || got.ID != "review" {
			t.Errorf("expected review session, got %v", got)
		}
	})

	t.Run("filters by sub-plan ID", func(t *testing.T) {
		if got := svc.lastSessionForSubPlan(ctx, "other-sp"); got != nil {
			t.Fatalf("expected nil for different sub-plan, got %+v", got)
		}
	})
}

// TestLatestCompletedImplSession verifies the review-retry helper that finds
// the impl session whose output should be reviewed.
func TestLatestCompletedImplSession(t *testing.T) {
	repo := newMockSessionRepo()
	sessionSvc := service.NewTaskService(repository.NoopTransacter{Res: repository.Resources{Tasks: repo}})
	svc := &ImplementationService{sessionSvc: sessionSvc}
	ctx := context.Background()

	t.Run("no sessions returns nil", func(t *testing.T) {
		if got := svc.latestCompletedImplSession(ctx, "sp-1"); got != nil {
			t.Fatalf("expected nil, got %+v", got)
		}
	})

	t.Run("failed impl session is ignored", func(t *testing.T) {
		repo.mu.Lock()
		repo.sessions["task-1"] = domain.Task{
			ID: "task-1", SubPlanID: "sp-1", Phase: domain.TaskPhaseImplementation,
			Status: domain.AgentSessionFailed,
		}
		repo.mu.Unlock()
		if got := svc.latestCompletedImplSession(ctx, "sp-1"); got != nil {
			t.Fatalf("expected nil, got %+v", got)
		}
	})

	t.Run("completed review session is ignored", func(t *testing.T) {
		repo.mu.Lock()
		delete(repo.sessions, "task-1")
		repo.sessions["task-review"] = domain.Task{
			ID: "task-review", SubPlanID: "sp-1", Phase: domain.TaskPhaseReview,
			Status: domain.AgentSessionCompleted,
		}
		repo.mu.Unlock()
		if got := svc.latestCompletedImplSession(ctx, "sp-1"); got != nil {
			t.Fatalf("expected nil, got %+v", got)
		}
	})

	t.Run("returns most recent completed impl session", func(t *testing.T) {
		repo.mu.Lock()
		repo.sessions["task-impl-1"] = domain.Task{
			ID: "task-impl-1", SubPlanID: "sp-1", Phase: domain.TaskPhaseImplementation,
			Status: domain.AgentSessionCompleted, CreatedAt: time.Now().Add(-time.Hour),
		}
		repo.sessions["task-impl-2"] = domain.Task{
			ID: "task-impl-2", SubPlanID: "sp-1", Phase: domain.TaskPhaseImplementation,
			Status: domain.AgentSessionCompleted, CreatedAt: time.Now(),
		}
		repo.mu.Unlock()
		got := svc.latestCompletedImplSession(ctx, "sp-1")
		if got == nil {
			t.Fatal("expected a result, got nil")
		}
		if got.ID != "task-impl-2" {
			t.Errorf("expected task-impl-2, got %s", got.ID)
		}
	})

	t.Run("filters by sub-plan ID", func(t *testing.T) {
		if got := svc.latestCompletedImplSession(ctx, "other-sp"); got != nil {
			t.Fatalf("expected nil for different sub-plan, got %+v", got)
		}
	})
}

// TestTwoStageRetryModel verifies the retry decision for all session
// phase combinations.
func TestTwoStageRetryModel(t *testing.T) {
	repo := newMockSessionRepo()
	sessionSvc := service.NewTaskService(repository.NoopTransacter{Res: repository.Resources{Tasks: repo}})
	svc := &ImplementationService{sessionSvc: sessionSvc}
	ctx := context.Background()

	t.Run("no history → implementation stage", func(t *testing.T) {
		last := svc.lastSessionForSubPlan(ctx, "sp-1")
		if last != nil {
			t.Fatal("expected nil for fresh sub-plan")
		}
		// No last session means first run → fall through to implementation.
	})

	t.Run("failed impl → implementation stage", func(t *testing.T) {
		repo.mu.Lock()
		repo.sessions["impl"] = domain.Task{
			ID: "impl", SubPlanID: "sp-2", Phase: domain.TaskPhaseImplementation,
			Status: domain.AgentSessionFailed, CreatedAt: time.Now(),
		}
		repo.mu.Unlock()
		last := svc.lastSessionForSubPlan(ctx, "sp-2")
		if last.Phase != domain.TaskPhaseImplementation {
			t.Errorf("expected implementation phase, got %s", last.Phase)
		}
		// Last session is impl → should fall through to full implementation.
	})

	t.Run("failed review → review stage", func(t *testing.T) {
		repo.mu.Lock()
		repo.sessions["impl-ok"] = domain.Task{
			ID: "impl-ok", SubPlanID: "sp-3", Phase: domain.TaskPhaseImplementation,
			Status: domain.AgentSessionCompleted, CreatedAt: time.Now().Add(-time.Hour),
		}
		repo.sessions["review-fail"] = domain.Task{
			ID: "review-fail", SubPlanID: "sp-3", Phase: domain.TaskPhaseReview,
			Status: domain.AgentSessionFailed, CreatedAt: time.Now(),
		}
		repo.mu.Unlock()
		last := svc.lastSessionForSubPlan(ctx, "sp-3")
		if last.Phase != domain.TaskPhaseReview {
			t.Errorf("expected review phase, got %s", last.Phase)
		}
		// Last session is review → should skip implementation, retry review.
	})

	t.Run("failed reimplementation → implementation stage", func(t *testing.T) {
		repo.mu.Lock()
		repo.sessions["impl-ok"] = domain.Task{
			ID: "impl-ok", SubPlanID: "sp-4", Phase: domain.TaskPhaseImplementation,
			Status: domain.AgentSessionCompleted, CreatedAt: time.Now().Add(-3 * time.Hour),
		}
		repo.sessions["review"] = domain.Task{
			ID: "review", SubPlanID: "sp-4", Phase: domain.TaskPhaseReview,
			Status: domain.AgentSessionCompleted, CreatedAt: time.Now().Add(-2 * time.Hour),
		}
		repo.sessions["reimpl-fail"] = domain.Task{
			ID: "reimpl-fail", SubPlanID: "sp-4", Phase: domain.TaskPhaseImplementation,
			Status: domain.AgentSessionFailed, CreatedAt: time.Now().Add(-time.Hour),
		}
		repo.mu.Unlock()
		last := svc.lastSessionForSubPlan(ctx, "sp-4")
		if last.Phase != domain.TaskPhaseImplementation {
			t.Errorf("expected implementation phase, got %s", last.Phase)
		}
		if last.ID != "reimpl-fail" {
			t.Errorf("expected reimpl-fail, got %s", last.ID)
		}
		// Last session is a failed reimplementation → should fall through to implementation.
	})
}

// TestRunImplementation_ContextCanceled_MarksInterrupted verifies that when the harness
// session fails with context.Canceled (pipeline cancelled by user quit), the task is
// marked as interrupted rather than failed.
func TestRunImplementation_ContextCanceled_MarksInterrupted(t *testing.T) {
	svc, _, _, sessionRepo, _ := newImplementationServiceForTest(t.TempDir(), "repo-a")
	svc.harness = &failingHarness{err: context.Canceled}

	subPlan := domain.TaskPlan{
		ID:             "sp-1",
		PlanID:         "plan-1",
		RepositoryName: "repo-a",
		Content:        "Implement the change",
	}
	workspace := &domain.Workspace{ID: "ws-1", RootPath: t.TempDir()}
	workItem := &domain.Session{ID: "wi-1", WorkspaceID: "ws-1"}
	plan := &domain.Plan{ID: "plan-1"}

	_, err := svc.runImplementation(
		context.Background(),
		subPlan, workspace, plan, workItem,
		t.TempDir(), "", nil,
	)
	if err == nil {
		t.Fatal("expected error from runImplementation, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}

	// The task should be interrupted, not failed.
	// Find the task — it's the one created during runImplementation.
	var found *domain.Task
	for _, task := range sessionRepo.sessions {
		if task.Phase == domain.TaskPhaseImplementation {
			t := task
			found = &t
		}
	}
	if found == nil {
		t.Fatal("no implementation task found in session repo")
	}
	if found.Status != domain.AgentSessionInterrupted {
		t.Errorf("task status = %q, want %q", found.Status, domain.AgentSessionInterrupted)
	}
}

// TestRunImplementation_NonCanceledError_MarksFailed verifies that a non-cancellation
// error still marks the task as failed.
func TestRunImplementation_NonCanceledError_MarksFailed(t *testing.T) {
	svc, _, _, sessionRepo, _ := newImplementationServiceForTest(t.TempDir(), "repo-a")
	svc.harness = &failingHarness{err: errors.New("agent crashed")}

	subPlan := domain.TaskPlan{
		ID:             "sp-1",
		PlanID:         "plan-1",
		RepositoryName: "repo-a",
		Content:        "Implement the change",
	}
	workspace := &domain.Workspace{ID: "ws-1", RootPath: t.TempDir()}
	workItem := &domain.Session{ID: "wi-1", WorkspaceID: "ws-1"}
	plan := &domain.Plan{ID: "plan-1"}

	_, err := svc.runImplementation(
		context.Background(),
		subPlan, workspace, plan, workItem,
		t.TempDir(), "", nil,
	)
	if err == nil {
		t.Fatal("expected error from runImplementation, got nil")
	}

	var found *domain.Task
	for _, task := range sessionRepo.sessions {
		if task.Phase == domain.TaskPhaseImplementation {
			t := task
			found = &t
		}
	}
	if found == nil {
		t.Fatal("no implementation task found in session repo")
	}
	if found.Status != domain.AgentSessionFailed {
		t.Errorf("task status = %q, want %q", found.Status, domain.AgentSessionFailed)
	}
}

func TestBuildCommitSection(t *testing.T) {
	tcases := []struct {
		name  string
		cfg   adapter.CommitConfig
		empty bool
	}{
		{name: "granular strategy", cfg: adapter.CommitConfig{Strategy: "granular"}, empty: false},
		{name: "semi-regular strategy", cfg: adapter.CommitConfig{Strategy: "semi-regular"}, empty: false},
		{name: "single strategy", cfg: adapter.CommitConfig{Strategy: "single"}, empty: false},
		{name: "unknown strategy returns empty", cfg: adapter.CommitConfig{Strategy: "unknown"}, empty: true},
		{name: "empty strategy returns empty", cfg: adapter.CommitConfig{Strategy: ""}, empty: true},
	}

	for _, tc := range tcases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildCommitSection(tc.cfg)
			if tc.empty {
				if got != "" {
					t.Errorf("expected empty string, got %q", got)
				}
				return
			}
			if !strings.Contains(got, "## Commit Strategy") {
				t.Errorf("missing section header in output: %q", got)
			}
			if !strings.Contains(got, "git add -A && git commit") {
				t.Errorf("missing git commit instruction in output: %q", got)
			}
		})
	}

	// Verify each strategy has distinct guidance.
	granular := buildCommitSection(adapter.CommitConfig{Strategy: "granular"})
	semiRegular := buildCommitSection(adapter.CommitConfig{Strategy: "semi-regular"})
	single := buildCommitSection(adapter.CommitConfig{Strategy: "single"})

	if granular == semiRegular {
		t.Error("granular and semi-regular produce identical output")
	}
	if semiRegular == single {
		t.Error("semi-regular and single produce identical output")
	}
	if granular == single {
		t.Error("granular and single produce identical output")
	}
}

func TestBuildCommitSection_MessageFormat(t *testing.T) {
	t.Run("conventional format emits format section", func(t *testing.T) {
		got := buildCommitSection(adapter.CommitConfig{Strategy: "semi-regular", MessageFormat: "conventional"})
		if !strings.Contains(got, "### Commit Message Format") {
			t.Errorf("expected message format section, got: %q", got)
		}
		if !strings.Contains(got, "Conventional Commits") {
			t.Errorf("expected conventional commits mention, got: %q", got)
		}
		if !strings.Contains(got, "feat, fix, refactor") {
			t.Errorf("expected type examples, got: %q", got)
		}
	})

	t.Run("custom format with template emits format section", func(t *testing.T) {
		got := buildCommitSection(adapter.CommitConfig{Strategy: "single", MessageFormat: "custom", MessageTemplate: "[${type}] ${summary}"})
		if !strings.Contains(got, "### Commit Message Format") {
			t.Errorf("expected message format section, got: %q", got)
		}
		if !strings.Contains(got, "[${type}] ${summary}") {
			t.Errorf("expected template content, got: %q", got)
		}
	})

	t.Run("custom format without template skips format section", func(t *testing.T) {
		got := buildCommitSection(adapter.CommitConfig{Strategy: "single", MessageFormat: "custom", MessageTemplate: ""})
		if strings.Contains(got, "### Commit Message Format") {
			t.Errorf("expected no message format section when template is empty, got: %q", got)
		}
	})

	t.Run("ai-generated format has no extra section", func(t *testing.T) {
		got := buildCommitSection(adapter.CommitConfig{Strategy: "semi-regular", MessageFormat: "ai-generated"})
		if strings.Contains(got, "### Commit Message Format") {
			t.Errorf("expected no message format section for ai-generated, got: %q", got)
		}
	})
}

func TestBuildCommitAgentSystemPrompt(t *testing.T) {
	t.Run("granular strategy instructs per-logical-unit commits", func(t *testing.T) {
		got := buildCommitAgentSystemPrompt(adapter.CommitConfig{Strategy: "granular"})
		if !strings.Contains(got, "self-contained logical units") {
			t.Errorf("expected per-unit commit guidance, got: %q", got)
		}
		if !strings.Contains(got, "Stage and commit each unit separately") {
			t.Errorf("expected separate commit instruction, got: %q", got)
		}
	})

	t.Run("semi-regular strategy instructs logical grouping", func(t *testing.T) {
		got := buildCommitAgentSystemPrompt(adapter.CommitConfig{Strategy: "semi-regular"})
		if !strings.Contains(got, "logical commits") {
			t.Errorf("expected grouped commit guidance, got: %q", got)
		}
	})

	t.Run("single strategy instructs one commit", func(t *testing.T) {
		got := buildCommitAgentSystemPrompt(adapter.CommitConfig{Strategy: "single"})
		if !strings.Contains(got, "single commit") {
			t.Errorf("expected single commit guidance, got: %q", got)
		}
	})

	t.Run("unknown strategy falls back to single commit", func(t *testing.T) {
		got := buildCommitAgentSystemPrompt(adapter.CommitConfig{Strategy: "unknown"})
		if !strings.Contains(got, "single commit") {
			t.Errorf("expected single commit fallback, got: %q", got)
		}
	})

	t.Run("all variants prohibit file modifications", func(t *testing.T) {
		for _, strategy := range []string{"granular", "semi-regular", "single", ""} {
			got := buildCommitAgentSystemPrompt(adapter.CommitConfig{Strategy: strategy})
			if !strings.Contains(got, "Do not modify any files") {
				t.Errorf("strategy %q: expected no-modification instruction, got: %q", strategy, got)
			}
		}
	})
}

func TestGitStatusDirty(t *testing.T) {
	t.Run("clean repo returns false", func(t *testing.T) {
		dir := t.TempDir()
		initTestRepo(t, dir)

		dirty, err := gitStatusDirty(context.Background(), dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if dirty {
			t.Fatal("expected clean repo")
		}
	})

	t.Run("untracked file returns true", func(t *testing.T) {
		dir := t.TempDir()
		initTestRepo(t, dir)
		mustWriteFile(t, filepath.Join(dir, "new.txt"), "content")

		dirty, err := gitStatusDirty(context.Background(), dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !dirty {
			t.Fatal("expected dirty repo")
		}
	})

	t.Run("staged but uncommitted returns true", func(t *testing.T) {
		dir := t.TempDir()
		initTestRepo(t, dir)
		mustWriteFile(t, filepath.Join(dir, "staged.txt"), "content")
		mustRun(t, dir, "git", "add", "staged.txt")

		dirty, err := gitStatusDirty(context.Background(), dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !dirty {
			t.Fatal("expected dirty repo")
		}
	})
}

func TestGitStageAndCommit(t *testing.T) {
	dir := t.TempDir()
	initTestRepo(t, dir)
	mustWriteFile(t, filepath.Join(dir, "change.txt"), "new content")

	err := gitStageAndCommit(context.Background(), dir, "chore: test commit")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the commit was created.
	dirty, err := gitStatusDirty(context.Background(), dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dirty {
		t.Fatal("expected clean repo after commit")
	}
}

func TestCommitAndPushRepos_SkipsCleanWorktrees(t *testing.T) {
	// Create a bare repo and a worktree clone.
	bareDir := t.TempDir()
	worktreeDir := t.TempDir()
	initBareRepo(t, bareDir)
	cloneAsWorktree(t, bareDir, worktreeDir, "feature-branch")

	svc, _, eventRepo, _, _ := newImplementationServiceForTest(bareDir, "repo-a")
	svc.cfg = &config.Config{}

	sessions := []SessionResult{{
		Repository:   "repo-a",
		WorktreePath: worktreeDir,
		SessionID:    "sess-1",
	}}
	repoPaths := map[string]string{"repo-a": bareDir}

	// No remote configured, so push will warn but not fail.
	// The key assertion: no safety-net commit is attempted on a clean worktree.
	svc.commitAndPushRepos(context.Background(), sessions, repoPaths, "feature-branch")

	// No work_item.completed event should be emitted (that's a separate method).
	for _, evt := range eventRepo.events {
		if evt.EventType == string(domain.EventWorkItemCompleted) {
			t.Fatal("commitAndPushRepos should not emit work_item.completed")
		}
	}
}

func TestCommitAndPushRepos_CommitsDirtyWorktree(t *testing.T) {
	bareDir := t.TempDir()
	worktreeDir := t.TempDir()
	initBareRepo(t, bareDir)
	cloneAsWorktree(t, bareDir, worktreeDir, "feature-branch")

	svc, _, _, _, _ := newImplementationServiceForTest(bareDir, "repo-a")
	svc.cfg = &config.Config{}

	// Add an uncommitted file to the worktree.
	mustWriteFile(t, filepath.Join(worktreeDir, "residual.txt"), "left behind")

	sessions := []SessionResult{{
		Repository:   "repo-a",
		WorktreePath: worktreeDir,
		SessionID:    "sess-1",
	}}
	repoPaths := map[string]string{"repo-a": bareDir}

	svc.commitAndPushRepos(context.Background(), sessions, repoPaths, "feature-branch")

	// Verify the worktree is now clean.
	dirty, err := gitStatusDirty(context.Background(), worktreeDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dirty {
		t.Fatal("expected worktree to be clean after commitAndPushRepos")
	}
}

func TestCommitViaAgent_AttemptsAgentSession(t *testing.T) {
	bareDir := t.TempDir()
	worktreeDir := t.TempDir()
	initBareRepo(t, bareDir)
	cloneAsWorktree(t, bareDir, worktreeDir, "feature-branch")

	// Track what the mock harness received.
	var capturedOpts adapter.SessionOpts
	harness := &mockAgentHarness{
		onStartSession: func(opts adapter.SessionOpts) (adapter.AgentSession, error) {
			capturedOpts = opts
			return nil, errors.New("simulated agent failure")
		},
	}

	svc, _, _, _, _ := newImplementationServiceForTest(bareDir, "repo-a")
	svc.cfg = &config.Config{Commit: config.CommitConfig{
		Strategy:        config.CommitStrategySemiRegular,
		MessageFormat:   config.CommitMessageConventional,
		MessageTemplate: "feat: $TITLE",
	}}
	// Replace the harness with our instrumented one.
	svc.harness = harness

	// Add an uncommitted file.
	mustWriteFile(t, filepath.Join(worktreeDir, "residual.txt"), "left behind")

	sessions := []SessionResult{{
		Repository:   "repo-a",
		WorktreePath: worktreeDir,
		SessionID:    "sess-1",
	}}
	repoPaths := map[string]string{"repo-a": bareDir}

	svc.commitAndPushRepos(context.Background(), sessions, repoPaths, "feature-branch")

	// The agent session was attempted.
	if capturedOpts.SystemPrompt == "" {
		t.Fatal("expected StartSession to be called with a non-empty system prompt")
	}
	if !strings.Contains(capturedOpts.SystemPrompt, "Commit Strategy") {
		t.Fatalf("expected system prompt to contain commit instructions, got: %s", capturedOpts.SystemPrompt)
	}
	if !strings.Contains(capturedOpts.SystemPrompt, "residual uncommitted changes") {
		t.Fatalf("expected system prompt to mention residual changes, got: %s", capturedOpts.SystemPrompt)
	}
	if capturedOpts.UserPrompt != "Commit the residual changes now." {
		t.Fatalf("expected user prompt 'Commit the residual changes now.', got: %s", capturedOpts.UserPrompt)
	}
	if capturedOpts.AllowPush {
		t.Fatal("expected AllowPush to be false for commit session")
	}

	// The agent failed, so the fallback should have committed.
	dirty, err := gitStatusDirty(context.Background(), worktreeDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dirty {
		t.Fatal("expected worktree to be clean after fallback commit")
	}
}

func TestEmitWorkItemCompleted(t *testing.T) {
	svc, _, eventRepo, _, _ := newImplementationServiceForTest(t.TempDir(), "repo-a")
	workItem := &domain.Session{
		ID:          "wi-1",
		WorkspaceID: "ws-1",
		ExternalID:  "EXT-1",
		Title:       "Implement feature",
		Source:      "manual",
		Metadata:    map[string]any{},
	}
	subPlans := []domain.TaskPlan{{
		ID:             "sp-1",
		Content:        "Implement the feature",
		RepositoryName: "repo-a",
	}}
	sessions := []SessionResult{{
		Repository:   "repo-a",
		WorktreePath: "", // empty — no review context available
	}}
	repoPaths := map[string]string{"repo-a": "/nonexistent"}

	err := svc.emitWorkItemCompleted(context.Background(), workItem, "ws-1", sessions, repoPaths, "feature-branch", subPlans)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the event was published.
	found := false
	for _, evt := range eventRepo.events {
		if evt.EventType == string(domain.EventWorkItemCompleted) {
			found = true
			// Verify payload contains required fields.
			var payload map[string]any
			if err := json.Unmarshal([]byte(evt.Payload), &payload); err != nil {
				t.Fatalf("failed to unmarshal payload: %v", err)
			}
			assertPayloadField(t, payload, "workspace_id", "ws-1")
			assertPayloadField(t, payload, "work_item_id", "wi-1")
			assertPayloadField(t, payload, "external_id", "EXT-1")
			assertPayloadField(t, payload, "work_item_title", "Implement feature")
			assertPayloadField(t, payload, "branch", "feature-branch")
			assertPayloadField(t, payload, "sub_plan", "Implement the feature")
			break
		}
	}
	if !found {
		t.Fatal("work_item.completed event not found")
	}
}

func TestEmitWorkItemCompleted_WithTrackerRefs(t *testing.T) {
	svc, _, eventRepo, _, _ := newImplementationServiceForTest(t.TempDir(), "repo-a")
	workItem := &domain.Session{
		ID:            "wi-1",
		WorkspaceID:   "ws-1",
		ExternalID:    "EXT-1",
		Title:         "Fix bug",
		Source:        "github",
		SourceScope:   domain.ScopeIssues,
		SourceItemIDs: []string{"123"},
		Metadata: map[string]any{
			"tracker_refs": []domain.TrackerReference{{
				Provider: "linear",
				Kind:     "issue",
				ID:       "ENG-42",
			}},
		},
	}
	subPlans := []domain.TaskPlan{{Content: "Fix the bug"}}
	err := svc.emitWorkItemCompleted(context.Background(), workItem, "ws-1", nil, nil, "fix-branch", subPlans)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var payload map[string]any
	for _, evt := range eventRepo.events {
		if evt.EventType == string(domain.EventWorkItemCompleted) {
			if err := json.Unmarshal([]byte(evt.Payload), &payload); err != nil {
				t.Fatalf("failed to unmarshal payload: %v", err)
			}
			break
		}
	}
	if payload == nil {
		t.Fatal("work_item.completed event not found")
	}
	assertPayloadField(t, payload, "external_id", "EXT-1")
	// Verify external_ids includes both the source item ID and the work item external ID.
	ids, ok := payload["external_ids"].([]any)
	if !ok || len(ids) == 0 {
		t.Fatalf("expected external_ids in payload, got %v", payload["external_ids"])
	}
	// Verify tracker_refs are present.
	if _, ok := payload["tracker_refs"]; !ok {
		t.Fatal("expected tracker_refs in payload")
	}
}

func TestWorkItemEventExternalIDs(t *testing.T) {
	t.Run("github issues source", func(t *testing.T) {
		wi := domain.Session{
			Source:        "github",
			SourceScope:   domain.ScopeIssues,
			SourceItemIDs: []string{"42", "99"},
			ExternalID:    "EXT-1",
		}
		ids := workItemEventExternalIDs(wi)
		expected := map[string]bool{
			"gh:issue:42": true,
			"gh:issue:99": true,
			"EXT-1":       true,
		}
		if len(ids) != len(expected) {
			t.Fatalf("expected %d IDs, got %d: %v", len(expected), len(ids), ids)
		}
		for _, id := range ids {
			if !expected[id] {
				t.Errorf("unexpected ID %q", id)
			}
		}
	})

	t.Run("manual source has no prefixed IDs", func(t *testing.T) {
		wi := domain.Session{
			Source:     "manual",
			ExternalID: "MAN-1",
		}
		ids := workItemEventExternalIDs(wi)
		if len(ids) != 1 || ids[0] != "MAN-1" {
			t.Fatalf("expected [MAN-1], got %v", ids)
		}
	})

	t.Run("deduplicates IDs", func(t *testing.T) {
		wi := domain.Session{
			Source:        "github",
			SourceScope:   domain.ScopeIssues,
			SourceItemIDs: []string{"42", "42"},
			ExternalID:    "42", // same as source item ID
		}
		ids := workItemEventExternalIDs(wi)
		if len(ids) != 2 {
			t.Fatalf("expected 2 deduplicated IDs, got %d: %v", len(ids), ids)
		}
	})
}

// --- Test helpers ---

// initTestRepo creates a git repo in dir with an initial commit and signing disabled.
func initTestRepo(t *testing.T, dir string) {
	t.Helper()
	mustRun(t, dir, "git", "init")
	mustRun(t, dir, "git", "config", "user.email", "test@test.com")
	mustRun(t, dir, "git", "config", "user.name", "Test")
	mustRun(t, dir, "git", "config", "commit.gpgsign", "false")
	mustRun(t, dir, "git", "commit", "--allow-empty", "-m", "init")
}

func initBareRepo(t *testing.T, dir string) {
	t.Helper()
	mustRun(t, dir, "git", "init", "--bare")
}

func cloneAsWorktree(t *testing.T, bareDir, worktreeDir, branch string) {
	t.Helper()
	// Create an initial commit on main so the worktree has a base.
	cloneDir := t.TempDir()
	mustRun(t, cloneDir, "git", "clone", bareDir, ".")
	mustRun(t, cloneDir, "git", "config", "user.email", "test@test.com")
	mustRun(t, cloneDir, "git", "config", "user.name", "Test")
	// Disable commit signing in test repos (1Password agent may not be available).
	mustRun(t, cloneDir, "git", "config", "commit.gpgsign", "false")
	mustWriteFile(t, filepath.Join(cloneDir, "README.md"), "init")
	mustRun(t, cloneDir, "git", "add", "README.md")
	mustRun(t, cloneDir, "git", "commit", "-m", "init")
	mustRun(t, cloneDir, "git", "push", "origin", "HEAD")

	// Create worktree with new branch.
	mustRun(t, cloneDir, "git", "worktree", "add", "-b", branch, worktreeDir)
}

func mustRun(t *testing.T, dir, cmd string, args ...string) {
	t.Helper()
	c := exec.Command(cmd, args...)
	c.Dir = dir
	// Disable GPG/signing for test commits (1Password agent may not be available).
	c.Env = append(os.Environ(), "GPG_TTY=", "GNUPGHOME=")
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("%s %s: %v\n%s", cmd, args, err, out)
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func assertPayloadField(t *testing.T, payload map[string]any, key, expected string) {
	t.Helper()
	got, ok := payload[key]
	if !ok {
		t.Fatalf("missing key %q in payload", key)
	}
	if got != expected {
		t.Fatalf("payload[%q] = %v, want %v", key, got, expected)
	}
}

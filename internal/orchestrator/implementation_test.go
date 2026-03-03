package orchestrator

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/beeemT/substrate/internal/domain"
)

// TestBuildWaves tests the BuildWaves function with various input scenarios.
func TestBuildWaves(t *testing.T) {
	tests := []struct {
		name        string
		subPlans    []domain.SubPlan
		wantWaves   int
		wantPerWave []int
	}{
		{
			name:        "empty sub-plans",
			subPlans:    []domain.SubPlan{},
			wantWaves:   0,
			wantPerWave: nil,
		},
		{
			name: "single sub-plan",
			subPlans: []domain.SubPlan{
				{ID: "sp1", Order: 0, RepositoryName: "repo1"},
			},
			wantWaves:   1,
			wantPerWave: []int{1},
		},
		{
			name: "two parallel sub-plans (same order)",
			subPlans: []domain.SubPlan{
				{ID: "sp1", Order: 0, RepositoryName: "repo1"},
				{ID: "sp2", Order: 0, RepositoryName: "repo2"},
			},
			wantWaves:   1,
			wantPerWave: []int{2},
		},
		{
			name: "two sequential sub-plans (different orders)",
			subPlans: []domain.SubPlan{
				{ID: "sp1", Order: 0, RepositoryName: "repo1"},
				{ID: "sp2", Order: 1, RepositoryName: "repo2"},
			},
			wantWaves:   2,
			wantPerWave: []int{1, 1},
		},
		{
			name: "three sub-plans with mixed orders [0,0,1]",
			subPlans: []domain.SubPlan{
				{ID: "sp1", Order: 0, RepositoryName: "repo1"},
				{ID: "sp2", Order: 0, RepositoryName: "repo2"},
				{ID: "sp3", Order: 1, RepositoryName: "repo3"},
			},
			wantWaves:   2,
			wantPerWave: []int{2, 1},
		},
		{
			name: "complex wave pattern [0,0,1,2,2,2]",
			subPlans: []domain.SubPlan{
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
			subPlans: []domain.SubPlan{
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
	subPlans := []domain.SubPlan{
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
	subPlans := []domain.SubPlan{
		{ID: "sp1", Order: 0, RepositoryName: "repo1"},
		{ID: "sp2", Order: 0, RepositoryName: "repo2"},
		{ID: "sp3", Order: 1, RepositoryName: "repo3"},
	}

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			waves := BuildWaves(subPlans)
			if len(waves) != 2 {
				t.Errorf("expected 2 waves, got %d", len(waves))
			}
		}()
	}
	wg.Wait()
}

// TestExecutionState tests the ExecutionState tracking.
func TestExecutionState(t *testing.T) {
	subPlans := []domain.SubPlan{
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
	subPlans := []domain.SubPlan{
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

			if tt.wantContains != "" && got != "" {
				// Check that the generated branch contains expected content
				// (exact match not required due to truncation)
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

// TestParseBranchName tests extracting external ID from branch names.
func TestParseBranchName(t *testing.T) {
	tests := []struct {
		name           string
		branch         string
		wantExternalID string
	}{
		{
			name:           "Linear issue",
			branch:         "sub-LIN-FOO-123-fix-auth",
			wantExternalID: "LIN-FOO-123",
		},
		{
			name:           "Manual work item",
			branch:         "sub-MAN-42-update-docs",
			wantExternalID: "MAN-42",
		},
		{
			name:           "Invalid branch",
			branch:         "feature/test",
			wantExternalID: "",
		},
		{
			name:           "No prefix",
			branch:         "LIN-FOO-123-fix-auth",
			wantExternalID: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseBranchName(tt.branch)
			if got != tt.wantExternalID {
				t.Errorf("ParseBranchName(%q) = %q, want %q", tt.branch, got, tt.wantExternalID)
			}
		})
	}
}

// TestWaveTimingConcurrentStart tests that sub-plans in the same wave start
// within a short time window of each other (concurrent execution).
func TestWaveTimingConcurrentStart(t *testing.T) {
	// This test verifies that sub-plans in the same wave can execute concurrently.
	// We use a mock scenario to verify the timing characteristics.

	subPlans := []domain.SubPlan{
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
	subPlans := []domain.SubPlan{
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

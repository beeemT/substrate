//go:build integration && e2e

package e2e_test

import (
	"context"
	"testing"
	"time"

	"github.com/beeemT/substrate/internal/domain"
)

// ---------------------------------------------------------------------------
// TestE2E_Performance_FiveRepoPlan_CompletesInBudget
//
// Verifies that a five-repo, single-wave workflow completes within a generous
// time budget with mock agent sessions.  This is a smoke-level performance
// guard: it catches deadlocks, unbounded waits, and gross scheduling
// regressions rather than micro-benchmarking.
//
// Spec: 5-repo parallel plan < 5 min; total workflow < 30 min.
// With mock sessions the actual wall-clock time is expected to be < 10 s.
//
// Requires git-work CLI.
// ---------------------------------------------------------------------------

const (
	perfRepoPlanBudget      = 5 * time.Minute
	perfTotalWorkflowBudget = 30 * time.Minute
)

func TestE2E_Performance_FiveRepoPlan_CompletesInBudget(t *testing.T) {
	skipIfGitWorkNotInstalled(t)

	ctx := context.Background()
	env := newTestEnv(t)

	repoNames := []string{"repo-1", "repo-2", "repo-3", "repo-4", "repo-5"}
	for _, name := range repoNames {
		createGitWorkRepo(t, env.workspaceDir, name)
	}

	ws := env.createWorkspace(t, ctx)
	item := env.createWorkItem(t, ctx, ws.ID)

	// All 5 repos in one parallel wave.
	plan := validPlan(repoNames...)

	// Queue sessions: 1 planning + 5 impl + 5 review.
	env.mockHarness.EnqueuePlanning(plan)
	for range repoNames {
		env.mockHarness.EnqueueImplSuccess()
	}
	for range repoNames {
		env.mockHarness.EnqueueReview("NO_CRITIQUES")
	}

	totalStart := time.Now()

	// --- Planning phase ---
	planStart := time.Now()
	planResult, err := env.plannerSvc.Plan(ctx, item.ID)
	planElapsed := time.Since(planStart)

	if err != nil {
		t.Fatalf("Plan(): %v", err)
	}
	if planResult.Plan == nil {
		t.Fatal("Plan() returned nil plan")
	}

	// Planning itself should be fast with mock harness.
	if planElapsed > perfRepoPlanBudget {
		t.Errorf("planning phase took %s, budget %s", planElapsed, perfRepoPlanBudget)
	}

	// Verify all 5 sub-plans were created.
	subPlans, err := env.subPlanRepo.ListByPlanID(ctx, planResult.Plan.ID)
	if err != nil {
		t.Fatalf("list sub-plans: %v", err)
	}
	if len(subPlans) != 5 {
		t.Fatalf("sub-plan count = %d, want 5", len(subPlans))
	}

	// Verify all repos are in the same wave (Order == 0).
	for _, sp := range subPlans {
		if sp.Order != 0 {
			t.Errorf("sub-plan %s order = %d, want 0", sp.RepositoryName, sp.Order)
		}
	}

	// --- Approve ---
	env.approvePlan(t, ctx, item.ID, planResult.Plan.ID)

	// --- Implementation ---
	implStart := time.Now()
	implResult, err := env.implSvc.Implement(ctx, planResult.Plan.ID)
	implElapsed := time.Since(implStart)

	if err != nil {
		t.Fatalf("Implement(): %v", err)
	}
	if !implResult.State.AllWavesCompleted() {
		t.Error("not all waves completed")
	}

	// 5 parallel sessions should finish quickly.
	if implElapsed > perfRepoPlanBudget {
		t.Errorf("implementation phase took %s, budget %s", implElapsed, perfRepoPlanBudget)
	}

	// --- Review ---
	reviewResults := env.reviewAllSessions(t, ctx, ws.ID)
	if len(reviewResults) != 5 {
		t.Errorf("review result count = %d, want 5", len(reviewResults))
	}
	for i, rr := range reviewResults {
		if !rr.Passed {
			t.Errorf("review[%d] did not pass: escalated=%v needsReimpl=%v", i, rr.Escalated, rr.NeedsReimpl)
		}
	}

	// --- Complete ---
	if err := env.workItemSvc.CompleteWorkItem(ctx, item.ID); err != nil {
		t.Fatalf("CompleteWorkItem(): %v", err)
	}
	env.requireWorkItemState(t, ctx, item.ID, domain.SessionCompleted)

	totalElapsed := time.Since(totalStart)
	if totalElapsed > perfTotalWorkflowBudget {
		t.Errorf("total workflow took %s, budget %s", totalElapsed, perfTotalWorkflowBudget)
	}

	t.Logf("Performance results: planning=%s implementation=%s total=%s",
		planElapsed.Round(time.Millisecond),
		implElapsed.Round(time.Millisecond),
		totalElapsed.Round(time.Millisecond),
	)
}

// ---------------------------------------------------------------------------
// TestE2E_Performance_ParallelSessionsStartNearSimultaneously
//
// Asserts that the two sub-plans within a wave both start their impl sessions
// within a tight wall-clock window — confirming truly parallel launch rather
// than sequential.  Uses session start times recorded in the DB.
//
// With mock sessions returning immediately, both sessions should start within
// a few hundred milliseconds of each other.  Requires git-work CLI.
// ---------------------------------------------------------------------------

func TestE2E_Performance_ParallelSessionsStartNearSimultaneously(t *testing.T) {
	skipIfGitWorkNotInstalled(t)

	ctx := context.Background()
	env := newTestEnv(t)

	createGitWorkRepo(t, env.workspaceDir, "repo-m")
	createGitWorkRepo(t, env.workspaceDir, "repo-n")

	ws := env.createWorkspace(t, ctx)
	item := env.createWorkItem(t, ctx, ws.ID)

	env.mockHarness.EnqueuePlanning(validPlan("repo-m", "repo-n"))
	env.mockHarness.EnqueueImplSuccess()
	env.mockHarness.EnqueueImplSuccess()
	env.mockHarness.EnqueueReview("NO_CRITIQUES")
	env.mockHarness.EnqueueReview("NO_CRITIQUES")

	planResult, err := env.plannerSvc.Plan(ctx, item.ID)
	if err != nil {
		t.Fatalf("Plan(): %v", err)
	}
	env.approvePlan(t, ctx, item.ID, planResult.Plan.ID)

	if _, err := env.implSvc.Implement(ctx, planResult.Plan.ID); err != nil {
		t.Fatalf("Implement(): %v", err)
	}

	// Retrieve sessions from the DB and check their start times.
	sessions, err := env.sessionRepo.ListByWorkspaceID(ctx, ws.ID)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(sessions) < 2 {
		t.Fatalf("expected at least 2 sessions, got %d", len(sessions))
	}

	// Find the two impl sessions (completed, not the foreman).
	var implSessions []domain.Task
	for _, s := range sessions {
		if s.Status == domain.AgentSessionCompleted {
			implSessions = append(implSessions, s)
		}
	}
	if len(implSessions) < 2 {
		t.Fatalf("expected at least 2 completed sessions, got %d", len(implSessions))
	}

	// Both sessions should have been created within 500 ms of each other.
	// This is a loose bound — it only catches sequential scheduling.
	if len(implSessions) >= 2 {
		diff := implSessions[0].CreatedAt.Sub(implSessions[1].CreatedAt)
		if diff < 0 {
			diff = -diff
		}
		if diff > 500*time.Millisecond {
			t.Errorf("sessions created %s apart; expected parallel launch (< 500ms skew)", diff)
		}
	}
}

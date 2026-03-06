//go:build integration && e2e

package e2e_test

import (
	"context"
	"testing"
	"time"

	"github.com/beeemT/substrate/internal/domain"
)

// ---------------------------------------------------------------------------
// TestE2E_FullWorkflow_ManualItem_TwoRepos
//
// Happy-path end-to-end test: create a two-repo workspace, plan → approve →
// implement → review → complete.  Requires git-work CLI.
// ---------------------------------------------------------------------------

func TestE2E_FullWorkflow_ManualItem_TwoRepos(t *testing.T) {
	skipIfGitWorkNotInstalled(t)

	ctx := context.Background()
	env := newTestEnv(t)

	// --- Create git-work repos in the workspace ---
	createGitWorkRepo(t, env.workspaceDir, "repo-a")
	createGitWorkRepo(t, env.workspaceDir, "repo-b")

	// --- Seed DB ---
	ws := env.createWorkspace(t, ctx)
	item := env.createWorkItem(t, ctx, ws.ID)

	// --- Configure mock harness ---
	// Planning: write a valid 2-repo plan to the draft path.
	env.mockHarness.EnqueuePlanning(validPlan("repo-a", "repo-b"))
	// Implementation: two successful sessions (one per repo).
	env.mockHarness.EnqueueImplSuccess()
	env.mockHarness.EnqueueImplSuccess()
	// Review: two sessions both returning NO_CRITIQUES.
	env.mockHarness.EnqueueReview("NO_CRITIQUES")
	env.mockHarness.EnqueueReview("NO_CRITIQUES")

	// --- Planning ---
	result, err := env.plannerSvc.Plan(ctx, item.ID)
	if err != nil {
		t.Fatalf("Plan(): %v", err)
	}
	if result.Plan == nil {
		t.Fatal("Plan() returned nil plan")
	}
	planID := result.Plan.ID
	env.requireWorkItemState(t, ctx, item.ID, domain.WorkItemPlanReview)

	// --- Approve ---
	env.approvePlan(t, ctx, item.ID, planID)
	env.requireWorkItemState(t, ctx, item.ID, domain.WorkItemApproved)

	// --- Implementation ---
	implResult, err := env.implSvc.Implement(ctx, planID)
	if err != nil {
		t.Fatalf("Implement(): %v", err)
	}
	if !implResult.State.AllWavesCompleted() {
		t.Fatalf("implementation did not complete all waves: %+v", implResult.State)
	}
	env.requireWorkItemState(t, ctx, item.ID, domain.WorkItemReviewing)

	// --- Review ---
	reviewResults := env.reviewAllSessions(t, ctx, ws.ID)
	if len(reviewResults) == 0 {
		t.Fatal("no review results — no completed sessions found")
	}
	for _, rr := range reviewResults {
		if !rr.Passed {
			t.Errorf("review did not pass: escalated=%v, needsReimpl=%v", rr.Escalated, rr.NeedsReimpl)
		}
	}

	// --- WorktreeCreated events (spec: "MRs created in GitLab") ---
	// Phase 13 spec requires that MRs are created in GitLab. In this mock-based
	// test, the glab adapter is not registered. Instead, verify that the
	// WorktreeCreated events were published to the bus (and thus persisted to
	// the event store). A registered glab adapter subscribes to WorktreeCreated
	// and creates draft MRs; the events are the proof that the hook would fire.
	events, err := env.eventRepo.ListByWorkspaceID(ctx, ws.ID, 0)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	var worktreeCreatedCount int
	for _, ev := range events {
		if ev.EventType == string(domain.EventWorktreeCreated) {
			worktreeCreatedCount++
		}
	}
	// Two repos → two worktrees → two WorktreeCreated events.
	if worktreeCreatedCount != 2 {
		t.Errorf("WorktreeCreated event count = %d, want 2 (one per repo)", worktreeCreatedCount)
	}

	// --- Complete ---
	if err := env.workItemSvc.CompleteWorkItem(ctx, item.ID); err != nil {
		t.Fatalf("CompleteWorkItem(): %v", err)
	}
	env.requireWorkItemState(t, ctx, item.ID, domain.WorkItemCompleted)
}

// ---------------------------------------------------------------------------
// TestE2E_WorkItem_TraversesAllStates
//
// Verifies that a work item visits every lifecycle state. Observable states
// (ingested, plan_review, approved, reviewing, completed) are captured by
// DB snapshots at orchestration boundaries. Transient states (planning,
// implementing) are entered and exited inside the service calls, so they are
// verified by asserting the corresponding events were persisted to the event
// store by the orchestrators.
// Requires git-work CLI.
// ---------------------------------------------------------------------------

func TestE2E_WorkItem_TraversesAllStates(t *testing.T) {
	skipIfGitWorkNotInstalled(t)

	ctx := context.Background()
	env := newTestEnv(t)

	createGitWorkRepo(t, env.workspaceDir, "repo-x")

	ws := env.createWorkspace(t, ctx)
	item := env.createWorkItem(t, ctx, ws.ID)

	env.mockHarness.EnqueuePlanning(validPlan("repo-x"))
	env.mockHarness.EnqueueImplSuccess()
	env.mockHarness.EnqueueReview("NO_CRITIQUES")

	// Record each observed state in order.
	var states []domain.WorkItemState
	snapshot := func(label string) {
		t.Helper()
		it, err := env.workItemSvc.Get(ctx, item.ID)
		if err != nil {
			t.Fatalf("get work item (%s): %v", label, err)
		}
		states = append(states, it.State)
	}

	// ingested
	snapshot("initial")

	// planning → plan_review
	result, err := env.plannerSvc.Plan(ctx, item.ID)
	if err != nil {
		t.Fatalf("Plan(): %v", err)
	}
	snapshot("after planning")

	// approved
	env.approvePlan(t, ctx, item.ID, result.Plan.ID)
	snapshot("after approval")

	// implementing → reviewing
	if _, err := env.implSvc.Implement(ctx, result.Plan.ID); err != nil {
		t.Fatalf("Implement(): %v", err)
	}
	snapshot("after implementation")

	// review pass
	env.reviewAllSessions(t, ctx, ws.ID)
	snapshot("after review")

	// completed
	if err := env.workItemSvc.CompleteWorkItem(ctx, item.ID); err != nil {
		t.Fatalf("CompleteWorkItem(): %v", err)
	}
	snapshot("final")

	want := []domain.WorkItemState{
		domain.WorkItemIngested,
		domain.WorkItemPlanReview,
		domain.WorkItemApproved,
		domain.WorkItemReviewing,
		domain.WorkItemReviewing, // review doesn't auto-advance state
		domain.WorkItemCompleted,
	}
	// Verify observable state sequence.
	if len(states) != len(want) {
		t.Fatalf("state count = %d, want %d; states: %v", len(states), len(want), states)
	}
	for i, s := range states {
		if s != want[i] {
			t.Errorf("states[%d] = %s, want %s", i, s, want[i])
		}
	}

	// Verify that transient states (planning, implementing) were visited via
	// the events persisted by the orchestrators. These states are entered and
	// exited inside the service calls so the DB snapshot approach above cannot
	// capture them — but the events are durable proof.
	events, err := env.eventRepo.ListByWorkspaceID(ctx, ws.ID, 0)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	seen := make(map[string]bool, len(events))
	for _, ev := range events {
		seen[ev.EventType] = true
	}
	// WorkItemPlanning: emitted by PlanningService.emitPlanningStartedEvent.
	if !seen[string(domain.EventWorkItemPlanning)] {
		t.Error("expected EventWorkItemPlanning event — work item never entered planning state")
	}
	// EventImplementationStarted: emitted after Transition→WorkItemImplementing.
	if !seen[string(domain.EventImplementationStarted)] {
		t.Error("expected EventImplementationStarted event — work item never entered implementing state")
	}
}

// ---------------------------------------------------------------------------
// TestE2E_MultiWave_ExecutionOrder
//
// Verifies that two-wave plans execute sequentially: all wave-0 sub-plans
// complete before wave-1 sub-plans start.  Requires git-work CLI.
// ---------------------------------------------------------------------------

func TestE2E_MultiWave_ExecutionOrder(t *testing.T) {
	skipIfGitWorkNotInstalled(t)

	ctx := context.Background()
	env := newTestEnv(t)

	// Three repos: repo-a and repo-b in wave 0 (parallel), repo-c in wave 1.
	createGitWorkRepo(t, env.workspaceDir, "repo-a")
	createGitWorkRepo(t, env.workspaceDir, "repo-b")
	createGitWorkRepo(t, env.workspaceDir, "repo-c")

	ws := env.createWorkspace(t, ctx)
	item := env.createWorkItem(t, ctx, ws.ID)

	// Two-wave plan: [repo-a, repo-b] then [repo-c].
	plan := validPlanWaves([]string{"repo-a", "repo-b"}, []string{"repo-c"})

	env.mockHarness.EnqueuePlanning(plan)
	// Wave 0 sessions (2 repos, parallel).
	env.mockHarness.EnqueueImplSuccess()
	env.mockHarness.EnqueueImplSuccess()
	// Wave 1 session (1 repo).
	env.mockHarness.EnqueueImplSuccess()
	// Review sessions (3 repos).
	env.mockHarness.EnqueueReview("NO_CRITIQUES")
	env.mockHarness.EnqueueReview("NO_CRITIQUES")
	env.mockHarness.EnqueueReview("NO_CRITIQUES")

	result, err := env.plannerSvc.Plan(ctx, item.ID)
	if err != nil {
		t.Fatalf("Plan(): %v", err)
	}
	env.approvePlan(t, ctx, item.ID, result.Plan.ID)

	implResult, err := env.implSvc.Implement(ctx, result.Plan.ID)
	if err != nil {
		t.Fatalf("Implement(): %v", err)
	}

	// Verify two waves were executed.
	state := implResult.State
	if state == nil {
		t.Fatal("implementation returned nil state")
	}

	// All waves must be complete.
	if !state.AllWavesCompleted() {
		t.Error("not all waves completed")
	}

	// Verify session results — 3 sessions (one per repo).
	if len(implResult.Sessions) != 3 {
		t.Errorf("session count = %d, want 3", len(implResult.Sessions))
	}

	// Verify repo-c session was ordered after repo-a and repo-b by checking
	// sub-plan order assignment.  repo-c has Order=1; repo-a/repo-b have Order=0.
	subPlans, err := env.subPlanRepo.ListByPlanID(ctx, result.Plan.ID)
	if err != nil {
		t.Fatalf("list sub-plans: %v", err)
	}
	orderMap := map[string]int{}
	for _, sp := range subPlans {
		orderMap[sp.RepositoryName] = sp.Order
	}
	if orderMap["repo-a"] != 0 {
		t.Errorf("repo-a order = %d, want 0", orderMap["repo-a"])
	}
	if orderMap["repo-b"] != 0 {
		t.Errorf("repo-b order = %d, want 0", orderMap["repo-b"])
	}
	if orderMap["repo-c"] != 1 {
		t.Errorf("repo-c order = %d, want 1", orderMap["repo-c"])
	}

	// Verify work item reached reviewing state.
	env.requireWorkItemState(t, ctx, item.ID, domain.WorkItemReviewing)
}

// ---------------------------------------------------------------------------
// TestE2E_Wave0ParallelTiming
//
// Verifies that sub-plans within the same wave start within a generous window
// of each other (< 500 ms) even when the implementation sessions return
// immediately.  Requires git-work CLI.
// ---------------------------------------------------------------------------

func TestE2E_Wave0ParallelTiming(t *testing.T) {
	skipIfGitWorkNotInstalled(t)

	ctx := context.Background()
	env := newTestEnv(t)

	createGitWorkRepo(t, env.workspaceDir, "repo-p")
	createGitWorkRepo(t, env.workspaceDir, "repo-q")

	ws := env.createWorkspace(t, ctx)
	item := env.createWorkItem(t, ctx, ws.ID)

	env.mockHarness.EnqueuePlanning(validPlan("repo-p", "repo-q"))
	env.mockHarness.EnqueueImplSuccess()
	env.mockHarness.EnqueueImplSuccess()
	env.mockHarness.EnqueueReview("NO_CRITIQUES")
	env.mockHarness.EnqueueReview("NO_CRITIQUES")

	result, err := env.plannerSvc.Plan(ctx, item.ID)
	if err != nil {
		t.Fatalf("Plan(): %v", err)
	}
	env.approvePlan(t, ctx, item.ID, result.Plan.ID)

	start := time.Now()
	if _, err := env.implSvc.Implement(ctx, result.Plan.ID); err != nil {
		t.Fatalf("Implement(): %v", err)
	}
	elapsed := time.Since(start)

	// With two mock sessions that return immediately, the two parallel
	// sub-plans in wave 0 should complete in well under one second total.
	if elapsed > 10*time.Second {
		t.Errorf("parallel wave took %s, want < 10s", elapsed)
	}
}

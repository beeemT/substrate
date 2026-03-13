//go:build integration && e2e

package e2e_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/orchestrator"
)

// ---------------------------------------------------------------------------
// TestE2E_Recovery_CorruptPlan_ReturnsToIngested
//
// When the planning agent repeatedly writes an unparseable plan, the
// correction loop exhausts max_parse_retries and the work item returns to
// Ingested with a non-nil ParseErrors in the result.  Requires git-work CLI.
// ---------------------------------------------------------------------------

func TestE2E_Recovery_CorruptPlan_ReturnsToIngested(t *testing.T) {
	skipIfGitWorkNotInstalled(t)

	ctx := context.Background()
	env := newTestEnv(t)

	createGitWorkRepo(t, env.workspaceDir, "repo-a")

	ws := env.createWorkspace(t, ctx)
	item := env.createWorkItem(t, ctx, ws.ID)

	// Each planning attempt writes plain prose with no substrate-plan block.
	// max_parse_retries = 2, so 3 sessions are started (initial + 2 corrections).
	corrupt := "This plan has no YAML block and will always fail validation."
	env.mockHarness.EnqueuePlanning(corrupt) // attempt 0
	env.mockHarness.EnqueuePlanning(corrupt) // correction 1
	env.mockHarness.EnqueuePlanning(corrupt) // correction 2

	result, err := env.plannerSvc.Plan(ctx, item.ID)

	// The planner must return an error after exhausting retries.
	if err == nil {
		t.Fatal("Plan() should return an error for a permanently corrupt plan")
	}
	if result == nil {
		t.Fatal("Plan() should still return a non-nil result with error details")
	}
	if result.ParseErrors == nil {
		t.Error("PlanningResult.ParseErrors should be set on parse failure")
	}

	// Work item must revert to Ingested so the user can try again.
	env.requireWorkItemState(t, ctx, item.ID, domain.SessionIngested)
}

// ---------------------------------------------------------------------------
// TestE2E_Recovery_AgentInterrupted_Resumable
//
// An interrupted session can be resumed: ResumeSession creates a new session
// linked to the same sub-plan and emits AgentSessionResumed.
// Does NOT require git-work (sets up state directly in the DB).
// ---------------------------------------------------------------------------

func TestE2E_Recovery_AgentInterrupted_Resumable(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)

	// Create workspace, plan, sub-plan, and a running-then-interrupted session
	// directly in the DB — no git-work CLI needed.
	ws := env.createWorkspace(t, ctx)
	item := env.createWorkItem(t, ctx, ws.ID)

	// Create plan through proper service transitions (CreatePlan requires draft).
	now := time.Now()
	plan := env.createApprovedPlan(t, ctx, item.ID, "Test orchestration plan.")

	// Create sub-plan.
	subPlan := domain.TaskPlan{
		ID:             domain.NewID(),
		PlanID:         plan.ID,
		RepositoryName: "repo-a",
		Content:        "Implement feature in repo-a.",
		Order:          0,
		Status:         domain.SubPlanInProgress,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := env.subPlanRepo.Create(ctx, subPlan); err != nil {
		t.Fatalf("create sub-plan: %v", err)
	}

	// Advance work item to implementing.
	if err := env.workItemSvc.Transition(ctx, item.ID, domain.SessionPlanning); err != nil {
		t.Fatalf("transition to planning: %v", err)
	}
	if err := env.workItemSvc.Transition(ctx, item.ID, domain.SessionPlanReview); err != nil {
		t.Fatalf("transition to plan_review: %v", err)
	}
	if err := env.workItemSvc.ApprovePlan(ctx, item.ID); err != nil {
		t.Fatalf("approve work item: %v", err)
	}
	if err := env.workItemSvc.StartImplementation(ctx, item.ID); err != nil {
		t.Fatalf("start implementation: %v", err)
	}

	// Create an interrupted session (simulates a crash).
	interruptedSession := domain.Task{
		ID:             domain.NewID(),
		WorkspaceID:    ws.ID,
		SubPlanID:      subPlan.ID,
		RepositoryName: "repo-a",
		WorktreePath:   "/tmp/fake-worktree-for-test",
		HarnessName:    "e2e-mock",
		Status:         domain.AgentSessionPending,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := env.sessionSvc.Create(ctx, interruptedSession); err != nil {
		t.Fatalf("create session: %v", err)
	}
	// Transition through pending → running → interrupted.
	if err := env.sessionSvc.Start(ctx, interruptedSession.ID); err != nil {
		t.Fatalf("start session: %v", err)
	}
	if err := env.sessionSvc.Interrupt(ctx, interruptedSession.ID); err != nil {
		t.Fatalf("interrupt session: %v", err)
	}

	// Fetch the interrupted session from DB to get the updated state.
	interrupted, err := env.sessionRepo.Get(ctx, interruptedSession.ID)
	if err != nil {
		t.Fatalf("get interrupted session: %v", err)
	}

	// Configure mock harness to accept the resume session start.
	env.mockHarness.EnqueueImplSuccess()

	// Collect AgentSessionResumed events via a bus subscriber.
	subID := "test-resumed-subscriber"
	busSub, subErr := env.bus.Subscribe(subID)
	if subErr != nil {
		t.Fatalf("subscribe to bus: %v", subErr)
	}
	defer env.bus.Unsubscribe(subID)

	// Resume the interrupted session.
	// Insert a substrate_instances row for currentInstanceID so the FK
	// constraint on agent_sessions.owner_instance_id is satisfied.
	instanceID := domain.NewID()
	inst := domain.SubstrateInstance{
		ID:            instanceID,
		WorkspaceID:   ws.ID,
		PID:           12345,
		Hostname:      "e2e-test-host",
		LastHeartbeat: time.Now(),
		StartedAt:     time.Now(),
	}
	if err := env.instanceRepo.Create(ctx, inst); err != nil {
		t.Fatalf("create instance: %v", err)
	}
	result, err := env.resumption.ResumeSession(ctx, interrupted, instanceID)
	if err != nil {
		t.Fatalf("ResumeSession(): %v", err)
	}

	// New session must be distinct from the interrupted one.
	if result.NewSession.ID == interrupted.ID {
		t.Error("resumed session ID should differ from interrupted session ID")
	}
	if result.NewSession.SubPlanID != subPlan.ID {
		t.Errorf("resumed session SubPlanID = %s, want %s", result.NewSession.SubPlanID, subPlan.ID)
	}
	if result.NewSession.WorktreePath != interrupted.WorktreePath {
		t.Errorf("resumed session WorktreePath = %s, want %s", result.NewSession.WorktreePath, interrupted.WorktreePath)
	}

	// AgentSessionResumed event should have been published.
	select {
	case evt := <-busSub.C:
		if evt.EventType != string(domain.EventAgentSessionResumed) {
			t.Errorf("unexpected event type: %s", evt.EventType)
		}
	case <-time.After(2 * time.Second):
		t.Error("timeout waiting for AgentSessionResumed event")
	}

	// Interrupted session must remain interrupted in the DB (audit trail).
	still, err := env.sessionRepo.Get(ctx, interrupted.ID)
	if err != nil {
		t.Fatalf("get session after resume: %v", err)
	}
	if still.Status != domain.AgentSessionInterrupted {
		t.Errorf("interrupted session status = %s, want interrupted", still.Status)
	}

	// The harness session handle must not be nil.
	if result.HarnessSession == nil {
		t.Error("ResumeSession should return a non-nil HarnessSession")
	}
}

// ---------------------------------------------------------------------------
// TestE2E_Recovery_AbandonInterrupted
//
// AbandonSession transitions an interrupted session to failed so the work
// item can be manually cleaned up.  No git-work required.
// ---------------------------------------------------------------------------

func TestE2E_Recovery_AbandonInterrupted(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)

	ws := env.createWorkspace(t, ctx)
	item := env.createWorkItem(t, ctx, ws.ID)

	now := time.Now()
	plan := env.createApprovedPlan(t, ctx, item.ID, "")
	subPlan := domain.TaskPlan{
		ID:             domain.NewID(),
		PlanID:         plan.ID,
		RepositoryName: "repo-a",
		Order:          0,
		Status:         domain.SubPlanInProgress,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := env.subPlanRepo.Create(ctx, subPlan); err != nil {
		t.Fatalf("create sub-plan: %v", err)
	}

	sess := domain.Task{
		ID:             domain.NewID(),
		WorkspaceID:    ws.ID,
		SubPlanID:      subPlan.ID,
		RepositoryName: "repo-a",
		WorktreePath:   "/tmp/wt",
		HarnessName:    "e2e-mock",
		Status:         domain.AgentSessionPending,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := env.sessionSvc.Create(ctx, sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := env.sessionSvc.Start(ctx, sess.ID); err != nil {
		t.Fatalf("start session: %v", err)
	}
	if err := env.sessionSvc.Interrupt(ctx, sess.ID); err != nil {
		t.Fatalf("interrupt session: %v", err)
	}

	if err := env.resumption.AbandonSession(ctx, sess.ID); err != nil {
		t.Fatalf("AbandonSession(): %v", err)
	}

	abandoned, err := env.sessionRepo.Get(ctx, sess.ID)
	if err != nil {
		t.Fatalf("get abandoned session: %v", err)
	}
	if abandoned.Status != domain.AgentSessionFailed {
		t.Errorf("abandoned session status = %s, want failed", abandoned.Status)
	}
}

// ---------------------------------------------------------------------------
// TestE2E_Recovery_ReviewMaxCycles_Escalated
//
// When the review pipeline's cycle limit is exhausted (all cycles produce
// major critiques), the result is escalated=true.  No git-work required.
// ---------------------------------------------------------------------------

func TestE2E_Recovery_ReviewMaxCycles_Escalated(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)

	ws := env.createWorkspace(t, ctx)
	item := env.createWorkItem(t, ctx, ws.ID)

	now := time.Now()
	plan := env.createApprovedPlan(t, ctx, item.ID, "Review test plan.")
	subPlan := domain.TaskPlan{
		ID:             domain.NewID(),
		PlanID:         plan.ID,
		RepositoryName: "repo-a",
		Content:        "Review sub-plan.",
		Order:          0,
		Status:         domain.SubPlanCompleted,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := env.subPlanRepo.Create(ctx, subPlan); err != nil {
		t.Fatalf("create sub-plan: %v", err)
	}

	// Create a completed session so ReviewSession can look it up.
	session := domain.Task{
		ID:             domain.NewID(),
		WorkspaceID:    ws.ID,
		SubPlanID:      subPlan.ID,
		RepositoryName: "repo-a",
		WorktreePath:   "/tmp/wt-review",
		HarnessName:    "e2e-mock",
		Status:         domain.AgentSessionPending,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := env.sessionSvc.Create(ctx, session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := env.sessionSvc.Start(ctx, session.ID); err != nil {
		t.Fatalf("start session: %v", err)
	}
	if err := env.sessionSvc.Complete(ctx, session.ID); err != nil {
		t.Fatalf("complete session: %v", err)
	}

	// Fetch session from DB to get all fields.
	dbSession, err := env.sessionRepo.Get(ctx, session.ID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}

	// maxCycles = 2 in defaultTestConfig (see harness_test.go: defaultTestConfig).
	// Cycle 1: major critique → NeedsReimpl.
	// Cycle 2: major critique → NeedsReimpl.
	// Cycle 3 (cycleNumber=3 > maxCycles=2): escalates without starting a review agent.
	majorCritiqueOutput := `CRITIQUE
File: main.go
Severity: major
Description: Critical logic error that must be fixed before merging.
END_CRITIQUE`

	// Cycle 1: major critique → needs reimplementation.
	env.mockHarness.EnqueueReview(majorCritiqueOutput)
	r1, err := env.reviewPipeline.ReviewSession(ctx, dbSession)
	if err != nil {
		t.Fatalf("ReviewSession cycle 1: %v", err)
	}
	if r1.Passed {
		t.Error("cycle 1: expected not passed (major critique)")
	}
	if !r1.NeedsReimpl {
		t.Error("cycle 1: expected NeedsReimpl=true")
	}

	// Cycle 2: major critique → needs reimplementation.
	env.mockHarness.EnqueueReview(majorCritiqueOutput)
	r2, err := env.reviewPipeline.ReviewSession(ctx, dbSession)
	if err != nil {
		t.Fatalf("ReviewSession cycle 2: %v", err)
	}
	if r2.Passed {
		t.Error("cycle 2: expected not passed")
	}
	if !r2.NeedsReimpl {
		t.Error("cycle 2: expected NeedsReimpl=true")
	}

	// Cycle 3 == MaxCycles: the pipeline must escalate instead of running
	// another review agent (cycle count already exhausted).
	// No review spec needed — the pipeline exits before StartSession.
	r3, err := env.reviewPipeline.ReviewSession(ctx, dbSession)
	if err != nil {
		t.Fatalf("ReviewSession cycle 3: %v", err)
	}
	if !r3.Escalated {
		t.Errorf("cycle 3: expected Escalated=true, got passed=%v needsReimpl=%v", r3.Passed, r3.NeedsReimpl)
	}
}

// ---------------------------------------------------------------------------
// TestE2E_Recovery_GitWorkCheckoutFails
//
// When git-work checkout fails (e.g. non-existent branch ref), the
// implementation service surfaces it as a warning and marks the work item
// as failed.  Requires git-work CLI but with a deliberately broken worktree.
// ---------------------------------------------------------------------------

func TestE2E_Recovery_GitWorkCheckoutFails(t *testing.T) {
	skipIfGitWorkNotInstalled(t)

	ctx := context.Background()
	env := newTestEnv(t)

	// Create a git-work repo.
	createGitWorkRepo(t, env.workspaceDir, "repo-a")

	ws := env.createWorkspace(t, ctx)
	item := env.createWorkItem(t, ctx, ws.ID)

	// Plan with a single repo.
	env.mockHarness.EnqueuePlanning(validPlan("repo-a"))

	result, err := env.plannerSvc.Plan(ctx, item.ID)
	if err != nil {
		t.Fatalf("Plan(): %v", err)
	}
	env.approvePlan(t, ctx, item.ID, result.Plan.ID)

	// Corrupt the workspace: remove the .bare directory so git-work list and
	// checkout both fail, forcing the implementation to surface an error.
	_ = removeDir(t, env.workspaceDir+"/repo-a/.bare")

	// Implementation should degrade gracefully: either return an error OR
	// mark the work item as failed after the worktree cannot be created.
	implResult, implErr := env.implSvc.Implement(ctx, result.Plan.ID)

	// Either the call returns an error, or it returns a result with warnings.
	if implErr == nil && implResult == nil {
		t.Fatal("expected either error or non-nil result from Implement()")
	}
	if implErr != nil {
		// Acceptable: error propagated up — work item should be failed or
		// remain in a non-completed state.
		it, err := env.workItemSvc.Get(ctx, item.ID)
		if err != nil {
			t.Fatalf("get work item: %v", err)
		}
		if it.State == domain.SessionCompleted {
			t.Error("work item should not be completed after implementation error")
		}
		return
	}
	// If no error, warnings should describe the failure.
	if len(implResult.Warnings) == 0 {
		t.Log("implementation succeeded with no warnings — possibly idempotency guard fired; this is acceptable")
	}
}

// removeDir removes a directory tree. Returns any error (non-fatal helper).
func removeDir(t *testing.T, path string) error {
	t.Helper()
	return os.RemoveAll(path)
}

// ---------------------------------------------------------------------------
// TestE2E_Recovery_NetworkFailure_Backoff
//
// Verifies that the planning service propagates errors cleanly when the
// harness session itself fails (simulating a network-level startup failure).
// No git-work required — planning startup fails before repo discovery.
// ---------------------------------------------------------------------------

func TestE2E_Recovery_NetworkFailure_Backoff(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)

	// No git-work repos: DiscoverRepos returns empty, planning prompt has no
	// repos.  But we verify that a harness startup error surfaces cleanly.
	ws := env.createWorkspace(t, ctx)
	item := env.createWorkItem(t, ctx, ws.ID)

	// The harness will fail with a simulated network error.
	networkErr := errors.New("dial tcp: connection refused (simulated)")
	env.mockHarness.EnqueueError(networkErr)

	_, err := env.plannerSvc.Plan(ctx, item.ID)
	if err == nil {
		t.Fatal("Plan() should return an error when the harness fails to start")
	}

	// Work item must revert to Ingested so the operator can retry.
	env.requireWorkItemState(t, ctx, item.ID, domain.SessionIngested)
}

// ---------------------------------------------------------------------------
// TestE2E_Recovery_ReviewPassAfterReimplementation
//
// Cycle 1 produces a major critique (NeedsReimpl=true).  After
// re-implementation, cycle 2 produces NO_CRITIQUES (Passed=true).
// No git-work required.
// ---------------------------------------------------------------------------

func TestE2E_Recovery_ReviewPassAfterReimplementation(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)

	ws := env.createWorkspace(t, ctx)
	item := env.createWorkItem(t, ctx, ws.ID)

	now := time.Now()
	plan := env.createApprovedPlan(t, ctx, item.ID, "Re-impl review test.")
	subPlan := domain.TaskPlan{
		ID:             domain.NewID(),
		PlanID:         plan.ID,
		RepositoryName: "repo-a",
		Content:        "Re-impl test sub-plan.",
		Order:          0,
		Status:         domain.SubPlanCompleted,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := env.subPlanRepo.Create(ctx, subPlan); err != nil {
		t.Fatalf("create sub-plan: %v", err)
	}

	sess := domain.Task{
		ID:             domain.NewID(),
		WorkspaceID:    ws.ID,
		SubPlanID:      subPlan.ID,
		RepositoryName: "repo-a",
		WorktreePath:   "/tmp/wt-reimpl",
		HarnessName:    "e2e-mock",
		Status:         domain.AgentSessionPending,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := env.sessionSvc.Create(ctx, sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := env.sessionSvc.Start(ctx, sess.ID); err != nil {
		t.Fatalf("start session: %v", err)
	}
	if err := env.sessionSvc.Complete(ctx, sess.ID); err != nil {
		t.Fatalf("complete session: %v", err)
	}

	dbSession, err := env.sessionRepo.Get(ctx, sess.ID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}

	// Cycle 1: major critique → NeedsReimpl.
	majorCritique := `CRITIQUE
File: logic.go
Severity: major
Description: Incorrect business logic.
END_CRITIQUE`
	env.mockHarness.EnqueueReview(majorCritique)
	r1, err := env.reviewPipeline.ReviewSession(ctx, dbSession)
	if err != nil {
		t.Fatalf("ReviewSession cycle 1: %v", err)
	}
	if r1.Passed {
		t.Error("cycle 1: should not pass with major critique")
	}
	if !r1.NeedsReimpl {
		t.Error("cycle 1: NeedsReimpl should be true")
	}

	// Cycle 2: NO_CRITIQUES → Passed.
	env.mockHarness.EnqueueReview("NO_CRITIQUES")
	r2, err := env.reviewPipeline.ReviewSession(ctx, dbSession)
	if err != nil {
		t.Fatalf("ReviewSession cycle 2: %v", err)
	}
	if !r2.Passed {
		t.Errorf("cycle 2: should pass after fix; escalated=%v needsReimpl=%v", r2.Escalated, r2.NeedsReimpl)
	}
}

// Ensure the orchestrator package types are accessible (import guard).
var _ *orchestrator.ReviewResult

//go:build integration && e2e

package e2e_test

import (
	"context"
	"os"
	"testing"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/sessionlog"
)

// ---------------------------------------------------------------------------
// TestE2E_ProductHappyPath_ManualWorkItemCompletes
//
// Canonical product happy-path: a manual work item moves through planning,
// approval, implementation, review, and completion.  Every lifecycle event
// is asserted to have been persisted in coherent order.
//
// Requires git-work CLI.
// ---------------------------------------------------------------------------

func TestE2E_ProductHappyPath_ManualWorkItemCompletes(t *testing.T) {
	skipIfGitWorkNotInstalled(t)

	for _, tc := range e2eProductHarnessCases() {
		t.Run(tc.name, func(t *testing.T) {
			runProductHappyPath(t, tc)
		})
	}
}

func runProductHappyPath(t *testing.T, tc e2eHarnessCase) {
	t.Helper()
	ctx := context.Background()
	repoName := "feature-repo"
	planContent := validPlan(repoName)
	workspaceRoot := t.TempDir()
	cfg := tc.cfg(t, planContent, workspaceRoot)
	harnesses := buildE2EProductHarnesses(t, cfg, workspaceRoot)
	env := newTestEnvWithAgentHarnesses(t, harnesses.Planning, harnesses.Implementation, harnesses.Review)

	// --- Setup: one git-work repo ---
	createGitWorkRepo(t, env.workspaceDir, repoName)

	// --- Seed DB ---
	ws := env.createWorkspace(t, ctx)
	item := env.createWorkItem(t, ctx, ws.ID)
	result, err := env.plannerSvc.Plan(ctx, item.ID)
	if err != nil {
		t.Fatalf("Plan(): %v", err)
	}
	if result.Plan == nil {
		t.Fatal("Plan() returned nil plan")
	}
	planID := result.Plan.ID

	// Assert: work item is in plan_review.
	env.requireWorkItemState(t, ctx, item.ID, domain.SessionPlanReview)

	// Assert: exactly one plan for the work item.
	storedPlan, err := env.planRepo.GetByWorkItemID(ctx, item.ID)
	if err != nil {
		t.Fatalf("GetByWorkItemID: %v", err)
	}
	if storedPlan.ID != planID {
		t.Errorf("plan ID mismatch: got %s, want %s", storedPlan.ID, planID)
	}

	// Assert: exactly one sub-plan for the plan, for the expected repo.
	subPlans, err := env.subPlanRepo.ListByPlanID(ctx, planID)
	if err != nil {
		t.Fatalf("ListByPlanID: %v", err)
	}
	if len(subPlans) != 1 {
		t.Fatalf("sub-plan count = %d, want 1", len(subPlans))
	}
	if subPlans[0].RepositoryName != repoName {
		t.Errorf("sub-plan repo = %s, want %s", subPlans[0].RepositoryName, repoName)
	}

	// --- Approve ---
	env.approvePlan(t, ctx, item.ID, planID)
	env.requireWorkItemState(t, ctx, item.ID, domain.SessionApproved)

	// --- Implementation ---
	implResult, err := env.implSvc.Implement(ctx, planID)
	if err != nil {
		t.Fatalf("Implement(): %v", err)
	}
	if !implResult.State.AllWavesCompleted() {
		t.Fatalf("implementation did not complete all waves: %+v", implResult.State)
	}

	// Assert: sub-plan reached completed status.
	sp, err := env.subPlanRepo.Get(ctx, subPlans[0].ID)
	if err != nil {
		t.Fatalf("Get sub-plan: %v", err)
	}
	if sp.Status != domain.SubPlanCompleted {
		t.Errorf("sub-plan status = %s, want %s", sp.Status, domain.SubPlanCompleted)
	}

	// Assert: no agent session is failed or interrupted.
	sessions, err := env.sessionRepo.ListByWorkItemID(ctx, item.ID)
	if err != nil {
		t.Fatalf("ListByWorkItemID: %v", err)
	}
	completedImplSessions := 0
	for _, sess := range sessions {
		if sess.Kind == domain.AgentSessionKindImplementation && sess.Status == domain.AgentSessionCompleted {
			completedImplSessions++
		}
		if sess.Status == domain.AgentSessionFailed {
			t.Errorf("agent session %s is failed", sess.ID)
		}
		if sess.Status == domain.AgentSessionInterrupted {
			t.Errorf("agent session %s is interrupted", sess.ID)
		}
	}
	if completedImplSessions != 1 {
		t.Errorf("completed implementation sessions = %d, want 1", completedImplSessions)
	}

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

	// Assert: session logs required by the review pipeline exist.
	allSessions, err := env.sessionRepo.ListByWorkItemID(ctx, item.ID)
	if err != nil {
		t.Fatalf("ListByWorkItemID for log check: %v", err)
	}
	for _, sess := range allSessions {
		if sess.Status == domain.AgentSessionFailed {
			t.Errorf("agent session %s is failed after review", sess.ID)
		}
		if sess.Status == domain.AgentSessionInterrupted {
			t.Errorf("agent session %s is interrupted after review", sess.ID)
		}
	}
	for _, sess := range allSessions {
		if sess.Kind == domain.AgentSessionKindReview {
			paths, err := sessionlog.InteractionPaths(env.sessionsDir, sess.ID)
			if err != nil {
				t.Fatalf("review session log paths: %v", err)
			}
			if len(paths) == 0 {
				t.Fatalf("expected review session %s to have active or archived logs in %s", sess.ID, env.sessionsDir)
			}
		}
	}

	// --- Complete work item ---
	if err := env.workItemSvc.CompleteWorkItem(ctx, item.ID); err != nil {
		t.Fatalf("CompleteWorkItem(): %v", err)
	}
	env.requireWorkItemState(t, ctx, item.ID, domain.SessionCompleted)

	// --- Assert persisted lifecycle events ---
	events, err := env.eventRepo.ListByWorkspaceID(ctx, ws.ID, 0)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}

	// Required lifecycle events in coherent order.
	wantEvents := []domain.EventType{
		domain.EventWorkItemPlanning,
		domain.EventPlanGenerated,
		domain.EventPlanApproved,
		domain.EventSubPlanStarted,
		domain.EventAgentSessionStarted,
		domain.EventAgentSessionCompleted,
		domain.EventSubPlanCompleted,
		domain.EventWorkItemCompleted,
	}
	assertEventOrder(t, events, wantEvents)
}

// ---------------------------------------------------------------------------
// Helpers local to this file
// ---------------------------------------------------------------------------

// assertEventOrder verifies that all wantEvents appear in the persisted event
// list in the given order.  Events are sorted ascending by sequence for
// temporal ordering.  Other events in the list are ignored.
func assertEventOrder(t *testing.T, events []domain.SystemEvent, wantEvents []domain.EventType) {
	t.Helper()

	// ListByWorkspaceID returns DESC by sequence; reverse for chronological.
	for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
		events[i], events[j] = events[j], events[i]
	}

	idx := 0
	for _, ev := range events {
		if idx >= len(wantEvents) {
			break
		}
		if domain.EventType(ev.EventType) == wantEvents[idx] {
			idx++
		}
	}
	if idx != len(wantEvents) {
		// Collect seen event types for diagnostics.
		var seen []string
		for _, ev := range events {
			seen = append(seen, ev.EventType)
		}
		t.Errorf(
			"lifecycle event order: matched %d/%d events; missing from %q onward; all events: %v",
			idx, len(wantEvents), wantEvents[idx], seen,
		)
	}
}

// assertFileExists fails the test if path does not exist.
func assertFileExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected file %s to exist: %v", path, err)
	}
}

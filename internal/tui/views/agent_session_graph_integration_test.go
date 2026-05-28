package views

import (
	"testing"
	"time"

	"github.com/beeemT/substrate/internal/domain"
)

// TestSidebarEntryFromWorkItem_RetryProjection covers the work-item retry
// projection: an old interrupted/failed implementation has a child running
// session, so the sidebar entry must NOT report HasInterrupted (the new
// running session is the leaf) and the work-item state label "Implementing"
// must be preserved.
func TestSidebarEntryFromWorkItem_RetryProjection(t *testing.T) {
	t0 := time.Now().Add(-time.Hour)
	t1 := t0.Add(10 * time.Minute)

	app := newTestApp(Services{
		WorkspaceID:   "ws-1",
		WorkspaceName: "workspace",
		Settings:      newTestSettingsService(),
	})

	wi := domain.Session{
		ID:        "wi-1",
		Title:     "Retry projection",
		State:     domain.SessionImplementing,
		CreatedAt: t0,
		UpdatedAt: t1,
	}
	app.workItems = []domain.Session{wi}
	app.plans["wi-1"] = &domain.Plan{ID: "plan-1", WorkItemID: "wi-1", Status: domain.PlanApproved, UpdatedAt: t0}
	app.subPlans["plan-1"] = []domain.TaskPlan{{ID: "sp-1", PlanID: "plan-1", RepositoryName: "repo-a", Status: domain.SubPlanInProgress, UpdatedAt: t1}}
	app.sessions = []domain.AgentSession{
		{
			ID:             "old",
			WorkItemID:     "wi-1",
			WorkspaceID:    "ws-1",
			SubPlanID:      "sp-1",
			RepositoryName: "repo-a",
			Kind:           domain.AgentSessionKindImplementation,
			Status:         domain.AgentSessionInterrupted,
			CreatedAt:      t0,
			UpdatedAt:      t0,
		},
		{
			ID:                   "new",
			WorkItemID:           "wi-1",
			WorkspaceID:          "ws-1",
			SubPlanID:            "sp-1",
			RepositoryName:       "repo-a",
			Kind:                 domain.AgentSessionKindImplementation,
			Status:               domain.AgentSessionRunning,
			CreatedAt:            t1,
			UpdatedAt:            t1,
			ParentAgentSessionID: "old",
		},
	}

	entry := app.sidebarEntryFromWorkItem(wi)
	if entry.HasInterrupted {
		t.Errorf("HasInterrupted = true, want false (old interrupted session has a child)")
	}
	if entry.HasOpenQuestion {
		t.Errorf("HasOpenQuestion = true, want false")
	}
	if got := entry.Subtitle(); got != "Implementing" {
		t.Errorf("Subtitle = %q, want %q", got, "Implementing")
	}
}

// TestSidebarEntryFromWorkItem_LegacyInterruptedFallback covers the legacy
// fallback: pre-graph rows with NO ParentAgentSessionID set anywhere. The
// newest session for the (sub_plan, repo) group wins, so an old interrupted
// row no longer poisons the work-item label after a newer running session
// has replaced it.
func TestSidebarEntryFromWorkItem_LegacyInterruptedFallback(t *testing.T) {
	t0 := time.Now().Add(-time.Hour)
	t1 := t0.Add(10 * time.Minute)

	app := newTestApp(Services{
		WorkspaceID:   "ws-1",
		WorkspaceName: "workspace",
		Settings:      newTestSettingsService(),
	})

	wi := domain.Session{
		ID:        "wi-1",
		Title:     "Legacy",
		State:     domain.SessionImplementing,
		CreatedAt: t0,
		UpdatedAt: t1,
	}
	app.workItems = []domain.Session{wi}
	app.plans["wi-1"] = &domain.Plan{ID: "plan-1", WorkItemID: "wi-1", Status: domain.PlanApproved, UpdatedAt: t0}
	app.subPlans["plan-1"] = []domain.TaskPlan{{ID: "sp-1", PlanID: "plan-1", RepositoryName: "repo-a", Status: domain.SubPlanInProgress, UpdatedAt: t1}}
	app.sessions = []domain.AgentSession{
		{
			ID:             "legacy-old",
			WorkItemID:     "wi-1",
			WorkspaceID:    "ws-1",
			SubPlanID:      "sp-1",
			RepositoryName: "repo-a",
			Kind:           domain.AgentSessionKindImplementation,
			Status:         domain.AgentSessionInterrupted,
			CreatedAt:      t0,
			UpdatedAt:      t0,
		},
		{
			ID:             "legacy-new",
			WorkItemID:     "wi-1",
			WorkspaceID:    "ws-1",
			SubPlanID:      "sp-1",
			RepositoryName: "repo-a",
			Kind:           domain.AgentSessionKindImplementation,
			Status:         domain.AgentSessionRunning,
			CreatedAt:      t1,
			UpdatedAt:      t1,
			// No ParentAgentSessionID — legacy fallback should still suppress
			// the older interrupted leaf.
		},
	}

	entry := app.sidebarEntryFromWorkItem(wi)
	if entry.HasInterrupted {
		t.Errorf("HasInterrupted = true, want false (legacy fallback should suppress old row)")
	}
	if got := entry.Subtitle(); got != "Implementing" {
		t.Errorf("Subtitle = %q, want %q", got, "Implementing")
	}
}

// TestSidebarEntryFromWorkItem_LeafInterruptedSurfaces verifies that when the
// LEAF itself is interrupted (no newer child replaces it), HasInterrupted is
// surfaced and the subtitle reflects it.
func TestSidebarEntryFromWorkItem_LeafInterruptedSurfaces(t *testing.T) {
	t0 := time.Now().Add(-time.Hour)
	t1 := t0.Add(10 * time.Minute)

	app := newTestApp(Services{
		WorkspaceID:   "ws-1",
		WorkspaceName: "workspace",
		Settings:      newTestSettingsService(),
	})

	wi := domain.Session{
		ID:        "wi-1",
		Title:     "Stuck",
		State:     domain.SessionImplementing,
		CreatedAt: t0,
		UpdatedAt: t1,
	}
	app.workItems = []domain.Session{wi}
	app.plans["wi-1"] = &domain.Plan{ID: "plan-1", WorkItemID: "wi-1", Status: domain.PlanApproved, UpdatedAt: t0}
	app.subPlans["plan-1"] = []domain.TaskPlan{{ID: "sp-1", PlanID: "plan-1", RepositoryName: "repo-a", Status: domain.SubPlanInProgress, UpdatedAt: t1}}
	app.sessions = []domain.AgentSession{
		{
			ID:             "impl",
			WorkItemID:     "wi-1",
			WorkspaceID:    "ws-1",
			SubPlanID:      "sp-1",
			RepositoryName: "repo-a",
			Kind:           domain.AgentSessionKindImplementation,
			Status:         domain.AgentSessionCompleted,
			CreatedAt:      t0,
			UpdatedAt:      t0,
		},
		{
			ID:                   "review",
			WorkItemID:           "wi-1",
			WorkspaceID:          "ws-1",
			SubPlanID:            "sp-1",
			RepositoryName:       "repo-a",
			Kind:                 domain.AgentSessionKindReview,
			Status:               domain.AgentSessionInterrupted,
			CreatedAt:            t1,
			UpdatedAt:            t1,
			ParentAgentSessionID: "impl",
		},
	}

	entry := app.sidebarEntryFromWorkItem(wi)
	if !entry.HasInterrupted {
		t.Errorf("HasInterrupted = false, want true (review leaf is interrupted)")
	}
	if got := entry.Subtitle(); got != "Interrupted" {
		t.Errorf("Subtitle = %q, want %q", got, "Interrupted")
	}
}

// TestLatestTaskForSubPlan_RetryProjection verifies that the overview task
// row for a sub-plan does NOT report `interrupted` when the interrupted
// session has a running child.
func TestLatestTaskForSubPlan_RetryProjection(t *testing.T) {
	t0 := time.Now().Add(-time.Hour)
	t1 := t0.Add(10 * time.Minute)

	app := newTestApp(Services{
		WorkspaceID:   "ws-1",
		WorkspaceName: "workspace",
		Settings:      newTestSettingsService(),
	})

	wi := domain.Session{ID: "wi-1", State: domain.SessionImplementing}
	app.workItems = []domain.Session{wi}
	app.plans["wi-1"] = &domain.Plan{ID: "plan-1", WorkItemID: "wi-1"}
	app.subPlans["plan-1"] = []domain.TaskPlan{{ID: "sp-1", PlanID: "plan-1", RepositoryName: "repo-a"}}
	app.sessions = []domain.AgentSession{
		{
			ID:             "old",
			WorkItemID:     "wi-1",
			SubPlanID:      "sp-1",
			RepositoryName: "repo-a",
			Kind:           domain.AgentSessionKindImplementation,
			Status:         domain.AgentSessionInterrupted,
			CreatedAt:      t0,
			UpdatedAt:      t0,
		},
		{
			ID:                   "new",
			WorkItemID:           "wi-1",
			SubPlanID:            "sp-1",
			RepositoryName:       "repo-a",
			Kind:                 domain.AgentSessionKindImplementation,
			Status:               domain.AgentSessionRunning,
			CreatedAt:            t1,
			UpdatedAt:            t1,
			ParentAgentSessionID: "old",
		},
	}

	latest, waiting, interrupted := app.latestTaskForSubPlan("wi-1", "sp-1")
	if latest == nil || latest.ID != "new" {
		t.Errorf("latest = %v, want id=new", latest)
	}
	if waiting != nil {
		t.Errorf("waiting = %v, want nil", waiting)
	}
	if interrupted != nil {
		t.Errorf("interrupted = %v, want nil (interrupted has a child)", interrupted)
	}
}

// TestLatestTaskForSubPlan_LeafInterruptedSurfaces verifies that when the
// leaf session is itself interrupted, latestTaskForSubPlan returns it as the
// `interrupted` projection so the overview row shows the interrupted note.
func TestLatestTaskForSubPlan_LeafInterruptedSurfaces(t *testing.T) {
	t0 := time.Now().Add(-time.Hour)
	t1 := t0.Add(10 * time.Minute)

	app := newTestApp(Services{
		WorkspaceID:   "ws-1",
		WorkspaceName: "workspace",
		Settings:      newTestSettingsService(),
	})

	wi := domain.Session{ID: "wi-1", State: domain.SessionImplementing}
	app.workItems = []domain.Session{wi}
	app.plans["wi-1"] = &domain.Plan{ID: "plan-1", WorkItemID: "wi-1"}
	app.subPlans["plan-1"] = []domain.TaskPlan{{ID: "sp-1", PlanID: "plan-1", RepositoryName: "repo-a"}}
	app.sessions = []domain.AgentSession{
		{
			ID:             "impl",
			WorkItemID:     "wi-1",
			SubPlanID:      "sp-1",
			RepositoryName: "repo-a",
			Kind:           domain.AgentSessionKindImplementation,
			Status:         domain.AgentSessionCompleted,
			CreatedAt:      t0,
			UpdatedAt:      t0,
		},
		{
			ID:                   "review",
			WorkItemID:           "wi-1",
			SubPlanID:            "sp-1",
			RepositoryName:       "repo-a",
			Kind:                 domain.AgentSessionKindReview,
			Status:               domain.AgentSessionInterrupted,
			CreatedAt:            t1,
			UpdatedAt:            t1,
			ParentAgentSessionID: "impl",
		},
	}

	_, _, interrupted := app.latestTaskForSubPlan("wi-1", "sp-1")
	if interrupted == nil || interrupted.ID != "review" {
		t.Errorf("interrupted = %v, want id=review", interrupted)
	}
}

func TestSidebarEntryFromWorkItem_ReviewRetrySupersedesStaleInterruptedLeaf(t *testing.T) {
	t0 := time.Now().Add(-time.Hour)
	t1 := t0.Add(10 * time.Minute)
	t2 := t1.Add(10 * time.Minute)
	t3 := t2.Add(10 * time.Minute)

	app := newTestApp(Services{
		WorkspaceID:   "ws-1",
		WorkspaceName: "workspace",
		Settings:      newTestSettingsService(),
	})

	wi := domain.Session{ID: "wi-1", Title: "Review retry", State: domain.SessionImplementing, CreatedAt: t0, UpdatedAt: t3}
	app.workItems = []domain.Session{wi}
	app.plans["wi-1"] = &domain.Plan{ID: "plan-1", WorkItemID: "wi-1", Status: domain.PlanApproved, UpdatedAt: t0}
	app.subPlans["plan-1"] = []domain.TaskPlan{{ID: "sp-1", PlanID: "plan-1", RepositoryName: "repo-a", Status: domain.SubPlanInProgress, UpdatedAt: t3}}
	app.sessions = []domain.AgentSession{
		{
			ID:             "impl",
			WorkItemID:     "wi-1",
			WorkspaceID:    "ws-1",
			SubPlanID:      "sp-1",
			RepositoryName: "repo-a",
			Kind:           domain.AgentSessionKindImplementation,
			Status:         domain.AgentSessionCompleted,
			CreatedAt:      t0,
			UpdatedAt:      t0,
		},
		{
			ID:                   "old-review",
			WorkItemID:           "wi-1",
			WorkspaceID:          "ws-1",
			SubPlanID:            "sp-1",
			RepositoryName:       "repo-a",
			Kind:                 domain.AgentSessionKindReview,
			Status:               domain.AgentSessionFailed,
			CreatedAt:            t1,
			UpdatedAt:            t1,
			ParentAgentSessionID: "impl",
		},
		{
			ID:                   "stale-impl",
			WorkItemID:           "wi-1",
			WorkspaceID:          "ws-1",
			SubPlanID:            "sp-1",
			RepositoryName:       "repo-a",
			Kind:                 domain.AgentSessionKindImplementation,
			Status:               domain.AgentSessionInterrupted,
			CreatedAt:            t2,
			UpdatedAt:            t2,
			ParentAgentSessionID: "old-review",
		},
		{
			ID:                   "new-review",
			WorkItemID:           "wi-1",
			WorkspaceID:          "ws-1",
			SubPlanID:            "sp-1",
			RepositoryName:       "repo-a",
			Kind:                 domain.AgentSessionKindReview,
			Status:               domain.AgentSessionRunning,
			CreatedAt:            t3,
			UpdatedAt:            t3,
			ParentAgentSessionID: "stale-impl",
		},
	}

	entry := app.sidebarEntryFromWorkItem(wi)
	if entry.HasInterrupted {
		t.Errorf("HasInterrupted = true, want false (retry review supersedes stale interrupted leaf)")
	}
	if got := entry.Subtitle(); got != "Implementing" {
		t.Errorf("Subtitle = %q, want %q", got, "Implementing")
	}
}

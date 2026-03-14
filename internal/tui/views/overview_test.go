package views

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/beeemT/substrate/internal/domain"
)

func TestTaskOverviewMatchesRootOverviewContent(t *testing.T) {
	t.Parallel()

	app := newSidebarDrilldownTestApp()
	rootView := stripBrowseANSI(app.content.View())
	if app.content.Mode() != ContentModeOverview {
		t.Fatalf("content mode = %v, want %v", app.content.Mode(), ContentModeOverview)
	}

	model, cmd := app.Update(teaKeyRight())
	updated := model.(App)
	if cmd != nil {
		t.Fatalf("overview drilldown cmd = %v, want nil", cmd)
	}
	if updated.content.Mode() != ContentModeOverview {
		t.Fatalf("content mode = %v, want %v", updated.content.Mode(), ContentModeOverview)
	}
	if got := stripBrowseANSI(updated.content.View()); got != rootView {
		t.Fatalf("overview view mismatch\nroot:\n%s\n\ntask overview:\n%s", rootView, got)
	}
}

func TestPlanReviewOverviewExposesActionControls(t *testing.T) {
	t.Parallel()

	now := time.Now()
	app := NewApp(Services{WorkspaceID: "ws-local", WorkspaceName: "local", Settings: &SettingsService{}})
	app.content.SetSize(90, 24)
	app.workItems = []domain.Session{{
		ID:          "wi-1",
		WorkspaceID: "ws-local",
		ExternalID:  "SUB-1",
		Title:       "Review plan",
		State:       domain.SessionPlanReview,
		CreatedAt:   now,
		UpdatedAt:   now,
	}}
	app.plans["wi-1"] = &domain.Plan{
		ID:               "plan-1",
		WorkItemID:       "wi-1",
		Status:           domain.PlanPendingReview,
		Version:          2,
		UpdatedAt:        now,
		OrchestratorPlan: "```substrate-plan\nexecution_groups:\n  - [repo-a]\n```\n\n## Orchestration\n\nShip it.\n\n## SubPlan: repo-a\nDo the thing.\n",
		FAQ:              []domain.FAQEntry{{ID: "faq-1"}},
	}
	app.subPlans["plan-1"] = []domain.TaskPlan{{
		ID:             "sp-1",
		PlanID:         "plan-1",
		RepositoryName: "repo-a",
		Status:         domain.SubPlanPending,
		UpdatedAt:      now,
	}}
	app.currentWorkItemID = "wi-1"
	app.rebuildSidebar()
	app.sidebar.SelectWorkItem("wi-1")
	if cmd := app.updateContentFromState(); cmd != nil {
		t.Fatalf("updateContentFromState() cmd = %v, want nil", cmd)
	}
	if app.content.Mode() != ContentModeOverview {
		t.Fatalf("content mode = %v, want %v", app.content.Mode(), ContentModeOverview)
	}
	if got := len(app.content.overview.data.Actions); got != 1 {
		t.Fatalf("overview actions = %d, want 1", got)
	}
	hints := app.content.KeybindHints()
	for _, want := range []string{"Approve", "Changes", "Reject", "Inspect"} {
		found := false
		for _, hint := range hints {
			if hint.Label == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("keybind hints = %#v, want label %q", hints, want)
		}
	}
	if context := strings.Join(app.content.overview.data.Actions[0].Context, " | "); !strings.Contains(context, "Version: v2") {
		t.Fatalf("overview action context = %q, want plan version", context)
	}
	view := stripBrowseANSI(app.content.View())
	for _, want := range []string{"Action required", "Plan review required", "repo-a"} {
		if !strings.Contains(view, want) {
			t.Fatalf("content view = %q, want %q", view, want)
		}
	}
}

func TestOverviewUsesDurableSourceSummariesWhenAvailable(t *testing.T) {
	t.Parallel()

	now := time.Now()
	app := NewApp(Services{WorkspaceID: "ws-local", WorkspaceName: "local", Settings: &SettingsService{}})
	app.content.SetSize(100, 40)
	app.workItems = []domain.Session{{
		ID:            "wi-1",
		WorkspaceID:   "ws-local",
		ExternalID:    "SUB-1",
		Source:        "github",
		SourceScope:   domain.ScopeIssues,
		SourceItemIDs: []string{"acme/rocket#42", "acme/rocket#43"},
		Title:         "Issue 42 (+1 more)",
		State:         domain.SessionIngested,
		Metadata: map[string]any{
			"tracker_refs":     []domain.TrackerReference{{Provider: "github", Kind: "issue", Owner: "acme", Repo: "rocket", Number: 42}, {Provider: "github", Kind: "issue", Owner: "acme", Repo: "rocket", Number: 43}},
			"source_summaries": []domain.SourceSummary{{Provider: "github", Ref: "acme/rocket#42", Title: "Fix auth", Excerpt: "Investigate auth timeouts", URL: "https://github.com/acme/rocket/issues/42"}, {Provider: "github", Ref: "acme/rocket#43", Title: "Repair billing", Excerpt: "Stabilize billing retries", URL: "https://github.com/acme/rocket/issues/43"}},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}}
	app.currentWorkItemID = "wi-1"
	app.rebuildSidebar()
	app.sidebar.SelectWorkItem("wi-1")
	if cmd := app.updateContentFromState(); cmd != nil {
		t.Fatalf("updateContentFromState() cmd = %v, want nil", cmd)
	}
	view := stripBrowseANSI(app.content.View())
	for _, want := range []string{"Fix auth", "Repair billing", "Investigate auth timeouts", "Stabilize billing retries"} {
		if !strings.Contains(view, want) {
			t.Fatalf("content view = %q, want %q", view, want)
		}
	}
}

func teaKeyRight() tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRight}
}

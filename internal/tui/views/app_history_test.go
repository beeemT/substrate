package views

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/orchestrator"
)

func TestSessionSearchPollingRefreshStaysSilent(t *testing.T) {
	now := time.Now()
	app := NewApp(Services{Settings: &SettingsService{}})
	app.activeOverlay = overlaySessionSearch
	app.sessionSearch.Open(sessionHistoryScopeGlobal, false)
	app.sessionSearch.SetEntries([]domain.SessionHistoryEntry{{
		SessionID:          "sess-1",
		WorkspaceID:        "ws-1",
		WorkspaceName:      "workspace",
		WorkItemID:         "wi-1",
		WorkItemExternalID: "SUB-1",
		WorkItemTitle:      "Work item",
		UpdatedAt:          now,
		CreatedAt:          now,
	}})
	app.sessionSearch.SetLoading(false)

	model, cmd := app.Update(PollTickMsg{})
	if cmd == nil {
		t.Fatal("expected poll tick command batch")
	}
	updated, ok := model.(App)
	if !ok {
		t.Fatalf("model = %T, want App", model)
	}
	if updated.sessionSearch.loading {
		t.Fatal("expected background poll refresh to keep loading indicator hidden")
	}
	if view := updated.sessionSearch.View(); strings.Contains(view, "Searching…") {
		t.Fatalf("view = %q, want no searching indicator during background refresh", view)
	}

	model, cmd = updated.Update(SessionHistorySearchRequestedMsg{})
	if cmd == nil {
		t.Fatal("expected interactive search command")
	}
	updated, ok = model.(App)
	if !ok {
		t.Fatalf("model = %T, want App", model)
	}
	if !updated.sessionSearch.loading {
		t.Fatal("expected interactive search request to show loading indicator")
	}
}

func TestLoadHistoryEntry_LocalWorkspaceUsesWorkItemContent(t *testing.T) {
	app := NewApp(Services{
		WorkspaceID:   "ws-local",
		WorkspaceName: "local",
		Settings:      &SettingsService{},
	})
	app.content.SetSize(80, 20)
	app.workItems = []domain.WorkItem{{
		ID:          "wi-1",
		ExternalID:  "SUB-1",
		Title:       "Local item",
		Description: "## Summary\n\nThis is **important**.",
		State:       domain.WorkItemIngested,
	}}

	cmd := app.loadHistoryEntry(SidebarEntry{
		Kind:        SidebarEntrySessionHistory,
		WorkItemID:  "wi-1",
		SessionID:   "sess-local",
		WorkspaceID: "ws-local",
		ExternalID:  "SUB-1",
		Title:       "Local item",
	})

	if cmd != nil {
		t.Fatalf("loadHistoryEntry() cmd = %v, want nil for local workspace entry", cmd)
	}
	if app.currentWorkItemID != "wi-1" {
		t.Fatalf("currentWorkItemID = %q, want wi-1", app.currentWorkItemID)
	}
	if app.currentHistorySessionID != "" {
		t.Fatalf("currentHistorySessionID = %q, want empty", app.currentHistorySessionID)
	}
	if app.content.Mode() != ContentModeReadyToPlan {
		t.Fatalf("content mode = %v, want %v", app.content.Mode(), ContentModeReadyToPlan)
	}

	view := stripBrowseANSI(app.content.View())
	for _, want := range []string{"SUB-1 · Local item", "Description", "Next step", "Summary", "This is important.", "Press [Enter]"} {
		if !strings.Contains(view, want) {
			t.Fatalf("content view = %q, want %q", view, want)
		}
	}
	for _, raw := range []string{"## Summary", "**important**"} {
		if strings.Contains(view, raw) {
			t.Fatalf("content view = %q, must not contain raw markdown token %q", view, raw)
		}
	}
}

func TestLoadHistoryEntry_RemoteWorkspaceUsesSessionInteraction(t *testing.T) {
	app := NewApp(Services{
		WorkspaceID:   "ws-local",
		WorkspaceName: "local",
		Settings:      &SettingsService{},
	})

	cmd := app.loadHistoryEntry(SidebarEntry{
		Kind:          SidebarEntrySessionHistory,
		SessionID:     "sess-remote",
		WorkspaceID:   "ws-remote",
		WorkspaceName: "remote",
		ExternalID:    "SUB-2",
		Title:         "Remote item",
	})
	if cmd == nil {
		t.Fatal("loadHistoryEntry() cmd = nil, want interaction load command")
	}
	if app.currentWorkItemID != "" {
		t.Fatalf("currentWorkItemID = %q, want empty", app.currentWorkItemID)
	}
	if app.currentHistorySessionID != "sess-remote" {
		t.Fatalf("currentHistorySessionID = %q, want sess-remote", app.currentHistorySessionID)
	}
	if app.content.Mode() != ContentModeSessionInteraction {
		t.Fatalf("content mode = %v, want %v", app.content.Mode(), ContentModeSessionInteraction)
	}

	msg := cmd()
	loaded, ok := msg.(SessionInteractionLoadedMsg)
	if !ok {
		t.Fatalf("cmd() message = %T, want SessionInteractionLoadedMsg", msg)
	}
	if loaded.SessionID != "sess-remote" {
		t.Fatalf("loaded session id = %q, want sess-remote", loaded.SessionID)
	}
}

func TestLoadHistoryEntry_RemoteWorkspaceWithoutAgentSessionShowsSummary(t *testing.T) {
	app := NewApp(Services{
		WorkspaceID:   "ws-local",
		WorkspaceName: "local",
		Settings:      &SettingsService{},
	})
	app.content.SetSize(80, 20)

	cmd := app.loadHistoryEntry(SidebarEntry{
		Kind:          SidebarEntrySessionHistory,
		WorkspaceID:   "ws-remote",
		WorkspaceName: "remote",
		WorkItemID:    "wi-remote",
		ExternalID:    "SUB-3",
		Title:         "Remote planning item",
		State:         domain.WorkItemPlanning,
	})
	if cmd != nil {
		t.Fatalf("loadHistoryEntry() cmd = %v, want nil when no agent session exists", cmd)
	}
	if app.currentHistorySessionID != "" {
		t.Fatalf("currentHistorySessionID = %q, want empty", app.currentHistorySessionID)
	}
	if app.content.Mode() != ContentModeSessionInteraction {
		t.Fatalf("content mode = %v, want %v", app.content.Mode(), ContentModeSessionInteraction)
	}
	if view := app.content.View(); !strings.Contains(view, "No agent-session log is available") {
		t.Fatalf("content view = %q, want summary fallback", view)
	}
}

func TestRebuildSidebarSortsByLastActivity(t *testing.T) {
	now := time.Now()
	older := now.Add(-2 * time.Hour)
	newer := now.Add(-15 * time.Minute)

	app := NewApp(Services{
		WorkspaceID:   "ws-local",
		WorkspaceName: "local",
		Settings:      &SettingsService{},
	})
	app.workItems = []domain.WorkItem{
		{ID: "wi-old", ExternalID: "SUB-1", Title: "Old", State: domain.WorkItemIngested, CreatedAt: older, UpdatedAt: older},
		{ID: "wi-new", ExternalID: "SUB-2", Title: "New", State: domain.WorkItemIngested, CreatedAt: older, UpdatedAt: newer},
	}

	app.rebuildSidebar()

	sel := app.sidebar.Selected()
	if sel == nil {
		t.Fatal("selected sidebar entry = nil")
	}
	if sel.WorkItemID != "wi-new" {
		t.Fatalf("selected work item = %q, want wi-new", sel.WorkItemID)
	}
	app.sidebar.MoveDown()
	sel = app.sidebar.Selected()
	if sel == nil || sel.WorkItemID != "wi-old" {
		t.Fatalf("second work item = %v, want wi-old", sel)
	}
}

func TestWorkItemCreatedMsgUpdatesSidebarImmediately(t *testing.T) {
	now := time.Now()
	older := now.Add(-time.Hour)
	app := NewApp(Services{
		WorkspaceID:   "ws-local",
		WorkspaceName: "local",
		Settings:      &SettingsService{},
	})
	app.workItems = []domain.WorkItem{{
		ID:          "wi-old",
		WorkspaceID: "ws-local",
		ExternalID:  "SUB-1",
		Title:       "Old item",
		State:       domain.WorkItemIngested,
		CreatedAt:   older,
		UpdatedAt:   older,
	}}
	app.rebuildSidebar()

	model, _ := app.Update(WorkItemCreatedMsg{
		WorkItem: domain.WorkItem{
			ID:          "wi-new",
			WorkspaceID: "ws-local",
			ExternalID:  "SUB-2",
			Title:       "New item",
			State:       domain.WorkItemIngested,
			CreatedAt:   now,
			UpdatedAt:   now,
		},
		Message: "Work item created: SUB-2",
	})
	updated, ok := model.(App)
	if !ok {
		t.Fatalf("model = %T, want App", model)
	}
	if updated.currentWorkItemID != "wi-new" {
		t.Fatalf("currentWorkItemID = %q, want wi-new", updated.currentWorkItemID)
	}
	if updated.content.Mode() != ContentModeReadyToPlan {
		t.Fatalf("content mode = %v, want %v", updated.content.Mode(), ContentModeReadyToPlan)
	}
	sel := updated.sidebar.Selected()
	if sel == nil || sel.WorkItemID != "wi-new" {
		t.Fatalf("selected sidebar entry = %v, want wi-new", sel)
	}
	if len(updated.workItems) != 2 {
		t.Fatalf("work item count = %d, want 2", len(updated.workItems))
	}
}

func TestWorkItemCreatedMsgAutoStartsPlanningWhenConfigured(t *testing.T) {
	now := time.Now()
	app := NewApp(Services{
		WorkspaceID:   "ws-local",
		WorkspaceName: "local",
		Settings:      &SettingsService{},
		Planning:      &orchestrator.PlanningService{},
	})

	model, cmd := app.Update(WorkItemCreatedMsg{
		WorkItem: domain.WorkItem{
			ID:          "wi-new",
			WorkspaceID: "ws-local",
			ExternalID:  "SUB-2",
			Title:       "New item",
			State:       domain.WorkItemIngested,
			CreatedAt:   now,
			UpdatedAt:   now,
		},
		Message: "Session created: SUB-2",
	})
	if cmd == nil {
		t.Fatal("expected planning command after work item creation")
	}
	updated, ok := model.(App)
	if !ok {
		t.Fatalf("model = %T, want App", model)
	}
	if updated.content.Mode() != ContentModePlanning {
		t.Fatalf("content mode = %v, want %v", updated.content.Mode(), ContentModePlanning)
	}
	if len(updated.workItems) != 1 || updated.workItems[0].State != domain.WorkItemPlanning {
		t.Fatalf("work items = %#v, want one planning work item", updated.workItems)
	}
}

func newSidebarDrilldownTestApp() App {
	now := time.Now()
	app := NewApp(Services{
		WorkspaceID:   "ws-local",
		WorkspaceName: "local",
		Settings:      &SettingsService{},
	})
	workItem := domain.WorkItem{
		ID:          "wi-1",
		WorkspaceID: "ws-local",
		ExternalID:  "SUB-1",
		Title:       "Work item",
		State:       domain.WorkItemImplementing,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	plan := &domain.Plan{ID: "plan-1", WorkItemID: workItem.ID}
	subPlan := domain.SubPlan{
		ID:             "sp-1",
		PlanID:         plan.ID,
		RepositoryName: "repo-a",
		Status:         domain.SubPlanInProgress,
		UpdatedAt:      now,
	}
	session := domain.AgentSession{
		ID:             "sess-1",
		WorkspaceID:    "ws-local",
		SubPlanID:      subPlan.ID,
		RepositoryName: subPlan.RepositoryName,
		HarnessName:    "omp",
		Status:         domain.AgentSessionRunning,
		UpdatedAt:      now,
	}
	app.workItems = []domain.WorkItem{workItem}
	app.plans[workItem.ID] = plan
	app.subPlans[plan.ID] = []domain.SubPlan{subPlan}
	app.sessions = []domain.AgentSession{session}
	app.rebuildSidebar()
	app.currentWorkItemID = workItem.ID
	app.sidebar.SelectWorkItem(workItem.ID)
	_ = app.updateContentFromState()
	return app
}

func TestSidebarRightDrillsIntoRunsOverview(t *testing.T) {
	app := newSidebarDrilldownTestApp()
	model, cmd := app.Update(tea.KeyMsg{Type: tea.KeyRight})
	updated, ok := model.(App)
	if !ok {
		t.Fatalf("model = %T, want App", model)
	}
	if updated.sidebarMode != sidebarPaneRuns {
		t.Fatalf("sidebarMode = %v, want sidebarPaneRuns", updated.sidebarMode)
	}
	if updated.mainFocus != mainFocusSidebar {
		t.Fatalf("mainFocus = %v, want mainFocusSidebar", updated.mainFocus)
	}
	if updated.sidebar.title != "SUB-1 · Tasks" {
		t.Fatalf("sidebar title = %q, want %q", updated.sidebar.title, "SUB-1 · Tasks")
	}
	sel := updated.sidebar.Selected()
	if sel == nil || sel.Kind != SidebarEntrySessionOverview || sel.SessionID != "" {
		t.Fatalf("selected entry = %#v, want overview row", sel)
	}
	if updated.content.Mode() != ContentModeImplementing {
		t.Fatalf("content mode = %v, want %v", updated.content.Mode(), ContentModeImplementing)
	}
	if cmd == nil {
		t.Fatal("expected overview drilldown to preserve implementing tail command")
	}
}

func TestSidebarRunSelectionShowsRunContent(t *testing.T) {
	app := newSidebarDrilldownTestApp()
	model, _ := app.Update(tea.KeyMsg{Type: tea.KeyRight})
	updated := model.(App)
	model, cmd := updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = model.(App)
	sel := updated.sidebar.Selected()
	if sel == nil || sel.Kind != SidebarEntrySessionRun || sel.SessionID != "sess-1" {
		t.Fatalf("selected entry = %#v, want run row for sess-1", sel)
	}
	if updated.selectedRunSessionID() != "sess-1" {
		t.Fatalf("selected run session = %q, want sess-1", updated.selectedRunSessionID())
	}
	if updated.content.Mode() != ContentModeSessionInteraction {
		t.Fatalf("content mode = %v, want %v", updated.content.Mode(), ContentModeSessionInteraction)
	}
	if updated.content.sessionLog.sessionID != "sess-1" {
		t.Fatalf("session log session id = %q, want sess-1", updated.content.sessionLog.sessionID)
	}
	if cmd == nil {
		t.Fatal("expected selecting a run to tail its log")
	}
}

func TestSidebarLeftBacksOutFromRunContentToSessions(t *testing.T) {
	app := newSidebarDrilldownTestApp()
	model, _ := app.Update(tea.KeyMsg{Type: tea.KeyRight})
	updated := model.(App)
	model, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = model.(App)
	model, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRight})
	updated = model.(App)
	if updated.mainFocus != mainFocusContent {
		t.Fatalf("mainFocus = %v, want mainFocusContent", updated.mainFocus)
	}
	model, _ = updated.Update(tea.KeyMsg{Type: tea.KeyLeft})
	updated = model.(App)
	if updated.mainFocus != mainFocusSidebar || updated.sidebarMode != sidebarPaneRuns {
		t.Fatalf("focus/sidebarMode = %v/%v, want sidebar focus in run pane", updated.mainFocus, updated.sidebarMode)
	}
	model, _ = updated.Update(tea.KeyMsg{Type: tea.KeyLeft})
	updated = model.(App)
	if updated.sidebarMode != sidebarPaneSessions {
		t.Fatalf("sidebarMode = %v, want sidebarPaneSessions", updated.sidebarMode)
	}
	sel := updated.sidebar.Selected()
	if sel == nil || sel.Kind != SidebarEntryWorkItem || sel.WorkItemID != "wi-1" {
		t.Fatalf("selected entry = %#v, want parent work item row", sel)
	}
	if updated.content.Mode() != ContentModeImplementing {
		t.Fatalf("content mode = %v, want %v", updated.content.Mode(), ContentModeImplementing)
	}
}

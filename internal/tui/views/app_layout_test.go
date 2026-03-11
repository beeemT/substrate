package views

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/tui/styles"
)

func TestAppStatusBarTextIncludesWorkspace(t *testing.T) {
	app := NewApp(Services{
		WorkspaceID:   "ws-1",
		WorkspaceName: "workspace",
		Settings:      &SettingsService{},
	})

	if got := app.statusBarText(); got != "workspace · 0 active sessions" {
		t.Fatalf("status bar text = %q, want %q", got, "workspace · 0 active sessions")
	}
}

func TestAppStatusBarTextCountsOnlyActiveSessions(t *testing.T) {
	t.Parallel()

	app := NewApp(Services{
		WorkspaceID:   "ws-1",
		WorkspaceName: "workspace",
		Settings:      &SettingsService{},
	})
	app.sessions = []domain.AgentSession{
		{ID: "pending", Status: domain.AgentSessionPending},
		{ID: "running", Status: domain.AgentSessionRunning},
		{ID: "waiting", Status: domain.AgentSessionWaitingForAnswer},
		{ID: "interrupted", Status: domain.AgentSessionInterrupted},
		{ID: "completed", Status: domain.AgentSessionCompleted},
		{ID: "failed", Status: domain.AgentSessionFailed},
	}

	if got := app.statusBarText(); got != "workspace · 3 active sessions" {
		t.Fatalf("status bar text = %q, want %q", got, "workspace · 3 active sessions")
	}
}

func TestAppDeleteShortcutAppearsAndTriggersForSelectedTaskSession(t *testing.T) {
	t.Parallel()

	app := NewApp(Services{WorkspaceID: "ws-1", Settings: &SettingsService{}})
	app.sidebarMode = sidebarPaneTasks
	app.currentWorkItemID = "wi-1"
	app.taskSessionSelectionByWorkItem["wi-1"] = "sess-1"
	app.plans["wi-1"] = &domain.Plan{ID: "plan-1", WorkItemID: "wi-1"}
	app.subPlans["plan-1"] = []domain.SubPlan{{ID: "sp-1", PlanID: "plan-1"}}
	app.sessions = []domain.AgentSession{{ID: "sess-1", SubPlanID: "sp-1", Status: domain.AgentSessionCompleted}}

	if got := app.deletableSessionID(); got != "sess-1" {
		t.Fatalf("deletable session id = %q, want sess-1", got)
	}

	hints := app.currentHints()
	found := false
	for _, hint := range hints {
		if hint.Key == "d" && hint.Label == "Delete session" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("current hints = %#v, want delete session hint", hints)
	}

	model, cmd := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	updated, ok := model.(App)
	if !ok {
		t.Fatalf("model = %T, want App", model)
	}
	if cmd != nil {
		t.Fatalf("cmd = %v, want nil while showing confirm dialog", cmd)
	}
	if !updated.confirmActive {
		t.Fatal("expected delete shortcut to open confirm dialog")
	}
	confirmView := stripBrowseANSI(updated.confirm.View())
	if !strings.Contains(confirmView, "Delete Session") || !strings.Contains(confirmView, "review data") {
		t.Fatalf("confirm view = %q, want delete session confirmation copy", confirmView)
	}
}

func TestAppViewUsesFooterForWorkspaceInfo(t *testing.T) {
	app := NewApp(Services{
		WorkspaceID:   "ws-1",
		WorkspaceName: "workspace",
		Settings:      &SettingsService{},
	})

	model, _ := app.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	updated, ok := model.(App)
	if !ok {
		t.Fatalf("model = %T, want App", model)
	}

	view := updated.View()
	lines := strings.Split(view, "\n")
	if !strings.Contains(view, "workspace · 0 active sessions") {
		t.Fatalf("view = %q, want workspace info in footer", view)
	}
	if strings.Contains(view, "Substrate ─ workspace") {
		t.Fatalf("view = %q, want header line removed", view)
	}
	if len(lines) != 20 {
		t.Fatalf("line count = %d, want 20", len(lines))
	}
	if !strings.Contains(lines[0], "╭") || !strings.Contains(lines[0], "╮") {
		t.Fatalf("top body line = %q, want rounded top borders", lines[0])
	}
	if !strings.Contains(lines[len(lines)-2], "╰") || !strings.Contains(lines[len(lines)-2], "╯") {
		t.Fatalf("bottom body line = %q, want rounded bottom borders above the footer", lines[len(lines)-2])
	}
	if strings.Contains(lines[len(lines)-1], "─") {
		t.Fatalf("footer line = %q, want borderless status bar", lines[len(lines)-1])
	}
}

func sizedLayoutTestApp(t *testing.T, width, height int) App {
	t.Helper()

	app := NewApp(Services{
		WorkspaceID:   "ws-1",
		WorkspaceName: "workspace",
		Settings:      &SettingsService{},
	})

	model, _ := app.Update(tea.WindowSizeMsg{Width: width, Height: height})
	updated, ok := model.(App)
	if !ok {
		t.Fatalf("model = %T, want App", model)
	}
	return updated
}

func assertAppViewFitsWindow(t *testing.T, view string, width, height int) []string {
	t.Helper()

	lines := strings.Split(view, "\n")
	if got := len(lines); got != height {
		t.Fatalf("line count = %d, want %d\nview:\n%s", got, height, view)
	}
	for i, line := range lines {
		if got := ansi.StringWidth(line); got > width {
			t.Fatalf("line %d width = %d, want <= %d\nline: %q", i+1, got, width, line)
		}
	}
	return lines
}

func assertBodyEndsAboveFooter(t *testing.T, lines []string) {
	t.Helper()

	if !strings.Contains(lines[len(lines)-2], "╰") || !strings.Contains(lines[len(lines)-2], "╯") {
		t.Fatalf("bottom body line = %q, want rounded bottom borders above the footer", lines[len(lines)-2])
	}
	if strings.Contains(lines[len(lines)-1], "─") {
		t.Fatalf("footer line = %q, want borderless status bar", lines[len(lines)-1])
	}
}

func TestComputeMainPageLayoutReservesSettingsStylePaneGap(t *testing.T) {
	t.Parallel()

	layout := styles.ComputeMainPageLayout(80, 20, SidebarWidth, styles.DefaultChromeMetrics)

	if layout.PaneGapWidth != 1 {
		t.Fatalf("pane gap width = %d, want 1", layout.PaneGapWidth)
	}
	if got := layout.SidebarPaneWidth + layout.ContentPaneWidth + layout.PaneGapWidth; got != 80 {
		t.Fatalf("layout width = %d, want 80", got)
	}
}

func TestComputeMainPageLayoutDropsPaneGapWhenContentDoesNotFit(t *testing.T) {
	t.Parallel()

	layout := styles.ComputeMainPageLayout(36, 20, SidebarWidth, styles.DefaultChromeMetrics)

	if layout.PaneGapWidth != 0 {
		t.Fatalf("pane gap width = %d, want 0", layout.PaneGapWidth)
	}
	if layout.ContentPaneWidth != 0 {
		t.Fatalf("content pane width = %d, want 0", layout.ContentPaneWidth)
	}
}

func TestComputeMainPageLayoutShrinksSidebarToPreserveGapAndContentFrame(t *testing.T) {
	t.Parallel()

	layout := styles.ComputeMainPageLayout(37, 20, SidebarWidth, styles.DefaultChromeMetrics)

	if layout.PaneGapWidth != 1 {
		t.Fatalf("pane gap width = %d, want 1", layout.PaneGapWidth)
	}
	if layout.ContentPaneWidth != styles.DefaultChromeMetrics.Pane.HorizontalFrame() {
		t.Fatalf("content pane width = %d, want %d", layout.ContentPaneWidth, styles.DefaultChromeMetrics.Pane.HorizontalFrame())
	}
	if got := layout.SidebarPaneWidth + layout.ContentPaneWidth + layout.PaneGapWidth; got != 37 {
		t.Fatalf("layout width = %d, want 37", got)
	}
}

func TestAppViewRendersSingleColumnPaneGap(t *testing.T) {
	t.Parallel()

	app := sizedLayoutTestApp(t, 80, 20)

	lines := assertAppViewFitsWindow(t, app.View(), 80, 20)
	if !strings.Contains(ansi.Strip(lines[0]), "╮ ╭") {
		t.Fatalf("top body line = %q, want a single-column gap between panes", ansi.Strip(lines[0]))
	}
}

func TestAppViewWithSessionInteractionFitsWindow(t *testing.T) {
	t.Parallel()

	app := sizedLayoutTestApp(t, 72, 16)
	app.sidebar.SetEntries([]SidebarEntry{{
		Kind:           SidebarEntryWorkItem,
		WorkItemID:     "wi-1",
		SessionID:      "sess-1",
		ExternalID:     "SUB-1",
		Title:          "Investigate overflow",
		WorkspaceName:  "workspace",
		RepositoryName: "repo-1",
		State:          domain.WorkItemImplementing,
		SessionStatus:  domain.AgentSessionRunning,
	}})
	app.content.SetSessionInteraction(
		"SUB-1 · Investigate overflow",
		"SUB-1 · workspace · repo-1 · sess-1",
		[]string{"line 1", "line 2", "line 3", "line 4"},
	)

	lines := assertAppViewFitsWindow(t, app.View(), 72, 16)
	assertBodyEndsAboveFooter(t, lines)
}

func TestAppViewWithReadyToPlanOverviewFitsWindow(t *testing.T) {
	t.Parallel()

	app := sizedLayoutTestApp(t, 72, 16)
	app.sidebar.SetEntries([]SidebarEntry{{
		Kind:       SidebarEntryWorkItem,
		WorkItemID: "wi-1",
		ExternalID: "SUB-1",
		Title:      "Investigate overflow",
		State:      domain.WorkItemIngested,
	}})
	app.content.SetWorkItem(&domain.WorkItem{
		ID:          "wi-1",
		ExternalID:  "SUB-1",
		Source:      "github",
		Title:       "Investigate overflow",
		Description: "## Summary\n\nThis is **important**.",
		Labels:      []string{"bug", "backend"},
		Metadata: map[string]any{
			"tracker_refs": []domain.TrackerReference{{Provider: "github", Kind: "issue", Owner: "acme", Repo: "rocket", Number: 42}},
		},
		State: domain.WorkItemIngested,
	})
	app.content.SetMode(ContentModeReadyToPlan)

	lines := assertAppViewFitsWindow(t, app.View(), 72, 16)
	assertBodyEndsAboveFooter(t, lines)
	plain := ansi.Strip(strings.Join(lines, "\n"))
	for _, want := range []string{"Details", "╭", "╮", "┌", "┐"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("view = %q, want %q in ready overview layout", plain, want)
		}
	}
	for _, hidden := range []string{"GitHub", "acme/rocket", "Labels: bug, backend"} {
		if strings.Contains(plain, hidden) {
			t.Fatalf("view = %q, want ready overview to omit source detail %q", plain, hidden)
		}
	}
	footerRegion := ansi.Strip(strings.Join(lines[max(0, len(lines)-6):], "\n"))
	if !strings.Contains(footerRegion, "Press [Enter]") {
		t.Fatalf("footer region = %q, want the CTA near the bottom of the content pane", footerRegion)
	}
}

func TestAppViewWithImplementingSessionFitsWindow(t *testing.T) {
	t.Parallel()

	app := sizedLayoutTestApp(t, 72, 16)
	app.sidebar.SetEntries([]SidebarEntry{{
		Kind:          SidebarEntryWorkItem,
		WorkItemID:    "wi-1",
		ExternalID:    "SUB-1",
		Title:         "Implement overflow fix",
		State:         domain.WorkItemImplementing,
		SessionStatus: domain.AgentSessionRunning,
	}})
	app.content.SetWorkItem(&domain.WorkItem{
		ID:         "wi-1",
		ExternalID: "SUB-1",
		Title:      "Implement overflow fix",
		State:      domain.WorkItemImplementing,
	})
	app.content.SetMode(ContentModeImplementing)
	app.content.implementing.SetRepos([]RepoProgress{{
		Name:      "repo-1",
		SubPlanID: "sp-1",
		SessionID: "sess-1",
		Status:    domain.SubPlanInProgress,
	}})

	lines := assertAppViewFitsWindow(t, app.View(), 72, 16)
	assertBodyEndsAboveFooter(t, lines)
}

func TestAppViewWithDuplicateSessionDialogFitsWindow(t *testing.T) {
	t.Parallel()

	app := sizedLayoutTestApp(t, 48, 14)
	app.showDuplicateSessionDialog(
		domain.WorkItem{ID: "wi-requested", ExternalID: "SUB-99", Title: "Requested item"},
		domain.WorkItem{ID: "wi-existing", ExternalID: "SUB-1", Title: "Existing item", State: domain.WorkItemIngested},
	)

	lines := assertAppViewFitsWindow(t, app.View(), 48, 14)
	plain := ansi.Strip(strings.Join(lines, "\n"))
	for _, want := range []string{"Work item already exists", "Existing work item:", "Open existing", "Start planning"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("view = %q, want %q in duplicate-session dialog", plain, want)
		}
	}
}

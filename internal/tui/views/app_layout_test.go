package views

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/sessionlog"
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
	app.sessions = []domain.Task{
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
	app.subPlans["plan-1"] = []domain.TaskPlan{{ID: "sp-1", PlanID: "plan-1"}}
	app.sessions = []domain.Task{{ID: "sess-1", WorkItemID: "wi-1", Phase: domain.TaskPhaseImplementation, SubPlanID: "sp-1", Status: domain.AgentSessionCompleted}}

	if got := app.deletableSessionID(); got != "wi-1" {
		t.Fatalf("deletable session id = %q, want wi-1", got)
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
	if !strings.Contains(confirmView, "Delete Session") || !strings.Contains(confirmView, "full session") {
		t.Fatalf("confirm view = %q, want delete session confirmation copy", confirmView)
	}
}

func TestAppDeleteShortcutAppearsAndTriggersForSelectedSingleSessionWorkItem(t *testing.T) {
	t.Parallel()

	app := newSidebarDrilldownTestApp()

	if got := app.deletableSessionID(); got != "wi-1" {
		t.Fatalf("deletable session id = %q, want wi-1", got)
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
	if !strings.Contains(confirmView, "Delete Session") || !strings.Contains(confirmView, "full session") {
		t.Fatalf("confirm view = %q, want delete session confirmation copy", confirmView)
	}
}

func TestAppViewShowsDeleteHintForSelectedSingleSessionWorkItem(t *testing.T) {
	t.Parallel()

	app := newSidebarDrilldownTestApp()
	app.svcs.WorkspaceName = "workspace-with-a-very-long-name"

	model, _ := app.Update(tea.WindowSizeMsg{Width: 72, Height: 18})
	updated := model.(App)

	if got := updated.deletableSessionID(); got != "wi-1" {
		t.Fatalf("deletable session id = %q, want wi-1", got)
	}

	lines := assertAppViewFitsWindow(t, updated.View(), 72, 18)
	// Delete hint is the leading contextual hint, always on footer line 1 (lines[len-2] when sbHeight=2).
	// Concatenate both footer lines so the check works regardless of footer height.
	footer := stripBrowseANSI(lines[len(lines)-2]) + " " + stripBrowseANSI(lines[len(lines)-1])
	if !strings.Contains(footer, "Delete session") {
		t.Fatalf("footer = %q, want delete hint for selected single-session work item", footer)
	}
}

func TestAppViewPlacesDeleteHintBetweenNewAndSearch(t *testing.T) {
	t.Parallel()

	app := newSidebarDrilldownTestApp()
	app.svcs.WorkspaceName = "workspace-with-a-very-long-name"

	model, _ := app.Update(tea.WindowSizeMsg{Width: 140, Height: 18})
	updated := model.(App)

	lines := assertAppViewFitsWindow(t, updated.View(), 140, 18)
	// At width=140 with a long workspace name, hints may overflow to a second line.
	// Concatenate both footer lines so all hints are found regardless of line split.
	footer := stripBrowseANSI(lines[len(lines)-2]) + " " + stripBrowseANSI(lines[len(lines)-1])

	if !strings.Contains(footer, "New session") || !strings.Contains(footer, "Delete session") || !strings.Contains(footer, "Search sessions") {
		t.Fatalf("footer = %q, want new, delete, and search hints visible", footer)
	}
}

func TestAppViewShowsDeleteHintForSelectedTaskSession(t *testing.T) {
	t.Parallel()

	app := newSidebarDrilldownTestApp()
	app.svcs.WorkspaceName = "workspace-with-a-very-long-name"

	model, _ := app.Update(tea.WindowSizeMsg{Width: 44, Height: 18})
	updated := model.(App)

	model, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRight})
	updated = model.(App)
	model, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = model.(App)
	model, cmd := updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = model.(App)
	if cmd == nil {
		t.Fatal("expected task selection to tail its log")
	}

	lines := assertAppViewFitsWindow(t, updated.View(), 44, 18)
	footer := stripBrowseANSI(lines[len(lines)-2]) + " " + stripBrowseANSI(lines[len(lines)-1])
	if !strings.Contains(footer, "Delete session") {
		t.Fatalf("footer = %q, want delete hint for selected task session", footer)
	}
}

func TestAppViewShowsDeleteHintForFocusedTaskSession(t *testing.T) {
	t.Parallel()

	app := newSidebarDrilldownTestApp()
	app.svcs.WorkspaceName = "workspace-with-a-very-long-name"

	model, _ := app.Update(tea.WindowSizeMsg{Width: 44, Height: 18})
	updated := model.(App)

	model, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRight})
	updated = model.(App)
	model, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = model.(App)
	model, cmd := updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = model.(App)
	if cmd == nil {
		t.Fatal("expected task selection to tail its log")
	}
	model, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRight})
	updated = model.(App)

	lines := assertAppViewFitsWindow(t, updated.View(), 44, 18)
	footer := stripBrowseANSI(lines[len(lines)-2]) + " " + stripBrowseANSI(lines[len(lines)-1])
	if !strings.Contains(footer, "Delete session") {
		t.Fatalf("footer = %q, want delete hint for focused task session", footer)
	}
}

func TestAppViewKeepsDeleteKeyVisibleForSelectedTaskSessionAtNarrowWidth(t *testing.T) {
	t.Parallel()

	app := newSidebarDrilldownTestApp()
	app.svcs.WorkspaceName = "workspace-with-a-very-long-name"

	model, _ := app.Update(tea.WindowSizeMsg{Width: 18, Height: 18})
	updated := model.(App)

	model, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRight})
	updated = model.(App)
	model, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = model.(App)
	model, cmd := updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = model.(App)
	if cmd == nil {
		t.Fatal("expected task selection to tail its log")
	}

	lines := assertAppViewFitsWindow(t, updated.View(), 18, 18)
	// Delete hint is the leading contextual hint; it appears on footer line 1.
	// Concatenate both footer lines to handle either 1- or 2-line status bar.
	footer := stripBrowseANSI(lines[len(lines)-2]) + " " + stripBrowseANSI(lines[len(lines)-1])
	if !strings.Contains(footer, "[d]") {
		t.Fatalf("footer = %q, want delete key visible for selected task session", footer)
	}
	if strings.Contains(footer, "active sessions") {
		t.Fatalf("footer = %q, want workspace metadata dropped before the delete action", footer)
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
	assertBodyEndsAboveFooter(t, lines)
}

func TestAppViewHighlightsActivePaneWithoutChangingBodyText(t *testing.T) {
	t.Parallel()

	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(previousProfile)

	app := sizedLayoutTestApp(t, 80, 20)

	sidebarLines := assertAppViewFitsWindow(t, app.View(), 80, 20)
	assertBodyEndsAboveFooter(t, sidebarLines)
	sidebarBody := joinViewBodyLines(sidebarLines)

	app.mainFocus = mainFocusContent
	contentLines := assertAppViewFitsWindow(t, app.View(), 80, 20)
	assertBodyEndsAboveFooter(t, contentLines)
	contentBody := joinViewBodyLines(contentLines)

	if sidebarBody == contentBody {
		t.Fatal("expected app body styling to change when focus moves between sidebar and content panes")
	}
	if ansi.Strip(sidebarBody) != ansi.Strip(contentBody) {
		t.Fatal("expected pane focus change to affect styling only, not body text layout")
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
	// Scan backward past status bar lines to find the pane bottom border.
	borderIdx := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.Contains(lines[i], "\u2570") && strings.Contains(lines[i], "\u256f") {
			borderIdx = i
			break
		}
	}
	if borderIdx == -1 {
		t.Fatalf("no pane bottom border found in view")
	}
	// All lines below the border should be status bar lines (no horizontal rule).
	for i := borderIdx + 1; i < len(lines); i++ {
		if strings.Contains(lines[i], "─") {
			t.Fatalf("status bar line %d = %q, want borderless status bar", i, lines[i])
		}
	}
}

// joinViewBodyLines joins only the body lines of the view (excluding footer lines).
// It scans backward for the pane bottom border chars to find where the body ends.
func joinViewBodyLines(lines []string) string {
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.Contains(lines[i], "\u2570") && strings.Contains(lines[i], "\u256f") {
			return strings.Join(lines[:i+1], "\n")
		}
	}
	return strings.Join(lines, "\n")
}

func TestComputeMainPageLayoutReservesSettingsStylePaneGap(t *testing.T) {
	t.Parallel()

	layout := styles.ComputeMainPageLayout(80, 20, SidebarWidth, styles.DefaultChromeMetrics, styles.DefaultChromeMetrics.StatusBarHeight)

	if layout.PaneGapWidth != 1 {
		t.Fatalf("pane gap width = %d, want 1", layout.PaneGapWidth)
	}
	if got := layout.SidebarPaneWidth + layout.ContentPaneWidth + layout.PaneGapWidth; got != 80 {
		t.Fatalf("layout width = %d, want 80", got)
	}
}

func TestComputeMainPageLayoutDropsPaneGapWhenContentDoesNotFit(t *testing.T) {
	t.Parallel()

	layout := styles.ComputeMainPageLayout(36, 20, SidebarWidth, styles.DefaultChromeMetrics, styles.DefaultChromeMetrics.StatusBarHeight)

	if layout.PaneGapWidth != 0 {
		t.Fatalf("pane gap width = %d, want 0", layout.PaneGapWidth)
	}
	if layout.ContentPaneWidth != 0 {
		t.Fatalf("content pane width = %d, want 0", layout.ContentPaneWidth)
	}
}

func TestComputeMainPageLayoutDropsPaneGapOnNarrowTerminals(t *testing.T) {
	t.Parallel()

	layout := styles.ComputeMainPageLayout(59, 20, SidebarWidth, styles.DefaultChromeMetrics, styles.DefaultChromeMetrics.StatusBarHeight)

	if layout.PaneGapWidth != 0 {
		t.Fatalf("pane gap width = %d, want 0 on narrow terminal", layout.PaneGapWidth)
	}
	// Without the gap, the full sidebar and all remaining columns go to content.
	expectedSidebarPane := SidebarWidth + styles.DefaultChromeMetrics.Pane.HorizontalFrame()
	if layout.SidebarPaneWidth != expectedSidebarPane {
		t.Fatalf("sidebar pane width = %d, want %d (full size, no gap to preserve)", layout.SidebarPaneWidth, expectedSidebarPane)
	}
	expectedContent := 59 - expectedSidebarPane
	if layout.ContentPaneWidth != expectedContent {
		t.Fatalf("content pane width = %d, want %d", layout.ContentPaneWidth, expectedContent)
	}
}

func TestComputeMainPageLayoutShrinksSidebarToPreserveGapAndContentFrame(t *testing.T) {
	t.Parallel()

	// Use a low gap threshold so the shrinking code path is exercisable.
	chrome := styles.DefaultChromeMetrics
	chrome.MinWidthForPaneGap = 37
	layout := styles.ComputeMainPageLayout(37, 20, SidebarWidth, chrome, styles.DefaultChromeMetrics.StatusBarHeight)

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

func TestAppViewRendersNoGapOnNarrowTerminal(t *testing.T) {
	t.Parallel()

	app := sizedLayoutTestApp(t, 59, 20)

	lines := assertAppViewFitsWindow(t, app.View(), 59, 20)
	topLine := ansi.Strip(lines[0])
	if strings.Contains(topLine, "╮ ╭") {
		t.Fatalf("top body line = %q, want no gap between panes on narrow terminal", topLine)
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
		State:          domain.SessionImplementing,
		SessionStatus:  domain.AgentSessionRunning,
	}})
	app.content.SetSessionInteraction(
		"SUB-1 · Investigate overflow",
		"SUB-1 · workspace · repo-1 · sess-1",
		[]sessionlog.Entry{{Kind: sessionlog.KindPlain, Text: "line 1"}, {Kind: sessionlog.KindPlain, Text: "line 2"}, {Kind: sessionlog.KindPlain, Text: "line 3"}, {Kind: sessionlog.KindPlain, Text: "line 4"}},
	)

	lines := assertAppViewFitsWindow(t, app.View(), 72, 16)
	assertBodyEndsAboveFooter(t, lines)
}

func TestAppViewAddsHorizontalInsetToMainContentPane(t *testing.T) {
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
		State:          domain.SessionImplementing,
		SessionStatus:  domain.AgentSessionRunning,
	}})
	app.content.SetSessionInteraction(
		"SUB-1 · Investigate overflow",
		"SUB-1 · workspace · repo-1 · sess-1",
		[]sessionlog.Entry{{Kind: sessionlog.KindPlain, Text: "line 1"}, {Kind: sessionlog.KindPlain, Text: "line 2"}, {Kind: sessionlog.KindPlain, Text: "line 3"}, {Kind: sessionlog.KindPlain, Text: "line 4"}},
	)

	lines := assertAppViewFitsWindow(t, app.View(), 72, 16)

	found := false
	for _, line := range lines {
		plain := ansi.Strip(line)
		if !strings.Contains(plain, "line 1") {
			continue
		}
		found = true
		if !strings.Contains(plain, "│ line 1") {
			t.Fatalf("line = %q, want a horizontal inset between the content border and session output", plain)
		}

		break
	}
	if !found {
		t.Fatal("expected a rendered session output line in the content pane")
	}
}

func TestAppSidebarShowsProviderWithoutExternalIDPrefix(t *testing.T) {
	t.Parallel()

	app := sizedLayoutTestApp(t, 72, 16)
	app.workItems = []domain.Session{{
		ID:          "wi-1",
		WorkspaceID: "ws-1",
		ExternalID:  "gh:issue:acme/rocket#42",
		Source:      "github",
		Title:       "Investigate overflow",
		State:       domain.SessionIngested,
	}}
	app.rebuildSidebar()

	lines := assertAppViewFitsWindow(t, app.View(), 72, 16)
	assertBodyEndsAboveFooter(t, lines)
	plain := ansi.Strip(strings.Join(lines, "\n"))
	if strings.Contains(plain, "gh:issue:") {
		t.Fatalf("view = %q, want sidebar titles to omit the raw adapter prefix", plain)
	}
	if !strings.Contains(plain, "acme/rocket#42 · GitHub") {
		t.Fatalf("view = %q, want sidebar node to keep the ref and surface the provider label", plain)
	}
}

func TestAppViewWithOverviewActionRequiredFitsWindow(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		width  int
		height int
	}{
		{name: "regular", width: 72, height: 24},
		{name: "narrow", width: 48, height: 18},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			app := sizedLayoutTestApp(t, tc.width, tc.height)
			app.sidebar.SetEntries([]SidebarEntry{{
				Kind:       SidebarEntryWorkItem,
				WorkItemID: "wi-1",
				ExternalID: "SUB-1",
				Title:      "Review plan",
				State:      domain.SessionPlanReview,
			}})
			app.content.SetWorkItem(&domain.Session{
				ID:         "wi-1",
				ExternalID: "SUB-1",
				Title:      "Review plan",
				State:      domain.SessionPlanReview,
			})
			app.content.SetOverviewData(SessionOverviewData{
				WorkItemID: "wi-1",
				State:      domain.SessionPlanReview,
				Header: OverviewHeader{
					ExternalID:   "SUB-1",
					Title:        "Review plan",
					StatusLabel:  "Plan review needed",
					ProgressText: "0/2 repos complete",
					Badges:       []string{"waiting for approval"},
				},
				Actions: []OverviewActionCard{{
					Kind:     overviewActionPlanReview,
					Title:    "Plan review required",
					Blocked:  "Implementation is waiting for plan approval",
					Why:      "The plan must be approved, revised, or rejected before implementation can continue.",
					Affected: []string{"repo-a", "repo-b"},
					Context:  []string{"Version: v2", "Affected repos: 2", "Ship it safely."},
				}},
				Sources: []OverviewSourceItem{{Provider: "GitHub", Ref: "acme/rocket#42"}},
				Plan: OverviewPlan{
					StateLabel: "Plan review needed",
					Exists:     true,
					Version:    2,
					RepoCount:  2,
					Excerpt:    []string{"Ship it safely.", "Repo-a first, repo-b second."},
				},
				Tasks: []OverviewTaskRow{{RepoName: "repo-a", TaskPlanStatus: "Pending"}, {RepoName: "repo-b", TaskPlanStatus: "Pending"}},
			})
			app.content.SetMode(ContentModeOverview)

			lines := assertAppViewFitsWindow(t, app.View(), tc.width, tc.height)
			assertBodyEndsAboveFooter(t, lines)
			plain := ansi.Strip(strings.Join(lines, "\n"))
			wants := []string{"Summary"}
			if tc.name == "regular" {
				wants = append(wants, "Action required")
			}
			for _, want := range wants {
				if !strings.Contains(plain, want) {
					t.Fatalf("view = %q, want %q in overview layout", plain, want)
				}
			}
		})
	}
}

func TestAppViewWithDuplicateSessionDialogFitsWindow(t *testing.T) {
	t.Parallel()

	app := sizedLayoutTestApp(t, 48, 14)
	app.showDuplicateSessionDialog(
		domain.Session{ID: "wi-requested", ExternalID: "SUB-99", Title: "Requested item"},
		domain.Session{ID: "wi-existing", ExternalID: "SUB-1", Title: "Existing item", State: domain.SessionIngested},
	)

	lines := assertAppViewFitsWindow(t, app.View(), 48, 14)
	plain := ansi.Strip(strings.Join(lines, "\n"))
	for _, want := range []string{"Session already exists", "Existing session:", "Open existing", "Start planning"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("view = %q, want %q in duplicate-session dialog", plain, want)
		}
	}
}

func TestSidebarSessionsHintsIncludeFilterGroupSort(t *testing.T) {
	t.Parallel()

	app := NewApp(Services{WorkspaceID: "ws-1", Settings: &SettingsService{}})
	// Default state: sidebar focused, sessions pane.
	if app.mainFocus != mainFocusSidebar || app.sidebarMode != sidebarPaneSessions {
		t.Fatal("expected default app state to be sidebar-focused on the sessions pane")
	}

	hints := app.currentHints()
	want := []struct{ key, label string }{
		{"f", "Filter"},
		{"g", "Group"},
		{"o", "Sort"},
	}
	for _, w := range want {
		found := false
		for _, h := range hints {
			if h.Key == w.key && h.Label == w.label {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("currentHints() = %#v, want hint {%q, %q}", hints, w.key, w.label)
		}
	}
}

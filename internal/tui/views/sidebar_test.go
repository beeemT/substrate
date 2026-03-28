package views_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/tui/styles"
	"github.com/beeemT/substrate/internal/tui/views"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func makeSidebarStyles() styles.Styles {
	return styles.NewStyles(styles.DefaultTheme)
}

var ansiEscapePattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string {
	return ansiEscapePattern.ReplaceAllString(s, "")
}

func trimSidebarBorder(line string) string {
	if trimmed, ok := strings.CutSuffix(line, "│"); ok {
		return trimmed
	}

	return line
}

func headerStart(line, text string) int {
	return lipgloss.Width(strings.Split(line, text)[0])
}

func makeSessions(n int) []views.SidebarEntry {
	sessions := make([]views.SidebarEntry, n)
	for i := range sessions {
		sessions[i] = views.SidebarEntry{
			Kind:       views.SidebarEntryWorkItem,
			WorkItemID: string(rune('A' + i)),
			ExternalID: string(rune('A' + i)),
			Title:      "Session " + string(rune('A'+i)),
			State:      domain.SessionIngested,
		}
	}

	return sessions
}

func TestSidebarEmpty(t *testing.T) {
	t.Helper()
	m := views.NewSidebarModel(makeSidebarStyles())
	m.SetHeight(20)
	out := m.View()
	if out == "" {
		t.Fatal("expected non-empty View() from sidebar with height set")
	}
}

func TestSidebarFillsRequestedHeight(t *testing.T) {
	m := views.NewSidebarModel(makeSidebarStyles())
	m.SetHeight(20)

	lines := strings.Split(stripANSI(m.View()), "\n")
	if got := len(lines); got != 20 {
		t.Fatalf("sidebar line count = %d, want 20", got)
	}
}

func TestSidebarDoesNotRenderGlobalFooterHints(t *testing.T) {
	m := views.NewSidebarModel(makeSidebarStyles())
	m.SetHeight(20)
	m.SetEntries(makeSessions(1))

	out := stripANSI(m.View())
	if strings.Contains(out, "[n] New") || strings.Contains(out, "[q] Quit") {
		t.Fatalf("sidebar should not duplicate global footer hints: %q", out)
	}
}

func TestSidebarHeaderCentersSessionsTitle(t *testing.T) {
	m := views.NewSidebarModel(makeSidebarStyles())
	m.SetHeight(20)

	lines := strings.Split(stripANSI(m.View()), "\n")
	if len(lines) == 0 {
		t.Fatal("expected sidebar view to include a header line")
	}

	header := trimSidebarBorder(lines[0])
	const title = "Sessions"
	wantStart := (views.SidebarWidth - lipgloss.Width(title)) / 2
	if got := headerStart(header, title); got != wantStart {
		t.Fatalf("expected %q header to start at column %d, got %d in %q", title, wantStart, got, header)
	}
}

func TestSidebarUsesDisplayWidthForStyledWorkItemPrefix(t *testing.T) {
	t.Parallel()

	m := views.NewSidebarModel(makeSidebarStyles())
	m.SetWidth(12)
	m.SetHeight(8)
	m.SetEntries([]views.SidebarEntry{{
		Kind:       views.SidebarEntryWorkItem,
		WorkItemID: "wi-1",
		ExternalID: "SUB-123",
		Title:      "Short title",
		State:      domain.SessionImplementing,
	}})

	lines := strings.Split(m.View(), "\n")
	if len(lines) < 3 {
		t.Fatalf("sidebar lines = %d, want at least 3", len(lines))
	}

	prefixLine := strings.TrimRight(ansi.Strip(lines[2]), " ")
	if got := ansi.StringWidth(lines[2]); got > 12 {
		t.Fatalf("prefix line width = %d, want <= 12\nline: %q", got, lines[2])
	}
	if prefixLine != "● SUB-123" {
		t.Fatalf("prefix line = %q, want full visible prefix without premature ellipsis", prefixLine)
	}
}

func TestSidebarNavigation(t *testing.T) {
	m := views.NewSidebarModel(makeSidebarStyles())
	m.SetHeight(40)
	sessions := makeSessions(3)
	m.SetEntries(sessions)

	if sel := m.Selected(); sel != nil {
		t.Fatalf("expected no selected item before navigation, got %v", sel)
	}

	// MoveDown from no selection -> first.
	m.MoveDown()
	sel := m.Selected()
	if sel == nil || sel.WorkItemID != sessions[0].WorkItemID {
		t.Fatalf("after MoveDown from empty selection expected first session, got %v", sel)
	}

	// MoveDown -> second.
	m.MoveDown()
	sel = m.Selected()
	if sel == nil || sel.WorkItemID != sessions[1].WorkItemID {
		t.Fatalf("after second MoveDown expected second session, got %v", sel)
	}

	// MoveDown -> third.
	m.MoveDown()
	sel = m.Selected()
	if sel == nil || sel.WorkItemID != sessions[2].WorkItemID {
		t.Fatalf("after third MoveDown expected third session, got %v", sel)
	}

	// MoveDown at end -> still third (no wrap).
	m.MoveDown()
	sel = m.Selected()
	if sel == nil || sel.WorkItemID != sessions[2].WorkItemID {
		t.Fatalf("MoveDown past end should stay at last, got %v", sel)
	}

	// MoveUp -> second.
	m.MoveUp()
	sel = m.Selected()
	if sel == nil || sel.WorkItemID != sessions[1].WorkItemID {
		t.Fatalf("after MoveUp expected second session, got %v", sel)
	}
}

func TestSidebarMoveUpFromUnselectedJumpsToBottom(t *testing.T) {
	m := views.NewSidebarModel(makeSidebarStyles())
	m.SetHeight(20)
	sessions := makeSessions(2)
	m.SetEntries(sessions)

	m.MoveUp()
	sel := m.Selected()
	if sel == nil {
		t.Fatal("expected selected item after MoveUp from empty selection")
	}
	if sel.WorkItemID != sessions[len(sessions)-1].WorkItemID {
		t.Fatalf("MoveUp from empty selection should choose last item, got %q", sel.WorkItemID)
	}
}

func TestSidebarSingleSession(t *testing.T) {
	m := views.NewSidebarModel(makeSidebarStyles())
	m.SetHeight(20)
	sessions := makeSessions(1)
	m.SetEntries(sessions)

	m.MoveDown()
	sel := m.Selected()
	if sel == nil || sel.WorkItemID != sessions[0].WorkItemID {
		t.Fatalf("MoveDown with single session should stay at only item, got %v", sel)
	}

	m.MoveUp()
	sel = m.Selected()
	if sel == nil || sel.WorkItemID != sessions[0].WorkItemID {
		t.Fatalf("MoveUp with single session should stay at only item, got %v", sel)
	}
}

func TestSidebarSelected_Empty(t *testing.T) {
	m := views.NewSidebarModel(makeSidebarStyles())
	m.SetHeight(20)
	// No sessions set
	sel := m.Selected()
	if sel != nil {
		t.Fatalf("expected nil Selected() with no sessions, got %+v", sel)
	}
}

func TestSidebarClipsOverflowingEntriesToRequestedHeight(t *testing.T) {
	t.Parallel()

	m := views.NewSidebarModel(makeSidebarStyles())
	m.SetHeight(10)
	m.SetEntries(makeSessions(5))
	m.GotoBottom()

	view := stripANSI(m.View())
	lines := strings.Split(view, "\n")
	if got := len(lines); got != 10 {
		t.Fatalf("sidebar line count = %d, want 10", got)
	}
	if !strings.Contains(view, "Session E") {
		t.Fatalf("view = %q, want bottom-most selected entry visible", view)
	}
	if strings.Contains(view, "Session C") {
		t.Fatalf("view = %q, want clipped middle entries removed at full-entry boundaries", view)
	}
}

func TestSidebarNavigationSkipsGroupHeaders(t *testing.T) {
	m := views.NewSidebarModel(makeSidebarStyles())
	m.SetHeight(20)
	m.SetEntries([]views.SidebarEntry{
		{Kind: views.SidebarEntryTaskOverview, WorkItemID: "wi-1"},
		{Kind: views.SidebarEntryGroupHeader, GroupTitle: "Planning"},
		{Kind: views.SidebarEntryTaskSession, WorkItemID: "wi-1", SessionID: "s1"},
		{Kind: views.SidebarEntryTaskSession, WorkItemID: "wi-1", SessionID: "s2"},
	})

	m.MoveDown()
	// First MoveDown from no selection lands on overview (first selectable).
	if sel := m.Selected(); sel == nil || sel.Kind != views.SidebarEntryTaskOverview {
		t.Fatalf("first MoveDown should land on overview, got %v", sel)
	}

	// MoveDown should skip the group header and land on first session.
	m.MoveDown()
	sel := m.Selected()
	if sel == nil || sel.SessionID != "s1" {
		t.Fatalf("MoveDown from overview should land on s1, got %v", sel)
	}

	// MoveDown again should land on s2 (not a group header).
	m.MoveDown()
	sel = m.Selected()
	if sel == nil || sel.SessionID != "s2" {
		t.Fatalf("MoveDown should land on s2, got %v", sel)
	}

	// MoveUp from s2 should go back to s1.
	m.MoveUp()
	sel = m.Selected()
	if sel == nil || sel.SessionID != "s1" {
		t.Fatalf("MoveUp should land on s1, got %v", sel)
	}

	// MoveUp from s1 should go to overview (skipping group header).
	m.MoveUp()
	sel = m.Selected()
	if sel == nil || sel.Kind != views.SidebarEntryTaskOverview {
		t.Fatalf("MoveUp should land on overview, got %v", sel)
	}
}

func TestSidebarGroupHeaderRendersCorrectly(t *testing.T) {
	t.Parallel()

	m := views.NewSidebarModel(makeSidebarStyles())
	m.SetHeight(20)
	m.SetEntries([]views.SidebarEntry{
		{Kind: views.SidebarEntryGroupHeader, GroupTitle: "Planning"},
	})

	view := stripANSI(m.View())
	if !strings.Contains(view, "Planning") {
		t.Fatalf("view should contain group title \"Planning\", got %q", view)
	}
	if !strings.Contains(view, "─") {
		t.Fatalf("view should contain divider characters, got %q", view)
	}
}

func TestSidebarMoveDownStaysWhenNothingPastGroupHeader(t *testing.T) {
	m := views.NewSidebarModel(makeSidebarStyles())
	m.SetHeight(20)
	m.SetEntries([]views.SidebarEntry{
		{Kind: views.SidebarEntryTaskOverview, WorkItemID: "wi-1"},
		{Kind: views.SidebarEntryGroupHeader, GroupTitle: "Planning"},
	})

	// MoveDown twice: lands on overview, then cannot skip past group header to find next selectable.
	m.MoveDown()
	m.MoveDown()
	sel := m.Selected()
	if sel == nil || sel.Kind != views.SidebarEntryTaskOverview {
		t.Fatalf("cursor should stay on overview when no selectable entry after group header, got %v", sel)
	}
}

func TestSidebarGotoTopBottomSkipGroupHeaders(t *testing.T) {
	m := views.NewSidebarModel(makeSidebarStyles())
	m.SetHeight(20)
	m.SetEntries([]views.SidebarEntry{
		{Kind: views.SidebarEntryGroupHeader, GroupTitle: "Planning"},
		{Kind: views.SidebarEntryTaskSession, WorkItemID: "wi-1", SessionID: "s1"},
		{Kind: views.SidebarEntryGroupHeader, GroupTitle: "Foreman"},
		{Kind: views.SidebarEntryTaskSession, WorkItemID: "wi-1", SessionID: "s2"},
	})

	m.GotoTop()
	if sel := m.Selected(); sel == nil || sel.SessionID != "s1" {
		t.Fatalf("GotoTop should skip group header to s1, got %v", sel)
	}

	m.GotoBottom()
	if sel := m.Selected(); sel == nil || sel.SessionID != "s2" {
		t.Fatalf("GotoBottom should skip group header to s2, got %v", sel)
	}
}

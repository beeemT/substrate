package views_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/tui/styles"
	"github.com/beeemT/substrate/internal/tui/views"
	"github.com/charmbracelet/lipgloss"
)

func makeSidebarStyles() styles.Styles {
	return styles.NewStyles(styles.DefaultTheme)
}

var ansiEscapePattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string {
	return ansiEscapePattern.ReplaceAllString(s, "")
}

func trimSidebarBorder(line string) string {
	if strings.HasSuffix(line, "│") {
		return strings.TrimSuffix(line, "│")
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
			State:      domain.WorkItemIngested,
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

func TestSidebarNavigation(t *testing.T) {
	m := views.NewSidebarModel(makeSidebarStyles())
	m.SetHeight(40)
	sessions := makeSessions(3)
	m.SetEntries(sessions)

	// Default: first item selected
	sel := m.Selected()
	if sel == nil {
		t.Fatal("expected selected item, got nil")
	}
	if sel.WorkItemID != sessions[0].WorkItemID {
		t.Fatalf("expected first session, got %q", sel.WorkItemID)
	}

	// MoveDown -> second
	m.MoveDown()
	sel = m.Selected()
	if sel == nil || sel.WorkItemID != sessions[1].WorkItemID {
		t.Fatalf("after MoveDown expected second session, got %v", sel)
	}

	// MoveDown -> third
	m.MoveDown()
	sel = m.Selected()
	if sel == nil || sel.WorkItemID != sessions[2].WorkItemID {
		t.Fatalf("after second MoveDown expected third session, got %v", sel)
	}

	// MoveDown at end -> still third (no wrap)
	m.MoveDown()
	sel = m.Selected()
	if sel == nil || sel.WorkItemID != sessions[2].WorkItemID {
		t.Fatalf("MoveDown past end should stay at last, got %v", sel)
	}

	// MoveUp -> second
	m.MoveUp()
	sel = m.Selected()
	if sel == nil || sel.WorkItemID != sessions[1].WorkItemID {
		t.Fatalf("after MoveUp expected second session, got %v", sel)
	}
}

func TestSidebarMoveUpAtTop(t *testing.T) {
	m := views.NewSidebarModel(makeSidebarStyles())
	m.SetHeight(20)
	sessions := makeSessions(2)
	m.SetEntries(sessions)

	// Already at index 0; MoveUp should stay at 0
	m.MoveUp()
	sel := m.Selected()
	if sel == nil {
		t.Fatal("expected selected item, got nil")
	}
	if sel.WorkItemID != sessions[0].WorkItemID {
		t.Fatalf("MoveUp at top should keep first item selected, got %q", sel.WorkItemID)
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

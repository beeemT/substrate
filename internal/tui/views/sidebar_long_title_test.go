package views_test

import (
	"strings"
	"testing"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/tui/views"
	"github.com/charmbracelet/x/ansi"
)

// taskSessionEntries returns a deterministic set of entries that resembles the
// tasks sidebar: an overview, a planning session, a group header, and several
// implementation sessions under a repo. Total rendered height is
// 1 (overview) + 2 (planning) + 2 (group header) + 3*4 (impl sessions) = 17 rows
// before any header.
func taskSessionEntries() []views.SidebarEntry {
	return []views.SidebarEntry{
		{Kind: views.SidebarEntryTaskOverview, WorkItemID: "wi-1", Title: "Overview", State: domain.SessionImplementing},
		{Kind: views.SidebarEntryGroupHeader, WorkItemID: "wi-1", GroupTitle: "Planning"},
		{Kind: views.SidebarEntryTaskSession, WorkItemID: "wi-1", SessionID: "plan-1", Title: "Plan sess-1", RepositoryName: "Planning"},
		{Kind: views.SidebarEntryGroupHeader, WorkItemID: "wi-1", GroupTitle: "acme/rocket"},
		{Kind: views.SidebarEntryTaskSession, WorkItemID: "wi-1", SessionID: "impl-1", Title: "Implementation sess-1", RepositoryName: "acme/rocket", State: domain.SessionImplementing},
		{Kind: views.SidebarEntryTaskSession, WorkItemID: "wi-1", SessionID: "impl-2", Title: "Implementation sess-2", RepositoryName: "acme/rocket", State: domain.SessionImplementing},
		{Kind: views.SidebarEntryTaskSession, WorkItemID: "wi-1", SessionID: "impl-3", Title: "Implementation sess-3", RepositoryName: "acme/rocket", State: domain.SessionImplementing},
		{Kind: views.SidebarEntryTaskSession, WorkItemID: "wi-1", SessionID: "impl-4", Title: "Implementation sess-4", RepositoryName: "acme/rocket", State: domain.SessionImplementing},
	}
}

// TestSidebarLongTitleHeaderStaysWithinRequestedHeight is the regression for the
// reported bug: a long sidebar title wrapped to multiple lines, and the hard-coded
// "title + divider = 2 rows" assumption caused the focused entry to be clipped
// out of bounds. The rendered pane must remain exactly the requested height
// regardless of how many lines the title wraps to.
func TestSidebarLongTitleHeaderStaysWithinRequestedHeight(t *testing.T) {
	t.Parallel()

	const height = 20
	const width = 30
	m := views.NewSidebarModel(makeSidebarStyles())
	m.SetWidth(width)
	m.SetHeight(height)
	m.SetTitle("Investigate flaky retry loop in the implementation harness that produces empty PRs after backoff exhaustion")
	m.SetEntries(taskSessionEntries())
	// Focus the last entry; the bug caused this entry to be clipped.
	m.GotoBottom()

	plain := stripANSI(m.View())
	lines := strings.Split(plain, "\n")
	if got := len(lines); got != height {
		t.Fatalf("sidebar line count = %d, want %d", got, height)
	}
	// The focused entry's prefix must appear somewhere in the rendered output.
	if !strings.Contains(plain, "Implementation sess-4") {
		t.Fatalf("focused entry not visible with long title; rendered:\n%s", plain)
	}
	// No line may exceed the rendered pane width.
	for i, line := range lines {
		if w := ansi.StringWidth(line); w > width {
			t.Fatalf("line %d width = %d, want <= %d\nline: %q", i+1, w, width, line)
		}
	}
}

// TestSidebarVeryLongTitleCappedAtFiveLines verifies the cap: a title that would
// otherwise wrap to 10+ lines is truncated to at most maxSidebarTitleLines=5
// wrapped lines, so a pathological work-item title cannot consume the entire pane.
func TestSidebarVeryLongTitleCappedAtFiveLines(t *testing.T) {
	t.Parallel()

	const height = 20
	const width = 30
	// Title is long enough to wrap to 8+ lines at width=30.
	title := strings.Repeat("lorem ipsum dolor sit amet ", 12) + " \u00b7 Tasks"
	m := views.NewSidebarModel(makeSidebarStyles())
	m.SetWidth(width)
	m.SetHeight(height)
	m.SetTitle(title)
	m.SetEntries(taskSessionEntries())
	m.GotoBottom()

	plain := stripANSI(m.View())
	lines := strings.Split(plain, "\n")
	if got := len(lines); got != height {
		t.Fatalf("sidebar line count = %d, want %d", got, height)
	}
	// The divider marks the boundary between the (truncated) header and the
	// entries. With the cap, the divider must appear within the first
	// maxSidebarTitleLines+1 = 6 lines.
	dividerLine := dividerRow(t, m)
	if dividerLine < 0 {
		t.Fatalf("divider not found in rendered output:\n%s", plain)
	}
	const maxTitleLines = 5
	if dividerLine >= maxTitleLines+1 {
		t.Fatalf("divider at line %d, want < %d (title was not capped at %d lines)",
			dividerLine, maxTitleLines+1, maxTitleLines)
	}
	// Focused entry is still visible.
	if !strings.Contains(plain, "Implementation sess-4") {
		t.Fatalf("focused entry not visible; rendered:\n%s", plain)
	}
}

// TestSidebarHeaderHeightIsDynamic verifies that the header height responds to
// a width that forces the title to wrap. At width 200 the same title fits in
// one line; at width 20 it wraps. The divider line marks the end of the
// header, so the divider's row index equals the wrapped title's line count.
func TestSidebarHeaderHeightIsDynamic(t *testing.T) {
	t.Parallel()

	m := views.NewSidebarModel(makeSidebarStyles())
	m.SetTitle("Investigate flaky retry loop in the implementation harness that produces empty PRs")
	m.SetEntries(taskSessionEntries())

	// Single-line at wide width.
	m.SetWidth(200)
	m.SetHeight(20)
	if got := dividerRow(t, m); got != 1 {
		t.Fatalf("divider at row %d, want 1 for single-line title at width 200", got)
	}

	// Now narrow the width to force wrapping.
	m.SetWidth(20)
	if got := dividerRow(t, m); got < 2 {
		t.Fatalf("divider at row %d, want >= 2 for wrapped title at width 20", got)
	}
}

// dividerRow returns the 0-based row index of the first non-empty divider line
// in the rendered sidebar. If no divider is found, returns -1.
func dividerRow(t *testing.T, m views.SidebarModel) int {
	t.Helper()
	plain := stripANSI(m.View())
	lines := strings.Split(plain, "\n")
	for i, line := range lines {
		if strings.Contains(line, "─") && strings.Trim(line, " ") != "" {
			return i
		}
	}
	return -1
}

// TestSidebarEnsureCursorVisibleWithLongTitle verifies that navigating to the
// last entry keeps it visible even when the title wraps.
func TestSidebarEnsureCursorVisibleWithLongTitle(t *testing.T) {
	t.Parallel()

	m := views.NewSidebarModel(makeSidebarStyles())
	m.SetWidth(30)
	m.SetHeight(15)
	m.SetTitle("Investigate flaky retry loop in the implementation harness that produces empty PRs after backoff exhaustion")
	m.SetEntries(taskSessionEntries())

	// GotoBottom selects the last entry. The render must keep it visible.
	m.GotoBottom()

	rendered := stripANSI(m.View())
	if !strings.Contains(rendered, "Implementation sess-4") {
		t.Fatalf("last entry not visible after GotoBottom with long title; rendered:\n%s", rendered)
	}
	lines := strings.Split(rendered, "\n")
	if got := len(lines); got != 15 {
		t.Fatalf("sidebar line count = %d, want 15", got)
	}
}

// TestSidebarTitleChangeInvalidatesHeader verifies that calling SetTitle with
// a longer title after a render causes the next render to recompute the
// header height. The divider must move from row 1 (short title) to row >= 2
// (long title) and the focused last entry must remain visible.
func TestSidebarTitleChangeInvalidatesHeader(t *testing.T) {
	t.Parallel()

	m := views.NewSidebarModel(makeSidebarStyles())
	m.SetWidth(30)
	m.SetHeight(15)
	m.SetTitle("Short")
	m.SetEntries(taskSessionEntries())
	m.GotoBottom()
	_ = m.View() // prime the cache

	dividerAt := dividerRow(t, m)
	if dividerAt != 1 {
		t.Fatalf("divider at row %d with short title, want 1", dividerAt)
	}

	// Now change to a long title.
	m.SetTitle("Investigate flaky retry loop in the implementation harness that produces empty PRs after backoff exhaustion")
	dividerAt = dividerRow(t, m)
	if dividerAt < 2 {
		t.Fatalf("divider at row %d with long title, want >= 2", dividerAt)
	}
	plain := stripANSI(m.View())
	if !strings.Contains(plain, "Implementation sess-4") {
		t.Fatalf("focused entry not visible after title change; rendered:\n%s", plain)
	}
	if got := len(strings.Split(plain, "\n")); got != 15 {
		t.Fatalf("sidebar line count = %d, want 15", got)
	}
}

// TestSidebarShortTitleUnaffected ensures the default behavior with a short
// title is preserved: header is exactly 2 rows (1 title + 1 divider).
func TestSidebarShortTitleUnaffected(t *testing.T) {
	t.Parallel()

	m := views.NewSidebarModel(makeSidebarStyles())
	m.SetWidth(34)
	m.SetHeight(20)
	m.SetTitle("SUB-1 \u00b7 Tasks")
	m.SetEntries(taskSessionEntries())

	plain := stripANSI(m.View())
	lines := strings.Split(plain, "\n")
	if got := dividerRow(t, m); got != 1 {
		t.Fatalf("divider at row %d, want 1 for short title", got)
	}
	if got := len(lines); got != 20 {
		t.Fatalf("sidebar line count = %d, want 20", got)
	}
}

func TestSidebarRerendersAfterSelectionMoveWithCachedHeader(t *testing.T) {
	t.Parallel()

	m := views.NewSidebarModel(makeSidebarStyles())
	m.SetWidth(30)
	m.SetHeight(15)
	m.SetTitle("Investigate flaky retry loop in the implementation harness that produces empty PRs")
	m.SetEntries(taskSessionEntries())
	m.MoveDown()
	first := m.View()

	m.MoveDown()
	second := m.View()

	if first == second {
		t.Fatal("sidebar view did not change after moving the selection")
	}
}

func TestSidebarRerendersAfterEntriesChangeWithCachedHeader(t *testing.T) {
	t.Parallel()

	m := views.NewSidebarModel(makeSidebarStyles())
	m.SetWidth(30)
	m.SetHeight(15)
	m.SetTitle("Investigate flaky retry loop in the implementation harness that produces empty PRs")
	m.SetEntries(taskSessionEntries())
	before := stripANSI(m.View())

	entries := taskSessionEntries()
	entries[0].Title = "Changed overview title"
	m.SetEntries(entries)
	after := stripANSI(m.View())

	if before == after {
		t.Fatal("sidebar view did not change after replacing entries")
	}
	if !strings.Contains(after, "Changed overview title") {
		t.Fatalf("updated entry title not rendered:\n%s", after)
	}
}

func TestSidebarRerendersAfterStatusLabelChangeWithCachedHeader(t *testing.T) {
	t.Parallel()

	m := views.NewSidebarModel(makeSidebarStyles())
	m.SetWidth(30)
	m.SetHeight(15)
	m.SetTitle("Investigate flaky retry loop in the implementation harness that produces empty PRs")
	m.SetEntries(taskSessionEntries())
	before := stripANSI(m.View())

	m.CycleFilter()
	after := stripANSI(m.View())

	if before == after {
		t.Fatal("sidebar view did not change after changing the status label")
	}
	if !strings.Contains(after, "active") {
		t.Fatalf("updated status label not rendered:\n%s", after)
	}
}

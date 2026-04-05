package views_test

import (
	"regexp"
	"strings"
	"testing"
	"time"

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

func TestSidebarNoScrollbarWhenAllEntriesFit(t *testing.T) {
	t.Parallel()

	m := views.NewSidebarModel(makeSidebarStyles())
	m.SetHeight(20)
	m.SetEntries(makeSessions(2)) // 2*4=8 rows content + 2 header = 10 < 20

	view := stripANSI(m.View())
	// Scrollbar uses ▏ and ▐ characters which should NOT appear when no overflow.
	if strings.Contains(view, "▏") || strings.Contains(view, "▐") {
		t.Fatalf("scrollbar should not appear when all entries fit: %q", view)
	}
}

func TestSidebarScrollbarAppearsOnOverflow(t *testing.T) {
	t.Parallel()

	m := views.NewSidebarModel(makeSidebarStyles())
	m.SetHeight(10)
	m.SetEntries(makeSessions(5)) // 5*4=20 rows content, available=8, overflows
	m.GotoBottom()

	view := m.View() // raw view with ANSI for scrollbar styling
	// The scrollbar track character must be present.
	if !strings.Contains(view, "▏") {
		t.Fatalf("expected scrollbar track character in overflow view: %q", stripANSI(view))
	}
	// The scrollbar thumb character must be present.
	if !strings.Contains(view, "▐") {
		t.Fatalf("expected scrollbar thumb character in overflow view: %q", stripANSI(view))
	}
	// Output height must still equal the requested height.
	lines := strings.Split(stripANSI(view), "\n")
	if got := len(lines); got != 10 {
		t.Fatalf("sidebar line count = %d, want 10", got)
	}
}

func TestSidebarScrollbarThumbMovesWithScroll(t *testing.T) {
	t.Parallel()

	m := views.NewSidebarModel(makeSidebarStyles())
	m.SetHeight(10)
	m.SetEntries(makeSessions(5))

	// Scroll to top: select first entry.
	m.GotoTop()
	viewTop := stripANSI(m.View())
	// Find thumb position (column with ▐).
	thumbTopLine := thumbLineIndex(viewTop)
	// Scroll to bottom.
	m.GotoBottom()
	viewBottom := stripANSI(m.View())
	thumbBottomLine := thumbLineIndex(viewBottom)

	if thumbTopLine >= thumbBottomLine {
		t.Fatalf("thumb should move down when scrolling: top=%d, bottom=%d", thumbTopLine, thumbBottomLine)
	}
}

func thumbLineIndex(view string) int {
	lines := strings.Split(view, "\n")
	for i, line := range lines {
		if strings.Contains(line, "▐") {
			return i
		}
	}
	return -1
}

// Filter / Dimension / Direction tests

func makeWorkItemEntry(id, extID string, state domain.SessionState, source string, lastActivity, createdAt time.Time) views.SidebarEntry {
	return views.SidebarEntry{
		Kind:         views.SidebarEntryWorkItem,
		WorkItemID:   id,
		ExternalID:   extID,
		Source:       source,
		Title:        "Session " + id,
		State:        state,
		LastActivity: lastActivity,
		CreatedAt:    createdAt,
	}
}

func TestFilterSidebarEntries(t *testing.T) {
	now := time.Now()
	entries := []views.SidebarEntry{
		makeWorkItemEntry("a", "gh:issue:1", domain.SessionPlanning, "github", now, now),
		makeWorkItemEntry("b", "gh:issue:2", domain.SessionCompleted, "github", now, now),
		makeWorkItemEntry("c", "gl:issue:1", domain.SessionFailed, "gitlab", now, now),
		makeWorkItemEntry("d", "gh:issue:3", domain.SessionImplementing, "github", now, now),
		makeWorkItemEntry("e", "gh:issue:4", domain.SessionPlanReview, "github", now, now),
		makeWorkItemEntry("f", "gh:issue:5", domain.SessionIngested, "github", now, now),
	}

	t.Run("all", func(t *testing.T) {
		got := views.FilterSidebarEntries(entries, views.SidebarFilterAll)
		if len(got) != len(entries) {
			t.Fatalf("expected %d entries, got %d", len(entries), len(got))
		}
	})

	t.Run("active", func(t *testing.T) {
		got := views.FilterSidebarEntries(entries, views.SidebarFilterActive)
		if len(got) != 3 {
			t.Fatalf("expected 3 active entries, got %d", len(got))
		}
		for _, e := range got {
			if e.Kind != views.SidebarEntryWorkItem {
				continue
			}
			switch e.State {
			case domain.SessionPlanning, domain.SessionPlanReview, domain.SessionImplementing, domain.SessionReviewing:
			default:
				t.Fatalf("unexpected state in active filter: %s", e.State)
			}
		}
	})

	t.Run("needs_attention", func(t *testing.T) {
		// PlanReview entry only.
		entries2 := []views.SidebarEntry{
			makeWorkItemEntry("a", "gh:issue:1", domain.SessionPlanning, "github", now, now),
			makeWorkItemEntry("e", "gh:issue:4", domain.SessionPlanReview, "github", now, now),
			{Kind: views.SidebarEntryWorkItem, WorkItemID: "h", State: domain.SessionImplementing, HasOpenQuestion: true, LastActivity: now, CreatedAt: now},
			{Kind: views.SidebarEntryWorkItem, WorkItemID: "i", State: domain.SessionImplementing, HasInterrupted: true, LastActivity: now, CreatedAt: now},
		}
		got := views.FilterSidebarEntries(entries2, views.SidebarFilterNeedsAttention)
		if len(got) != 3 {
			t.Fatalf("expected 3 attention entries, got %d", len(got))
		}
	})

	t.Run("completed", func(t *testing.T) {
		got := views.FilterSidebarEntries(entries, views.SidebarFilterCompleted)
		if len(got) != 2 {
			t.Fatalf("expected 2 completed entries, got %d", len(got))
		}
		for _, e := range got {
			if e.Kind != views.SidebarEntryWorkItem {
				continue
			}
			if e.State != domain.SessionCompleted && e.State != domain.SessionFailed {
				t.Fatalf("unexpected state in completed filter: %s", e.State)
			}
		}
	})

	t.Run("passes through non-work-item entries", func(t *testing.T) {
		entries3 := []views.SidebarEntry{
			{Kind: views.SidebarEntryTaskOverview, WorkItemID: "wi-1"},
			makeWorkItemEntry("a", "gh:issue:1", domain.SessionCompleted, "github", now, now),
		}
		got := views.FilterSidebarEntries(entries3, views.SidebarFilterActive)
		if len(got) != 1 {
			t.Fatalf("expected 1 entry (overview passthrough), got %d", len(got))
		}
		if got[0].Kind != views.SidebarEntryTaskOverview {
			t.Fatalf("expected TaskOverview entry to pass through")
		}
	})
}

func TestApplyDimensionAndDirection_None(t *testing.T) {
	now := time.Now()
	entries := []views.SidebarEntry{
		makeWorkItemEntry("b", "gh:issue:2", domain.SessionCompleted, "github", now.Add(-1*time.Hour), now),
		makeWorkItemEntry("a", "gh:issue:1", domain.SessionPlanning, "github", now, now),
	}

	got := views.ApplyDimensionAndDirection(entries, views.SidebarDimNone, views.SidebarDirDesc)
	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(got))
	}
	// Should be sorted by activity desc (a before b).
	if got[0].WorkItemID != "a" {
		t.Fatalf("expected 'a' first (most recent activity), got %q", got[0].WorkItemID)
	}
}

func TestApplyDimensionAndDirection_State(t *testing.T) {
	now := time.Now()
	entries := []views.SidebarEntry{
		makeWorkItemEntry("c", "gh:issue:3", domain.SessionCompleted, "github", now, now),
		makeWorkItemEntry("a", "gh:issue:1", domain.SessionPlanning, "github", now, now),
		makeWorkItemEntry("b", "gh:issue:2", domain.SessionFailed, "github", now, now),
	}

	got := views.ApplyDimensionAndDirection(entries, views.SidebarDimState, views.SidebarDirDesc)
	// Should have group headers + entries. Active first (a), then Completed (c), then Failed (b).
	// Find the group headers.
	var groups []string
	for _, e := range got {
		if e.Kind == views.SidebarEntryGroupHeader {
			groups = append(groups, e.GroupTitle)
		}
	}
	if len(groups) != 3 {
		t.Fatalf("expected 3 group headers, got %d: %v", len(groups), groups)
	}
	if !strings.HasPrefix(groups[0], "Active") {
		t.Fatalf("expected first group to be Active, got %q", groups[0])
	}
	if !strings.HasPrefix(groups[1], "Completed") {
		t.Fatalf("expected second group to be Completed, got %q", groups[1])
	}
	if !strings.HasPrefix(groups[2], "Failed") {
		t.Fatalf("expected third group to be Failed, got %q", groups[2])
	}
}

func TestApplyDimensionAndDirection_State_Asc(t *testing.T) {
	now := time.Now()
	entries := []views.SidebarEntry{
		makeWorkItemEntry("a", "gh:issue:1", domain.SessionPlanning, "github", now, now),
		makeWorkItemEntry("b", "gh:issue:2", domain.SessionFailed, "github", now, now),
		makeWorkItemEntry("c", "gh:issue:3", domain.SessionCompleted, "github", now, now),
	}

	got := views.ApplyDimensionAndDirection(entries, views.SidebarDimState, views.SidebarDirAsc)
	var groups []string
	for _, e := range got {
		if e.Kind == views.SidebarEntryGroupHeader {
			groups = append(groups, e.GroupTitle)
		}
	}
	if len(groups) != 3 {
		t.Fatalf("expected 3 group headers, got %d", len(groups))
	}
	// Asc reverses the natural order (Active, Review, Waiting, Completed, Failed).
	// With Review and Waiting empty: Failed, Completed, Active.
	if !strings.HasPrefix(groups[0], "Failed") {
		t.Fatalf("expected first group to be Failed in asc, got %q", groups[0])
	}
	if !strings.HasPrefix(groups[1], "Completed") {
		t.Fatalf("expected second group to be Completed in asc, got %q", groups[1])
	}
	if !strings.HasPrefix(groups[2], "Active") {
		t.Fatalf("expected last group to be Active in asc, got %q", groups[2])
	}
}

func TestApplyDimensionAndDirection_Source(t *testing.T) {
	now := time.Now()
	entries := []views.SidebarEntry{
		makeWorkItemEntry("c", "gl:issue:1", domain.SessionPlanning, "gitlab", now, now),
		makeWorkItemEntry("a", "gh:issue:1", domain.SessionPlanning, "github", now, now),
		makeWorkItemEntry("b", "gh:issue:2", domain.SessionPlanning, "github", now, now),
	}

	got := views.ApplyDimensionAndDirection(entries, views.SidebarDimSource, views.SidebarDirDesc)
	var groups []string
	for _, e := range got {
		if e.Kind == views.SidebarEntryGroupHeader {
			groups = append(groups, e.GroupTitle)
		}
	}
	if len(groups) != 2 {
		t.Fatalf("expected 2 group headers, got %d: %v", len(groups), groups)
	}
	// Desc: largest group first (GitHub has 2, GitLab has 1).
	if !strings.HasPrefix(groups[0], "GitHub") {
		t.Fatalf("expected first group to be GitHub (largest), got %q", groups[0])
	}
	if !strings.HasPrefix(groups[1], "GitLab") {
		t.Fatalf("expected second group to be GitLab, got %q", groups[1])
	}
}

func TestApplyDimensionAndDirection_EmptyGroupsHidden(t *testing.T) {
	now := time.Now()
	entries := []views.SidebarEntry{
		makeWorkItemEntry("a", "gh:issue:1", domain.SessionPlanning, "github", now, now),
	}

	got := views.ApplyDimensionAndDirection(entries, views.SidebarDimState, views.SidebarDirDesc)
	var groups []string
	for _, e := range got {
		if e.Kind == views.SidebarEntryGroupHeader {
			groups = append(groups, e.GroupTitle)
		}
	}
	if len(groups) != 1 {
		t.Fatalf("expected 1 group header (Active only), got %d: %v", len(groups), groups)
	}
	if !strings.HasPrefix(groups[0], "Active") {
		t.Fatalf("expected group to be Active, got %q", groups[0])
	}
}

func TestApplyDimensionAndDirection_State_ReviewGroup(t *testing.T) {
	now := time.Now()
	entries := []views.SidebarEntry{
		makeWorkItemEntry("a", "gh:issue:1", domain.SessionPlanning, "github", now, now),
		makeWorkItemEntry("b", "gh:issue:2", domain.SessionPlanReview, "github", now, now),
		makeWorkItemEntry("c", "gh:issue:3", domain.SessionReviewing, "github", now, now),
		makeWorkItemEntry("d", "gh:issue:4", domain.SessionCompleted, "github", now, now),
	}

	got := views.ApplyDimensionAndDirection(entries, views.SidebarDimState, views.SidebarDirDesc)
	var groups []string
	for _, e := range got {
		if e.Kind == views.SidebarEntryGroupHeader {
			groups = append(groups, e.GroupTitle)
		}
	}
	if len(groups) != 3 {
		t.Fatalf("expected 3 group headers (Active, Review, Completed), got %d: %v", len(groups), groups)
	}
	if !strings.HasPrefix(groups[0], "Active") {
		t.Fatalf("expected first group to be Active, got %q", groups[0])
	}
	if !strings.HasPrefix(groups[1], "Review") {
		t.Fatalf("expected second group to be Review, got %q", groups[1])
	}
	if !strings.HasPrefix(groups[2], "Completed") {
		t.Fatalf("expected third group to be Completed, got %q", groups[2])
	}
}

func TestTimeBucket(t *testing.T) {
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, now.Location())
	yesterday := today.Add(-24 * time.Hour)
	// Mirror the implementation's formula to find Monday of the current week.
	mondayOffset := (int(now.Weekday()) - int(time.Monday) + 7) % 7
	thisWeekMonday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, -mondayOffset)
	// A "This Week" test date must be >= Monday midnight and < yesterday midnight.
	// That range is empty when today is Monday (Monday==today) or Tuesday (Monday==yesterday).
	thisWeekDate := thisWeekMonday.Add(12 * time.Hour) // noon on Monday
	thisWeekValid := thisWeekDate.Before(yesterday)

	var thisMonthCandidate time.Time
	var thisMonthValid bool
	if thisWeekValid {
		// For "this month": need a date after Monday-of-next-week (so it falls past thisWeek)
		// but still in this month and before today.
		nextMonday := thisWeekMonday.AddDate(0, 0, 7)
		tmp := nextMonday.Add(12 * time.Hour)
		thisMonthValid = tmp.Before(today) && tmp.Month() == now.Month()
		thisMonthCandidate = tmp
	} else {
		tmp := thisWeekMonday.Add(12*time.Hour).AddDate(0, 0, 7)
		thisMonthValid = tmp.Before(today) && tmp.Month() == now.Month()
		thisMonthCandidate = tmp
	}
	twoMonthsAgo := time.Date(now.Year(), now.Month()-2, 15, 12, 0, 0, 0, now.Location())
	veryOld := time.Date(2020, 1, 1, 0, 0, 0, 0, now.Location())

	tests := []struct {
		name   string
		t      time.Time
		want   string
		skipIf bool
	}{
		{"today", today, "Today", false},
		{"yesterday", yesterday, "Yesterday", false},
		{"this week", thisWeekDate, "This Week", !thisWeekValid},
		{"this month", thisMonthCandidate, "This Month", !thisMonthValid},
		{"two months ago", twoMonthsAgo, "Last 3 Months", false},
		{"very old", veryOld, "Earlier", false},
		{"zero time", time.Time{}, "Earlier", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.skipIf {
				t.Skipf("skipping: no suitable date for %q test in current week/month", tt.name)
			}
			got := views.TimeBucket(tt.t)
			if got != tt.want {
				t.Fatalf("TimeBucket(%v) = %q, want %q", tt.t, got, tt.want)
			}
		})
	}
}

func TestSidebarStatusLabel(t *testing.T) {
	m := views.NewSidebarModel(makeSidebarStyles())
	// Default state: no label.
	if got := m.StatusLabel(); got != "" {
		t.Fatalf("expected empty status label at default, got %q", got)
	}

	m.CycleFilter()
	if got := m.StatusLabel(); got != "active" {
		t.Fatalf("expected 'active' after one filter cycle, got %q", got)
	}

	m.CycleDimension()
	// Should contain both 'active' and a dimension label.
	got := m.StatusLabel()
	if !strings.Contains(got, "active") || !strings.Contains(got, "state") {
		t.Fatalf("expected label to contain 'active' and 'state', got %q", got)
	}
}

func TestSidebarCycleFilter(t *testing.T) {
	m := views.NewSidebarModel(makeSidebarStyles())
	// Cycle through all 4 filter modes.
	expected := []views.SidebarFilter{
		views.SidebarFilterActive,
		views.SidebarFilterNeedsAttention,
		views.SidebarFilterCompleted,
		views.SidebarFilterAll,
	}
	for _, want := range expected {
		m.CycleFilter()
		if got := m.FilterMode(); got != want {
			t.Fatalf("CycleFilter() = %d, want %d", got, want)
		}
	}
}

func TestSidebarCycleDimension(t *testing.T) {
	m := views.NewSidebarModel(makeSidebarStyles())
	expected := []views.SidebarDimension{
		views.SidebarDimState,
		views.SidebarDimSource,
		views.SidebarDimCreated,
		views.SidebarDimActivity,
		views.SidebarDimNone,
	}
	for _, want := range expected {
		m.CycleDimension()
		if got := m.DimensionMode(); got != want {
			t.Fatalf("CycleDimension() = %d, want %d", got, want)
		}
	}
}

func TestSidebarToggleDirection(t *testing.T) {
	m := views.NewSidebarModel(makeSidebarStyles())
	if got := m.DirectionMode(); got != views.SidebarDirDesc {
		t.Fatalf("expected default direction Desc, got %d", got)
	}
	m.ToggleDirection()
	if got := m.DirectionMode(); got != views.SidebarDirAsc {
		t.Fatalf("expected Asc after toggle, got %d", got)
	}
	m.ToggleDirection()
	if got := m.DirectionMode(); got != views.SidebarDirDesc {
		t.Fatalf("expected Desc after second toggle, got %d", got)
	}
}

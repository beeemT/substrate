package views_test

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/beeemT/substrate/internal/tui/views"
)

func TestPlanReviewModel_SetPlanDocument(t *testing.T) {
	st := newTestStyles(t)
	m := views.NewPlanReviewModel(st)
	m.SetSize(80, 30)
	m.SetPlanDocument("p1", "## Orchestration\n\nStep one.\n\n## SubPlan: repo-a\n### Goal\nShip it.")
	m.SetWorkItemID("wi1")

	v := m.View()
	if v == "" {
		t.Fatal("expected non-empty View() after SetPlanDocument")
	}
}

func TestPlanReviewModel_Update_Approve(t *testing.T) {
	st := newTestStyles(t)
	m := views.NewPlanReviewModel(st)
	m.SetSize(80, 30)
	m.SetPlanDocument("p1", "## Orchestration\n\n# My Plan")
	m.SetWorkItemID("wi1")

	updated, cmd := m.Update(tea.KeyMsg{
		Type:  tea.KeyRunes,
		Runes: []rune{'a'},
	})
	_ = updated

	if cmd == nil {
		t.Fatal("expected non-nil cmd after pressing 'a' (plan approval)")
	}
	result := cmd()
	msg, ok := result.(views.PlanApproveMsg)
	if !ok {
		t.Fatalf("expected PlanApproveMsg, got %T", result)
	}
	if msg.PlanID != "p1" {
		t.Errorf("expected PlanID p1, got %q", msg.PlanID)
	}
	if msg.WorkItemID != "wi1" {
		t.Errorf("expected WorkItemID wi1, got %q", msg.WorkItemID)
	}
}

func TestPlanReviewModel_WrapsAndNumbersPlanLines(t *testing.T) {
	t.Parallel()

	m := views.NewPlanReviewModel(newTestStyles(t))
	m.SetSize(28, 12)
	m.SetTitle("SUB-1")
	m.SetPlanDocument("p1", "alpha beta gamma delta epsilon\nSecond short line.")

	rendered := m.View()
	plain := ansi.Strip(rendered)
	if strings.Count(plain, " 1 │") != 1 {
		t.Fatalf("view = %q, want exactly one numbered row for line 1", plain)
	}
	if !strings.Contains(plain, " 2 │ Second short line.") {
		t.Fatalf("view = %q, want numbered second line", plain)
	}
	if strings.Contains(plain, "alp\n") || strings.Contains(plain, "bet\n") || strings.Contains(plain, "gamm\n") {
		t.Fatalf("view = %q, want wrapping only at word boundaries", plain)
	}
	for _, want := range []string{" 1 │ alpha beta gamma delta", "   │ epsilon"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("view = %q, want %q", plain, want)
		}
	}
	continuationFound := false
	for line := range strings.SplitSeq(plain, "\n") {
		if strings.HasPrefix(line, "   │ ") {
			continuationFound = true

			break
		}
	}
	if !continuationFound {
		t.Fatalf("view = %q, want wrapped continuation row with empty line-number gutter", plain)
	}
	hints := m.KeybindHints()
	labels := make([]string, 0, len(hints))
	for _, hint := range hints {
		labels = append(labels, hint.Label)
	}
	if !strings.Contains(strings.Join(labels, " | "), "Close") {
		t.Fatalf("keybind hints = %#v, want close hint", hints)
	}
	for i, line := range strings.Split(rendered, "\n") {
		if got := ansi.StringWidth(line); got > 28 {
			t.Fatalf("line %d width = %d, want <= 28\nline: %q", i+1, got, line)
		}
	}
}

func TestPlanReviewModel_ShowsFullPlanSectionsAndPreservesYamlIndentation(t *testing.T) {
	t.Parallel()

	m := views.NewPlanReviewModel(newTestStyles(t))
	m.SetSize(72, 32)
	m.SetTitle("SUB-2")
	m.SetPlanDocument("p2", "```substrate-plan\nexecution_groups:\n  - [repo-a, repo-b]\n```\n\n## Orchestration\nCoordinate contract changes.\n\n## SubPlan: repo-a\n### Goal\nShip repo a.\n\n### Scope\n- internal/a.go\n\n### Changes\n1. Update parser.\n2. Add tests.\n3. Wire callers.\n\n### Validation\n- go test ./...\n\n### Risks\n- Preserve backwards compatibility assumptions.\n")

	plain := ansi.Strip(m.View())
	for _, want := range []string{"execution_groups:", "  - [repo-a, repo-b]", "Orchestration", "Coordinate contract changes.", "SubPlan: repo-a", "Goal", "Ship repo a.", "internal/a.go", "go test ./...", "Preserve backwards compatibility assumptions."} {
		if !strings.Contains(plain, want) {
			t.Fatalf("view = %q, want %q", plain, want)
		}
	}
	for _, raw := range []string{"## Orchestration", "## SubPlan: repo-a", "### Goal", "### Scope", "### Changes"} {
		if strings.Contains(plain, raw) {
			t.Fatalf("view = %q, want markdown heading %q rendered without raw markers", plain, raw)
		}
	}
}

// checkFeedbackViewBounds asserts every rendered line is within width and the
// total line count equals height.
func checkFeedbackViewBounds(t *testing.T, label, view string, wantWidth, wantHeight int) {
	t.Helper()
	lines := strings.Split(view, "\n")
	if got := len(lines); got != wantHeight {
		t.Errorf("%s: line count = %d, want %d\nview:\n%s", label, got, wantHeight, view)
	}
	for i, line := range lines {
		if got := ansi.StringWidth(line); got > wantWidth {
			t.Errorf("%s: line %d width = %d, want <= %d; line = %q", label, i+1, got, wantWidth, line)
		}
	}
}

// countPlanLines counts the number of plan-content rows visible in view by
// looking for the line-number │ separator that renderPlanReviewContent injects.
func countPlanLines(view string) int {
	n := 0
	for line := range strings.SplitSeq(view, "\n") {
		if strings.Contains(ansi.Strip(line), " │ ") {
			n++
		}
	}

	return n
}

// wantViewport computes the expected visible plan rows: terminal height minus
// 2 base reserved rows (title + header-divider), 1 feedback label row, and the
// current textarea row count.
func wantViewport(termHeight, feedbackRows int) int {
	return termHeight - 2 - 1 - feedbackRows
}

// TestPlanReviewFeedbackInputGrowsAndScrolls verifies that the feedback textarea
// grows from 1 row to feedbackMaxLines (6) as the user types, then caps and
// scrolls. Both layout bounds (width × height) and viewport shrinkage are
// asserted at each stage.
func TestPlanReviewFeedbackInputGrowsAndScrolls(t *testing.T) {
	t.Parallel()

	const width, height = 80, 24
	// 30 plan lines: more than the maximum viewport height (18), so the plan
	// content always fills the viewport and the countPlanLines delta is observable.
	planContent := strings.Repeat("plan line\n", 30)

	m := views.NewPlanReviewModel(newTestStyles(t))
	m.SetSize(width, height)
	m.SetPlanDocument("p1", planContent)
	m.SetTitle("TEST")

	// Enter request-changes mode.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	checkFeedbackViewBounds(t, "after c (1-row)", m.View(), width, height)
	if got, want := countPlanLines(m.View()), wantViewport(height, 1); got != want {
		t.Errorf("after c: plan lines = %d, want %d", got, want)
	}

	// Type text that word-wraps to exactly 3 display rows.
	// Inner width 80: "word" (4 chars). Each line fits 16 words (4 + 5×15 = 79).
	// 33 words → line1(16) + line2(16) + line3(1) = 3 visual rows.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(strings.Repeat("word ", 33))})
	checkFeedbackViewBounds(t, "3-row input", m.View(), width, height)
	if got, want := countPlanLines(m.View()), wantViewport(height, 3); got != want {
		t.Errorf("3-row: plan lines = %d, want %d", got, want)
	}

	// Extend past feedbackMaxLines — textarea caps at 6 and scrolls.
	// 120 more words → total ≥ 7 visual rows → capped at 6.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(strings.Repeat("word ", 120))})
	checkFeedbackViewBounds(t, "capped 6-row", m.View(), width, height)
	if got, want := countPlanLines(m.View()), wantViewport(height, 6); got != want {
		t.Errorf("capped: plan lines = %d, want %d", got, want)
	}
}

// TestPlanReviewFeedbackInputNarrowTerminal guards the layout at a narrow (40-col)
// terminal where word-wrapping is more aggressive.
func TestPlanReviewFeedbackInputNarrowTerminal(t *testing.T) {
	t.Parallel()

	const width, height = 40, 18
	// 15 plan lines > max viewport height (12) so the count is always meaningful.
	planContent := strings.Repeat("plan line\n", 15)

	m := views.NewPlanReviewModel(newTestStyles(t))
	m.SetSize(width, height)
	m.SetPlanDocument("p1", planContent)
	m.SetTitle("N")

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	checkFeedbackViewBounds(t, "narrow 1-row", m.View(), width, height)
	if got, want := countPlanLines(m.View()), wantViewport(height, 1); got != want {
		t.Errorf("narrow 1-row: plan lines = %d, want %d", got, want)
	}

	// Inner width 40: "word" (4 chars) fits 8 per line (4 + 5×7 = 39).
	// 60 words → ceil(60/8) = 8 rows → capped at 6.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(strings.Repeat("word ", 60))})
	checkFeedbackViewBounds(t, "narrow capped", m.View(), width, height)
	if got, want := countPlanLines(m.View()), wantViewport(height, 6); got != want {
		t.Errorf("narrow capped: plan lines = %d, want %d", got, want)
	}
}

// TestPlanReviewFeedbackInputRejectMode ensures the rejection-reason input
// obeys the same growth and scrolling rules.
func TestPlanReviewFeedbackInputRejectMode(t *testing.T) {
	t.Parallel()

	const width, height = 80, 22
	// 20 plan lines > max viewport height (16).
	planContent := strings.Repeat("plan line\n", 20)

	m := views.NewPlanReviewModel(newTestStyles(t))
	m.SetSize(width, height)
	m.SetPlanDocument("p1", planContent)
	m.SetTitle("TEST")

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	checkFeedbackViewBounds(t, "reject 1-row", m.View(), width, height)
	if got, want := countPlanLines(m.View()), wantViewport(height, 1); got != want {
		t.Errorf("reject 1-row: plan lines = %d, want %d", got, want)
	}

	// 120 words appended → total ≥ 7 rows → capped at 6.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(strings.Repeat("word ", 120))})
	checkFeedbackViewBounds(t, "reject capped", m.View(), width, height)
	if got, want := countPlanLines(m.View()), wantViewport(height, 6); got != want {
		t.Errorf("reject capped: plan lines = %d, want %d", got, want)
	}
}

// TestPlanReviewModel_RendersMarkdownTable verifies that GFM tables in plan content
// are rendered via glamour (with box-drawing characters) rather than shown as raw
// pipe-delimited text.
func TestPlanReviewModel_RendersMarkdownTable(t *testing.T) {
	t.Parallel()

	plan := strings.Join([]string{
		"## Overview",
		"",
		"| Component | Status | Notes |",
		"| --- | --- | --- |",
		"| Auth service | Done | Migrated to OAuth2 |",
		"| Payment API | In progress | Blocked on PCI review |",
		"",
		"Next steps below.",
	}, "\n")

	m := views.NewPlanReviewModel(newTestStyles(t))
	m.SetSize(80, 30)
	m.SetTitle("PLAN")
	m.SetPlanDocument("p1", plan)
	m.SetWorkItemID("wi1")

	v := m.View()
	if v == "" {
		t.Fatal("expected non-empty View()")
	}
	plain := ansi.Strip(v)

	// Column headers and cell data must appear in the rendered output.
	for _, want := range []string{
		"Component", "Status", "Notes",
		"Auth service", "Done", "Migrated to OAuth2",
		"Payment API", "In progress", "Blocked on PCI review",
	} {
		if !strings.Contains(plain, want) {
			t.Fatalf("rendered = %q, want %q", plain, want)
		}
	}

	// The non-table content must also still be present.
	if !strings.Contains(plain, "Next steps below.") {
		t.Fatalf("rendered = %q, want 'Next steps below.'", plain)
	}
}

// TestPlanReviewModel_TableRespectsWidth ensures that table rendering at a narrow
// terminal width does not produce lines wider than the terminal.
func TestPlanReviewModel_TableRespectsWidth(t *testing.T) {
	t.Parallel()

	plan := strings.Join([]string{
		"| Column A | Column B with a longer header | Column C |",
		"| --- | --- | --- |",
		"| aaa | bbbbbbbbbbbbbbbbbbbbbbbb | ccc |",
	}, "\n")

	const width = 50
	m := views.NewPlanReviewModel(newTestStyles(t))
	m.SetSize(width, 20)
	m.SetTitle("W")
	m.SetPlanDocument("p1", plan)

	rendered := m.View()
	for i, line := range strings.Split(rendered, "\n") {
		if got := ansi.StringWidth(line); got > width {
			t.Fatalf("line %d width = %d, want <= %d\nline: %q", i+1, got, width, line)
		}
	}
}

// TestPlanReviewModel_TableNotDetectedInsideCodeBlock ensures that pipe characters
// inside fenced code blocks are not treated as table rows.
func TestPlanReviewModel_TableNotDetectedInsideCodeBlock(t *testing.T) {
	t.Parallel()

	plan := strings.Join([]string{
		"```bash",
		"echo hello | grep foo",
		"cat file.txt | sort | uniq",
		"```",
		"",
		"After the block.",
	}, "\n")

	m := views.NewPlanReviewModel(newTestStyles(t))
	m.SetSize(80, 20)
	m.SetTitle("CODE")
	m.SetPlanDocument("p1", plan)

	plain := ansi.Strip(m.View())
	// The pipe commands must appear as-is in the rendered output.
	for _, want := range []string{"echo hello | grep foo", "cat file.txt | sort | uniq", "After the block."} {
		if !strings.Contains(plain, want) {
			t.Fatalf("rendered = %q, want %q", plain, want)
		}
	}
}

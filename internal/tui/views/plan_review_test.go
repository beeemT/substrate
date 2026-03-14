package views_test

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/beeemT/substrate/internal/domain"
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
	for _, line := range strings.Split(plain, "\n") {
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

func TestReadyToPlanModelViewSeparatesSectionsAndRespectsSize(t *testing.T) {
	t.Parallel()

	m := views.NewReadyToPlanModel(newTestStyles(t))
	m.SetSize(48, 12)
	m.SetWorkItem(&domain.Session{
		ID:          "wi-1",
		ExternalID:  "SUB-1",
		Source:      "github",
		Title:       "Investigate overflow",
		Description: "## Summary\n\nThis is **important**.",
		Labels:      []string{"bug", "backend"},
		Metadata: map[string]any{
			"tracker_refs": []domain.TrackerReference{{Provider: "github", Kind: "issue", Owner: "acme", Repo: "rocket", Number: 42}},
		},
	})

	rendered := m.View()
	plain := ansi.Strip(rendered)
	plainLines := strings.Split(plain, "\n")
	if len(plainLines) == 0 || !strings.HasPrefix(plainLines[0], "  SUB-1 · Investigate overflow") {
		t.Fatalf("first line = %q, want title inset from the pane edge", plainLines[0])
	}
	for _, want := range []string{"SUB-1 · Investigate overflow", "Details", "Summary", "This is important.", "Press [Enter]", "╭", "╮", "┌", "┐"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("view = %q, want %q", plain, want)
		}
	}
	for _, hidden := range []string{"GitHub", "acme/rocket", "Labels: bug, backend"} {
		if strings.Contains(plain, hidden) {
			t.Fatalf("view = %q, want ready overview to omit source detail %q", plain, hidden)
		}
	}
	for _, raw := range []string{"## Summary", "**important**"} {
		if strings.Contains(plain, raw) {
			t.Fatalf("view = %q, must not contain raw markdown token %q", plain, raw)
		}
	}

	lines := strings.Split(rendered, "\n")
	if got := len(lines); got != 12 {
		t.Fatalf("line count = %d, want 12", got)
	}
	for i, line := range lines {
		if got := ansi.StringWidth(line); got > 48 {
			t.Fatalf("line %d width = %d, want <= 48\nline: %q", i+1, got, line)
		}
	}

	cardIndex := -1
	for i, line := range plainLines {
		if strings.Contains(line, "┌") {
			cardIndex = i
			break
		}
	}
	if cardIndex == -1 {
		t.Fatalf("view = %q, want next-step card near the bottom", plain)
	}
	cardLine := plainLines[cardIndex]
	leftInset := len(cardLine) - len(strings.TrimLeft(cardLine, " "))
	rightInset := len(cardLine) - len(strings.TrimRight(cardLine, " "))
	if leftInset < 2 {
		t.Fatalf("card line = %q, want at least 2 spaces of left inset", cardLine)
	}
	if rightInset < 2 {
		t.Fatalf("card line = %q, want at least 2 spaces of right inset", cardLine)
	}
	if leftInset != rightInset {
		t.Fatalf("card line = %q, want symmetric left/right inset, got left=%d right=%d", cardLine, leftInset, rightInset)
	}
	if strings.TrimSpace(plainLines[len(plainLines)-1]) != "" {
		t.Fatalf("last line = %q, want bottom padding below the next-step card", plainLines[len(plainLines)-1])
	}

	footerRegion := ansi.Strip(strings.Join(lines[max(0, len(lines)-5):], "\n"))
	if !strings.Contains(footerRegion, "Press [Enter]") {
		t.Fatalf("footer region = %q, want next-step CTA near the bottom", footerRegion)
	}
}

func TestPlanReviewHintsRowHasSymmetricLeadingSpace(t *testing.T) {
	t.Parallel()

	// Use a wide enough view so none of the hint labels are truncated.
	m := views.NewPlanReviewModel(newTestStyles(t))
	m.SetSize(120, 20)
	m.SetTitle("SUB-1")
	m.SetPlanDocument("p1", "## Plan\n\nSome content.")

	rendered := m.View()
	plain := ansi.Strip(rendered)
	lines := strings.Split(plain, "\n")

	// The last line is the hints row. It should be exactly width chars.
	hintsLine := lines[len(lines)-1]
	if got := len([]rune(hintsLine)); got != 120 {
		t.Fatalf("hints line width = %d, want 120; line=%q", got, hintsLine)
	}

	// Must have at least 1 leading space before [a] (Padding(0,1) applied).
	if !strings.HasPrefix(hintsLine, " ") {
		t.Fatalf("hints line = %q, want at least one leading space before first hint", hintsLine)
	}
	firstTextIdx := len(hintsLine) - len(strings.TrimLeft(hintsLine, " "))
	if firstTextIdx < 1 {
		t.Fatalf("hints line = %q, want firstTextIdx >= 1, got %d", hintsLine, firstTextIdx)
	}
	if !strings.HasPrefix(hintsLine[firstTextIdx:], "[a]") {
		t.Fatalf("hints line = %q, want [a] as first hint text (after leading spaces), got prefix %q", hintsLine, hintsLine[firstTextIdx:])
	}

	// Must have at least 1 trailing space after the last hint text (Padding(0,1)).
	if !strings.HasSuffix(hintsLine, " ") {
		t.Fatalf("hints line = %q, want at least one trailing space after last hint", hintsLine)
	}
}

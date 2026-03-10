package views_test

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/tui/views"
)

func TestPlanReviewModel_SetPlan(t *testing.T) {
	st := newTestStyles(t)
	m := views.NewPlanReviewModel(st)
	m.SetSize(80, 30)
	m.SetPlan(domain.Plan{
		ID:               "p1",
		WorkItemID:       "wi1",
		OrchestratorPlan: "# My Plan\n\nStep one.",
	})
	m.SetWorkItemID("wi1")

	v := m.View()
	if v == "" {
		t.Fatal("expected non-empty View() after SetPlan")
	}
}

func TestPlanReviewModel_Update_Approve(t *testing.T) {
	st := newTestStyles(t)
	m := views.NewPlanReviewModel(st)
	m.SetSize(80, 30)
	m.SetPlan(domain.Plan{
		ID:               "p1",
		WorkItemID:       "wi1",
		OrchestratorPlan: "# My Plan",
	})
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

func TestReadyToPlanModelViewSeparatesSectionsAndRespectsSize(t *testing.T) {
	t.Parallel()

	m := views.NewReadyToPlanModel(newTestStyles(t))
	m.SetSize(48, 12)
	m.SetWorkItem(&domain.WorkItem{
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

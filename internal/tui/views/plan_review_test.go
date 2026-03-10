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
		Title:       "Investigate overflow",
		Description: "## Summary\n\nThis is **important**.",
	})

	rendered := m.View()
	plain := ansi.Strip(rendered)
	plainLines := strings.Split(plain, "\n")
	if len(plainLines) == 0 || !strings.HasPrefix(plainLines[0], "  SUB-1 · Investigate overflow") {
		t.Fatalf("first line = %q, want title inset from the pane edge", plainLines[0])
	}
	for _, want := range []string{"SUB-1 · Investigate overflow", "Details", "Next step", "Summary", "This is important.", "Press [Enter]", "╭", "╮", "┌", "┐"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("view = %q, want %q", plain, want)
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

	labelIndex := -1
	cardIndex := -1
	for i, line := range plainLines {
		if labelIndex == -1 && strings.Contains(line, "Next step") {
			labelIndex = i
		}
		if cardIndex == -1 && strings.Contains(line, "┌") {
			cardIndex = i
		}
	}
	if labelIndex == -1 || cardIndex != labelIndex+1 {
		t.Fatalf("label index = %d card index = %d, want card immediately below the label", labelIndex, cardIndex)
	}
	labelLine := plainLines[labelIndex]
	cardLine := plainLines[cardIndex]
	if got := len(labelLine) - len(strings.TrimLeft(labelLine, " ")); got < 2 {
		t.Fatalf("label line = %q, want at least 2 spaces of left inset", labelLine)
	}
	if got := len(labelLine) - len(strings.TrimRight(labelLine, " ")); got < 2 {
		t.Fatalf("label line = %q, want at least 2 spaces of right inset", labelLine)
	}
	if got := len(cardLine) - len(strings.TrimLeft(cardLine, " ")); got < 2 {
		t.Fatalf("card line = %q, want at least 2 spaces of left inset", cardLine)
	}
	if got := len(cardLine) - len(strings.TrimRight(cardLine, " ")); got < 2 {
		t.Fatalf("card line = %q, want at least 2 spaces of right inset", cardLine)
	}
	if strings.TrimSpace(plainLines[len(plainLines)-1]) != "" {
		t.Fatalf("last line = %q, want bottom padding below the next-step card", plainLines[len(plainLines)-1])
	}

	footerRegion := ansi.Strip(strings.Join(lines[max(0, len(lines)-5):], "\n"))
	if !strings.Contains(footerRegion, "Press [Enter]") {
		t.Fatalf("footer region = %q, want next-step CTA near the bottom", footerRegion)
	}
}

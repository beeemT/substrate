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
	for _, want := range []string{"SUB-1 · Investigate overflow", "Description", "Next step", "Summary", "This is important.", "Press [Enter]"} {
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
}

package views

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func cmdEmitsFollowUpPlan(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	msg := cmd()
	if _, ok := msg.(FollowUpPlanMsg); ok {
		return true
	}
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		return false
	}
	for _, c := range batch {
		if cmdEmitsFollowUpPlan(c) {
			return true
		}
	}

	return false
}

func TestCompletedModelWheelScrollsWhileFeedbackActive(t *testing.T) {
	t.Parallel()

	planLines := make([]string, 0, 40)
	for i := 1; i <= 40; i++ {
		planLines = append(planLines, fmt.Sprintf("line %02d", i))
	}

	m := NewCompletedModel(testStyles())
	m.SetSize(80, 12)
	m.SetTitle("T")
	m.SetWorkItemID("wi-1")
	m.SetPlan(strings.Join(planLines, "\n"))
	_ = m.OpenFeedback()

	before := ansi.Strip(m.View())
	if !strings.Contains(before, " 1 │ line 01") {
		t.Fatalf("before view = %q, want first plan line visible", before)
	}

	m, _ = m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown})
	after := ansi.Strip(m.View())
	if before == after {
		t.Fatal("wheel-down while feedback active did not change rendered view")
	}
	if strings.Contains(after, " 1 │ line 01") {
		t.Fatalf("after view = %q, want plan viewport scrolled past first line", after)
	}
}

func TestCompletedModelEmptyFeedbackDiscarded(t *testing.T) {
	t.Parallel()

	m := NewCompletedModel(testStyles())
	m.SetSize(80, 24)
	m.SetTitle("T")
	m.SetWorkItemID("wi-1")
	m.SetPlan("plan content")
	_ = m.OpenFeedback()

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("   ")})
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmdEmitsFollowUpPlan(cmd) {
		t.Fatal("empty feedback emitted FollowUpPlanMsg")
	}
	if m.InputCaptured() {
		t.Fatal("feedback remained active after empty submission")
	}
}

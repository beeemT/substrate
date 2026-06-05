package views

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func cmdFollowUpPlan(cmd tea.Cmd) (FollowUpPlanMsg, bool) {
	if cmd == nil {
		return FollowUpPlanMsg{}, false
	}
	msg := cmd()
	if followUp, ok := msg.(FollowUpPlanMsg); ok {
		return followUp, true
	}
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		return FollowUpPlanMsg{}, false
	}
	for _, c := range batch {
		if followUp, ok := cmdFollowUpPlan(c); ok {
			return followUp, true
		}
	}

	return FollowUpPlanMsg{}, false
}

func cmdEmitsFollowUpPlan(cmd tea.Cmd) bool {
	_, ok := cmdFollowUpPlan(cmd)
	return ok
}

func cmdFollowUpSession(cmd tea.Cmd) (FollowUpSessionMsg, bool) {
	if cmd == nil {
		return FollowUpSessionMsg{}, false
	}
	msg := cmd()
	if followUp, ok := msg.(FollowUpSessionMsg); ok {
		return followUp, true
	}
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		return FollowUpSessionMsg{}, false
	}
	for _, c := range batch {
		if followUp, ok := cmdFollowUpSession(c); ok {
			return followUp, true
		}
	}

	return FollowUpSessionMsg{}, false
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

func TestCompletedModelCodeFeedbackEmitsSessionFollowUp(t *testing.T) {
	t.Parallel()

	m := NewCompletedModel(testStyles())
	m.SetSize(80, 24)
	m.SetTitle("T")
	m.SetWorkItemID("wi-1")
	m.SetCodeFollowUpSessionID("review-leaf-1")
	m.SetPlan("plan content")
	_ = m.OpenCodeFollowUp()

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("please adjust the code")})
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmdEmitsFollowUpPlan(cmd) {
		t.Fatal("code feedback emitted FollowUpPlanMsg")
	}
	followUp, ok := cmdFollowUpSession(cmd)
	if !ok {
		t.Fatalf("cmd did not emit FollowUpSessionMsg: %#v", cmd)
	}
	if followUp.TaskID != "review-leaf-1" {
		t.Fatalf("TaskID = %q, want review-leaf-1", followUp.TaskID)
	}
	if followUp.Feedback != "please adjust the code" {
		t.Fatalf("Feedback = %q", followUp.Feedback)
	}
	if m.InputCaptured() {
		t.Fatal("feedback remained active after submission")
	}
}

package views

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestPlanReviewCtrlWDeletesWord(t *testing.T) {
	st := testStyles()
	m := NewPlanReviewModel(st)
	m.SetSize(80, 40)
	m.SetPlanDocument("p1", "# Test Plan\nSome content.")

	// Press "c" to enter changes mode.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	if m.inputMode != planReviewChanges {
		t.Fatalf("inputMode = %v, want %v", m.inputMode, planReviewChanges)
	}

	// Type "hello world".
	for _, r := range "hello world" {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if got := m.feedbackInput.Value(); got != "hello world" {
		t.Fatalf("after typing: got %q, want %q", got, "hello world")
	}

	// ctrl+w should delete "world" (this is what Warp sends for ⌥+Backspace).
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlW})
	got := m.feedbackInput.Value()
	want := "hello "
	if got != want {
		t.Fatalf("after ctrl+w: got %q, want %q", got, want)
	}
}

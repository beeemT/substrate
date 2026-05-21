package views

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/beeemT/substrate/internal/tui/styles"
)

func TestInterruptedOverlayUpdate_FiresResumeSessionMsgWithWorkItemID(t *testing.T) {
	t.Parallel()

	m := NewInterruptedModel(styles.NewStyles(styles.DefaultTheme))
	m.SetSession("sess-1", "sp-1", "repo", "/tmp/repo", "wi-1", false, true)

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if cmd == nil {
		t.Fatal("pressing r on interrupted overlay must return a command")
	}
	msg := cmd()
	resume, ok := msg.(ResumeSessionMsg)
	if !ok {
		t.Fatalf("expected ResumeSessionMsg, got %T", msg)
	}
	if resume.WorkItemID != "wi-1" {
		t.Fatalf("ResumeSessionMsg.WorkItemID = %q, want %q", resume.WorkItemID, "wi-1")
	}
}

func TestInterruptedOverlayUpdate_FiresRestartPlanMsgWithWorkItemID(t *testing.T) {
	t.Parallel()

	m := NewInterruptedModel(styles.NewStyles(styles.DefaultTheme))
	m.SetSession("sess-plan", "", "", "", "wi-1", true, true)

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if cmd == nil {
		t.Fatal("pressing r on interrupted planning overlay must return a command")
	}
	msg := cmd()
	restart, ok := msg.(RestartPlanMsg)
	if !ok {
		t.Fatalf("expected RestartPlanMsg, got %T", msg)
	}
	if restart.WorkItemID != "wi-1" {
		t.Fatalf("RestartPlanMsg.WorkItemID = %q, want %q", restart.WorkItemID, "wi-1")
	}
}

func TestInterruptedOverlayKeybindHints_ShowsResumeAll(t *testing.T) {
	t.Parallel()

	m := NewInterruptedModel(styles.NewStyles(styles.DefaultTheme))
	m.SetSession("sess-1", "sp-1", "repo", "/tmp/repo", "wi-1", false, true)

	hints := m.KeybindHints()
	if len(hints) == 0 || hints[0].Key != "r" || hints[0].Label != "Resume all" {
		t.Fatalf("hints = %#v, want first hint r Resume all", hints)
	}
}

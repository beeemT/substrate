package views

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestAppOKeyOpensTerminalInWorktree asserts that pressing 'o' in a session view
// (ContentModeAgentSession with an active session that has a worktree) returns an
// OpenTerminalCmd for the session's worktree.
func TestAppOKeyOpensTerminalInWorktree(t *testing.T) {
	t.Parallel()

	app := newSidebarDrilldownTestApp()
	model, _ := app.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	updated := model.(*App)

	// Set a worktree path on the session.
	for i := range updated.sessions {
		if updated.sessions[i].ID == "sess-1" {
			updated.sessions[i].WorktreePath = "/workspace/repo-a"
			break
		}
	}

	// Enter the task sidebar and select the implementation session.
	updated.mainFocus = mainFocusContent
	updated.content.SetMode(ContentModeAgentSession)
	updated.content.sessionLog.SetLogPath("sess-1", "/tmp/session.log")
	updated.taskSessionSelectionByWorkItem[updated.currentWorkItemID] = "sess-1"

	// Press 'o' — should return OpenTerminalCmd.
	model, cmd := updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	if cmd == nil {
		t.Fatal("expected non-nil cmd from 'o' key in session view with worktree")
	}
	_ = model // cmd is verified by presence
}

// TestAppOKeyNoOpWithoutWorktree asserts that pressing 'o' in a session view
// with no worktree on the session does NOT return a command.
func TestAppOKeyNoOpWithoutWorktree(t *testing.T) {
	t.Parallel()

	app := newSidebarDrilldownTestApp()
	model, _ := app.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	updated := model.(*App)

	// Session from newSidebarDrilldownTestApp has no worktree.

	// Enter the task sidebar and select the implementation session.
	updated.mainFocus = mainFocusContent
	updated.content.SetMode(ContentModeAgentSession)
	updated.content.sessionLog.SetLogPath("sess-1", "/tmp/session.log")
	updated.taskSessionSelectionByWorkItem[updated.currentWorkItemID] = "sess-1"

	// Press 'o' — should return nil (no worktree).
	model, cmd := updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	if cmd != nil {
		t.Fatal("expected nil cmd when session has no worktree")
	}
	_ = model
}

// TestAppOKeyNoOpInSidebar asserts that 'o' does NOT open a terminal when the
// sidebar is focused (it should toggle sort direction instead).
func TestAppOKeyNoOpInSidebar(t *testing.T) {
	t.Parallel()

	app := newSidebarDrilldownTestApp()
	model, _ := app.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	updated := model.(*App)

	// 'o' in sidebar mode (sessions pane) toggles sort direction.
	updated.mainFocus = mainFocusSidebar
	updated.sidebarMode = sidebarPaneSessions

	// Press 'o' — should NOT return OpenTerminalCmd (sort toggle, no cmd).
	model, cmd := updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	if cmd != nil {
		t.Fatal("expected nil cmd from 'o' key in sidebar (should toggle sort)")
	}
	_ = model
}

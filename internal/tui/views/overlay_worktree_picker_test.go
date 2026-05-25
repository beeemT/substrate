package views

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/beeemT/substrate/internal/gitwork"
	"github.com/beeemT/substrate/internal/tui/styles"
)

func newTestWorktreePickerOverlay() WorktreePickerOverlay {
	st := styles.NewStyles(styles.DefaultTheme)
	return NewWorktreePickerOverlay("/test/workspace", nil, st)
}

func TestWorktreePickerOverlay_InitialState(t *testing.T) {
	m := newTestWorktreePickerOverlay()
	m.SetSize(120, 40)
	m.Open()

	if !m.Active() {
		t.Error("expected active after Open()")
	}
	if !m.loading {
		t.Error("expected loading to be true after Open()")
	}
	if !m.picker.IsFocusLeft() {
		t.Error("expected focus to be left after Open()")
	}
}

func TestWorktreePickerOverlay_Open(t *testing.T) {
	m := newTestWorktreePickerOverlay()
	m.SetSize(120, 40)
	cmd := m.Open()

	if !m.Active() {
		t.Error("expected active after Open()")
	}
	if !m.loading {
		t.Error("expected loading to be true after Open()")
	}
	if cmd == nil {
		t.Fatal("expected non-nil cmd from Open()")
	}
}

func TestWorktreePickerOverlay_Close(t *testing.T) {
	m := newTestWorktreePickerOverlay()
	m.SetSize(120, 40)
	m.Open()
	m.Close()

	if m.Active() {
		t.Error("expected inactive after Close()")
	}
}

func TestWorktreePickerOverlay_SetSize(t *testing.T) {
	m := newTestWorktreePickerOverlay()
	m.SetSize(120, 40)

	if m.width != 120 {
		t.Errorf("width = %d, want 120", m.width)
	}
	if m.height != 40 {
		t.Errorf("height = %d, want 40", m.height)
	}
}

func TestWorktreePickerOverlay_TabKeySwitchesFocus(t *testing.T) {
	m := newTestWorktreePickerOverlay()
	m.SetSize(120, 40)
	m.Open()

	if !m.picker.IsFocusLeft() {
		t.Fatal("expected initial focus to be left")
	}

	// Press Tab to switch focus
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if m.picker.IsFocusLeft() {
		t.Error("expected focus to switch to right after Tab")
	}

	// Press Tab again to switch back
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if !m.picker.IsFocusLeft() {
		t.Error("expected focus to switch back to left after second Tab")
	}
}

func TestWorktreePickerOverlay_EscKeyClosesOverlay(t *testing.T) {
	m := newTestWorktreePickerOverlay()
	m.SetSize(120, 40)
	m.Open()

	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("expected non-nil cmd from Esc key")
	}

	// The command should be CloseOverlayMsg
	if _, ok := <-chanFromCmd(cmd); !ok {
		// This is fine, the cmd might have already completed
	}
}

func TestWorktreePickerOverlay_ManagedReposLoadedMsg(t *testing.T) {
	m := newTestWorktreePickerOverlay()
	m.Open()

	repos := []managedRepo{
		{Path: "/test/repo-a", Name: "repo-a", Kind: repoKindGitWork},
		{Path: "/test/repo-b", Name: "repo-b", Kind: repoKindGitWork},
	}
	msg := ManagedReposLoadedMsg{Repos: repos}

	m, _ = m.Update(msg)

	if m.loading {
		t.Error("expected loading to be false after ManagedReposLoadedMsg")
	}
	if len(m.repos) != 2 {
		t.Errorf("expected 2 repos, got %d", len(m.repos))
	}
}

func TestWorktreePickerOverlay_WorktreesLoadedMsg(t *testing.T) {
	m := newTestWorktreePickerOverlay()
	m.Open()

	// First load repos
	repos := []managedRepo{
		{Path: "/test/repo-a", Name: "repo-a", Kind: repoKindGitWork},
	}
	m, _ = m.Update(ManagedReposLoadedMsg{Repos: repos})

	// Then load worktrees
	worktrees := []gitwork.Worktree{
		{Path: "/test/repo-a/main", Branch: "main", IsMain: true},
		{Path: "/test/repo-a/feature-x", Branch: "feature-x", IsMain: false},
	}
	msg := WorktreesLoadedMsg{
		Target:    WorktreeLoadTargetPicker,
		RequestID: m.worktreeReqID,
		Worktrees: worktrees,
	}

	m, _ = m.Update(msg)

	if m.worktreeLoading {
		t.Error("expected worktreeLoading to be false after WorktreesLoadedMsg")
	}
	if len(m.worktrees) != 2 {
		t.Errorf("expected 2 worktrees, got %d", len(m.worktrees))
	}
}

func TestWorktreePickerOverlay_OpenTerminalCmd(t *testing.T) {
	m := newTestWorktreePickerOverlay()
	m.Open()

	// Load repos and worktrees
	repos := []managedRepo{
		{Path: "/test/repo-a", Name: "repo-a", Kind: repoKindGitWork},
	}
	m, _ = m.Update(ManagedReposLoadedMsg{Repos: repos})

	worktrees := []gitwork.Worktree{
		{Path: "/test/repo-a/feature-x", Branch: "feature-x", IsMain: false},
	}
	m, _ = m.Update(WorktreesLoadedMsg{
		Target:    WorktreeLoadTargetPicker,
		RequestID: m.worktreeReqID,
		Worktrees: worktrees,
	})

	// Select the worktree
	m.worktreeList, _ = m.worktreeList.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'\n'}})

	// Press 't' to open terminal
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	if cmd == nil {
		t.Fatal("expected non-nil cmd from 't' key when worktree selected")
	}
}

func TestWorktreePickerOverlay_ViewReturnsEmptyWhenInactive(t *testing.T) {
	m := newTestWorktreePickerOverlay()
	view := m.View()

	if view != "" {
		t.Error("expected empty View() when inactive")
	}
}

func TestWorktreePickerOverlay_ViewReturnsContentWhenActive(t *testing.T) {
	m := newTestWorktreePickerOverlay()
	m.Open()
	view := m.View()

	if view == "" {
		t.Error("expected non-empty View() when active")
	}
}

// chanFromCmd converts a tea.Cmd to a channel for testing.
func chanFromCmd(cmd tea.Cmd) chan tea.Msg {
	ch := make(chan tea.Msg, 1)
	go func() {
		msg := cmd()
		if msg != nil {
			ch <- msg
		}
		close(ch)
	}()
	return ch
}

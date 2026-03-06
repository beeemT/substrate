package views_test

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/tui/styles"
	"github.com/beeemT/substrate/internal/tui/views"
)

func newTestStyles(t *testing.T) styles.Styles {
	t.Helper()
	return styles.NewStyles(styles.DefaultTheme)
}

func TestWorkspaceInitModal_Active(t *testing.T) {
	st := newTestStyles(t)
	m := views.NewWorkspaceInitModal("/tmp", st, nil)
	if !m.Active() {
		t.Fatal("expected Active() == true after construction")
	}
}

func TestWorkspaceInitModal_View(t *testing.T) {
	st := newTestStyles(t)
	m := views.NewWorkspaceInitModal("/tmp", st, nil)
	m.SetSize(120, 40)
	v := m.View()
	if v == "" {
		t.Fatal("expected non-empty View()")
	}
}

func TestWorkspaceInitModal_HealthCheckMsg(t *testing.T) {
	st := newTestStyles(t)
	m := views.NewWorkspaceInitModal("/tmp", st, nil)
	m.SetSize(120, 40)

	msg := views.WorkspaceHealthCheckMsg{
		Check: domain.WorkspaceHealthCheck{
			GitWorkRepos: []string{"/tmp/repo"},
		},
	}
	updated, _ := m.Update(msg)

	v := updated.View()
	if v == "" {
		t.Fatal("expected non-empty View() after HealthCheckMsg")
	}
}

func TestWorkspaceInitModal_CancelKey(t *testing.T) {
	st := newTestStyles(t)
	m := views.NewWorkspaceInitModal("/tmp", st, nil)
	m.SetSize(120, 40)

	// Advance past loading state so key events are processed.
	healthMsg := views.WorkspaceHealthCheckMsg{
		Check: domain.WorkspaceHealthCheck{},
	}
	m, _ = m.Update(healthMsg)

	// "esc" maps to "n" which fires WorkspaceCancelMsg.
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("expected non-nil cmd after Esc key (should emit WorkspaceCancelMsg)")
	}
	result := cmd()
	if _, ok := result.(views.WorkspaceCancelMsg); !ok {
		t.Fatalf("expected WorkspaceCancelMsg, got %T", result)
	}
}

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

// --- NewNewReposModal tests ---

func TestNewReposModal_Active(t *testing.T) {
	st := newTestStyles(t)
	m := views.NewNewReposModal("/tmp/ws", st, nil)
	if !m.Active() {
		t.Fatal("expected Active() == true after construction")
	}
}

func TestNewReposModal_AutoDismissOnNoPlainRepos(t *testing.T) {
	st := newTestStyles(t)
	m := views.NewNewReposModal("/tmp/ws", st, nil)
	m.SetSize(120, 40)

	// Health check with zero plain repos must auto-dismiss via CloseOverlayMsg.
	msg := views.WorkspaceHealthCheckMsg{Check: domain.WorkspaceHealthCheck{}}
	_, cmd := m.Update(msg)
	if cmd == nil {
		t.Fatal("expected non-nil cmd (CloseOverlayMsg) when no plain repos found")
	}
	result := cmd()
	if _, ok := result.(views.CloseOverlayMsg); !ok {
		t.Fatalf("expected CloseOverlayMsg, got %T", result)
	}
}

func TestNewReposModal_ShowsPlainRepos(t *testing.T) {
	st := newTestStyles(t)
	m := views.NewNewReposModal("/tmp/ws", st, nil)
	m.SetSize(120, 40)

	msg := views.WorkspaceHealthCheckMsg{
		Check: domain.WorkspaceHealthCheck{
			PlainGitClones: []string{"/tmp/ws/alpha", "/tmp/ws/beta"},
		},
	}
	updated, _ := m.Update(msg)

	v := updated.View()
	if v == "" {
		t.Fatal("expected non-empty view after health check")
	}
	// Repo names must appear in the rendered output.
	for _, want := range []string{"alpha", "beta"} {
		if !containsSubstring(v, want) {
			t.Fatalf("view does not contain repo name %q", want)
		}
	}
}

func TestNewReposModal_CancelKey_EmitsCloseOverlayMsg(t *testing.T) {
	st := newTestStyles(t)
	m := views.NewNewReposModal("/tmp/ws", st, nil)
	m.SetSize(120, 40)

	// Advance past loading.
	healthMsg := views.WorkspaceHealthCheckMsg{
		Check: domain.WorkspaceHealthCheck{PlainGitClones: []string{"/tmp/ws/repo"}},
	}
	m, _ = m.Update(healthMsg)

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("expected non-nil cmd after Esc")
	}
	result := cmd()
	if _, ok := result.(views.CloseOverlayMsg); !ok {
		t.Fatalf("expected CloseOverlayMsg, got %T — must NOT quit the app", result)
	}
}

func TestNewReposModal_ConfirmKey_EmitsInitCmd(t *testing.T) {
	st := newTestStyles(t)
	m := views.NewNewReposModal("/tmp/ws", st, nil)
	m.SetSize(120, 40)

	healthMsg := views.WorkspaceHealthCheckMsg{
		Check: domain.WorkspaceHealthCheck{PlainGitClones: []string{"/tmp/ws/repo"}},
	}
	m, _ = m.Update(healthMsg)

	// [y] must produce a non-nil command (initNewReposCmd).
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Fatal("expected non-nil cmd after [y] key")
	}
}

func TestNewReposModal_NewReposInitDoneMsg_SetsInactive(t *testing.T) {
	st := newTestStyles(t)
	m := views.NewNewReposModal("/tmp/ws", st, nil)
	m.SetSize(120, 40)

	healthMsg := views.WorkspaceHealthCheckMsg{
		Check: domain.WorkspaceHealthCheck{PlainGitClones: []string{"/tmp/ws/repo"}},
	}
	m, _ = m.Update(healthMsg)

	updated, _ := m.Update(views.NewReposInitDoneMsg{Count: 1})
	if updated.Active() {
		t.Fatal("expected Active() == false after NewReposInitDoneMsg")
	}
}

// containsSubstring is a test helper; strings.Contains import-free alternative.
func containsSubstring(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || func() bool {
		for i := 0; i <= len(s)-len(sub); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	}())
}

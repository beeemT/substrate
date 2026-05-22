package views_test

import (
	"errors"
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

func TestNewReposModal_ErrMsg_ResetsProgressAndCloses(t *testing.T) {
	st := newTestStyles(t)
	m := views.NewNewReposModal("/tmp/ws", st, nil)
	m.SetSize(120, 40)

	// Advance past loading and start init.
	healthMsg := views.WorkspaceHealthCheckMsg{
		Check: domain.WorkspaceHealthCheck{PlainGitClones: []string{"/tmp/ws/repo1", "/tmp/ws/repo2"}},
	}
	m, _ = m.Update(healthMsg)

	// Start progress tracking.
	progressMsg := views.RepoInitProgressMsg{Initialized: 1, Total: 2}
	m, _ = m.Update(progressMsg)
	if !m.Active() {
		t.Fatal("expected Active() == true before error")
	}

	// Simulate error during init. Modal stays open to show the error.
	updated, _ := m.Update(views.ErrMsg{Err: errors.New("git-work init failed")})
	if !updated.Active() {
		t.Fatal("expected Active() == true after ErrMsg (modal shows error, not closed)")
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

// --- Progress counter tests ---

func TestNewReposModal_RepoInitProgressMsg_UpdatesProgress(t *testing.T) {
	st := newTestStyles(t)
	m := views.NewNewReposModal("/tmp/ws", st, nil)
	m.SetSize(120, 40)

	// First, advance past loading to show the init button.
	healthMsg := views.WorkspaceHealthCheckMsg{
		Check: domain.WorkspaceHealthCheck{PlainGitClones: []string{"/tmp/ws/repo1", "/tmp/ws/repo2", "/tmp/ws/repo3"}},
	}
	m, _ = m.Update(healthMsg)

	// Send progress message for first repo.
	progressMsg := views.RepoInitProgressMsg{Initialized: 1, Total: 3}
	updated, _ := m.Update(progressMsg)

	v := updated.View()
	// View must contain "1/3" progress counter.
	if !containsSubstring(v, "1/3") {
		t.Fatalf("view does not contain progress counter 1/3, got: %s", v)
	}
	// View must contain progress bar characters (█ or ░).
	if !containsSubstring(v, "█") || !containsSubstring(v, "░") {
		t.Fatalf("view does not contain progress bar characters, got: %s", v)
	}
}

func TestNewReposModal_RepoInitProgressMsg_UpdatesCounter(t *testing.T) {
	st := newTestStyles(t)
	m := views.NewNewReposModal("/tmp/ws", st, nil)
	m.SetSize(120, 40)

	// Advance past loading.
	healthMsg := views.WorkspaceHealthCheckMsg{
		Check: domain.WorkspaceHealthCheck{PlainGitClones: []string{"/tmp/ws/repo1", "/tmp/ws/repo2"}},
	}
	m, _ = m.Update(healthMsg)

	// Progress: 2/5 (initial + 2 new repos).
	progressMsg := views.RepoInitProgressMsg{Initialized: 2, Total: 5}
	updated, _ := m.Update(progressMsg)

	v := updated.View()
	if !containsSubstring(v, "2/5") {
		t.Fatalf("view does not contain progress counter 2/5, got: %s", v)
	}
}

func TestNewReposModal_NoProgressCounterBeforeInit(t *testing.T) {
	st := newTestStyles(t)
	m := views.NewNewReposModal("/tmp/ws", st, nil)
	m.SetSize(120, 40)

	// Health check with repos.
	healthMsg := views.WorkspaceHealthCheckMsg{
		Check: domain.WorkspaceHealthCheck{PlainGitClones: []string{"/tmp/ws/repo"}},
	}
	m, _ = m.Update(healthMsg)

	// No progress message sent yet.
	v := m.View()
	// View must NOT contain a progress counter (no "/" followed by digits at end).
	// We check that "1/1" does not appear when progress hasn't started.
	if containsSubstring(v, "1/1") {
		t.Fatalf("view contains unexpected progress counter before init started: %s", v)
	}
}

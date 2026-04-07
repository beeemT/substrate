package views

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/beeemT/substrate/internal/gitwork"
	"github.com/beeemT/substrate/internal/tui/styles"
)

func newTestRepoManagerOverlay() RepoManagerOverlay {
	return NewRepoManagerOverlay("/tmp/workspace", nil, styles.NewStyles(styles.DefaultTheme))
}

// loadTestRepos sends ManagedReposLoadedMsg to m and returns the updated overlay.
// Callers that need worktree state should discard the returned cmd from Update.
func loadTestRepos(m RepoManagerOverlay, repos []managedRepo) RepoManagerOverlay {
	m, _ = m.Update(ManagedReposLoadedMsg{Repos: repos})
	return m
}

func TestRepoManagerOverlayInactiveBeforeOpen(t *testing.T) {
	t.Parallel()
	m := newTestRepoManagerOverlay()
	if m.Active() {
		t.Fatal("expected overlay to be inactive before Open()")
	}
}

func TestRepoManagerOverlayActiveAfterOpen(t *testing.T) {
	t.Parallel()
	m := newTestRepoManagerOverlay()
	_ = m.Open()
	if !m.Active() {
		t.Fatal("expected overlay to be active after Open()")
	}
}

func TestRepoManagerOverlayViewEmptyWhenInactive(t *testing.T) {
	t.Parallel()
	m := newTestRepoManagerOverlay()
	if v := m.View(); v != "" {
		t.Fatalf("View() = %q, want empty when inactive", v)
	}
}

func TestRepoManagerOverlayViewFitsWindow_80x20(t *testing.T) {
	t.Parallel()
	m := newTestRepoManagerOverlay()
	m.SetSize(80, 20)
	_ = m.Open()
	v := m.View()
	if v == "" {
		t.Fatal("View() is empty after Open()")
	}
	assertOverlayFits(t, v, 80, 20)
}

func TestRepoManagerOverlayViewFitsWindow_72x20(t *testing.T) {
	t.Parallel()
	m := newTestRepoManagerOverlay()
	m.SetSize(72, 20)
	_ = m.Open()
	v := m.View()
	if v == "" {
		t.Fatal("View() is empty after Open()")
	}
	assertOverlayFits(t, v, 72, 20)
}

func TestRepoManagerOverlayLoadingState(t *testing.T) {
	t.Parallel()
	m := newTestRepoManagerOverlay()
	m.SetSize(80, 24)
	_ = m.Open()
	// Before any ManagedReposLoadedMsg arrives, overlay is in loading state.
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "Loading") {
		t.Fatalf("view = %q, want loading indicator", v)
	}
}

func TestRepoManagerOverlayEmptyState(t *testing.T) {
	t.Parallel()
	m := newTestRepoManagerOverlay()
	m.SetSize(80, 24)
	_ = m.Open()
	m = loadTestRepos(m, nil)
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "No repositories") {
		t.Fatalf("view = %q, want 'No repositories'", v)
	}
}

func TestRepoManagerOverlayShowsRepoName(t *testing.T) {
	t.Parallel()
	m := newTestRepoManagerOverlay()
	m.SetSize(80, 24)
	_ = m.Open()
	repos := []managedRepo{
		{Path: "/tmp/workspace/myrepo", Name: "myrepo", Kind: repoKindGitWork},
	}
	m = loadTestRepos(m, repos)
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "myrepo") {
		t.Fatalf("view = %q, want repo name 'myrepo'", v)
	}
}

func TestRepoManagerOverlayPlainGitWarning(t *testing.T) {
	t.Parallel()
	m := newTestRepoManagerOverlay()
	m.SetSize(80, 24)
	_ = m.Open()
	repos := []managedRepo{
		{Path: "/tmp/workspace/plainrepo", Name: "plainrepo", Kind: repoKindPlainGit},
	}
	m = loadTestRepos(m, repos)
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "Not managed by substrate") {
		t.Fatalf("view = %q, want plain git warning text 'Not managed by substrate'", v)
	}
}

func TestRepoManagerOverlayDKeyShowsConfirm(t *testing.T) {
	t.Parallel()
	m := newTestRepoManagerOverlay()
	m.SetSize(80, 24)
	_ = m.Open()
	repos := []managedRepo{
		{Path: "/tmp/workspace/myrepo", Name: "myrepo", Kind: repoKindGitWork},
	}
	m = loadTestRepos(m, repos)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "Delete") && !strings.Contains(v, "confirm") {
		t.Fatalf("view = %q, want delete confirmation prompt", v)
	}
}

func TestRepoManagerOverlayNKeyDismissesConfirm(t *testing.T) {
	t.Parallel()
	m := newTestRepoManagerOverlay()
	m.SetSize(80, 24)
	_ = m.Open()
	repos := []managedRepo{
		{Path: "/tmp/workspace/myrepo", Name: "myrepo", Kind: repoKindGitWork},
	}
	m = loadTestRepos(m, repos)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	// 'n' should cancel the confirm dialog and return to normal view.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if m.pendingDelete != nil {
		t.Fatal("pendingDelete should be nil after pressing 'n'")
	}
}

func TestRepoManagerOverlayYKeyFiresRemoveCmd(t *testing.T) {
	t.Parallel()
	m := newTestRepoManagerOverlay()
	m.SetSize(80, 24)
	_ = m.Open()
	repos := []managedRepo{
		{Path: "/tmp/workspace/myrepo", Name: "myrepo", Kind: repoKindGitWork},
	}
	m = loadTestRepos(m, repos)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Fatal("expected RemoveRepoCmd after y key in confirm")
	}
	msg := cmd()
	removed, ok := msg.(RepoRemovedMsg)
	if !ok {
		t.Fatalf("cmd() = %T, want RepoRemovedMsg", msg)
	}
	if removed.RepoPath != "/tmp/workspace/myrepo" {
		t.Fatalf("RepoRemovedMsg.RepoPath = %q, want %q", removed.RepoPath, "/tmp/workspace/myrepo")
	}
}

func TestRepoManagerOverlayEscCloses(t *testing.T) {
	t.Parallel()
	m := newTestRepoManagerOverlay()
	m.SetSize(80, 24)
	_ = m.Open()
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("expected cmd after Esc")
	}
	msg := cmd()
	if _, ok := msg.(CloseOverlayMsg); !ok {
		t.Fatalf("cmd() = %T, want CloseOverlayMsg", msg)
	}
}

func TestRepoManagerOverlayAKeyEmitsShowAddRepo(t *testing.T) {
	t.Parallel()
	m := newTestRepoManagerOverlay()
	m.SetSize(80, 24)
	_ = m.Open()
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if cmd == nil {
		t.Fatal("expected cmd after 'a' key")
	}
	msg := cmd()
	if _, ok := msg.(ShowAddRepoMsg); !ok {
		t.Fatalf("cmd() = %T, want ShowAddRepoMsg", msg)
	}
}

func TestRepoManagerOverlayWorktreeLoading(t *testing.T) {
	t.Parallel()
	m := newTestRepoManagerOverlay()
	m.SetSize(80, 24)
	_ = m.Open()
	repos := []managedRepo{
		{Path: "/tmp/workspace/myrepo", Name: "myrepo", Kind: repoKindGitWork},
	}
	// After sending ManagedReposLoadedMsg, maybeLoadWorktrees fires LoadWorktreesCmd
	// and sets worktreeLoading = true. We discard the cmd — result hasn't arrived yet.
	m, _ = m.Update(ManagedReposLoadedMsg{Repos: repos})
	if !m.worktreeLoading {
		t.Fatal("expected worktreeLoading = true after repos loaded, before worktrees arrive")
	}
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "Loading") {
		t.Fatalf("view = %q, want worktree loading indicator", v)
	}
}

func TestRepoManagerOverlayWorktreeLoaded(t *testing.T) {
	t.Parallel()
	m := newTestRepoManagerOverlay()
	m.SetSize(80, 24)
	_ = m.Open()
	repos := []managedRepo{
		{Path: "/tmp/workspace/myrepo", Name: "myrepo", Kind: repoKindGitWork},
	}
	// ManagedReposLoadedMsg increments worktreeReqID to 1 via nextWorktreeRequestID.
	m, _ = m.Update(ManagedReposLoadedMsg{Repos: repos})
	m, _ = m.Update(WorktreesLoadedMsg{
		RequestID: 1,
		RepoPath:  "/tmp/workspace/myrepo",
		Worktrees: []gitwork.Worktree{
			{Path: "/tmp/workspace/myrepo/main", Branch: "main", IsMain: true},
		},
	})
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "main") {
		t.Fatalf("view = %q, want branch name 'main'", v)
	}
}

func TestRepoManagerOverlayStaleWorktreeDiscarded(t *testing.T) {
	t.Parallel()
	m := newTestRepoManagerOverlay()
	m.SetSize(80, 24)
	_ = m.Open()
	repos := []managedRepo{
		{Path: "/tmp/workspace/myrepo", Name: "myrepo", Kind: repoKindGitWork},
	}
	m, _ = m.Update(ManagedReposLoadedMsg{Repos: repos})
	// worktreeReqID is now 1; send a stale response with ID=99.
	m, _ = m.Update(WorktreesLoadedMsg{
		RequestID: 99,
		RepoPath:  "/tmp/workspace/myrepo",
		Worktrees: []gitwork.Worktree{
			{Path: "/tmp/workspace/myrepo/stale", Branch: "stale-branch", IsMain: false},
		},
	})
	// worktreeLoading should still be true (stale msg discarded), worktrees nil.
	if !m.worktreeLoading {
		t.Fatal("worktreeLoading should remain true after stale response")
	}
	if len(m.worktrees) != 0 {
		t.Fatalf("worktrees = %v, want empty after stale response discarded", m.worktrees)
	}
}

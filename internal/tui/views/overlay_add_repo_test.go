package views_test

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/tui/views"
)

// mockRepoSource implements adapter.RepoSource for tests.
type mockRepoSource struct {
	name  string
	repos []adapter.RepoItem
	err   error
}

func (m *mockRepoSource) Name() string { return m.name }

func (m *mockRepoSource) ListRepos(_ context.Context, _ adapter.RepoListOpts) (*adapter.RepoListResult, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &adapter.RepoListResult{Repos: m.repos, HasMore: false}, nil
}

func testRepos() []adapter.RepoItem {
	return []adapter.RepoItem{
		{
			Name: "substrate", FullName: "beeemT/substrate", Description: "A TUI app",
			URL: "https://github.com/beeemT/substrate.git", SSHURL: "git@github.com:beeemT/substrate.git",
			DefaultBranch: "main", IsPrivate: false, Source: "github", Owner: "beeemT",
		},
		{
			Name: "cli", FullName: "beeemT/cli", Description: "A CLI tool",
			URL: "https://github.com/beeemT/cli.git", SSHURL: "git@github.com:beeemT/cli.git",
			DefaultBranch: "main", IsPrivate: true, Source: "github", Owner: "beeemT",
		},
	}
}

func newAddRepoOverlay(t *testing.T, sources []adapter.RepoSource) views.AddRepoOverlay {
	t.Helper()
	return views.NewAddRepoOverlay(sources, "/tmp/workspace", nil, newTestStyles(t))
}

func TestAddRepoOverlay_ActiveAfterOpen(t *testing.T) {
	t.Parallel()

	m := newAddRepoOverlay(t, nil)
	m.Open()

	if !m.Active() {
		t.Fatal("expected Active() == true after Open")
	}
}

func TestAddRepoOverlay_InactiveAfterClose(t *testing.T) {
	t.Parallel()

	m := newAddRepoOverlay(t, nil)
	m.Open()
	m.Close()

	if m.Active() {
		t.Fatal("expected Active() == false after Close")
	}
}

func TestAddRepoOverlay_ViewEmptyWhenInactive(t *testing.T) {
	t.Parallel()

	m := newAddRepoOverlay(t, nil)
	v := m.View()

	if v != "" {
		t.Fatalf("expected empty View() when inactive, got %q", v)
	}
}

func TestAddRepoOverlay_ViewFitsSize_120x40(t *testing.T) {
	t.Parallel()

	m := newAddRepoOverlay(t, []adapter.RepoSource{&mockRepoSource{name: "github", repos: testRepos()}})
	m.SetSize(120, 40)
	m.Open()

	// Simulate repos being loaded.
	m, _ = m.Update(views.RepoListLoadedMsg{Repos: testRepos(), HasMore: false})

	v := m.View()
	if v == "" {
		t.Fatal("expected non-empty View()")
	}
	assertViewFitsSize(t, v, 120, 40)
}

func TestAddRepoOverlay_ViewFitsSize_60x20(t *testing.T) {
	t.Parallel()

	m := newAddRepoOverlay(t, []adapter.RepoSource{&mockRepoSource{name: "github", repos: testRepos()}})
	m.SetSize(60, 20)
	m.Open()

	m, _ = m.Update(views.RepoListLoadedMsg{Repos: testRepos(), HasMore: false})

	v := m.View()
	if v == "" {
		t.Fatal("expected non-empty View()")
	}
	assertViewFitsSize(t, v, 60, 20)
}

func TestAddRepoOverlay_EscCloses(t *testing.T) {
	t.Parallel()

	m := newAddRepoOverlay(t, []adapter.RepoSource{&mockRepoSource{name: "github"}})
	m.SetSize(120, 40)
	m.Open()

	escKey := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e', 's', 'c'}}
	_, cmd := m.Update(escKey)

	if cmd == nil {
		t.Fatal("expected non-nil cmd after Esc key")
	}
	msg := cmd()
	if _, ok := msg.(views.CloseOverlayMsg); !ok {
		t.Fatalf("expected CloseOverlayMsg, got %T", msg)
	}
}

func TestAddRepoOverlay_ManualModeToggle(t *testing.T) {
	t.Parallel()

	m := newAddRepoOverlay(t, []adapter.RepoSource{&mockRepoSource{name: "github"}})
	m.SetSize(120, 40)
	m.Open()

	ctrlN := tea.KeyMsg{Type: tea.KeyCtrlN}
	m, _ = m.Update(ctrlN)

	v := m.View()
	plain := ansi.Strip(v)
	if !strings.Contains(plain, "Clone URL:") {
		t.Error("expected View to contain 'Clone URL:' after toggling manual mode")
	}
}

func TestAddRepoOverlay_SourceCycling(t *testing.T) {
	t.Parallel()

	sources := []adapter.RepoSource{
		&mockRepoSource{name: "github"},
		&mockRepoSource{name: "gitlab"},
	}
	m := newAddRepoOverlay(t, sources)
	m.SetSize(120, 40)
	m.Open()

	tabKey := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t', 'a', 'b'}}

	// Tab once: should cycle from index 0 to 1.
	m, _ = m.Update(tabKey)
	v1 := ansi.Strip(m.View())
	if !strings.Contains(v1, "gitlab") {
		t.Error("expected 'gitlab' in view after first Tab")
	}

	// Tab again: should wrap back to index 0.
	m, _ = m.Update(tabKey)
	v2 := ansi.Strip(m.View())
	if !strings.Contains(v2, "github") {
		t.Error("expected 'github' in view after second Tab (wrap)")
	}
}

func TestAddRepoOverlay_ViewContainsTitle(t *testing.T) {
	t.Parallel()

	m := newAddRepoOverlay(t, []adapter.RepoSource{&mockRepoSource{name: "github"}})
	m.SetSize(120, 40)
	m.Open()

	v := m.View()
	plain := ansi.Strip(v)
	if !strings.Contains(plain, "Browse Repositories") {
		t.Error("expected View to contain 'Browse Repositories' title")
	}
}

func TestAddRepoOverlay_LoadingState(t *testing.T) {
	t.Parallel()

	m := newAddRepoOverlay(t, []adapter.RepoSource{&mockRepoSource{name: "github"}})
	m.SetSize(120, 40)
	m.Open()

	// Before sending RepoListLoadedMsg, the view should show a loading state.
	v := m.View()
	plain := ansi.Strip(v)
	if !strings.Contains(plain, "Loading") {
		t.Error("expected View to show loading state before repos are loaded")
	}
}

func TestAddRepoOverlay_DetailPaneContent(t *testing.T) {
	t.Parallel()

	repos := testRepos()
	m := newAddRepoOverlay(t, []adapter.RepoSource{&mockRepoSource{name: "github", repos: repos}})
	m.SetSize(120, 40)
	m.Open()

	m, _ = m.Update(views.RepoListLoadedMsg{Repos: repos, HasMore: false})

	v := m.View()
	plain := ansi.Strip(v)

	// The detail pane should show info about the first (selected) repo.
	if !strings.Contains(plain, "beeemT/substrate") {
		t.Error("expected detail pane to contain selected repo full name")
	}
	if !strings.Contains(plain, "main") {
		t.Error("expected detail pane to contain default branch")
	}
	if !strings.Contains(plain, "Public") {
		t.Error("expected detail pane to show 'Public' access level")
	}
}

func TestAddRepoOverlay_EmptyDetailWhenNoSelection(t *testing.T) {
	t.Parallel()

	m := newAddRepoOverlay(t, []adapter.RepoSource{&mockRepoSource{name: "github"}})
	m.SetSize(120, 40)
	m.Open()

	m, _ = m.Update(views.RepoListLoadedMsg{Repos: nil, HasMore: false})

	v := m.View()
	plain := ansi.Strip(v)

	if !strings.Contains(plain, "No repository selected") {
		t.Error("expected detail pane to show empty-state message when no repos loaded")
	}
}

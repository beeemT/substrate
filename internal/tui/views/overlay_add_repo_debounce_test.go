package views

import (
	"context"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/tui/styles"
)

// debounceTestRepos returns a minimal repo list for debounce tests.
func debounceTestRepos() []adapter.RepoItem {
	return []adapter.RepoItem{
		{Name: "substrate", FullName: "beeemT/substrate", URL: "https://github.com/beeemT/substrate.git"},
		{Name: "cli", FullName: "beeemT/cli", URL: "https://github.com/beeemT/cli.git"},
	}
}

// debounceRepoSource is a minimal adapter.RepoSource for debounce tests.
type debounceRepoSource struct{}

func (d *debounceRepoSource) Name() string { return "github" }
func (d *debounceRepoSource) ListRepos(_ context.Context, _ adapter.RepoListOpts) (*adapter.RepoListResult, error) {
	return &adapter.RepoListResult{Repos: debounceTestRepos()}, nil
}

// newDebounceTestOverlay builds a ready-to-use AddRepoOverlay for debounce tests.
func newDebounceTestOverlay(t *testing.T) AddRepoOverlay {
	t.Helper()
	st := styles.NewStyles(styles.DefaultTheme)
	m := NewAddRepoOverlay([]adapter.RepoSource{&debounceRepoSource{}}, "/tmp/workspace", nil, st)
	m.SetSize(120, 40)
	m.Open(nil)
	m, _ = m.Update(RepoListLoadedMsg{Repos: debounceTestRepos(), HasMore: false})
	return m
}

// TestAddRepoOverlay_SearchDebounce_SeqIncrements asserts that typing a character
// while the search control is focused increments searchDebounceSeq and returns
// a non-nil command (the debounce tick).
func TestAddRepoOverlay_SearchDebounce_SeqIncrements(t *testing.T) {
	t.Parallel()

	m := newDebounceTestOverlay(t)

	keyR := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}}
	postKey, cmd := m.Update(keyR)

	if cmd == nil {
		t.Fatal("expected a command after typing in search bar")
	}
	if postKey.searchDebounceSeq != 1 {
		t.Fatalf("searchDebounceSeq = %d, want 1 after first keystroke", postKey.searchDebounceSeq)
	}
}

// TestAddRepoOverlay_SearchDebounce_FiresOnMatch asserts that delivering an
// addRepoDebounceMsg with the current seq triggers a reload command.
func TestAddRepoOverlay_SearchDebounce_FiresOnMatch(t *testing.T) {
	t.Parallel()

	m := newDebounceTestOverlay(t)

	keyR := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}}
	postKey, _ := m.Update(keyR)

	// Inject the debounce message directly so the test does not need a real timer.
	_, reloadCmd := postKey.Update(addRepoDebounceMsg{seq: postKey.searchDebounceSeq})
	if reloadCmd == nil {
		t.Fatal("expected a reload command after debounce message with matching seq")
	}
}

// TestAddRepoOverlay_SearchDebounce_StaleDiscarded asserts that a debounce
// message with an outdated seq (user typed again) is dropped without issuing a
// reload.
func TestAddRepoOverlay_SearchDebounce_StaleDiscarded(t *testing.T) {
	t.Parallel()

	m := newDebounceTestOverlay(t)

	keyR := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}}
	keyS := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}}

	// Two keystrokes: seq becomes 2.
	postKey1, _ := m.Update(keyR)        // seq → 1
	postKey2, _ := postKey1.Update(keyS) // seq → 2

	// Deliver stale seq=1 to the model at seq=2.
	_, staleCmd := postKey2.Update(addRepoDebounceMsg{seq: 1})
	if staleCmd != nil {
		msg := staleCmd()
		if _, ok := msg.(RepoListLoadedMsg); ok {
			t.Error("stale debounce message must not trigger a reload")
		}
	}
}

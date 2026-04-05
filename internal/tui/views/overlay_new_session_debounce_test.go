package views

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/tui/styles"
)

// newDebounceTestNewSessionOverlay builds a ready-to-use NewSessionOverlay for
// debounce tests: opened, sized, and pre-loaded with two items so the search
// control is meaningful.
func newDebounceTestNewSessionOverlay(t *testing.T) NewSessionOverlay {
	t.Helper()
	src := &browseTestAdapter{
		name:         "github",
		browseScopes: []domain.SelectionScope{domain.ScopeIssues},
		browseFilters: map[domain.SelectionScope]adapter.BrowseFilterCapabilities{
			domain.ScopeIssues: {},
		},
	}
	overlay := NewNewSessionOverlay([]adapter.WorkItemAdapter{src}, "ws-1", styles.NewStyles(styles.DefaultTheme))
	overlay.Open()
	overlay.SetSize(120, 40)
	overlay, _ = overlay.Update(loadedMsg(
		adapter.ListItem{ID: "1", Provider: "github", Title: "fix login"},
		adapter.ListItem{ID: "2", Provider: "github", Title: "add tests"},
	))
	return overlay
}

// TestNewSessionOverlay_SearchDebounce_SeqIncrements asserts that typing into
// the search control increments browseDebounceSeq and returns a non-nil command.
func TestNewSessionOverlay_SearchDebounce_SeqIncrements(t *testing.T) {
	t.Parallel()

	overlay := newDebounceTestNewSessionOverlay(t)

	keyR := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}}
	postKey, cmd := overlay.Update(keyR)

	if cmd == nil {
		t.Fatal("expected a command after typing in search bar")
	}
	if postKey.browseDebounceSeq != 1 {
		t.Fatalf("browseDebounceSeq = %d, want 1 after first keystroke", postKey.browseDebounceSeq)
	}
}

// TestNewSessionOverlay_SearchDebounce_FiresOnMatch asserts that delivering a
// browseDebounceMsg with the current seq triggers a reload command.
func TestNewSessionOverlay_SearchDebounce_FiresOnMatch(t *testing.T) {
	t.Parallel()

	overlay := newDebounceTestNewSessionOverlay(t)

	keyR := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}}
	postKey, _ := overlay.Update(keyR)

	_, reloadCmd := postKey.Update(browseDebounceMsg{seq: postKey.browseDebounceSeq})
	if reloadCmd == nil {
		t.Fatal("expected a reload command after debounce message with matching seq")
	}
}

// TestNewSessionOverlay_SearchDebounce_StaleDiscarded asserts that a debounce
// message with an outdated seq is dropped without issuing a reload.
func TestNewSessionOverlay_SearchDebounce_StaleDiscarded(t *testing.T) {
	t.Parallel()

	overlay := newDebounceTestNewSessionOverlay(t)

	keyR := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}}
	keyS := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}}

	postKey1, _ := overlay.Update(keyR)  // seq → 1
	postKey2, _ := postKey1.Update(keyS) // seq → 2

	// Deliver stale seq=1 to the model at seq=2.
	_, staleCmd := postKey2.Update(browseDebounceMsg{seq: 1})
	if staleCmd != nil {
		msg := staleCmd()
		if _, ok := msg.(issueListLoadedMsg); ok {
			t.Error("stale debounce message must not trigger a reload")
		}
	}
}

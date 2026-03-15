package views

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/tui/styles"
)

func TestSessionSearchOverlaySortsByUpdatedAt(t *testing.T) {
	overlay := NewSessionSearchOverlay(styles.NewStyles(styles.DefaultTheme))
	overlay.Open(sessionHistoryScopeGlobal, false)

	older := time.Now().Add(-time.Hour)
	newer := time.Now()
	overlay.SetEntries([]domain.SessionHistoryEntry{
		{WorkItemID: "wi-old", WorkItemTitle: "Old", UpdatedAt: older, CreatedAt: older},
		{WorkItemID: "wi-new", WorkItemTitle: "New", UpdatedAt: newer, CreatedAt: older},
	})

	sel := overlay.Selected()
	if sel == nil {
		t.Fatal("selected entry = nil")
	}
	if sel.WorkItemID != "wi-new" {
		t.Fatalf("selected work item = %q, want wi-new", sel.WorkItemID)
	}
}

func TestSessionSearchOverlayInputChangeEnqueuesDebouncedSearch(t *testing.T) {
	overlay := NewSessionSearchOverlay(styles.NewStyles(styles.DefaultTheme))
	overlay.Open(sessionHistoryScopeGlobal, false)

	// Keystroke must return a debounce tick cmd, not an immediate search.
	updated, cmd := overlay.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if cmd == nil {
		t.Fatal("expected debounce tick cmd after input change")
	}
	if got := updated.Filter("").Search; got != "r" {
		t.Fatalf("filter search = %q, want r", got)
	}

	// Firing the debounce msg with the matching seq must trigger a search.
	updated2, searchCmd := updated.Update(sessionSearchDebounceMsg{seq: updated.searchDebounceSeq})
	if searchCmd == nil {
		t.Fatal("debounce msg with current seq must yield a search request cmd")
	}
	if _, ok := searchCmd().(SessionHistorySearchRequestedMsg); !ok {
		t.Fatalf("searchCmd() = %T, want SessionHistorySearchRequestedMsg", searchCmd())
	}
	_ = updated2
}

func TestSessionSearchOverlayEnterOpensSelection(t *testing.T) {
	overlay := NewSessionSearchOverlay(styles.NewStyles(styles.DefaultTheme))
	overlay.Open(sessionHistoryScopeGlobal, true)
	overlay.SetEntries([]domain.SessionHistoryEntry{{
		SessionID:          "sess-1",
		WorkspaceID:        "ws-1",
		WorkspaceName:      "workspace",
		WorkItemID:         "wi-1",
		WorkItemExternalID: "SUB-1",
		WorkItemTitle:      "Work item",
		UpdatedAt:          time.Now(),
		CreatedAt:          time.Now(),
	}})

	overlay, _ = overlay.Update(tea.KeyMsg{Type: tea.KeyTab})
	updated, cmd := overlay.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if updated.focus != sessionSearchFocusResults {
		t.Fatalf("focus = %v, want results", updated.focus)
	}
	if cmd == nil {
		t.Fatal("expected open command")
	}
	msg := cmd()
	openMsg, ok := msg.(OpenSessionHistoryMsg)
	if !ok {
		t.Fatalf("cmd() message = %T, want OpenSessionHistoryMsg", msg)
	}
	if openMsg.Entry.WorkItemID != "wi-1" {
		t.Fatalf("opened work item = %q, want wi-1", openMsg.Entry.WorkItemID)
	}
}

func TestSessionSearchOverlayDeleteRequestsConfirmation(t *testing.T) {
	overlay := NewSessionSearchOverlay(styles.NewStyles(styles.DefaultTheme))
	overlay.Open(sessionHistoryScopeGlobal, true)
	overlay.SetEntries([]domain.SessionHistoryEntry{{
		SessionID:          "sess-1",
		WorkspaceID:        "ws-1",
		WorkspaceName:      "workspace",
		WorkItemID:         "wi-1",
		WorkItemExternalID: "SUB-1",
		WorkItemTitle:      "Work item",
		UpdatedAt:          time.Now(),
		CreatedAt:          time.Now(),
	}})

	overlay, _ = overlay.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated, cmd := overlay.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if updated.focus != sessionSearchFocusResults {
		t.Fatalf("focus = %v, want results", updated.focus)
	}
	if cmd == nil {
		t.Fatal("expected delete confirmation command")
	}
	msg := cmd()
	confirmMsg, ok := msg.(ConfirmDeleteSessionMsg)
	if !ok {
		t.Fatalf("cmd() message = %T, want ConfirmDeleteSessionMsg", msg)
	}
	if confirmMsg.SessionID != "wi-1" {
		t.Fatalf("session id = %q, want wi-1", confirmMsg.SessionID)
	}
}

func TestSessionSearchOverlayArrowKeysMoveFocus(t *testing.T) {
	overlay := NewSessionSearchOverlay(styles.NewStyles(styles.DefaultTheme))
	overlay.Open(sessionHistoryScopeGlobal, true)
	overlay.SetEntries([]domain.SessionHistoryEntry{{
		SessionID:          "sess-1",
		WorkspaceID:        "ws-1",
		WorkspaceName:      "workspace",
		WorkItemID:         "wi-1",
		WorkItemExternalID: "SUB-1",
		WorkItemTitle:      "Work item",
		UpdatedAt:          time.Now(),
		CreatedAt:          time.Now(),
	}})

	updated, _ := overlay.Update(tea.KeyMsg{Type: tea.KeyDown})
	if updated.focus != sessionSearchFocusResults {
		t.Fatalf("focus after down = %v, want results", updated.focus)
	}
	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRight})
	if updated.focus != sessionSearchFocusPreview {
		t.Fatalf("focus after right = %v, want preview", updated.focus)
	}
	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if updated.focus != sessionSearchFocusResults {
		t.Fatalf("focus after left = %v, want results", updated.focus)
	}
	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if updated.focus != sessionSearchFocusInput {
		t.Fatalf("focus after second left = %v, want input", updated.focus)
	}
}

func TestSessionSearchOverlayUpArrowReturnsToInputFromTopResult(t *testing.T) {
	overlay := NewSessionSearchOverlay(styles.NewStyles(styles.DefaultTheme))
	overlay.Open(sessionHistoryScopeGlobal, true)
	overlay.SetEntries([]domain.SessionHistoryEntry{{
		SessionID:          "sess-1",
		WorkspaceID:        "ws-1",
		WorkspaceName:      "workspace",
		WorkItemID:         "wi-1",
		WorkItemExternalID: "SUB-1",
		WorkItemTitle:      "Work item",
		UpdatedAt:          time.Now(),
		CreatedAt:          time.Now(),
	}})

	updated, _ := overlay.Update(tea.KeyMsg{Type: tea.KeyDown})
	if updated.focus != sessionSearchFocusResults {
		t.Fatalf("focus after down = %v, want results", updated.focus)
	}
	if updated.list.Index() != 0 {
		t.Fatalf("list index after down = %d, want 0", updated.list.Index())
	}

	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyUp})
	if updated.focus != sessionSearchFocusInput {
		t.Fatalf("focus after up = %v, want input", updated.focus)
	}
}

func TestSessionSearchOverlayArrowKeysToggleScope(t *testing.T) {
	overlay := NewSessionSearchOverlay(styles.NewStyles(styles.DefaultTheme))
	overlay.Open(sessionHistoryScopeGlobal, true)

	updated, cmd := overlay.Update(tea.KeyMsg{Type: tea.KeyUp})
	if updated.focus != sessionSearchFocusScope {
		t.Fatalf("focus after up = %v, want scope", updated.focus)
	}
	if cmd != nil {
		t.Fatalf("cmd after up = %v, want nil", cmd)
	}

	updated, cmd = updated.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if updated.Scope() != sessionHistoryScopeWorkspace {
		t.Fatalf("scope after left = %v, want workspace", updated.Scope())
	}
	assertSessionSearchRequestCmd(t, cmd)

	updated, cmd = updated.Update(tea.KeyMsg{Type: tea.KeyRight})
	if updated.Scope() != sessionHistoryScopeGlobal {
		t.Fatalf("scope after right = %v, want global", updated.Scope())
	}
	assertSessionSearchRequestCmd(t, cmd)

	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	if updated.focus != sessionSearchFocusInput {
		t.Fatalf("focus after down = %v, want input", updated.focus)
	}
}

func TestSessionSearchOverlayKeepsScopeFocusWhenToggleReturnsNoResults(t *testing.T) {
	overlay := NewSessionSearchOverlay(styles.NewStyles(styles.DefaultTheme))
	overlay.Open(sessionHistoryScopeGlobal, true)

	updated, _ := overlay.Update(tea.KeyMsg{Type: tea.KeyUp})
	if updated.focus != sessionSearchFocusScope {
		t.Fatalf("focus after up = %v, want scope", updated.focus)
	}

	updated.SetEntries(nil)
	if updated.focus != sessionSearchFocusScope {
		t.Fatalf("focus after empty results = %v, want scope", updated.focus)
	}
}

func TestSessionSearchOverlayClearsPreviewFocusWhenResultsDisappear(t *testing.T) {
	overlay := NewSessionSearchOverlay(styles.NewStyles(styles.DefaultTheme))
	overlay.Open(sessionHistoryScopeGlobal, true)
	overlay.SetEntries([]domain.SessionHistoryEntry{{
		SessionID:          "sess-1",
		WorkspaceID:        "ws-1",
		WorkspaceName:      "workspace",
		WorkItemID:         "wi-1",
		WorkItemExternalID: "SUB-1",
		WorkItemTitle:      "Work item",
		UpdatedAt:          time.Now(),
		CreatedAt:          time.Now(),
	}})

	updated, _ := overlay.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRight})
	if updated.focus != sessionSearchFocusPreview {
		t.Fatalf("focus before clear = %v, want preview", updated.focus)
	}

	updated.SetEntries(nil)
	if updated.focus != sessionSearchFocusInput {
		t.Fatalf("focus after clear = %v, want input", updated.focus)
	}
}

func assertSessionSearchRequestCmd(t *testing.T, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected search request command")
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, batchCmd := range batch {
			if batchCmd == nil {
				continue
			}
			if _, ok := batchCmd().(SessionHistorySearchRequestedMsg); ok {
				return
			}
		}
	} else if _, ok := msg.(SessionHistorySearchRequestedMsg); ok {
		return
	}
	t.Fatalf("cmd() message = %T, want SessionHistorySearchRequestedMsg in response", msg)
}

func TestSessionSearchOverlayViewFitsSmallWindow(t *testing.T) {
	overlay := NewSessionSearchOverlay(styles.NewStyles(styles.DefaultTheme))
	overlay.Open(sessionHistoryScopeWorkspace, true)
	overlay.SetSize(72, 18)
	overlay.SetEntries([]domain.SessionHistoryEntry{{
		SessionID:          "sess-overflow-check",
		WorkspaceID:        "ws-1",
		WorkspaceName:      "workspace-with-a-very-long-name",
		WorkItemID:         "wi-1",
		WorkItemExternalID: "SUBSTRATE-12345",
		WorkItemTitle:      "A very long work item title that should wrap inside the preview pane instead of overflowing the terminal width",
		WorkItemState:      domain.SessionImplementing,
		RepositoryName:     "repository-name-that-is-deliberately-long",
		HarnessName:        "claude-sonnet-4",
		Status:             domain.AgentSessionWaitingForAnswer,
		AgentSessionCount:  3,
		HasOpenQuestion:    true,
		UpdatedAt:          time.Now(),
		CreatedAt:          time.Now(),
	}})

	view := overlay.View()
	assertOverlayFits(t, view, 72, 18)
}

func TestSessionSearchOverlayViewHeightStableUnderLoading(t *testing.T) {
	overlay := NewSessionSearchOverlay(styles.NewStyles(styles.DefaultTheme))
	overlay.Open(sessionHistoryScopeGlobal, false)
	overlay.SetSize(120, 30)

	// loading=true (set by Open)
	viewLoading := overlay.View()
	linesLoading := len(strings.Split(viewLoading, "\n"))

	// loading=false
	overlay.SetLoading(false)
	viewNotLoading := overlay.View()
	linesNotLoading := len(strings.Split(viewNotLoading, "\n"))

	if linesLoading != linesNotLoading {
		t.Fatalf("view height changes with loading state: loading=%d lines, not-loading=%d lines", linesLoading, linesNotLoading)
	}
}

func TestSessionSearchOverlaySpinnerTickActivatesSpinner(t *testing.T) {
	overlay := NewSessionSearchOverlay(styles.NewStyles(styles.DefaultTheme))
	overlay.Open(sessionHistoryScopeGlobal, false)
	overlay.SetSize(120, 30)
	overlay.SetLoading(true)

	if overlay.spinnerVisible {
		t.Fatal("spinner must not be visible immediately after SetLoading(true)")
	}

	updated, cmd := overlay.Update(sessionSearchSpinnerTickMsg{})
	if !updated.spinnerVisible {
		t.Fatal("spinner must be visible after first spinner tick msg")
	}
	if cmd == nil {
		t.Fatal("spinner tick must return a follow-up tick command to animate")
	}
}

func TestSessionSearchOverlaySpinnerHiddenWhenLoadingStopped(t *testing.T) {
	overlay := NewSessionSearchOverlay(styles.NewStyles(styles.DefaultTheme))
	overlay.Open(sessionHistoryScopeGlobal, false)
	overlay.SetSize(120, 30)
	overlay.SetLoading(true)

	// activate spinner
	updated, _ := overlay.Update(sessionSearchSpinnerTickMsg{})
	if !updated.spinnerVisible {
		t.Fatal("spinner must be visible after tick")
	}

	// stop loading
	updated.SetLoading(false)
	if updated.spinnerVisible {
		t.Fatal("spinner must be hidden after SetLoading(false)")
	}

	// stale tick after loading stops must be a no-op
	updated2, cmd := updated.Update(sessionSearchSpinnerTickMsg{})
	if updated2.spinnerVisible {
		t.Fatal("stale spinner tick must not show spinner after loading stopped")
	}
	if cmd != nil {
		t.Fatal("stale spinner tick while not loading must return nil cmd")
	}
}

func TestSessionSearchOverlayViewWithSpinnerFitsSmallWindow(t *testing.T) {
	overlay := NewSessionSearchOverlay(styles.NewStyles(styles.DefaultTheme))
	overlay.Open(sessionHistoryScopeWorkspace, true)
	overlay.SetSize(72, 18)
	overlay.spinnerVisible = true
	overlay.spinnerFrame = 0
	view := overlay.View()
	assertOverlayFits(t, view, 72, 18)
}

func TestSessionSearchOverlayDebounceDiscardsStaleSeq(t *testing.T) {
	overlay := NewSessionSearchOverlay(styles.NewStyles(styles.DefaultTheme))
	overlay.Open(sessionHistoryScopeGlobal, false)

	// First keystroke.
	updated, _ := overlay.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	seq1 := updated.searchDebounceSeq

	// Second keystroke before first debounce fires.
	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	seq2 := updated.searchDebounceSeq

	if seq2 <= seq1 {
		t.Fatalf("second keystroke must increment debounce seq: seq1=%d seq2=%d", seq1, seq2)
	}

	// Stale tick from the first keystroke must be a no-op.
	_, stalecmd := updated.Update(sessionSearchDebounceMsg{seq: seq1})
	if stalecmd != nil {
		t.Fatal("stale debounce msg must not trigger a search request")
	}

	// Current tick must fire the search.
	_, livecmd := updated.Update(sessionSearchDebounceMsg{seq: seq2})
	if livecmd == nil {
		t.Fatal("current debounce msg must trigger a search request cmd")
	}
	if _, ok := livecmd().(SessionHistorySearchRequestedMsg); !ok {
		t.Fatalf("livecmd() = %T, want SessionHistorySearchRequestedMsg", livecmd())
	}
}

func TestSessionSearchOverlayNoLoadingTextInDetailOrList(t *testing.T) {
	overlay := NewSessionSearchOverlay(styles.NewStyles(styles.DefaultTheme))
	overlay.Open(sessionHistoryScopeGlobal, false)
	overlay.SetSize(120, 30)

	if !overlay.loading {
		t.Fatal("expected overlay in loading state after Open")
	}

	// Detail pane must not show 'Loading' text; spinner communicates progress instead.
	if content := overlay.detailContent(); strings.Contains(content, "Loading") {
		t.Fatalf("detailContent() must not contain 'Loading' while loading: %q", content)
	}

	// View must not swap the list for a 'Loading…' placeholder.
	view := overlay.View()
	if strings.Contains(view, "Loading") {
		t.Fatalf("View() must not contain 'Loading' text while loading: %q", view)
	}
}

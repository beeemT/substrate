package views

import (
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

func TestSessionSearchOverlayInputChangeRequestsSearch(t *testing.T) {
	overlay := NewSessionSearchOverlay(styles.NewStyles(styles.DefaultTheme))
	overlay.Open(sessionHistoryScopeGlobal, false)

	updated, cmd := overlay.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if cmd == nil {
		t.Fatal("expected search request command")
	}
	msg := cmd()
	foundRequest := false
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, batchCmd := range batch {
			if batchCmd == nil {
				continue
			}
			if _, ok := batchCmd().(SessionHistorySearchRequestedMsg); ok {
				foundRequest = true
			}
		}
	} else if _, ok := msg.(SessionHistorySearchRequestedMsg); ok {
		foundRequest = true
	}
	if !foundRequest {
		t.Fatalf("cmd() message = %T, want SessionHistorySearchRequestedMsg in response", msg)
	}
	if got := updated.Filter("").Search; got != "r" {
		t.Fatalf("filter search = %q, want r", got)
	}
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

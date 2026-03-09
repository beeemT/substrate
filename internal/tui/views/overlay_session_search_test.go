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
		{SessionID: "old", WorkItemTitle: "Old", UpdatedAt: older, CreatedAt: older},
		{SessionID: "new", WorkItemTitle: "New", UpdatedAt: newer, CreatedAt: older},
	})

	sel := overlay.Selected()
	if sel == nil {
		t.Fatal("selected entry = nil")
	}
	if sel.SessionID != "new" {
		t.Fatalf("selected session = %q, want new", sel.SessionID)
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
	if openMsg.Entry.SessionID != "sess-1" {
		t.Fatalf("opened session = %q, want sess-1", openMsg.Entry.SessionID)
	}
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
		RepositoryName:     "repository-name-that-is-deliberately-long",
		HarnessName:        "claude-sonnet-4",
		Status:             domain.AgentSessionWaitingForAnswer,
		UpdatedAt:          time.Now(),
		CreatedAt:          time.Now(),
	}})

	view := overlay.View()
	assertOverlayFits(t, view, 72, 18)
}

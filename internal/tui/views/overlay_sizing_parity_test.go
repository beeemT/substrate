package views

import (
	"testing"
	"time"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/tui/styles"
)

func TestSplitOverlaySizingSpecsMatchNewSession(t *testing.T) {
	t.Parallel()

	if addRepoSizingSpec != browseSizingSpec {
		t.Fatalf("add repo sizing spec = %+v, want %+v", addRepoSizingSpec, browseSizingSpec)
	}
	if sessionSearchSizingSpec != browseSizingSpec {
		t.Fatalf("session search sizing spec = %+v, want %+v", sessionSearchSizingSpec, browseSizingSpec)
	}
	if newSessionAutonomousSizingSpec != browseSizingSpec {
		t.Fatalf("new session autonomous sizing spec = %+v, want %+v", newSessionAutonomousSizingSpec, browseSizingSpec)
	}
}

func TestAlignedSplitOverlaySizingFitsNarrowWindows(t *testing.T) {
	t.Parallel()

	addRepo := newDebounceTestOverlay(t)
	addRepo.SetSize(60, 20)
	assertOverlayFits(t, addRepo.View(), 60, 20)

	sessionSearch := NewSessionSearchOverlay(styles.NewStyles(styles.DefaultTheme))
	sessionSearch.Open(sessionHistoryScopeWorkspace, true)
	sessionSearch.SetSize(72, 18)

	fixed := time.Unix(1_700_000_000, 0)
	sessionSearch.SetEntries([]domain.SessionHistoryEntry{{
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
		UpdatedAt:          fixed,
		CreatedAt:          fixed,
	}})
	assertOverlayFits(t, sessionSearch.View(), 72, 18)

	newSession := newDebounceTestNewSessionOverlay(t)
	newSession.SetSize(72, 18)
	assertOverlayFits(t, newSession.View(), 72, 18)

	autonomous := NewNewSessionAutonomousOverlay(styles.NewStyles(styles.DefaultTheme))
	autonomous.SetSavedFilters(testAutonomousFilters())
	autonomous.Open()
	autonomous.SetSize(72, 18)
	assertOverlayFits(t, autonomous.View(), 72, 18)
}
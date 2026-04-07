package views

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

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
	if repoManagerSizingSpec != browseSizingSpec {
		t.Fatalf("repo manager sizing spec = %+v, want %+v", repoManagerSizingSpec, browseSizingSpec)
	}
}

func TestAlignedSplitOverlaySizingFitsNarrowWindows(t *testing.T) {
	t.Parallel()

	addRepo := newDebounceTestOverlay(t)
	addRepo.SetSize(60, 20)
	assertOverlayFits(t, addRepo.View(), 60, 20)

	sessionSearch := newSizingParitySessionSearchOverlay()
	sessionSearch.SetSize(72, 18)
	assertOverlayFits(t, sessionSearch.View(), 72, 18)

	newSession := newDebounceTestNewSessionOverlay(t)
	newSession.SetSize(72, 18)
	assertOverlayFits(t, newSession.View(), 72, 18)

	autonomous := newSizingParityAutonomousOverlay()
	autonomous.SetSize(72, 18)
	assertOverlayFits(t, autonomous.View(), 72, 18)

	repoManager := newSizingParityRepoManagerOverlay()
	repoManager.SetSize(60, 20)
	assertOverlayFits(t, repoManager.View(), 60, 20)
}

func TestAlignedSplitOverlayCenterInsetsMatchNewSession(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		width  int
		height int
	}{
		{width: 72, height: 18},
		{width: 120, height: 30},
		{width: 240, height: 60},
		{width: 250, height: 60},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(fmt.Sprintf("%dx%d", tc.width, tc.height), func(t *testing.T) {
			t.Parallel()

			newSession := newDebounceTestNewSessionOverlay(t)
			newSession.SetSize(tc.width, tc.height)
			newSessionTop, newSessionBottom := centeredOverlayInsets(newSession.View(), tc.width, tc.height)

			sessionSearch := newSizingParitySessionSearchOverlay()
			sessionSearch.SetSize(tc.width, tc.height)
			sessionSearchTop, sessionSearchBottom := centeredOverlayInsets(sessionSearch.View(), tc.width, tc.height)
			if sessionSearchTop != newSessionTop || sessionSearchBottom != newSessionBottom {
				t.Fatalf("session search insets = (%d,%d), want (%d,%d)", sessionSearchTop, sessionSearchBottom, newSessionTop, newSessionBottom)
			}

			addRepo := newDebounceTestOverlay(t)
			addRepo.SetSize(tc.width, tc.height)
			addRepoTop, addRepoBottom := centeredOverlayInsets(addRepo.View(), tc.width, tc.height)
			if addRepoTop != newSessionTop || addRepoBottom != newSessionBottom {
				t.Fatalf("add repo insets = (%d,%d), want (%d,%d)", addRepoTop, addRepoBottom, newSessionTop, newSessionBottom)
			}

			autonomous := newSizingParityAutonomousOverlay()
			autonomous.SetSize(tc.width, tc.height)
			autonomousTop, autonomousBottom := centeredOverlayInsets(autonomous.View(), tc.width, tc.height)
			if autonomousTop != newSessionTop || autonomousBottom != newSessionBottom {
				t.Fatalf("autonomous insets = (%d,%d), want (%d,%d)", autonomousTop, autonomousBottom, newSessionTop, newSessionBottom)
			}

			repoMgr := newSizingParityRepoManagerOverlay()
			repoMgr.SetSize(tc.width, tc.height)
			repoMgrTop, repoMgrBottom := centeredOverlayInsets(repoMgr.View(), tc.width, tc.height)
			if repoMgrTop != newSessionTop || repoMgrBottom != newSessionBottom {
				t.Fatalf("repo manager insets = (%d,%d), want (%d,%d)", repoMgrTop, repoMgrBottom, newSessionTop, newSessionBottom)
			}
		})
	}
}

func newSizingParitySessionSearchOverlay() SessionSearchOverlay {
	overlay := NewSessionSearchOverlay(styles.NewStyles(styles.DefaultTheme))
	overlay.Open(sessionHistoryScopeWorkspace, true)

	fixed := time.Unix(1_700_000_000, 0)
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
		UpdatedAt:          fixed,
		CreatedAt:          fixed,
	}})

	return overlay
}

func newSizingParityAutonomousOverlay() NewSessionAutonomousOverlay {
	overlay := NewNewSessionAutonomousOverlay(styles.NewStyles(styles.DefaultTheme))
	overlay.SetSavedFilters(testAutonomousFilters())
	overlay.Open()

	return overlay
}

func newSizingParityRepoManagerOverlay() RepoManagerOverlay {
	overlay := NewRepoManagerOverlay("/tmp/workspace", nil, styles.NewStyles(styles.DefaultTheme))
	overlay.Open()
	return overlay
}

func centeredOverlayInsets(overlayView string, width, height int) (int, int) {
	placed := ansi.Strip(renderOverlay(overlayView, width, height))
	lines := strings.Split(placed, "\n")

	first, last := -1, -1
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if first < 0 {
			first = i
		}
		last = i
	}
	if first < 0 {
		return len(lines), 0
	}

	return first, len(lines) - 1 - last
}

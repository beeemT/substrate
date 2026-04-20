package views

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

var errRepoManagerTest = errors.New("test error")

func newRepoManagerTestApp(t *testing.T) App {
	t.Helper()
	return NewApp(Services{WorkspaceID: "ws-1", WorkspaceName: "ws", Settings: &SettingsService{}})
}

// TestAppRKeyOpensRepoManagerOverlay asserts that pressing 'R' on the main screen
// opens the repo manager overlay.
func TestAppRKeyOpensRepoManagerOverlay(t *testing.T) {
	t.Parallel()

	app := newRepoManagerTestApp(t)
	model, _ := app.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	updated := model.(App)

	model, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'R'}})
	updated = model.(App)

	if updated.activeOverlay != overlayRepoManager {
		t.Fatalf("activeOverlay = %v, want overlayRepoManager", updated.activeOverlay)
	}
	if !updated.repoManager.Active() {
		t.Fatal("expected repoManager overlay to be active after 'R' key")
	}
}

// TestAppRKeyOpensRepoManagerWithWorkItemSelected is a regression test:
// pressing 'R' must open the repo manager even when a work item is selected
// and the content panel is in ContentModeOverview.
func TestAppRKeyOpensRepoManagerWithWorkItemSelected(t *testing.T) {
	t.Parallel()

	app := newRepoManagerTestApp(t)
	model, _ := app.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	updated := model.(App)

	// Simulate a typical home-page state: work item selected, overview visible, sidebar focused.
	updated.currentWorkItemID = "wi-1"
	updated.content.SetMode(ContentModeOverview)
	updated.mainFocus = mainFocusSidebar

	model, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'R'}})
	updated = model.(App)

	if updated.activeOverlay != overlayRepoManager {
		t.Fatalf("activeOverlay = %v, want overlayRepoManager", updated.activeOverlay)
	}
	if !updated.repoManager.Active() {
		t.Fatal("expected repoManager overlay to be active after 'R' key with work item selected")
	}
}

// TestAppRepoManagerEscClosesOverlay asserts that Esc while the repo manager is
// open produces CloseOverlayMsg and that processing it returns to overlayNone.
func TestAppRepoManagerEscClosesOverlay(t *testing.T) {
	t.Parallel()

	app := newRepoManagerTestApp(t)
	model, _ := app.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	updated := model.(App)

	// Open repo manager.
	model, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'R'}})
	updated = model.(App)

	// Esc is routed to repoManager.Update, which returns CloseOverlayMsg as a cmd.
	model, cmd := updated.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated = model.(App)
	if cmd == nil {
		t.Fatal("expected Esc to return a close-overlay command while repo manager is open")
	}
	msg := cmd()
	if _, ok := msg.(CloseOverlayMsg); !ok {
		t.Fatalf("cmd() = %T, want CloseOverlayMsg", msg)
	}

	// Dispatch CloseOverlayMsg to close the overlay.
	model, _ = updated.Update(msg)
	closed := model.(App)

	if closed.activeOverlay == overlayRepoManager {
		t.Fatalf("activeOverlay = %v after Esc, want overlay closed", closed.activeOverlay)
	}
	if closed.repoManager.Active() {
		t.Fatal("expected repoManager overlay to be inactive after Esc")
	}
}

// TestAppRepoManagerViewFitsWindow asserts the full app view stays within 80×20
// while the repo manager overlay is open.
func TestAppRepoManagerViewFitsWindow(t *testing.T) {
	t.Parallel()

	app := newRepoManagerTestApp(t)
	model, _ := app.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	updated := model.(App)

	model, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'R'}})
	updated = model.(App)

	view := updated.View()
	lines := strings.Split(view, "\n")
	if len(lines) != 20 {
		t.Fatalf("line count = %d, want 20\nview:\n%s", len(lines), view)
	}
	for i, line := range lines {
		if w := ansi.StringWidth(line); w > 80 {
			t.Fatalf("line %d width = %d, want <= 80\nline: %q", i+1, w, line)
		}
	}
}

// TestAppRepoManagerShowAddRepoTransition asserts that ShowAddRepoMsg emitted from
// inside the repo manager closes the manager and opens the add-repo overlay.
func TestAppRepoManagerShowAddRepoTransition(t *testing.T) {
	t.Parallel()

	app := newRepoManagerTestApp(t)
	model, _ := app.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	updated := model.(App)

	// Open repo manager.
	model, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'R'}})
	updated = model.(App)

	if updated.activeOverlay != overlayRepoManager {
		t.Fatalf("precondition: activeOverlay = %v, want overlayRepoManager", updated.activeOverlay)
	}

	// Simulate the overlay emitting ShowAddRepoMsg (what 'a' inside the overlay does).
	model, _ = updated.Update(ShowAddRepoMsg{})
	transitioned := model.(App)

	if transitioned.activeOverlay != overlayAddRepo {
		t.Fatalf("activeOverlay = %v after ShowAddRepoMsg, want overlayAddRepo", transitioned.activeOverlay)
	}
	if transitioned.repoManager.Active() {
		t.Fatal("expected repoManager to be inactive after ShowAddRepoMsg transition")
	}
	if !transitioned.addRepo.Active() {
		t.Fatal("expected addRepo overlay to be active after ShowAddRepoMsg transition")
	}
}

// TestAppRepoRemovedMsgShowsSuccessToast asserts that a successful RepoRemovedMsg
// results in a visible success toast containing the repo name.
func TestAppRepoRemovedMsgShowsSuccessToast(t *testing.T) {
	t.Parallel()

	app := newRepoManagerTestApp(t)
	model, _ := app.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	updated := model.(App)

	model, _ = updated.Update(RepoRemovedMsg{RepoPath: "/workspace/my-repo"})
	updated = model.(App)

	view := updated.View()
	if !strings.Contains(view, "my-repo") {
		t.Fatalf("expected view to contain repo name 'my-repo' in toast\nview:\n%s", view)
	}
}

// TestAppRepoRemovedMsgErrorShowsErrorToast asserts that a failed RepoRemovedMsg
// results in a visible error toast.
func TestAppRepoRemovedMsgErrorShowsErrorToast(t *testing.T) {
	t.Parallel()

	app := newRepoManagerTestApp(t)
	model, _ := app.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	updated := model.(App)

	model, _ = updated.Update(RepoRemovedMsg{
		RepoPath: "/workspace/my-repo",
		Err:      errRepoManagerTest,
	})
	updated = model.(App)

	view := updated.View()
	// Error toasts contain the error message.
	if !strings.Contains(ansi.Strip(view), "Failed") {
		t.Fatalf("expected error toast in view after failed RepoRemovedMsg\nview:\n%s", view)
	}
}

// TestAppRepoInitializedMsgShowsSuccessToast asserts that a successful
// RepoInitializedMsg produces a visible success toast.
func TestAppRepoInitializedMsgShowsSuccessToast(t *testing.T) {
	t.Parallel()

	app := newRepoManagerTestApp(t)
	model, _ := app.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	updated := model.(App)

	model, _ = updated.Update(RepoInitializedMsg{RepoPath: "/workspace/plain-repo"})
	updated = model.(App)

	view := updated.View()
	if !strings.Contains(view, "plain-repo") {
		t.Fatalf("expected view to contain repo name 'plain-repo' in toast\nview:\n%s", view)
	}
}

// TestAppAKeyNoLongerOpensAddRepoOverlay is a regression guard: pressing 'a' on the
// main screen must NOT open the add-repo overlay directly. Use 'R' → 'a' instead.
// (This test lives in app_add_repo_test.go; this comment documents the intent here.)

// TestAppRepoManagerOpenDoesNotOpenAddRepo asserts that opening the repo manager
// does not accidentally activate the add-repo overlay.
func TestAppRepoManagerOpenDoesNotOpenAddRepo(t *testing.T) {
	t.Parallel()

	app := newRepoManagerTestApp(t)
	model, _ := app.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	updated := model.(App)

	model, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'R'}})
	updated = model.(App)

	if updated.activeOverlay == overlayAddRepo {
		t.Fatal("'R' key must open repoManager, not addRepo overlay")
	}
	if updated.addRepo.Active() {
		t.Fatal("addRepo overlay must not be active when repo manager opens")
	}
}

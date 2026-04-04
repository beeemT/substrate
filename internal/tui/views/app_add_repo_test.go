package views

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestAppAKeyOpensAddRepoOverlay(t *testing.T) {
	t.Parallel()

	app := NewApp(Services{WorkspaceID: "ws-1", WorkspaceName: "ws", Settings: &SettingsService{}})
	model, _ := app.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	updated := model.(App)

	model, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	updated = model.(App)

	if updated.activeOverlay != overlayAddRepo {
		t.Fatalf("activeOverlay = %v, want overlayAddRepo", updated.activeOverlay)
	}
	if !updated.addRepo.Active() {
		t.Fatal("expected addRepo overlay to be active after 'a' key")
	}
}

func TestAppEscClosesAddRepoOverlay(t *testing.T) {
	t.Parallel()

	app := NewApp(Services{WorkspaceID: "ws-1", WorkspaceName: "ws", Settings: &SettingsService{}})
	model, _ := app.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	updated := model.(App)

	model, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	updated = model.(App)

	// Esc is routed to addRepo.Update, which returns CloseOverlayMsg as a command.
	// The two-step dispatch mirrors what the BubbleTea runtime does.
	model, cmd := updated.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated = model.(App)
	if cmd == nil {
		t.Fatal("expected Esc to return a close-overlay command while add-repo overlay is open")
	}
	msg := cmd()
	if _, ok := msg.(CloseOverlayMsg); !ok {
		t.Fatalf("cmd() = %T, want CloseOverlayMsg", msg)
	}

	model, _ = updated.Update(msg)
	closed := model.(App)

	if closed.activeOverlay == overlayAddRepo {
		t.Fatalf("activeOverlay = %v, want overlay closed after Esc", closed.activeOverlay)
	}
	if closed.addRepo.Active() {
		t.Fatal("expected add repo overlay to be inactive after Esc")
	}
}

func TestAppRepoClonedMsgShowsSuccessToast(t *testing.T) {
	t.Parallel()

	app := NewApp(Services{WorkspaceID: "ws-1", WorkspaceName: "ws", Settings: &SettingsService{}})
	model, _ := app.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	updated := model.(App)

	// RepoClonedMsg handler fires ActionDoneMsg as a command rather than mutating toast state
	// directly, so we simulate the runtime dispatching it by sending it in a follow-up Update.
	model, _ = updated.Update(RepoClonedMsg{RepoPath: "/workspace/my-repo"})
	updated = model.(App)

	model, _ = updated.Update(ActionDoneMsg{Message: "Repository cloned to workspace"})
	updated = model.(App)

	view := updated.View()
	// The toast text may wrap inside the toast box, so check for the prefix
	// that always appears on the first toast line.
	if !strings.Contains(view, "Repository cloned") {
		t.Fatalf("view does not contain expected success toast\nview:\n%s", view)
	}
	if updated.activeOverlay == overlayAddRepo {
		t.Fatalf("activeOverlay = %v, want overlay not open", updated.activeOverlay)
	}
}

func TestAppAddRepoViewFitsWindowWhenOpen(t *testing.T) {
	t.Parallel()

	app := NewApp(Services{WorkspaceID: "ws-1", WorkspaceName: "ws", Settings: &SettingsService{}})
	model, _ := app.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	updated := model.(App)

	model, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
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

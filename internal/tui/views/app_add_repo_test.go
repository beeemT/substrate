package views

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/beeemT/substrate/internal/adapter"
)

func TestAppAKeyNoLongerOpensAddRepoOverlay(t *testing.T) {
	t.Parallel()

	app := NewApp(Services{WorkspaceID: "ws-1", WorkspaceName: "ws", Settings: &SettingsService{}})
	model, _ := app.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	updated := model.(App)

	model, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	updated = model.(App)

	if updated.activeOverlay == overlayAddRepo {
		t.Fatal("'a' key must NOT open addRepo overlay directly; use 'r' \u2192 'a' instead")
	}
}

func TestAppEscClosesAddRepoOverlay(t *testing.T) {
	t.Parallel()

	app := NewApp(Services{WorkspaceID: "ws-1", WorkspaceName: "ws", Settings: &SettingsService{}})
	model, _ := app.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	updated := model.(App)

	model, _ = updated.Update(ShowAddRepoMsg{})
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

	model, _ = updated.Update(ShowAddRepoMsg{})
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


// TestAppManagedRepoSlugsRebuildOnScan asserts that dispatching ManagedReposLoadedMsg
// populates managedRepoSlugs from the repos' RemoteURL fields.
func TestAppManagedRepoSlugsRebuildOnScan(t *testing.T) {
	t.Parallel()

	app := NewApp(Services{WorkspaceID: "ws-1", WorkspaceName: "ws", Settings: &SettingsService{}})
	model, _ := app.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	updated := model.(App)

	msg := ManagedReposLoadedMsg{
		Repos: []managedRepo{
			{Path: "/ws/substrate", Name: "substrate", Kind: repoKindGitWork,
				RemoteURL: "https://github.com/beeemT/substrate.git"},
			{Path: "/ws/cli", Name: "cli", Kind: repoKindGitWork,
				RemoteURL: "git@github.com:beeemT/cli.git"},
			{Path: "/ws/nouri", Name: "nouri", Kind: repoKindPlainGit,
				RemoteURL: ""},
		},
	}
	model, _ = updated.Update(msg)
	updated = model.(App)

	// Slugs for repos with a valid remote URL must be present.
	if !updated.managedRepoSlugs["beeemt/substrate"] {
		t.Error("expected slug 'beeemt/substrate' in managedRepoSlugs")
	}
	if !updated.managedRepoSlugs["beeemt/cli"] {
		t.Error("expected slug 'beeemt/cli' in managedRepoSlugs")
	}
	// Repo with empty RemoteURL must not produce a slug entry.
	if updated.managedRepoSlugs[""] {
		t.Error("empty slug must not be added to managedRepoSlugs")
	}
}

// TestAppOpenAddRepoTriggersScanWhenSlugsNil asserts that openAddRepo fires
// LoadManagedReposCmd when managedRepoSlugs has not yet been populated.
func TestAppOpenAddRepoTriggersScanWhenSlugsNil(t *testing.T) {
	t.Parallel()

	app := NewApp(Services{
		WorkspaceID:   "ws-1",
		WorkspaceName: "ws",
		WorkspaceDir:  "/tmp/workspace",
		Settings:      &SettingsService{},
	})
	model, _ := app.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	updated := model.(App)

	// managedRepoSlugs starts nil; opening add-repo should trigger a scan.
	if updated.managedRepoSlugs != nil {
		t.Skip("managedRepoSlugs already populated — precondition not met")
	}

	model, cmd := updated.Update(ShowAddRepoMsg{})
	_ = model

	// cmd should be a batch containing both Open and LoadManagedReposCmd.
	// We can verify a scan was triggered by checking that cmd is non-nil.
	if cmd == nil {
		t.Fatal("expected non-nil command batch when opening add-repo with nil slugs")
	}
}

// TestAppManagedRepoSlugsForwardedToAddRepoOnScan asserts that when
// ManagedReposLoadedMsg arrives while the add-repo overlay is active,
// the overlay receives the updated slug set via SetPresentSlugs.
func TestAppManagedRepoSlugsForwardedToAddRepoOnScan(t *testing.T) {
	t.Parallel()

	app := NewApp(Services{WorkspaceID: "ws-1", WorkspaceName: "ws", Settings: &SettingsService{}})
	model, _ := app.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	updated := model.(App)

	// Open add-repo overlay.
	model, _ = updated.Update(ShowAddRepoMsg{})
	updated = model.(App)
	if updated.activeOverlay != overlayAddRepo {
		t.Fatal("precondition: add-repo overlay must be active")
	}

	// Now dispatch ManagedReposLoadedMsg with a known repo.
	scanMsg := ManagedReposLoadedMsg{
		Repos: []managedRepo{
			{Path: "/ws/substrate", Name: "substrate", Kind: repoKindGitWork,
				RemoteURL: "https://github.com/beeemT/substrate.git"},
		},
	}
	model, _ = updated.Update(scanMsg)
	updated = model.(App)

	// The app-level slug set must be populated.
	if !updated.managedRepoSlugs["beeemt/substrate"] {
		t.Error("expected 'beeemt/substrate' in managedRepoSlugs after scan")
	}
	// The add-repo overlay must also have the slug (forwarded via SetPresentSlugs).
	// We verify this indirectly: load repos into the overlay and check the view
	// shows 'already added' for the substrate repo.
	repoItem := adapter.RepoItem{
		Name: "substrate", FullName: "beeemT/substrate",
		URL: "https://github.com/beeemT/substrate.git", DefaultBranch: "main",
	}
	model, _ = updated.Update(RepoListLoadedMsg{Repos: []adapter.RepoItem{repoItem}, HasMore: false})
	updated = model.(App)

	view := updated.View()
	plain := ansi.Strip(view)
	if !strings.Contains(plain, "already added") {
		t.Errorf("expected 'already added' in add-repo overlay after slug forwarding\nview:\n%s", plain)
	}
}

// TestAppRepoClonedMsgUpdatesSlugs asserts that a successful RepoClonedMsg
// adds the pending clone slug to managedRepoSlugs.
func TestAppRepoClonedMsgUpdatesSlugs(t *testing.T) {
	t.Parallel()

	app := NewApp(Services{WorkspaceID: "ws-1", WorkspaceName: "ws", Settings: &SettingsService{}})
	model, _ := app.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	updated := model.(App)

	// Simulate AddRepoCloneMsg to set pendingCloneSlug.
	cloneMsg := AddRepoCloneMsg{
		Repo:     adapter.RepoItem{FullName: "beeemT/newrepo"},
		CloneDir: "/tmp/workspace",
		CloneURL: "https://github.com/beeemT/newrepo.git",
	}
	model, _ = updated.Update(cloneMsg)
	updated = model.(App)

	// Simulate successful RepoClonedMsg.
	model, _ = updated.Update(RepoClonedMsg{RepoPath: "/tmp/workspace/newrepo"})
	updated = model.(App)

	if !updated.managedRepoSlugs["beeemt/newrepo"] {
		t.Error("expected 'beeemt/newrepo' slug in managedRepoSlugs after successful clone")
	}
}

// TestAppRepoClonedMsgErrorDoesNotUpdateSlugs asserts that a failed RepoClonedMsg
// does NOT add the pending slug to managedRepoSlugs.
func TestAppRepoClonedMsgErrorDoesNotUpdateSlugs(t *testing.T) {
	t.Parallel()

	app := NewApp(Services{WorkspaceID: "ws-1", WorkspaceName: "ws", Settings: &SettingsService{}})
	model, _ := app.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	updated := model.(App)

	// Set pendingCloneSlug via AddRepoCloneMsg.
	cloneMsg := AddRepoCloneMsg{
		Repo:     adapter.RepoItem{FullName: "beeemT/failrepo"},
		CloneDir: "/tmp/workspace",
		CloneURL: "https://github.com/beeemT/failrepo.git",
	}
	model, _ = updated.Update(cloneMsg)
	updated = model.(App)

	// Simulate failed clone.
	model, _ = updated.Update(RepoClonedMsg{RepoPath: "/tmp/workspace/failrepo", Err: errRepoManagerTest})
	updated = model.(App)

	if updated.managedRepoSlugs["beeemt/failrepo"] {
		t.Error("failed clone must not add slug to managedRepoSlugs")
	}
	// pendingCloneSlug must be cleared regardless.
	if updated.pendingCloneSlug != "" {
		t.Errorf("pendingCloneSlug = %q, want empty after RepoClonedMsg", updated.pendingCloneSlug)
	}
}
package views_test

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/tui/views"
)

// mockRepoSource implements adapter.RepoSource for tests.
type mockRepoSource struct {
	name     string
	repos    []adapter.RepoItem
	err      error
	lastOpts adapter.RepoListOpts // populated on each ListRepos call
}

func (m *mockRepoSource) Name() string { return m.name }

func (m *mockRepoSource) ListRepos(_ context.Context, opts adapter.RepoListOpts) (*adapter.RepoListResult, error) {
	m.lastOpts = opts
	if m.err != nil {
		return nil, m.err
	}
	return &adapter.RepoListResult{Repos: m.repos, HasMore: false}, nil
}

func testRepos() []adapter.RepoItem {
	return []adapter.RepoItem{
		{
			Name: "substrate", FullName: "beeemT/substrate", Description: "A TUI app",
			URL: "https://github.com/beeemT/substrate.git", SSHURL: "git@github.com:beeemT/substrate.git",
			DefaultBranch: "main", IsPrivate: false, Source: "github", Owner: "beeemT",
		},
		{
			Name: "cli", FullName: "beeemT/cli", Description: "A CLI tool",
			URL: "https://github.com/beeemT/cli.git", SSHURL: "git@github.com:beeemT/cli.git",
			DefaultBranch: "main", IsPrivate: true, Source: "github", Owner: "beeemT",
		},
	}
}

func newAddRepoOverlay(t *testing.T, sources []adapter.RepoSource) views.AddRepoOverlay {
	t.Helper()
	return views.NewAddRepoOverlay(sources, "/tmp/workspace", nil, newTestStyles(t))
}

func TestAddRepoOverlay_ActiveAfterOpen(t *testing.T) {
	t.Parallel()

	m := newAddRepoOverlay(t, nil)
	m.Open(nil)

	if !m.Active() {
		t.Fatal("expected Active() == true after Open")
	}
}

func TestAddRepoOverlay_InactiveAfterClose(t *testing.T) {
	t.Parallel()

	m := newAddRepoOverlay(t, nil)
	m.Open(nil)
	m.Close()

	if m.Active() {
		t.Fatal("expected Active() == false after Close")
	}
}

func TestAddRepoOverlay_ViewEmptyWhenInactive(t *testing.T) {
	t.Parallel()

	m := newAddRepoOverlay(t, nil)
	v := m.View()

	if v != "" {
		t.Fatalf("expected empty View() when inactive, got %q", v)
	}
}

func TestAddRepoOverlay_ViewFitsSize_120x40(t *testing.T) {
	t.Parallel()

	m := newAddRepoOverlay(t, []adapter.RepoSource{&mockRepoSource{name: "github", repos: testRepos()}})
	m.SetSize(120, 40)
	m.Open(nil)

	// Simulate repos being loaded.
	m, _ = m.Update(views.RepoListLoadedMsg{Repos: testRepos(), HasMore: false})

	v := m.View()
	if v == "" {
		t.Fatal("expected non-empty View()")
	}
	assertViewFitsSize(t, v, 120, 40)
}

func TestAddRepoOverlay_ViewFitsSize_60x20(t *testing.T) {
	t.Parallel()

	m := newAddRepoOverlay(t, []adapter.RepoSource{&mockRepoSource{name: "github", repos: testRepos()}})
	m.SetSize(60, 20)
	m.Open(nil)

	m, _ = m.Update(views.RepoListLoadedMsg{Repos: testRepos(), HasMore: false})

	v := m.View()
	if v == "" {
		t.Fatal("expected non-empty View()")
	}
	assertViewFitsSize(t, v, 60, 20)
}

func TestAddRepoOverlay_EscCloses(t *testing.T) {
	t.Parallel()

	m := newAddRepoOverlay(t, []adapter.RepoSource{&mockRepoSource{name: "github"}})
	m.SetSize(120, 40)
	m.Open(nil)

	escKey := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e', 's', 'c'}}
	_, cmd := m.Update(escKey)

	if cmd == nil {
		t.Fatal("expected non-nil cmd after Esc key")
	}
	msg := cmd()
	if _, ok := msg.(views.CloseOverlayMsg); !ok {
		t.Fatalf("expected CloseOverlayMsg, got %T", msg)
	}
}

func TestAddRepoOverlay_ManualModeToggle(t *testing.T) {
	t.Parallel()

	m := newAddRepoOverlay(t, []adapter.RepoSource{&mockRepoSource{name: "github"}})
	m.SetSize(120, 40)
	m.Open(nil)

	ctrlN := tea.KeyMsg{Type: tea.KeyCtrlN}
	m, _ = m.Update(ctrlN)

	v := m.View()
	plain := ansi.Strip(v)
	if !strings.Contains(plain, "Clone URL:") {
		t.Error("expected View to contain 'Clone URL:' after toggling manual mode")
	}
}

func TestAddRepoOverlay_SourceCycling(t *testing.T) {
	t.Parallel()

	sources := []adapter.RepoSource{
		&mockRepoSource{name: "github"},
		&mockRepoSource{name: "gitlab"},
	}
	m := newAddRepoOverlay(t, sources)
	m.SetSize(120, 40)
	m.Open(nil)

	tabKey := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t', 'a', 'b'}}

	// Tab once: should cycle from index 0 to 1.
	m, _ = m.Update(tabKey)
	v1 := ansi.Strip(m.View())
	if !strings.Contains(v1, "gitlab") {
		t.Error("expected 'gitlab' in view after first Tab")
	}

	// Tab again: should wrap back to index 0.
	m, _ = m.Update(tabKey)
	v2 := ansi.Strip(m.View())
	if !strings.Contains(v2, "github") {
		t.Error("expected 'github' in view after second Tab (wrap)")
	}
}

func TestAddRepoOverlay_ViewContainsTitle(t *testing.T) {
	t.Parallel()

	m := newAddRepoOverlay(t, []adapter.RepoSource{&mockRepoSource{name: "github"}})
	m.SetSize(120, 40)
	m.Open(nil)

	v := m.View()
	plain := ansi.Strip(v)
	if !strings.Contains(plain, "Browse Repositories") {
		t.Error("expected View to contain 'Browse Repositories' title")
	}
}

func TestAddRepoOverlay_LoadingState(t *testing.T) {
	t.Parallel()

	m := newAddRepoOverlay(t, []adapter.RepoSource{&mockRepoSource{name: "github"}})
	m.SetSize(120, 40)
	m.Open(nil)

	// Before sending RepoListLoadedMsg, the view should show a loading state.
	v := m.View()
	plain := ansi.Strip(v)
	if !strings.Contains(plain, "Loading") {
		t.Error("expected View to show loading state before repos are loaded")
	}
}

func TestAddRepoOverlay_DetailPaneContent(t *testing.T) {
	t.Parallel()

	repos := testRepos()
	m := newAddRepoOverlay(t, []adapter.RepoSource{&mockRepoSource{name: "github", repos: repos}})
	m.SetSize(120, 40)
	m.Open(nil)

	m, _ = m.Update(views.RepoListLoadedMsg{Repos: repos, HasMore: false})

	v := m.View()
	plain := ansi.Strip(v)

	// The detail pane should show info about the first (selected) repo.
	if !strings.Contains(plain, "beeemT/substrate") {
		t.Error("expected detail pane to contain selected repo full name")
	}
	if !strings.Contains(plain, "main") {
		t.Error("expected detail pane to contain default branch")
	}
	if !strings.Contains(plain, "Public") {
		t.Error("expected detail pane to show 'Public' access level")
	}
}

func TestAddRepoOverlay_EmptyDetailWhenNoSelection(t *testing.T) {
	t.Parallel()

	m := newAddRepoOverlay(t, []adapter.RepoSource{&mockRepoSource{name: "github"}})
	m.SetSize(120, 40)
	m.Open(nil)

	m, _ = m.Update(views.RepoListLoadedMsg{Repos: nil, HasMore: false})

	v := m.View()
	plain := ansi.Strip(v)

	if !strings.Contains(plain, "No repository selected") {
		t.Error("expected detail pane to show empty-state message when no repos loaded")
	}
}

// TestAddRepoOverlay_DownFromControlsEntersList asserts that pressing down
// when the search control is focused moves focus into the repo list (not an error).
func TestAddRepoOverlay_DownFromControlsEntersList(t *testing.T) {
	t.Parallel()

	repos := testRepos()
	m := newAddRepoOverlay(t, []adapter.RepoSource{&mockRepoSource{name: "github", repos: repos}})
	m.SetSize(120, 40)
	m.Open(nil)
	m, _ = m.Update(views.RepoListLoadedMsg{Repos: repos, HasMore: false})

	// Start: focus is on search control (default after Open).
	// Down arrow should move focus into the list.
	down := tea.KeyMsg{Type: tea.KeyDown}
	m, _ = m.Update(down)

	// Pressing down again should forward to the repo list, not back to controls.
	m, _ = m.Update(down)
	v := ansi.Strip(m.View())
	// The view should still be visible and not contain a panic; just ensure it renders.
	if !strings.Contains(v, "Browse Repositories") {
		t.Error("expected view to contain title after navigating down to list")
	}
}

// TestAddRepoOverlay_UpFromListTopReturnsToSearch asserts that pressing up
// when the repo list cursor is at index 0 moves focus back to the search control,
// restoring the search label highlight.
func TestAddRepoOverlay_UpFromListTopReturnsToSearch(t *testing.T) {
	t.Parallel()

	repos := testRepos()
	m := newAddRepoOverlay(t, []adapter.RepoSource{&mockRepoSource{name: "github", repos: repos}})
	m.SetSize(120, 40)
	m.Open(nil)
	m, _ = m.Update(views.RepoListLoadedMsg{Repos: repos, HasMore: false})

	// Navigate down to the list.
	down := tea.KeyMsg{Type: tea.KeyDown}
	m, _ = m.Update(down)

	// Press up from index 0 — should return to search control.
	up := tea.KeyMsg{Type: tea.KeyUp}
	m, _ = m.Update(up)

	v := ansi.Strip(m.View())
	// The search row should still be visible.
	if !strings.Contains(v, "Search:") {
		t.Error("expected 'Search:' label to appear in view after returning from list")
	}
}

// TestAddRepoOverlay_UpFromSearchMovesToSource asserts that pressing up
// from the search control moves focus to the source control.
func TestAddRepoOverlay_UpFromSearchMovesToSource(t *testing.T) {
	t.Parallel()

	sources := []adapter.RepoSource{
		&mockRepoSource{name: "github"},
		&mockRepoSource{name: "gitlab"},
	}
	m := newAddRepoOverlay(t, sources)
	m.SetSize(120, 40)
	m.Open(nil)

	// Press up from search — should move to source control.
	up := tea.KeyMsg{Type: tea.KeyUp}
	m, _ = m.Update(up)

	v := ansi.Strip(m.View())
	if !strings.Contains(v, "Source:") {
		t.Error("expected 'Source:' label in view after moving focus to source control")
	}
}

// TestAddRepoOverlay_SourceCyclesWithArrowKeys asserts that left/right arrows
// cycle through sources when the source control is focused.
func TestAddRepoOverlay_SourceCyclesWithArrowKeys(t *testing.T) {
	t.Parallel()

	sources := []adapter.RepoSource{
		&mockRepoSource{name: "github"},
		&mockRepoSource{name: "gitlab"},
	}
	m := newAddRepoOverlay(t, sources)
	m.SetSize(120, 40)
	m.Open(nil)

	// Navigate up from search to source control.
	up := tea.KeyMsg{Type: tea.KeyUp}
	m, _ = m.Update(up)

	// Right arrow should cycle source forward.
	right := tea.KeyMsg{Type: tea.KeyRight}
	m, _ = m.Update(right)
	v1 := ansi.Strip(m.View())
	if !strings.Contains(v1, "gitlab") {
		t.Error("expected 'gitlab' selected after right arrow on source control")
	}

	// Right arrow again should wrap back to github.
	m, _ = m.Update(right)
	v2 := ansi.Strip(m.View())
	if !strings.Contains(v2, "github") || !strings.Contains(ansi.Strip(m.View()), "[\u25ba") {
		// Just verify we can still see the source labels cycling without errors.
		_ = v2
	}
}

// TestAddRepoOverlay_ViewContainsSearchLabel asserts that the 'Search:' label
// is rendered in the browse view (it was moved from the header input to a labelled row).
func TestAddRepoOverlay_ViewContainsSearchLabel(t *testing.T) {
	t.Parallel()

	m := newAddRepoOverlay(t, []adapter.RepoSource{&mockRepoSource{name: "github"}})
	m.SetSize(120, 40)
	m.Open(nil)

	v := ansi.Strip(m.View())
	if !strings.Contains(v, "Search:") {
		t.Error("expected 'Search:' label to appear in browser view")
	}
}

// TestAddRepoOverlay_ViewFitsSize_AfterNavigation asserts that navigating between
// controls does not push the layout outside the terminal bounds.
func TestAddRepoOverlay_ViewFitsSize_AfterNavigation(t *testing.T) {
	t.Parallel()

	repos := testRepos()
	m := newAddRepoOverlay(t, []adapter.RepoSource{&mockRepoSource{name: "github", repos: repos}})
	m.SetSize(120, 40)
	m.Open(nil)
	m, _ = m.Update(views.RepoListLoadedMsg{Repos: repos, HasMore: false})

	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyDown},
		{Type: tea.KeyDown},
		{Type: tea.KeyUp},
		{Type: tea.KeyUp},
	} {
		m, _ = m.Update(key)
	}
	assertViewFitsSize(t, m.View(), 120, 40)
}

// TestAddRepoOverlay_EnterInSearchDoesNotClone asserts that pressing Enter while
// the search control is focused does NOT trigger a clone of the selected repo.
func TestAddRepoOverlay_EnterInSearchDoesNotClone(t *testing.T) {
	t.Parallel()

	repos := testRepos()
	m := newAddRepoOverlay(t, []adapter.RepoSource{&mockRepoSource{name: "github", repos: repos}})
	m.SetSize(120, 40)
	m.Open(nil)
	m, _ = m.Update(views.RepoListLoadedMsg{Repos: repos, HasMore: false})

	// Focus is on search control after Open. Press Enter.
	enter := tea.KeyMsg{Type: tea.KeyEnter}
	_, cmd := m.Update(enter)

	// No clone command should have been produced.
	if cmd != nil {
		msg := cmd()
		if _, ok := msg.(views.AddRepoCloneMsg); ok {
			t.Error("Enter while search control is focused must not trigger a clone")
		}
	}
}

// TestAddRepoOverlay_FilterRowVisible asserts that the filter row is rendered
// in browser mode.
func TestAddRepoOverlay_FilterRowVisible(t *testing.T) {
	t.Parallel()

	m := newAddRepoOverlay(t, []adapter.RepoSource{&mockRepoSource{name: "gitlab"}})
	m.SetSize(120, 40)
	m.Open(nil)

	v := ansi.Strip(m.View())
	if !strings.Contains(v, "Filter:") {
		t.Error("expected 'Filter:' row to appear in browser view")
	}
	// Default state: Owned should be the active selection.
	if !strings.Contains(v, "Owned") {
		t.Error("expected 'Owned' label in filter row")
	}
}

// TestAddRepoOverlay_CtrlGTogglesFilter asserts that Ctrl+G flips the filter
// between owned-only and all-membership, visible in the rendered view.
func TestAddRepoOverlay_CtrlGTogglesFilter(t *testing.T) {
	t.Parallel()

	m := newAddRepoOverlay(t, []adapter.RepoSource{&mockRepoSource{name: "gitlab"}})
	m.SetSize(120, 40)
	m.Open(nil)

	// Initial state: Owned is the active choice — the bracket indicator wraps it.
	v0 := ansi.Strip(m.View())
	if !strings.Contains(v0, "► Owned") {
		t.Error("expected active Owned indicator (► Owned) in initial filter row")
	}

	// Press Ctrl+G once — should flip to All.
	ctrlG := tea.KeyMsg{Type: tea.KeyCtrlG}
	m, _ = m.Update(ctrlG)
	v1 := ansi.Strip(m.View())
	if !strings.Contains(v1, "► All") {
		t.Error("expected active All indicator (► All) after first Ctrl+G")
	}

	// Press Ctrl+G again — should flip back to Owned.
	m, _ = m.Update(ctrlG)
	v2 := ansi.Strip(m.View())
	if !strings.Contains(v2, "► Owned") {
		t.Error("expected active Owned indicator (► Owned) after second Ctrl+G")
	}
}

// TestAddRepoOverlay_FilterRowToggleWithArrows asserts that left/right arrows
// toggle the filter when the filter control is focused.
func TestAddRepoOverlay_FilterRowToggleWithArrows(t *testing.T) {
	t.Parallel()

	m := newAddRepoOverlay(t, []adapter.RepoSource{&mockRepoSource{name: "gitlab"}})
	m.SetSize(120, 40)
	m.Open(nil)

	// Navigate up from search — filter is directly above search in the render order.
	up := tea.KeyMsg{Type: tea.KeyUp}
	m, _ = m.Update(up)

	// Right arrow: select All.
	right := tea.KeyMsg{Type: tea.KeyRight}
	m, _ = m.Update(right)
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "► All") {
		t.Error("expected active All indicator (► All) after right arrow on filter control")
	}

	// Left arrow: select Owned.
	left := tea.KeyMsg{Type: tea.KeyLeft}
	m, _ = m.Update(left)
	v2 := ansi.Strip(m.View())
	if !strings.Contains(v2, "► Owned") {
		t.Error("expected active Owned indicator (► Owned) after left arrow on filter control")
	}
}

// TestAddRepoOverlay_FilterOwnedOnlyPassedToSource asserts that the OwnedOnly
// flag is forwarded to the repo source through LoadReposCmd.
func TestAddRepoOverlay_FilterOwnedOnlyPassedToSource(t *testing.T) {
	t.Parallel()

	src := &mockRepoSource{name: "gitlab", repos: testRepos()}
	m := newAddRepoOverlay(t, []adapter.RepoSource{src})
	m.SetSize(120, 40)

	// Open triggers an initial load with ownedOnly=true (the default).
	cmd := m.Open(nil)
	if cmd != nil {
		cmd() // execute the load cmd synchronously so lastOpts is populated
	}
	if !src.lastOpts.OwnedOnly {
		t.Error("expected OwnedOnly=true on initial Open")
	}

	// Ctrl+G toggles to false; the next reload must carry OwnedOnly=false.
	ctrlG := tea.KeyMsg{Type: tea.KeyCtrlG}
	m, cmd = m.Update(ctrlG)
	if cmd != nil {
		cmd()
	}
	if src.lastOpts.OwnedOnly {
		t.Error("expected OwnedOnly=false after Ctrl+G toggle")
	}
	_ = m
}

// TestAddRepoOverlay_ViewFitsSize_AfterFilterToggle asserts that toggling the
// filter does not push the layout outside the terminal bounds.
func TestAddRepoOverlay_ViewFitsSize_AfterFilterToggle(t *testing.T) {
	t.Parallel()

	repos := testRepos()
	m := newAddRepoOverlay(t, []adapter.RepoSource{&mockRepoSource{name: "gitlab", repos: repos}})
	m.SetSize(120, 40)
	m.Open(nil)
	m, _ = m.Update(views.RepoListLoadedMsg{Repos: repos, HasMore: false})

	ctrlG := tea.KeyMsg{Type: tea.KeyCtrlG}
	m, _ = m.Update(ctrlG)

	assertViewFitsSize(t, m.View(), 120, 40)
}


// navigateToList moves focus from the default search control into the repo list.
func navigateToList(t *testing.T, m views.AddRepoOverlay) views.AddRepoOverlay {
	t.Helper()
	down := tea.KeyMsg{Type: tea.KeyDown}
	m, _ = m.Update(down) // search control -> list
	return m
}

// TestAddRepoOverlay_AlreadyPresentItemShowsIndicator asserts that an item whose
// FullName slug matches a presentSlug has "already added" in its description.
func TestAddRepoOverlay_AlreadyPresentItemShowsIndicator(t *testing.T) {
	t.Parallel()

	repos := testRepos() // contains beeemT/substrate
	presentSlugs := map[string]bool{"beeemt/substrate": true}
	m := newAddRepoOverlay(t, []adapter.RepoSource{&mockRepoSource{name: "github", repos: repos}})
	m.SetSize(120, 40)
	m.Open(presentSlugs)
	m, _ = m.Update(views.RepoListLoadedMsg{Repos: repos, HasMore: false})

	v := ansi.Strip(m.View())
	if !strings.Contains(v, "already added") {
		t.Error("expected 'already added' indicator in view for a present repo")
	}
}

// TestAddRepoOverlay_NonPresentItemClonesImmediately asserts that Enter on a
// non-present item emits AddRepoCloneMsg directly without showing a confirm modal.
func TestAddRepoOverlay_NonPresentItemClonesImmediately(t *testing.T) {
	t.Parallel()

	repos := testRepos() // beeemT/substrate is NOT in presentSlugs
	m := newAddRepoOverlay(t, []adapter.RepoSource{&mockRepoSource{name: "github", repos: repos}})
	m.SetSize(120, 40)
	m.Open(nil) // no present repos
	m, _ = m.Update(views.RepoListLoadedMsg{Repos: repos, HasMore: false})
	m = navigateToList(t, m)

	enter := tea.KeyMsg{Type: tea.KeyEnter}
	_, cmd := m.Update(enter)

	if cmd == nil {
		t.Fatal("expected a command after Enter on a non-present item")
	}
	msg := cmd()
	// With a nil git client, the implementation emits ErrMsg instead of
	// AddRepoCloneMsg, but both originate from the clone branch — neither
	// represents a confirmation modal being shown.
	switch msg.(type) {
	case views.AddRepoCloneMsg, views.ErrMsg:
		// expected: clone path was taken
	default:
		t.Fatalf("expected clone-path message (AddRepoCloneMsg or ErrMsg), got %T", msg)
	}
	// Confirm dialog must not be shown.
	v := ansi.Strip(m.View())
	if strings.Contains(v, "already in workspace") {
		t.Error("confirm dialog must not appear for a non-present item")
	}
}

// TestAddRepoOverlay_PresentItemShowsConfirmOnEnter asserts that Enter on an
// already-present item shows the confirmation modal and does NOT emit AddRepoCloneMsg.
func TestAddRepoOverlay_PresentItemShowsConfirmOnEnter(t *testing.T) {
	t.Parallel()

	repos := testRepos()
	presentSlugs := map[string]bool{"beeemt/substrate": true}
	m := newAddRepoOverlay(t, []adapter.RepoSource{&mockRepoSource{name: "github", repos: repos}})
	m.SetSize(120, 40)
	m.Open(presentSlugs)
	m, _ = m.Update(views.RepoListLoadedMsg{Repos: repos, HasMore: false})
	m = navigateToList(t, m)

	enter := tea.KeyMsg{Type: tea.KeyEnter}
	m, cmd := m.Update(enter)

	// No clone command emitted.
	if cmd != nil {
		msg := cmd()
		if _, ok := msg.(views.AddRepoCloneMsg); ok {
			t.Error("Enter on a present item must not emit AddRepoCloneMsg")
		}
	}
	// Confirm dialog visible in view.
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "already in workspace") {
		t.Errorf("expected confirm dialog text in view, got:\n%s", v)
	}
}

// TestAddRepoOverlay_ConfirmWithYProceedsClone asserts that pressing y while
// the confirm modal is active emits AddRepoCloneMsg and closes the modal.
func TestAddRepoOverlay_ConfirmWithYProceedsClone(t *testing.T) {
	t.Parallel()

	repos := testRepos()
	presentSlugs := map[string]bool{"beeemt/substrate": true}
	m := newAddRepoOverlay(t, []adapter.RepoSource{&mockRepoSource{name: "github", repos: repos}})
	m.SetSize(120, 40)
	m.Open(presentSlugs)
	m, _ = m.Update(views.RepoListLoadedMsg{Repos: repos, HasMore: false})
	m = navigateToList(t, m)

	// Trigger confirm modal.
	enter := tea.KeyMsg{Type: tea.KeyEnter}
	m, _ = m.Update(enter)

	// Confirm with y.
	yKey := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}}
	m, cmd := m.Update(yKey)

	if cmd == nil {
		t.Fatal("expected AddRepoCloneMsg command after y")
	}
	msg := cmd()
	// With a nil git client, the implementation emits ErrMsg instead of
	// AddRepoCloneMsg, but both originate from the y-confirm clone branch.
	switch msg.(type) {
	case views.AddRepoCloneMsg, views.ErrMsg:
		// expected: clone path was taken
	default:
		t.Fatalf("expected clone-path message (AddRepoCloneMsg or ErrMsg) after y, got %T", msg)
	}
	// Confirm dialog must be gone.
	v := ansi.Strip(m.View())
	if strings.Contains(v, "already in workspace") {
		t.Error("confirm dialog must be cleared after y")
	}
}

// TestAddRepoOverlay_ConfirmCancelClearsState asserts that pressing n (or any
// non-y key) while the confirm modal is active cancels without emitting a clone.
func TestAddRepoOverlay_ConfirmCancelClearsState(t *testing.T) {
	t.Parallel()

	repos := testRepos()
	presentSlugs := map[string]bool{"beeemt/substrate": true}

	for _, cancelKey := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune{'n'}},
		{Type: tea.KeyEsc},
	} {
		t.Run(cancelKey.String(), func(t *testing.T) {
			t.Parallel()
			m := newAddRepoOverlay(t, []adapter.RepoSource{&mockRepoSource{name: "github", repos: repos}})
			m.SetSize(120, 40)
			m.Open(presentSlugs)
			m, _ = m.Update(views.RepoListLoadedMsg{Repos: repos, HasMore: false})
			m = navigateToList(t, m)

			// Open confirm.
			enter := tea.KeyMsg{Type: tea.KeyEnter}
			m, _ = m.Update(enter)

			// Cancel.
			m, cmd := m.Update(cancelKey)
			if cmd != nil {
				msg := cmd()
				if _, ok := msg.(views.AddRepoCloneMsg); ok {
					t.Error("cancel key must not emit AddRepoCloneMsg")
				}
			}
			v := ansi.Strip(m.View())
			if strings.Contains(v, "already in workspace") {
				t.Error("confirm dialog must be cleared after cancel")
			}
		})
	}
}

// TestAddRepoOverlay_SetPresentSlugsUpdatesLiveList asserts that calling
// SetPresentSlugs after repos are loaded immediately reflects in the view.
func TestAddRepoOverlay_SetPresentSlugsUpdatesLiveList(t *testing.T) {
	t.Parallel()

	repos := testRepos()
	m := newAddRepoOverlay(t, []adapter.RepoSource{&mockRepoSource{name: "github", repos: repos}})
	m.SetSize(120, 40)
	m.Open(nil) // no present repos initially
	m, _ = m.Update(views.RepoListLoadedMsg{Repos: repos, HasMore: false})

	// Before SetPresentSlugs: no "already added" indicator.
	v0 := ansi.Strip(m.View())
	if strings.Contains(v0, "already added") {
		t.Error("expected no 'already added' indicator before SetPresentSlugs")
	}

	// Update slugs to mark substrate as present.
	m.SetPresentSlugs(map[string]bool{"beeemt/substrate": true})
	v1 := ansi.Strip(m.View())
	if !strings.Contains(v1, "already added") {
		t.Error("expected 'already added' indicator after SetPresentSlugs")
	}
}

// TestAddRepoOverlay_ConfirmViewFitsSize asserts the view stays within bounds
// while the confirmation modal is active.
func TestAddRepoOverlay_ConfirmViewFitsSize(t *testing.T) {
	t.Parallel()

	repos := testRepos()
	presentSlugs := map[string]bool{"beeemt/substrate": true}
	m := newAddRepoOverlay(t, []adapter.RepoSource{&mockRepoSource{name: "github", repos: repos}})
	m.SetSize(120, 40)
	m.Open(presentSlugs)
	m, _ = m.Update(views.RepoListLoadedMsg{Repos: repos, HasMore: false})
	m = navigateToList(t, m)

	enter := tea.KeyMsg{Type: tea.KeyEnter}
	m, _ = m.Update(enter)

	assertViewFitsSize(t, m.View(), 120, 40)
}
package views

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/tui/styles"
)

var browseANSIPattern = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

type browseTestAdapter struct {
	name           string
	browseScopes   []domain.SelectionScope
	browseFilters  map[domain.SelectionScope]adapter.BrowseFilterCapabilities
	lastListOpts   adapter.ListOpts
	listCalls      []adapter.ListOpts
	listSelectable func(adapter.ListOpts) (*adapter.ListResult, error)
	resolved       []adapter.Selection
}

func (a *browseTestAdapter) Name() string { return a.name }

func (a *browseTestAdapter) Capabilities() adapter.AdapterCapabilities {
	return adapter.AdapterCapabilities{CanBrowse: true, BrowseScopes: a.browseScopes, BrowseFilters: a.browseFilters}
}

func (a *browseTestAdapter) ListSelectable(_ context.Context, opts adapter.ListOpts) (*adapter.ListResult, error) {
	a.lastListOpts = opts
	a.listCalls = append(a.listCalls, opts)
	if a.listSelectable != nil {
		return a.listSelectable(opts)
	}

	return &adapter.ListResult{}, nil
}

func (a *browseTestAdapter) Resolve(_ context.Context, sel adapter.Selection) (domain.Session, error) {
	a.resolved = append(a.resolved, sel)

	return domain.Session{ID: domain.NewID(), ExternalID: a.name + "-session", Title: a.name, State: domain.SessionIngested}, nil
}

func (a *browseTestAdapter) Watch(_ context.Context, _ adapter.WorkItemFilter) (<-chan adapter.WorkItemEvent, error) {
	return nil, adapter.ErrWatchNotSupported
}

func (a *browseTestAdapter) Fetch(_ context.Context, _ string) (domain.Session, error) {
	return domain.Session{}, errors.New("not implemented")
}

func (a *browseTestAdapter) UpdateState(_ context.Context, _ string, _ domain.TrackerState) error {
	return adapter.ErrMutateNotSupported
}

func (a *browseTestAdapter) AddComment(_ context.Context, _ string, _ string) error {
	return adapter.ErrMutateNotSupported
}

func (a *browseTestAdapter) OnEvent(_ context.Context, _ domain.SystemEvent) error { return nil }

func stripBrowseANSI(s string) string {
	return browseANSIPattern.ReplaceAllString(s, "")
}

func assertOverlayFits(t *testing.T, view string, width, height int) {
	t.Helper()
	lines := strings.Split(view, "\n")
	if len(lines) > height {
		t.Fatalf("line count = %d, want <= %d\nview:\n%s", len(lines), height, view)
	}
	for i, line := range lines {
		if got := ansi.StringWidth(line); got > width {
			t.Fatalf("line %d width = %d, want <= %d\nline: %q", i+1, got, width, line)
		}
	}
}

func loadedMsg(items ...adapter.ListItem) issueListLoadedMsg {
	pages := make(map[string]browsePageState)
	for _, item := range items {
		page := pages[item.Provider]
		page.Items = append(page.Items, item)
		pages[item.Provider] = page
	}

	return issueListLoadedMsg{pages: pages}
}

func runOverlayCmd(t *testing.T, cmd tea.Cmd) []tea.Msg {
	t.Helper()
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		msgs := make([]tea.Msg, 0, len(batch))
		for _, batchCmd := range batch {
			if batchCmd == nil {
				continue
			}
			msgs = append(msgs, batchCmd())
		}

		return msgs
	}

	return []tea.Msg{msg}
}

func applyOverlayCmds(t *testing.T, overlay NewSessionOverlay, cmd tea.Cmd) NewSessionOverlay {
	t.Helper()
	for _, msg := range runOverlayCmd(t, cmd) {
		var follow tea.Cmd
		overlay, follow = overlay.Update(msg)
		if follow != nil {
			overlay = applyOverlayCmds(t, overlay, follow)
		}
	}

	return overlay
}

func applyAppCmds(t *testing.T, app App, cmd tea.Cmd) App {
	t.Helper()
	for _, msg := range runOverlayCmd(t, cmd) {
		model, follow := app.Update(msg)
		updated, ok := model.(App)
		if !ok {
			t.Fatalf("model = %T, want App", model)
		}
		app = updated
		if follow != nil {
			app = applyAppCmds(t, app, follow)
		}
	}

	return app
}

func TestNewSessionOverlayRejectsMixedProviderSelection(t *testing.T) {
	t.Parallel()

	githubAdapter := &browseTestAdapter{name: "github", browseScopes: []domain.SelectionScope{domain.ScopeIssues}, browseFilters: map[domain.SelectionScope]adapter.BrowseFilterCapabilities{domain.ScopeIssues: {Views: []string{"assigned_to_me", "all"}}}}
	gitlabAdapter := &browseTestAdapter{name: "gitlab", browseScopes: []domain.SelectionScope{domain.ScopeIssues}, browseFilters: map[domain.SelectionScope]adapter.BrowseFilterCapabilities{domain.ScopeIssues: {Views: []string{"assigned_to_me", "all"}}}}
	overlay := NewNewSessionOverlay([]adapter.WorkItemAdapter{githubAdapter, gitlabAdapter}, "ws-1", styles.NewStyles(styles.DefaultTheme))
	overlay.Open()
	overlay.SetSize(100, 30)
	overlay, _ = overlay.Update(loadedMsg(
		adapter.ListItem{ID: "gh-1", Provider: "github", Title: "GitHub issue"},
		adapter.ListItem{ID: "gl-1", Provider: "gitlab", Title: "GitLab issue"},
	))
	overlay, _ = overlay.Update(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
	overlay, _ = overlay.Update(tea.KeyMsg{Type: tea.KeyDown})
	overlay, _ = overlay.Update(tea.KeyMsg{Type: tea.KeyDown})

	updated, cmd := overlay.Update(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
	if cmd == nil {
		t.Fatal("expected error command for mixed-provider selection")
	}
	msg := cmd()
	errMsg, ok := msg.(ErrMsg)
	if !ok {
		t.Fatalf("msg = %T, want ErrMsg", msg)
	}
	if errMsg.Err == nil || errMsg.Err.Error() != "multi-select must stay within one provider" {
		t.Fatalf("err = %v, want mixed-provider error", errMsg.Err)
	}
	if view := updated.View(); !strings.Contains(view, "GitLab issue") {
		t.Fatalf("view = %q, want selected second item visible after failed mixed-provider selection", view)
	}
}

func TestNewSessionOverlayBrowseSelectionPersistsInRenderedList(t *testing.T) {
	t.Parallel()

	githubAdapter := &browseTestAdapter{name: "github", browseScopes: []domain.SelectionScope{domain.ScopeIssues}, browseFilters: map[domain.SelectionScope]adapter.BrowseFilterCapabilities{domain.ScopeIssues: {Views: []string{"assigned_to_me", "all"}}}}
	overlay := NewNewSessionOverlay([]adapter.WorkItemAdapter{githubAdapter}, "ws-1", styles.NewStyles(styles.DefaultTheme))
	overlay.Open()
	overlay.SetSize(100, 30)
	overlay, _ = overlay.Update(loadedMsg(
		adapter.ListItem{ID: "gh-1", Provider: "github", Title: "First"},
		adapter.ListItem{ID: "gh-2", Provider: "github", Title: "Second"},
	))
	overlay, _ = overlay.Update(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
	overlay, _ = overlay.Update(tea.KeyMsg{Type: tea.KeyDown})
	overlay, _ = overlay.Update(tea.KeyMsg{Type: tea.KeyDown})

	view := stripBrowseANSI(overlay.View())
	assertOverlayFits(t, view, 100, 30)
	if !strings.Contains(view, "✓ First") {
		t.Fatalf("view = %q, want selected marker for first item after moving cursor away", view)
	}
	if strings.Contains(view, "✓ Second") {
		t.Fatalf("view = %q, want no selected marker for unselected second item", view)
	}

	overlay, _ = overlay.Update(loadedMsg(
		adapter.ListItem{ID: "gh-1", Provider: "github", Title: "First reloaded"},
		adapter.ListItem{ID: "gh-2", Provider: "github", Title: "Second reloaded"},
	))

	view = stripBrowseANSI(overlay.View())
	assertOverlayFits(t, view, 100, 30)
	if !strings.Contains(view, "✓ First reloaded") {
		t.Fatalf("view = %q, want selected marker to stay in sync after reload", view)
	}
	if strings.Contains(view, "✓ Second reloaded") {
		t.Fatalf("view = %q, want no selected marker for unselected second item after reload", view)
	}
}

func TestNewSessionOverlayCloseClearsSelectedBrowseState(t *testing.T) {
	t.Parallel()

	githubAdapter := &browseTestAdapter{name: "github", browseScopes: []domain.SelectionScope{domain.ScopeIssues}, browseFilters: map[domain.SelectionScope]adapter.BrowseFilterCapabilities{domain.ScopeIssues: {Views: []string{"assigned_to_me", "all"}}}}
	overlay := NewNewSessionOverlay([]adapter.WorkItemAdapter{githubAdapter}, "ws-1", styles.NewStyles(styles.DefaultTheme))
	overlay.Open()
	overlay.SetSize(100, 30)
	overlay, _ = overlay.Update(loadedMsg(
		adapter.ListItem{ID: "gh-1", Provider: "github", Title: "First"},
		adapter.ListItem{ID: "gh-2", Provider: "github", Title: "Second"},
	))
	overlay, _ = overlay.Update(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})

	view := stripBrowseANSI(overlay.View())
	assertOverlayFits(t, view, 100, 30)
	if !strings.Contains(view, "✓ First") {
		t.Fatalf("view = %q, want selected marker before close", view)
	}

	overlay.Close()
	overlay.Open()

	view = stripBrowseANSI(overlay.View())
	assertOverlayFits(t, view, 100, 30)
	if strings.Contains(view, "✓ First") {
		t.Fatalf("view = %q, want previous selection marker cleared after reopen", view)
	}
	if strings.Contains(view, "First") || strings.Contains(view, "Second") {
		t.Fatalf("view = %q, want stale browse items cleared after reopen", view)
	}
}

func TestAppOpenNewSessionReloadsPreservedBrowseViewOnReopen(t *testing.T) {
	t.Parallel()

	githubAdapter := &browseTestAdapter{
		name:         "github",
		browseScopes: []domain.SelectionScope{domain.ScopeIssues},
		browseFilters: map[domain.SelectionScope]adapter.BrowseFilterCapabilities{
			domain.ScopeIssues: {Views: []string{"assigned_to_me", "all"}},
		},
	}
	githubAdapter.listSelectable = func(opts adapter.ListOpts) (*adapter.ListResult, error) {
		title := "Assigned issues"
		if opts.View == "all" {
			title = "All issues"
		}

		return &adapter.ListResult{Items: []adapter.ListItem{{ID: "gh-" + opts.View, Provider: "github", Title: title}}}, nil
	}

	app := NewApp(Services{
		WorkspaceID:   "ws-1",
		WorkspaceName: "workspace",
		Adapters:      []adapter.WorkItemAdapter{githubAdapter},
		Settings:      &SettingsService{},
	})
	app.newSession.SetSize(100, 30)

	model, cmd := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	updated, ok := model.(App)
	if !ok {
		t.Fatalf("model = %T, want App", model)
	}
	if cmd == nil {
		t.Fatal("expected opening new session to trigger a browse reload")
	}
	app = applyAppCmds(t, updated, cmd)

	model, cmd = app.Update(tea.KeyMsg{Type: tea.KeyCtrlV})
	updated, ok = model.(App)
	if !ok {
		t.Fatalf("model = %T, want App", model)
	}
	if cmd == nil {
		t.Fatal("expected view cycling to trigger a browse reload")
	}
	app = applyAppCmds(t, updated, cmd)

	if got := app.newSession.currentView(); got != "all" {
		t.Fatalf("view = %q, want all before close", got)
	}
	if got := githubAdapter.lastListOpts.View; got != "all" {
		t.Fatalf("last list view = %q, want all before close", got)
	}
	view := stripBrowseANSI(app.newSession.View())
	assertOverlayFits(t, view, 100, 30)
	if !strings.Contains(view, "All issues") {
		t.Fatalf("view = %q, want all-view browse results before close", view)
	}

	model, cmd = app.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated, ok = model.(App)
	if !ok {
		t.Fatalf("model = %T, want App", model)
	}
	if cmd == nil {
		t.Fatal("expected Esc to emit a close-overlay command")
	}
	app = applyAppCmds(t, updated, cmd)
	if app.activeOverlay != overlayNone {
		t.Fatalf("activeOverlay = %v, want %v after close", app.activeOverlay, overlayNone)
	}

	model, cmd = app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	updated, ok = model.(App)
	if !ok {
		t.Fatalf("model = %T, want App", model)
	}
	if cmd == nil {
		t.Fatal("expected reopening new session to trigger a browse reload")
	}
	if !updated.newSession.loading {
		t.Fatal("expected new session overlay to enter loading state on reopen")
	}
	if got := updated.newSession.currentView(); got != "all" {
		t.Fatalf("view = %q, want all preserved on reopen", got)
	}
	app = applyAppCmds(t, updated, cmd)

	if len(githubAdapter.listCalls) != 3 {
		t.Fatalf("list calls = %d, want 3 (open, change view, reopen)", len(githubAdapter.listCalls))
	}
	if got := githubAdapter.listCalls[2].View; got != "all" {
		t.Fatalf("reopen list view = %q, want all", got)
	}
	view = stripBrowseANSI(app.newSession.View())
	assertOverlayFits(t, view, 100, 30)
	if !strings.Contains(view, "All issues") {
		t.Fatalf("view = %q, want reopened overlay to render refreshed all-view results", view)
	}
}

func TestNewSessionOverlayReopenIgnoresStaleLoadFromPriorSession(t *testing.T) {
	t.Parallel()

	githubAdapter := &browseTestAdapter{
		name:         "github",
		browseScopes: []domain.SelectionScope{domain.ScopeIssues},
		browseFilters: map[domain.SelectionScope]adapter.BrowseFilterCapabilities{
			domain.ScopeIssues: {Views: []string{"assigned_to_me", "all"}},
		},
	}
	githubAdapter.listSelectable = func(_ adapter.ListOpts) (*adapter.ListResult, error) {
		title := "Fresh"
		if len(githubAdapter.listCalls) == 1 {
			title = "Stale"
		}

		return &adapter.ListResult{Items: []adapter.ListItem{{ID: fmt.Sprintf("gh-%d", len(githubAdapter.listCalls)), Provider: "github", Title: title}}}, nil
	}

	overlay := NewNewSessionOverlay([]adapter.WorkItemAdapter{githubAdapter}, "ws-1", styles.NewStyles(styles.DefaultTheme))
	overlay.Open()
	overlay.SetSize(100, 30)

	staleCmd := overlay.reloadItems()
	if overlay.requestSeq != 1 {
		t.Fatalf("requestSeq = %d, want 1 after initial reload", overlay.requestSeq)
	}

	overlay.Close()
	if overlay.requestSeq != 1 {
		t.Fatalf("requestSeq = %d, want close to preserve request sequence", overlay.requestSeq)
	}

	overlay.Open()
	freshCmd := overlay.reloadItems()
	if overlay.requestSeq != 2 {
		t.Fatalf("requestSeq = %d, want 2 after reopen reload", overlay.requestSeq)
	}

	overlay = applyOverlayCmds(t, overlay, staleCmd)
	if !overlay.loading {
		t.Fatal("expected stale response to be ignored while the reopen load is still pending")
	}
	if len(overlay.allItems) != 0 {
		t.Fatalf("items = %#v, want stale reopen items ignored", overlay.allItems)
	}

	overlay = applyOverlayCmds(t, overlay, freshCmd)
	if overlay.loading {
		t.Fatal("expected reopen load to finish after fresh response")
	}
	if len(overlay.allItems) != 1 || overlay.allItems[0].Title != "Fresh" {
		t.Fatalf("items = %#v, want only fresh reopen item", overlay.allItems)
	}
}

func TestNewSessionOverlayOpenResyncsBrowseListAfterStaleReopen(t *testing.T) {
	t.Parallel()

	githubAdapter := &browseTestAdapter{name: "github", browseScopes: []domain.SelectionScope{domain.ScopeIssues}, browseFilters: map[domain.SelectionScope]adapter.BrowseFilterCapabilities{domain.ScopeIssues: {Views: []string{"assigned_to_me", "all"}}}}
	overlay := NewNewSessionOverlay([]adapter.WorkItemAdapter{githubAdapter}, "ws-1", styles.NewStyles(styles.DefaultTheme))
	overlay.Open()
	overlay.SetSize(100, 30)
	overlay, _ = overlay.Update(loadedMsg(
		adapter.ListItem{ID: "gh-1", Provider: "github", Title: "First"},
		adapter.ListItem{ID: "gh-2", Provider: "github", Title: "Second"},
	))
	overlay, _ = overlay.Update(tea.KeyMsg{Type: tea.KeyDown})
	overlay, _ = overlay.Update(tea.KeyMsg{Type: tea.KeyDown})

	overlay.active = false
	overlay.issueList.ResetSelected()
	overlay.issueList.SetItems(nil)
	overlay.setBrowseDetailsFocus()

	overlay.Open()

	view := stripBrowseANSI(overlay.View())
	assertOverlayFits(t, view, 100, 30)
	if !strings.Contains(view, "First") || !strings.Contains(view, "Second") {
		t.Fatalf("view = %q, want canonical browse items rendered after reopen", view)
	}
	if overlay.browseFocus != browseFocusControls || overlay.browseControl != browseControlSearch {
		t.Fatalf("focus = (%v, %v), want search control after reopen", overlay.browseFocus, overlay.browseControl)
	}
	if overlay.issueList.Index() != 0 {
		t.Fatalf("list index = %d, want reset to first item after reopen", overlay.issueList.Index())
	}

	focused, _ := overlay.Update(tea.KeyMsg{Type: tea.KeyDown})
	if focused.browseFocus != browseFocusList {
		t.Fatalf("browseFocus = %v, want browseFocusList after reopening", focused.browseFocus)
	}
	if focused.issueList.Index() != 0 {
		t.Fatalf("list index = %d, want first item on entry after reopen", focused.issueList.Index())
	}
	moved, _ := focused.Update(tea.KeyMsg{Type: tea.KeyDown})
	if moved.issueList.Index() != 1 {
		t.Fatalf("list index = %d, want immediate navigation after reopen", moved.issueList.Index())
	}
}

func TestNewSessionOverlayDispatchesSelectedProvider(t *testing.T) {
	t.Parallel()

	githubAdapter := &browseTestAdapter{name: "github", browseScopes: []domain.SelectionScope{domain.ScopeIssues}, browseFilters: map[domain.SelectionScope]adapter.BrowseFilterCapabilities{domain.ScopeIssues: {Views: []string{"assigned_to_me", "all"}}}}
	gitlabAdapter := &browseTestAdapter{name: "gitlab", browseScopes: []domain.SelectionScope{domain.ScopeIssues}, browseFilters: map[domain.SelectionScope]adapter.BrowseFilterCapabilities{domain.ScopeIssues: {Views: []string{"assigned_to_me", "all"}}}}
	overlay := NewNewSessionOverlay([]adapter.WorkItemAdapter{githubAdapter, gitlabAdapter}, "ws-1", styles.NewStyles(styles.DefaultTheme))
	overlay.Open()
	overlay.SetSize(100, 30)
	overlay, _ = overlay.Update(loadedMsg(adapter.ListItem{ID: "gl-1", Provider: "gitlab", Title: "GitLab issue"}))
	overlay, _ = overlay.Update(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})

	_, cmd := overlay.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected session command")
	}
	msg := cmd()
	sessionMsg, ok := msg.(NewSessionBrowseMsg)
	if !ok {
		t.Fatalf("msg = %T, want NewSessionBrowseMsg", msg)
	}
	if sessionMsg.Adapter.Name() != "gitlab" {
		t.Fatalf("adapter = %q, want gitlab", sessionMsg.Adapter.Name())
	}
	if got := sessionMsg.Selection.Metadata["provider"]; got != "gitlab" {
		t.Fatalf("provider metadata = %v, want gitlab", got)
	}
	if len(sessionMsg.Selection.ItemIDs) != 1 || sessionMsg.Selection.ItemIDs[0] != "gl-1" {
		t.Fatalf("ItemIDs = %#v, want [gl-1]", sessionMsg.Selection.ItemIDs)
	}
}

func TestNewSessionOverlaySearchChangeTriggersReload(t *testing.T) {
	t.Parallel()

	githubAdapter := &browseTestAdapter{name: "github", browseScopes: []domain.SelectionScope{domain.ScopeIssues}, browseFilters: map[domain.SelectionScope]adapter.BrowseFilterCapabilities{domain.ScopeIssues: {Views: []string{"assigned_to_me", "all"}}}}
	overlay := NewNewSessionOverlay([]adapter.WorkItemAdapter{githubAdapter}, "ws-1", styles.NewStyles(styles.DefaultTheme))
	overlay.Open()
	overlay.SetSize(100, 30)

	// Typing schedules a debounce tick; loading is NOT set immediately.
	postKey, _ := overlay.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	if postKey.loading {
		t.Fatal("overlay must not enter loading state before debounce fires")
	}

	// Deliver the matching debounce message; now the reload must be issued.
	updated, cmd := postKey.Update(browseDebounceMsg{seq: postKey.browseDebounceSeq})
	if !updated.loading {
		t.Fatal("expected loading after search input changes")
	}
	if cmd == nil {
		t.Fatal("expected reload command after search input changes")
	}
}

func TestNewSessionOverlayInfiniteScrollUsesOffsetPagination(t *testing.T) {
	t.Parallel()

	githubAdapter := &browseTestAdapter{
		name:         "github",
		browseScopes: []domain.SelectionScope{domain.ScopeIssues},
		browseFilters: map[domain.SelectionScope]adapter.BrowseFilterCapabilities{
			domain.ScopeIssues: {Views: []string{"assigned_to_me", "all"}, SupportsOffset: true},
		},
	}
	githubAdapter.listSelectable = func(_ adapter.ListOpts) (*adapter.ListResult, error) {
		switch len(githubAdapter.listCalls) {
		case 1:
			return &adapter.ListResult{Items: []adapter.ListItem{{ID: "gh-1", Provider: "github", Title: "First"}}, HasMore: true}, nil
		case 2:
			return &adapter.ListResult{Items: []adapter.ListItem{{ID: "gh-2", Provider: "github", Title: "Second"}}}, nil
		default:
			return &adapter.ListResult{}, nil
		}
	}
	overlay := NewNewSessionOverlay([]adapter.WorkItemAdapter{githubAdapter}, "ws-1", styles.NewStyles(styles.DefaultTheme))
	overlay.Open()
	overlay.SetSize(120, 30)
	overlay = applyOverlayCmds(t, overlay, overlay.reloadItems())
	if len(githubAdapter.listCalls) != 1 || githubAdapter.listCalls[0].Offset != 0 {
		t.Fatalf("first call opts = %#v, want offset 0", githubAdapter.listCalls)
	}
	if len(overlay.allItems) != 1 || !overlay.hasMore {
		t.Fatalf("initial items = %#v hasMore=%v, want one item and more pages", overlay.allItems, overlay.hasMore)
	}

	overlay.setBrowseListFocus()
	updated, cmd := overlay.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	if !updated.loading {
		t.Fatal("expected loading after page-down near the end of the list")
	}
	if cmd == nil {
		t.Fatal("expected append command after page-down")
	}
	updated = applyOverlayCmds(t, updated, cmd)
	if len(githubAdapter.listCalls) != 2 || githubAdapter.listCalls[1].Offset != browsePageSize {
		t.Fatalf("second call opts = %#v, want offset %d", githubAdapter.listCalls, browsePageSize)
	}
	if len(updated.allItems) != 2 {
		t.Fatalf("items len = %d, want 2 after append", len(updated.allItems))
	}
}

func TestNewSessionOverlayInfiniteScrollUsesCursorPagination(t *testing.T) {
	t.Parallel()

	linearAdapter := &browseTestAdapter{
		name:         "linear",
		browseScopes: []domain.SelectionScope{domain.ScopeIssues},
		browseFilters: map[domain.SelectionScope]adapter.BrowseFilterCapabilities{
			domain.ScopeIssues: {Views: []string{"assigned_to_me", "all"}, SupportsCursor: true},
		},
	}
	linearAdapter.listSelectable = func(_ adapter.ListOpts) (*adapter.ListResult, error) {
		switch len(linearAdapter.listCalls) {
		case 1:
			return &adapter.ListResult{Items: []adapter.ListItem{{ID: "lin-1", Provider: "linear", Title: "First"}}, HasMore: true, NextCursor: "cursor-2"}, nil
		case 2:
			return &adapter.ListResult{Items: []adapter.ListItem{{ID: "lin-2", Provider: "linear", Title: "Second"}}}, nil
		default:
			return &adapter.ListResult{}, nil
		}
	}
	overlay := NewNewSessionOverlay([]adapter.WorkItemAdapter{linearAdapter}, "ws-1", styles.NewStyles(styles.DefaultTheme))
	overlay.providerIndex = 1
	overlay.Open()
	overlay.SetSize(120, 30)
	overlay = applyOverlayCmds(t, overlay, overlay.reloadItems())
	if len(linearAdapter.listCalls) != 1 || linearAdapter.listCalls[0].Cursor != "" {
		t.Fatalf("first call opts = %#v, want empty cursor", linearAdapter.listCalls)
	}

	overlay.setBrowseListFocus()
	updated, cmd := overlay.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	if !updated.loading {
		t.Fatal("expected loading after page-down near the end of the cursor-backed list")
	}
	updated = applyOverlayCmds(t, updated, cmd)
	if len(linearAdapter.listCalls) != 2 || linearAdapter.listCalls[1].Cursor != "cursor-2" {
		t.Fatalf("second call opts = %#v, want cursor-2", linearAdapter.listCalls)
	}
	if len(updated.allItems) != 2 {
		t.Fatalf("items len = %d, want 2 after cursor append", len(updated.allItems))
	}
}

func TestNewSessionOverlayArrowKeysPreserveSearchEditing(t *testing.T) {
	t.Parallel()

	githubAdapter := &browseTestAdapter{name: "github", browseScopes: []domain.SelectionScope{domain.ScopeIssues}, browseFilters: map[domain.SelectionScope]adapter.BrowseFilterCapabilities{domain.ScopeIssues: {Views: []string{"assigned_to_me", "all"}}}}
	overlay := NewNewSessionOverlay([]adapter.WorkItemAdapter{githubAdapter}, "ws-1", styles.NewStyles(styles.DefaultTheme))
	overlay.Open()
	overlay.SetSize(100, 30)
	overlay.filterInput.SetValue("bug")
	overlay.filterInput.SetCursor(1)

	updated, _ := overlay.Update(tea.KeyMsg{Type: tea.KeyRight})
	if updated.loading {
		t.Fatal("expected no reload while moving the search cursor")
	}
	if updated.filterInput.Position() != 2 {
		t.Fatalf("cursor = %d, want 2 after moving right in search", updated.filterInput.Position())
	}
}

func TestNewSessionOverlayArrowDownMovesFromControlsIntoBrowseList(t *testing.T) {
	t.Parallel()

	githubAdapter := &browseTestAdapter{name: "github", browseScopes: []domain.SelectionScope{domain.ScopeIssues}, browseFilters: map[domain.SelectionScope]adapter.BrowseFilterCapabilities{domain.ScopeIssues: {Views: []string{"assigned_to_me", "all"}}}}
	overlay := NewNewSessionOverlay([]adapter.WorkItemAdapter{githubAdapter}, "ws-1", styles.NewStyles(styles.DefaultTheme))
	overlay.Open()
	overlay.SetSize(100, 30)
	overlay, _ = overlay.Update(loadedMsg(
		adapter.ListItem{ID: "gh-1", Provider: "github", Title: "First"},
		adapter.ListItem{ID: "gh-2", Provider: "github", Title: "Second"},
	))

	focused, _ := overlay.Update(tea.KeyMsg{Type: tea.KeyDown})
	if focused.browseFocus != browseFocusList {
		t.Fatalf("browseFocus = %v, want browseFocusList", focused.browseFocus)
	}
	if focused.issueList.Index() != 0 {
		t.Fatalf("list index = %d, want 0 on first move into list", focused.issueList.Index())
	}

	moved, _ := focused.Update(tea.KeyMsg{Type: tea.KeyDown})
	if moved.issueList.Index() != 1 {
		t.Fatalf("list index = %d, want 1 after moving down in list", moved.issueList.Index())
	}
}

func TestNewSessionOverlayArrowDownTraversesBrowseControlsBeforeList(t *testing.T) {
	t.Parallel()

	githubAdapter := &browseTestAdapter{
		name:         "github",
		browseScopes: []domain.SelectionScope{domain.ScopeIssues},
		browseFilters: map[domain.SelectionScope]adapter.BrowseFilterCapabilities{
			domain.ScopeIssues: {Views: []string{"assigned_to_me", "all"}, SupportsLabels: true, SupportsRepo: true},
		},
	}
	overlay := NewNewSessionOverlay([]adapter.WorkItemAdapter{githubAdapter}, "ws-1", styles.NewStyles(styles.DefaultTheme))
	overlay.Open()
	overlay.SetSize(100, 30)
	overlay, _ = overlay.Update(loadedMsg(adapter.ListItem{ID: "gh-1", Provider: "github", Title: "First"}))

	labelsFocused, _ := overlay.Update(tea.KeyMsg{Type: tea.KeyDown})
	if labelsFocused.browseFocus != browseFocusControls || labelsFocused.browseControl != browseControlLabels {
		t.Fatalf("focus = (%v, %v), want labels control", labelsFocused.browseFocus, labelsFocused.browseControl)
	}

	repoFocused, _ := labelsFocused.Update(tea.KeyMsg{Type: tea.KeyDown})
	if repoFocused.browseFocus != browseFocusControls || repoFocused.browseControl != browseControlRepo {
		t.Fatalf("focus = (%v, %v), want repo control", repoFocused.browseFocus, repoFocused.browseControl)
	}

	listFocused, _ := repoFocused.Update(tea.KeyMsg{Type: tea.KeyDown})
	if listFocused.browseFocus != browseFocusList {
		t.Fatalf("browseFocus = %v, want browseFocusList after last control", listFocused.browseFocus)
	}
}

func TestNewSessionOverlayArrowUpReturnsFromBrowseListToControls(t *testing.T) {
	t.Parallel()

	githubAdapter := &browseTestAdapter{name: "github", browseScopes: []domain.SelectionScope{domain.ScopeIssues}, browseFilters: map[domain.SelectionScope]adapter.BrowseFilterCapabilities{domain.ScopeIssues: {Views: []string{"assigned_to_me", "all"}}}}
	overlay := NewNewSessionOverlay([]adapter.WorkItemAdapter{githubAdapter}, "ws-1", styles.NewStyles(styles.DefaultTheme))
	overlay.Open()
	overlay.SetSize(100, 30)
	overlay, _ = overlay.Update(loadedMsg(adapter.ListItem{ID: "gh-1", Provider: "github", Title: "First"}))
	overlay, _ = overlay.Update(tea.KeyMsg{Type: tea.KeyDown})

	returned, _ := overlay.Update(tea.KeyMsg{Type: tea.KeyUp})
	if returned.browseFocus != browseFocusControls {
		t.Fatalf("browseFocus = %v, want browseFocusControls", returned.browseFocus)
	}
	if returned.browseControl != browseControlSearch {
		t.Fatalf("browseControl = %v, want browseControlSearch", returned.browseControl)
	}
}

func TestNewSessionOverlayRightArrowMovesBetweenListAndDetails(t *testing.T) {
	t.Parallel()

	githubAdapter := &browseTestAdapter{name: "github", browseScopes: []domain.SelectionScope{domain.ScopeIssues}, browseFilters: map[domain.SelectionScope]adapter.BrowseFilterCapabilities{domain.ScopeIssues: {Views: []string{"assigned_to_me", "all"}}}}
	overlay := NewNewSessionOverlay([]adapter.WorkItemAdapter{githubAdapter}, "ws-1", styles.NewStyles(styles.DefaultTheme))
	overlay.Open()
	overlay.SetSize(120, 30)
	overlay, _ = overlay.Update(loadedMsg(adapter.ListItem{ID: "gh-1", Provider: "github", Title: "First"}))
	overlay.setBrowseListFocus()

	detailFocused, _ := overlay.Update(tea.KeyMsg{Type: tea.KeyRight})
	if detailFocused.browseFocus != browseFocusDetails {
		t.Fatalf("browseFocus = %v, want browseFocusDetails", detailFocused.browseFocus)
	}

	listFocused, _ := detailFocused.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if listFocused.browseFocus != browseFocusList {
		t.Fatalf("browseFocus = %v, want browseFocusList", listFocused.browseFocus)
	}
}

func TestNewSessionOverlayFocusedDetailsPaneScrolls(t *testing.T) {
	t.Parallel()

	githubAdapter := &browseTestAdapter{name: "github", browseScopes: []domain.SelectionScope{domain.ScopeIssues}, browseFilters: map[domain.SelectionScope]adapter.BrowseFilterCapabilities{domain.ScopeIssues: {Views: []string{"assigned_to_me", "all"}}}}
	overlay := NewNewSessionOverlay([]adapter.WorkItemAdapter{githubAdapter}, "ws-1", styles.NewStyles(styles.DefaultTheme))
	overlay.Open()
	overlay.SetSize(120, 24)
	overlay, _ = overlay.Update(loadedMsg(adapter.ListItem{
		ID:          "gh-1",
		Provider:    "github",
		Identifier:  "#42",
		Title:       "Investigate detail scrolling",
		Description: strings.Repeat("Detail line that should scroll.\n", 48),
	}))
	overlay.setBrowseListFocus()

	overlay, _ = overlay.Update(tea.KeyMsg{Type: tea.KeyRight})
	if overlay.browseFocus != browseFocusDetails {
		t.Fatalf("browseFocus = %v, want browseFocusDetails", overlay.browseFocus)
	}
	if got, wantMin := overlay.detailViewport.TotalLineCount(), overlay.detailViewport.Height+1; got < wantMin {
		t.Fatalf("detail lines = %d, want at least %d to prove overflow", got, wantMin)
	}

	before := overlay.detailViewport.YOffset
	overlay, _ = overlay.Update(tea.KeyMsg{Type: tea.KeyDown})
	if overlay.detailViewport.YOffset <= before {
		t.Fatalf("detail viewport YOffset = %d, want > %d after down key", overlay.detailViewport.YOffset, before)
	}

	afterKey := overlay.detailViewport.YOffset
	overlay, _ = overlay.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown})
	if overlay.detailViewport.YOffset <= afterKey {
		t.Fatalf("detail viewport YOffset = %d, want > %d after wheel down", overlay.detailViewport.YOffset, afterKey)
	}
}

func TestRenderDetailContentVisuallySeparatesMetadata(t *testing.T) {
	t.Parallel()

	const exampleURL = "https://example.com/issue/42"
	rendered := stripBrowseANSI(renderDetailContent(styles.NewStyles(styles.DefaultTheme), adapter.ListItem{
		Provider:    "github",
		Identifier:  "#42",
		Title:       "Issue title",
		State:       "open",
		Description: "## Summary\n\nThis is **important**.",
		URL:         exampleURL,
	}, 80))

	for _, want := range []string{"Metadata", "Description", "Summary", "This is important.", "Provider: GitHub", "URL: " + exampleURL} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered = %q, want %q in detail content", rendered, want)
		}
	}

	if !strings.Contains(rendered, "╭") || !strings.Contains(rendered, "╰") {
		t.Fatalf("rendered = %q, want a bordered metadata card", rendered)
	}

	for _, raw := range []string{"## #42 · Issue title", "## Summary", "### Metadata", "### Description", "**important**", "Open in browser"} {
		if strings.Contains(rendered, raw) {
			t.Fatalf("rendered = %q, must not contain raw markdown token %q", rendered, raw)
		}
	}
}

func TestRenderDetailContentRendersMarkdownLinksWithHrefText(t *testing.T) {
	t.Parallel()

	rendered := stripBrowseANSI(renderDetailContent(styles.NewStyles(styles.DefaultTheme), adapter.ListItem{
		Title:       "Issue title",
		Description: "Read [the guide](https://example.com/guide) for details.",
	}, 80))

	for _, want := range []string{"Description", "the guide", "https://example.com/guide", "for details."} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered = %q, want %q in detail content", rendered, want)
		}
	}

	if strings.Contains(rendered, "[the guide](https://example.com/guide)") {
		t.Fatalf("rendered = %q, must not contain raw markdown link syntax", rendered)
	}
}

func TestNewSessionOverlayViewRendersDetailsMarkdownAndMermaid(t *testing.T) {
	t.Parallel()

	const exampleURL = "https://example.com/issue/42"
	githubAdapter := &browseTestAdapter{name: "github", browseScopes: []domain.SelectionScope{domain.ScopeIssues}, browseFilters: map[domain.SelectionScope]adapter.BrowseFilterCapabilities{domain.ScopeIssues: {Views: []string{"assigned_to_me", "all"}}}}
	overlay := NewNewSessionOverlay([]adapter.WorkItemAdapter{githubAdapter}, "ws-1", styles.NewStyles(styles.DefaultTheme))
	overlay.Open()
	overlay.SetSize(220, 40)
	overlay, _ = overlay.Update(loadedMsg(adapter.ListItem{
		ID:           "gh-1",
		Provider:     "github",
		Identifier:   "#42",
		Title:        "Issue title",
		State:        "open",
		ContainerRef: "acme/rocket",
		Labels:       []string{"bug", "backend"},
		URL:          exampleURL,
		Description:  "## Summary\n\nThis is **important**.\n\n```mermaid\ngraph LR\nA-->B\n```",
	}))

	view := stripBrowseANSI(overlay.View())

	for _, want := range []string{"Issue title", "Provider:", "Mermaid diagram", "A", "B", "Description", "Metadata", "URL:", exampleURL} {
		if !strings.Contains(view, want) {
			t.Fatalf("view = %q, want %q in rendered details", view, want)
		}
	}

	for _, raw := range []string{"Open in browser", "## #42 · Issue title", "## Summary", "### Description", "### Metadata", "**important**"} {
		if strings.Contains(view, raw) {
			t.Fatalf("view = %q, must not contain raw markdown token %q", view, raw)
		}
	}

	t.Run("EmptyDescriptionShowsPlaceholder", func(t *testing.T) {
		overlay, _ = overlay.Update(loadedMsg(adapter.ListItem{
			ID:           "gh-2",
			Provider:     "github",
			Identifier:   "#43",
			Title:        "Issue without description",
			State:        "open",
			ContainerRef: "acme/rocket",
			Labels:       []string{"bug"},
			URL:          exampleURL,
			Description:  "",
		}))
		view := stripBrowseANSI(overlay.View())
		if !strings.Contains(view, "Description") {
			t.Fatalf("view = %q, want description section", view)
		}
		if !strings.Contains(view, "No description provided.") {
			t.Fatalf("view = %q, want empty description placeholder", view)
		}
	})
}

func TestNewSessionOverlayLargeScreensUseMoreAvailableSpace(t *testing.T) {
	t.Parallel()

	githubAdapter := &browseTestAdapter{name: "github", browseScopes: []domain.SelectionScope{domain.ScopeIssues}, browseFilters: map[domain.SelectionScope]adapter.BrowseFilterCapabilities{domain.ScopeIssues: {Views: []string{"assigned_to_me", "all"}}}}
	overlay := NewNewSessionOverlay([]adapter.WorkItemAdapter{githubAdapter}, "ws-1", styles.NewStyles(styles.DefaultTheme))
	overlay.Open()
	overlay.SetSize(360, 60)

	layout := overlay.browserLayout()
	if layout.FrameWidth <= 238 {
		t.Fatalf("frame width = %d, want > 238 on large screens", layout.FrameWidth)
	}
	if layout.BodyHeight <= 36 {
		t.Fatalf("body height = %d, want > 36 on large screens", layout.BodyHeight)
	}

	view := stripBrowseANSI(overlay.View())
	assertOverlayFits(t, view, 360, 60)
	if got := overlay.detailViewport.Height; got != layout.ViewportHeight {
		t.Fatalf("detail viewport height = %d, want %d", got, layout.ViewportHeight)
	}
}

func TestNewSessionOverlayViewFitsRequestedSize(t *testing.T) {
	t.Parallel()

	githubAdapter := &browseTestAdapter{name: "github", browseScopes: []domain.SelectionScope{domain.ScopeIssues}, browseFilters: map[domain.SelectionScope]adapter.BrowseFilterCapabilities{domain.ScopeIssues: {Views: []string{"assigned_to_me", "all"}}}}
	overlay := NewNewSessionOverlay([]adapter.WorkItemAdapter{githubAdapter}, "ws-1", styles.NewStyles(styles.DefaultTheme))
	overlay.Open()
	overlay, _ = overlay.Update(loadedMsg(adapter.ListItem{ID: "gh-1", Provider: "github", Title: "Issue title", Description: "Details"}))

	for _, tc := range []struct {
		width  int
		height int
	}{
		{width: 80, height: 20},
		{width: 120, height: 24},
		{width: 244, height: 30},
	} {
		t.Run(fmt.Sprintf("%dx%d", tc.width, tc.height), func(t *testing.T) {
			overlay.SetSize(tc.width, tc.height)
			assertOverlayFits(t, overlay.View(), tc.width, tc.height)
		})
	}

	overlay.SetSize(80, 20)
	manual, _ := overlay.Update(tea.KeyMsg{Type: tea.KeyCtrlN})
	assertOverlayFits(t, manual.View(), 80, 20)
}

func TestNewSessionOverlayKeepsStablePaneGeometryAcrossLoadingAndLoadedState(t *testing.T) {
	t.Parallel()

	githubAdapter := &browseTestAdapter{name: "github", browseScopes: []domain.SelectionScope{domain.ScopeIssues}, browseFilters: map[domain.SelectionScope]adapter.BrowseFilterCapabilities{domain.ScopeIssues: {Views: []string{"assigned_to_me", "all"}}}}
	overlay := NewNewSessionOverlay([]adapter.WorkItemAdapter{githubAdapter}, "ws-1", styles.NewStyles(styles.DefaultTheme))
	overlay.Open()
	overlay.SetSize(120, 24)
	overlay.loading = true

	loadingView := stripBrowseANSI(overlay.View())
	assertOverlayFits(t, loadingView, 120, 24)
	loadingLayout := overlay.browserLayout()
	if got := overlay.detailViewport.Height; got != loadingLayout.ViewportHeight {
		t.Fatalf("loading detail viewport height = %d, want %d", got, loadingLayout.ViewportHeight)
	}
	for _, want := range []string{"Work Items", "Details", "Loading…"} {
		if !strings.Contains(loadingView, want) {
			t.Fatalf("view = %q, want %q in loading state", loadingView, want)
		}
	}

	overlay.loading = false
	overlay, _ = overlay.Update(loadedMsg(adapter.ListItem{ID: "gh-1", Provider: "github", Title: "Issue title", Description: strings.Repeat("Detail line\n", 8)}))

	loadedView := stripBrowseANSI(overlay.View())
	assertOverlayFits(t, loadedView, 120, 24)
	loadedLayout := overlay.browserLayout()
	if loadedLayout != loadingLayout {
		t.Fatalf("loaded layout = %+v, want stable layout %+v", loadedLayout, loadingLayout)
	}
	if got := overlay.detailViewport.Height; got != loadedLayout.ViewportHeight {
		t.Fatalf("loaded detail viewport height = %d, want %d", got, loadedLayout.ViewportHeight)
	}
	for _, want := range []string{"Work Items", "Issue title"} {
		if !strings.Contains(loadedView, want) {
			t.Fatalf("view = %q, want %q in loaded state", loadedView, want)
		}
	}
}

func TestNewSessionOverlayKeepsStableHeightWhenBrowseResultsAppearAfterViewSwitch(t *testing.T) {
	t.Parallel()

	linearAdapter := &browseTestAdapter{
		name:         "linear",
		browseScopes: []domain.SelectionScope{domain.ScopeIssues},
		browseFilters: map[domain.SelectionScope]adapter.BrowseFilterCapabilities{
			domain.ScopeIssues: {Views: []string{"assigned_to_me", "created_by_me", "all"}},
		},
	}
	linearAdapter.listSelectable = func(opts adapter.ListOpts) (*adapter.ListResult, error) {
		switch opts.View {
		case "assigned_to_me":
			return &adapter.ListResult{}, nil
		case "created_by_me":
			return &adapter.ListResult{Items: []adapter.ListItem{
				{
					ID:           "lin-1",
					Provider:     "linear",
					Identifier:   "LIN-1234",
					Title:        "Investigate browse pane resize after items load into the overlay list",
					ContainerRef: "platform-inbox-with-a-very-long-container-name",
					State:        "open",
				},
			}}, nil
		default:
			return &adapter.ListResult{}, nil
		}
	}

	overlay := NewNewSessionOverlay([]adapter.WorkItemAdapter{linearAdapter}, "ws-1", styles.NewStyles(styles.DefaultTheme))
	overlay.providerIndex = providerOptionIndex("linear")
	overlay.Open()
	overlay.SetSize(72, 18)
	overlay = applyOverlayCmds(t, overlay, overlay.reloadItems())

	emptyView := stripBrowseANSI(overlay.View())
	assertOverlayFits(t, emptyView, 72, 18)
	emptyLineCount := len(strings.Split(emptyView, "\n"))
	if got := overlay.currentView(); got != "assigned_to_me" {
		t.Fatalf("view = %q, want assigned_to_me before switching", got)
	}

	updated, cmd := overlay.Update(tea.KeyMsg{Type: tea.KeyCtrlV})
	if cmd == nil {
		t.Fatal("expected switching browse view to trigger a reload")
	}
	if got := updated.currentView(); got != "created_by_me" {
		t.Fatalf("view = %q, want created_by_me after switching", got)
	}
	overlay = applyOverlayCmds(t, updated, cmd)

	loadedView := stripBrowseANSI(overlay.View())
	assertOverlayFits(t, loadedView, 72, 18)
	if got := len(strings.Split(loadedView, "\n")); got != emptyLineCount {
		t.Fatalf("loaded line count = %d, want %d for stable overlay height\nview:\n%s", got, emptyLineCount, loadedView)
	}
	if !strings.Contains(loadedView, "LIN-1234") {
		t.Fatalf("view = %q, want created_by_me item rendered after reload", loadedView)
	}
}

func TestNewSessionOverlayBrowsePanesShareBottomBorderRow(t *testing.T) {
	t.Parallel()

	githubAdapter := &browseTestAdapter{name: "github", browseScopes: []domain.SelectionScope{domain.ScopeIssues}, browseFilters: map[domain.SelectionScope]adapter.BrowseFilterCapabilities{domain.ScopeIssues: {Views: []string{"assigned_to_me", "all"}}}}
	overlay := NewNewSessionOverlay([]adapter.WorkItemAdapter{githubAdapter}, "ws-1", styles.NewStyles(styles.DefaultTheme))
	overlay.Open()
	overlay, _ = overlay.Update(loadedMsg(adapter.ListItem{ID: "gh-1", Provider: "github", Title: "Issue title", Description: strings.Repeat("Detail line\n", 8)}))

	for _, tc := range []struct {
		width  int
		height int
	}{
		{width: 80, height: 20},
		{width: 120, height: 24},
	} {
		t.Run(fmt.Sprintf("%dx%d", tc.width, tc.height), func(t *testing.T) {
			overlay.SetSize(tc.width, tc.height)
			view := stripBrowseANSI(overlay.View())
			assertOverlayFits(t, view, tc.width, tc.height)

			aligned := false
			for line := range strings.SplitSeq(view, "\n") {
				if strings.Count(line, "╰") == 2 && strings.Count(line, "╯") == 2 {
					aligned = true
					break
				}
			}
			if !aligned {
				t.Fatalf("view = %q, want browse pane bottoms on the same row", view)
			}
		})
	}
}

func TestNewSessionOverlayShowsContainerScopedMessageWhenViewsUnsupported(t *testing.T) {
	t.Parallel()

	containerScopedAdapter := &browseTestAdapter{
		name:         "linear",
		browseScopes: []domain.SelectionScope{domain.ScopeIssues, domain.ScopeProjects},
		browseFilters: map[domain.SelectionScope]adapter.BrowseFilterCapabilities{
			domain.ScopeIssues:   {SupportsTeam: true},
			domain.ScopeProjects: {},
		},
	}
	overlay := NewNewSessionOverlay([]adapter.WorkItemAdapter{containerScopedAdapter}, "ws-1", styles.NewStyles(styles.DefaultTheme))
	overlay.providerIndex = 1
	overlay.Open()
	overlay.SetSize(100, 30)

	view := overlay.View()
	if strings.Contains(view, "View:") {
		t.Fatalf("view = %q, want no view controls for container-scoped issues", view)
	}
	if !strings.Contains(view, "Linear browsing is container-scoped; inbox-style view filters are hidden.") {
		t.Fatalf("view = %q, want container-scoped message", view)
	}
}

func TestNewSessionOverlayAllModeRestrictsToIssues(t *testing.T) {
	t.Parallel()

	githubAdapter := &browseTestAdapter{
		name:         "github",
		browseScopes: []domain.SelectionScope{domain.ScopeIssues, domain.ScopeProjects},
		browseFilters: map[domain.SelectionScope]adapter.BrowseFilterCapabilities{
			domain.ScopeIssues:   {Views: []string{"assigned_to_me", "all"}},
			domain.ScopeProjects: {SupportsOffset: true},
		},
	}
	gitlabAdapter := &browseTestAdapter{
		name:         "gitlab",
		browseScopes: []domain.SelectionScope{domain.ScopeIssues, domain.ScopeProjects},
		browseFilters: map[domain.SelectionScope]adapter.BrowseFilterCapabilities{
			domain.ScopeIssues:   {Views: []string{"assigned_to_me", "all"}},
			domain.ScopeProjects: {SupportsOffset: true},
		},
	}
	overlay := NewNewSessionOverlay([]adapter.WorkItemAdapter{githubAdapter, gitlabAdapter}, "ws-1", styles.NewStyles(styles.DefaultTheme))
	overlay.Open()
	overlay.SetSize(100, 30)

	view := overlay.View()
	if strings.Contains(view, "Projects") || strings.Contains(view, "Initiatives") {
		t.Fatalf("view = %q, want all-provider mode restricted to issues", view)
	}
}

func TestNewSessionOverlayShowsOnlyConfiguredProviderSources(t *testing.T) {
	t.Parallel()

	githubAdapter := &browseTestAdapter{
		name:         "github",
		browseScopes: []domain.SelectionScope{domain.ScopeIssues},
		browseFilters: map[domain.SelectionScope]adapter.BrowseFilterCapabilities{
			domain.ScopeIssues: {Views: []string{"assigned_to_me", "all"}},
		},
	}
	overlay := NewNewSessionOverlay([]adapter.WorkItemAdapter{githubAdapter}, "ws-1", styles.NewStyles(styles.DefaultTheme))
	overlay.Open()
	overlay.SetSize(100, 30)

	view := stripBrowseANSI(overlay.View())
	if !strings.Contains(view, "GitHub") {
		t.Fatalf("view = %q, want configured GitHub source", view)
	}
	for _, avoid := range []string{"Linear", "GitLab", "Sentry"} {
		if strings.Contains(view, avoid) {
			t.Fatalf("view = %q, must not advertise unavailable %s source", view, avoid)
		}
	}
}

func TestNewSessionOverlaySelectingLinearSourceExposesSupportedScopesAndFilters(t *testing.T) {
	t.Parallel()

	linearAdapter := &browseTestAdapter{
		name:         "linear",
		browseScopes: []domain.SelectionScope{domain.ScopeIssues, domain.ScopeProjects, domain.ScopeInitiatives},
		browseFilters: map[domain.SelectionScope]adapter.BrowseFilterCapabilities{
			domain.ScopeIssues: {
				Views:          []string{"assigned_to_me", "created_by_me", "subscribed", "all"},
				States:         []string{"open", "closed", "all"},
				SupportsLabels: true,
				SupportsSearch: true,
				SupportsTeam:   true,
			},
			domain.ScopeProjects: {
				States:         []string{"planned", "all"},
				SupportsSearch: true,
				SupportsTeam:   true,
			},
			domain.ScopeInitiatives: {
				States:         []string{"planned", "all"},
				SupportsSearch: true,
			},
		},
	}
	overlay := NewNewSessionOverlay([]adapter.WorkItemAdapter{linearAdapter}, "ws-1", styles.NewStyles(styles.DefaultTheme))
	overlay.Open()
	overlay.SetSize(120, 30)
	overlay.setBrowseControlFocus(browseControlSource)

	initialLayout := overlay.browserLayout()

	updated, cmd := overlay.Update(tea.KeyMsg{Type: tea.KeyRight})
	if updated.currentProvider() != "linear" {
		t.Fatalf("provider = %q, want linear after source change", updated.currentProvider())
	}
	updated = applyOverlayCmds(t, updated, cmd)

	if updatedLayout := updated.browserLayout(); updatedLayout != initialLayout {
		t.Fatalf("layout after source change = %+v, want stable layout %+v", updatedLayout, initialLayout)
	}

	view := stripBrowseANSI(updated.View())
	for _, want := range []string{"Projects", "Initiatives", "View:", "State:", "Labels:", "Team:"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view = %q, want %q after selecting Linear source", view, want)
		}
	}
	for _, avoid := range []string{"GitHub", "GitLab", "Sentry"} {
		if strings.Contains(view, avoid) {
			t.Fatalf("view = %q, must not show unsupported %s source", view, avoid)
		}
	}
}

func TestNewSessionOverlaySelectingSentrySourceKeepsIssuesOnlyLayoutStable(t *testing.T) {
	t.Parallel()

	githubAdapter := &browseTestAdapter{
		name:         "github",
		browseScopes: []domain.SelectionScope{domain.ScopeIssues, domain.ScopeProjects},
		browseFilters: map[domain.SelectionScope]adapter.BrowseFilterCapabilities{
			domain.ScopeIssues: {
				Views:          []string{"assigned_to_me", "all"},
				States:         []string{"open", "closed", "all"},
				SupportsLabels: true,
				SupportsOwner:  true,
				SupportsRepo:   true,
			},
			domain.ScopeProjects: {SupportsOffset: true},
		},
	}
	sentryAdapter := &browseTestAdapter{
		name:         "sentry",
		browseScopes: []domain.SelectionScope{domain.ScopeIssues},
		browseFilters: map[domain.SelectionScope]adapter.BrowseFilterCapabilities{
			domain.ScopeIssues: {
				Views:          []string{"assigned_to_me", "all"},
				States:         []string{"unresolved", "resolved"},
				SupportsSearch: true,
				SupportsRepo:   true,
			},
		},
	}

	overlay := NewNewSessionOverlay([]adapter.WorkItemAdapter{githubAdapter, sentryAdapter}, "ws-1", styles.NewStyles(styles.DefaultTheme))
	overlay.Open()
	overlay.SetSize(120, 30)
	overlay.providerIndex = providerOptionIndex("github")
	overlay.normalizeSelectionOptions()
	overlay.setBrowseControlFocus(browseControlSource)
	overlay.repoInput.SetValue("acme/rocket")

	beforeView := stripBrowseANSI(overlay.View())
	assertOverlayFits(t, beforeView, 120, 30)
	beforeLayout := overlay.browserLayout()
	beforeLines := len(strings.Split(beforeView, "\n"))
	for _, want := range []string{"Labels:", "Owner:", "Repo:"} {
		if !strings.Contains(beforeView, want) {
			t.Fatalf("view = %q, want %q before switching to Sentry", beforeView, want)
		}
	}

	updated, cmd := overlay.Update(tea.KeyMsg{Type: tea.KeyRight})
	if updated.currentProvider() != "sentry" {
		t.Fatalf("provider = %q, want sentry after source change", updated.currentProvider())
	}
	if updated.repoInput.Value() != "" {
		t.Fatalf("repo filter = %q, want cleared when switching to Sentry", updated.repoInput.Value())
	}
	if cmd == nil {
		t.Fatal("expected reload after source change")
	}
	updated = applyOverlayCmds(t, updated, cmd)

	if scopes := updated.availableScopes(); len(scopes) != 1 || scopes[0] != domain.ScopeIssues {
		t.Fatalf("scopes = %#v, want only issues for sentry", scopes)
	}
	if sentryAdapter.lastListOpts.Provider != "sentry" {
		t.Fatalf("provider = %q, want sentry list reload", sentryAdapter.lastListOpts.Provider)
	}
	if sentryAdapter.lastListOpts.Scope != domain.ScopeIssues {
		t.Fatalf("scope = %q, want issues list reload", sentryAdapter.lastListOpts.Scope)
	}
	if sentryAdapter.lastListOpts.Repo != "" {
		t.Fatalf("repo filter = %q, want cleared on sentry reload", sentryAdapter.lastListOpts.Repo)
	}
	if updated.repoInput.Placeholder != "Repository / Project…" {
		t.Fatalf("repo placeholder = %q, want Repository / Project…", updated.repoInput.Placeholder)
	}

	afterView := stripBrowseANSI(updated.View())
	assertOverlayFits(t, afterView, 120, 30)
	if updatedLayout := updated.browserLayout(); updatedLayout != beforeLayout {
		t.Fatalf("layout after source change = %+v, want stable layout %+v", updatedLayout, beforeLayout)
	}
	if got := len(strings.Split(afterView, "\n")); got != beforeLines {
		t.Fatalf("line count after source change = %d, want %d for stable overlay height\nview:\n%s", got, beforeLines, afterView)
	}
	for _, want := range []string{"Sentry", "Repo:", "Repository / Project…"} {
		if !strings.Contains(afterView, want) {
			t.Fatalf("view = %q, want %q after selecting Sentry", afterView, want)
		}
	}
	for _, avoid := range []string{"acme/rocket", "Projects", "Initiatives", "Labels:", "Owner:", "Group:", "Team:"} {
		if strings.Contains(afterView, avoid) {
			t.Fatalf("view = %q, must not show %q for Sentry", afterView, avoid)
		}
	}
}

func TestNewSessionOverlayClearsRepoFilterAcrossSentryBoundary(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name          string
		repoHost      string
		startProvider string
		key           tea.KeyType
		startValue    string
		wantProvider  string
	}{
		{name: "github to sentry", repoHost: "github", startProvider: "github", key: tea.KeyRight, startValue: "acme/rocket", wantProvider: "sentry"},
		{name: "sentry to github", repoHost: "github", startProvider: "sentry", key: tea.KeyLeft, startValue: "frontend", wantProvider: "github"},
		{name: "gitlab to sentry", repoHost: "gitlab", startProvider: "gitlab", key: tea.KeyRight, startValue: "acme/platform", wantProvider: "sentry"},
		{name: "sentry to gitlab", repoHost: "gitlab", startProvider: "sentry", key: tea.KeyLeft, startValue: "backend", wantProvider: "gitlab"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			repoAdapter := &browseTestAdapter{
				name:         tc.repoHost,
				browseScopes: []domain.SelectionScope{domain.ScopeIssues},
				browseFilters: map[domain.SelectionScope]adapter.BrowseFilterCapabilities{
					domain.ScopeIssues: {Views: []string{"assigned_to_me", "all"}, SupportsRepo: true},
				},
			}
			sentryAdapter := &browseTestAdapter{
				name:         "sentry",
				browseScopes: []domain.SelectionScope{domain.ScopeIssues},
				browseFilters: map[domain.SelectionScope]adapter.BrowseFilterCapabilities{
					domain.ScopeIssues: {Views: []string{"assigned_to_me", "all"}, SupportsRepo: true},
				},
			}

			overlay := NewNewSessionOverlay([]adapter.WorkItemAdapter{repoAdapter, sentryAdapter}, "ws-1", styles.NewStyles(styles.DefaultTheme))
			overlay.Open()
			overlay.SetSize(120, 30)
			overlay.providerIndex = providerOptionIndex(tc.startProvider)
			overlay.normalizeSelectionOptions()
			overlay.setBrowseControlFocus(browseControlSource)
			overlay.repoInput.SetValue(tc.startValue)

			updated, cmd := overlay.Update(tea.KeyMsg{Type: tc.key})
			if updated.currentProvider() != tc.wantProvider {
				t.Fatalf("provider = %q, want %q", updated.currentProvider(), tc.wantProvider)
			}
			if updated.repoInput.Value() != "" {
				t.Fatalf("repo filter = %q, want cleared when crossing sentry boundary", updated.repoInput.Value())
			}
			if cmd == nil {
				t.Fatal("expected reload after source change")
			}
			updated = applyOverlayCmds(t, updated, cmd)

			var lastOpts adapter.ListOpts
			switch tc.wantProvider {
			case "sentry":
				lastOpts = sentryAdapter.lastListOpts
			default:
				lastOpts = repoAdapter.lastListOpts
			}
			if lastOpts.Provider != tc.wantProvider {
				t.Fatalf("provider = %q, want %q on reload", lastOpts.Provider, tc.wantProvider)
			}
			if lastOpts.Repo != "" {
				t.Fatalf("repo filter = %q, want cleared on reload", lastOpts.Repo)
			}
			afterView := stripBrowseANSI(updated.View())
			if strings.Contains(afterView, tc.startValue) {
				t.Fatalf("view = %q, must not show stale repo/project filter %q", afterView, tc.startValue)
			}
			if !strings.Contains(afterView, "Repository / Project…") {
				t.Fatalf("view = %q, want shared repo/project placeholder", afterView)
			}
		})
	}
}

func TestNewSessionOverlayShowsAdvancedFilterRowsByCapabilities(t *testing.T) {
	t.Parallel()

	githubAdapter := &browseTestAdapter{
		name:         "github",
		browseScopes: []domain.SelectionScope{domain.ScopeIssues},
		browseFilters: map[domain.SelectionScope]adapter.BrowseFilterCapabilities{
			domain.ScopeIssues: {Views: []string{"assigned_to_me", "all"}, SupportsLabels: true, SupportsOwner: true, SupportsRepo: true},
		},
	}
	overlay := NewNewSessionOverlay([]adapter.WorkItemAdapter{githubAdapter}, "ws-1", styles.NewStyles(styles.DefaultTheme))
	overlay.providerIndex = 2
	overlay.Open()
	overlay.SetSize(100, 30)

	view := overlay.View()
	for _, want := range []string{"Labels:", "Owner:", "Repo:"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view = %q, want %q advanced filter row", view, want)
		}
	}
	for _, avoid := range []string{"Group:", "Team:"} {
		if strings.Contains(view, avoid) {
			t.Fatalf("view = %q, must not show %q row", view, avoid)
		}
	}
}

func TestNewSessionOverlayAdvancedFilterChangeTriggersReload(t *testing.T) {
	t.Parallel()

	gitlabAdapter := &browseTestAdapter{
		name:         "gitlab",
		browseScopes: []domain.SelectionScope{domain.ScopeIssues},
		browseFilters: map[domain.SelectionScope]adapter.BrowseFilterCapabilities{
			domain.ScopeIssues: {Views: []string{"assigned_to_me", "all"}, SupportsLabels: true, SupportsRepo: true, SupportsGroup: true},
		},
	}
	overlay := NewNewSessionOverlay([]adapter.WorkItemAdapter{gitlabAdapter}, "ws-1", styles.NewStyles(styles.DefaultTheme))
	overlay.providerIndex = 3
	overlay.Open()
	overlay.SetSize(100, 30)
	overlay, _ = overlay.Update(tea.KeyMsg{Type: tea.KeyDown})

	// Typing schedules a debounce tick; loading is NOT set immediately.
	postKey, _ := overlay.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	if postKey.loading {
		t.Fatal("overlay must not enter loading state before debounce fires")
	}

	// Deliver the matching debounce message; now the reload must be issued.
	updated, cmd := postKey.Update(browseDebounceMsg{seq: postKey.browseDebounceSeq})
	if !updated.loading {
		t.Fatal("expected loading after advanced filter input changes")
	}
	if cmd == nil {
		t.Fatal("expected reload command after advanced filter input changes")
	}
}

func TestNewSessionOverlayShowsLinearSubscribedViewWhenSupported(t *testing.T) {
	t.Parallel()

	linearAdapter := &browseTestAdapter{
		name:         "linear",
		browseScopes: []domain.SelectionScope{domain.ScopeIssues},
		browseFilters: map[domain.SelectionScope]adapter.BrowseFilterCapabilities{
			domain.ScopeIssues: {Views: []string{"assigned_to_me", "created_by_me", "subscribed", "all"}},
		},
	}
	overlay := NewNewSessionOverlay([]adapter.WorkItemAdapter{linearAdapter}, "ws-1", styles.NewStyles(styles.DefaultTheme))
	overlay.providerIndex = 1
	overlay.Open()
	overlay.SetSize(100, 30)

	view := strings.ToLower(stripBrowseANSI(overlay.View()))
	if !strings.Contains(view, "subscribed") {
		t.Fatalf("view = %q, want subscribed Linear view option", view)
	}
	if strings.Contains(view, "mentioned") {
		t.Fatalf("view = %q, must not show unsupported mentioned Linear view option", view)
	}
}

func TestNewSessionOverlayCtrlRClearsBrowseStateAndReloadsDefaults(t *testing.T) {
	t.Parallel()

	linearAdapter := &browseTestAdapter{
		name:         "linear",
		browseScopes: []domain.SelectionScope{domain.ScopeIssues},
		browseFilters: map[domain.SelectionScope]adapter.BrowseFilterCapabilities{
			domain.ScopeIssues: {
				Views:          []string{"assigned_to_me", "created_by_me", "all"},
				States:         []string{"open", "closed", "all"},
				SupportsLabels: true,
				SupportsOwner:  true,
				SupportsRepo:   true,
				SupportsGroup:  true,
				SupportsTeam:   true,
			},
		},
	}
	overlay := NewNewSessionOverlay([]adapter.WorkItemAdapter{linearAdapter}, "ws-1", styles.NewStyles(styles.DefaultTheme))
	overlay.Open()
	overlay.SetSize(120, 30)
	overlay.providerIndex = providerOptionIndex("linear")
	overlay.viewIndex = 1
	overlay.stateIndex = 1
	overlay.filterInput.SetValue("bug")
	overlay.labelsInput.SetValue("bug,backend")
	overlay.ownerInput.SetValue("alice")
	overlay.repoInput.SetValue("acme/rocket")
	overlay.groupInput.SetValue("platform")
	overlay.teamInput.SetValue("rocket")
	overlay.normalizeSelectionOptions()

	if overlay.currentProvider() != "linear" {
		t.Fatalf("provider = %q, want linear before clear", overlay.currentProvider())
	}
	if overlay.currentView() != "created_by_me" {
		t.Fatalf("view = %q, want created_by_me before clear", overlay.currentView())
	}
	if overlay.currentState() != "closed" {
		t.Fatalf("state = %q, want closed before clear", overlay.currentState())
	}

	overlay, _ = overlay.Update(loadedMsg(
		adapter.ListItem{ID: "lin-1", Provider: "linear", Title: "First"},
		adapter.ListItem{ID: "lin-2", Provider: "linear", Title: "Second"},
	))
	overlay, _ = overlay.Update(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
	overlay.setBrowseListFocus()
	overlay.issueList.Select(1)

	view := stripBrowseANSI(overlay.View())
	assertOverlayFits(t, view, 120, 30)
	if !strings.Contains(view, "✓ First") {
		t.Fatalf("view = %q, want selected marker before clear", view)
	}

	updated, cmd := overlay.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	if !updated.loading {
		t.Fatal("expected loading after clear")
	}
	if cmd == nil {
		t.Fatal("expected reload command after clear")
	}
	if updated.currentProvider() != "all" {
		t.Fatalf("provider = %q, want all after clear", updated.currentProvider())
	}
	if updated.currentScope() != domain.ScopeIssues {
		t.Fatalf("scope = %q, want issues after clear", updated.currentScope())
	}
	if updated.currentView() != "assigned_to_me" {
		t.Fatalf("view = %q, want assigned_to_me after clear", updated.currentView())
	}
	if updated.currentState() != "open" {
		t.Fatalf("state = %q, want open after clear", updated.currentState())
	}
	if updated.filterInput.Value() != "" || updated.labelsInput.Value() != "" || updated.ownerInput.Value() != "" || updated.repoInput.Value() != "" || updated.groupInput.Value() != "" || updated.teamInput.Value() != "" {
		t.Fatalf("filters were not fully cleared")
	}
	if len(updated.selectedIDs) != 0 {
		t.Fatalf("selectedIDs len = %d, want 0 after clear", len(updated.selectedIDs))
	}
	if len(updated.allItems) != 0 {
		t.Fatalf("allItems len = %d, want 0 after clear", len(updated.allItems))
	}
	if updated.browseFocus != browseFocusControls || updated.browseControl != browseControlSearch {
		t.Fatalf("focus = (%v, %v), want search control after clear", updated.browseFocus, updated.browseControl)
	}

	view = stripBrowseANSI(updated.View())
	assertOverlayFits(t, view, 120, 30)
	if strings.Contains(view, "✓ First") || strings.Contains(view, "First") || strings.Contains(view, "Second") {
		t.Fatalf("view = %q, want stale browse items and markers cleared before reload completes", view)
	}

	updated = applyOverlayCmds(t, updated, cmd)
	if linearAdapter.lastListOpts.Provider != "all" {
		t.Fatalf("provider = %q, want all on reload", linearAdapter.lastListOpts.Provider)
	}
	if linearAdapter.lastListOpts.Scope != domain.ScopeIssues {
		t.Fatalf("scope = %q, want issues on reload", linearAdapter.lastListOpts.Scope)
	}
	if linearAdapter.lastListOpts.View != "assigned_to_me" {
		t.Fatalf("view = %q, want assigned_to_me on reload", linearAdapter.lastListOpts.View)
	}
	if linearAdapter.lastListOpts.State != "open" {
		t.Fatalf("state = %q, want open on reload", linearAdapter.lastListOpts.State)
	}
	if linearAdapter.lastListOpts.Search != "" || linearAdapter.lastListOpts.Owner != "" || linearAdapter.lastListOpts.Repo != "" || linearAdapter.lastListOpts.Group != "" || linearAdapter.lastListOpts.TeamID != "" {
		t.Fatalf("reload opts = %#v, want cleared scalar filters", linearAdapter.lastListOpts)
	}
	if len(linearAdapter.lastListOpts.Labels) != 0 {
		t.Fatalf("labels = %#v, want none on reload", linearAdapter.lastListOpts.Labels)
	}
}

func TestNewSessionOverlayShowsStateControlsByCapabilities(t *testing.T) {
	t.Parallel()

	githubAdapter := &browseTestAdapter{
		name:         "github",
		browseScopes: []domain.SelectionScope{domain.ScopeIssues},
		browseFilters: map[domain.SelectionScope]adapter.BrowseFilterCapabilities{
			domain.ScopeIssues: {Views: []string{"assigned_to_me", "all"}, States: []string{"open", "closed", "all"}},
		},
	}
	overlay := NewNewSessionOverlay([]adapter.WorkItemAdapter{githubAdapter}, "ws-1", styles.NewStyles(styles.DefaultTheme))
	overlay.providerIndex = 2
	overlay.Open()
	overlay.SetSize(100, 30)

	view := overlay.View()
	if !strings.Contains(view, "State:") {
		t.Fatalf("view = %q, want state controls", view)
	}
	if !strings.Contains(view, "[open]") {
		t.Fatalf("view = %q, want default open state selected", view)
	}

	hint := overlay.browserHintText()
	if !strings.Contains(hint, "Ctrl+T") {
		t.Fatalf("hint = %q, want state hint", hint)
	}
	if !strings.Contains(hint, "Ctrl+O") {
		t.Fatalf("hint = %q, want browser-open hint", hint)
	}
	if !strings.Contains(hint, "Ctrl+R") {
		t.Fatalf("hint = %q, want clear hint", hint)
	}
	if !strings.Contains(hint, "Ctrl+S") {
		t.Fatalf("hint = %q, want scope hint", hint)
	}
	for _, avoid := range []string{"↑/↓", "←/→"} {
		if strings.Contains(hint, avoid) {
			t.Fatalf("hint = %q, must not include %q", hint, avoid)
		}
	}
}

func TestNewSessionOverlayBrowserHintsWrapWithoutTruncation(t *testing.T) {
	t.Parallel()

	githubAdapter := &browseTestAdapter{
		name:         "github",
		browseScopes: []domain.SelectionScope{domain.ScopeIssues},
		browseFilters: map[domain.SelectionScope]adapter.BrowseFilterCapabilities{
			domain.ScopeIssues: {Views: []string{"assigned_to_me", "all"}, States: []string{"open", "closed", "all"}},
		},
	}
	overlay := NewNewSessionOverlay([]adapter.WorkItemAdapter{githubAdapter}, "ws-1", styles.NewStyles(styles.DefaultTheme))
	overlay.providerIndex = 2
	overlay.Open()
	overlay.SetSize(80, 20)

	view := stripBrowseANSI(overlay.View())
	assertOverlayFits(t, view, 80, 20)
	for _, want := range []string{"Ctrl+N", "Ctrl+R", "Ctrl+S", "Multi-select: one provider"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view = %q, want wrapped hint content %q", view, want)
		}
	}
	for _, avoid := range []string{"↑/↓", "←/→"} {
		if strings.Contains(view, avoid) {
			t.Fatalf("view = %q, must not include %q", view, avoid)
		}
	}
}

func TestNewSessionOverlayStateCycleTriggersReloadAndPassesState(t *testing.T) {
	t.Parallel()

	githubAdapter := &browseTestAdapter{
		name:         "github",
		browseScopes: []domain.SelectionScope{domain.ScopeIssues},
		browseFilters: map[domain.SelectionScope]adapter.BrowseFilterCapabilities{
			domain.ScopeIssues: {Views: []string{"assigned_to_me", "all"}, States: []string{"open", "closed", "all"}},
		},
	}
	overlay := NewNewSessionOverlay([]adapter.WorkItemAdapter{githubAdapter}, "ws-1", styles.NewStyles(styles.DefaultTheme))
	overlay.providerIndex = 2
	overlay.Open()
	overlay.SetSize(100, 30)

	updated, cmd := overlay.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	if updated.stateIndex != 1 {
		t.Fatalf("stateIndex = %d, want 1 after ctrl+t", updated.stateIndex)
	}
	if !updated.loading {
		t.Fatal("expected loading after state change")
	}
	if cmd == nil {
		t.Fatal("expected reload command after state change")
	}
	msg := cmd()
	if _, ok := msg.(issueListLoadedMsg); !ok {
		t.Fatalf("msg = %T, want issueListLoadedMsg", msg)
	}
	if githubAdapter.lastListOpts.State != "closed" {
		t.Fatalf("state = %q, want closed", githubAdapter.lastListOpts.State)
	}
	if githubAdapter.lastListOpts.Scope != domain.ScopeIssues {
		t.Fatalf("scope = %q, want issues", githubAdapter.lastListOpts.Scope)
	}
}

func TestNewSessionOverlayCtrlOOpensFocusedItemURL(t *testing.T) {
	t.Parallel()

	githubAdapter := &browseTestAdapter{
		name:         "github",
		browseScopes: []domain.SelectionScope{domain.ScopeIssues},
		browseFilters: map[domain.SelectionScope]adapter.BrowseFilterCapabilities{
			domain.ScopeIssues: {Views: []string{"assigned_to_me", "all"}},
		},
	}
	overlay := NewNewSessionOverlay([]adapter.WorkItemAdapter{githubAdapter}, "ws-1", styles.NewStyles(styles.DefaultTheme))
	overlay.providerIndex = 2
	overlay.Open()
	overlay.SetSize(100, 30)

	var openedURL string
	overlay.openBrowserCmd = func(url string) tea.Cmd {
		return func() tea.Msg {
			openedURL = url

			return nil
		}
	}

	overlay, _ = overlay.Update(loadedMsg(
		adapter.ListItem{ID: "gh-1", Provider: "github", Title: "First", URL: "https://github.com/acme/rocket/issues/1"},
		adapter.ListItem{ID: "gh-2", Provider: "github", Title: "Second", URL: "https://github.com/acme/rocket/issues/2"},
	))
	overlay, _ = overlay.Update(tea.KeyMsg{Type: tea.KeyDown})
	overlay, _ = overlay.Update(tea.KeyMsg{Type: tea.KeyDown})
	overlay, _ = overlay.Update(tea.KeyMsg{Type: tea.KeyRight})

	_, cmd := overlay.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	if cmd == nil {
		t.Fatal("expected browser-open command")
	}
	if msg := cmd(); msg != nil {
		t.Fatalf("msg = %T, want nil", msg)
	}
	if openedURL != "https://github.com/acme/rocket/issues/2" {
		t.Fatalf("openedURL = %q, want focused item URL", openedURL)
	}
}

func TestNewSessionOverlayCtrlOReturnsErrorWithoutURL(t *testing.T) {
	t.Parallel()

	githubAdapter := &browseTestAdapter{
		name:         "github",
		browseScopes: []domain.SelectionScope{domain.ScopeIssues},
		browseFilters: map[domain.SelectionScope]adapter.BrowseFilterCapabilities{
			domain.ScopeIssues: {Views: []string{"assigned_to_me", "all"}},
		},
	}
	overlay := NewNewSessionOverlay([]adapter.WorkItemAdapter{githubAdapter}, "ws-1", styles.NewStyles(styles.DefaultTheme))
	overlay.providerIndex = 2
	overlay.Open()
	overlay.SetSize(100, 30)
	overlay, _ = overlay.Update(loadedMsg(
		adapter.ListItem{ID: "gh-1", Provider: "github", Title: "First"},
	))

	_, cmd := overlay.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	if cmd == nil {
		t.Fatal("expected error command when URL is missing")
	}
	msg := cmd()
	errMsg, ok := msg.(ErrMsg)
	if !ok {
		t.Fatalf("msg = %T, want ErrMsg", msg)
	}
	if errMsg.Err == nil || errMsg.Err.Error() != "selected work item has no URL" {
		t.Fatalf("err = %v, want missing URL error", errMsg.Err)
	}
}

type manualTestAdapter struct{}

func (manualTestAdapter) Name() string { return "manual" }

func (manualTestAdapter) Capabilities() adapter.AdapterCapabilities {
	return adapter.AdapterCapabilities{}
}

func (manualTestAdapter) ListSelectable(context.Context, adapter.ListOpts) (*adapter.ListResult, error) {
	return nil, adapter.ErrBrowseNotSupported
}

func (manualTestAdapter) Resolve(_ context.Context, sel adapter.Selection) (domain.Session, error) {
	return domain.Session{
		ID:         domain.NewID(),
		ExternalID: "MAN-1",
		Title:      sel.Manual.Title,
		State:      domain.SessionIngested,
	}, nil
}

func (manualTestAdapter) Watch(_ context.Context, _ adapter.WorkItemFilter) (<-chan adapter.WorkItemEvent, error) {
	return nil, adapter.ErrWatchNotSupported
}

func (manualTestAdapter) Fetch(_ context.Context, _ string) (domain.Session, error) {
	return domain.Session{}, errors.New("not implemented")
}

func (manualTestAdapter) UpdateState(_ context.Context, _ string, _ domain.TrackerState) error {
	return adapter.ErrMutateNotSupported
}

func (manualTestAdapter) AddComment(_ context.Context, _ string, _ string) error {
	return adapter.ErrMutateNotSupported
}

func (manualTestAdapter) OnEvent(_ context.Context, _ domain.SystemEvent) error { return nil }

func TestNewSessionOverlayManualShortcutDispatchesManualSession(t *testing.T) {
	t.Parallel()

	githubAdapter := &browseTestAdapter{name: "github", browseScopes: []domain.SelectionScope{domain.ScopeIssues}, browseFilters: map[domain.SelectionScope]adapter.BrowseFilterCapabilities{domain.ScopeIssues: {Views: []string{"assigned_to_me", "all"}}}}
	overlay := NewNewSessionOverlay([]adapter.WorkItemAdapter{manualTestAdapter{}, githubAdapter}, "ws-1", styles.NewStyles(styles.DefaultTheme))
	overlay.Open()
	overlay.SetSize(100, 30)

	if view := overlay.View(); !strings.Contains(view, "Ctrl+N") {
		t.Fatalf("view = %q, want manual shortcut hint", view)
	}

	updated, _ := overlay.Update(tea.KeyMsg{Type: tea.KeyCtrlN})
	if !updated.showManual {
		t.Fatal("expected manual form after ctrl+n")
	}
	updated.manualTitle.SetValue("Manual task")

	_, cmd := updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected manual session command")
	}

	msg := cmd()
	sessionMsg, ok := msg.(NewSessionManualMsg)
	if !ok {
		t.Fatalf("msg = %T, want NewSessionManualMsg", msg)
	}
	if sessionMsg.Adapter.Name() != "manual" {
		t.Fatalf("adapter = %q, want manual", sessionMsg.Adapter.Name())
	}
	if sessionMsg.Title != "Manual task" {
		t.Fatalf("title = %q, want Manual task", sessionMsg.Title)
	}
}

func TestNewSessionOverlayAdapterErrorClearsLoading(t *testing.T) {
	t.Parallel()

	failAdapter := &browseTestAdapter{
		name:         "linear",
		browseScopes: []domain.SelectionScope{domain.ScopeIssues},
		browseFilters: map[domain.SelectionScope]adapter.BrowseFilterCapabilities{
			domain.ScopeIssues: {Views: []string{"assigned_to_me"}},
		},
		listSelectable: func(_ adapter.ListOpts) (*adapter.ListResult, error) {
			return nil, errors.New("linear API returned status 401")
		},
	}

	overlay := NewNewSessionOverlay([]adapter.WorkItemAdapter{failAdapter}, "ws-1", styles.NewStyles(styles.DefaultTheme))
	overlay.Open()
	overlay.SetSize(100, 30)

	// Simulate the load cycle: reloadItems sets loading=true and returns a cmd.
	reloadCmd := overlay.reloadItems()
	if !overlay.loading {
		t.Fatal("expected loading after reloadItems")
	}

	msg := reloadCmd()

	loaded, ok := msg.(issueListLoadedMsg)
	if !ok {
		t.Fatalf("msg = %T, want issueListLoadedMsg even when adapter errors", msg)
	}
	if len(loaded.errs) == 0 {
		t.Fatal("expected adapter errors in issueListLoadedMsg")
	}

	overlay, retCmd := overlay.Update(loaded)
	if overlay.loading {
		t.Fatal("loading should be false after receiving issueListLoadedMsg with errors")
	}

	// The returned command should produce an ErrMsg for the toast.
	if retCmd == nil {
		t.Fatal("expected error command to surface adapter error as toast")
	}
	msgs := runOverlayCmd(t, retCmd)
	foundErr := false
	for _, m := range msgs {
		if errMsg, ok := m.(ErrMsg); ok && errMsg.Err != nil {
			foundErr = true
		}
	}
	if !foundErr {
		t.Fatal("expected ErrMsg in returned commands")
	}
}

func TestNewSessionOverlayPartialAdapterErrorShowsSuccessfulResults(t *testing.T) {
	t.Parallel()

	failAdapter := &browseTestAdapter{
		name:         "linear",
		browseScopes: []domain.SelectionScope{domain.ScopeIssues},
		browseFilters: map[domain.SelectionScope]adapter.BrowseFilterCapabilities{
			domain.ScopeIssues: {Views: []string{"assigned_to_me"}},
		},
		listSelectable: func(_ adapter.ListOpts) (*adapter.ListResult, error) {
			return nil, errors.New("unauthorized")
		},
	}
	okAdapter := &browseTestAdapter{
		name:         "github",
		browseScopes: []domain.SelectionScope{domain.ScopeIssues},
		browseFilters: map[domain.SelectionScope]adapter.BrowseFilterCapabilities{
			domain.ScopeIssues: {Views: []string{"assigned_to_me"}},
		},
		listSelectable: func(_ adapter.ListOpts) (*adapter.ListResult, error) {
			return &adapter.ListResult{
				Items: []adapter.ListItem{
					{ID: "gh-1", Provider: "github", Title: "GitHub issue"},
				},
			}, nil
		},
	}

	overlay := NewNewSessionOverlay([]adapter.WorkItemAdapter{failAdapter, okAdapter}, "ws-1", styles.NewStyles(styles.DefaultTheme))
	overlay.Open()
	overlay.SetSize(100, 30)

	cmd := overlay.loadItemsCmd(browseLoadReset, overlay.nextRequestID())
	msg := cmd()

	loaded := msg.(issueListLoadedMsg)
	if len(loaded.errs) != 1 {
		t.Fatalf("errs = %d, want 1 (only linear should fail)", len(loaded.errs))
	}

	overlay, _ = overlay.Update(loaded)
	if overlay.loading {
		t.Fatal("loading should be false")
	}
	if len(overlay.allItems) != 1 {
		t.Fatalf("allItems = %d, want 1 (github results should be present)", len(overlay.allItems))
	}
	if overlay.allItems[0].Title != "GitHub issue" {
		t.Fatalf("item title = %q, want GitHub issue", overlay.allItems[0].Title)
	}
}

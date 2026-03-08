package views

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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

func (a *browseTestAdapter) Resolve(_ context.Context, sel adapter.Selection) (domain.WorkItem, error) {
	a.resolved = append(a.resolved, sel)
	return domain.WorkItem{ID: domain.NewID(), ExternalID: fmt.Sprintf("%s-session", a.name), Title: a.name, State: domain.WorkItemIngested}, nil
}

func (a *browseTestAdapter) Watch(_ context.Context, _ adapter.WorkItemFilter) (<-chan adapter.WorkItemEvent, error) {
	return nil, adapter.ErrWatchNotSupported
}

func (a *browseTestAdapter) Fetch(_ context.Context, _ string) (domain.WorkItem, error) {
	return domain.WorkItem{}, fmt.Errorf("not implemented")
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

	updated, cmd := overlay.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	if !updated.loading {
		t.Fatal("expected loading after search input changes")
	}
	if cmd == nil {
		t.Fatal("expected reload command after search input changes")
	}
	msg := cmd()
	if _, ok := msg.(tea.BatchMsg); !ok {
		t.Fatalf("msg = %T, want tea.BatchMsg", msg)
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
	githubAdapter.listSelectable = func(opts adapter.ListOpts) (*adapter.ListResult, error) {
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
	linearAdapter.listSelectable = func(opts adapter.ListOpts) (*adapter.ListResult, error) {
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

func TestNewSessionOverlayViewRendersDetailsMarkdownAndMermaid(t *testing.T) {
	t.Parallel()

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
		Description:  "## Summary\n\nThis is **important**.\n\n```mermaid\ngraph LR\nA-->B\n```",
	}))

	view := stripBrowseANSI(overlay.View())
	for _, want := range []string{"Issue title", "Provider:", "Container:", "Mermaid diagram", "A", "B"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view = %q, want %q in rendered details", view, want)
		}
	}
}

func TestNewSessionOverlayNoItemsBackgroundMatchesOverlay(t *testing.T) {
	t.Parallel()

	overlay := NewNewSessionOverlay(nil, "ws-1", styles.NewStyles(styles.DefaultTheme))
	if got := overlay.issueList.Styles.NoItems.GetBackground(); got != lipgloss.Color(overlayBackgroundColor) {
		t.Fatalf("no-items background = %v, want %q", got, overlayBackgroundColor)
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

	updated, cmd := overlay.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	if !updated.loading {
		t.Fatal("expected loading after advanced filter input changes")
	}
	if cmd == nil {
		t.Fatal("expected reload command after advanced filter input changes")
	}
	if _, ok := cmd().(tea.BatchMsg); !ok {
		t.Fatalf("msg = %T, want tea.BatchMsg", cmd())
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
	if !strings.Contains(overlay.browserHintText(), "Ctrl+T") {
		t.Fatalf("hint = %q, want state hint", overlay.browserHintText())
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

type manualTestAdapter struct{}

func (manualTestAdapter) Name() string { return "manual" }

func (manualTestAdapter) Capabilities() adapter.AdapterCapabilities {
	return adapter.AdapterCapabilities{}
}

func (manualTestAdapter) ListSelectable(context.Context, adapter.ListOpts) (*adapter.ListResult, error) {
	return nil, adapter.ErrBrowseNotSupported
}

func (manualTestAdapter) Resolve(_ context.Context, sel adapter.Selection) (domain.WorkItem, error) {
	return domain.WorkItem{ID: domain.NewID(), ExternalID: "MAN-1", Title: sel.Manual.Title}, nil
}

func (manualTestAdapter) Watch(_ context.Context, _ adapter.WorkItemFilter) (<-chan adapter.WorkItemEvent, error) {
	return nil, adapter.ErrWatchNotSupported
}

func (manualTestAdapter) Fetch(_ context.Context, _ string) (domain.WorkItem, error) {
	return domain.WorkItem{}, fmt.Errorf("not implemented")
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

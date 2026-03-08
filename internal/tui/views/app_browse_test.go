package views

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/tui/styles"
)

type browseTestAdapter struct {
	name          string
	browseScopes  []domain.SelectionScope
	browseFilters map[domain.SelectionScope]adapter.BrowseFilterCapabilities
	lastListOpts  adapter.ListOpts
	resolved      []adapter.Selection
}

func (a *browseTestAdapter) Name() string { return a.name }

func (a *browseTestAdapter) Capabilities() adapter.AdapterCapabilities {
	return adapter.AdapterCapabilities{CanBrowse: true, BrowseScopes: a.browseScopes, BrowseFilters: a.browseFilters}
}

func (a *browseTestAdapter) ListSelectable(_ context.Context, opts adapter.ListOpts) (*adapter.ListResult, error) {
	a.lastListOpts = opts
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

func TestNewSessionOverlayRejectsMixedProviderSelection(t *testing.T) {
	t.Parallel()

	githubAdapter := &browseTestAdapter{name: "github", browseScopes: []domain.SelectionScope{domain.ScopeIssues}, browseFilters: map[domain.SelectionScope]adapter.BrowseFilterCapabilities{domain.ScopeIssues: {Views: []string{"assigned_to_me", "all"}}}}
	gitlabAdapter := &browseTestAdapter{name: "gitlab", browseScopes: []domain.SelectionScope{domain.ScopeIssues}, browseFilters: map[domain.SelectionScope]adapter.BrowseFilterCapabilities{domain.ScopeIssues: {Views: []string{"assigned_to_me", "all"}}}}
	overlay := NewNewSessionOverlay([]adapter.WorkItemAdapter{githubAdapter, gitlabAdapter}, "ws-1", styles.NewStyles(styles.DefaultTheme))
	overlay.Open()
	overlay.SetSize(100, 30)
	overlay, _ = overlay.Update(issueListLoadedMsg{items: []adapter.ListItem{
		{ID: "gh-1", Provider: "github", Title: "GitHub issue"},
		{ID: "gl-1", Provider: "gitlab", Title: "GitLab issue"},
	}})
	overlay, _ = overlay.Update(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
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
	if view := updated.View(); !strings.Contains(view, "Multi-select: same provider only") {
		t.Fatalf("view = %q, want same-provider hint", view)
	}
}

func TestNewSessionOverlayDispatchesSelectedProvider(t *testing.T) {
	t.Parallel()

	githubAdapter := &browseTestAdapter{name: "github", browseScopes: []domain.SelectionScope{domain.ScopeIssues}, browseFilters: map[domain.SelectionScope]adapter.BrowseFilterCapabilities{domain.ScopeIssues: {Views: []string{"assigned_to_me", "all"}}}}
	gitlabAdapter := &browseTestAdapter{name: "gitlab", browseScopes: []domain.SelectionScope{domain.ScopeIssues}, browseFilters: map[domain.SelectionScope]adapter.BrowseFilterCapabilities{domain.ScopeIssues: {Views: []string{"assigned_to_me", "all"}}}}
	overlay := NewNewSessionOverlay([]adapter.WorkItemAdapter{githubAdapter, gitlabAdapter}, "ws-1", styles.NewStyles(styles.DefaultTheme))
	overlay.Open()
	overlay.SetSize(100, 30)
	overlay, _ = overlay.Update(issueListLoadedMsg{items: []adapter.ListItem{{ID: "gl-1", Provider: "gitlab", Title: "GitLab issue"}}})
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

func TestNewSessionOverlayPaginationControlsReload(t *testing.T) {
	t.Parallel()

	githubAdapter := &browseTestAdapter{name: "github", browseScopes: []domain.SelectionScope{domain.ScopeIssues}, browseFilters: map[domain.SelectionScope]adapter.BrowseFilterCapabilities{domain.ScopeIssues: {Views: []string{"assigned_to_me", "all"}}}}
	overlay := NewNewSessionOverlay([]adapter.WorkItemAdapter{githubAdapter}, "ws-1", styles.NewStyles(styles.DefaultTheme))
	overlay.Open()
	overlay.SetSize(100, 30)
	overlay, _ = overlay.Update(issueListLoadedMsg{hasMore: true})

	next, cmd := overlay.Update(tea.KeyMsg{Type: tea.KeyCtrlN})
	if next.offset != 50 {
		t.Fatalf("offset = %d, want 50 after ctrl+n", next.offset)
	}
	if !next.loading {
		t.Fatal("expected loading after ctrl+n")
	}
	if cmd == nil {
		t.Fatal("expected reload command after ctrl+n")
	}

	prev, cmd := next.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
	if prev.offset != 0 {
		t.Fatalf("offset = %d, want 0 after ctrl+p", prev.offset)
	}
	if !prev.loading {
		t.Fatal("expected loading after ctrl+p")
	}
	if cmd == nil {
		t.Fatal("expected reload command after ctrl+p")
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
	if !strings.Contains(view, "Ctrl+T") {
		t.Fatalf("view = %q, want state hint", view)
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

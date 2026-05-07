package views

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/tui/styles"
)

// TestNewSessionOverlay_ScrollLoadMore_TriggersAtViewportBottom verifies that
// when the cursor reaches the bottom of the visible viewport, a load is triggered.
func TestNewSessionOverlay_ScrollLoadMore_TriggersAtViewportBottom(t *testing.T) {
	t.Parallel()

	githubAdapter := &browseTestAdapter{
		name:         "github",
		browseScopes: []domain.SelectionScope{domain.ScopeIssues},
		browseFilters: map[domain.SelectionScope]adapter.BrowseFilterCapabilities{
			domain.ScopeIssues: {SupportsOffset: true},
		},
	}
	githubAdapter.listSelectable = func(_ adapter.ListOpts) (*adapter.ListResult, error) {
		return &adapter.ListResult{Items: []adapter.ListItem{{ID: "gh-1", Provider: "github", Title: "First"}}, HasMore: true}, nil
	}
	overlay := NewNewSessionOverlay([]adapter.WorkItemAdapter{githubAdapter}, "ws-1", styles.NewStyles(styles.DefaultTheme))
	overlay.Open()
	overlay.SetSize(120, 30)
	// Render to finalize layout calculations
	_ = overlay.View()
	overlay = applyOverlayCmds(t, overlay, overlay.reloadItems())
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
}

// TestNewSessionOverlay_ScrollLoadMore_DoesNotTriggerMidViewport verifies that
// when there are more items below the viewport, no load is triggered.
func TestNewSessionOverlay_ScrollLoadMore_DoesNotTriggerMidViewport(t *testing.T) {
	t.Parallel()

	githubAdapter := &browseTestAdapter{
		name:         "github",
		browseScopes: []domain.SelectionScope{domain.ScopeIssues},
		browseFilters: map[domain.SelectionScope]adapter.BrowseFilterCapabilities{
			domain.ScopeIssues: {SupportsOffset: true},
		},
	}
	// Return enough items to fill the viewport multiple times
	githubAdapter.listSelectable = func(_ adapter.ListOpts) (*adapter.ListResult, error) {
		items := make([]adapter.ListItem, 30)
		for i := range items {
			items[i] = adapter.ListItem{ID: "gh-" + string(rune('a'+i)), Provider: "github", Title: "Item"}
		}
		return &adapter.ListResult{Items: items, HasMore: true}, nil
	}
	overlay := NewNewSessionOverlay([]adapter.WorkItemAdapter{githubAdapter}, "ws-1", styles.NewStyles(styles.DefaultTheme))
	overlay.Open()
	overlay.SetSize(120, 30)
	_ = overlay.View()
	overlay = applyOverlayCmds(t, overlay, overlay.reloadItems())
	if len(overlay.allItems) != 30 || !overlay.hasMore {
		t.Fatalf("expected 30 items with more pages")
	}

	overlay.setBrowseListFocus()
	// Move to middle of list (should not trigger load)
	updated, cmd := overlay.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	if cmd != nil {
		t.Error("load more should not trigger when there are items below the viewport")
	}
	if updated.loading {
		t.Error("loading should not be set when viewport is not exhausted")
	}
}

// TestNewSessionOverlay_ScrollLoadMore_RespectsLoadingGuard verifies that
// while loading is in progress, no additional load is triggered.
func TestNewSessionOverlay_ScrollLoadMore_RespectsLoadingGuard(t *testing.T) {
	t.Parallel()

	githubAdapter := &browseTestAdapter{
		name:         "github",
		browseScopes: []domain.SelectionScope{domain.ScopeIssues},
		browseFilters: map[domain.SelectionScope]adapter.BrowseFilterCapabilities{
			domain.ScopeIssues: {SupportsOffset: true},
		},
	}
	githubAdapter.listSelectable = func(_ adapter.ListOpts) (*adapter.ListResult, error) {
		return &adapter.ListResult{Items: []adapter.ListItem{{ID: "gh-1", Provider: "github", Title: "First"}}, HasMore: true}, nil
	}
	overlay := NewNewSessionOverlay([]adapter.WorkItemAdapter{githubAdapter}, "ws-1", styles.NewStyles(styles.DefaultTheme))
	overlay.Open()
	overlay.SetSize(120, 30)
	_ = overlay.View()
	overlay = applyOverlayCmds(t, overlay, overlay.reloadItems())
	overlay.setBrowseListFocus()

	// Trigger first load
	overlay, _ = overlay.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	if !overlay.loading {
		t.Fatal("expected loading to be true after PgDown")
	}

	// Manually keep loading=true and try to trigger again
	overlay.loading = true
	_, cmd := overlay.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	if cmd != nil {
		t.Error("load more should not trigger while loading is in progress")
	}
}

// TestNewSessionOverlay_ScrollLoadMore_DoesNotTriggerWhenNoMoreItems verifies
// that pagination does not trigger when hasMore is false.
func TestNewSessionOverlay_ScrollLoadMore_DoesNotTriggerWhenNoMoreItems(t *testing.T) {
	t.Parallel()

	githubAdapter := &browseTestAdapter{
		name:         "github",
		browseScopes: []domain.SelectionScope{domain.ScopeIssues},
		browseFilters: map[domain.SelectionScope]adapter.BrowseFilterCapabilities{
			domain.ScopeIssues: {SupportsOffset: true},
		},
	}
	githubAdapter.listSelectable = func(_ adapter.ListOpts) (*adapter.ListResult, error) {
		return &adapter.ListResult{Items: []adapter.ListItem{{ID: "gh-1", Provider: "github", Title: "First"}}}, nil
	}
	overlay := NewNewSessionOverlay([]adapter.WorkItemAdapter{githubAdapter}, "ws-1", styles.NewStyles(styles.DefaultTheme))
	overlay.Open()
	overlay.SetSize(120, 30)
	_ = overlay.View()
	overlay = applyOverlayCmds(t, overlay, overlay.reloadItems())
	if overlay.hasMore {
		t.Fatal("expected hasMore=false for last page")
	}
	overlay.setBrowseListFocus()

	// Page-down should not trigger load
	updated, cmd := overlay.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	if cmd != nil {
		t.Error("load more should not trigger when hasMore=false")
	}
	if updated.loading {
		t.Error("loading should not be set when hasMore=false")
	}
}

// TestNewSessionOverlay_ScrollLoadMore_DoesNotTriggerWhenControlsFocused verifies
// that pagination does not trigger when browse focus is on controls.
func TestNewSessionOverlay_ScrollLoadMore_DoesNotTriggerWhenControlsFocused(t *testing.T) {
	t.Parallel()

	githubAdapter := &browseTestAdapter{
		name:         "github",
		browseScopes: []domain.SelectionScope{domain.ScopeIssues},
		browseFilters: map[domain.SelectionScope]adapter.BrowseFilterCapabilities{
			domain.ScopeIssues: {SupportsOffset: true},
		},
	}
	githubAdapter.listSelectable = func(_ adapter.ListOpts) (*adapter.ListResult, error) {
		return &adapter.ListResult{Items: []adapter.ListItem{{ID: "gh-1", Provider: "github", Title: "First"}}, HasMore: true}, nil
	}
	overlay := NewNewSessionOverlay([]adapter.WorkItemAdapter{githubAdapter}, "ws-1", styles.NewStyles(styles.DefaultTheme))
	overlay.Open()
	overlay.SetSize(120, 30)
	_ = overlay.View()
	overlay = applyOverlayCmds(t, overlay, overlay.reloadItems())
	// Focus is on controls (default), not the list
	overlay.setBrowseControlFocus(browseControlSearch)

	// Page-down goes to controls, not list
	_, cmd := overlay.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	if cmd != nil {
		t.Error("load more should not trigger when list is not focused")
	}
}

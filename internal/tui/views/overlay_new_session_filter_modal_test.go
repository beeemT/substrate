package views

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/tui/styles"
)

func newFilterModalTestOverlay(t *testing.T) NewSessionOverlay {
	t.Helper()

	githubAdapter := &browseTestAdapter{
		name:         "github",
		browseScopes: []domain.SelectionScope{domain.ScopeIssues},
		browseFilters: map[domain.SelectionScope]adapter.BrowseFilterCapabilities{
			domain.ScopeIssues: {Views: []string{"assigned_to_me", "all"}},
		},
	}
	gitlabAdapter := &browseTestAdapter{
		name:         "gitlab",
		browseScopes: []domain.SelectionScope{domain.ScopeIssues},
		browseFilters: map[domain.SelectionScope]adapter.BrowseFilterCapabilities{
			domain.ScopeIssues: {Views: []string{"assigned_to_me", "all"}},
		},
	}
	overlay := NewNewSessionOverlay([]adapter.WorkItemAdapter{githubAdapter, gitlabAdapter}, "ws-1", styles.NewStyles(styles.DefaultTheme))
	overlay.Open()
	overlay.SetSize(100, 30)
	return overlay
}

func typeText(overlay NewSessionOverlay, value string) NewSessionOverlay {
	for _, r := range value {
		overlay, _ = overlay.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return overlay
}

func TestNewSessionOverlayCtrlFOpenSavePromptAndEnterSendsSaveMsg(t *testing.T) {
	t.Parallel()

	overlay := newFilterModalTestOverlay(t)
	updated, cmd := overlay.Update(tea.KeyMsg{Type: tea.KeyCtrlF})
	if cmd != nil {
		t.Fatalf("unexpected command opening save prompt: %v", cmd)
	}
	if updated.filterModalMode != newSessionFilterModalSavePrompt {
		t.Fatalf("filter modal mode = %v, want save prompt", updated.filterModalMode)
	}
	if strings.TrimSpace(updated.saveFilterNameInput.Value()) != "" {
		t.Fatalf("expected save prompt to start blank, got %q", updated.saveFilterNameInput.Value())
	}

	expectedName := "My custom filter"
	updated.saveFilterNameInput.SetValue(expectedName)
	updated, cmd = updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected save command after confirming save prompt")
	}
	msg := cmd()
	saveMsg, ok := msg.(SaveNewSessionFilterMsg)
	if !ok {
		t.Fatalf("msg = %T, want SaveNewSessionFilterMsg", msg)
	}
	if saveMsg.Provider != viewFilterAll {
		t.Fatalf("provider = %q, want %q", saveMsg.Provider, viewFilterAll)
	}
	if saveMsg.Name != expectedName {
		t.Fatalf("name = %q, want %q", saveMsg.Name, expectedName)
	}
	if updated.filterModalMode != newSessionFilterModalNone {
		t.Fatalf("filter modal mode = %v, want closed", updated.filterModalMode)
	}
}

func TestNewSessionOverlayCtrlLOpenPickerAndEnterAppliesSelectedFilter(t *testing.T) {
	t.Parallel()

	overlay := newFilterModalTestOverlay(t)
	overlay.SetSavedNewSessionFilters([]domain.NewSessionFilter{
		{
			ID:       "f-gh",
			Name:     "GitHub triage",
			Provider: "github",
			Criteria: domain.NewSessionFilterCriteria{Scope: domain.ScopeIssues, Search: "label:triage"},
		},
		{
			ID:       "f-gl",
			Name:     "GitLab backlog",
			Provider: "gitlab",
			Criteria: domain.NewSessionFilterCriteria{Scope: domain.ScopeIssues, Search: "status:backlog"},
		},
	})

	updated, cmd := overlay.Update(tea.KeyMsg{Type: tea.KeyCtrlL})
	if cmd != nil {
		t.Fatalf("unexpected command opening load picker: %v", cmd)
	}
	if updated.filterModalMode != newSessionFilterModalLoadPicker {
		t.Fatalf("filter modal mode = %v, want load picker", updated.filterModalMode)
	}
	if got := len(updated.loadFilterChoices); got != 2 {
		t.Fatalf("picker choice count = %d, want 2", got)
	}

	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated, cmd = updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected reload command after applying saved filter")
	}
	if got := updated.currentProvider(); got != "gitlab" {
		t.Fatalf("provider = %q, want gitlab", got)
	}
	if got := updated.filterInput.Value(); got != "status:backlog" {
		t.Fatalf("search = %q, want %q", got, "status:backlog")
	}
	if updated.filterModalMode != newSessionFilterModalNone {
		t.Fatalf("filter modal mode = %v, want closed", updated.filterModalMode)
	}
}

func TestNewSessionOverlayCtrlSCyclesScope(t *testing.T) {
	t.Parallel()

	testAdapter := &browseTestAdapter{
		name:         "github",
		browseScopes: []domain.SelectionScope{domain.ScopeIssues, domain.ScopeProjects},
		browseFilters: map[domain.SelectionScope]adapter.BrowseFilterCapabilities{
			domain.ScopeIssues:   {Views: []string{"assigned_to_me", "all"}},
			domain.ScopeProjects: {Views: []string{"assigned_to_me", "all"}},
		},
	}
	overlay := NewNewSessionOverlay([]adapter.WorkItemAdapter{testAdapter}, "ws-1", styles.NewStyles(styles.DefaultTheme))
	overlay.Open()
	overlay.SetSize(100, 30)
	overlay.providerIndex = providerOptionIndex("github")
	overlay.normalizeSelectionOptions()

	initialScope := overlay.currentScope()
	updated, cmd := overlay.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	if cmd == nil {
		t.Fatal("expected reload command when cycling scope with ctrl+s")
	}
	if got := updated.currentScope(); got == initialScope {
		t.Fatalf("scope did not change on ctrl+s: got %q", got)
	}
}

func TestNewSessionOverlayLoadPickerDDeletesSelectedFilter(t *testing.T) {
	t.Parallel()

	overlay := newFilterModalTestOverlay(t)
	overlay.SetSavedNewSessionFilters([]domain.NewSessionFilter{
		{
			ID:       "f-gh",
			Name:     "GitHub triage",
			Provider: "github",
			Criteria: domain.NewSessionFilterCriteria{Scope: domain.ScopeIssues, Search: "label:triage"},
		},
		{
			ID:       "f-gl",
			Name:     "GitLab backlog",
			Provider: "gitlab",
			Criteria: domain.NewSessionFilterCriteria{Scope: domain.ScopeIssues, Search: "status:backlog"},
		},
	})

	updated, _ := overlay.Update(tea.KeyMsg{Type: tea.KeyCtrlL})
	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated, cmd := updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if cmd == nil {
		t.Fatal("expected delete command after pressing d in load picker")
	}
	msg := cmd()
	deleteMsg, ok := msg.(DeleteNewSessionFilterMsg)
	if !ok {
		t.Fatalf("msg = %T, want DeleteNewSessionFilterMsg", msg)
	}
	if deleteMsg.WorkspaceID != "ws-1" {
		t.Fatalf("workspaceID = %q, want ws-1", deleteMsg.WorkspaceID)
	}
	if deleteMsg.FilterID != "f-gl" {
		t.Fatalf("filterID = %q, want f-gl", deleteMsg.FilterID)
	}
}

func TestNewSessionOverlayLoadPickerRefreshesWhenSavedFiltersChange(t *testing.T) {
	t.Parallel()

	overlay := newFilterModalTestOverlay(t)
	overlay.SetSavedNewSessionFilters([]domain.NewSessionFilter{
		{ID: "f-gh", Name: "GitHub triage", Provider: "github", Criteria: domain.NewSessionFilterCriteria{Scope: domain.ScopeIssues}},
		{ID: "f-gl", Name: "GitLab backlog", Provider: "gitlab", Criteria: domain.NewSessionFilterCriteria{Scope: domain.ScopeIssues}},
	})

	updated, _ := overlay.Update(tea.KeyMsg{Type: tea.KeyCtrlL})
	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown}) // select f-gl

	updated.SetSavedNewSessionFilters([]domain.NewSessionFilter{
		{ID: "f-gh", Name: "GitHub triage", Provider: "github", Criteria: domain.NewSessionFilterCriteria{Scope: domain.ScopeIssues}},
	})

	if updated.filterModalMode != newSessionFilterModalLoadPicker {
		t.Fatalf("filter modal mode = %v, want load picker", updated.filterModalMode)
	}
	if got := len(updated.loadFilterChoices); got != 1 {
		t.Fatalf("picker choice count = %d, want 1", got)
	}
	if got := updated.loadFilterChoices[0].ID; got != "f-gh" {
		t.Fatalf("remaining filter id = %q, want f-gh", got)
	}
}

func TestNewSessionOverlayFilterModalViewFitsNarrowLayout(t *testing.T) {
	t.Parallel()

	overlay := newFilterModalTestOverlay(t)
	overlay.SetSavedNewSessionFilters([]domain.NewSessionFilter{{
		ID:       "f-gh",
		Name:     "GitHub Inbox",
		Provider: "github",
		Criteria: domain.NewSessionFilterCriteria{Scope: domain.ScopeIssues, Search: "inbox"},
	}})
	overlay.SetSize(64, 18)

	overlay, _ = overlay.Update(tea.KeyMsg{Type: tea.KeyCtrlF})
	saveView := ansi.Strip(overlay.View())
	if !strings.Contains(saveView, "Save New Session Filter") {
		t.Fatalf("save view = %q, want save modal title", saveView)
	}
	assertOverlayFits(t, saveView, 64, 18)

	overlay, _ = overlay.Update(tea.KeyMsg{Type: tea.KeyEsc})
	overlay, _ = overlay.Update(tea.KeyMsg{Type: tea.KeyCtrlL})
	loadView := ansi.Strip(overlay.View())
	if !strings.Contains(loadView, "Load New Session Filter") {
		t.Fatalf("load view = %q, want load modal title", loadView)
	}
	assertOverlayFits(t, loadView, 64, 18)
}

package views

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/tui/styles"
)

type providerOption struct {
	Key   string
	Label string
}

var providerOptions = []providerOption{
	{Key: "all", Label: "All"},
	{Key: "linear", Label: "Linear"},
	{Key: "github", Label: "GitHub"},
	{Key: "gitlab", Label: "GitLab"},
}

var scopeOptions = []domain.SelectionScope{
	domain.ScopeIssues,
	domain.ScopeProjects,
	domain.ScopeInitiatives,
}

var defaultViewOptions = []string{"assigned_to_me", "created_by_me", "mentioned", "subscribed", "all"}

// selectableItem adapts adapter.ListItem for the bubbles list widget.
type selectableItem struct {
	item     adapter.ListItem
	selected bool
}

func (i selectableItem) Title() string {
	prefix := i.item.Title
	if i.item.Identifier != "" {
		prefix = i.item.Identifier + "  " + i.item.Title
	}
	return strings.TrimSpace(prefix)
}

func (i selectableItem) Description() string {
	parts := make([]string, 0, 3)
	if i.item.Provider != "" {
		parts = append(parts, strings.ToUpper(i.item.Provider))
	}
	if i.item.ContainerRef != "" {
		parts = append(parts, i.item.ContainerRef)
	}
	if i.item.State != "" {
		parts = append(parts, i.item.State)
	}
	return strings.Join(parts, " · ")
}

func (i selectableItem) FilterValue() string {
	return strings.Join([]string{i.item.Provider, i.item.Identifier, i.item.ContainerRef, i.item.Title, i.item.Description}, " ")
}

// NewSessionOverlay is the overlay for creating a new work item session.
type NewSessionOverlay struct {
	adapters       []adapter.WorkItemAdapter
	browseAdapters []adapter.WorkItemAdapter
	workspaceID    string
	providerIndex  int
	scopeIndex     int
	viewIndex      int
	stateIndex     int
	filterInput    textinput.Model
	labelsInput    textinput.Model
	ownerInput     textinput.Model
	repoInput      textinput.Model
	groupInput     textinput.Model
	teamInput      textinput.Model
	issueList      list.Model
	allItems       []adapter.ListItem
	selectedIDs    map[string]bool
	loading        bool
	offset         int
	hasMore        bool
	nextCursor     string
	manualTitle    textinput.Model
	manualDesc     textarea.Model
	manualFocus    int
	showManual     bool
	styles         styles.Styles
	width          int
	height         int
	active         bool
	statusMessage  string
}

// NewNewSessionOverlay constructs a NewSessionOverlay with sane defaults.
func NewNewSessionOverlay(adapters []adapter.WorkItemAdapter, workspaceID string, st styles.Styles) NewSessionOverlay {
	fi := textinput.New()
	fi.Placeholder = "Search work items…"
	fi.CharLimit = 200

	labels := textinput.New()
	labels.Placeholder = "Labels (comma-separated)…"
	labels.CharLimit = 200

	owner := textinput.New()
	owner.Placeholder = "Owner…"
	owner.CharLimit = 200

	repo := textinput.New()
	repo.Placeholder = "Repository / project path…"
	repo.CharLimit = 200

	group := textinput.New()
	group.Placeholder = "Group…"
	group.CharLimit = 200

	team := textinput.New()
	team.Placeholder = "Team…"
	team.CharLimit = 200

	mt := textinput.New()
	mt.Placeholder = "Work item title…"
	mt.CharLimit = 200

	md := textarea.New()
	md.Placeholder = "Description (optional)…"
	md.SetWidth(60)
	md.SetHeight(3)
	md.CharLimit = 2000

	delegate := list.NewDefaultDelegate()
	delegate.ShowDescription = true
	il := list.New([]list.Item{}, delegate, 60, 10)
	il.Title = "Work Items"
	il.SetShowStatusBar(false)
	il.SetFilteringEnabled(true)
	il.SetShowHelp(false)

	browseAdapters := make([]adapter.WorkItemAdapter, 0, len(adapters))
	for _, a := range adapters {
		if a.Capabilities().CanBrowse {
			browseAdapters = append(browseAdapters, a)
		}
	}

	return NewSessionOverlay{
		adapters:       adapters,
		browseAdapters: browseAdapters,
		workspaceID:    workspaceID,
		providerIndex:  0,
		scopeIndex:     0,
		viewIndex:      0,
		stateIndex:     0,
		filterInput:    fi,
		labelsInput:    labels,
		ownerInput:     owner,
		repoInput:      repo,
		groupInput:     group,
		teamInput:      team,
		issueList:      il,
		selectedIDs:    make(map[string]bool),
		manualTitle:    mt,
		manualDesc:     md,
		styles:         st,
	}
}

// Open activates the overlay and sets initial focus.
func (m *NewSessionOverlay) Open() {
	m.active = true
	m.showManual = false
	m.normalizeSelectionOptions()
	m.filterInput.Focus()
}

// Close deactivates the overlay and resets all transient state.
func (m *NewSessionOverlay) Close() {
	m.active = false
	m.filterInput.SetValue("")
	m.filterInput.Blur()
	m.labelsInput.SetValue("")
	m.ownerInput.SetValue("")
	m.repoInput.SetValue("")
	m.groupInput.SetValue("")
	m.teamInput.SetValue("")
	m.manualTitle.SetValue("")
	m.manualDesc.SetValue("")
	m.manualDesc.Blur()
	m.selectedIDs = make(map[string]bool)
	m.offset = 0
	m.hasMore = false
	m.nextCursor = ""
	m.allItems = nil
	m.statusMessage = ""
}

// Active reports whether the overlay is currently shown.
func (m NewSessionOverlay) Active() bool { return m.active }

func (m NewSessionOverlay) selectedProvider() string {
	var provider string
	for _, item := range m.allItems {
		if !m.selectedIDs[item.ID] {
			continue
		}
		if provider == "" {
			provider = item.Provider
			continue
		}
		if item.Provider != provider {
			return "mixed"
		}
	}
	return provider
}

func (m NewSessionOverlay) adapterForName(name string) adapter.WorkItemAdapter {
	for _, a := range m.browseAdapters {
		if a.Name() == name {
			return a
		}
	}
	return nil
}

func (m *NewSessionOverlay) toggleSelection(item adapter.ListItem) error {
	provider := m.selectedProvider()
	if m.selectedIDs[item.ID] {
		delete(m.selectedIDs, item.ID)
		return nil
	}
	if provider != "" && provider != item.Provider {
		return fmt.Errorf("multi-select must stay within one provider")
	}
	m.selectedIDs[item.ID] = true
	return nil
}

func (m NewSessionOverlay) currentProvider() string {
	if m.providerIndex < 0 || m.providerIndex >= len(providerOptions) {
		return "all"
	}
	return providerOptions[m.providerIndex].Key
}

func (m NewSessionOverlay) activeProviderOptions() []providerOption {
	if m.currentScope() == domain.ScopeIssues {
		return providerOptions
	}
	active := make([]providerOption, 0, len(providerOptions)-1)
	for _, option := range providerOptions {
		if option.Key == "all" {
			continue
		}
		active = append(active, option)
	}
	return active
}

func (m NewSessionOverlay) currentScope() domain.SelectionScope {
	if m.scopeIndex < 0 || m.scopeIndex >= len(scopeOptions) {
		return domain.ScopeIssues
	}
	return scopeOptions[m.scopeIndex]
}

func (m NewSessionOverlay) currentView() string {
	views := m.availableViewOptions()
	if len(views) == 0 {
		return ""
	}
	if m.viewIndex < 0 || m.viewIndex >= len(views) {
		return views[0]
	}
	return views[m.viewIndex]
}

func (m NewSessionOverlay) availableStateOptions() []string {
	adapters := m.adaptersForProvider()
	if len(adapters) == 0 {
		return nil
	}
	scope := m.currentScope()
	if !m.scopeSupportedByAdapters(scope, adapters) {
		return nil
	}
	intersection := browseFilterIntersection(scope, adapters)
	if len(intersection.States) == 0 {
		return nil
	}
	return intersection.States
}

func (m NewSessionOverlay) currentState() string {
	states := m.availableStateOptions()
	if len(states) == 0 {
		return ""
	}
	if m.stateIndex < 0 || m.stateIndex >= len(states) {
		return states[0]
	}
	return states[m.stateIndex]
}

func (m NewSessionOverlay) adaptersForProvider() []adapter.WorkItemAdapter {
	provider := m.currentProvider()
	if provider == "all" && m.currentScope() == domain.ScopeIssues {
		return append([]adapter.WorkItemAdapter(nil), m.browseAdapters...)
	}
	filtered := make([]adapter.WorkItemAdapter, 0, len(m.browseAdapters))
	for _, a := range m.browseAdapters {
		if provider == "all" || a.Name() == provider {
			filtered = append(filtered, a)
		}
	}
	return filtered
}

func (m NewSessionOverlay) availableScopes() []domain.SelectionScope {
	provider := m.currentProvider()
	if provider == "all" {
		return []domain.SelectionScope{domain.ScopeIssues}
	}
	adapters := m.adaptersForProvider()
	available := make([]domain.SelectionScope, 0, len(scopeOptions))
	for _, scope := range scopeOptions {
		if m.scopeSupportedByAdapters(scope, adapters) {
			available = append(available, scope)
		}
	}
	if len(available) == 0 {
		return []domain.SelectionScope{domain.ScopeIssues}
	}
	return available
}

func (m NewSessionOverlay) scopeSupportedByAdapters(scope domain.SelectionScope, adapters []adapter.WorkItemAdapter) bool {
	if len(adapters) == 0 {
		return false
	}
	for _, a := range adapters {
		caps := a.Capabilities()
		supported := false
		for _, available := range caps.BrowseScopes {
			if available == scope {
				supported = true
				break
			}
		}
		if !supported {
			return false
		}
	}
	return true
}

func (m NewSessionOverlay) availableViewOptions() []string {
	adapters := m.adaptersForProvider()
	if len(adapters) == 0 {
		return nil
	}
	scope := m.currentScope()
	if !m.scopeSupportedByAdapters(scope, adapters) {
		return nil
	}
	intersection := browseFilterIntersection(scope, adapters)
	if len(intersection.Views) == 0 {
		return nil
	}
	return intersection.Views
}

func browseFilterIntersection(scope domain.SelectionScope, adapters []adapter.WorkItemAdapter) adapter.BrowseFilterCapabilities {
	first := true
	var merged adapter.BrowseFilterCapabilities
	for _, a := range adapters {
		caps := a.Capabilities()
		filterCaps, ok := caps.BrowseFilters[scope]
		if !ok {
			return adapter.BrowseFilterCapabilities{}
		}
		if first {
			merged = filterCaps
			first = false
			continue
		}
		merged.Views = intersectStrings(merged.Views, filterCaps.Views)
		merged.States = intersectStrings(merged.States, filterCaps.States)
		merged.SupportsLabels = merged.SupportsLabels && filterCaps.SupportsLabels
		merged.SupportsSearch = merged.SupportsSearch && filterCaps.SupportsSearch
		merged.SupportsCursor = merged.SupportsCursor && filterCaps.SupportsCursor
		merged.SupportsOffset = merged.SupportsOffset && filterCaps.SupportsOffset
		merged.SupportsOwner = merged.SupportsOwner && filterCaps.SupportsOwner
		merged.SupportsRepo = merged.SupportsRepo && filterCaps.SupportsRepo
		merged.SupportsGroup = merged.SupportsGroup && filterCaps.SupportsGroup
		merged.SupportsTeam = merged.SupportsTeam && filterCaps.SupportsTeam
	}
	return merged
}

func intersectStrings(left, right []string) []string {
	if len(left) == 0 || len(right) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(right))
	for _, value := range right {
		seen[value] = struct{}{}
	}
	merged := make([]string, 0, len(left))
	for _, value := range left {
		if _, ok := seen[value]; ok {
			merged = append(merged, value)
		}
	}
	return merged
}

func (m *NewSessionOverlay) normalizeSelectionOptions() {
	availableScopes := m.availableScopes()
	if len(availableScopes) > 0 {
		current := m.currentScope()
		m.scopeIndex = 0
		for i, scope := range availableScopes {
			if scope == current {
				m.scopeIndex = i
				break
			}
		}
	}
	availableViews := m.availableViewOptions()
	if len(availableViews) == 0 {
		m.viewIndex = 0
	} else {
		current := ""
		if m.viewIndex >= 0 && m.viewIndex < len(defaultViewOptions) {
			current = defaultViewOptions[m.viewIndex]
		}
		m.viewIndex = 0
		for i, view := range availableViews {
			if view == current {
				m.viewIndex = i
				break
			}
		}
	}
	availableStates := m.availableStateOptions()
	if len(availableStates) == 0 {
		m.stateIndex = 0
	} else {
		current := ""
		if m.stateIndex >= 0 && m.stateIndex < len(availableStates) {
			current = availableStates[m.stateIndex]
		}
		m.stateIndex = 0
		for i, state := range availableStates {
			if state == current {
				m.stateIndex = i
				break
			}
		}
	}
	m.statusMessage = m.providerScopeStatusMessage()
}

func (m NewSessionOverlay) providerScopeStatusMessage() string {
	filters := m.currentFilterCapabilities()
	if m.currentScope() == domain.ScopeIssues && len(filters.Views) == 0 && filters.SupportsTeam {
		provider := m.currentProvider()
		label := strings.Title(provider)
		if provider == "all" || provider == "" {
			label = "This provider"
		}
		return label + " browsing is container-scoped; inbox-style view filters are hidden."
	}
	return ""
}

func parseCommaSeparated(value string) []string {
	parts := strings.Split(value, ",")
	labels := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			labels = append(labels, trimmed)
		}
	}
	return labels
}

func (m NewSessionOverlay) currentFilterCapabilities() adapter.BrowseFilterCapabilities {
	return browseFilterIntersection(m.currentScope(), m.adaptersForProvider())
}

func (m NewSessionOverlay) advancedFilterRows() []string {
	filters := m.currentFilterCapabilities()
	rows := make([]string, 0, 5)
	if filters.SupportsLabels {
		rows = append(rows, lipgloss.NewStyle().Foreground(lipgloss.Color("#b0b0b0")).Render("Labels: ")+m.labelsInput.View())
	}
	if filters.SupportsOwner {
		rows = append(rows, lipgloss.NewStyle().Foreground(lipgloss.Color("#b0b0b0")).Render("Owner:  ")+m.ownerInput.View())
	}
	if filters.SupportsRepo {
		rows = append(rows, lipgloss.NewStyle().Foreground(lipgloss.Color("#b0b0b0")).Render("Repo:   ")+m.repoInput.View())
	}
	if filters.SupportsGroup {
		rows = append(rows, lipgloss.NewStyle().Foreground(lipgloss.Color("#b0b0b0")).Render("Group:  ")+m.groupInput.View())
	}
	if filters.SupportsTeam {
		rows = append(rows, lipgloss.NewStyle().Foreground(lipgloss.Color("#b0b0b0")).Render("Team:   ")+m.teamInput.View())
	}
	return rows
}

func providerSortKey(name string) int {
	for i, option := range providerOptions {
		if option.Key == name {
			return i
		}
	}
	return len(providerOptions) + 1
}

// loadItemsCmd fetches items from the selected browse-capable adapters.
func (m NewSessionOverlay) loadItemsCmd() tea.Cmd {
	adapters := m.adaptersForProvider()
	if len(adapters) == 0 {
		return func() tea.Msg { return issueListLoadedMsg{} }
	}
	provider := m.currentProvider()
	scope := m.currentScope()
	view := m.currentView()
	state := m.currentState()
	search := strings.TrimSpace(m.filterInput.Value())
	labels := parseCommaSeparated(m.labelsInput.Value())
	owner := strings.TrimSpace(m.ownerInput.Value())
	repo := strings.TrimSpace(m.repoInput.Value())
	group := strings.TrimSpace(m.groupInput.Value())
	team := strings.TrimSpace(m.teamInput.Value())
	offset := m.offset
	return func() tea.Msg {
		merged := make([]adapter.ListItem, 0)
		total := 0
		hasMore := false
		nextCursor := ""
		for _, a := range adapters {
			caps := a.Capabilities()
			if _, ok := caps.BrowseFilters[scope]; !ok {
				continue
			}
			result, err := a.ListSelectable(context.Background(), adapter.ListOpts{
				WorkspaceID: m.workspaceID,
				Provider:    provider,
				Scope:       scope,
				Search:      search,
				Limit:       50,
				Offset:      offset,
				View:        view,
				State:       state,
				Labels:      labels,
				Owner:       owner,
				Repo:        repo,
				Group:       group,
				TeamID:      team,
			})
			if err != nil {
				return ErrMsg{Err: err}
			}
			for _, item := range result.Items {
				if item.Provider == "" {
					item.Provider = a.Name()
				}
				merged = append(merged, item)
			}
			total += result.TotalCount
			hasMore = hasMore || result.HasMore
			if nextCursor == "" && result.NextCursor != "" {
				nextCursor = result.NextCursor
			}
		}
		sort.SliceStable(merged, func(i, j int) bool {
			if providerSortKey(merged[i].Provider) != providerSortKey(merged[j].Provider) {
				return providerSortKey(merged[i].Provider) < providerSortKey(merged[j].Provider)
			}
			if !merged[i].UpdatedAt.Equal(merged[j].UpdatedAt) {
				return merged[i].UpdatedAt.After(merged[j].UpdatedAt)
			}
			return merged[i].Title < merged[j].Title
		})
		return issueListLoadedMsg{items: merged, total: total, hasMore: hasMore, nextCursor: nextCursor}
	}
}

// issueListLoadedMsg is an internal msg carrying fetched list items.
type issueListLoadedMsg struct {
	items      []adapter.ListItem
	total      int
	hasMore    bool
	nextCursor string
}

// Update handles incoming messages for the overlay.
func (m NewSessionOverlay) Update(msg tea.Msg) (NewSessionOverlay, tea.Cmd) {
	var cmds []tea.Cmd
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case issueListLoadedMsg:
		m.loading = false
		m.allItems = msg.items
		m.hasMore = msg.hasMore
		m.nextCursor = msg.nextCursor
		items := make([]list.Item, len(msg.items))
		for i, it := range msg.items {
			items[i] = selectableItem{item: it}
		}
		m.issueList.SetItems(items)

	case tea.KeyMsg:
		if m.showManual {
			switch msg.String() {
			case "esc":
				return m, func() tea.Msg { return CloseOverlayMsg{} }
			case "backtab", "shift+tab":
				if m.manualFocus == 1 {
					m.manualDesc.Blur()
					m.manualFocus = 0
					m.manualTitle.Focus()
				}
			case "tab":
				if m.manualFocus == 0 {
					m.manualTitle.Blur()
					m.manualFocus = 1
					m.manualDesc.Focus()
				} else {
					m.showManual = false
					m.manualDesc.Blur()
					m.filterInput.Focus()
				}
			case "enter":
				if m.manualFocus == 1 || m.manualTitle.Value() != "" {
					title := strings.TrimSpace(m.manualTitle.Value())
					if title == "" {
						break
					}
					desc := m.manualDesc.Value()
					for _, a := range m.adapters {
						if a.Name() == "manual" {
							return m, func() tea.Msg { return NewSessionManualMsg{Adapter: a, Title: title, Desc: desc} }
						}
					}
					return m, func() tea.Msg { return ErrMsg{Err: fmt.Errorf("no manual adapter configured")} }
				}
			default:
				if m.manualFocus == 0 {
					m.manualTitle, cmd = m.manualTitle.Update(msg)
				} else {
					m.manualDesc, cmd = m.manualDesc.Update(msg)
				}
				cmds = append(cmds, cmd)
			}
			break
		}

		switch msg.String() {
		case "esc":
			return m, func() tea.Msg { return CloseOverlayMsg{} }
		case "tab":
			options := m.activeProviderOptions()
			if len(options) > 0 {
				m.providerIndex = (m.providerIndex + 1) % len(options)
				m.normalizeSelectionOptions()
				m.offset = 0
				m.loading = true
				cmds = append(cmds, m.loadItemsCmd())
			}
		case "shift+tab", "backtab":
			options := m.activeProviderOptions()
			if len(options) > 0 {
				m.providerIndex = (m.providerIndex - 1 + len(options)) % len(options)
				m.normalizeSelectionOptions()
				m.offset = 0
				m.loading = true
				cmds = append(cmds, m.loadItemsCmd())
			}
		case "ctrl+s":
			availableScopes := m.availableScopes()
			if len(availableScopes) > 0 {
				current := m.currentScope()
				next := 0
				for i, scope := range availableScopes {
					if scope == current {
						next = (i + 1) % len(availableScopes)
						break
					}
				}
				m.scopeIndex = next
				m.normalizeSelectionOptions()
				m.offset = 0
				m.loading = true
				cmds = append(cmds, m.loadItemsCmd())
			}
		case "ctrl+v":
			availableViews := m.availableViewOptions()
			if len(availableViews) > 0 {
				m.viewIndex = (m.viewIndex + 1) % len(availableViews)
				m.offset = 0
				m.loading = true
				cmds = append(cmds, m.loadItemsCmd())
			}
		case "ctrl+t":
			availableStates := m.availableStateOptions()
			if len(availableStates) > 0 {
				m.stateIndex = (m.stateIndex + 1) % len(availableStates)
				m.offset = 0
				m.loading = true
				cmds = append(cmds, m.loadItemsCmd())
			}
		case "ctrl+m":
			m.showManual = true
			m.filterInput.Blur()
			m.manualTitle.Focus()
			m.manualFocus = 0
		case "ctrl+n":
			if m.hasMore {
				m.offset += 50
				m.loading = true
				cmds = append(cmds, m.loadItemsCmd())
			}
		case "ctrl+p":
			if m.offset >= 50 {
				m.offset -= 50
				m.loading = true
				cmds = append(cmds, m.loadItemsCmd())
			}
		case "enter":
			if len(m.selectedIDs) == 0 {
				if sel, ok := m.issueList.SelectedItem().(selectableItem); ok {
					if err := m.toggleSelection(sel.item); err != nil {
						return m, func() tea.Msg { return ErrMsg{Err: err} }
					}
				}
			}
			if len(m.selectedIDs) > 0 {
				provider := m.selectedProvider()
				if provider == "" || provider == "mixed" {
					return m, func() tea.Msg {
						return ErrMsg{Err: fmt.Errorf("selected work items must come from exactly one provider")}
					}
				}
				ids := make([]string, 0, len(m.selectedIDs))
				for id := range m.selectedIDs {
					ids = append(ids, id)
				}
				sort.Strings(ids)
				a := m.adapterForName(provider)
				if a == nil {
					return m, func() tea.Msg {
						return ErrMsg{Err: fmt.Errorf("no browse adapter configured for provider %s", provider)}
					}
				}
				for _, available := range a.Capabilities().BrowseScopes {
					if available == m.currentScope() {
						sel := adapter.Selection{Scope: m.currentScope(), ItemIDs: ids, Metadata: map[string]any{"provider": a.Name()}}
						return m, func() tea.Msg { return NewSessionBrowseMsg{Adapter: a, Selection: sel} }
					}
				}
				return m, func() tea.Msg {
					return ErrMsg{Err: fmt.Errorf("provider %s does not support %s selection", provider, m.currentScope())}
				}
			}
		case " ":
			if sel, ok := m.issueList.SelectedItem().(selectableItem); ok {
				if err := m.toggleSelection(sel.item); err != nil {
					return m, func() tea.Msg { return ErrMsg{Err: err} }
				}
			}
		default:
			m.issueList, cmd = m.issueList.Update(msg)
			cmds = append(cmds, cmd)
			beforeSearch := m.filterInput.Value()
			beforeLabels := m.labelsInput.Value()
			beforeOwner := m.ownerInput.Value()
			beforeRepo := m.repoInput.Value()
			beforeGroup := m.groupInput.Value()
			beforeTeam := m.teamInput.Value()
			m.filterInput, cmd = m.filterInput.Update(msg)
			cmds = append(cmds, cmd)
			m.labelsInput, cmd = m.labelsInput.Update(msg)
			cmds = append(cmds, cmd)
			m.ownerInput, cmd = m.ownerInput.Update(msg)
			cmds = append(cmds, cmd)
			m.repoInput, cmd = m.repoInput.Update(msg)
			cmds = append(cmds, cmd)
			m.groupInput, cmd = m.groupInput.Update(msg)
			cmds = append(cmds, cmd)
			m.teamInput, cmd = m.teamInput.Update(msg)
			cmds = append(cmds, cmd)
			if beforeSearch != m.filterInput.Value() || beforeLabels != m.labelsInput.Value() || beforeOwner != m.ownerInput.Value() || beforeRepo != m.repoInput.Value() || beforeGroup != m.groupInput.Value() || beforeTeam != m.teamInput.Value() {
				m.offset = 0
				m.loading = true
				cmds = append(cmds, m.loadItemsCmd())
			}
		}
	}

	return m, tea.Batch(cmds...)
}

// View renders the overlay, or empty string when inactive.
func (m NewSessionOverlay) View() string {
	if !m.active {
		return ""
	}

	w := 88
	if m.width > 0 && m.width < 96 {
		w = m.width - 4
	}

	providerLabels := make([]string, 0, len(m.activeProviderOptions()))
	for i, option := range m.activeProviderOptions() {
		if i == m.providerIndex {
			providerLabels = append(providerLabels, lipgloss.NewStyle().Foreground(lipgloss.Color("#5b8def")).Bold(true).Render("[► "+option.Label+" ◄]"))
		} else {
			providerLabels = append(providerLabels, lipgloss.NewStyle().Foreground(lipgloss.Color("#6b7280")).Render(option.Label))
		}
	}

	scopeLabels := make([]string, 0, len(m.availableScopes()))
	for _, option := range m.availableScopes() {
		label := strings.Title(string(option))
		if option == m.currentScope() {
			scopeLabels = append(scopeLabels, lipgloss.NewStyle().Foreground(lipgloss.Color("#34d399")).Bold(true).Render("["+label+"]"))
		} else {
			scopeLabels = append(scopeLabels, lipgloss.NewStyle().Foreground(lipgloss.Color("#6b7280")).Render(label))
		}
	}

	viewLabels := make([]string, 0, len(m.availableViewOptions()))
	for _, option := range m.availableViewOptions() {
		label := strings.ReplaceAll(option, "_", " ")
		if option == m.currentView() {
			viewLabels = append(viewLabels, lipgloss.NewStyle().Foreground(lipgloss.Color("#f59e0b")).Bold(true).Render("["+label+"]"))
		} else {
			viewLabels = append(viewLabels, lipgloss.NewStyle().Foreground(lipgloss.Color("#6b7280")).Render(label))
		}
	}
	stateLabels := make([]string, 0, len(m.availableStateOptions()))
	for _, option := range m.availableStateOptions() {
		if option == m.currentState() {
			stateLabels = append(stateLabels, lipgloss.NewStyle().Foreground(lipgloss.Color("#a78bfa")).Bold(true).Render("["+option+"]"))
		} else {
			stateLabels = append(stateLabels, lipgloss.NewStyle().Foreground(lipgloss.Color("#6b7280")).Render(option))
		}
	}
	header := []string{
		lipgloss.NewStyle().Foreground(lipgloss.Color("#f0f0f0")).Bold(true).Render("Browse Work Items"),
		"Source: " + strings.Join(providerLabels, "  "),
		"Scope:  " + strings.Join(scopeLabels, "  "),
	}
	if len(viewLabels) > 0 {
		header = append(header, "View:   "+strings.Join(viewLabels, "  "))
	}
	if len(stateLabels) > 0 {
		header = append(header, "State:  "+strings.Join(stateLabels, "  "))
	}
	if m.statusMessage != "" {
		header = append(header, lipgloss.NewStyle().Foreground(lipgloss.Color("#f59e0b")).Render(m.statusMessage))
	}

	var body string
	if m.showManual {
		body = m.manualView(w)
	} else {
		body = m.browserView(w)
	}

	content := strings.Join(header, "\n") + "\n\n" + body
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#2d2d44")).
		Background(lipgloss.Color("#1a1a2e")).
		Padding(1, 2).
		Width(w)
	return boxStyle.Render(content)
}

func (m NewSessionOverlay) browserView(w int) string {
	var lines []string
	filterRow := lipgloss.NewStyle().Foreground(lipgloss.Color("#b0b0b0")).Render("Search: ") + m.filterInput.View()
	lines = append(lines, filterRow)
	advancedRows := m.advancedFilterRows()
	if len(advancedRows) > 0 {
		lines = append(lines, advancedRows...)
	}
	lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("#2d2d44")).Render(strings.Repeat("─", w-4)))
	if m.loading {
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("#6b7280")).Render("Loading…"))
	} else {
		m.issueList.SetWidth(w - 4)
		lines = append(lines, m.issueList.View())
	}
	hintText := "[Enter] Start  [Space] Select  [Tab] Source  [Ctrl+S] Scope  [Ctrl+V] View  [Ctrl+T] State  [Ctrl+M] Manual  [Ctrl+N/P] Page  [Esc] Cancel  Multi-select: same provider only"
	if len(m.availableViewOptions()) == 0 && len(m.availableStateOptions()) == 0 {
		hintText = "[Enter] Start  [Space] Select  [Tab] Source  [Ctrl+S] Scope  [Ctrl+M] Manual  [Ctrl+N/P] Page  [Esc] Cancel  Multi-select: same provider only"
	} else if len(m.availableViewOptions()) == 0 {
		hintText = "[Enter] Start  [Space] Select  [Tab] Source  [Ctrl+S] Scope  [Ctrl+T] State  [Ctrl+M] Manual  [Ctrl+N/P] Page  [Esc] Cancel  Multi-select: same provider only"
	} else if len(m.availableStateOptions()) == 0 {
		hintText = "[Enter] Start  [Space] Select  [Tab] Source  [Ctrl+S] Scope  [Ctrl+V] View  [Ctrl+M] Manual  [Ctrl+N/P] Page  [Esc] Cancel  Multi-select: same provider only"
	}
	hints := lipgloss.NewStyle().Foreground(lipgloss.Color("#6b7280")).Render(hintText)
	lines = append(lines, hints)
	return strings.Join(lines, "\n")
}

func (m NewSessionOverlay) manualView(_ int) string {
	titleLabel := lipgloss.NewStyle().Foreground(lipgloss.Color("#b0b0b0")).Render("Title:       ")
	descLabel := lipgloss.NewStyle().Foreground(lipgloss.Color("#b0b0b0")).Render("Description: ")
	hints := lipgloss.NewStyle().Foreground(lipgloss.Color("#6b7280")).Render(
		"[Tab] Next field  [Enter] Start  [Tab on desc] Return to browser  [Esc] Cancel")
	return strings.Join([]string{
		titleLabel + m.manualTitle.View(),
		descLabel + m.manualDesc.View(),
		"",
		hints,
	}, "\n")
}

// SetSize updates the overlay dimensions and propagates to sub-widgets.
func (m *NewSessionOverlay) SetSize(w, h int) {
	m.width = w
	m.height = h
	m.issueList.SetWidth(w - 8)
	m.issueList.SetHeight(h / 2)
}

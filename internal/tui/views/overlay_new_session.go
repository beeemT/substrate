package views

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

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

const (
	browseWindowWidth       = 240
	browseHeightNumerator   = 3
	browseHeightDenom       = 5
	browseMinListHeight     = 6
	browseMinPaneWidth      = 36
	detailMinPaneWidth      = 44
	browsePageSize          = 50
	infiniteScrollThreshold = 5
	overlayHorizontalFrame  = 2
	overlayHorizontalPad    = 4
	paneHorizontalFrame     = 4
	paneVerticalFrame       = 2
	overlayBackgroundColor  = "#1a1a2e"
)

type browseFocusArea int

const (
	browseFocusControls browseFocusArea = iota
	browseFocusList
	browseFocusDetails
)

type browseControl int

const (
	browseControlSource browseControl = iota
	browseControlScope
	browseControlView
	browseControlState
	browseControlSearch
	browseControlLabels
	browseControlOwner
	browseControlRepo
	browseControlGroup
	browseControlTeam
)

type browseLoadMode int

const (
	browseLoadReset browseLoadMode = iota
	browseLoadAppend
)

type browsePageState struct {
	Items      []adapter.ListItem
	Offset     int
	NextCursor string
	HasMore    bool
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

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
	browsePages    map[string]browsePageState
	selectedIDs    map[string]bool
	loading        bool
	hasMore        bool
	manualTitle    textinput.Model
	manualDesc     textarea.Model
	manualFocus    int
	showManual     bool
	browseFocus    browseFocusArea
	browseControl  browseControl
	detailViewport viewport.Model
	detailItemID   string
	detailWidth    int
	requestSeq     int
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
	il.Styles.NoItems = il.Styles.NoItems.Background(lipgloss.Color(overlayBackgroundColor))
	il.Styles.StatusEmpty = il.Styles.StatusEmpty.Background(lipgloss.Color(overlayBackgroundColor))

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
		browsePages:    make(map[string]browsePageState),
		selectedIDs:    make(map[string]bool),
		manualTitle:    mt,
		manualDesc:     md,
		browseFocus:    browseFocusControls,
		browseControl:  browseControlSearch,
		detailViewport: viewport.New(0, 0),
		styles:         st,
	}
}

// Open activates the overlay and sets initial focus.
func (m *NewSessionOverlay) Open() {
	m.active = true
	m.showManual = false
	m.normalizeSelectionOptions()
	m.setBrowseControlFocus(browseControlSearch)
	m.syncDetailViewport(true)
}

// Close deactivates the overlay and resets all transient state.
func (m *NewSessionOverlay) Close() {
	m.active = false
	m.filterInput.SetValue("")
	m.labelsInput.SetValue("")
	m.ownerInput.SetValue("")
	m.repoInput.SetValue("")
	m.groupInput.SetValue("")
	m.teamInput.SetValue("")
	m.blurBrowseInputs()
	m.manualTitle.SetValue("")
	m.manualTitle.Blur()
	m.manualDesc.SetValue("")
	m.manualDesc.Blur()
	m.browsePages = make(map[string]browsePageState)
	m.selectedIDs = make(map[string]bool)
	m.allItems = nil
	m.loading = false
	m.hasMore = false
	m.detailViewport.SetContent("")
	m.detailViewport.YOffset = 0
	m.detailItemID = ""
	m.detailWidth = 0
	m.requestSeq = 0
	m.statusMessage = ""
	m.browseFocus = browseFocusControls
	m.browseControl = browseControlSearch
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
		m.scopeIndex = scopeOptionIndex(availableScopes[0])
		for _, scope := range availableScopes {
			if scope == current {
				m.scopeIndex = scopeOptionIndex(scope)
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
	m.normalizeBrowseFocus()
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

func providerOptionIndex(key string) int {
	for i, option := range providerOptions {
		if option.Key == key {
			return i
		}
	}
	return 0
}

func scopeOptionIndex(scope domain.SelectionScope) int {
	for i, option := range scopeOptions {
		if option == scope {
			return i
		}
	}
	return 0
}

func (m NewSessionOverlay) browseControls() []browseControl {
	controls := []browseControl{browseControlSource, browseControlScope}
	if len(m.availableViewOptions()) > 0 {
		controls = append(controls, browseControlView)
	}
	if len(m.availableStateOptions()) > 0 {
		controls = append(controls, browseControlState)
	}
	controls = append(controls, browseControlSearch)
	filters := m.currentFilterCapabilities()
	if filters.SupportsLabels {
		controls = append(controls, browseControlLabels)
	}
	if filters.SupportsOwner {
		controls = append(controls, browseControlOwner)
	}
	if filters.SupportsRepo {
		controls = append(controls, browseControlRepo)
	}
	if filters.SupportsGroup {
		controls = append(controls, browseControlGroup)
	}
	if filters.SupportsTeam {
		controls = append(controls, browseControlTeam)
	}
	return controls
}

func (m NewSessionOverlay) hasBrowsableItems() bool {
	return !m.loading && len(m.allItems) > 0
}

func (m NewSessionOverlay) isBrowseControlFocused(control browseControl) bool {
	return m.browseFocus == browseFocusControls && m.browseControl == control
}

func (m NewSessionOverlay) controlLabel(label string, control browseControl) string {
	style := lipgloss.NewStyle().Foreground(lipgloss.Color("#b0b0b0"))
	if m.isBrowseControlFocused(control) {
		style = style.Foreground(lipgloss.Color("#f0f0f0")).Bold(true)
	}
	return style.Render(label)
}

func (m *NewSessionOverlay) blurBrowseInputs() {
	m.filterInput.Blur()
	m.labelsInput.Blur()
	m.ownerInput.Blur()
	m.repoInput.Blur()
	m.groupInput.Blur()
	m.teamInput.Blur()
}

func (m *NewSessionOverlay) setBrowseControlFocus(control browseControl) {
	controls := m.browseControls()
	if len(controls) == 0 {
		m.browseFocus = browseFocusList
		m.blurBrowseInputs()
		return
	}
	valid := false
	for _, candidate := range controls {
		if candidate == control {
			valid = true
			break
		}
	}
	if !valid {
		control = browseControlSearch
		valid = false
		for _, candidate := range controls {
			if candidate == control {
				valid = true
				break
			}
		}
		if !valid {
			control = controls[0]
		}
	}
	m.browseFocus = browseFocusControls
	m.browseControl = control
	m.blurBrowseInputs()
	switch control {
	case browseControlSearch:
		m.filterInput.Focus()
	case browseControlLabels:
		m.labelsInput.Focus()
	case browseControlOwner:
		m.ownerInput.Focus()
	case browseControlRepo:
		m.repoInput.Focus()
	case browseControlGroup:
		m.groupInput.Focus()
	case browseControlTeam:
		m.teamInput.Focus()
	}
}

func (m *NewSessionOverlay) setBrowseListFocus() {
	m.browseFocus = browseFocusList
	m.blurBrowseInputs()
}

func (m *NewSessionOverlay) setBrowseDetailsFocus() {
	m.browseFocus = browseFocusDetails
	m.blurBrowseInputs()
}

func (m *NewSessionOverlay) normalizeBrowseFocus() {
	controls := m.browseControls()
	if len(controls) == 0 {
		return
	}
	switch m.browseFocus {
	case browseFocusList, browseFocusDetails:
		if m.hasBrowsableItems() {
			m.blurBrowseInputs()
			return
		}
		m.setBrowseControlFocus(controls[len(controls)-1])
	default:
		m.setBrowseControlFocus(m.browseControl)
	}
}

func (m *NewSessionOverlay) moveBrowseFocus(delta int) bool {
	controls := m.browseControls()
	if len(controls) == 0 {
		return false
	}
	if m.browseFocus == browseFocusList {
		if delta < 0 && m.issueList.Index() == 0 {
			m.setBrowseControlFocus(controls[len(controls)-1])
			return true
		}
		return false
	}
	if m.browseFocus == browseFocusDetails {
		return false
	}
	currentIndex := 0
	for i, control := range controls {
		if control == m.browseControl {
			currentIndex = i
			break
		}
	}
	nextIndex := currentIndex + delta
	switch {
	case nextIndex < 0:
		m.setBrowseControlFocus(controls[0])
	case nextIndex >= len(controls):
		if m.hasBrowsableItems() {
			m.setBrowseListFocus()
		} else {
			m.setBrowseControlFocus(controls[len(controls)-1])
		}
	default:
		m.setBrowseControlFocus(controls[nextIndex])
	}
	return true
}

func (m NewSessionOverlay) activeProviderIndices() []int {
	indices := make([]int, 0, len(providerOptions))
	for i, option := range providerOptions {
		if option.Key == "all" && m.currentScope() != domain.ScopeIssues {
			continue
		}
		indices = append(indices, i)
	}
	return indices
}

func (m *NewSessionOverlay) nextRequestID() int {
	m.requestSeq++
	return m.requestSeq
}

func (m *NewSessionOverlay) reloadItems() tea.Cmd {
	m.loading = true
	return m.loadItemsCmd(browseLoadReset, m.nextRequestID())
}

func (m *NewSessionOverlay) loadMoreItems() tea.Cmd {
	if m.loading || !m.hasMore {
		return nil
	}
	m.loading = true
	return m.loadItemsCmd(browseLoadAppend, m.nextRequestID())
}

func (m *NewSessionOverlay) cycleProvider(delta int) tea.Cmd {
	indices := m.activeProviderIndices()
	if len(indices) == 0 {
		return nil
	}
	current := 0
	for i, index := range indices {
		if index == m.providerIndex {
			current = i
			break
		}
	}
	next := (current + delta + len(indices)) % len(indices)
	m.providerIndex = indices[next]
	m.normalizeSelectionOptions()
	return m.reloadItems()
}

func (m *NewSessionOverlay) cycleScope(delta int) tea.Cmd {
	availableScopes := m.availableScopes()
	if len(availableScopes) == 0 {
		return nil
	}
	current := 0
	for i, scope := range availableScopes {
		if scope == m.currentScope() {
			current = i
			break
		}
	}
	next := (current + delta + len(availableScopes)) % len(availableScopes)
	m.scopeIndex = scopeOptionIndex(availableScopes[next])
	m.normalizeSelectionOptions()
	return m.reloadItems()
}

func (m *NewSessionOverlay) cycleView(delta int) tea.Cmd {
	availableViews := m.availableViewOptions()
	if len(availableViews) == 0 {
		return nil
	}
	current := 0
	for i, view := range availableViews {
		if view == m.currentView() {
			current = i
			break
		}
	}
	m.viewIndex = (current + delta + len(availableViews)) % len(availableViews)
	return m.reloadItems()
}

func (m *NewSessionOverlay) cycleState(delta int) tea.Cmd {
	availableStates := m.availableStateOptions()
	if len(availableStates) == 0 {
		return nil
	}
	current := 0
	for i, state := range availableStates {
		if state == m.currentState() {
			current = i
			break
		}
	}
	m.stateIndex = (current + delta + len(availableStates)) % len(availableStates)
	return m.reloadItems()
}

func (m *NewSessionOverlay) updateFocusedBrowseInput(msg tea.KeyMsg) []tea.Cmd {
	if m.browseFocus != browseFocusControls {
		return nil
	}
	var cmds []tea.Cmd
	var cmd tea.Cmd
	var before, after string
	switch m.browseControl {
	case browseControlSearch:
		before = m.filterInput.Value()
		m.filterInput, cmd = m.filterInput.Update(msg)
		after = m.filterInput.Value()
	case browseControlLabels:
		before = m.labelsInput.Value()
		m.labelsInput, cmd = m.labelsInput.Update(msg)
		after = m.labelsInput.Value()
	case browseControlOwner:
		before = m.ownerInput.Value()
		m.ownerInput, cmd = m.ownerInput.Update(msg)
		after = m.ownerInput.Value()
	case browseControlRepo:
		before = m.repoInput.Value()
		m.repoInput, cmd = m.repoInput.Update(msg)
		after = m.repoInput.Value()
	case browseControlGroup:
		before = m.groupInput.Value()
		m.groupInput, cmd = m.groupInput.Update(msg)
		after = m.groupInput.Value()
	case browseControlTeam:
		before = m.teamInput.Value()
		m.teamInput, cmd = m.teamInput.Update(msg)
		after = m.teamInput.Value()
	default:
		return nil
	}
	cmds = append(cmds, cmd)
	if before != after {
		cmds = append(cmds, m.reloadItems())
	}
	return cmds
}

func (m NewSessionOverlay) advancedFilterRows() []string {
	filters := m.currentFilterCapabilities()
	rows := make([]string, 0, 5)
	if filters.SupportsLabels {
		rows = append(rows, m.controlLabel("Labels: ", browseControlLabels)+m.labelsInput.View())
	}
	if filters.SupportsOwner {
		rows = append(rows, m.controlLabel("Owner:  ", browseControlOwner)+m.ownerInput.View())
	}
	if filters.SupportsRepo {
		rows = append(rows, m.controlLabel("Repo:   ", browseControlRepo)+m.repoInput.View())
	}
	if filters.SupportsGroup {
		rows = append(rows, m.controlLabel("Group:  ", browseControlGroup)+m.groupInput.View())
	}
	if filters.SupportsTeam {
		rows = append(rows, m.controlLabel("Team:   ", browseControlTeam)+m.teamInput.View())
	}
	return rows
}

func fitOverlayLine(line string, width int) string {
	if width <= 0 {
		return ""
	}
	return lipgloss.NewStyle().Width(width).Render(ansi.Truncate(line, width, ""))
}

func (m NewSessionOverlay) renderWidth() int {
	if m.width <= 0 {
		return maxInt(1, browseWindowWidth-overlayHorizontalFrame)
	}
	return maxInt(1, minInt(browseWindowWidth-overlayHorizontalFrame, m.width-overlayHorizontalFrame))
}

func (m NewSessionOverlay) renderContentWidth() int {
	return maxInt(1, m.renderWidth()-overlayHorizontalPad)
}

func (m NewSessionOverlay) browserInputWidth(renderWidth int) int {
	return maxInt(1, renderWidth-20)
}

func (m *NewSessionOverlay) resizeInputs(renderWidth int) {
	inputWidth := m.browserInputWidth(renderWidth)
	m.filterInput.Width = inputWidth
	m.labelsInput.Width = inputWidth
	m.ownerInput.Width = inputWidth
	m.repoInput.Width = inputWidth
	m.groupInput.Width = inputWidth
	m.teamInput.Width = inputWidth
	m.manualTitle.Width = inputWidth
	m.manualDesc.SetWidth(inputWidth)
}

func (m NewSessionOverlay) browserChromeLines(advancedRows int) int {
	headerLines := 3
	if len(m.availableViewOptions()) > 0 {
		headerLines++
	}
	if len(m.availableStateOptions()) > 0 {
		headerLines++
	}
	if m.statusMessage != "" {
		headerLines++
	}
	return headerLines + advancedRows + 8
}

func (m NewSessionOverlay) browserPaneHeight(advancedRows int) int {
	target := browseMinListHeight * 3
	if m.height > 0 {
		target = maxInt(browseMinListHeight, (m.height*browseHeightNumerator+browseHeightDenom-1)/browseHeightDenom)
	}
	if m.height <= 0 {
		return target
	}
	maxHeight := m.height - m.browserChromeLines(advancedRows)
	if maxHeight < 1 {
		return 1
	}
	return maxInt(1, minInt(target, maxHeight))
}

func (m NewSessionOverlay) browserPaneWidths(renderWidth int) (int, int) {
	available := maxInt(1, renderWidth-1)
	if renderWidth <= browseMinPaneWidth+detailMinPaneWidth+1 {
		leftWidth := maxInt(1, available/2)
		rightWidth := maxInt(1, available-leftWidth)
		return leftWidth, rightWidth
	}

	leftWidth := maxInt(browseMinPaneWidth, renderWidth*2/5)
	rightWidth := maxInt(detailMinPaneWidth, available-leftWidth)
	if leftWidth+rightWidth > available {
		rightWidth = maxInt(1, available-leftWidth)
	}
	if rightWidth < detailMinPaneWidth {
		rightWidth = detailMinPaneWidth
		leftWidth = maxInt(1, available-rightWidth)
	}
	return leftWidth, rightWidth
}

func (m *NewSessionOverlay) syncDetailViewport(forceTop bool) {
	if m.showManual {
		return
	}
	contentWidth := m.renderContentWidth()
	m.resizeInputs(contentWidth)
	advancedRows := len(m.advancedFilterRows())
	paneHeight := m.browserPaneHeight(advancedRows)
	_, rightWidth := m.browserPaneWidths(contentWidth)
	viewportWidth := maxInt(1, rightWidth-paneHorizontalFrame)
	viewportHeight := maxInt(1, paneHeight-paneVerticalFrame-2)
	m.detailViewport.Width = viewportWidth
	m.detailViewport.Height = viewportHeight

	item, ok := m.currentListItem()
	if !ok {
		content := lipgloss.NewStyle().Foreground(lipgloss.Color("#6b7280")).Render(
			"No work item selected yet.\n\nUse ↑/↓ to browse results, → to focus details, and Ctrl+N to create a manual item.")
		m.detailViewport.SetContent(ansi.Hardwrap(content, viewportWidth, true))
		m.detailViewport.GotoTop()
		m.detailItemID = ""
		m.detailWidth = viewportWidth
		return
	}
	if !forceTop && item.ID == m.detailItemID && viewportWidth == m.detailWidth {
		return
	}
	content := ansi.Hardwrap(renderMarkdownDocument(detailMarkdown(item), viewportWidth), viewportWidth, true)
	m.detailViewport.SetContent(content)
	if forceTop || item.ID != m.detailItemID || viewportWidth != m.detailWidth {
		m.detailViewport.GotoTop()
	}
	m.detailItemID = item.ID
	m.detailWidth = viewportWidth
}

func detailMarkdown(item adapter.ListItem) string {
	title := item.Title
	if item.Identifier != "" {
		title = item.Identifier + " · " + item.Title
	}
	meta := make([]string, 0, 6)
	if item.Provider != "" {
		meta = append(meta, "- **Provider:** "+strings.Title(item.Provider))
	}
	if item.State != "" {
		meta = append(meta, "- **State:** "+item.State)
	}
	if item.ContainerRef != "" {
		meta = append(meta, "- **Container:** "+item.ContainerRef)
	}
	if len(item.Labels) > 0 {
		meta = append(meta, "- **Labels:** "+strings.Join(item.Labels, ", "))
	}
	if !item.UpdatedAt.IsZero() {
		meta = append(meta, "- **Updated:** "+item.UpdatedAt.Local().Format("2006-01-02 15:04"))
	}
	if item.URL != "" {
		meta = append(meta, "- **Link:** [Open in browser]("+item.URL+")")
	}

	var b strings.Builder
	b.WriteString("# ")
	b.WriteString(title)
	b.WriteString("\n\n")
	if len(meta) > 0 {
		b.WriteString(strings.Join(meta, "\n"))
		b.WriteString("\n\n")
	}
	if strings.TrimSpace(item.Description) == "" {
		b.WriteString("_No description provided._")
	} else {
		b.WriteString(item.Description)
	}
	return b.String()
}

func cloneBrowsePages(src map[string]browsePageState) map[string]browsePageState {
	if len(src) == 0 {
		return make(map[string]browsePageState)
	}
	dst := make(map[string]browsePageState, len(src))
	for key, page := range src {
		copied := page
		copied.Items = append([]adapter.ListItem(nil), page.Items...)
		dst[key] = copied
	}
	return dst
}

func normalizePageItems(items []adapter.ListItem, provider string) []adapter.ListItem {
	normalized := make([]adapter.ListItem, len(items))
	for i, item := range items {
		normalized[i] = item
		if normalized[i].Provider == "" {
			normalized[i].Provider = provider
		}
	}
	return normalized
}

func appendUniqueItems(existing []adapter.ListItem, fresh []adapter.ListItem) []adapter.ListItem {
	seen := make(map[string]struct{}, len(existing))
	merged := append([]adapter.ListItem(nil), existing...)
	for _, item := range existing {
		seen[item.ID] = struct{}{}
	}
	for _, item := range fresh {
		if _, ok := seen[item.ID]; ok {
			continue
		}
		seen[item.ID] = struct{}{}
		merged = append(merged, item)
	}
	return merged
}

func flattenBrowsePages(pages map[string]browsePageState) []adapter.ListItem {
	merged := make([]adapter.ListItem, 0)
	for _, page := range pages {
		merged = append(merged, page.Items...)
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
	return merged
}

func anyPageHasMore(pages map[string]browsePageState) bool {
	for _, page := range pages {
		if page.HasMore {
			return true
		}
	}
	return false
}

func (m *NewSessionOverlay) pruneSelectedIDs() {
	if len(m.selectedIDs) == 0 {
		return
	}
	valid := make(map[string]struct{}, len(m.allItems))
	for _, item := range m.allItems {
		valid[item.ID] = struct{}{}
	}
	for id := range m.selectedIDs {
		if _, ok := valid[id]; !ok {
			delete(m.selectedIDs, id)
		}
	}
}

func (m NewSessionOverlay) currentListItem() (adapter.ListItem, bool) {
	if len(m.allItems) == 0 {
		return adapter.ListItem{}, false
	}
	if selected, ok := m.issueList.SelectedItem().(selectableItem); ok {
		return selected.item, true
	}
	index := m.issueList.Index()
	if index >= 0 && index < len(m.allItems) {
		return m.allItems[index], true
	}
	return m.allItems[0], true
}

func (m *NewSessionOverlay) maybeLoadMore() tea.Cmd {
	if m.browseFocus != browseFocusList || m.loading || !m.hasMore || len(m.allItems) == 0 {
		return nil
	}
	threshold := maxInt(0, len(m.allItems)-minInt(infiniteScrollThreshold, len(m.allItems)))
	if m.issueList.Index() < threshold {
		return nil
	}
	return m.loadMoreItems()
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
func (m NewSessionOverlay) loadItemsCmd(mode browseLoadMode, requestID int) tea.Cmd {
	adapters := m.adaptersForProvider()
	if len(adapters) == 0 {
		return func() tea.Msg {
			return issueListLoadedMsg{requestID: requestID, pages: make(map[string]browsePageState)}
		}
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
	pages := cloneBrowsePages(m.browsePages)
	if mode == browseLoadReset {
		pages = make(map[string]browsePageState, len(adapters))
	}
	return func() tea.Msg {
		nextPages := cloneBrowsePages(pages)
		for _, a := range adapters {
			caps := a.Capabilities()
			filterCaps, ok := caps.BrowseFilters[scope]
			if !ok {
				delete(nextPages, a.Name())
				continue
			}
			page := nextPages[a.Name()]
			opts := adapter.ListOpts{
				WorkspaceID: m.workspaceID,
				Provider:    provider,
				Scope:       scope,
				Search:      search,
				Limit:       browsePageSize,
				View:        view,
				State:       state,
				Labels:      labels,
				Owner:       owner,
				Repo:        repo,
				Group:       group,
				TeamID:      team,
			}
			if mode == browseLoadAppend {
				if !page.HasMore {
					continue
				}
				switch {
				case filterCaps.SupportsCursor:
					if page.NextCursor == "" {
						continue
					}
					opts.Cursor = page.NextCursor
				case filterCaps.SupportsOffset:
					opts.Offset = page.Offset
				default:
					continue
				}
			}
			result, err := a.ListSelectable(context.Background(), opts)
			if err != nil {
				return ErrMsg{Err: err}
			}
			items := normalizePageItems(result.Items, a.Name())
			if mode == browseLoadAppend {
				page.Items = appendUniqueItems(page.Items, items)
			} else {
				page.Items = items
			}
			if filterCaps.SupportsOffset {
				if mode == browseLoadAppend {
					page.Offset += browsePageSize
				} else {
					page.Offset = browsePageSize
				}
			} else {
				page.Offset = 0
			}
			if filterCaps.SupportsCursor {
				page.NextCursor = result.NextCursor
			} else {
				page.NextCursor = ""
			}
			page.HasMore = result.HasMore
			nextPages[a.Name()] = page
		}
		return issueListLoadedMsg{requestID: requestID, pages: nextPages}
	}
}

// issueListLoadedMsg is an internal msg carrying fetched list items.
type issueListLoadedMsg struct {
	requestID int
	pages     map[string]browsePageState
}

// Update handles incoming messages for the overlay.
func (m NewSessionOverlay) Update(msg tea.Msg) (NewSessionOverlay, tea.Cmd) {
	defer m.syncDetailViewport(false)

	var cmds []tea.Cmd
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case issueListLoadedMsg:
		if msg.requestID != 0 && msg.requestID != m.requestSeq {
			break
		}
		m.loading = false
		m.browsePages = cloneBrowsePages(msg.pages)
		m.allItems = flattenBrowsePages(m.browsePages)
		m.hasMore = anyPageHasMore(m.browsePages)
		m.pruneSelectedIDs()
		items := make([]list.Item, len(m.allItems))
		for i, it := range m.allItems {
			items[i] = selectableItem{item: it}
		}
		m.issueList.SetItems(items)
		m.normalizeBrowseFocus()
		m.syncDetailViewport(true)

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
					m.setBrowseControlFocus(browseControlSearch)
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
			if cmd = m.cycleProvider(1); cmd != nil {
				cmds = append(cmds, cmd)
			}
		case "shift+tab", "backtab":
			if cmd = m.cycleProvider(-1); cmd != nil {
				cmds = append(cmds, cmd)
			}
		case "ctrl+s":
			if cmd = m.cycleScope(1); cmd != nil {
				cmds = append(cmds, cmd)
			}
		case "ctrl+v":
			if cmd = m.cycleView(1); cmd != nil {
				cmds = append(cmds, cmd)
			}
		case "ctrl+t":
			if cmd = m.cycleState(1); cmd != nil {
				cmds = append(cmds, cmd)
			}
		case "ctrl+n":
			m.showManual = true
			m.blurBrowseInputs()
			m.manualTitle.Focus()
			m.manualFocus = 0
		case "up":
			if m.browseFocus == browseFocusDetails {
				m.detailViewport, cmd = m.detailViewport.Update(msg)
				cmds = append(cmds, cmd)
				break
			}
			if m.moveBrowseFocus(-1) {
				break
			}
			m.issueList, cmd = m.issueList.Update(msg)
			cmds = append(cmds, cmd)
		case "down":
			if m.browseFocus == browseFocusDetails {
				m.detailViewport, cmd = m.detailViewport.Update(msg)
				cmds = append(cmds, cmd)
				break
			}
			if m.moveBrowseFocus(1) {
				break
			}
			m.issueList, cmd = m.issueList.Update(msg)
			cmds = append(cmds, cmd)
			if cmd = m.maybeLoadMore(); cmd != nil {
				cmds = append(cmds, cmd)
			}
		case "pgup":
			if m.browseFocus == browseFocusDetails {
				m.detailViewport, cmd = m.detailViewport.Update(msg)
				cmds = append(cmds, cmd)
				break
			}
			if m.browseFocus == browseFocusList {
				m.issueList, cmd = m.issueList.Update(msg)
				cmds = append(cmds, cmd)
			}
		case "pgdown":
			if m.browseFocus == browseFocusDetails {
				m.detailViewport, cmd = m.detailViewport.Update(msg)
				cmds = append(cmds, cmd)
				break
			}
			if m.browseFocus == browseFocusList {
				m.issueList, cmd = m.issueList.Update(msg)
				cmds = append(cmds, cmd)
				if cmd = m.maybeLoadMore(); cmd != nil {
					cmds = append(cmds, cmd)
				}
			}
		case "left":
			if m.browseFocus == browseFocusDetails {
				m.setBrowseListFocus()
				break
			}
			if m.browseFocus == browseFocusControls {
				switch m.browseControl {
				case browseControlSource:
					if cmd = m.cycleProvider(-1); cmd != nil {
						cmds = append(cmds, cmd)
					}
				case browseControlScope:
					if cmd = m.cycleScope(-1); cmd != nil {
						cmds = append(cmds, cmd)
					}
				case browseControlView:
					if cmd = m.cycleView(-1); cmd != nil {
						cmds = append(cmds, cmd)
					}
				case browseControlState:
					if cmd = m.cycleState(-1); cmd != nil {
						cmds = append(cmds, cmd)
					}
				default:
					cmds = append(cmds, m.updateFocusedBrowseInput(msg)...)
				}
			}
		case "right":
			if m.browseFocus == browseFocusList {
				m.setBrowseDetailsFocus()
				break
			}
			if m.browseFocus == browseFocusControls {
				switch m.browseControl {
				case browseControlSource:
					if cmd = m.cycleProvider(1); cmd != nil {
						cmds = append(cmds, cmd)
					}
				case browseControlScope:
					if cmd = m.cycleScope(1); cmd != nil {
						cmds = append(cmds, cmd)
					}
				case browseControlView:
					if cmd = m.cycleView(1); cmd != nil {
						cmds = append(cmds, cmd)
					}
				case browseControlState:
					if cmd = m.cycleState(1); cmd != nil {
						cmds = append(cmds, cmd)
					}
				default:
					cmds = append(cmds, m.updateFocusedBrowseInput(msg)...)
				}
			}
		case "enter":
			if len(m.selectedIDs) == 0 {
				if item, ok := m.currentListItem(); ok {
					if err := m.toggleSelection(item); err != nil {
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
			if item, ok := m.currentListItem(); ok {
				if err := m.toggleSelection(item); err != nil {
					return m, func() tea.Msg { return ErrMsg{Err: err} }
				}
			}
		default:
			switch m.browseFocus {
			case browseFocusList:
				m.issueList, cmd = m.issueList.Update(msg)
				cmds = append(cmds, cmd)
				if cmd = m.maybeLoadMore(); cmd != nil {
					cmds = append(cmds, cmd)
				}
			case browseFocusDetails:
				m.detailViewport, cmd = m.detailViewport.Update(msg)
				cmds = append(cmds, cmd)
			default:
				cmds = append(cmds, m.updateFocusedBrowseInput(msg)...)
			}
		}
	}

	return m, tea.Batch(cmds...)
}

// View renders the overlay, or empty string when inactive.
func (m *NewSessionOverlay) View() string {
	if !m.active {
		return ""
	}

	boxWidth := m.renderWidth()
	contentWidth := m.renderContentWidth()
	m.resizeInputs(contentWidth)
	m.syncDetailViewport(false)

	providerLabels := make([]string, 0, len(m.activeProviderOptions()))
	for _, option := range m.activeProviderOptions() {
		if option.Key == m.currentProvider() {
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
		m.controlLabel("Source: ", browseControlSource) + strings.Join(providerLabels, "  "),
		m.controlLabel("Scope:  ", browseControlScope) + strings.Join(scopeLabels, "  "),
	}
	if len(viewLabels) > 0 {
		header = append(header, m.controlLabel("View:   ", browseControlView)+strings.Join(viewLabels, "  "))
	}
	if len(stateLabels) > 0 {
		header = append(header, m.controlLabel("State:  ", browseControlState)+strings.Join(stateLabels, "  "))
	}
	if m.statusMessage != "" {
		header = append(header, lipgloss.NewStyle().Foreground(lipgloss.Color("#f59e0b")).Render(m.statusMessage))
	}

	var body string
	if m.showManual {
		body = m.manualView(contentWidth)
	} else {
		body = m.browserView(contentWidth)
	}

	content := strings.Join(header, "\n") + "\n\n" + body
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#2d2d44")).
		Background(lipgloss.Color(overlayBackgroundColor)).
		Padding(0, 2).
		Width(boxWidth)
	return boxStyle.Render(content)
}

func (m *NewSessionOverlay) browserView(w int) string {
	lines := make([]string, 0, 6)
	advancedRows := m.advancedFilterRows()
	filterRow := m.controlLabel("Search: ", browseControlSearch) + m.filterInput.View()
	lines = append(lines, filterRow)
	if len(advancedRows) > 0 {
		lines = append(lines, advancedRows...)
	}
	lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("#2d2d44")).Width(w).Render(strings.Repeat("─", maxInt(1, w))))

	paneHeight := m.browserPaneHeight(len(advancedRows))
	leftWidth, rightWidth := m.browserPaneWidths(w)
	leftInnerWidth := maxInt(1, leftWidth-paneHorizontalFrame)
	rightInnerWidth := maxInt(1, rightWidth-paneHorizontalFrame)
	listHeight := maxInt(1, paneHeight-paneVerticalFrame)
	m.issueList.SetWidth(leftInnerWidth)
	m.issueList.SetHeight(listHeight)
	m.syncDetailViewport(false)

	leftBorder := lipgloss.Color("#2d2d44")
	if m.browseFocus != browseFocusDetails {
		leftBorder = lipgloss.Color("#60a5fa")
	}
	leftContent := m.issueList.View()
	if m.loading && len(m.allItems) == 0 {
		leftContent = lipgloss.NewStyle().Foreground(lipgloss.Color("#6b7280")).Render("Loading…")
	}
	if m.loading && len(m.allItems) > 0 {
		leftContent += "\n" + lipgloss.NewStyle().Foreground(lipgloss.Color("#6b7280")).Render("Loading more…")
	}
	leftPane := lipgloss.NewStyle().
		Width(leftInnerWidth).
		Height(listHeight).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(leftBorder).
		Padding(0, 1).
		Render(leftContent)

	rightBorder := lipgloss.Color("#2d2d44")
	if m.browseFocus == browseFocusDetails {
		rightBorder = lipgloss.Color("#60a5fa")
	}
	detailHeader := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#f0f0f0")).Render("Details")
	detailDivider := lipgloss.NewStyle().Foreground(lipgloss.Color("#2d2d44")).Width(rightInnerWidth).Render(strings.Repeat("─", maxInt(1, rightInnerWidth)))
	rightContent := strings.Join([]string{detailHeader, detailDivider, m.detailViewport.View()}, "\n")
	rightPane := lipgloss.NewStyle().
		Width(rightInnerWidth).
		Height(listHeight).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(rightBorder).
		Padding(0, 1).
		Render(rightContent)

	panes := lipgloss.JoinHorizontal(lipgloss.Top, leftPane, " ", rightPane)
	hints := lipgloss.NewStyle().Foreground(lipgloss.Color("#6b7280")).Render(truncate(m.browserHintText(), maxInt(1, w)))
	lines = append(lines, panes, hints)
	return strings.Join(lines, "\n")
}

func (m NewSessionOverlay) browserHintText() string {
	parts := []string{"[↑/↓] Focus/List/Scroll", "[←/→] List⇄Details", "[PgUp/PgDn] Scroll", "[Ctrl+N] Manual"}
	if len(m.availableStateOptions()) > 0 {
		parts = append(parts, "[Ctrl+T] State")
	}
	if len(m.availableViewOptions()) > 0 {
		parts = append(parts, "[Ctrl+V] View")
	}
	parts = append(parts, "[Enter] Start", "[Space] Select", "[Tab] Source", "[Ctrl+S] Scope", "[Esc] Cancel", "Multi-select: same provider only")
	return strings.Join(parts, "  ")
}

func (m NewSessionOverlay) manualView(width int) string {
	titleLabel := lipgloss.NewStyle().Foreground(lipgloss.Color("#b0b0b0")).Render("Title:       ")
	descLabel := lipgloss.NewStyle().Foreground(lipgloss.Color("#b0b0b0")).Render("Description: ")
	hints := lipgloss.NewStyle().Foreground(lipgloss.Color("#6b7280")).Render(
		truncate("[Tab] Next field  [Enter] Start  [Tab on desc] Return to browser  [Esc] Cancel", maxInt(1, width)))
	return strings.Join([]string{
		titleLabel + m.manualTitle.View(),
		descLabel + m.manualDesc.View(),
		"",
		hints,
	}, "\n")
}

// SetSize stores the available terminal dimensions for responsive rendering.
func (m *NewSessionOverlay) SetSize(w, h int) {
	m.width = w
	m.height = h
	m.syncDetailViewport(false)
}

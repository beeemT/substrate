package views

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/tui/components"
	"github.com/beeemT/substrate/internal/tui/styles"
)

type providerOption struct {
	Key   string
	Label string
}

var providerOptions = []providerOption{
	{Key: viewFilterAll, Label: "All"},
	{Key: "linear", Label: "Linear"},
	{Key: "github", Label: "GitHub"},
	{Key: "gitlab", Label: "GitLab"},
	{Key: providerSentry, Label: "Sentry"},
}

var scopeOptions = []domain.SelectionScope{
	domain.ScopeIssues,
	domain.ScopeProjects,
	domain.ScopeInitiatives,
}

const (
	viewFilterAll  = "all"
	providerSentry = "sentry"
	providerManual = "manual"
	panelLeft      = "left"
	panelRight     = "right"

	browseMaxOverlayWidth   = 0
	browseHeightNumerator   = 4
	browseHeightDenom       = 5
	browseMinListHeight     = 6
	browseMinPaneWidth      = 36
	detailMinPaneWidth      = 44
	browsePageSize          = 50
	infiniteScrollThreshold = 5
)

var browseSizingSpec = components.SplitOverlaySizingSpec{
	MaxOverlayWidth:   browseMaxOverlayWidth,
	LeftMinWidth:      browseMinPaneWidth,
	RightMinWidth:     detailMinPaneWidth,
	LeftWeight:        2,
	RightWeight:       3,
	MinBodyHeight:     browseMinListHeight,
	DefaultBodyHeight: browseMinListHeight * 3,
	HeightRatioNum:    browseHeightNumerator,
	HeightRatioDen:    browseHeightDenom,
	InputWidthOffset:  20,
}

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

const browseDebounceDelay = 200 * time.Millisecond

// browseDebounceMsg is delivered by a tea.Tick scheduled when the user types
// in a browse control. Only the tick matching the current browseDebounceSeq
// fires a network reload; earlier ticks are silently dropped.
type browseDebounceMsg struct{ seq int }

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
	if i.selected {
		prefix = "✓ " + prefix
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
type NewSessionOverlay struct { //nolint:recvcheck // Bubble Tea: Update returns value, View on value receiver
	adapters          []adapter.WorkItemAdapter
	browseAdapters    []adapter.WorkItemAdapter
	workspaceID       string
	providerIndex     int
	scopeIndex        int
	viewIndex         int
	stateIndex        int
	filterInput       textinput.Model
	labelsInput       textinput.Model
	ownerInput        textinput.Model
	repoInput         textinput.Model
	groupInput        textinput.Model
	teamInput         textinput.Model
	issueList         list.Model
	allItems          []adapter.ListItem
	browsePages       map[string]browsePageState
	selectedIDs       map[string]bool
	loading           bool
	hasMore           bool
	manualTitle       textinput.Model
	manualDesc        textarea.Model
	manualFocus       int
	showManual        bool
	browseFocus       browseFocusArea
	browseControl     browseControl
	detailViewport    viewport.Model
	detailItemID      string
	detailWidth       int
	requestSeq        int
	browseDebounceSeq int
	styles            styles.Styles
	width             int
	height            int
	active            bool
	openBrowserCmd    func(string) tea.Cmd
	statusMessage     string
}

// NewNewSessionOverlay constructs a NewSessionOverlay with sane defaults.
func NewNewSessionOverlay(adapters []adapter.WorkItemAdapter, workspaceID string, st styles.Styles) NewSessionOverlay {
	fi := components.NewTextInput()
	fi.Placeholder = "Search work items…"
	fi.CharLimit = 200

	labels := components.NewTextInput()
	labels.Placeholder = "Labels (comma-separated)…"
	labels.CharLimit = 200

	owner := components.NewTextInput()
	owner.Placeholder = "Owner…"
	owner.CharLimit = 200

	repo := components.NewTextInput()
	repo.Placeholder = "Repository / Project…"
	repo.CharLimit = 200

	group := components.NewTextInput()
	group.Placeholder = "Group…"
	group.CharLimit = 200

	team := components.NewTextInput()
	team.Placeholder = "Team…"
	team.CharLimit = 200

	mt := components.NewTextInput()
	mt.Placeholder = "Work item title…"
	mt.CharLimit = 200

	md := components.NewTextArea()
	md.Placeholder = "Description (optional)…"
	md.SetWidth(60)
	md.SetHeight(3)
	md.CharLimit = 2000

	delegate := list.NewDefaultDelegate()
	delegate.ShowDescription = true
	il := list.New([]list.Item{}, delegate, 60, 10)
	il.Title = "Work Items"
	il.SetShowTitle(false)
	il.SetShowStatusBar(false)
	il.SetShowPagination(false)
	il.SetFilteringEnabled(true)
	il.SetShowHelp(false)
	il = components.ApplyOverlayListStyles(il, st)

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
		openBrowserCmd: OpenBrowserCmd,
	}
}

// Open activates the overlay and sets initial focus.
func (m *NewSessionOverlay) Open() {
	m.active = true
	m.showManual = false
	m.normalizeSelectionOptions()
	m.syncBrowseListState(true)
	m.setBrowseControlFocus(browseControlSearch)
	m.syncDetailViewport(true)
}

func (m *NewSessionOverlay) syncBrowseListState(resetCursor bool) {
	m.refreshBrowseListItems()
	if len(m.allItems) == 0 {
		m.issueList.ResetSelected()
		return
	}
	if resetCursor || m.issueList.Index() < 0 || m.issueList.Index() >= len(m.allItems) {
		m.issueList.Select(0)
		return
	}
	m.issueList.Select(m.issueList.Index())
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
	m.issueList.ResetSelected()
	m.issueList.SetItems(nil)
	m.loading = false
	m.hasMore = false
	m.detailViewport.SetContent("")
	m.detailViewport.YOffset = 0
	m.detailItemID = ""
	m.detailWidth = 0
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
		m.refreshBrowseListItems()
		return nil
	}
	if provider != "" && provider != item.Provider {
		return errors.New("multi-select must stay within one provider")
	}
	m.selectedIDs[item.ID] = true
	m.refreshBrowseListItems()
	return nil
}

func (m NewSessionOverlay) currentProvider() string {
	if m.providerIndex < 0 || m.providerIndex >= len(providerOptions) {
		return viewFilterAll
	}
	return providerOptions[m.providerIndex].Key
}

func crossesSentryProjectFilterBoundary(fromProvider, toProvider string) bool {
	return (fromProvider == providerSentry) != (toProvider == providerSentry)
}

func (m NewSessionOverlay) activeProviderOptions() []providerOption {
	indices := m.activeProviderIndices()
	options := make([]providerOption, 0, len(indices))
	for _, index := range indices {
		options = append(options, providerOptions[index])
	}
	return options
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
	if provider == viewFilterAll && m.currentScope() == domain.ScopeIssues {
		return append([]adapter.WorkItemAdapter(nil), m.browseAdapters...)
	}
	filtered := make([]adapter.WorkItemAdapter, 0, len(m.browseAdapters))
	for _, a := range m.browseAdapters {
		if provider == viewFilterAll || a.Name() == provider {
			filtered = append(filtered, a)
		}
	}
	return filtered
}

func (m NewSessionOverlay) availableScopes() []domain.SelectionScope {
	provider := m.currentProvider()
	if provider == viewFilterAll {
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
		if !slices.Contains(caps.BrowseScopes, scope) {
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

func (m *NewSessionOverlay) normalizeProviderSelection() {
	indices := m.activeProviderIndices()
	if slices.Contains(indices, m.providerIndex) {
		return
	}
	m.providerIndex = indices[0]
}

func (m *NewSessionOverlay) normalizeSelectionOptions() {
	currentScope := m.currentScope()
	currentView := m.currentView()
	currentState := m.currentState()
	m.normalizeProviderSelection()
	availableScopes := m.availableScopes()
	if len(availableScopes) > 0 {
		m.scopeIndex = scopeOptionIndex(availableScopes[0])
		for _, scope := range availableScopes {
			if scope == currentScope {
				m.scopeIndex = scopeOptionIndex(scope)
				break
			}
		}
	}
	availableViews := m.availableViewOptions()
	if len(availableViews) == 0 {
		m.viewIndex = 0
	} else {
		m.viewIndex = 0
		for i, view := range availableViews {
			if view == currentView {
				m.viewIndex = i
				break
			}
		}
	}
	availableStates := m.availableStateOptions()
	if len(availableStates) == 0 {
		m.stateIndex = 0
	} else {
		m.stateIndex = 0
		for i, state := range availableStates {
			if state == currentState {
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
		label := cases.Title(language.English).String(provider)
		if provider == viewFilterAll || provider == "" {
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
	style := m.styles.Label
	if m.isBrowseControlFocused(control) {
		style = m.styles.Title
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
	if !slices.Contains(controls, control) {
		control = browseControlSearch
		if !slices.Contains(controls, control) {
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
	if m.currentScope() == domain.ScopeIssues {
		indices = append(indices, providerOptionIndex(viewFilterAll))
	}
	for i, option := range providerOptions {
		if option.Key == viewFilterAll {
			continue
		}
		providerAdapter := m.adapterForName(option.Key)
		if providerAdapter == nil {
			continue
		}
		if !m.scopeSupportedByAdapters(m.currentScope(), []adapter.WorkItemAdapter{providerAdapter}) {
			continue
		}
		indices = append(indices, i)
	}
	if len(indices) == 0 {
		return []int{providerOptionIndex(viewFilterAll)}
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

func (m *NewSessionOverlay) resetBrowseState() {
	m.providerIndex = providerOptionIndex(viewFilterAll)
	m.scopeIndex = scopeOptionIndex(domain.ScopeIssues)
	m.viewIndex = 0
	m.stateIndex = 0
	m.filterInput.SetValue("")
	m.labelsInput.SetValue("")
	m.ownerInput.SetValue("")
	m.repoInput.SetValue("")
	m.groupInput.SetValue("")
	m.teamInput.SetValue("")
	m.browsePages = make(map[string]browsePageState)
	m.selectedIDs = make(map[string]bool)
	m.allItems = nil
	m.loading = false
	m.hasMore = false
	m.detailViewport.SetContent("")
	m.detailViewport.YOffset = 0
	m.detailItemID = ""
	m.detailWidth = 0
	m.normalizeSelectionOptions()
	m.syncBrowseListState(true)
	m.setBrowseControlFocus(browseControlSearch)
	m.syncDetailViewport(true)
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
	currentProvider := m.currentProvider()
	next := (current + delta + len(indices)) % len(indices)
	nextProvider := providerOptions[indices[next]].Key
	if crossesSentryProjectFilterBoundary(currentProvider, nextProvider) {
		m.repoInput.SetValue("")
	}
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
		m.browseDebounceSeq++
		seq := m.browseDebounceSeq
		cmds = append(cmds, tea.Tick(browseDebounceDelay, func(time.Time) tea.Msg {
			return browseDebounceMsg{seq: seq}
		}))
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

func (m *NewSessionOverlay) resizeInputs(inputWidth int) {
	inputWidth = maxInt(1, inputWidth)
	m.filterInput.Width = inputWidth
	m.labelsInput.Width = inputWidth
	m.ownerInput.Width = inputWidth
	m.repoInput.Width = inputWidth
	m.groupInput.Width = inputWidth
	m.teamInput.Width = inputWidth
	m.manualTitle.Width = inputWidth
	m.manualDesc.SetWidth(inputWidth)
}

func countAdvancedBrowseFilterRows(filters adapter.BrowseFilterCapabilities) int {
	rows := 0
	if filters.SupportsLabels {
		rows++
	}
	if filters.SupportsOwner {
		rows++
	}
	if filters.SupportsRepo {
		rows++
	}
	if filters.SupportsGroup {
		rows++
	}
	if filters.SupportsTeam {
		rows++
	}
	return rows
}

type browseChromeBudget struct {
	hasViews         bool
	hasStates        bool
	hasStatusMessage bool
	advancedRows     int
}

func (m NewSessionOverlay) stableBrowseChromeBudget() browseChromeBudget {
	budget := browseChromeBudget{}
	for _, browseAdapter := range m.browseAdapters {
		caps := browseAdapter.Capabilities()
		for _, scope := range caps.BrowseScopes {
			filters, ok := caps.BrowseFilters[scope]
			if !ok {
				continue
			}
			if len(filters.Views) > 0 {
				budget.hasViews = true
			}
			if len(filters.States) > 0 {
				budget.hasStates = true
			}
			if scope == domain.ScopeIssues && len(filters.Views) == 0 && filters.SupportsTeam {
				budget.hasStatusMessage = true
			}
			if rows := countAdvancedBrowseFilterRows(filters); rows > budget.advancedRows {
				budget.advancedRows = rows
			}
		}
	}
	return budget
}

func (m NewSessionOverlay) browserChromeLines(renderWidth int) int {
	budget := m.stableBrowseChromeBudget()
	headerLines := 3
	if budget.hasViews {
		headerLines++
	}
	if budget.hasStates {
		headerLines++
	}
	if budget.hasStatusMessage {
		headerLines++
	}
	return headerLines + budget.advancedRows + 5 + browserHintLineCountForParts(browserHintParts(budget.hasStates, budget.hasViews), renderWidth)
}

func (m NewSessionOverlay) browserLayout() components.SplitOverlayLayout {
	baseLayout := components.ComputeSplitOverlayLayout(m.width, m.height, 0, browseSizingSpec)
	chromeLines := m.browserChromeLines(maxInt(1, baseLayout.ContentWidth-4))
	return components.ComputeSplitOverlayLayout(m.width, m.height, chromeLines, browseSizingSpec)
}

func (m *NewSessionOverlay) syncDetailViewport(forceTop bool) {
	if m.showManual {
		return
	}
	m.syncDetailViewportWithLayout(m.browserLayout(), forceTop)
}

func (m *NewSessionOverlay) syncDetailViewportWithLayout(layout components.SplitOverlayLayout, forceTop bool) {
	if m.showManual {
		return
	}
	m.resizeInputs(layout.InputWidth)
	viewportWidth := layout.ViewportWidth
	viewportHeight := layout.ViewportHeight
	m.detailViewport.Width = viewportWidth
	m.detailViewport.Height = viewportHeight

	item, ok := m.currentListItem()
	if !ok {
		content := m.styles.Muted.Render(
			"No work item selected yet.\n\nUse the browse list to review results, switch to details for more context, or press Ctrl+N to create a manual item.")
		m.detailViewport.SetContent(ansi.Hardwrap(content, viewportWidth, true))
		m.detailViewport.GotoTop()
		m.detailItemID = ""
		m.detailWidth = viewportWidth
		return
	}
	if !forceTop && item.ID == m.detailItemID && viewportWidth == m.detailWidth {
		return
	}
	content := renderDetailContent(m.styles, item, viewportWidth)
	m.detailViewport.SetContent(content)
	if forceTop || item.ID != m.detailItemID || viewportWidth != m.detailWidth {
		m.detailViewport.GotoTop()
	}
	m.detailItemID = item.ID
	m.detailWidth = viewportWidth
}

func renderDetailContent(st styles.Styles, item adapter.ListItem, width int) string {
	if width < 20 {
		width = 20
	}

	metaInnerWidth := components.CalloutInnerWidth(st, width)
	sections := []string{
		st.SectionLabel.Render("Metadata"),
		components.RenderCallout(st, components.CalloutSpec{
			Body:  renderDetailMetadata(st, item, metaInnerWidth),
			Width: width,
		}),
		st.SectionLabel.Render("Description"),
		renderMarkdownDocument(detailMarkdown(item), width),
	}

	return strings.Join(sections, "\n\n")
}

func renderDetailMetadata(st styles.Styles, item adapter.ListItem, width int) string {
	labelStyle := st.SectionLabel
	valueStyle := st.SettingsText
	linkStyle := st.Link
	mutedStyle := st.Muted

	rows := make([]string, 0, 6)
	add := func(label, value string, style lipgloss.Style) {
		if strings.TrimSpace(value) == "" {
			return
		}
		line := labelStyle.Render(label+": ") + style.Render(value)
		rows = append(rows, ansi.Hardwrap(line, width, true))
	}

	add("Provider", detailProviderLabel(item.Provider), valueStyle)
	add("State", item.State, valueStyle)
	add("Container", item.ContainerRef, valueStyle)
	if len(item.Labels) > 0 {
		add("Labels", strings.Join(item.Labels, ", "), valueStyle)
	}
	if !item.UpdatedAt.IsZero() {
		add("Updated", item.UpdatedAt.Local().Format("2006-01-02 15:04"), valueStyle)
	}
	add("URL", item.URL, linkStyle)

	if len(rows) == 0 {
		return mutedStyle.Render("No metadata available.")
	}

	return strings.Join(rows, "\n")
}

func detailProviderLabel(provider string) string {
	if provider == "" {
		return ""
	}
	for _, option := range providerOptions {
		if option.Key == provider {
			return option.Label
		}
	}
	return cases.Title(language.English).String(provider)
}

func detailPaneTitle(st styles.Styles, item adapter.ListItem, width int) string {
	availableWidth := maxInt(1, width-st.Title.GetHorizontalFrameSize())
	title := ansi.Truncate(strings.TrimSpace(detailTitle(item)), availableWidth, "…")
	if title == "" {
		return "Details"
	}
	return title
}

func detailTitle(item adapter.ListItem) string {
	title := strings.TrimSpace(item.Title)
	if item.Identifier != "" && title != "" {
		return item.Identifier + " · " + title
	}
	if item.Identifier != "" {
		return item.Identifier
	}
	return title
}

func detailMarkdown(item adapter.ListItem) string {
	trimmed := strings.TrimSpace(item.Description)
	if trimmed == "" {
		return "_No description provided._"
	}
	return trimmed
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
	merged := make([]adapter.ListItem, 0, len(pages)*10)
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

func (m *NewSessionOverlay) refreshBrowseListItems() {
	items := make([]list.Item, len(m.allItems))
	for i, it := range m.allItems {
		items[i] = selectableItem{item: it, selected: m.selectedIDs[it.ID]}
	}
	m.issueList.SetItems(items)
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

func (m NewSessionOverlay) openCurrentItemInBrowserCmd() tea.Cmd {
	item, ok := m.currentListItem()
	if !ok {
		return func() tea.Msg { return ErrMsg{Err: errors.New("no work item selected")} }
	}
	if strings.TrimSpace(item.URL) == "" {
		return func() tea.Msg { return ErrMsg{Err: errors.New("selected work item has no URL")} }
	}
	openBrowserCmd := m.openBrowserCmd
	if openBrowserCmd == nil {
		openBrowserCmd = OpenBrowserCmd
	}
	return openBrowserCmd(item.URL)
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
		var errs []error
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
				slog.Error("adapter list failed",
					"adapter", a.Name(),
					"scope", scope,
					"view", view,
					"error", err,
				)
				errs = append(errs, fmt.Errorf("%s: %w", a.Name(), err))
				continue
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
		return issueListLoadedMsg{requestID: requestID, pages: nextPages, errs: errs}
	}
}

// issueListLoadedMsg is an internal msg carrying fetched list items.
// errs collects per-adapter errors so partial results are still shown and the
// overlay always transitions out of the loading state.
type issueListLoadedMsg struct {
	requestID int
	pages     map[string]browsePageState
	errs      []error
}

// Update handles incoming messages for the overlay.
func (m NewSessionOverlay) Update(msg tea.Msg) (NewSessionOverlay, tea.Cmd) {
	var cmds []tea.Cmd
	var cmd tea.Cmd
	forceDetailTop := false

	switch msg := msg.(type) {
	case browseDebounceMsg:
		if msg.seq == m.browseDebounceSeq {
			cmds = append(cmds, m.reloadItems())
		}

	case issueListLoadedMsg:
		if msg.requestID != 0 && msg.requestID != m.requestSeq {
			break
		}
		m.loading = false
		m.browsePages = cloneBrowsePages(msg.pages)
		m.allItems = flattenBrowsePages(m.browsePages)
		m.hasMore = anyPageHasMore(m.browsePages)
		m.pruneSelectedIDs()
		m.refreshBrowseListItems()
		m.normalizeBrowseFocus()
		forceDetailTop = true
		for _, e := range msg.errs {
			err := e
			cmds = append(cmds, func() tea.Msg { return ErrMsg{Err: err} })
		}

	case tea.MouseMsg:
		if !m.showManual && msg.Action == tea.MouseActionPress {
			switch msg.Button {
			case tea.MouseButtonWheelUp, tea.MouseButtonWheelDown:
				switch m.browseFocus {
				case browseFocusList:
					m.issueList, cmd = m.issueList.Update(msg)
					cmds = append(cmds, cmd)
					if msg.Button == tea.MouseButtonWheelDown {
						if cmd = m.maybeLoadMore(); cmd != nil {
							cmds = append(cmds, cmd)
						}
					}
				case browseFocusDetails:
					m.detailViewport, cmd = m.detailViewport.Update(msg)
					return m, cmd
				}
			}
		}

	case tea.KeyMsg:
		if m.showManual {
			switch msg.String() {
			case keyEsc:
				return m, func() tea.Msg { return CloseOverlayMsg{} }
			case "backtab", keyShiftTab:
				if m.manualFocus == 1 {
					m.manualDesc.Blur()
					m.manualFocus = 0
					m.manualTitle.Focus()
				}
			case keyTab:
				if m.manualFocus == 0 {
					m.manualTitle.Blur()
					m.manualFocus = 1
					m.manualDesc.Focus()
				} else {
					m.showManual = false
					m.manualDesc.Blur()
					m.setBrowseControlFocus(browseControlSearch)
				}
			case keyEnter:
				if m.manualFocus == 1 || m.manualTitle.Value() != "" {
					title := strings.TrimSpace(m.manualTitle.Value())
					if title == "" {
						break
					}
					desc := m.manualDesc.Value()
					for _, a := range m.adapters {
						if a.Name() == providerManual {
							return m, func() tea.Msg { return NewSessionManualMsg{Adapter: a, Title: title, Desc: desc} }
						}
					}
					return m, func() tea.Msg { return ErrMsg{Err: errors.New("no manual adapter configured")} }
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
		case keyEsc:
			return m, func() tea.Msg { return CloseOverlayMsg{} }
		case keyTab:
			if cmd = m.cycleProvider(1); cmd != nil {
				cmds = append(cmds, cmd)
			}
		case keyShiftTab, "backtab":
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
		case "ctrl+r":
			m.resetBrowseState()
			if cmd = m.reloadItems(); cmd != nil {
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
				return m, cmd
			}
			if m.moveBrowseFocus(-1) {
				break
			}
			m.issueList, cmd = m.issueList.Update(msg)
			cmds = append(cmds, cmd)
		case keyDown:
			if m.browseFocus == browseFocusDetails {
				m.detailViewport, cmd = m.detailViewport.Update(msg)
				return m, cmd
			}
			if m.moveBrowseFocus(1) {
				break
			}
			m.issueList, cmd = m.issueList.Update(msg)
			cmds = append(cmds, cmd)
			if cmd = m.maybeLoadMore(); cmd != nil {
				cmds = append(cmds, cmd)
			}
		case keyPgUp:
			if m.browseFocus == browseFocusDetails {
				m.detailViewport, cmd = m.detailViewport.Update(msg)
				return m, cmd
			}
			if m.browseFocus == browseFocusList {
				m.issueList, cmd = m.issueList.Update(msg)
				cmds = append(cmds, cmd)
			}
		case keyPgDown:
			if m.browseFocus == browseFocusDetails {
				m.detailViewport, cmd = m.detailViewport.Update(msg)
				return m, cmd
			}
			if m.browseFocus == browseFocusList {
				m.issueList, cmd = m.issueList.Update(msg)
				cmds = append(cmds, cmd)
				if cmd = m.maybeLoadMore(); cmd != nil {
					cmds = append(cmds, cmd)
				}
			}
		case panelLeft:
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
		case panelRight:
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
		case "ctrl+o":
			return m, m.openCurrentItemInBrowserCmd()
		case keyEnter:
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
						return ErrMsg{Err: errors.New("selected work items must come from exactly one provider")}
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
				if slices.Contains(a.Capabilities().BrowseScopes, m.currentScope()) {
					sel := adapter.Selection{Scope: m.currentScope(), ItemIDs: ids, Metadata: map[string]any{"provider": a.Name()}}
					return m, func() tea.Msg { return NewSessionBrowseMsg{Adapter: a, Selection: sel} }
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
				return m, cmd
			default:
				cmds = append(cmds, m.updateFocusedBrowseInput(msg)...)
			}
		}
	}

	m.syncDetailViewport(forceDetailTop)
	return m, tea.Batch(cmds...)
}

// View renders the overlay, or empty string when inactive.
func (m *NewSessionOverlay) View() string {
	if !m.active {
		return ""
	}

	layout := m.browserLayout()
	renderWidth := maxInt(1, layout.ContentWidth-4)
	m.resizeInputs(layout.InputWidth)
	m.syncDetailViewportWithLayout(layout, false)

	budget := m.stableBrowseChromeBudget()
	activeLabelStyle := m.styles.Accent
	inactiveLabelStyle := m.styles.Hint
	providerLabels := make([]string, 0, len(m.activeProviderOptions()))
	for _, option := range m.activeProviderOptions() {
		if option.Key == m.currentProvider() {
			providerLabels = append(providerLabels, activeLabelStyle.Render("[► "+option.Label+" ◄]"))
		} else {
			providerLabels = append(providerLabels, inactiveLabelStyle.Render(option.Label))
		}
	}

	scopeLabels := make([]string, 0, len(m.availableScopes()))
	for _, option := range m.availableScopes() {
		label := cases.Title(language.English).String(string(option))
		if option == m.currentScope() {
			scopeLabels = append(scopeLabels, activeLabelStyle.Render("["+label+"]"))
		} else {
			scopeLabels = append(scopeLabels, inactiveLabelStyle.Render(label))
		}
	}

	viewLabels := make([]string, 0, len(m.availableViewOptions()))
	for _, option := range m.availableViewOptions() {
		label := strings.ReplaceAll(option, "_", " ")
		if option == m.currentView() {
			viewLabels = append(viewLabels, activeLabelStyle.Render("["+label+"]"))
		} else {
			viewLabels = append(viewLabels, inactiveLabelStyle.Render(label))
		}
	}
	stateLabels := make([]string, 0, len(m.availableStateOptions()))
	for _, option := range m.availableStateOptions() {
		if option == m.currentState() {
			stateLabels = append(stateLabels, activeLabelStyle.Render("["+option+"]"))
		} else {
			stateLabels = append(stateLabels, inactiveLabelStyle.Render(option))
		}
	}
	header := []string{
		m.styles.Title.Render("Browse Work Items"),
		m.controlLabel("Source: ", browseControlSource) + strings.Join(providerLabels, "  "),
		m.controlLabel("Scope:  ", browseControlScope) + strings.Join(scopeLabels, "  "),
	}
	if budget.hasViews {
		if len(viewLabels) > 0 {
			header = append(header, m.controlLabel("View:   ", browseControlView)+strings.Join(viewLabels, "  "))
		} else {
			header = append(header, "")
		}
	}
	if budget.hasStates {
		if len(stateLabels) > 0 {
			header = append(header, m.controlLabel("State:  ", browseControlState)+strings.Join(stateLabels, "  "))
		} else {
			header = append(header, "")
		}
	}
	if budget.hasStatusMessage {
		if m.statusMessage != "" {
			header = append(header, m.styles.Warning.Render(m.statusMessage))
		} else {
			header = append(header, "")
		}
	}

	var body string
	footer := ""
	if m.showManual {
		body = m.manualView(renderWidth)
	} else {
		body = m.browserView(layout)
		footer = m.styles.Hint.Render(m.wrappedBrowserHintText(renderWidth))
	}

	return components.RenderOverlayFrame(m.styles, layout.FrameWidth, components.OverlayFrameSpec{
		HeaderLines: header,
		Body:        body,
		Footer:      footer,
		Focused:     true,
	})
}

func (m *NewSessionOverlay) browserView(layout components.SplitOverlayLayout) string {
	budget := m.stableBrowseChromeBudget()
	lines := make([]string, 0, 5+budget.advancedRows)
	advancedRows := m.advancedFilterRows()
	filterRow := m.controlLabel("Search: ", browseControlSearch) + m.filterInput.View()
	lines = append(lines, filterRow)
	if len(advancedRows) > 0 {
		lines = append(lines, advancedRows...)
	}
	for len(advancedRows) < budget.advancedRows {
		lines = append(lines, "")
		advancedRows = append(advancedRows, "")
	}
	lines = append(lines, components.RenderOverlayDivider(m.styles, maxInt(1, layout.ContentWidth-4)))

	m.issueList.SetWidth(layout.LeftInnerWidth)
	m.issueList.SetHeight(layout.ViewportHeight)
	m.syncDetailViewportWithLayout(layout, false)

	leftContent := m.issueList.View()
	if m.loading && len(m.allItems) == 0 {
		leftContent = lipgloss.NewStyle().Width(layout.LeftInnerWidth).Height(layout.ViewportHeight).Render(m.styles.Muted.Render("Loading…"))
	}
	if m.loading && len(m.allItems) > 0 {
		leftContent += "\n" + m.styles.Muted.Render("Loading more…")
	}

	leftPaneTitle := "Work Items"
	rightPaneTitle := "Details"
	if item, ok := m.currentListItem(); ok {
		rightPaneTitle = detailPaneTitle(m.styles, item, layout.RightInnerWidth)
	}

	panes := components.RenderSplitOverlayBody(m.styles, layout, components.SplitOverlaySpec{
		LeftPane: components.OverlayPaneSpec{
			Title:   leftPaneTitle,
			Body:    leftContent,
			Focused: m.browseFocus != browseFocusDetails,
		},
		RightPane: components.OverlayPaneSpec{
			Title:   rightPaneTitle,
			Body:    m.detailViewport.View(),
			Focused: m.browseFocus == browseFocusDetails,
		},
	})
	lines = append(lines, panes)
	return strings.Join(lines, "\n")
}

func browserHintParts(includeState, includeView bool) []string {
	parts := []string{"Ctrl+O open", "Ctrl+R clear", "Ctrl+N manual"}
	if includeState {
		parts = append(parts, "Ctrl+T state")
	}
	if includeView {
		parts = append(parts, "Ctrl+V view")
	}
	parts = append(parts, "Enter start", "Space select", "Tab source", "Ctrl+S scope", "Esc cancel", "Multi-select: one provider")
	return parts
}

func wrapBrowserHintParts(parts []string, width int) string {
	if width <= 0 {
		return ""
	}
	lines := make([]string, 0, len(parts))
	current := ""
	for _, part := range parts {
		if part == "" {
			continue
		}
		if ansi.StringWidth(part) > width {
			if current != "" {
				lines = append(lines, current)
				current = ""
			}
			lines = append(lines, strings.Split(ansi.Hardwrap(part, width, true), "\n")...)
			continue
		}
		candidate := part
		if current != "" {
			candidate = current + "  " + part
		}
		if current != "" && ansi.StringWidth(candidate) > width {
			lines = append(lines, current)
			current = part
			continue
		}
		current = candidate
	}
	if current != "" {
		lines = append(lines, current)
	}
	return strings.Join(lines, "\n")
}

func browserHintLineCountForParts(parts []string, width int) int {
	wrapped := wrapBrowserHintParts(parts, width)
	if wrapped == "" {
		return 1
	}
	return strings.Count(wrapped, "\n") + 1
}

func (m NewSessionOverlay) browserHintText() string {
	return strings.Join(browserHintParts(len(m.availableStateOptions()) > 0, len(m.availableViewOptions()) > 0), "  ")
}

func (m NewSessionOverlay) wrappedBrowserHintText(width int) string {
	return wrapBrowserHintParts(browserHintParts(len(m.availableStateOptions()) > 0, len(m.availableViewOptions()) > 0), width)
}

func (m NewSessionOverlay) manualView(width int) string {
	titleLabel := m.styles.Label.Render("Title:       ")
	descLabel := m.styles.Label.Render("Description: ")
	hints := m.styles.Hint.Render(
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

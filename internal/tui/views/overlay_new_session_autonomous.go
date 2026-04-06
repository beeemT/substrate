package views

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/tui/components"
	"github.com/beeemT/substrate/internal/tui/styles"
)

var newSessionAutonomousSizingSpec = browseSizingSpec

type newSessionAutonomousFocus int

const (
	newSessionAutonomousFocusList newSessionAutonomousFocus = iota
	newSessionAutonomousFocusDetails
)

type newSessionAutonomousFilterItem struct {
	filter   domain.NewSessionFilter
	selected bool
	active   bool
}

func (i newSessionAutonomousFilterItem) Title() string {
	mark := "[ ]"
	if i.selected {
		mark = "[x]"
	}
	active := ""
	if i.active {
		active = " · active"
	}
	name := strings.TrimSpace(i.filter.Name)
	if name == "" {
		name = i.filter.ID
	}
	return fmt.Sprintf("%s %s%s", mark, name, active)
}

func (i newSessionAutonomousFilterItem) Description() string {
	parts := []string{cases.Title(language.English).String(strings.TrimSpace(i.filter.Provider))}
	if scope := strings.TrimSpace(string(i.filter.Criteria.Scope)); scope != "" {
		parts = append(parts, scope)
	}
	if state := strings.TrimSpace(i.filter.Criteria.State); state != "" {
		parts = append(parts, state)
	}
	return strings.Join(parts, " · ")
}

func (i newSessionAutonomousFilterItem) FilterValue() string {
	return strings.Join([]string{i.filter.Name, i.filter.Provider, i.filter.ID}, " ")
}

type NewSessionAutonomousOverlay struct { //nolint:recvcheck // Bubble Tea: Update returns value, View on value receiver
	active         bool
	filters        []domain.NewSessionFilter
	selected       map[string]bool
	activeFilterID map[string]struct{}
	running        bool
	focus          newSessionAutonomousFocus
	list           list.Model
	detail         viewport.Model
	styles         styles.Styles
	width          int
	height         int
}

func NewNewSessionAutonomousOverlay(st styles.Styles) NewSessionAutonomousOverlay {
	delegate := list.NewDefaultDelegate()
	delegate.ShowDescription = true
	filterList := list.New([]list.Item{}, delegate, 60, 12)
	filterList.SetShowTitle(false)
	filterList.SetShowStatusBar(false)
	filterList.SetShowPagination(false)
	filterList.SetFilteringEnabled(false)
	filterList.SetShowHelp(false)
	filterList = components.ApplyOverlayListStyles(filterList, st)

	return NewSessionAutonomousOverlay{
		selected:       make(map[string]bool),
		activeFilterID: make(map[string]struct{}),
		focus:          newSessionAutonomousFocusList,
		list:           filterList,
		detail:         viewport.New(0, 0),
		styles:         st,
	}
}

func (m *NewSessionAutonomousOverlay) SetSize(width, height int) {
	m.width = width
	m.height = height
	m.syncDetailViewport(false)
}

func (m *NewSessionAutonomousOverlay) SetSavedFilters(filters []domain.NewSessionFilter) {
	allowed := make([]domain.NewSessionFilter, 0, len(filters))
	allowedIDs := make(map[string]struct{}, len(filters))
	for _, filter := range filters {
		if !isAutonomousEligibleFilter(filter) {
			continue
		}
		allowed = append(allowed, filter)
		if id := strings.TrimSpace(filter.ID); id != "" {
			allowedIDs[id] = struct{}{}
		}
	}
	m.filters = allowed
	sort.SliceStable(m.filters, func(i, j int) bool {
		if m.filters[i].Provider != m.filters[j].Provider {
			return m.filters[i].Provider < m.filters[j].Provider
		}
		if m.filters[i].Name != m.filters[j].Name {
			return m.filters[i].Name < m.filters[j].Name
		}
		return m.filters[i].ID < m.filters[j].ID
	})
	for id := range m.selected {
		if _, ok := allowedIDs[id]; ok {
			continue
		}
		delete(m.selected, id)
	}
	for id := range m.activeFilterID {
		if _, ok := allowedIDs[id]; ok {
			continue
		}
		delete(m.activeFilterID, id)
	}
	m.rebuildList()
	m.syncDetailViewport(true)
}

func (m *NewSessionAutonomousOverlay) SetRuntimeState(running bool, activeFilterIDs []string) {
	m.running = running
	allowedIDs := m.selectableFilterIDs()
	m.activeFilterID = make(map[string]struct{}, len(activeFilterIDs))
	for id := range m.selected {
		if _, ok := allowedIDs[id]; ok {
			continue
		}
		delete(m.selected, id)
	}
	for _, id := range activeFilterIDs {
		trimmed := strings.TrimSpace(id)
		if trimmed == "" {
			continue
		}
		if _, ok := allowedIDs[trimmed]; !ok {
			continue
		}
		m.activeFilterID[trimmed] = struct{}{}
		m.selected[trimmed] = true
	}
	m.rebuildList()
}

func (m *NewSessionAutonomousOverlay) Open() {
	m.active = true
	m.focus = newSessionAutonomousFocusList
	m.rebuildList()
	m.syncDetailViewport(true)
}

func (m *NewSessionAutonomousOverlay) Close() {
	m.active = false
	m.focus = newSessionAutonomousFocusList
}

func (m NewSessionAutonomousOverlay) Active() bool { return m.active }

func (m *NewSessionAutonomousOverlay) rebuildList() {
	cursor := m.list.Index()
	items := make([]list.Item, 0, len(m.filters))
	for _, filter := range m.filters {
		_, active := m.activeFilterID[filter.ID]
		items = append(items, newSessionAutonomousFilterItem{
			filter:   filter,
			selected: m.selected[filter.ID],
			active:   active,
		})
	}
	m.list.SetItems(items)
	if len(items) == 0 {
		m.list.ResetSelected()
		return
	}
	if cursor < 0 || cursor >= len(items) {
		cursor = 0
	}
	m.list.Select(cursor)
}

func (m NewSessionAutonomousOverlay) selectedFilter() *domain.NewSessionFilter {
	item, ok := m.list.SelectedItem().(newSessionAutonomousFilterItem)
	if !ok {
		return nil
	}
	filter := item.filter
	return &filter
}

func (m NewSessionAutonomousOverlay) selectableFilterIDs() map[string]struct{} {
	allowed := make(map[string]struct{}, len(m.filters))
	for _, filter := range m.filters {
		if !isAutonomousEligibleFilter(filter) {
			continue
		}
		id := strings.TrimSpace(filter.ID)
		if id == "" {
			continue
		}
		allowed[id] = struct{}{}
	}
	return allowed
}

func (m NewSessionAutonomousOverlay) selectedFilterIDs() []string {
	allowed := m.selectableFilterIDs()
	ids := make([]string, 0, len(m.selected))
	for id, selected := range m.selected {
		if !selected {
			continue
		}
		if _, ok := allowed[id]; !ok {
			continue
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (m *NewSessionAutonomousOverlay) toggleCurrentSelection() {
	item, ok := m.list.SelectedItem().(newSessionAutonomousFilterItem)
	if !ok {
		return
	}
	if m.selected[item.filter.ID] {
		delete(m.selected, item.filter.ID)
	} else {
		m.selected[item.filter.ID] = true
	}
	m.rebuildList()
}

func (m NewSessionAutonomousOverlay) chromeLines() int {
	return 8
}

func (m NewSessionAutonomousOverlay) layout() components.SplitOverlayLayout {
	return components.ComputeSplitOverlayLayout(m.width, m.height, m.chromeLines(), newSessionAutonomousSizingSpec)
}

func (m *NewSessionAutonomousOverlay) syncDetailViewport(forceTop bool) {
	m.syncDetailViewportWithLayout(m.layout(), forceTop)
}

func (m *NewSessionAutonomousOverlay) syncDetailViewportWithLayout(layout components.SplitOverlayLayout, forceTop bool) {
	m.detail.Width = layout.ViewportWidth
	m.detail.Height = layout.ViewportHeight
	m.detail.SetContent(ansi.Hardwrap(m.detailContent(), maxInt(1, layout.ViewportWidth), true))
	if forceTop {
		m.detail.GotoTop()
	}
}

func (m NewSessionAutonomousOverlay) detailContent() string {
	filter := m.selectedFilter()
	if filter == nil {
		return "No filter selected."
	}
	criteria := filter.Criteria
	lines := []string{
		"Name: " + firstNonEmptyString(strings.TrimSpace(filter.Name), filter.ID),
		"Provider: " + strings.TrimSpace(filter.Provider),
		"Scope: " + string(criteria.Scope),
	}
	if view := strings.TrimSpace(criteria.View); view != "" {
		lines = append(lines, "View: "+view)
	}
	if state := strings.TrimSpace(criteria.State); state != "" {
		lines = append(lines, "State: "+state)
	}
	if search := strings.TrimSpace(criteria.Search); search != "" {
		lines = append(lines, "Search: "+search)
	}
	if len(criteria.Labels) > 0 {
		lines = append(lines, "Labels: "+strings.Join(criteria.Labels, ", "))
	}
	if owner := strings.TrimSpace(criteria.Owner); owner != "" {
		lines = append(lines, "Owner: "+owner)
	}
	if repo := strings.TrimSpace(criteria.Repository); repo != "" {
		lines = append(lines, "Repository: "+repo)
	}
	if group := strings.TrimSpace(criteria.Group); group != "" {
		lines = append(lines, "Group: "+group)
	}
	if teamID := strings.TrimSpace(criteria.TeamID); teamID != "" {
		lines = append(lines, "Team: "+teamID)
	}
	if _, ok := m.activeFilterID[filter.ID]; ok {
		lines = append(lines, "", m.styles.Success.Render("Currently active"))
	}
	return strings.Join(lines, "\n")
}

func (m NewSessionAutonomousOverlay) hintText() string {
	parts := []string{"Space select", "Enter start", "X stop", "Esc close"}
	if m.running {
		parts = append(parts, "Autonomous mode is running")
	}
	return strings.Join(parts, "  ")
}

func (m *NewSessionAutonomousOverlay) openStartCmd() tea.Cmd {
	ids := m.selectedFilterIDs()
	if len(ids) == 0 {
		return func() tea.Msg {
			return ErrMsg{Err: errors.New("select at least one Filter to start autonomous mode")}
		}
	}
	return func() tea.Msg {
		return StartNewSessionAutonomousModeMsg{SelectedFilterIDs: ids}
	}
}

func (m NewSessionAutonomousOverlay) openStopCmd() tea.Cmd {
	return func() tea.Msg { return StopNewSessionAutonomousModeMsg{} }
}

func (m NewSessionAutonomousOverlay) Update(msg tea.Msg) (NewSessionAutonomousOverlay, tea.Cmd) {
	if !m.active {
		return m, nil
	}

	var cmd tea.Cmd
	syncDetailTop := false
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case keyEsc:
			return m, func() tea.Msg { return CloseOverlayMsg{} }
		case "x", "X":
			return m, m.openStopCmd()
		case keyEnter:
			return m, m.openStartCmd()
		case " ":
			m.toggleCurrentSelection()
			syncDetailTop = true
		case keyTab, "right":
			m.focus = newSessionAutonomousFocusDetails
		case "left":
			m.focus = newSessionAutonomousFocusList
		default:
			switch m.focus {
			case newSessionAutonomousFocusDetails:
				m.detail, cmd = m.detail.Update(msg)
				return m, cmd
			default:
				m.list, cmd = m.list.Update(msg)
				syncDetailTop = true
			}
		}
	} else if mouse, ok := msg.(tea.MouseMsg); ok && mouse.Action == tea.MouseActionPress {
		switch mouse.Button {
		case tea.MouseButtonWheelUp, tea.MouseButtonWheelDown:
			if m.focus == newSessionAutonomousFocusDetails {
				m.detail, cmd = m.detail.Update(msg)
				return m, cmd
			}
			m.list, cmd = m.list.Update(msg)
			syncDetailTop = true
		}
	}

	if syncDetailTop {
		m.syncDetailViewport(true)
	} else {
		m.syncDetailViewport(false)
	}
	return m, cmd
}

func (m *NewSessionAutonomousOverlay) View() string {
	if !m.active {
		return ""
	}
	layout := m.layout()
	m.list.SetWidth(layout.LeftInnerWidth)
	m.list.SetHeight(layout.ViewportHeight)
	m.syncDetailViewportWithLayout(layout, false)

	status := "Stopped"
	if m.running {
		status = "Running"
	}
	header := []string{
		m.styles.Title.Render("New Session Autonomous Mode"),
		m.styles.Label.Render("Status: ") + m.styles.Accent.Render(status),
	}

	body := components.RenderSplitOverlayBody(m.styles, layout, components.SplitOverlaySpec{
		LeftPane: components.OverlayPaneSpec{
			Title:   "Saved Filters",
			Body:    m.list.View(),
			Focused: m.focus == newSessionAutonomousFocusList,
		},
		RightPane: components.OverlayPaneSpec{
			Title:   "Filter Details",
			Body:    m.detail.View(),
			Focused: m.focus == newSessionAutonomousFocusDetails,
		},
	})

	hints := m.styles.Hint.Render(truncate(m.hintText(), maxInt(1, layout.ContentWidth-4)))
	return components.RenderOverlayFrame(m.styles, layout.FrameWidth, components.OverlayFrameSpec{
		HeaderLines: header,
		Body:        body,
		Footer:      hints,
		Focused:     true,
	})
}

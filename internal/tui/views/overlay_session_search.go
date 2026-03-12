package views

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/tui/components"
	"github.com/beeemT/substrate/internal/tui/styles"
)

const (
	sessionSearchWindowWidth    = 180
	sessionSearchPaneMinWidth   = 40
	sessionSearchDetailMinWidth = 52
)

var sessionSearchSizingSpec = components.SplitOverlaySizingSpec{
	MaxOverlayWidth:   sessionSearchWindowWidth,
	LeftMinWidth:      sessionSearchPaneMinWidth,
	RightMinWidth:     sessionSearchDetailMinWidth,
	LeftWeight:        2,
	RightWeight:       3,
	MinBodyHeight:     8,
	DefaultBodyHeight: 18,
	HeightRatioNum:    3,
	HeightRatioDen:    5,
	InputWidthOffset:  20,
}

type sessionSearchFocus int

const (
	sessionSearchFocusInput sessionSearchFocus = iota
	sessionSearchFocusScope
	sessionSearchFocusResults
	sessionSearchFocusPreview
)

type sessionSearchListItem struct {
	entry domain.SessionHistoryEntry
}

func (i sessionSearchListItem) Title() string {
	prefix := firstNonEmpty(i.entry.WorkItemExternalID, i.entry.WorkItemID, i.entry.SessionID)
	if i.entry.WorkItemTitle == "" {
		return prefix
	}
	return prefix + "  " + i.entry.WorkItemTitle
}

func (i sessionSearchListItem) Description() string {
	parts := []string{humanHistoryStatus(i.entry)}
	if i.entry.WorkspaceName != "" {
		parts = append(parts, i.entry.WorkspaceName)
	}
	if i.entry.RepositoryName != "" {
		parts = append(parts, i.entry.RepositoryName)
	}
	return strings.Join(parts, " · ")
}

func (i sessionSearchListItem) FilterValue() string {
	return strings.Join([]string{
		i.entry.WorkItemID,
		i.entry.SessionID,
		i.entry.WorkspaceName,
		i.entry.WorkItemExternalID,
		i.entry.WorkItemTitle,
		i.entry.RepositoryName,
		string(i.entry.WorkItemState),
		string(i.entry.Status),
	}, " ")
}

type SessionSearchOverlay struct {
	active             bool
	workspaceAvailable bool
	input              textinput.Model
	list               list.Model
	detail             viewport.Model
	entries            []domain.SessionHistoryEntry
	scope              sessionHistoryScope
	loading            bool
	focus              sessionSearchFocus
	styles             styles.Styles
	width              int
	height             int
}

func NewSessionSearchOverlay(st styles.Styles) SessionSearchOverlay {
	input := textinput.New()
	input.Placeholder = "Search sessions…"
	input.CharLimit = 200

	delegate := list.NewDefaultDelegate()
	delegate.ShowDescription = true
	resultList := list.New([]list.Item{}, delegate, 60, 12)
	resultList.Title = "Sessions"
	resultList.SetShowStatusBar(false)
	resultList.SetFilteringEnabled(false)
	resultList.SetShowHelp(false)
	resultList = components.ApplyOverlayListStyles(resultList, st)

	return SessionSearchOverlay{
		input:  input,
		list:   resultList,
		detail: viewport.New(0, 0),
		scope:  sessionHistoryScopeGlobal,
		focus:  sessionSearchFocusInput,
		styles: st,
	}
}

func (m *SessionSearchOverlay) Open(scope sessionHistoryScope, workspaceAvailable bool) {
	m.active = true
	m.workspaceAvailable = workspaceAvailable
	m.scope = scope
	m.focus = sessionSearchFocusInput
	m.loading = true
	m.entries = nil
	m.list.ResetSelected()
	m.list.SetItems(nil)
	m.detail.SetContent("")
	m.input.Focus()
	m.syncDetailViewport(true)
}

func (m *SessionSearchOverlay) Close() {
	m.active = false
	m.loading = false
	m.focus = sessionSearchFocusInput
	m.input.SetValue("")
	m.input.Blur()
	m.entries = nil
	m.list.ResetSelected()
	m.list.SetItems(nil)
	m.detail.SetContent("")
	m.detail.GotoTop()
}

func (m SessionSearchOverlay) Active() bool { return m.active }

func (m SessionSearchOverlay) Scope() sessionHistoryScope { return m.scope }

func (m SessionSearchOverlay) Filter(workspaceID string) domain.SessionHistoryFilter {
	filter := domain.SessionHistoryFilter{
		Search: strings.TrimSpace(m.input.Value()),
		Limit:  100,
	}
	if m.scope == sessionHistoryScopeWorkspace && workspaceID != "" {
		filter.WorkspaceID = &workspaceID
	}
	return filter
}

func (m *SessionSearchOverlay) SetLoading(loading bool) {
	m.loading = loading
}

func (m *SessionSearchOverlay) SetEntries(entries []domain.SessionHistoryEntry) {
	selectedWorkItemID := ""
	if entry := m.Selected(); entry != nil {
		selectedWorkItemID = entry.WorkItemID
	}

	m.entries = append([]domain.SessionHistoryEntry(nil), entries...)
	sort.SliceStable(m.entries, func(i, j int) bool {
		if !m.entries[i].UpdatedAt.Equal(m.entries[j].UpdatedAt) {
			return m.entries[i].UpdatedAt.After(m.entries[j].UpdatedAt)
		}
		if !m.entries[i].CreatedAt.Equal(m.entries[j].CreatedAt) {
			return m.entries[i].CreatedAt.After(m.entries[j].CreatedAt)
		}
		return m.entries[i].WorkItemID < m.entries[j].WorkItemID
	})

	items := make([]list.Item, 0, len(m.entries))
	selectedIndex := 0
	for i, entry := range m.entries {
		items = append(items, sessionSearchListItem{entry: entry})
		if entry.WorkItemID == selectedWorkItemID {
			selectedIndex = i
		}
	}
	m.list.SetItems(items)
	if len(items) > 0 {
		m.list.Select(selectedIndex)
	} else if m.focus == sessionSearchFocusResults || m.focus == sessionSearchFocusPreview {
		m.focusInput()
	}
	m.syncDetailViewport(true)
}

func (m SessionSearchOverlay) Selected() *domain.SessionHistoryEntry {
	item, ok := m.list.SelectedItem().(sessionSearchListItem)
	if !ok {
		return nil
	}
	entry := item.entry
	return &entry
}

func (m *SessionSearchOverlay) SetSize(width, height int) {
	m.width = width
	m.height = height
	m.syncDetailViewport(false)
}

func (m *SessionSearchOverlay) requestSearchCmd() tea.Cmd {
	return func() tea.Msg { return SessionHistorySearchRequestedMsg{} }
}

func (m *SessionSearchOverlay) focusInput() {
	m.focus = sessionSearchFocusInput
	m.input.Focus()
}

func (m *SessionSearchOverlay) focusScope() bool {
	if !m.workspaceAvailable {
		return false
	}
	m.focus = sessionSearchFocusScope
	m.input.Blur()
	return true
}

func (m *SessionSearchOverlay) focusResults() bool {
	if len(m.entries) == 0 {
		return false
	}
	m.focus = sessionSearchFocusResults
	m.input.Blur()
	return true
}

func (m *SessionSearchOverlay) focusPreview() bool {
	if len(m.entries) == 0 {
		return false
	}
	m.focus = sessionSearchFocusPreview
	m.input.Blur()
	return true
}

func (m *SessionSearchOverlay) cycleFocus() {
	switch m.focus {
	case sessionSearchFocusScope:
		m.focusInput()
	case sessionSearchFocusInput:
		m.focusResults()
	case sessionSearchFocusResults:
		if !m.focusPreview() {
			m.focusInput()
		}
	default:
		if !m.focusScope() {
			m.focusInput()
		}
	}
}

func (m *SessionSearchOverlay) moveFocusLeft() bool {
	switch m.focus {
	case sessionSearchFocusPreview:
		return m.focusResults()
	case sessionSearchFocusResults:
		m.focusInput()
		return true
	default:
		return false
	}
}

func (m *SessionSearchOverlay) moveFocusRight() bool {
	switch m.focus {
	case sessionSearchFocusResults:
		return m.focusPreview()
	default:
		return false
	}
}

func (m *SessionSearchOverlay) setScope(scope sessionHistoryScope) tea.Cmd {
	if !m.workspaceAvailable || m.scope == scope {
		return nil
	}
	m.scope = scope
	return m.requestSearchCmd()
}

func (m *SessionSearchOverlay) toggleScope() tea.Cmd {
	if !m.workspaceAvailable {
		return nil
	}
	if m.scope == sessionHistoryScopeWorkspace {
		return m.setScope(sessionHistoryScopeGlobal)
	}
	return m.setScope(sessionHistoryScopeWorkspace)
}

func humanAgentSessionStatus(status domain.AgentSessionStatus) string {
	switch status {
	case domain.AgentSessionPending:
		return "Pending"
	case domain.AgentSessionRunning:
		return "Running"
	case domain.AgentSessionWaitingForAnswer:
		return "Waiting for answer"
	case domain.AgentSessionCompleted:
		return "Completed"
	case domain.AgentSessionInterrupted:
		return "Interrupted"
	case domain.AgentSessionFailed:
		return "Failed"
	default:
		return ""
	}
}

func humanHistoryStatus(entry domain.SessionHistoryEntry) string {
	switch entry.WorkItemState {
	case domain.WorkItemIngested:
		return "Ready to plan"
	case domain.WorkItemPlanning:
		return "Planning"
	case domain.WorkItemPlanReview:
		return "Plan review needed"
	case domain.WorkItemApproved:
		return "Awaiting implementation"
	case domain.WorkItemImplementing:
		if entry.HasOpenQuestion {
			return "Waiting for answer"
		}
		if entry.HasInterrupted {
			return "Interrupted"
		}
		return "Implementing"
	case domain.WorkItemReviewing:
		return "Under review"
	case domain.WorkItemCompleted:
		return "Completed"
	case domain.WorkItemFailed:
		return "Failed"
	default:
		return firstNonEmpty(string(entry.WorkItemState), humanAgentSessionStatus(entry.Status), "Unknown")
	}
}

func formatSessionTime(t *time.Time) string {
	if t == nil {
		return "—"
	}
	return t.Local().Format("2006-01-02 15:04:05")
}

func (m SessionSearchOverlay) detailContent() string {
	entry := m.Selected()
	if entry == nil {
		if m.loading {
			return "Loading sessions…"
		}
		return "No sessions found for the current scope and query."
	}

	workspace := entry.WorkspaceName
	if workspace == "" {
		workspace = entry.WorkspaceID
	}
	lines := []string{
		entry.WorkItemTitle,
		"",
		fmt.Sprintf("Work item:            %s", firstNonEmpty(entry.WorkItemExternalID, entry.WorkItemID)),
		fmt.Sprintf("State:                %s", humanHistoryStatus(*entry)),
		fmt.Sprintf("Workspace:            %s", firstNonEmpty(workspace, "—")),
		fmt.Sprintf("Agent sessions:       %d", entry.AgentSessionCount),
		fmt.Sprintf("Latest agent session: %s", firstNonEmpty(entry.SessionID, "—")),
		fmt.Sprintf("Latest repo:          %s", firstNonEmpty(entry.RepositoryName, "—")),
		fmt.Sprintf("Latest harness:       %s", firstNonEmpty(entry.HarnessName, "—")),
		fmt.Sprintf("Latest agent status:  %s", firstNonEmpty(humanAgentSessionStatus(entry.Status), "—")),
		fmt.Sprintf("Created:              %s", entry.CreatedAt.Local().Format("2006-01-02 15:04:05")),
		fmt.Sprintf("Updated:              %s", entry.UpdatedAt.Local().Format("2006-01-02 15:04:05")),
		fmt.Sprintf("Finished:             %s", formatSessionTime(entry.CompletedAt)),
		"",
		"Press Enter to open the selected session.",
	}
	if strings.TrimSpace(entry.SessionID) != "" {
		lines = append(lines, "Press d to delete the latest agent session and related records.")
	}
	return strings.Join(lines, "\n")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func (m SessionSearchOverlay) chromeLines() int {
	lines := 10 // outer border plus non-body chrome lines
	if m.loading {
		lines++
	}
	return lines
}

func (m SessionSearchOverlay) layout() components.SplitOverlayLayout {
	return components.ComputeSplitOverlayLayout(m.width, m.height, m.chromeLines(), sessionSearchSizingSpec)
}

func (m *SessionSearchOverlay) syncDetailViewport(forceTop bool) {
	m.syncDetailViewportWithLayout(m.layout(), forceTop)
}

func (m *SessionSearchOverlay) syncDetailViewportWithLayout(layout components.SplitOverlayLayout, forceTop bool) {
	m.detail.Width = layout.ViewportWidth
	m.detail.Height = layout.ViewportHeight
	content := ansi.Hardwrap(m.detailContent(), layout.ViewportWidth, true)
	m.detail.SetContent(content)
	if forceTop {
		m.detail.GotoTop()
	}
}

func (m SessionSearchOverlay) hintText() string {
	return "[↑] Scope  [↓] Results  [←/→] Focus or toggle  [Ctrl+S] Toggle scope  [Enter] Open  [d] Delete session"
}

func (m SessionSearchOverlay) View() string {
	if !m.active {
		return ""
	}

	layout := m.layout()
	renderWidth := max(1, layout.ContentWidth-4)
	m.input.Width = layout.InputWidth
	m.list.SetWidth(layout.LeftInnerWidth)
	m.list.SetHeight(layout.ListHeight)
	m.syncDetailViewportWithLayout(layout, false)

	title := m.styles.Title.Render("Search Sessions")
	scopePrefixStyle := m.styles.Hint
	activeScopeStyle := m.styles.Warning.Copy().Bold(true)
	if m.focus == sessionSearchFocusScope {
		scopePrefixStyle = m.styles.Accent
		activeScopeStyle = activeScopeStyle.Underline(true)
	}
	scopePrefix := scopePrefixStyle.Render("Scope:")
	scopeWorkspace := m.styles.Hint.Render("workspace")
	if m.workspaceAvailable && m.scope == sessionHistoryScopeWorkspace {
		scopeWorkspace = activeScopeStyle.Render("[workspace]")
	}
	scopeGlobal := m.styles.Hint.Render("global")
	if m.scope == sessionHistoryScopeGlobal || !m.workspaceAvailable {
		scopeGlobal = activeScopeStyle.Render("[global]")
	}
	header := []string{
		title,
		truncate(scopePrefix+" "+scopeWorkspace+"  "+scopeGlobal, renderWidth),
		"Search: " + m.input.View(),
		components.RenderOverlayDivider(m.styles, renderWidth),
	}
	if m.loading {
		header = append(header, m.styles.Muted.Render("Searching…"))
	}

	leftContent := m.list.View()
	if m.loading && len(m.entries) == 0 {
		leftContent = m.styles.Muted.Render("Loading…")
	}

	body := components.RenderSplitOverlayBody(m.styles, layout, components.SplitOverlaySpec{
		LeftPane: components.OverlayPaneSpec{
			Body:    leftContent,
			Focused: m.focus == sessionSearchFocusResults,
		},
		RightPane: components.OverlayPaneSpec{
			Title:   "Preview",
			Body:    m.detail.View(),
			Focused: m.focus == sessionSearchFocusPreview,
		},
	})

	hints := m.styles.Hint.Render(truncate(m.hintText(), renderWidth))
	return components.RenderOverlayFrame(m.styles, layout.FrameWidth, components.OverlayFrameSpec{
		HeaderLines: header,
		Body:        body,
		Footer:      hints,
	})
}

func (m SessionSearchOverlay) Update(msg tea.Msg) (SessionSearchOverlay, tea.Cmd) {
	if !m.active {
		return m, nil
	}

	var cmd tea.Cmd
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			return m, func() tea.Msg { return CloseOverlayMsg{} }
		case "ctrl+s":
			return m, m.toggleScope()
		case "tab":
			m.cycleFocus()
			return m, nil
		case "shift+tab":
			if m.focus == sessionSearchFocusInput && m.focusScope() {
				return m, nil
			}
			if m.moveFocusLeft() {
				return m, nil
			}
		case "left", "right":
			if m.focus == sessionSearchFocusScope {
				if msg.String() == "left" {
					return m, m.setScope(sessionHistoryScopeWorkspace)
				}
				return m, m.setScope(sessionHistoryScopeGlobal)
			}
			if msg.String() == "left" && m.moveFocusLeft() {
				return m, nil
			}
			if msg.String() == "right" && m.moveFocusRight() {
				return m, nil
			}
		case "up":
			if m.focus == sessionSearchFocusInput && m.focusScope() {
				return m, nil
			}
		case "down":
			if m.focus == sessionSearchFocusScope {
				m.focusInput()
				return m, nil
			}
			if m.focus == sessionSearchFocusInput && m.focusResults() {
				return m, nil
			}
		}

		switch m.focus {
		case sessionSearchFocusScope:
			if msg.String() == "enter" {
				return m, m.toggleScope()
			}
			return m, nil
		case sessionSearchFocusInput:
			if msg.String() == "enter" {
				m.focusResults()
				return m, nil
			}
			before := m.input.Value()
			m.input, cmd = m.input.Update(msg)
			if strings.TrimSpace(before) != strings.TrimSpace(m.input.Value()) {
				if cmd == nil {
					return m, m.requestSearchCmd()
				}
				return m, tea.Batch(cmd, m.requestSearchCmd())
			}
			return m, cmd
		case sessionSearchFocusResults:
			if msg.String() == "enter" {
				if entry := m.Selected(); entry != nil {
					return m, func() tea.Msg { return OpenSessionHistoryMsg{Entry: *entry} }
				}
				return m, nil
			}
			if msg.String() == "d" {
				if entry := m.Selected(); entry != nil && strings.TrimSpace(entry.SessionID) != "" {
					return m, func() tea.Msg { return ConfirmDeleteSessionMsg{SessionID: entry.SessionID} }
				}
				return m, nil
			}
			before := ""
			if entry := m.Selected(); entry != nil {
				before = entry.WorkItemID
			}
			m.list, cmd = m.list.Update(msg)
			after := ""
			if entry := m.Selected(); entry != nil {
				after = entry.WorkItemID
			}
			if before != after {
				m.syncDetailViewport(true)
			}
			return m, cmd
		case sessionSearchFocusPreview:
			m.detail, cmd = m.detail.Update(msg)
			return m, cmd
		}
	}

	return m, nil
}

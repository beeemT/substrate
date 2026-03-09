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
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/tui/styles"
)

const (
	sessionSearchWindowWidth     = 180
	sessionSearchPaneMinWidth    = 40
	sessionSearchDetailMinWidth  = 52
	sessionSearchVerticalPadding = 7
)

type sessionSearchFocus int

const (
	sessionSearchFocusInput sessionSearchFocus = iota
	sessionSearchFocusResults
	sessionSearchFocusPreview
)

type sessionSearchListItem struct {
	entry domain.SessionHistoryEntry
}

func (i sessionSearchListItem) Title() string {
	prefix := i.entry.WorkItemExternalID
	if prefix == "" {
		prefix = i.entry.SessionID
	}
	if i.entry.WorkItemTitle == "" {
		return prefix
	}
	return prefix + "  " + i.entry.WorkItemTitle
}

func (i sessionSearchListItem) Description() string {
	parts := []string{humanSessionStatus(i.entry.Status)}
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
		i.entry.SessionID,
		i.entry.WorkspaceName,
		i.entry.WorkItemExternalID,
		i.entry.WorkItemTitle,
		i.entry.RepositoryName,
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
	input.Placeholder = "Search session history…"
	input.CharLimit = 200

	delegate := list.NewDefaultDelegate()
	delegate.ShowDescription = true
	resultList := list.New([]list.Item{}, delegate, 60, 12)
	resultList.Title = "Sessions"
	resultList.SetShowStatusBar(false)
	resultList.SetFilteringEnabled(false)
	resultList.SetShowHelp(false)
	resultList.Styles.NoItems = resultList.Styles.NoItems.Background(lipgloss.Color(overlayBackgroundColor))
	resultList.Styles.StatusEmpty = resultList.Styles.StatusEmpty.Background(lipgloss.Color(overlayBackgroundColor))

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
	selectedSessionID := ""
	if entry := m.Selected(); entry != nil {
		selectedSessionID = entry.SessionID
	}

	m.entries = append([]domain.SessionHistoryEntry(nil), entries...)
	sort.SliceStable(m.entries, func(i, j int) bool {
		if !m.entries[i].UpdatedAt.Equal(m.entries[j].UpdatedAt) {
			return m.entries[i].UpdatedAt.After(m.entries[j].UpdatedAt)
		}
		if !m.entries[i].CreatedAt.Equal(m.entries[j].CreatedAt) {
			return m.entries[i].CreatedAt.After(m.entries[j].CreatedAt)
		}
		return m.entries[i].SessionID < m.entries[j].SessionID
	})

	items := make([]list.Item, 0, len(m.entries))
	selectedIndex := 0
	for i, entry := range m.entries {
		items = append(items, sessionSearchListItem{entry: entry})
		if entry.SessionID == selectedSessionID {
			selectedIndex = i
		}
	}
	m.list.SetItems(items)
	if len(items) > 0 {
		m.list.Select(selectedIndex)
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

func (m *SessionSearchOverlay) cycleFocus() {
	switch m.focus {
	case sessionSearchFocusInput:
		if len(m.entries) > 0 {
			m.focus = sessionSearchFocusResults
			m.input.Blur()
		}
	case sessionSearchFocusResults:
		m.focus = sessionSearchFocusPreview
		m.input.Blur()
	default:
		m.focus = sessionSearchFocusInput
		m.input.Focus()
	}
}

func (m *SessionSearchOverlay) toggleScope() tea.Cmd {
	if !m.workspaceAvailable {
		return nil
	}
	if m.scope == sessionHistoryScopeWorkspace {
		m.scope = sessionHistoryScopeGlobal
	} else {
		m.scope = sessionHistoryScopeWorkspace
	}
	return m.requestSearchCmd()
}

func humanSessionStatus(status domain.AgentSessionStatus) string {
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
		return string(status)
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
		fmt.Sprintf("Work item: %s", firstNonEmpty(entry.WorkItemExternalID, entry.WorkItemID)),
		fmt.Sprintf("Session:   %s", entry.SessionID),
		fmt.Sprintf("Workspace: %s", firstNonEmpty(workspace, "—")),
		fmt.Sprintf("Repo:      %s", firstNonEmpty(entry.RepositoryName, "—")),
		fmt.Sprintf("Harness:   %s", firstNonEmpty(entry.HarnessName, "—")),
		fmt.Sprintf("Status:    %s", humanSessionStatus(entry.Status)),
		fmt.Sprintf("State:     %s", firstNonEmpty(string(entry.WorkItemState), "—")),
		fmt.Sprintf("Created:   %s", entry.CreatedAt.Local().Format("2006-01-02 15:04:05")),
		fmt.Sprintf("Updated:   %s", entry.UpdatedAt.Local().Format("2006-01-02 15:04:05")),
		fmt.Sprintf("Finished:  %s", formatSessionTime(entry.CompletedAt)),
		"",
		"Press Enter to open the selected session.",
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

func (m SessionSearchOverlay) renderWidth() int {
	if m.width <= 0 {
		return maxInt(1, sessionSearchWindowWidth-overlayHorizontalFrame)
	}
	return maxInt(1, minInt(sessionSearchWindowWidth-overlayHorizontalFrame, m.width-overlayHorizontalFrame))
}

func (m SessionSearchOverlay) renderContentWidth() int {
	return maxInt(1, m.renderWidth()-overlayHorizontalPad)
}

func (m SessionSearchOverlay) paneWidths(renderWidth int) (int, int) {
	available := maxInt(1, renderWidth-1)
	if renderWidth <= sessionSearchPaneMinWidth+sessionSearchDetailMinWidth+1 {
		leftWidth := maxInt(1, available/2)
		rightWidth := maxInt(1, available-leftWidth)
		return leftWidth, rightWidth
	}
	leftWidth := maxInt(sessionSearchPaneMinWidth, renderWidth*2/5)
	rightWidth := maxInt(sessionSearchDetailMinWidth, available-leftWidth)
	if leftWidth+rightWidth > available {
		rightWidth = maxInt(1, available-leftWidth)
	}
	if rightWidth < sessionSearchDetailMinWidth {
		rightWidth = sessionSearchDetailMinWidth
		leftWidth = maxInt(1, available-rightWidth)
	}
	return leftWidth, rightWidth
}

func (m SessionSearchOverlay) chromeLines() int {
	lines := 10 // outer border plus non-body chrome lines
	if m.loading {
		lines++
	}
	return lines
}

func (m SessionSearchOverlay) paneHeight() int {
	target := 18
	if m.height > 0 {
		target = maxInt(8, (m.height*browseHeightNumerator+browseHeightDenom-1)/browseHeightDenom)
	}
	if m.height <= 0 {
		return target
	}
	maxHeight := m.height - m.chromeLines()
	if maxHeight < 1 {
		return 1
	}
	return maxInt(1, minInt(target, maxHeight))
}

func (m *SessionSearchOverlay) syncDetailViewport(forceTop bool) {
	contentWidth := m.renderContentWidth()
	_, rightWidth := m.paneWidths(contentWidth)
	paneHeight := m.paneHeight()
	viewportWidth := maxInt(1, rightWidth-paneHorizontalFrame-2)
	viewportHeight := maxInt(1, paneHeight-paneVerticalFrame-2)
	m.detail.Width = viewportWidth
	m.detail.Height = viewportHeight
	content := ansi.Hardwrap(m.detailContent(), viewportWidth, true)
	m.detail.SetContent(content)
	if forceTop {
		m.detail.GotoTop()
	}
}

func (m SessionSearchOverlay) hintText() string {
	return "[Tab] Focus  [Ctrl+S] Toggle scope  [Enter] Open  [Esc] Close"
}

func (m SessionSearchOverlay) View() string {
	if !m.active {
		return ""
	}

	boxWidth := m.renderWidth()
	contentWidth := m.renderContentWidth()
	m.input.Width = maxInt(1, contentWidth-20)
	m.syncDetailViewport(false)

	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#f0f0f0")).Render("Search Sessions")
	scopeWorkspace := lipgloss.NewStyle().Foreground(lipgloss.Color("#6b7280")).Render("workspace")
	if m.workspaceAvailable && m.scope == sessionHistoryScopeWorkspace {
		scopeWorkspace = lipgloss.NewStyle().Foreground(lipgloss.Color("#f59e0b")).Bold(true).Render("[workspace]")
	}
	scopeGlobal := lipgloss.NewStyle().Foreground(lipgloss.Color("#6b7280")).Render("global")
	if m.scope == sessionHistoryScopeGlobal || !m.workspaceAvailable {
		scopeGlobal = lipgloss.NewStyle().Foreground(lipgloss.Color("#f59e0b")).Bold(true).Render("[global]")
	}
	header := []string{
		title,
		truncate("Scope: "+scopeWorkspace+"  "+scopeGlobal, maxInt(1, contentWidth)),
		"Search: " + m.input.View(),
		lipgloss.NewStyle().Foreground(lipgloss.Color("#2d2d44")).Width(contentWidth).Render(strings.Repeat("─", maxInt(1, contentWidth))),
	}
	if m.loading {
		header = append(header, lipgloss.NewStyle().Foreground(lipgloss.Color("#6b7280")).Render("Searching…"))
	}

	leftBorder := lipgloss.Color("#2d2d44")
	rightBorder := lipgloss.Color("#2d2d44")
	if m.focus == sessionSearchFocusResults {
		leftBorder = lipgloss.Color("#60a5fa")
	}
	if m.focus == sessionSearchFocusPreview {
		rightBorder = lipgloss.Color("#60a5fa")
	}

	paneHeight := m.paneHeight()
	leftWidth, rightWidth := m.paneWidths(contentWidth)
	leftInnerWidth := maxInt(1, leftWidth-paneHorizontalFrame)
	rightInnerWidth := maxInt(1, rightWidth-paneHorizontalFrame)
	listHeight := maxInt(1, paneHeight-paneVerticalFrame)
	m.list.SetWidth(leftInnerWidth)
	m.list.SetHeight(listHeight)
	m.syncDetailViewport(false)

	leftContent := m.list.View()
	if m.loading && len(m.entries) == 0 {
		leftContent = lipgloss.NewStyle().Foreground(lipgloss.Color("#6b7280")).Render("Loading…")
	}
	leftPane := lipgloss.NewStyle().
		Width(leftInnerWidth).
		Height(listHeight).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(leftBorder).
		Padding(0, 1).
		Render(leftContent)
	detailHeader := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#f0f0f0")).Render("Preview")
	detailDivider := lipgloss.NewStyle().Foreground(lipgloss.Color("#2d2d44")).Width(m.detail.Width).Render(strings.Repeat("─", maxInt(1, m.detail.Width)))
	rightPane := lipgloss.NewStyle().
		Width(rightInnerWidth).
		Height(listHeight).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(rightBorder).
		Padding(0, 1).
		Render(strings.Join([]string{detailHeader, detailDivider, m.detail.View()}, "\n"))
	body := lipgloss.JoinHorizontal(lipgloss.Top, leftPane, " ", rightPane)
	hints := lipgloss.NewStyle().Foreground(lipgloss.Color("#6b7280")).Render(truncate(m.hintText(), maxInt(1, contentWidth)))
	content := strings.Join(append(header, "", body, hints), "\n")
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#2d2d44")).
		Background(lipgloss.Color(overlayBackgroundColor)).
		Padding(0, 2).
		Width(boxWidth).
		Render(content)
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
		case "tab", "shift+tab":
			m.cycleFocus()
			return m, nil
		}

		switch m.focus {
		case sessionSearchFocusInput:
			if msg.String() == "enter" {
				if len(m.entries) > 0 {
					m.focus = sessionSearchFocusResults
					m.input.Blur()
				}
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
			before := ""
			if entry := m.Selected(); entry != nil {
				before = entry.SessionID
			}
			m.list, cmd = m.list.Update(msg)
			after := ""
			if entry := m.Selected(); entry != nil {
				after = entry.SessionID
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

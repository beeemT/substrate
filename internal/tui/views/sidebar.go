package views

import (
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/tui/components"
	"github.com/beeemT/substrate/internal/tui/styles"
)

// SidebarWidth is the fixed character width of the sidebar panel.
const SidebarWidth = 34

type SidebarEntryKind int

const (
	SidebarEntryWorkItem SidebarEntryKind = iota
	SidebarEntrySessionHistory
	SidebarEntryTaskOverview
	SidebarEntryTaskSourceDetails
	SidebarEntryTaskSession
	SidebarEntryGroupHeader
)

// SidebarEntry is one selectable row in the sidebar.
type SidebarEntry struct {
	Kind            SidebarEntryKind
	WorkItemID      string
	SessionID       string
	WorkspaceID     string
	WorkspaceName   string
	ExternalID      string
	Source          string
	Title           string
	SubtitleText    string
	State           domain.SessionState
	SessionStatus   domain.TaskStatus
	RepositoryName  string
	LastActivity    time.Time
	TotalSubPlans   int
	DoneSubPlans    int
	HasOpenQuestion bool
	HasInterrupted  bool
	GroupTitle      string
}

func (e SidebarEntry) titlePrefix() string {
	switch e.Kind {
	case SidebarEntryTaskOverview:
		return "Overview"
	case SidebarEntryTaskSourceDetails:
		return "Source"
	case SidebarEntryTaskSession:
		if e.RepositoryName != "" {
			return e.RepositoryName
		}
		if e.SessionID != "" {
			return "Task " + shortSessionID(e.SessionID)
		}

		return "Task"
	default:
		if label := e.displayExternalID(); label != "" {
			return label
		}
		if e.WorkItemID != "" {
			return e.WorkItemID
		}

		return e.SessionID
	}
}

func (e SidebarEntry) displayExternalID() string {
	raw := strings.TrimSpace(e.ExternalID)
	if after, ok := strings.CutPrefix(raw, "gh:issue:"); ok {
		return after
	}
	if after, ok := strings.CutPrefix(raw, "gl:issue:"); ok {
		return after
	}
	return raw
}

func (e SidebarEntry) displaySource() string {
	if source := strings.TrimSpace(e.Source); source != "" {
		return source
	}
	switch {
	case strings.HasPrefix(strings.TrimSpace(e.ExternalID), "gh:issue:"):
		return providerGithub
	case strings.HasPrefix(strings.TrimSpace(e.ExternalID), "gl:issue:"):
		return providerGitlab
	default:
		return ""
	}
}

func (e SidebarEntry) sidebarPrefix() string {
	prefix := e.titlePrefix()
	if e.Kind != SidebarEntryWorkItem && e.Kind != SidebarEntryTaskOverview {
		return prefix
	}
	provider := detailProviderLabel(e.displaySource())
	switch {
	case prefix == "":
		return provider
	case provider == "":
		return prefix
	default:
		return prefix + " · " + provider
	}
}

// StatusIcon returns the styled status icon for the sidebar entry.
func (e SidebarEntry) StatusIcon(st styles.Styles) string {
	if e.Kind == SidebarEntryTaskSourceDetails {
		return st.Muted.Render("◌")
	}
	if e.Kind == SidebarEntryTaskSession {
		switch e.SessionStatus {
		case domain.AgentSessionCompleted:
			return st.Success.Render("✓")
		case domain.AgentSessionFailed:
			return st.Error.Render("✗")
		case domain.AgentSessionInterrupted:
			return st.Interrupted.Render("⊘")
		case domain.AgentSessionWaitingForAnswer:
			return st.Warning.Render("◐")
		case domain.AgentSessionRunning:
			return st.Active.Render("●")
		default:
			return st.Muted.Render("◌")
		}
	}
	switch {
	case e.State == domain.SessionCompleted:
		return st.Success.Render("✓")
	case e.State == domain.SessionFailed:
		return st.Error.Render("✗")
	case (e.State == domain.SessionImplementing || e.State == domain.SessionReviewing) && e.HasInterrupted:
		return st.Interrupted.Render("⊘")
	case e.State == domain.SessionPlanReview || (e.State == domain.SessionImplementing && e.HasOpenQuestion):
		return st.Warning.Render("◐")
	case e.State == domain.SessionImplementing || e.State == domain.SessionPlanning || e.State == domain.SessionReviewing:
		return st.Active.Render("●")
	case e.HasOpenQuestion || e.HasInterrupted:
		return st.Warning.Render("◐")
	default:
		return st.Muted.Render("◌")
	}
}

// Subtitle returns the human-readable status line for this sidebar entry.
func (e SidebarEntry) Subtitle() string {
	if e.Kind == SidebarEntryTaskSession {
		return sessionStatusLabel(e.SessionStatus)
	}
	if e.SubtitleText != "" {
		return e.SubtitleText
	}
	status := ""
	switch e.State {
	case domain.SessionIngested:
		status = "Ready to plan"
	case domain.SessionPlanning:
		status = "Planning..."
	case domain.SessionPlanReview:
		status = "Plan review needed"
	case domain.SessionApproved:
		status = "Awaiting implementation"
	case domain.SessionImplementing:
		if e.HasOpenQuestion {
			status = "Waiting for answer"
		} else if e.HasInterrupted {
			status = "Interrupted"
		} else {
			status = "Implementing"
		}
	case domain.SessionReviewing:
		status = "Under review"
	case domain.SessionCompleted:
		status = "Completed"
	case domain.SessionFailed:
		status = "Failed"
	}
	if e.Kind != SidebarEntrySessionHistory {
		return status
	}
	parts := []string{status}
	if e.WorkspaceName != "" {
		parts = append(parts, e.WorkspaceName)
	}
	if e.RepositoryName != "" {
		parts = append(parts, e.RepositoryName)
	}

	return strings.Join(parts, " · ")
}

// SidebarModel manages the session list sidebar.
type SidebarModel struct { //nolint:recvcheck // Bubble Tea convention
	entries    []SidebarEntry
	cursor     int
	title      string
	styles     styles.Styles
	width      int
	height     int
	cachedView *string // render cache; pointer survives value-receiver copies
	viewDirty  *bool   // true when state changed since last View()
}

// NewSidebarModel creates a new SidebarModel with the given styles.
func NewSidebarModel(st styles.Styles) SidebarModel {
	dirty := true
	return SidebarModel{
		styles:     st,
		width:      SidebarWidth,
		title:      "Sessions",
		cursor:     -1,
		cachedView: new(string),
		viewDirty:  &dirty,
	}
}

// SetEntries replaces the sidebar entries and preserves selection when possible.
func (m *SidebarModel) SetEntries(entries []SidebarEntry) {
	selectedWorkItemID := ""
	selectedSessionID := ""
	hadSelection := false
	if current := m.Selected(); current != nil {
		selectedWorkItemID = current.WorkItemID
		selectedSessionID = current.SessionID
		hadSelection = true
	}
	m.entries = entries
	*m.viewDirty = true
	if len(m.entries) == 0 {
		m.cursor = -1

		return
	}
	if !hadSelection {
		m.cursor = -1

		return
	}
	for i, entry := range m.entries {
		if entry.WorkItemID == selectedWorkItemID && entry.SessionID == selectedSessionID {
			m.cursor = i

			return
		}
	}
	if m.cursor >= len(m.entries) {
		m.cursor = len(m.entries) - 1
	}
}

// SetWidth sets the available render width.
func (m *SidebarModel) SetWidth(w int) {
	m.width = max(0, w)
	*m.viewDirty = true
}

// SetHeight sets the available render height.
func (m *SidebarModel) SetHeight(h int) { m.height = h; *m.viewDirty = true }

// SetTitle sets the sidebar header title.
func (m *SidebarModel) SetTitle(title string) {
	m.title = title
	*m.viewDirty = true
}

// MoveUp moves the cursor up by one selectable entry, skipping group headers.
func (m *SidebarModel) MoveUp() {
	if len(m.entries) == 0 {
		m.cursor = -1
		*m.viewDirty = true

		return
	}
	if m.cursor < 0 {
		m.cursor = lastSelectableIndex(m.entries)
		*m.viewDirty = true

		return
	}
	for i := m.cursor - 1; i >= 0; i-- {
		if m.entries[i].Kind != SidebarEntryGroupHeader {
			m.cursor = i
			*m.viewDirty = true

			return
		}
	}
}

// MoveDown moves the cursor down by one selectable entry, skipping group headers.
func (m *SidebarModel) MoveDown() {
	if len(m.entries) == 0 {
		m.cursor = -1
		*m.viewDirty = true

		return
	}
	if m.cursor < 0 {
		m.cursor = firstSelectableIndex(m.entries)
		*m.viewDirty = true

		return
	}
	for i := m.cursor + 1; i < len(m.entries); i++ {
		if m.entries[i].Kind != SidebarEntryGroupHeader {
			m.cursor = i
			*m.viewDirty = true

			return
		}
	}
}

// GotoTop moves the cursor to the first selectable entry.
func (m *SidebarModel) GotoTop() {
	m.cursor = firstSelectableIndex(m.entries)
	*m.viewDirty = true
}

// GotoBottom moves the cursor to the last selectable entry.
func (m *SidebarModel) GotoBottom() {
	m.cursor = lastSelectableIndex(m.entries)
	*m.viewDirty = true
}

// ClearSelection clears the current sidebar selection.
func (m *SidebarModel) ClearSelection() {
	m.cursor = -1
	*m.viewDirty = true
}

// SelectEntry moves the cursor to the matching work-item/session pair when present.
func (m *SidebarModel) SelectEntry(workItemID, sessionID string) bool {
	for i, entry := range m.entries {
		if entry.WorkItemID == workItemID && entry.SessionID == sessionID {
			m.cursor = i
			*m.viewDirty = true

			return true
		}
	}

	return false
}

// SelectWorkItem moves the cursor to the matching overview/work-item entry when present.
func (m *SidebarModel) SelectWorkItem(workItemID string) bool {
	return m.SelectEntry(workItemID, "")
}

// Selected returns a copy of the currently selected entry, or nil if none.
func (m *SidebarModel) Selected() *SidebarEntry {
	if len(m.entries) == 0 || m.cursor < 0 || m.cursor >= len(m.entries) {
		return nil
	}
	entry := m.entries[m.cursor]
	if entry.Kind == SidebarEntryGroupHeader {
		return nil
	}

	return &entry
}

// View renders the full sidebar.
func (m SidebarModel) View() string {
	if m.height <= 0 {
		*m.cachedView = ""
		*m.viewDirty = false

		return ""
	}
	if !*m.viewDirty && *m.cachedView != "" {
		return *m.cachedView
	}
	width := m.width
	if width <= 0 {
		width = SidebarWidth
	}
	const headerLines = 2 // title + divider
	availableRows := max(0, m.height-headerLines)
	// Determine visible entries and whether scrolling is needed.
	var start, end int
	var needsScroll bool
	if availableRows > 0 {
		rowBudget := availableRows
		visibleRowCount := 0
		visibleEntryCount := 0
		for i := range m.entries {
			rows := entryRowHeight(m.entries[i])
			if visibleRowCount+rows > rowBudget && visibleRowCount > 0 {
				break
			}
			visibleRowCount += rows
			visibleEntryCount++
		}
		if visibleEntryCount < len(m.entries) {
			needsScroll = true
			// Need scrolling. Start from cursor (clamped to 0 if unselected).
			start = 0
			if m.cursor >= 0 {
				start = m.cursor
			}
			cursorRows := entryRowHeight(m.entries[start])
			for start > 0 {
				prevRows := entryRowHeight(m.entries[start-1])
				if cursorRows+prevRows > availableRows {
					break
				}
				start--
				cursorRows += prevRows
			}
			end = 0
			rowUsed := 0
			for i := start; i < len(m.entries); i++ {
				r := entryRowHeight(m.entries[i])
				if rowUsed+r > availableRows {
					break
				}
				rowUsed += r
				end = i + 1
			}
		} else {
			start = 0
			end = len(m.entries)
		}
	}
	// When entries overflow, shrink content width for a thin scrollbar.
	contentWidth := width
	var scrollbar string
	if needsScroll && width > 2 {
		contentWidth = width - 2 // 1 char scrollbar + 1 char gap
		scrollbar = renderSidebarScrollbar(m.styles, m.entries, availableRows, start, m.height)
	}
	// Build header lines at the resolved content width.
	var lines []string
	titleText := m.title
	if strings.TrimSpace(titleText) == "" {
		titleText = "Sessions"
	}
	title := m.styles.SectionLabel.Render(titleText)
	header := lipgloss.NewStyle().Width(contentWidth).AlignHorizontal(lipgloss.Center).Render(title)
	lines = append(lines, header)
	lines = append(lines, components.RenderDivider(m.styles, contentWidth))
	for i := start; i < end; i++ {
		entry := m.entries[i]
		if entry.Kind == SidebarEntryGroupHeader {
			lines = append(lines, renderGroupHeader(m.styles, entry.GroupTitle, contentWidth))
			lines = append(lines, "")
			continue
		}
		selected := i == m.cursor
		icon := entry.StatusIcon(m.styles)
		line1 := truncate(icon+" "+entry.sidebarPrefix(), contentWidth)
		titleLine := truncate("  "+entry.Title, contentWidth)
		var line3 string
		if (entry.Kind == SidebarEntryWorkItem || entry.Kind == SidebarEntryTaskOverview) && entry.State == domain.SessionImplementing && entry.TotalSubPlans > 0 {
			bar := components.RenderProgressBar(m.styles, entry.DoneSubPlans, entry.TotalSubPlans, max(1, contentWidth-4))
			line3 = "  " + truncate(bar, max(1, contentWidth-2))
		} else {
			line3 = "  " + m.styles.Subtitle.Render(truncate(entry.Subtitle(), max(1, contentWidth-2)))
		}
		block := strings.Join([]string{line1, titleLine, line3}, "\n")
		if selected {
			lines = append(lines, m.styles.SidebarSelected.Width(contentWidth).Render(block))
		} else {
			lines = append(lines, lipgloss.NewStyle().Width(contentWidth).Render(block))
		}
		lines = append(lines, "")
	}
	for len(lines) < m.height {
		lines = append(lines, lipgloss.NewStyle().Width(contentWidth).Render(""))
	}

	var result string
	if scrollbar != "" {
		content := fitViewBox(strings.Join(lines, "\n"), contentWidth, m.height)
		result = lipgloss.JoinHorizontal(lipgloss.Top, content, " ", scrollbar)
	} else {
		result = fitViewBox(strings.Join(lines, "\n"), width, m.height)
	}
	*m.cachedView = result
	*m.viewDirty = false

	return result
}

func sessionStatusLabel(status domain.TaskStatus) string {
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

func shortSessionID(id string) string {
	if len(id) <= 8 {
		return id
	}

	return id[:8]
}

// truncate trims s to maxLen display cells, appending "…" if truncated.
func truncate(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	if ansi.StringWidth(s) <= maxLen {
		return s
	}
	if maxLen == 1 {
		return "…"
	}

	return ansi.Truncate(s, maxLen, "…")
}

// entryRowHeight returns the number of rendered rows an entry occupies.

// firstSelectableIndex returns the index of the first non-group-header entry, or -1.
func firstSelectableIndex(entries []SidebarEntry) int {
	for i, e := range entries {
		if e.Kind != SidebarEntryGroupHeader {
			return i
		}
	}
	return -1
}

// lastSelectableIndex returns the index of the last non-group-header entry, or -1.
func lastSelectableIndex(entries []SidebarEntry) int {
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Kind != SidebarEntryGroupHeader {
			return i
		}
	}
	return -1
}

func entryRowHeight(e SidebarEntry) int {
	if e.Kind == SidebarEntryGroupHeader {
		return 2 // heading line + blank separator
	}
	return 4 // 3 lines + blank separator
}

// renderSidebarScrollbar renders a thin scrollbar column for the sidebar content area.
// The scrollbar covers the full rendered height; header rows show the track, and the
// thumb is positioned based on scrollOffset within the content portion.
func renderSidebarScrollbar(st styles.Styles, entries []SidebarEntry, contentHeight, firstVisible, totalHeight int) string {
	if totalHeight <= 0 {
		return ""
	}
	totalRows := 0
	for _, e := range entries {
		totalRows += entryRowHeight(e)
	}
	if totalRows <= contentHeight {
		return ""
	}
	scrollOffset := 0
	for i := 0; i < firstVisible; i++ {
		scrollOffset += entryRowHeight(entries[i])
	}
	headerRows := 2 // title + divider
	thumbHeight := max(1, (contentHeight*contentHeight)/max(1, totalRows))
	thumbHeight = min(thumbHeight, contentHeight)
	thumbRange := max(0, contentHeight-thumbHeight)
	scrollRange := max(1, totalRows-contentHeight)
	thumbTop := 0
	if thumbRange > 0 {
		thumbTop = (scrollOffset*thumbRange + scrollRange/2) / scrollRange
	}
	lines := make([]string, totalHeight)
	for i := range lines {
		lines[i] = st.ScrollbarTrack.Render("▏")
	}
	for i := 0; i < thumbHeight; i++ {
		idx := headerRows + thumbTop + i
		if idx < totalHeight {
			lines[idx] = st.ScrollbarThumb.Render("▐")
		}
	}

	return strings.Join(lines, "\n")
}

// renderGroupHeader renders a group section heading with a trailing divider on one line.
func renderGroupHeader(st styles.Styles, title string, width int) string {
	label := st.Muted.Bold(true).Render(title)
	dividerWidth := max(0, width-ansi.StringWidth(label)-1)
	if dividerWidth <= 0 {
		return lipgloss.NewStyle().Width(width).Render(label)
	}
	return label + " " + st.Divider.Render(strings.Repeat("─", dividerWidth))
}

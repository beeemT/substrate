package views

import (
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

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
)

// SidebarEntry is one selectable row in the sidebar.
type SidebarEntry struct {
	Kind            SidebarEntryKind
	WorkItemID      string
	SessionID       string
	WorkspaceID     string
	WorkspaceName   string
	ExternalID      string
	Title           string
	SubtitleText    string
	State           domain.WorkItemState
	SessionStatus   domain.AgentSessionStatus
	RepositoryName  string
	LastActivity    time.Time
	TotalSubPlans   int
	DoneSubPlans    int
	HasOpenQuestion bool
	HasInterrupted  bool
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
		if e.ExternalID != "" {
			return e.ExternalID
		}
		if e.WorkItemID != "" {
			return e.WorkItemID
		}
		return e.SessionID
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
	case e.State == domain.WorkItemCompleted:
		return st.Success.Render("✓")
	case e.State == domain.WorkItemFailed:
		return st.Error.Render("✗")
	case (e.State == domain.WorkItemImplementing || e.State == domain.WorkItemReviewing) && e.HasInterrupted:
		return st.Interrupted.Render("⊘")
	case e.State == domain.WorkItemPlanReview, e.State == domain.WorkItemImplementing && e.HasOpenQuestion:
		return st.Warning.Render("◐")
	case e.State == domain.WorkItemImplementing || e.State == domain.WorkItemPlanning || e.State == domain.WorkItemReviewing:
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
	case domain.WorkItemIngested:
		status = "Ready to plan"
	case domain.WorkItemPlanning:
		status = "Planning..."
	case domain.WorkItemPlanReview:
		status = "Plan review needed"
	case domain.WorkItemApproved:
		status = "Awaiting implementation"
	case domain.WorkItemImplementing:
		if e.HasOpenQuestion {
			status = "Waiting for answer"
		} else if e.HasInterrupted {
			status = "Interrupted"
		} else {
			status = "Implementing"
		}
	case domain.WorkItemReviewing:
		status = "Under review"
	case domain.WorkItemCompleted:
		status = "Completed"
	case domain.WorkItemFailed:
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
type SidebarModel struct {
	entries []SidebarEntry
	cursor  int
	title   string
	styles  styles.Styles
	width   int
	height  int
}

// NewSidebarModel creates a new SidebarModel with the given styles.
func NewSidebarModel(st styles.Styles) SidebarModel {
	return SidebarModel{styles: st, width: SidebarWidth, title: "Sessions", cursor: -1}
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
}

// SetHeight sets the available render height.
func (m *SidebarModel) SetHeight(h int) { m.height = h }

// SetTitle sets the sidebar header title.
func (m *SidebarModel) SetTitle(title string) {
	m.title = title
}

// MoveUp moves the cursor up by one entry.
func (m *SidebarModel) MoveUp() {
	if len(m.entries) == 0 {
		m.cursor = -1
		return
	}
	if m.cursor < 0 {
		m.cursor = len(m.entries) - 1
		return
	}
	if m.cursor > 0 {
		m.cursor--
	}
}

// MoveDown moves the cursor down by one entry.
func (m *SidebarModel) MoveDown() {
	if len(m.entries) == 0 {
		m.cursor = -1
		return
	}
	if m.cursor < 0 {
		m.cursor = 0
		return
	}
	if m.cursor < len(m.entries)-1 {
		m.cursor++
	}
}

// GotoTop moves the cursor to the first entry.
func (m *SidebarModel) GotoTop() {
	if len(m.entries) == 0 {
		m.cursor = -1
		return
	}
	m.cursor = 0
}

// GotoBottom moves the cursor to the last entry.
func (m *SidebarModel) GotoBottom() {
	if len(m.entries) == 0 {
		m.cursor = -1
		return
	}
	m.cursor = len(m.entries) - 1
}

// ClearSelection clears the current sidebar selection.
func (m *SidebarModel) ClearSelection() {
	m.cursor = -1
}

// SelectEntry moves the cursor to the matching work-item/session pair when present.
func (m *SidebarModel) SelectEntry(workItemID, sessionID string) bool {
	for i, entry := range m.entries {
		if entry.WorkItemID == workItemID && entry.SessionID == sessionID {
			m.cursor = i
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
	return &entry
}

// View renders the full sidebar.
func (m SidebarModel) View() string {
	if m.height <= 0 {
		return ""
	}
	width := m.width
	if width <= 0 {
		width = SidebarWidth
	}
	var lines []string

	titleText := m.title
	if strings.TrimSpace(titleText) == "" {
		titleText = "Sessions"
	}
	title := m.styles.SectionLabel.Render(titleText)
	header := lipgloss.NewStyle().Width(width).AlignHorizontal(lipgloss.Center).Render(title)
	lines = append(lines, header)
	lines = append(lines, components.RenderDivider(m.styles, width))

	visibleEntryCount := 0
	if availableRows := m.height - len(lines); availableRows > 0 {
		visibleEntryCount = availableRows / 4
	}
	start, end := 0, len(m.entries)
	if visibleEntryCount <= 0 {
		start, end = 0, 0
	} else if len(m.entries) > visibleEntryCount {
		start = min(max(0, m.cursor-visibleEntryCount+1), len(m.entries)-visibleEntryCount)
		end = start + visibleEntryCount
	}
	for i := start; i < end; i++ {
		entry := m.entries[i]
		selected := i == m.cursor
		icon := entry.StatusIcon(m.styles)
		line1 := truncate(icon+" "+entry.titlePrefix(), width)
		titleLine := truncate("  "+entry.Title, width)
		var line3 string
		if (entry.Kind == SidebarEntryWorkItem || entry.Kind == SidebarEntryTaskOverview) && entry.State == domain.WorkItemImplementing && entry.TotalSubPlans > 0 {
			bar := components.RenderProgressBar(m.styles, entry.DoneSubPlans, entry.TotalSubPlans, max(1, width-4))
			line3 = "  " + truncate(bar, max(1, width-2))
		} else {
			line3 = "  " + m.styles.Subtitle.Render(truncate(entry.Subtitle(), max(1, width-2)))
		}
		block := strings.Join([]string{line1, titleLine, line3}, "\n")
		if selected {
			lines = append(lines, m.styles.SidebarSelected.Copy().Width(width).Render(block))
		} else {
			lines = append(lines, lipgloss.NewStyle().Width(width).Render(block))
		}
		lines = append(lines, "")
	}
	for len(lines) < m.height {
		lines = append(lines, lipgloss.NewStyle().Width(width).Render(""))
	}
	return fitViewBox(strings.Join(lines, "\n"), width, m.height)
}

func sessionStatusLabel(status domain.AgentSessionStatus) string {
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

// truncate trims s to maxLen runes, appending "…" if truncated.
func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	if maxLen <= 1 {
		return "…"
	}
	return string(runes[:maxLen-1]) + "…"
}

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
const SidebarWidth = 26

type SidebarEntryKind int

const (
	SidebarEntryWorkItem SidebarEntryKind = iota
	SidebarEntrySessionHistory
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
	case SidebarEntrySessionHistory:
		if e.ExternalID != "" {
			return e.ExternalID
		}
		return e.SessionID
	default:
		if e.ExternalID != "" {
			return e.ExternalID
		}
		return e.WorkItemID
	}
}

// StatusIcon returns the styled status icon for the sidebar entry.
func (e SidebarEntry) StatusIcon(st styles.Styles) string {
	if e.Kind == SidebarEntrySessionHistory {
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
	if e.Kind == SidebarEntrySessionHistory {
		status := string(e.SessionStatus)
		switch e.SessionStatus {
		case domain.AgentSessionPending:
			status = "Pending"
		case domain.AgentSessionRunning:
			status = "Running"
		case domain.AgentSessionWaitingForAnswer:
			status = "Waiting for answer"
		case domain.AgentSessionCompleted:
			status = "Completed"
		case domain.AgentSessionInterrupted:
			status = "Interrupted"
		case domain.AgentSessionFailed:
			status = "Failed"
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
	switch e.State {
	case domain.WorkItemIngested:
		return "Ready to plan"
	case domain.WorkItemPlanning:
		return "Planning..."
	case domain.WorkItemPlanReview:
		return "Plan review needed"
	case domain.WorkItemApproved:
		return "Awaiting implementation"
	case domain.WorkItemImplementing:
		if e.HasOpenQuestion {
			return "Waiting for answer"
		}
		if e.HasInterrupted {
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
		return ""
	}
}

// SidebarModel manages the session list sidebar.
type SidebarModel struct {
	entries []SidebarEntry
	cursor  int
	styles  styles.Styles
	width   int
	height  int
}

// NewSidebarModel creates a new SidebarModel with the given styles.
func NewSidebarModel(st styles.Styles) SidebarModel {
	return SidebarModel{styles: st, width: SidebarWidth}
}

// SetEntries replaces the sidebar entries and preserves selection when possible.
func (m *SidebarModel) SetEntries(entries []SidebarEntry) {
	selectedWorkItemID := ""
	selectedSessionID := ""
	if current := m.Selected(); current != nil {
		selectedWorkItemID = current.WorkItemID
		selectedSessionID = current.SessionID
	}
	m.entries = entries
	for i, entry := range m.entries {
		if entry.WorkItemID == selectedWorkItemID && entry.SessionID == selectedSessionID {
			m.cursor = i
			return
		}
	}
	if m.cursor >= len(m.entries) && len(m.entries) > 0 {
		m.cursor = len(m.entries) - 1
	}
	if len(m.entries) == 0 {
		m.cursor = 0
	}
}

// SetWidth sets the available render width.
func (m *SidebarModel) SetWidth(w int) {
	m.width = max(0, w)
}

// SetHeight sets the available render height.
func (m *SidebarModel) SetHeight(h int) { m.height = h }

// MoveUp moves the cursor up by one entry.
func (m *SidebarModel) MoveUp() {
	if m.cursor > 0 {
		m.cursor--
	}
}

// MoveDown moves the cursor down by one entry.
func (m *SidebarModel) MoveDown() {
	if m.cursor < len(m.entries)-1 {
		m.cursor++
	}
}

// GotoTop moves the cursor to the first entry.
func (m *SidebarModel) GotoTop() {
	m.cursor = 0
}

// GotoBottom moves the cursor to the last entry.
func (m *SidebarModel) GotoBottom() {
	if len(m.entries) > 0 {
		m.cursor = len(m.entries) - 1
	}
}

// SelectWorkItem moves the cursor to the matching work item entry when present.
func (m *SidebarModel) SelectWorkItem(workItemID string) bool {
	for i, entry := range m.entries {
		if entry.Kind == SidebarEntryWorkItem && entry.WorkItemID == workItemID {
			m.cursor = i
			return true
		}
	}
	return false
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

	title := m.styles.Muted.Render("Sessions")
	header := lipgloss.NewStyle().Width(width).AlignHorizontal(lipgloss.Center).Render(title)
	lines = append(lines, header)
	lines = append(lines, m.styles.Muted.Render(strings.Repeat("─", width)))

	for i, entry := range m.entries {
		selected := i == m.cursor
		icon := entry.StatusIcon(m.styles)
		line1 := truncate(icon+" "+entry.titlePrefix(), width)
		titleLine := truncate("  "+entry.Title, width)
		var line3 string
		if entry.Kind == SidebarEntryWorkItem && entry.State == domain.WorkItemImplementing && entry.TotalSubPlans > 0 {
			bar := components.RenderProgressBar(entry.DoneSubPlans, entry.TotalSubPlans, max(1, width-4), "#5b8def", "#34d399", "#2d2d44")
			line3 = "  " + truncate(bar, max(1, width-2))
		} else {
			line3 = "  " + m.styles.Subtitle.Render(truncate(entry.Subtitle(), max(1, width-2)))
		}
		block := strings.Join([]string{line1, titleLine, line3}, "\n")
		if selected {
			lines = append(lines, lipgloss.NewStyle().Width(width).Background(lipgloss.Color("#1e293b")).Render(block))
		} else {
			lines = append(lines, lipgloss.NewStyle().Width(width).Render(block))
		}
		lines = append(lines, "")
	}
	for len(lines) < m.height {
		lines = append(lines, lipgloss.NewStyle().Width(width).Render(""))
	}
	return strings.Join(lines, "\n")
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

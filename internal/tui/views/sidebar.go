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

// SessionSummary aggregates all display info for one sidebar entry.
type SessionSummary struct {
	WorkItemID   string
	ExternalID   string
	Title        string
	State        domain.WorkItemState
	TotalSubPlans   int
	DoneSubPlans    int
	HasOpenQuestion bool
	HasInterrupted  bool
	CompletedAt *time.Time
	FailedAt    *time.Time
}

// StatusIcon returns the styled status icon for this session.
func (s SessionSummary) StatusIcon(st styles.Styles) string {
	switch {
	case s.State == domain.WorkItemCompleted:
		return st.Success.Render("✓")
	case s.State == domain.WorkItemFailed:
		return st.Error.Render("✗")
	case s.State == domain.WorkItemImplementing && s.HasInterrupted:
		return st.Interrupted.Render("⊘")
	case s.State == domain.WorkItemPlanReview, s.State == domain.WorkItemImplementing && s.HasOpenQuestion:
		return st.Warning.Render("◐")
	case s.State == domain.WorkItemImplementing || s.State == domain.WorkItemPlanning || s.State == domain.WorkItemReviewing:
		return st.Active.Render("●")
	case s.HasOpenQuestion || s.HasInterrupted:
		return st.Warning.Render("◐")
	default:
		return st.Muted.Render("◌")
	}
}

// Subtitle returns the human-readable status line for this session.
func (s SessionSummary) Subtitle() string {
	switch s.State {
	case domain.WorkItemIngested:
		return "Ready to plan"
	case domain.WorkItemPlanning:
		return "Planning..."
	case domain.WorkItemPlanReview:
		return "Plan review needed"
	case domain.WorkItemApproved:
		return "Awaiting implementation"
	case domain.WorkItemImplementing:
		if s.HasOpenQuestion {
			return "Waiting for answer"
		}
		if s.HasInterrupted {
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
	sessions []SessionSummary
	cursor   int
	styles   styles.Styles
	height   int
}

// NewSidebarModel creates a new SidebarModel with the given styles.
func NewSidebarModel(st styles.Styles) SidebarModel {
	return SidebarModel{styles: st}
}

// SetSessions replaces the session list and clamps the cursor.
func (m *SidebarModel) SetSessions(sessions []SessionSummary) {
	m.sessions = sessions
	if m.cursor >= len(m.sessions) && len(m.sessions) > 0 {
		m.cursor = len(m.sessions) - 1
	}
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
	if m.cursor < len(m.sessions)-1 {
		m.cursor++
	}
}

// GotoTop moves the cursor to the first session entry.
func (m *SidebarModel) GotoTop() {
	m.cursor = 0
}

// GotoBottom moves the cursor to the last session entry.
func (m *SidebarModel) GotoBottom() {
	if len(m.sessions) > 0 {
		m.cursor = len(m.sessions) - 1
	}
}

// Selected returns a pointer to the currently selected session, or nil if none.
func (m *SidebarModel) Selected() *SessionSummary {
	if len(m.sessions) == 0 || m.cursor < 0 || m.cursor >= len(m.sessions) {
		return nil
	}
	s := m.sessions[m.cursor]
	return &s
}

// View renders the full sidebar.
func (m SidebarModel) View() string {
	if m.height <= 0 {
		return ""
	}
	var lines []string

	// Section header
	header := m.styles.Muted.Render("Sessions") +
		lipgloss.NewStyle().Foreground(lipgloss.Color("#5b8def")).Render("         F1")
	lines = append(lines, lipgloss.NewStyle().Width(SidebarWidth).Render(header))
	lines = append(lines, m.styles.Muted.Render(strings.Repeat("─", SidebarWidth)))

	for i, s := range m.sessions {
		selected := i == m.cursor

		icon := s.StatusIcon(m.styles)
		line1 := truncate(icon+" "+s.ExternalID, SidebarWidth)

		title := truncate("  "+s.Title, SidebarWidth)

		var line3 string
		if s.State == domain.WorkItemImplementing && s.TotalSubPlans > 0 {
			bar := components.RenderProgressBar(s.DoneSubPlans, s.TotalSubPlans, SidebarWidth-4, "#5b8def", "#34d399", "#2d2d44")
			line3 = "  " + truncate(bar, SidebarWidth-2)
		} else {
			line3 = "  " + m.styles.Subtitle.Render(truncate(s.Subtitle(), SidebarWidth-2))
		}

		entry := strings.Join([]string{line1, title, line3}, "\n")

		if selected {
			entryStyle := lipgloss.NewStyle().
				Width(SidebarWidth).
				Background(lipgloss.Color("#1e293b"))
			lines = append(lines, entryStyle.Render(entry))
		} else {
			lines = append(lines, lipgloss.NewStyle().Width(SidebarWidth).Render(entry))
		}
		lines = append(lines, "") // blank separator between entries
	}

	// Fill remaining space before footer
	for len(lines) < m.height-3 {
		lines = append(lines, lipgloss.NewStyle().Width(SidebarWidth).Render(""))
	}

	lines = append(lines, m.styles.Muted.Render(strings.Repeat("─", SidebarWidth)))
	footerLine := m.styles.KeybindAccent.Render("[n]") + m.styles.Muted.Render(" New  ") +
		m.styles.KeybindAccent.Render("[q]") + m.styles.Muted.Render(" Quit")
	lines = append(lines, lipgloss.NewStyle().Width(SidebarWidth).Render(footerLine))

	result := strings.Join(lines, "\n")
	sidebarStyle := lipgloss.NewStyle().
		Width(SidebarWidth).
		BorderRight(true).
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("#2d2d44"))
	return sidebarStyle.Render(result)
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

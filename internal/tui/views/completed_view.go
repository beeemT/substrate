package views

import (
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/beeemT/substrate/internal/tui/components"
	"github.com/beeemT/substrate/internal/tui/styles"
)

// MRInfo holds MR/PR link information for a repo.
type MRInfo struct {
	RepoName string
	MRURL    string
	MRRef    string
	State    string
	IsOpen   bool
}

// CompletedModel shows the implemented plan, PR/MR completion links, and
// review artifacts for a finished work item. It also hosts the follow-up
// feedback input when the user requests additional changes via [c].
//
//nolint:recvcheck // Bubble Tea: Update returns value, View on value receiver
type CompletedModel struct {
	title         string
	statusLabel   string
	completedAt   time.Time
	mrLinks       []MRInfo
	warnings      []string
	planContent   string
	styles        styles.Styles
	width         int
	height        int
	viewport      viewport.Model
	workItemID    string
	feedbackInput textinput.Model
	inputActive   bool
}

func NewCompletedModel(st styles.Styles) CompletedModel {
	ti := components.NewTextInput()
	ti.Placeholder = "Describe what needs to change..."
	ti.CharLimit = 2000
	return CompletedModel{
		styles:        st,
		statusLabel:   "Review artifacts",
		feedbackInput: ti,
		viewport:      viewport.New(0, 0),
	}
}

func (m *CompletedModel) SetSize(w, h int) {
	m.width = w
	m.height = h
	m.syncViewportSize()
}

func (m *CompletedModel) SetTitle(t string) { m.title = t }

func (m *CompletedModel) SetStatusLabel(label string) {
	m.statusLabel = label
	m.syncViewportSize()
}

func (m *CompletedModel) SetWorkItemID(id string) { m.workItemID = id }

func (m *CompletedModel) SetData(completedAt time.Time, mrLinks []MRInfo, warnings []string) {
	m.completedAt = completedAt
	m.mrLinks = mrLinks
	m.warnings = warnings
	m.syncViewportSize()
}

// SetPlan sets the full plan document text displayed in the scrollable viewport.
// The call is idempotent: identical content is a no-op.
func (m *CompletedModel) SetPlan(content string) {
	if m.planContent == content {
		return
	}
	m.planContent = content
	m.refreshViewportContent()
}

// syncViewportSize recomputes the viewport dimensions from current model state.
// The viewport occupies the upper body area; metadata and optional feedback
// rows are reserved below it as fixed chrome.
func (m *CompletedModel) syncViewportSize() {
	// Top chrome: header line (title + status label) + divider = 2 rows.
	reservedRows := 2

	// Blank separator between plan viewport and the metadata section below it.
	reservedRows++

	// Metadata rows.
	statusLabel := strings.TrimSpace(m.statusLabel)
	if !m.completedAt.IsZero() && statusLabel == "✓ Completed" {
		reservedRows += 2 // timestamp line + trailing blank
	}
	if len(m.mrLinks) > 0 {
		reservedRows += 1 + len(m.mrLinks) // "Repos:" label + one line per link
	}
	reservedRows += len(m.warnings)

	// Feedback area when active: divider + input line.
	if m.inputActive {
		reservedRows += 2
	}

	m.viewport.Width = max(1, m.width)
	m.viewport.Height = max(1, m.height-reservedRows)
	m.refreshViewportContent()
}

// refreshViewportContent re-renders the plan text into the viewport.
// renderPlanReviewContent (plan_review.go, same package) is reused so the
// line-numbered, width-wrapped display is consistent with the plan review overlay.
func (m *CompletedModel) refreshViewportContent() {
	if m.viewport.Width <= 0 {
		return
	}
	m.viewport.SetContent(renderPlanReviewContent(m.styles, m.planContent, m.viewport.Width))
}

func (m CompletedModel) InputCaptured() bool { return m.inputActive }

func (m CompletedModel) KeybindHints() []KeybindHint {
	if m.inputActive {
		return []KeybindHint{
			{Key: "Enter", Label: "Submit"},
			{Key: "Esc", Label: "Cancel"},
		}
	}
	hints := []KeybindHint{{Key: "Esc", Label: "Close"}}
	if m.workItemID != "" {
		hints = append(hints, KeybindHint{Key: "c", Label: "Changes"})
	}
	if len(m.mrLinks) > 0 {
		hints = append([]KeybindHint{{Key: "Enter", Label: "Open MR"}}, hints...)
	}
	if strings.TrimSpace(m.planContent) != "" {
		hints = append([]KeybindHint{{Key: "↑↓", Label: "Scroll"}}, hints...)
	}
	return hints
}

func (m CompletedModel) Update(msg tea.Msg) (CompletedModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.inputActive {
			switch msg.String() {
			case "enter":
				feedback := m.feedbackInput.Value()
				m.feedbackInput.SetValue("")
				m.inputActive = false
				m.feedbackInput.Blur()
				m.syncViewportSize()
				return m, func() tea.Msg {
					return FollowUpPlanMsg{WorkItemID: m.workItemID, Feedback: feedback}
				}
			case "esc":
				m.feedbackInput.SetValue("")
				m.inputActive = false
				m.feedbackInput.Blur()
				m.syncViewportSize()
				return m, nil
			default:
				var cmd tea.Cmd
				m.feedbackInput, cmd = m.feedbackInput.Update(msg)
				return m, cmd
			}
		}

		switch msg.String() {
		case "up", "k", "pgup", "down", "j", "pgdown":
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd
		case "enter":
			// Open the first (or only) MR link. Multi-repo sessions are rare
			// and all links are listed in the view; Enter opens the primary one.
			if len(m.mrLinks) > 0 && strings.TrimSpace(m.mrLinks[0].MRURL) != "" {
				url := m.mrLinks[0].MRURL
				return m, func() tea.Msg { return OpenExternalURLMsg{URL: url} }
			}
		case "c":
			if m.workItemID != "" {
				m.inputActive = true
				m.syncViewportSize()
				return m, m.feedbackInput.Focus()
			}
		}

	case tea.MouseMsg:
		// Forward wheel events to the plan viewport so trackpad/mouse scrolling
		// works while the overlay is open and input is not captured.
		if msg.Action == tea.MouseActionPress {
			switch msg.Button {
			case tea.MouseButtonWheelUp, tea.MouseButtonWheelDown:
				var cmd tea.Cmd
				m.viewport, cmd = m.viewport.Update(msg)
				return m, cmd
			}
		}
	}

	return m, nil
}

func (m CompletedModel) View() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}
	statusLabel := strings.TrimSpace(m.statusLabel)
	if statusLabel == "" {
		statusLabel = "Review artifacts"
	}

	header := m.styles.Title.Render(m.title) + "  " + m.styles.Success.Render(statusLabel)
	divider := components.RenderDivider(m.styles, m.width)

	// Scrollable plan body occupies the upper portion of the overlay.
	planBody := m.viewport.View()
	if strings.TrimSpace(planBody) == "" {
		planBody = m.styles.Muted.Render("No plan content available.")
	}

	// Fixed metadata section rendered below the plan viewport.
	var metaLines []string
	metaLines = append(metaLines, "") // blank separator between plan and metadata
	if !m.completedAt.IsZero() && statusLabel == "✓ Completed" {
		metaLines = append(metaLines, m.styles.Subtitle.Render("Completed "+m.completedAt.Format("2006-01-02 15:04 MST")), "")
	}
	if len(m.mrLinks) > 0 {
		metaLines = append(metaLines, m.styles.SectionLabel.Render("Repos:"))
		for _, mr := range m.mrLinks {
			icon := m.styles.Success.Render("✓")
			status := ""
			if mr.MRRef != "" {
				status = "  " + m.styles.Active.Render(mr.MRRef)
			}
			if strings.TrimSpace(mr.State) != "" {
				status += "  " + m.styles.Muted.Render(strings.TrimSpace(mr.State))
			}
			metaLines = append(metaLines, "  "+icon+" "+m.styles.Subtitle.Render(mr.RepoName)+status)
		}
	}
	for _, w := range m.warnings {
		metaLines = append(metaLines, m.styles.Warning.Render("⚠ "+w))
	}

	parts := []string{header, divider, planBody}
	parts = append(parts, metaLines...)
	if m.inputActive {
		parts = append(parts, components.RenderDivider(m.styles, m.width), m.feedbackInput.View())
	}

	return fitViewBox(strings.Join(parts, "\n"), m.width, m.height)
}

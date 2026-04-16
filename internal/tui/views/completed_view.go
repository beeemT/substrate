package views

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/beeemT/substrate/internal/tui/components"
	"github.com/beeemT/substrate/internal/tui/styles"
)

// CompletedModel shows the implemented plan and hosts the follow-up feedback
// input when the user requests additional changes. Completion metadata (timestamp,
// MR/PR links) belongs on the overview page and is intentionally absent here.
//
//nolint:recvcheck // Bubble Tea: Update returns value, View on value receiver
type CompletedModel struct {
	title         string
	statusLabel   string
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

func (m *CompletedModel) SetTitle(t string)           { m.title = t }
func (m *CompletedModel) SetStatusLabel(label string) { m.statusLabel = label }
func (m *CompletedModel) SetWorkItemID(id string)     { m.workItemID = id }

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
// Reserved rows: 2 (header + divider) + [2 if feedback active (divider + input)].
func (m *CompletedModel) syncViewportSize() {
	reserved := 2 // header + divider
	if m.inputActive {
		reserved += 2 // divider + input line
	}
	m.viewport.Width = max(1, m.width)
	m.viewport.Height = max(1, m.height-reserved)
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
	var hints []KeybindHint
	if strings.TrimSpace(m.planContent) != "" {
		hints = append(hints, KeybindHint{Key: "↑↓", Label: "Scroll"})
	}
	if m.workItemID != "" {
		hints = append(hints, KeybindHint{Key: "Enter", Label: "Request changes"})
	}
	hints = append(hints, KeybindHint{Key: "Esc", Label: "Close"})
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
		case "enter", "c":
			// Both Enter and [c] open the follow-up feedback input.
			// Enter is the primary CTA visible in the hint bar;
			// [c] is the keyboard shortcut consistent with the action card.
			if m.workItemID != "" {
				m.inputActive = true
				m.syncViewportSize()
				return m, m.feedbackInput.Focus()
			}
		}

	case tea.MouseMsg:
		// Forward wheel events to the plan viewport so trackpad/mouse scrolling
		// works while the overlay is open and the feedback input is not active.
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

	planBody := m.viewport.View()
	if strings.TrimSpace(planBody) == "" {
		planBody = m.styles.Muted.Render("No plan content available.")
	}

	parts := []string{header, divider, planBody}
	if m.inputActive {
		parts = append(parts, components.RenderDivider(m.styles, m.width), m.feedbackInput.View())
	}

	return fitViewBox(strings.Join(parts, "\n"), m.width, m.height)
}

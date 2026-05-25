package views

import (
	"log/slog"
	"strings"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/beeemT/substrate/internal/tui/components"
	"github.com/beeemT/substrate/internal/tui/styles"
)

// completedScrollSource identifies GrowingTextAreaScrollMsg events from the
// completed-view follow-up input so they route to the plan viewport.
const completedScrollSource = "completed-feedback"

// completedFeedbackMaxLines caps the visual height of the follow-up textarea.
// completedFeedbackCharLimit keeps long pasted research usable while still bounding
// pathological input size; display height remains capped separately.
const (
	completedFeedbackMaxLines  = 6
	completedFeedbackCharLimit = 20000
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
	feedbackInput components.GrowingTextArea
	inputActive   bool
}

func NewCompletedModel(st styles.Styles) CompletedModel {
	ta := components.NewGrowingTextArea(completedScrollSource)
	ta.SetMaxLines(completedFeedbackMaxLines)
	ta.SetPlaceholder("Describe what needs to change...")
	ta.SetCharLimit(completedFeedbackCharLimit)
	return CompletedModel{
		styles:        st,
		statusLabel:   "Review artifacts",
		feedbackInput: ta,
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
// Reserved rows: 2 (header + divider) + [1 + textarea height] when feedback active.
func (m *CompletedModel) syncViewportSize() {
	reserved := 2 // header + divider
	if m.inputActive {
		reserved += 1 + m.feedbackInput.Height() // divider + textarea rows
	}
	m.feedbackInput.SetWidth(max(1, m.width))
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

// OpenFeedback enters request-changes mode and focuses the follow-up textarea.
// Returns the cmd that focuses the input while preserving mouse reporting;
// callers MUST batch it into their reply.
func (m *CompletedModel) OpenFeedback() tea.Cmd {
	m.inputActive = true
	cmd := m.feedbackInput.FocusKeepMouse()
	m.syncViewportSize()
	return cmd
}

func (m *CompletedModel) CloseFeedback() tea.Cmd {
	if !m.inputActive && m.feedbackInput.Value() == "" {
		return nil
	}
	resetCmd := m.feedbackInput.Reset()
	m.inputActive = false
	m.syncViewportSize()
	return resetCmd
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
		hints = append(hints, KeybindHint{Key: "c", Label: "Copy"})
	}
	if m.workItemID != "" {
		hints = append(hints, KeybindHint{Key: "Enter", Label: "Request changes"})
	}
	hints = append(hints, KeybindHint{Key: "Esc", Label: "Close"})
	return hints
}

func (m CompletedModel) Update(msg tea.Msg) (CompletedModel, tea.Cmd) {
	switch msg := msg.(type) {
	case components.GrowingTextAreaScrollMsg:
		if msg.Source != completedScrollSource {
			return m, nil
		}
		switch {
		case msg.Delta < 0:
			m.viewport.ScrollUp(-msg.Delta)
		case msg.Delta > 0:
			m.viewport.ScrollDown(msg.Delta)
		}
		return m, nil
	case tea.KeyMsg:
		if m.inputActive {
			switch msg.String() {
			case "enter":
				m.feedbackInput.Flush()
				feedback := m.feedbackInput.Value()
				resetCmd := m.feedbackInput.Reset()
				trimmedFeedback := strings.TrimSpace(feedback)
				m.inputActive = false
				m.syncViewportSize()
				if trimmedFeedback == "" {
					return m, resetCmd
				}
				return m, tea.Batch(
					func() tea.Msg {
						return FollowUpPlanMsg{WorkItemID: m.workItemID, Feedback: feedback}
					},
					resetCmd,
				)
			case "esc":
				resetCmd := m.feedbackInput.Reset()
				m.inputActive = false
				m.syncViewportSize()
				return m, resetCmd
			default:
				var cmd tea.Cmd
				m.feedbackInput, cmd = m.feedbackInput.Update(msg)
				m.syncViewportSize()
				return m, cmd
			}
		}

		switch msg.String() {
		case "up", "pgup", keyDown, "pgdown":
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd
		case "enter":
			if m.workItemID != "" {
				return m, m.OpenFeedback()
			}
		case "c":
			if clipErr := clipboard.WriteAll(m.planContent); clipErr != nil {
				slog.Warn("failed to copy plan to clipboard", "error", clipErr)
			}
			return m, func() tea.Msg { return ActionDoneMsg{Message: "Plan copied to clipboard"} }
		}

	case tea.MouseMsg:
		// Forward wheel events to the plan viewport so trackpad/mouse scrolling
		// works while the overlay is open, including while feedback is active.
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

package views

import (
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/tui/components"
	"github.com/beeemT/substrate/internal/tui/styles"
)

const (
	questionReplyScrollSource = "question-reply"
	questionReplyMaxLines     = 6
)

// QuestionModel handles human-in-the-loop question resolution.
type QuestionModel struct { //nolint:recvcheck // Bubble Tea: Update returns value, View on value receiver
	question         domain.Question
	foremanProposed  string
	foremanUncertain bool
	viewport         viewport.Model
	input            components.GrowingTextArea
	inputActive      bool
	title            string
	styles           styles.Styles
	width            int
	height           int
}

// NewQuestionModel constructs a QuestionModel with the given styles.
func NewQuestionModel(st styles.Styles) QuestionModel {
	ti := components.NewGrowingTextArea(questionReplyScrollSource)
	ti.SetPlaceholder("Type answer…")
	ti.SetCharLimit(1000)
	ti.SetMaxLines(questionReplyMaxLines)

	return QuestionModel{viewport: viewport.New(0, 0), input: ti, styles: st}
}

// SetSize updates the layout dimensions.
func (m *QuestionModel) SetSize(w, h int) {
	m.width = w
	m.height = h
	m.syncViewportSize(false)
}

// SetTitle sets the work-item title shown in the question view.
func (m *QuestionModel) SetTitle(t string) { m.title = t }

// SetQuestion loads a question into the model and activates the input.
func (m *QuestionModel) SetQuestion(q domain.Question, proposed string, uncertain bool) {
	sameQuestion := m.question.ID != "" && m.question.ID == q.ID
	m.question = q
	m.foremanProposed = proposed
	m.foremanUncertain = uncertain
	if sameQuestion {
		if !m.inputActive {
			m.inputActive = true
			m.input.Focus()
		}
		m.syncViewportSize(false)

		return
	}
	m.input.SetValue("")
	m.inputActive = true
	m.input.Focus()
	m.syncViewportSize(true)
}

// Close clears the draft answer and deactivates the input without resolving the question.
func (m *QuestionModel) Close() tea.Cmd {
	m.inputActive = false
	m.viewport.GotoTop()
	return m.input.Reset()
}

// KeybindHints returns the keybind hints for the status bar.
func (m QuestionModel) KeybindHints() []KeybindHint {
	return []KeybindHint{
		{Key: "Enter", Label: "Send answer"},
		{Key: "PgUp/PgDn", Label: "Scroll"},
		{Key: "Esc", Label: "Close"},
	}
}

// Update handles messages and input for QuestionModel.
func (m QuestionModel) Update(msg tea.Msg) (QuestionModel, tea.Cmd) {
	var cmd tea.Cmd
	if msg, ok := msg.(components.GrowingTextAreaScrollMsg); ok {
		if msg.Source != questionReplyScrollSource {
			return m, nil
		}
		switch {
		case msg.Delta < 0:
			m.viewport.ScrollUp(-msg.Delta)
		case msg.Delta > 0:
			m.viewport.ScrollDown(msg.Delta)
		}

		return m, nil
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case keyEnter:
			if !m.inputActive {
				return m, nil
			}
			m.input.Flush()
			answer := strings.TrimSpace(m.input.Value())
			if answer == "" {
				return m, nil
			}
			qID := m.question.ID
			m.inputActive = false

			return m, tea.Batch(
				func() tea.Msg {
					return AnswerQuestionMsg{QuestionID: qID, Answer: answer, AnsweredBy: "human"}
				},
				m.input.Reset(),
			)

		case keyEsc:
			return m, m.Close()

		case keyPgUp, keyPgDown:
			m.viewport, cmd = m.viewport.Update(msg)

		case "up", "k":
			if m.inputActive && m.input.AtTop() {
				m.viewport.ScrollUp(1)

				return m, nil
			}
			if m.inputActive {
				m.input, cmd = m.input.Update(msg)
				m.syncViewportSize(false)
			}

		case keyDown, "j":
			if m.inputActive && m.input.AtBottom() {
				m.viewport.ScrollDown(1)

				return m, nil
			}
			if m.inputActive {
				m.input, cmd = m.input.Update(msg)
				m.syncViewportSize(false)
			}

		default:
			if m.inputActive {
				m.input, cmd = m.input.Update(msg)
				m.syncViewportSize(false)
			}
		}

	case tea.MouseMsg:
		if msg.Action == tea.MouseActionPress {
			switch msg.Button {
			case tea.MouseButtonWheelUp, tea.MouseButtonWheelDown:
				m.viewport, cmd = m.viewport.Update(msg)
			}
		}

	case tea.WindowSizeMsg:
		m.syncViewportSize(false)
		m.viewport, cmd = m.viewport.Update(msg)

	default:
		m.viewport, cmd = m.viewport.Update(msg)
	}

	return m, cmd
}

// View renders the question escalation UI.
func (m QuestionModel) View() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}
	if m.viewport.Width != max(1, m.width-1) || m.viewport.Height != m.viewportHeight() || m.viewport.TotalLineCount() == 0 {
		m.syncViewportSize(false)
	}

	header := m.renderHeader()
	body := m.viewport.View()
	if strings.TrimSpace(body) == "" {
		body = m.styles.Muted.Render("No question content available.")
	} else if sb := renderViewportScrollbar(m.styles, m.viewport, m.viewport.Height, true); sb != "" {
		body = lipgloss.JoinHorizontal(lipgloss.Top, body, sb)
	}

	parts := append(strings.Split(header, "\n"), body, "", m.replyLabel(), m.input.View())

	return fitViewBox(strings.Join(parts, "\n"), m.width, m.height)
}

func (m *QuestionModel) syncViewportSize(reset bool) {
	if m.width <= 0 || m.height <= 0 {
		return
	}
	m.input.SetWidth(max(1, m.width))
	m.viewport.Width = max(1, m.width-1)
	m.viewport.Height = m.viewportHeight()
	m.refreshViewportContent(reset)
}

func (m QuestionModel) viewportHeight() int {
	if m.width <= 0 || m.height <= 0 {
		return 1
	}
	reserved := len(strings.Split(m.renderHeader(), "\n"))
	reserved += 1 // blank line before answer input
	reserved += len(strings.Split(m.replyLabel(), "\n"))
	reserved += m.input.Height()

	return max(1, m.height-reserved)
}

func (m *QuestionModel) refreshViewportContent(reset bool) {
	if m.viewport.Width <= 0 {
		return
	}
	previousOffset := m.viewport.YOffset
	m.viewport.SetContent(m.renderScrollableContent(m.viewport.Width))
	if reset {
		m.viewport.GotoTop()

		return
	}
	maxOffset := max(0, m.viewport.TotalLineCount()-m.viewport.Height)
	if previousOffset > maxOffset {
		previousOffset = maxOffset
	}
	if previousOffset < 0 {
		previousOffset = 0
	}
	m.viewport.YOffset = previousOffset
}

func (m QuestionModel) renderHeader() string {
	stageLabel := "Implementing"
	if m.question.Stage == domain.AgentSessionPhasePlanning {
		stageLabel = "Planning"
	}

	return components.RenderHeaderBlock(m.styles, components.HeaderBlockSpec{
		Title:   m.title + " · " + stageLabel + "  ◐ Question",
		Width:   m.width,
		Divider: true,
	})
}

func (m QuestionModel) replyLabel() string {
	if m.question.Stage == domain.AgentSessionPhasePlanning {
		return m.styles.Subtitle.Render("Reply to planner:")
	}

	return m.styles.Subtitle.Render("Your answer:")
}

func (m QuestionModel) renderScrollableContent(width int) string {
	stageLabel := "Agent question:"
	intro := ""
	showForeman := m.question.Stage != domain.AgentSessionPhasePlanning
	if m.question.Stage == domain.AgentSessionPhasePlanning {
		stageLabel = "Planning question"
		intro = m.styles.Subtitle.Render("The planner needs your input before it can continue.")
	}

	questionBody := m.question.Content
	if m.question.Structured != nil && len(m.question.Structured.Questions) > 0 {
		questionBody = renderStructuredQuestionSummary(*m.question.Structured)
	}
	questionBox := components.RenderCallout(m.styles, components.CalloutSpec{
		Body:    questionBody,
		Width:   width,
		Variant: components.CalloutWarning,
	})

	middleBlocks := []string{m.styles.Warning.Render(stageLabel), questionBox}
	if intro != "" {
		middleBlocks = append([]string{intro, ""}, middleBlocks...)
	}
	if m.question.Context != "" {
		middleBlocks = append(middleBlocks, m.styles.Subtitle.Render("Context: "+m.question.Context))
	}
	if showForeman && strings.TrimSpace(m.foremanProposed) != "" {
		uncertainLabel := ""
		if m.foremanUncertain {
			uncertainLabel = m.styles.Warning.Render(" (uncertain)")
		}
		foremanLabel := m.styles.Subtitle.Render("Foreman's proposed answer") + uncertainLabel
		foremanBox := components.RenderCallout(m.styles, components.CalloutSpec{
			Body:  m.foremanProposed,
			Width: width,
		})
		middleBlocks = append(middleBlocks, "", foremanLabel, foremanBox)
	}

	return strings.Join(middleBlocks, "\n")
}

func renderStructuredQuestionSummary(set domain.StructuredQuestionSet) string {
	var b strings.Builder
	for i, q := range set.Questions {
		if i > 0 {
			b.WriteString("\n\n")
		}
		if q.Header != "" {
			b.WriteString(q.Header)
			b.WriteString(": ")
		}
		b.WriteString(q.Question)
		for idx, opt := range q.Options {
			b.WriteString("\n  - ")
			b.WriteString(opt.Label)
			if q.RecommendedIndex != nil && *q.RecommendedIndex == idx {
				b.WriteString(" (recommended)")
			}
			if opt.Description != "" {
				b.WriteString(" — ")
				b.WriteString(opt.Description)
			}
		}
		if q.MultiSelect {
			b.WriteString("\n  Multi-select allowed.")
		}
	}
	return b.String()
}

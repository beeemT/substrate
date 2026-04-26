package views

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

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
	ti.SetPlaceholder("Type reply…")
	ti.SetCharLimit(1000)
	ti.SetMaxLines(questionReplyMaxLines)

	return QuestionModel{input: ti, styles: st}
}

// SetSize updates the layout dimensions.
func (m *QuestionModel) SetSize(w, h int) { m.width = w; m.height = h }

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

		return
	}
	m.input.SetValue("")
	m.inputActive = true
	m.input.Focus()
	m.input.SetWidth(max(1, m.width))
}

// KeybindHints returns the keybind hints for the status bar.
func (m QuestionModel) KeybindHints() []KeybindHint {
	if m.question.Stage == domain.TaskPhasePlanning {
		return []KeybindHint{{Key: "A", Label: "Send answer"}, {Key: "Esc", Label: "Skip"}}
	}
	return []KeybindHint{
		{Key: "A", Label: "Approve Foreman answer"},
		{Key: "Enter", Label: "Send to Foreman"},
		{Key: "Esc", Label: "Skip"},
	}
}

// Update handles messages and input for QuestionModel.
func (m QuestionModel) Update(msg tea.Msg) (QuestionModel, tea.Cmd) {
	var cmd tea.Cmd
	if msg, ok := msg.(components.GrowingTextAreaScrollMsg); ok {
		if msg.Source == questionReplyScrollSource {
			return m, nil
		}
	}
	if msg, ok := msg.(tea.KeyMsg); ok {
		switch msg.String() {
		case "A":
			m.input.Flush()
			answer := m.foremanProposed
			if answer == "" {
				answer = m.input.Value()
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
			qID := m.question.ID
			m.inputActive = false

			return m, tea.Batch(
				func() tea.Msg { return SkipQuestionMsg{QuestionID: qID} },
				m.input.Reset(),
			)

		case keyEnter:
			if m.question.Stage == domain.TaskPhasePlanning {
				break
			}
			if m.inputActive {
				m.input.Flush()
				text := m.input.Value()
				qID := m.question.ID

				return m, tea.Batch(
					func() tea.Msg {
						return SendToForemanMsg{QuestionID: qID, Message: text}
					},
					m.input.Reset(),
				)
			}

		default:
			if m.inputActive {
				m.input, cmd = m.input.Update(msg)
				m.input.SetWidth(max(1, m.width))
			}
		}
	}

	return m, cmd
}

// View renders the question escalation UI.
func (m QuestionModel) View() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}
	stageLabel := "Implementing"
	questionLabelText := "Agent question:"
	intro := ""
	showForeman := m.question.Stage != domain.TaskPhasePlanning
	if m.question.Stage == domain.TaskPhasePlanning {
		stageLabel = "Planning"
		questionLabelText = "Planning question"
		intro = m.styles.Subtitle.Render("The planner needs your input before it can continue.")
	}

	header := components.RenderHeaderBlock(m.styles, components.HeaderBlockSpec{
		Title:   m.title + " · " + stageLabel + "  ◐ Question",
		Width:   m.width,
		Divider: true,
	})

	questionLabel := m.styles.Warning.Render(questionLabelText)
	questionBody := m.question.Content
	if m.question.Structured != nil && len(m.question.Structured.Questions) > 0 {
		questionBody = renderStructuredQuestionSummary(*m.question.Structured)
	}
	questionBox := components.RenderCallout(m.styles, components.CalloutSpec{
		Body:    questionBody,
		Width:   m.width,
		Variant: components.CalloutWarning,
	})

	ctxBlock := ""
	if m.question.Context != "" {
		ctxBlock = m.styles.Subtitle.Render("Context: " + m.question.Context)
	}

	uncertainLabel := ""
	if m.foremanUncertain {
		uncertainLabel = m.styles.Warning.Render(" (uncertain)")
	}
	foremanLabel := m.styles.Subtitle.Render("Foreman's proposed answer") + uncertainLabel
	foremanBox := components.RenderCallout(m.styles, components.CalloutSpec{
		Body:  m.foremanProposed,
		Width: m.width,
	})

	replyLabel := m.styles.Subtitle.Render("Reply to planner:")
	if showForeman {
		replyLabel = m.styles.Subtitle.Render("Your reply (or press ") +
			m.styles.KeybindAccent.Render("[A]") +
			m.styles.Subtitle.Render(" to approve):")
	}
	m.input.SetWidth(max(1, m.width))
	headerLines := strings.Split(header, "\n")
	middleBlocks := []string{questionLabel, questionBox}
	if intro != "" {
		middleBlocks = append([]string{intro, ""}, middleBlocks...)
	}
	if ctxBlock != "" {
		middleBlocks = append(middleBlocks, ctxBlock)
	}
	if showForeman {
		middleBlocks = append(middleBlocks, "", foremanLabel, foremanBox)
	}
	footerBlocks := []string{"", replyLabel, m.input.View()}
	reserved := len(headerLines)
	for _, block := range footerBlocks {
		reserved += len(strings.Split(block, "\n"))
	}
	middleHeight := max(0, m.height-reserved)
	middle := fitViewHeight(strings.Join(middleBlocks, "\n"), middleHeight)
	parts := append([]string{}, headerLines...)
	if middle != "" {
		parts = append(parts, strings.Split(middle, "\n")...)
	}
	parts = append(parts, footerBlocks...)

	return fitViewBox(strings.Join(parts, "\n"), m.width, m.height)
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

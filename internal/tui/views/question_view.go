package views

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/tui/components"
	"github.com/beeemT/substrate/internal/tui/styles"
)

// QuestionModel handles human-in-the-loop question resolution.
type QuestionModel struct {
	question         domain.Question
	foremanProposed  string
	foremanUncertain bool
	input            textinput.Model
	inputActive      bool
	title            string
	styles           styles.Styles
	width            int
	height           int
}

// NewQuestionModel constructs a QuestionModel with the given styles.
func NewQuestionModel(st styles.Styles) QuestionModel {
	ti := textinput.New()
	ti.Placeholder = "Type reply to Foreman…"
	ti.CharLimit = 1000
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
}

// KeybindHints returns the keybind hints for the status bar.
func (m QuestionModel) KeybindHints() []KeybindHint {
	return []KeybindHint{
		{Key: "A", Label: "Approve Foreman answer"},
		{Key: "Enter", Label: "Send to Foreman"},
		{Key: "Esc", Label: "Skip"},
	}
}

// Update handles messages and input for QuestionModel.
func (m QuestionModel) Update(msg tea.Msg) (QuestionModel, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "A":
			answer := m.foremanProposed
			if answer == "" {
				answer = m.input.Value()
			}
			qID := m.question.ID
			m.inputActive = false
			m.input.Blur()
			return m, func() tea.Msg {
				return AnswerQuestionMsg{QuestionID: qID, Answer: answer, AnsweredBy: "human"}
			}

		case "esc":
			qID := m.question.ID
			m.inputActive = false
			m.input.Blur()
			return m, func() tea.Msg { return SkipQuestionMsg{QuestionID: qID} }

		case "enter":
			if m.inputActive {
				text := m.input.Value()
				m.input.SetValue("")
				qID := m.question.ID
				return m, func() tea.Msg {
					return SendToForemanMsg{QuestionID: qID, Message: text}
				}
			}

		default:
			if m.inputActive {
				m.input, cmd = m.input.Update(msg)
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
	header := components.RenderHeaderBlock(m.styles, components.HeaderBlockSpec{
		Title:   m.title + " · Implementing  ◐ Question",
		Width:   m.width,
		Divider: true,
	})

	questionLabel := m.styles.Warning.Render("Agent question:")
	questionBox := components.RenderCallout(m.styles, components.CalloutSpec{
		Body:    m.question.Content,
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

	replyLabel := m.styles.Subtitle.Render("Your reply (or press ") +
		m.styles.KeybindAccent.Render("[A]") +
		m.styles.Subtitle.Render(" to approve):")
	hints := renderOverlayHintsRow(m.styles, m.KeybindHints(), m.width)

	headerLines := strings.Split(header, "\n")
	middleBlocks := []string{questionLabel, questionBox}
	if ctxBlock != "" {
		middleBlocks = append(middleBlocks, ctxBlock)
	}
	middleBlocks = append(middleBlocks, "", foremanLabel, foremanBox)
	footerBlocks := []string{"", replyLabel, m.input.View(), "", hints}
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

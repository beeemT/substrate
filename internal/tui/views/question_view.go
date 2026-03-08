package views

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/beeemT/substrate/internal/domain"
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
	m.question = q
	m.foremanProposed = proposed
	m.foremanUncertain = uncertain
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
	titleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#f0f0f0")).Bold(true)
	dividerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#2d2d44"))
	questionStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#fbbf24")).Bold(true)
	contextStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#b0b0b0"))
	subtleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#b0b0b0"))
	accentStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#5b8def")).Bold(true)
	hintsStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#6b7280"))

	contentWidth := m.width - 2
	if contentWidth < 1 {
		contentWidth = 1
	}

	header := titleStyle.Render(m.title + " · Implementing  ◐ Question")
	divider := dividerStyle.Render(strings.Repeat("─", m.width))

	qLabel := questionStyle.Render("Agent question:")
	qContent := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#f0f0f0")).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#2d2d44")).
		Width(contentWidth).
		Padding(0, 1).
		Render(m.question.Content)

	var ctxBlock string
	if m.question.Context != "" {
		ctxBlock = contextStyle.Render("Context: " + m.question.Context)
	}

	uncertainLabel := ""
	if m.foremanUncertain {
		uncertainLabel = lipgloss.NewStyle().Foreground(lipgloss.Color("#fbbf24")).Render(" (uncertain)")
	}
	foremanLabel := subtleStyle.Render("Foreman's proposed answer") + uncertainLabel
	foremanBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#5b8def")).
		Width(contentWidth).
		Padding(0, 1).
		Render(m.foremanProposed)

	replyLabel := subtleStyle.Render("Your reply (or press ") +
		accentStyle.Render("[A]") +
		subtleStyle.Render(" to approve):")
	inputBox := m.input.View()

	hints := hintsStyle.Render("[Enter] Send to Foreman  [A] Approve & forward  [Esc] Skip")

	parts := []string{header, divider, qLabel, qContent}
	if ctxBlock != "" {
		parts = append(parts, ctxBlock)
	}
	parts = append(parts, "", foremanLabel, foremanBox, "", replyLabel, inputBox, "", hints)
	return strings.Join(parts, "\n")
}

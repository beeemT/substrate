package views

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/tui/components"
	"github.com/beeemT/substrate/internal/tui/styles"
)

type planReviewInputMode int

const (
	planReviewNormal  planReviewInputMode = iota
	planReviewChanges                     // user pressed [c]: request changes
	planReviewReject                      // user pressed [r]: rejection reason
)

// PlanReviewModel renders the plan and handles approval flow.
type PlanReviewModel struct {
	viewport      viewport.Model
	feedbackInput textinput.Model
	inputMode     planReviewInputMode
	title         string
	planID        string
	workItemID    string
	planContent   string
	styles        styles.Styles
	width         int
	height        int
}

func NewPlanReviewModel(st styles.Styles) PlanReviewModel {
	ti := textinput.New()
	ti.CharLimit = 500
	return PlanReviewModel{
		viewport:      viewport.New(0, 0),
		feedbackInput: ti,
		styles:        st,
	}
}

func (m *PlanReviewModel) SetSize(width, height int) {
	m.width = width
	m.height = height
	m.syncViewportSize()
}

func (m *PlanReviewModel) syncViewportSize() {
	reservedRows := 4 // header block + divider above hints + hint row
	if m.inputMode != planReviewNormal {
		reservedRows++
	}
	m.viewport.Width = m.width
	m.viewport.Height = max(1, m.height-reservedRows)
	m.refreshViewportContent(false)
}

func (m *PlanReviewModel) SetTitle(title string) { m.title = title }

func (m *PlanReviewModel) SetPlanDocument(planID, content string) {
	reset := m.planID != planID || m.planContent != content
	m.planID = planID
	m.planContent = content
	m.refreshViewportContent(reset)
}

func (m *PlanReviewModel) refreshViewportContent(reset bool) {
	previousOffset := m.viewport.YOffset
	if m.viewport.Width <= 0 {
		return
	}
	m.viewport.SetContent(renderPlanReviewContent(m.styles, m.planContent, m.viewport.Width))
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

func renderPlanReviewContent(st styles.Styles, content string, width int) string {
	if width <= 0 {
		return ""
	}
	trimmed := strings.TrimSuffix(content, "\n")
	if trimmed == "" {
		return ""
	}
	rawLines := strings.Split(trimmed, "\n")
	numberWidth := max(2, len(strconv.Itoa(len(rawLines))))
	separator := " │ "
	contentWidth := max(1, width-numberWidth-ansi.StringWidth(separator))
	rendered := make([]string, 0, len(rawLines))
	inCodeBlock := false
	for index, rawLine := range rawLines {
		trimmedLine := strings.TrimSpace(rawLine)
		segments := []string{""}
		renderMarkdown := false
		style := st.SettingsText
		switch {
		case inCodeBlock || strings.HasPrefix(trimmedLine, "```"):
			segments = wrapPlanReviewCodeLine(rawLine, contentWidth)
			if strings.HasPrefix(trimmedLine, "```") {
				style = st.Muted
			}
		case planReviewLineUsesMarkdown(trimmedLine):
			segments = renderPlanReviewMarkdownLine(rawLine, contentWidth)
			renderMarkdown = true
		case trimmedLine == "":
			segments = []string{""}
		default:
			segments = wrapPlanReviewPlainTextLine(rawLine, contentWidth)
		}
		for wrappedIndex, segment := range segments {
			lineNumber := strings.Repeat(" ", numberWidth)
			if wrappedIndex == 0 {
				lineNumber = fmt.Sprintf("%*d", numberWidth, index+1)
			}
			renderedSegment := segment
			if !renderMarkdown {
				renderedSegment = style.Render(segment)
			}
			renderedSegment = lipgloss.NewStyle().Width(contentWidth).Render(renderedSegment)
			rendered = append(rendered, st.Muted.Render(lineNumber+separator)+renderedSegment)
		}
		if strings.HasPrefix(trimmedLine, "```") {
			inCodeBlock = !inCodeBlock
		}
	}
	return strings.Join(rendered, "\n")
}

func planReviewLineUsesMarkdown(trimmedLine string) bool {
	if trimmedLine == "" {
		return false
	}
	if strings.HasPrefix(trimmedLine, "#") || strings.HasPrefix(trimmedLine, "- ") || strings.HasPrefix(trimmedLine, "* ") || strings.HasPrefix(trimmedLine, "+ ") || strings.Contains(trimmedLine, "**") || strings.Contains(trimmedLine, "__") || strings.Contains(trimmedLine, "[") && strings.Contains(trimmedLine, "](") {
		return true
	}
	for i, r := range trimmedLine {
		if r < '0' || r > '9' {
			return i > 0 && r == '.' && i+1 < len(trimmedLine) && trimmedLine[i+1] == ' '
		}
	}
	return false
}

func renderPlanReviewMarkdownLine(line string, width int) []string {
	if width <= 0 {
		return []string{line}
	}
	if strings.TrimSpace(line) == "" {
		return []string{""}
	}
	rendered := strings.Trim(renderMarkdownDocument(line, width), "\n")
	if rendered == "" {
		return []string{""}
	}
	parts := strings.Split(rendered, "\n")
	for len(parts) > 0 && strings.TrimSpace(ansi.Strip(parts[0])) == "" {
		parts = parts[1:]
	}
	for len(parts) > 0 && strings.TrimSpace(ansi.Strip(parts[len(parts)-1])) == "" {
		parts = parts[:len(parts)-1]
	}
	if len(parts) == 0 {
		return []string{""}
	}
	return parts
}

func wrapPlanReviewPlainTextLine(line string, width int) []string {
	if width <= 0 {
		return []string{line}
	}
	if strings.TrimSpace(line) == "" {
		return []string{""}
	}
	indentWidth := len(line) - len(strings.TrimLeft(line, " \t"))
	indent := line[:indentWidth]
	words := strings.Fields(line)
	if len(words) == 0 {
		return []string{""}
	}
	lines := make([]string, 0, 1)
	current := ""
	currentWidth := 0
	indentDisplayWidth := ansi.StringWidth(strings.ReplaceAll(indent, "\t", "    "))
	for _, word := range words {
		wordWidth := ansi.StringWidth(word)
		if current == "" {
			current = indent + word
			currentWidth = indentDisplayWidth + wordWidth
			continue
		}
		candidateWidth := currentWidth + 1 + wordWidth
		if candidateWidth > width {
			lines = append(lines, current)
			current = indent + word
			currentWidth = indentDisplayWidth + wordWidth
			continue
		}
		current += " " + word
		currentWidth = candidateWidth
	}
	if current != "" {
		lines = append(lines, current)
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

func wrapPlanReviewCodeLine(line string, width int) []string {
	return wrapPlanReviewPlainTextLine(strings.ReplaceAll(line, "\t", "    "), width)
}

func (m *PlanReviewModel) SetWorkItemID(id string) { m.workItemID = id }

func (m *PlanReviewModel) KeybindHints() []KeybindHint {
	if m.inputMode != planReviewNormal {
		return []KeybindHint{
			{Key: "Enter", Label: "Submit"},
			{Key: "Esc", Label: "Cancel"},
		}
	}
	return []KeybindHint{
		{Key: "a", Label: "Approve"},
		{Key: "c", Label: "Request changes"},
		{Key: "e", Label: "Edit in $EDITOR"},
		{Key: "r", Label: "Reject"},
		{Key: "↑↓", Label: "Scroll"},
		{Key: "Esc", Label: "Close"},
	}
}

func (m PlanReviewModel) Update(msg tea.Msg) (PlanReviewModel, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.inputMode != planReviewNormal {
			switch msg.String() {
			case "enter":
				text := m.feedbackInput.Value()
				m.feedbackInput.SetValue("")
				m.feedbackInput.Blur()
				if m.inputMode == planReviewChanges {
					m.inputMode = planReviewNormal
					m.syncViewportSize()
					return m, func() tea.Msg {
						return PlanRequestChangesMsg{PlanID: m.planID, Feedback: text}
					}
				}
				m.inputMode = planReviewNormal
				m.syncViewportSize()
				return m, func() tea.Msg {
					return PlanRejectMsg{PlanID: m.planID, Reason: text, WorkItemID: m.workItemID}
				}
			case "esc":
				m.inputMode = planReviewNormal
				m.feedbackInput.SetValue("")
				m.feedbackInput.Blur()
				m.syncViewportSize()
			default:
				m.feedbackInput, cmd = m.feedbackInput.Update(msg)
			}
			return m, cmd
		}
		switch msg.String() {
		case "a":
			return m, func() tea.Msg {
				return PlanApproveMsg{PlanID: m.planID, WorkItemID: m.workItemID}
			}
		case "c":
			m.inputMode = planReviewChanges
			m.feedbackInput.Placeholder = "Describe the orchestration or sub-plan changes needed…"
			m.feedbackInput.Focus()
			m.syncViewportSize()
		case "r":
			m.inputMode = planReviewReject
			m.feedbackInput.Placeholder = "Reason for rejection…"
			m.feedbackInput.Focus()
			m.syncViewportSize()
		case "e":
			return m, editPlanInEditorCmd(m.planID, m.workItemID, m.planContent)
		case "up", "k", "down", "j", "pgup", "pgdown":
			m.viewport, cmd = m.viewport.Update(msg)
		}
	case tea.WindowSizeMsg:
		m.syncViewportSize()
		m.viewport, cmd = m.viewport.Update(msg)
	default:
		m.viewport, cmd = m.viewport.Update(msg)
	}
	return m, cmd
}

func (m PlanReviewModel) View() string {
	header := components.RenderHeaderBlock(m.styles, components.HeaderBlockSpec{
		Title:   m.title + " · Plan Review",
		Width:   m.width,
		Divider: true,
	})

	body := m.viewport.View()
	if strings.TrimSpace(body) == "" {
		body = m.styles.Muted.Render("No plan content available.")
	}

	var feedbackRow string
	if m.inputMode != planReviewNormal {
		label := "Request changes: "
		if m.inputMode == planReviewReject {
			label = "Rejection reason: "
		}
		feedbackRow = m.styles.Warning.Render(label) + m.feedbackInput.View()
	}

	hints := renderOverlayHintsRow(m.styles, m.KeybindHints(), m.width)

	parts := append(strings.Split(header, "\n"), body)
	if feedbackRow != "" {
		parts = append(parts, feedbackRow)
	}
	parts = append(parts, components.RenderDivider(m.styles, m.width), hints)
	return fitViewBox(strings.Join(parts, "\n"), m.width, m.height)
}

// editPlanInEditorCmd opens the full plan document in $EDITOR.
func editPlanInEditorCmd(planID, workItemID, content string) tea.Cmd {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}
	f, err := os.CreateTemp("", "substrate-plan-*.md")
	if err != nil {
		return func() tea.Msg { return ErrMsg{Err: err} }
	}
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		os.Remove(f.Name())
		return func() tea.Msg { return ErrMsg{Err: err} }
	}
	f.Close()
	tmpFile := f.Name()
	return tea.ExecProcess(exec.Command(editor, tmpFile), func(err error) tea.Msg {
		if err != nil {
			os.Remove(tmpFile)
			return ErrMsg{Err: err}
		}
		data, readErr := os.ReadFile(tmpFile)
		os.Remove(tmpFile)
		if readErr != nil {
			return ErrMsg{Err: readErr}
		}
		return PlanEditedMsg{PlanID: planID, WorkItemID: workItemID, NewContent: string(data)}
	})
}

// ReadyToPlanModel shows work item details when state is "ingested".
type ReadyToPlanModel struct {
	workItem *domain.Session
	styles   styles.Styles
	width    int
	height   int
}

func NewReadyToPlanModel(st styles.Styles) ReadyToPlanModel {
	return ReadyToPlanModel{styles: st}
}

func (m *ReadyToPlanModel) SetSize(w, h int) { m.width = w; m.height = h }

func (m *ReadyToPlanModel) SetWorkItem(wi *domain.Session) { m.workItem = wi }

func (m ReadyToPlanModel) Update(msg tea.Msg) (ReadyToPlanModel, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok && key.String() == "enter" && m.workItem != nil {
		id := m.workItem.ID
		return m, func() tea.Msg { return StartPlanMsg{WorkItemID: id} }
	}
	return m, nil
}

func (m ReadyToPlanModel) View() string {
	if m.workItem == nil || m.width <= 0 || m.height <= 0 {
		return ""
	}

	description := strings.TrimSpace(m.workItem.Description)
	if description == "" {
		description = "_No description provided._"
	}

	descriptionInnerWidth := components.CalloutInnerWidth(m.styles, m.width)
	nextStepInset := 2
	nextStepWidth := max(1, m.width-(nextStepInset*2))
	nextStepInnerWidth := components.CalloutInnerWidth(m.styles, nextStepWidth)
	nextStep := m.styles.Muted.Render("Press ") +
		m.styles.KeybindAccent.Render("[Enter]") +
		m.styles.Muted.Render(" to start planning.")

	headingInset := lipgloss.NewStyle().PaddingLeft(2)
	nextStepInsetStyle := lipgloss.NewStyle().Padding(0, nextStepInset, 1, nextStepInset)
	descriptionContent := strings.Trim(renderMarkdownDocument(description, descriptionInnerWidth), "\n")
	nextStepContent := ansi.Hardwrap(nextStep, nextStepInnerWidth, true)
	nextStepCard := components.RenderCallout(m.styles, components.CalloutSpec{Body: nextStepContent, Width: nextStepWidth, Variant: components.CalloutCard})
	nextStepBlock := nextStepInsetStyle.Render(nextStepCard)

	topBlocks := []string{
		headingInset.Render(m.styles.Title.Render(m.workItem.ExternalID + " · " + m.workItem.Title)),
		headingInset.Render(m.styles.SectionLabel.Render("Details")),
		components.RenderCallout(m.styles, components.CalloutSpec{Body: descriptionContent, Width: m.width}),
	}

	footerLineCount := len(strings.Split(nextStepBlock, "\n"))
	bodyHeight := max(0, m.height-footerLineCount)
	body := fitViewHeight(strings.Join(topBlocks, "\n"), bodyHeight)
	if body == "" {
		return fitViewBox(nextStepBlock, m.width, m.height)
	}

	return fitViewBox(body+"\n"+nextStepBlock, m.width, m.height)
}

// AwaitingImplModel shows plan summary when state is "approved".
type AwaitingImplModel struct {
	workItem *domain.Session
	styles   styles.Styles
	width    int
	height   int
}

func NewAwaitingImplModel(st styles.Styles) AwaitingImplModel {
	return AwaitingImplModel{styles: st}
}

func (m *AwaitingImplModel) SetSize(w, h int) { m.width = w; m.height = h }

func (m *AwaitingImplModel) SetWorkItem(wi *domain.Session) { m.workItem = wi }

func (m AwaitingImplModel) View() string {
	if m.workItem == nil {
		return ""
	}
	return strings.Join([]string{
		m.styles.Title.Render(m.workItem.ExternalID + " · " + m.workItem.Title),
		components.RenderDivider(m.styles, m.width),
		"",
		m.styles.Active.Render("Plan approved."),
		m.styles.Subtitle.Render("Implementation will begin shortly."),
	}, "\n")
}

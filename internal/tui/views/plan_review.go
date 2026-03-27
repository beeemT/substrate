package views

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/beeemT/substrate/internal/tui/components"
	"github.com/beeemT/substrate/internal/tui/styles"
)

type planReviewInputMode int

const (
	planReviewNormal  planReviewInputMode = iota
	planReviewChanges                     // user pressed [c]: request changes
	planReviewReject                      // user pressed [r]: rejection reason
)

const feedbackMaxLines = 6

// PlanReviewModel renders the plan and handles approval flow.
type PlanReviewModel struct { //nolint:recvcheck // Bubble Tea: Update returns value, View on value receiver
	viewport       viewport.Model
	feedbackInput  textarea.Model
	feedbackHeight int
	inputMode      planReviewInputMode
	title          string
	planID         string
	workItemID     string
	planContent    string
	styles         styles.Styles
	width          int
	height         int
}

func NewPlanReviewModel(st styles.Styles) PlanReviewModel {
	ta := components.NewTextArea()
	ta.ShowLineNumbers = false
	ta.Prompt = ""
	ta.MaxHeight = feedbackMaxLines
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.BlurredStyle.CursorLine = lipgloss.NewStyle()
	ta.EndOfBufferCharacter = 0
	ta.SetHeight(1)

	return PlanReviewModel{
		viewport:       viewport.New(0, 0),
		feedbackInput:  ta,
		feedbackHeight: 1,
		styles:         st,
	}
}

func (m *PlanReviewModel) SetSize(width, height int) {
	m.width = width
	m.height = height
	m.syncViewportSize()
}

func (m *PlanReviewModel) syncViewportSize() {
	reservedRows := 2 // title + divider (header)
	if m.inputMode != planReviewNormal {
		reservedRows += 1 + m.feedbackHeight // label row + textarea rows
	}
	m.feedbackInput.SetWidth(max(1, m.width))
	m.viewport.Width = max(1, m.width-1)
	m.viewport.Height = max(1, m.height-reservedRows)
	m.refreshViewportContent(false)
}

// syncFeedbackHeight recomputes the number of visual rows the textarea needs,
// capped at feedbackMaxLines. If the height changed, syncViewportSize is called.
func (m *PlanReviewModel) syncFeedbackHeight() {
	if m.inputMode == planReviewNormal {
		return
	}
	innerWidth := m.feedbackInput.Width()
	if innerWidth <= 0 {
		return
	}
	value := m.feedbackInput.Value()
	logicalLines := strings.Split(value, "\n")
	total := 0
	for _, ln := range logicalLines {
		segments := wrapPlanReviewPlainTextLine(ln, innerWidth)
		total += len(segments)
	}
	h := max(1, min(total, feedbackMaxLines))
	if h == m.feedbackHeight {
		return
	}
	m.feedbackHeight = h
	m.feedbackInput.SetHeight(h)
	// SetHeight only changes viewport.Height; it leaves viewport.YOffset
	// where repositionView() put it (scrolled down to track the cursor).
	// SetValue calls Reset() → GotoTop() (zeroes YOffset) then re-inserts
	// the content, placing the cursor back at the end. This makes all typed
	// content visible from row 0 as the textarea grows.
	m.feedbackInput.SetValue(value)
	m.syncViewportSize()
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
	// formatLine returns a fully formatted output line: right-justified line
	// number (or blank for continuation lines) + separator + content padded to contentWidth.
	formatLine := func(lineNum int, showNum bool, content string) string {
		ln := strings.Repeat(" ", numberWidth)
		if showNum {
			ln = fmt.Sprintf("%*d", numberWidth, lineNum)
		}
		return st.Muted.Render(ln+separator) + lipgloss.NewStyle().Width(contentWidth).Render(content)
	}

	rendered := make([]string, 0, len(rawLines))
	inCodeBlock := false
	index := 0
	for index < len(rawLines) {
		rawLine := rawLines[index]
		trimmedLine := strings.TrimSpace(rawLine)

		isFence := strings.HasPrefix(trimmedLine, "```")

		// Inside a fenced code block: pass through as-is (no table detection).
		if inCodeBlock || isFence {
			segments := wrapPlanReviewCodeLine(rawLine, contentWidth)
			style := st.SettingsText
			if isFence {
				style = st.Muted
			}
			for wrappedIndex, segment := range segments {
				rendered = append(rendered, formatLine(index+1, wrappedIndex == 0, style.Render(segment)))
			}
			if isFence {
				inCodeBlock = !inCodeBlock
			}
			index++

			continue
		}

		// Table block: collect consecutive table lines and render as a unit.
		if planReviewIsTableLine(trimmedLine) {
			tableStart := index
			tableLines := make([]string, 0, 4)
			for index < len(rawLines) {
				tl := strings.TrimSpace(rawLines[index])
				if tl == "" || (!planReviewIsTableLine(tl) && tl != "") {
					break
				}
				if tl != "" {
					tableLines = append(tableLines, rawLines[index])
				}
				index++
			}
			tableBlock := strings.Join(tableLines, "\n")
			tableRendered := strings.Trim(renderMarkdownDocument(tableBlock, contentWidth), "\n")
			if tableRendered == "" {
				tableRendered = tableBlock
			}
			for lineIdx, trLine := range strings.Split(tableRendered, "\n") {
				rendered = append(rendered, formatLine(tableStart+1, lineIdx == 0, trLine))
			}

			continue
		}

		// Single-line markdown rendering (headings, lists, bold, links, etc.).
		var segments []string
		renderMarkdown := false
		style := st.SettingsText
		switch {
		case planReviewLineUsesMarkdown(trimmedLine):
			segments = renderPlanReviewMarkdownLine(rawLine, contentWidth)
			renderMarkdown = true
		case trimmedLine == "":
			segments = []string{""}
		default:
			segments = wrapPlanReviewPlainTextLine(rawLine, contentWidth)
		}
		for wrappedIndex, segment := range segments {
			renderedSegment := segment
			if !renderMarkdown {
				renderedSegment = style.Render(segment)
			}
			rendered = append(rendered, formatLine(index+1, wrappedIndex == 0, renderedSegment))
		}
		index++
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

// planReviewIsTableLine returns true for well-formed GFM table rows.
// A table row starts and ends with | and contains at least one more |.
func planReviewIsTableLine(trimmedLine string) bool {
	return len(trimmedLine) >= 3 &&
		trimmedLine[0] == '|' &&
		trimmedLine[len(trimmedLine)-1] == '|' &&
		strings.Contains(trimmedLine[1:], "|")
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
			case keyEnter:
				text := strings.TrimSpace(m.feedbackInput.Value())
				m.feedbackHeight = 1
				m.feedbackInput.SetHeight(1)
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
			case keyEsc:
				m.inputMode = planReviewNormal
				m.feedbackHeight = 1
				m.feedbackInput.SetHeight(1)
				m.feedbackInput.SetValue("")
				m.feedbackInput.Blur()
				m.syncViewportSize()
			default:
				m.feedbackInput, cmd = m.feedbackInput.Update(msg)
				m.syncFeedbackHeight()
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
		case "up", "k", "down", "j", "pgup", "pgdown": //nolint:goconst
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
	} else {
		if sb := m.renderScrollbar(m.viewport, m.viewport.Height); sb != "" {
			body = lipgloss.JoinHorizontal(lipgloss.Top, body, sb)
		}
	}

	var feedbackRow string
	if m.inputMode != planReviewNormal {
		label := "Request changes:"
		if m.inputMode == planReviewReject {
			label = "Rejection reason:"
		}
		feedbackRow = m.styles.Warning.Render(label) + "\n" + m.feedbackInput.View()
	}

	parts := append(strings.Split(header, "\n"), body)
	if feedbackRow != "" {
		parts = append(parts, feedbackRow)
	}

	return fitViewBox(strings.Join(parts, "\n"), m.width, m.height)
}

func (m PlanReviewModel) renderScrollbar(vp viewport.Model, height int) string {
	if height <= 0 {
		return ""
	}
	total := vp.TotalLineCount()
	if total <= height {
		return ""
	}
	lines := make([]string, height)
	thumbHeight := max(1, (height*height)/max(1, total))
	thumbHeight = min(thumbHeight, height)
	thumbRange := max(0, height-thumbHeight)
	scrollRange := max(1, total-height)
	thumbTop := 0
	if thumbRange > 0 {
		thumbTop = (vp.YOffset*thumbRange + scrollRange/2) / scrollRange
	}
	for i := range lines {
		lines[i] = m.styles.ScrollbarTrack.Render("▏")
		if i >= thumbTop && i < thumbTop+thumbHeight {
			lines[i] = m.styles.ScrollbarThumbFocused.Render("▐")
		}
	}

	return strings.Join(lines, "\n")
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
		os.Remove(f.Name()) //nolint:gosec // taint-analysis false positive

		return func() tea.Msg { return ErrMsg{Err: err} }
	}
	f.Close()
	tmpFile := f.Name()

	return tea.ExecProcess(exec.Command(editor, tmpFile), func(err error) tea.Msg { //nolint:gosec,noctx // taint-analysis false positive; Bubble Tea ExecProcess has no context parameter
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

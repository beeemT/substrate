package views

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
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
)

const feedbackMaxLines = 6

// sgrMouseFragRe extracts button codes from complete SGR mouse escape sequence
// bodies. It is non-anchored so it can find multiple fragments concatenated in
// a single KeyRunes event (common under heavy scroll when ESC bytes between
// sequences are stripped or split off by the terminal).
var sgrMouseFragRe = regexp.MustCompile(`\[<(\d+);\d+;\d+[Mm]`)

// allSGRBodyRunes returns true if every rune could appear in the body of an
// SGR mouse escape sequence: [ < 0-9 ; M m.
func allSGRBodyRunes(runes []rune) bool {
	for _, r := range runes {
		switch {
		case r >= '0' && r <= '9':
		case r == '[', r == '<', r == ';', r == 'M', r == 'm':
		default:
			return false
		}
	}
	return true
}

// isLikelySGRMouseFragment returns true when runes look like the body (or
// bodies) of SGR mouse escape sequences stripped of their leading ESC byte.
//
// The heuristic: len ≥ 2 and every rune is in the SGR body character set.
// Single-rune events are not flagged to avoid blocking legitimate typing.
// False positives in a feedback textarea are negligible — a user would have to
// type two or more characters exclusively from that set with no spaces.
func isLikelySGRMouseFragment(runes []rune) bool {
	return len(runes) >= 2 && allSGRBodyRunes(runes)
}

// extractSGRScrollLines scans runes for complete SGR mouse sequence bodies
// and returns the total viewport lines to scroll up (negative) and down
// (positive). Fragments that don't contain a complete [<btn;col;rowM pattern
// are silently discarded.
func extractSGRScrollLines(runes []rune) int {
	const linesPerTick = 3
	matches := sgrMouseFragRe.FindAllStringSubmatch(string(runes), -1)
	scroll := 0
	for _, m := range matches {
		btn, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		// SGR button encoding: bit 6 (value 64) = wheel flag, bit 0 = direction.
		const wheelBit = 0b0100_0000
		if btn&wheelBit == 0 {
			continue // non-wheel mouse event (click/drag)
		}
		if btn&1 == 0 {
			scroll -= linesPerTick // scroll up
		} else {
			scroll += linesPerTick // scroll down
		}
	}
	return scroll
}

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

	// pendingBracket is true when a lone '[' rune has been buffered as a
	// potential start of an SGR mouse fragment body. The next event
	// resolves whether it is a real fragment (starts with '<') or
	// legitimate text input (flushed to the textarea).
	pendingBracket bool
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
	if m.planID == planID && m.planContent == content {
		return
	}
	reset := m.planID != planID
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

// FeedbackValue returns the current text in the feedback textarea.
func (m *PlanReviewModel) FeedbackValue() string { return m.feedbackInput.Value() }

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
				// Flush any buffered '[' before reading the value.
				if m.pendingBracket {
					m.pendingBracket = false
					m.feedbackInput, _ = m.feedbackInput.Update(
						tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'['}},
					)
				}
				text := strings.TrimSpace(m.feedbackInput.Value())
				m.feedbackHeight = 1
				m.feedbackInput.SetHeight(1)
				m.feedbackInput.SetValue("")
				m.feedbackInput.Blur()
				m.inputMode = planReviewNormal
				m.syncViewportSize()

				return m, tea.Batch(
					func() tea.Msg {
						return PlanRequestChangesMsg{PlanID: m.planID, Feedback: text}
					},
					tea.EnableMouseCellMotion,
				)
			case keyEsc:
				m.pendingBracket = false // text discarded on cancel
				m.inputMode = planReviewNormal
				m.feedbackHeight = 1
				m.feedbackInput.SetHeight(1)
				m.feedbackInput.SetValue("")
				m.feedbackInput.Blur()
				m.syncViewportSize()
				return m, tea.EnableMouseCellMotion
			default:
				if msg.Type == tea.KeyRunes {
					// Discard Alt-modified runes in the SGR character set.
					// These come from \x1b being parsed together with the
					// next byte as an Alt-modified key (e.g. \x1b[ → Alt+[).
					if msg.Alt && allSGRBodyRunes(msg.Runes) {
						m.pendingBracket = false
						return m, nil
					}

					// Resolve a pending '[' against the current runes.
					// If the continuation starts with '<' and all runes
					// are in the SGR body set, it is a fragment.
					if m.pendingBracket {
						m.pendingBracket = false
						if len(msg.Runes) > 0 && msg.Runes[0] == '<' && allSGRBodyRunes(msg.Runes) {
							combined := append([]rune{'['}, msg.Runes...)
							scroll := extractSGRScrollLines(combined)
							if scroll < 0 {
								m.viewport.ScrollUp(-scroll)
							} else if scroll > 0 {
								m.viewport.ScrollDown(scroll)
							}
							return m, nil
						}
						// Not a fragment — flush the buffered '['.
						m.feedbackInput, _ = m.feedbackInput.Update(
							tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'['}},
						)
						// Fall through to process current msg normally.
					}

					// Buffer a lone '[' as a potential SGR fragment start.
					if len(msg.Runes) == 1 && msg.Runes[0] == '[' {
						m.pendingBracket = true
						return m, nil
					}

					// Intercept multi-rune SGR mouse fragments.
					if isLikelySGRMouseFragment(msg.Runes) {
						scroll := extractSGRScrollLines(msg.Runes)
						if scroll < 0 {
							m.viewport.ScrollUp(-scroll)
						} else if scroll > 0 {
							m.viewport.ScrollDown(scroll)
						}
						return m, nil
					}
				} else if m.pendingBracket {
					// Non-rune key with pending bracket — flush it first.
					m.pendingBracket = false
					m.feedbackInput, _ = m.feedbackInput.Update(
						tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'['}},
					)
				}
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
			return m, tea.DisableMouse
		case "e":
			return m, editPlanInEditorCmd(m.planID, m.workItemID, m.planContent)
		case "up", "k", keyDown, "j", "pgup", "pgdown":
			m.viewport, cmd = m.viewport.Update(msg)
		default:
			if msg.Type == tea.KeyRunes && isLikelySGRMouseFragment(msg.Runes) {
				if scroll := extractSGRScrollLines(msg.Runes); scroll != 0 {
					if scroll < 0 {
						m.viewport.ScrollUp(-scroll)
					} else {
						m.viewport.ScrollDown(scroll)
					}
				}
			}
		}
	case tea.MouseMsg:
		// Scroll the plan viewport on wheel events. Mouse reporting is
		// disabled while the feedback textarea is focused (inputMode != normal),
		// so these only arrive in normal/read mode.
		if msg.Action == tea.MouseActionPress {
			switch msg.Button {
			case tea.MouseButtonWheelUp, tea.MouseButtonWheelDown:
				m.viewport, cmd = m.viewport.Update(msg)
			}
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

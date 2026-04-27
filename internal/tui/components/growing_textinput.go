package components

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// DefaultGrowingTextInputMaxLines caps scalar inputs that wrap for visibility.
const DefaultGrowingTextInputMaxLines = 3

// GrowingTextInput is a scalar text input that wraps visually as content grows.
// It preserves textinput semantics: values are single-line, plain Enter is
// host-owned, and no newline insertion is supported. Use it for long scalar
// values such as URLs, names, labels, and settings values that should remain
// visible on narrow terminals without becoming prose textareas.
type GrowingTextInput struct {
	model    textinput.Model
	maxLines int
	height   int
}

// NewGrowingTextInput returns a scalar input that starts at one row and grows
// up to DefaultGrowingTextInputMaxLines as its value wraps at the configured
// width.
func NewGrowingTextInput() GrowingTextInput {
	m := NewTextInput()
	m.Prompt = ""
	return GrowingTextInput{
		model:    m,
		maxLines: DefaultGrowingTextInputMaxLines,
		height:   1,
	}
}

func (g *GrowingTextInput) SetMaxLines(n int) {
	if n < 1 {
		n = 1
	}
	g.maxLines = n
	g.syncHeight()
}

func (g *GrowingTextInput) SetWidth(w int) {
	if w < 1 {
		w = 1
	}
	g.model.Width = w
	g.syncHeight()
}

func (g GrowingTextInput) Width() int { return g.model.Width }

func (g GrowingTextInput) Height() int { return g.height }

func (g GrowingTextInput) Value() string { return g.model.Value() }

func (g *GrowingTextInput) SetValue(s string) {
	g.model.SetValue(sanitizeScalarInput(s))
	g.syncHeight()
}

func (g *GrowingTextInput) SetCursor(pos int) { g.model.SetCursor(pos) }

func (g *GrowingTextInput) SetPlaceholder(s string) { g.model.Placeholder = s }

func (g GrowingTextInput) Placeholder() string { return g.model.Placeholder }

func (g *GrowingTextInput) SetCharLimit(n int) { g.model.CharLimit = n }

func (g *GrowingTextInput) SetPrompt(s string) { g.model.Prompt = s }

func (g GrowingTextInput) Focused() bool { return g.model.Focused() }

func (g *GrowingTextInput) Focus() tea.Cmd { return g.model.Focus() }

func (g *GrowingTextInput) Blur() { g.model.Blur() }

func (g *GrowingTextInput) Reset() {
	g.model.SetValue("")
	g.model.Blur()
	g.height = 1
}

func (g GrowingTextInput) View() string {
	width := g.model.Width
	if width <= 0 {
		return g.model.View()
	}
	if g.model.Value() == "" {
		return g.model.View()
	}

	value := g.model.Value()
	valueRunes := []rune(value)
	cursor := g.model.Position()
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(valueRunes) {
		cursor = len(valueRunes)
	}

	visibleStart := 0
	wrappedBeforeCursor := wrapScalarInputValue(string(valueRunes[:cursor]), width)
	lineIndex := 0
	if wrappedBeforeCursor != "" {
		lineIndex = strings.Count(wrappedBeforeCursor, "\n")
	}

	wrapped := wrapScalarInputValue(value, width)
	lines := strings.Split(wrapped, "\n")
	if len(lines) > g.maxLines {
		visibleStart = len(lines) - g.maxLines
		lines = lines[visibleStart:]
		if len(lines) > 0 {
			lines[0] = ansi.TruncateLeft(lines[0], width, "…")
		}
	}
	cursorLine := lineIndex - visibleStart

	return g.viewLines(lines, width, cursorLine, cursor)
}

func (g GrowingTextInput) viewLines(lines []string, width int, cursorLine int, cursor int) string {
	if len(lines) == 0 {
		return ""
	}
	for i := range lines {
		lines[i] = padScalarInputLine(lines[i], width)
	}
	if g.model.Focused() {
		if cursorLine < 0 {
			cursorLine = 0
		}
		if cursorLine >= len(lines) {
			cursorLine = len(lines) - 1
		}
		line := lines[cursorLine]
		wrappedBeforeCursor := wrapScalarInputValue(string([]rune(g.model.Value())[:cursor]), width)
		cursorColumn := ansi.StringWidth(lastScalarInputWrappedLine(wrappedBeforeCursor))
		if cursorColumn >= width {
			cursorColumn = width - 1
		}
		g.model.Cursor.SetChar(scalarInputCharAtColumn(line, cursorColumn))
		if g.model.Cursor.Blink {
			g.model.Cursor.Blink = false
		}
		lines[cursorLine] = renderScalarInputCursor(line, width, cursorColumn, g.model.Cursor.View())
	}
	return strings.Join(lines, "\n")
}

func (g GrowingTextInput) Update(msg tea.Msg) (GrowingTextInput, tea.Cmd) {
	var cmd tea.Cmd
	g.model, cmd = g.model.Update(msg)
	g.model.SetValue(sanitizeScalarInput(g.model.Value()))
	g.syncHeight()
	return g, cmd
}

func (g *GrowingTextInput) syncHeight() {
	width := g.model.Width
	if width <= 0 {
		g.height = 1
		return
	}
	want := scalarInputWrappedRowCount(g.model.Value(), width)
	if want < 1 {
		want = 1
	}
	if want > g.maxLines {
		want = g.maxLines
	}
	g.height = want
}

func sanitizeScalarInput(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return s
}

func lastScalarInputWrappedLine(s string) string {
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	return lines[len(lines)-1]
}

func scalarInputCharAtColumn(line string, column int) string {
	if column < 0 {
		return " "
	}
	runes := []rune(line)
	if column >= len(runes) {
		return " "
	}
	return string(runes[column])
}

func renderScalarInputCursor(line string, width int, column int, cursor string) string {
	if width <= 0 {
		return line + cursor
	}
	if column < 0 {
		column = 0
	}
	if column >= width {
		column = width - 1
	}

	runes := []rune(line)
	if column >= len(runes) {
		return line + cursor
	}
	return string(runes[:column]) + cursor + string(runes[column+1:])
}

func scalarInputWrappedRowCount(value string, width int) int {
	if width <= 0 || value == "" {
		return 1
	}
	wrapped := wrapScalarInputValue(value, width)
	if wrapped == "" {
		return 1
	}
	return strings.Count(wrapped, "\n") + 1
}

func wrapScalarInputValue(value string, width int) string {
	if width <= 0 {
		return value
	}
	return ansi.Wrap(value, width, "")
}

func padScalarInputLine(s string, width int) string {
	if width <= 0 {
		return s
	}
	visible := ansi.StringWidth(s)
	switch {
	case visible < width:
		return s + strings.Repeat(" ", width-visible)
	case visible > width:
		return ansi.Truncate(s, width, "")
	default:
		return s
	}
}

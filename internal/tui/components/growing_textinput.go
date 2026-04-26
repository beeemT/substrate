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
	wrapped := wrapScalarInputValue(value, width)
	lines := strings.Split(wrapped, "\n")
	if len(lines) <= g.maxLines {
		return g.viewLines(lines, width)
	}

	lines = lines[len(lines)-g.maxLines:]
	if len(lines) > 0 {
		lines[0] = ansi.TruncateLeft(lines[0], width, "…")
	}
	return g.viewLines(lines, width)
}

func (g GrowingTextInput) viewLines(lines []string, width int) string {
	if len(lines) == 0 {
		return ""
	}
	for i := range lines {
		lines[i] = padScalarInputLine(lines[i], width)
	}
	if g.model.Focused() {
		last := lines[len(lines)-1]
		if ansi.StringWidth(last) >= width {
			last = ansi.Truncate(last, max(0, width-1), "")
		}
		lines[len(lines)-1] = last + g.model.Cursor.View()
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

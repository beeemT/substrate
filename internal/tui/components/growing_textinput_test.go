package components_test

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/beeemT/substrate/internal/tui/components"
)

func focusedGrowingTextInput(g components.GrowingTextInput) components.GrowingTextInput {
	_ = g.Focus()
	return g
}

func typeScalarRunes(g components.GrowingTextInput, s string) components.GrowingTextInput {
	for _, r := range s {
		g, _ = g.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return g
}

func TestGrowingTextInputStartsAtOneRow(t *testing.T) {
	t.Parallel()

	g := components.NewGrowingTextInput()
	g.SetWidth(20)

	if got := g.Height(); got != 1 {
		t.Fatalf("height = %d, want 1", got)
	}
}

func TestGrowingTextInputGrowsWithWrappedContent(t *testing.T) {
	t.Parallel()

	g := components.NewGrowingTextInput()
	g.SetWidth(5)
	g.SetMaxLines(3)
	g = focusedGrowingTextInput(g)
	g = typeScalarRunes(g, "abcdefghijkl")

	if got := g.Height(); got != 3 {
		t.Fatalf("height = %d, want 3", got)
	}
	if got := g.Value(); got != "abcdefghijkl" {
		t.Fatalf("value = %q, want original scalar text", got)
	}
}

func TestGrowingTextInputCapsAtMaxLines(t *testing.T) {
	t.Parallel()

	g := components.NewGrowingTextInput()
	g.SetWidth(4)
	g.SetMaxLines(2)
	g.SetValue("abcdefghijkl")

	if got := g.Height(); got != 2 {
		t.Fatalf("height = %d, want 2", got)
	}
	if lines := strings.Split(g.View(), "\n"); len(lines) != 2 {
		t.Fatalf("rendered lines = %d, want 2; view=%q", len(lines), g.View())
	}
}

func TestGrowingTextInputSanitizesNewlines(t *testing.T) {
	t.Parallel()

	g := components.NewGrowingTextInput()
	g.SetWidth(20)
	g.SetValue("one\ntwo\r\nthree")

	if got := g.Value(); got != "one two three" {
		t.Fatalf("value = %q, want scalar sanitized value", got)
	}
}

func TestGrowingTextInputEnterDoesNotInsertNewline(t *testing.T) {
	t.Parallel()

	g := components.NewGrowingTextInput()
	g.SetWidth(20)
	g = focusedGrowingTextInput(g)
	g = typeScalarRunes(g, "abc")
	g, _ = g.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if got := g.Value(); got != "abc" {
		t.Fatalf("value after enter = %q, want unchanged scalar", got)
	}
}

func TestGrowingTextInputRenderedLinesStayWithinWidth(t *testing.T) {
	t.Parallel()

	g := components.NewGrowingTextInput()
	g.SetWidth(6)
	g.SetMaxLines(3)
	g.SetValue("abcdefghijklmnop")

	for _, line := range strings.Split(g.View(), "\n") {
		if got := ansi.StringWidth(line); got > 6 {
			t.Fatalf("line width = %d, want <= 6; line=%q view=%q", got, line, g.View())
		}
	}
}

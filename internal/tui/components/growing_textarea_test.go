package components_test

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/beeemT/substrate/internal/tui/components"
)

// typeRunes feeds each rune as a separate KeyRunes event into the component.
// Caller MUST have focused g first.
func typeRunes(g components.GrowingTextArea, s string) components.GrowingTextArea {
	for _, r := range s {
		msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
		g, _ = g.Update(msg)
	}
	return g
}

// focused returns g focused and ready to accept input events. The textarea
// only processes events while focused.
func focused(g components.GrowingTextArea) components.GrowingTextArea {
	_ = g.Focus()
	return g
}

func TestGrowingTextAreaStartsAtOneRow(t *testing.T) {
	t.Parallel()

	g := components.NewGrowingTextArea("")
	g.SetWidth(40)

	if got := g.Height(); got != 1 {
		t.Fatalf("initial height = %d, want 1", got)
	}
}

func TestGrowingTextAreaGrowsOnNewline(t *testing.T) {
	t.Parallel()

	g := components.NewGrowingTextArea("")
	g.SetMaxLines(6)
	g.SetWidth(40)
	g = focused(g)

	// alt+enter is the configured newline binding (see macOSTextAreaKeyMap).
	g, _ = g.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	g, _ = g.Update(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	g, _ = g.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})

	if got := g.Height(); got != 2 {
		t.Fatalf("height after one newline = %d, want 2; value=%q", got, g.Value())
	}
	if got := g.Value(); got != "a\nb" {
		t.Fatalf("value = %q, want %q", got, "a\nb")
	}
}

func TestGrowingTextAreaCapsAtMaxLines(t *testing.T) {
	t.Parallel()

	g := components.NewGrowingTextArea("")
	g.SetMaxLines(3)
	g.SetWidth(40)
	// Ten visible logical lines.
	g.SetValue(strings.TrimSuffix(strings.Repeat("x\n", 10), "\n"))

	if got := g.Height(); got != 3 {
		t.Fatalf("capped height = %d, want 3", got)
	}
}

func TestGrowingTextAreaResetReturnsToOneRow(t *testing.T) {
	t.Parallel()

	g := components.NewGrowingTextArea("")
	g.SetMaxLines(6)
	g.SetWidth(40)
	g.SetValue("a\nb\nc\nd")
	if g.Height() < 2 {
		t.Fatalf("precondition: expected grown height, got %d", g.Height())
	}

	cmd := g.Reset()
	if cmd == nil {
		t.Fatal("Reset must return a tea.Cmd that re-enables mouse reporting")
	}
	if got := g.Height(); got != 1 {
		t.Fatalf("post-reset height = %d, want 1", got)
	}
	if got := g.Value(); got != "" {
		t.Fatalf("post-reset value = %q, want empty", got)
	}
	if g.Focused() {
		t.Fatal("Reset must blur the textarea")
	}
}

func TestGrowingTextAreaSGRFragmentDoesNotLeak(t *testing.T) {
	t.Parallel()

	g := components.NewGrowingTextArea("scroll-source")
	g.SetWidth(40)

	// Concatenated SGR scroll-down fragment.
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("[<65;97;554M")}
	g, cmd := g.Update(msg)

	if got := g.Value(); got != "" {
		t.Fatalf("SGR fragment leaked into textarea: %q", got)
	}
	if cmd == nil {
		t.Fatal("expected scroll-intent cmd from SGR fragment")
	}
	out := cmd()
	scroll, ok := out.(components.GrowingTextAreaScrollMsg)
	if !ok {
		t.Fatalf("expected GrowingTextAreaScrollMsg, got %T", out)
	}
	if scroll.Source != "scroll-source" {
		t.Fatalf("scroll.Source = %q, want %q", scroll.Source, "scroll-source")
	}
	if scroll.Delta <= 0 {
		t.Fatalf("scroll.Delta = %d, want positive (scroll down)", scroll.Delta)
	}
}

func TestGrowingTextAreaPendingBracketFlushedOnNonFragment(t *testing.T) {
	t.Parallel()

	g := components.NewGrowingTextArea("")
	g.SetWidth(40)
	g = focused(g)

	// Lone '[' must be buffered (not yet inserted).
	g, _ = g.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'['}})
	if got := g.Value(); got != "" {
		t.Fatalf("after lone '[' value = %q, want empty (buffered)", got)
	}
	// Continuation that is NOT an SGR fragment ('a') must flush the buffered
	// '[' and then insert 'a'.
	g, _ = g.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if got := g.Value(); got != "[a" {
		t.Fatalf("after flush value = %q, want %q", got, "[a")
	}
}

func TestGrowingTextAreaCharLimit(t *testing.T) {
	t.Parallel()

	g := components.NewGrowingTextArea("")
	g.SetWidth(40)
	g = focused(g)
	g.SetCharLimit(3)
	g = typeRunes(g, "abcdef")
	if got := g.Value(); got != "abc" {
		t.Fatalf("value = %q, want %q (CharLimit=3)", got, "abc")
	}
}

func TestGrowingTextAreaFocusReturnsCmd(t *testing.T) {
	t.Parallel()

	g := components.NewGrowingTextArea("")
	g.SetWidth(40)

	cmd := g.Focus()
	if cmd == nil {
		t.Fatal("Focus must return a tea.Cmd (textarea Focus + DisableMouse)")
	}
	if !g.Focused() {
		t.Fatal("Focus did not focus the textarea")
	}
}

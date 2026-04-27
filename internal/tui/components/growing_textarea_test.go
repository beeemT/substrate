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

func TestGrowingTextAreaVerticalBoundaries(t *testing.T) {
	t.Parallel()

	g := components.NewGrowingTextArea("")
	g.SetWidth(40)
	g = focused(g)

	if !g.AtTop() {
		t.Fatal("empty textarea should start at top")
	}
	if !g.AtBottom() {
		t.Fatal("empty textarea should start at bottom")
	}

	g = typeRunes(g, "first")
	g, _ = g.Update(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	g = typeRunes(g, "second")
	g, _ = g.Update(tea.KeyMsg{Type: tea.KeyUp})
	if !g.AtTop() {
		t.Fatal("cursor should be at top after SetValue")
	}
	if g.AtBottom() {
		t.Fatal("top of multi-line textarea reported bottom")
	}

	g, _ = g.Update(tea.KeyMsg{Type: tea.KeyDown})
	if g.AtTop() {
		t.Fatal("after Down, textarea still reported top")
	}
	if !g.AtBottom() {
		t.Fatal("after Down to final line, textarea should report bottom")
	}
}

func TestGrowingTextAreaViewRendersLineNumbersAsChrome(t *testing.T) {
	t.Parallel()

	g := components.NewGrowingTextArea("")
	g.SetWidth(24)
	g.SetValue("alpha beta gamma delta\nsecond line")

	view := g.View()
	if got := g.Value(); got != "alpha beta gamma delta\nsecond line" {
		t.Fatalf("value = %q, want content without rendered line numbers", got)
	}
	for _, want := range []string{"1", "2"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view = %q, want rendered line number %q", view, want)
		}
	}
	if strings.Contains(g.Value(), "1") || strings.Contains(g.Value(), "│") {
		t.Fatalf("value unexpectedly contains rendered gutter chrome: %q", g.Value())
	}
}

func TestGrowingTextAreaLargePasteShowsTailImmediately(t *testing.T) {
	t.Parallel()

	g := components.NewGrowingTextArea("")
	g.SetMaxLines(3)
	g.SetWidth(24)
	g = focused(g)

	pasted := strings.TrimSuffix(strings.Repeat("head line\n", 5), "\n") + "\nTAIL SENTINEL"
	g, _ = g.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(pasted)})

	view := g.View()
	if !strings.Contains(view, "TAIL SENTINEL") {
		t.Fatalf("large one-shot paste view did not scroll to tail immediately:\n%s", view)
	}
	if firstLine := strings.Split(view, "\n")[0]; strings.Contains(firstLine, "1 ") {
		t.Fatalf("large one-shot paste view still starts at rendered line 1 instead of the cursor tail:\n%s", view)
	}
	if !strings.Contains(view, "6 TAIL SENTINEL") {
		t.Fatalf("view = %q, want Bubbles line-number rendering for the cursor row", view)
	}
}

func TestGrowingTextAreaViewFollowsCursorAfterMovement(t *testing.T) {
	t.Parallel()

	g := components.NewGrowingTextArea("")
	g.SetMaxLines(3)
	g.SetWidth(24)
	g = focused(g)

	pasted := strings.TrimSuffix(strings.Repeat("top line\n", 5), "\n") + "\nTAIL SENTINEL"
	g, _ = g.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(pasted)})
	for range 5 {
		g, _ = g.Update(tea.KeyMsg{Type: tea.KeyUp})
	}

	view := g.View()
	if !strings.Contains(view, "1 top line") {
		t.Fatalf("view should follow cursor back to top, got:\n%s", view)
	}
	if strings.Contains(view, "TAIL SENTINEL") {
		t.Fatalf("view should not stay pinned to tail after cursor moves up, got:\n%s", view)
	}
}

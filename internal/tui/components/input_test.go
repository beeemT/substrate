package components_test

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/beeemT/substrate/internal/tui/components"
)

// TestNewTextAreaAltBWordBackward verifies alt+b (the sequence Warp sends for
// ⌥+←) triggers WordBackward.  This uses Key{Type:KeyRunes,Runes:['b'],Alt:true}
// — a different AST node than Key{Type:KeyLeft,Alt:true} — so it is distinct
// from TestNewTextAreaAltLeftStillWorks.
func TestNewTextAreaAltBWordBackward(t *testing.T) {
	t.Parallel()

	m := components.NewTextArea()
	m.SetWidth(80)
	m.SetHeight(3)
	m.Focus()
	for _, r := range "foo bar" {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	// alt+b (ESC+b, \x1bb in Warp) should move cursor back one word.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}, Alt: true})

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'X'}})
	got := m.Value()
	want := "foo Xbar"
	if got != want {
		t.Fatalf("after alt+b insert: got %q, want %q", got, want)
	}
}

// TestNewTextAreaAltFWordForward verifies alt+f (the sequence Warp sends for
// ⌥+→) triggers WordForward.
func TestNewTextAreaAltFWordForward(t *testing.T) {
	t.Parallel()

	m := components.NewTextArea()
	m.SetWidth(80)
	m.SetHeight(3)
	m.Focus()
	for _, r := range "foo bar" {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	// Go to start, then alt+f (ESC+f, \x1bf in Warp) skips past "foo".
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlA})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}, Alt: true})

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'X'}})
	got := m.Value()
	want := "fooX bar"
	if got != want {
		t.Fatalf("after alt+f insert: got %q, want %q", got, want)
	}
}

// TestNewTextInputCtrlWDeleteWordBackward verifies ctrl+w (\x17, the sequence
// Warp sends for ⌥+Backspace) deletes the previous word in a textinput.
func TestNewTextInputCtrlWDeleteWordBackward(t *testing.T) {
	t.Parallel()

	m := components.NewTextInput()
	m.Focus()
	for _, r := range "hello world" {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	// ctrl+w should delete "world".
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlW})
	got := m.Value()
	want := "hello "
	if got != want {
		t.Fatalf("after ctrl+w: got %q, want %q", got, want)
	}
}

// TestNewTextInputAltBWordBackward verifies alt+b (\x1bb, the sequence Warp
// sends for ⌥+←) moves the cursor backward one word in a textinput.
func TestNewTextInputAltBWordBackward(t *testing.T) {
	t.Parallel()

	m := components.NewTextInput()
	m.Focus()
	for _, r := range "hello world" {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	// alt+b should step back to the start of "world", then X inserts there.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}, Alt: true})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'X'}})
	got := m.Value()
	want := "hello Xworld"
	if got != want {
		t.Fatalf("after alt+b insert: got %q, want %q", got, want)
	}
}

// TestNewTextAreaWordForwardCtrlRight checks that the macOS-extended textarea
// key map fires WordForward on ctrl+right.  textarea's vanilla DefaultKeyMap
// omits ctrl+right (textinput has it; textarea does not), so this guards the
// parity fix in macOSTextAreaKeyMap.
func TestNewTextAreaWordForwardCtrlRight(t *testing.T) {
	t.Parallel()

	m := components.NewTextArea()
	m.SetWidth(80)
	m.SetHeight(3)
	m.Focus()
	// Type "hello world" so there is a word boundary to jump across.
	for _, r := range "hello world" {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	// Move cursor to the beginning of the input.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlA})

	// ctrl+right should advance the cursor by one word (past "hello").
	// Verify by inserting X at the new position and checking the resulting string.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlRight})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'X'}})
	got := m.Value()
	want := "helloX world"
	if got != want {
		t.Fatalf("after ctrl+right insert: got %q, want %q", got, want)
	}
}

// TestNewTextAreaWordBackwardCtrlLeft checks that ctrl+left fires WordBackward.
func TestNewTextAreaWordBackwardCtrlLeft(t *testing.T) {
	t.Parallel()

	m := components.NewTextArea()
	m.SetWidth(80)
	m.SetHeight(3)
	m.Focus()
	for _, r := range "hello world" {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	// Cursor is at end; ctrl+left should jump back one word to the start of "world".
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlLeft})

	// Insert X at the new cursor position; should produce "hello Xworld".
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'X'}})
	got := m.Value()
	want := "hello Xworld"
	if got != want {
		t.Fatalf("after ctrl+left insert: got %q, want %q", got, want)
	}
}

// TestNewTextAreaAltRightStillWorks ensures the existing alt+right binding
// was not accidentally removed when adding ctrl+right.
func TestNewTextAreaAltRightStillWorks(t *testing.T) {
	t.Parallel()

	m := components.NewTextArea()
	m.SetWidth(80)
	m.SetHeight(3)
	m.Focus()
	for _, r := range "foo bar" {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlA}) // go to start
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight, Alt: true})

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'X'}})
	got := m.Value()
	want := "fooX bar"
	if got != want {
		t.Fatalf("after alt+right insert: got %q, want %q", got, want)
	}
}

// TestNewTextAreaAltLeftStillWorks ensures the existing alt+left binding
// was not accidentally removed when adding ctrl+left.
func TestNewTextAreaAltLeftStillWorks(t *testing.T) {
	t.Parallel()

	m := components.NewTextArea()
	m.SetWidth(80)
	m.SetHeight(3)
	m.Focus()
	for _, r := range "foo bar" {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyLeft, Alt: true}) // alt+left → word backward

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'X'}})
	got := m.Value()
	want := "foo Xbar"
	if got != want {
		t.Fatalf("after alt+left insert: got %q, want %q", got, want)
	}
}

// TestNewTextInputReturnsUsableModel is a smoke test: NewTextInput should
// produce a functional textinput.Model that accepts and stores characters.
func TestNewTextInputReturnsUsableModel(t *testing.T) {
	t.Parallel()

	m := components.NewTextInput()
	m.Focus()
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h', 'i'}})
	if got := m.Value(); got != "hi" {
		t.Fatalf("value = %q, want %q", got, "hi")
	}
}

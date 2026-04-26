package components

import (
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"
)

// NewTextInput returns a textinput.Model ready for use.
// Use this everywhere in the TUI instead of textinput.New() so that input
// construction is centralised and any future key-binding or style changes
// apply uniformly without hunting call sites.
//
// The default textinput key map already covers all standard macOS terminal
// editing shortcuts (⌥+Backspace → alt+backspace, ⌘+Backspace → ctrl+u,
// ⌥+←/→ → alt+left/right, ⌘+←/→ → ctrl+a/ctrl+e, word movement via
// ctrl+left/ctrl+right).  No extra bindings are required for textinput.
func NewTextInput() textinput.Model {
	return textinput.New()
}

// NewTextArea returns a textarea.Model pre-configured with macOS-compatible
// key bindings and neutral cursor-line styling. Use this everywhere in the TUI
// instead of textarea.New().
//
// textarea's default key map omits ctrl+right and ctrl+left for word movement,
// while textinput's default includes them.  macOSTextAreaKeyMap restores that
// parity so ⌥+→/← works in both component types regardless of whether the
// terminal sends the CSI alt+right/alt+left sequence or the ctrl+right/ctrl+left
// sequence.
//
// Bubbles' default textarea styles paint the cursor line with a background
// (AdaptiveColor{Light:"255", Dark:"0"}) which renders as a visible grey/brown
// band on the active line in most terminal themes. We clear it on both the
// focused and blurred styles so multi-line inputs render cleanly against the
// surrounding overlay background.
func clearTextAreaCursorLine(m *textarea.Model) {
	clear := lipgloss.NewStyle()
	m.FocusedStyle.CursorLine = clear
	m.FocusedStyle.CursorLineNumber = clear
	m.BlurredStyle.CursorLine = clear
	m.BlurredStyle.CursorLineNumber = clear
}

func NewTextArea() textarea.Model {
	m := textarea.New()
	m.KeyMap = macOSTextAreaKeyMap()
	clearTextAreaCursorLine(&m)

	return m
}

// macOSTextAreaKeyMap returns the default textarea key map extended to match
// textinput's word-movement bindings:
//   - WordForward:  adds ctrl+right  (textinput default has it; textarea does not)
//   - WordBackward: adds ctrl+left   (textinput default has it; textarea does not)
//
// It also rebinds InsertNewline so callers can use plain `enter` as a
// confirm/submit key in overlays without losing the ability to insert a
// newline. Bubbles' default binds InsertNewline to `enter`, which conflicts
// with that pattern. We replace it with `alt+enter` and `ctrl+j` — both are
// reliable across macOS terminals (Warp, Terminal.app, iTerm2, kitty, WezTerm).
// `shift+enter` is also listed for forward compatibility, but bubbletea v1
// does not surface a distinct shift+enter key event: most terminals collapse
// shift+enter to a bare `\r` (KeyEnter) and bubbletea does not yet parse the
// CSI u sequence (`\x1b[13;2u`) that modern terminals emit when modifyOtherKeys
// is enabled. Until that lands upstream, users should rely on alt+enter or
// ctrl+j to insert a newline.
//
// All other bindings are preserved unchanged.  The package-level
// textarea.DefaultKeyMap is copied, not mutated, to avoid affecting future
// textarea.New() callers or test helpers.
func macOSTextAreaKeyMap() textarea.KeyMap {
	km := textarea.DefaultKeyMap
	km.WordForward = key.NewBinding(
		key.WithKeys("alt+right", "alt+f", "ctrl+right"),
		key.WithHelp("alt+right", "word forward"),
	)
	km.WordBackward = key.NewBinding(
		key.WithKeys("alt+left", "alt+b", "ctrl+left"),
		key.WithHelp("alt+left", "word backward"),
	)
	km.InsertNewline = key.NewBinding(
		key.WithKeys("shift+enter", "alt+enter", "ctrl+j"),
		key.WithHelp("alt+enter", "insert newline"),
	)

	return km
}

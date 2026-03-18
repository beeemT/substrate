package components

import (
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
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
// key bindings.  Use this everywhere in the TUI instead of textarea.New().
//
// textarea's default key map omits ctrl+right and ctrl+left for word movement,
// while textinput's default includes them.  macOSTextAreaKeyMap restores that
// parity so ⌥+→/← works in both component types regardless of whether the
// terminal sends the CSI alt+right/alt+left sequence or the ctrl+right/ctrl+left
// sequence.
func NewTextArea() textarea.Model {
	m := textarea.New()
	m.KeyMap = macOSTextAreaKeyMap()

	return m
}

// macOSTextAreaKeyMap returns the default textarea key map extended to match
// textinput's word-movement bindings:
//   - WordForward:  adds ctrl+right  (textinput default has it; textarea does not)
//   - WordBackward: adds ctrl+left   (textinput default has it; textarea does not)
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

	return km
}

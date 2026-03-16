package views

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
)

// macOSKeyFilter is a tea.WithFilter hook that runs before every message is
// dispatched to the model tree.  It is the extension point for normalising
// terminal-specific key sequences (e.g. kitty extended keyboard protocol
// sequences emitted by Warp) into their standard ctrl/alt equivalents.
//
// The standard macOS editing shortcuts (⌥+Backspace → alt+backspace,
// ⌘+Backspace → ctrl+u, ⌘+← → ctrl+a, …) are already parsed correctly by
// bubbletea and reach inputs via the macOS key maps in components.NewTextInput
// and components.NewTextArea.  This filter is the escape hatch for any
// sequence that bubbletea does NOT parse — identified via SUBSTRATE_KEY_DEBUG.
func macOSKeyFilter(_ tea.Model, msg tea.Msg) tea.Msg {
	if os.Getenv("SUBSTRATE_KEY_DEBUG") == "1" {
		if f, err := os.OpenFile("/tmp/substrate-keys.log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
			fmt.Fprintf(f, "%T %+v\n", msg, msg)
			f.Close()
		}
	}
	return msg
}

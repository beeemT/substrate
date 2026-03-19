package views

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
)

// macOSKeyFilter is a tea.WithFilter hook passed to tea.NewProgram.
//
// When SUBSTRATE_KEY_DEBUG=1, it logs every tea.Msg to
// /tmp/substrate-keys.log so that raw key sequences can be inspected
// (e.g. with cmd/keytest) without requiring a separate process.
// In all other circumstances it is a transparent pass-through.
func macOSKeyFilter(_ tea.Model, msg tea.Msg) tea.Msg {
	if os.Getenv("SUBSTRATE_KEY_DEBUG") == "1" {
		if f, err := os.OpenFile("/tmp/substrate-keys.log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
			fmt.Fprintf(f, "%T %+v\n", msg, msg)
			f.Close() //nolint:errcheck
		}
	}
	return msg
}

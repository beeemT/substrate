package views

import (
	"fmt"
	"os"
	"reflect"

	tea "github.com/charmbracelet/bubbletea"
)

// enableKittyKeyboard writes the kitty keyboard protocol enable sequence to the
// terminal output.  Flag 1 (disambiguate escape codes) is the only flag we need:
// it causes the terminal to send unambiguous sequences for modifier+backspace
// combinations (e.g. ⌥+Backspace → \x1b[127;3u) without changing any sequences
// that bubbletea already knows how to parse.
//
// Must be called before tea.Program.Run() so it reaches the terminal before
// bubbletea enters raw mode.  Terminals that do not support the kitty protocol
// silently ignore the sequence.
func enableKittyKeyboard() {
	// Write to stderr, which is bubbletea's default renderer output and is
	// therefore the fd connected to the controlling terminal.
	os.Stderr.WriteString("\x1b[>1u") //nolint:errcheck
}

// disableKittyKeyboard pops the kitty keyboard protocol stack, restoring the
// terminal's previous keyboard encoding.  Must be called after tea.Program.Run()
// returns.
func disableKittyKeyboard() {
	os.Stderr.WriteString("\x1b[<u") //nolint:errcheck
}

// macOSKeyFilter is a tea.WithFilter hook that runs before every message is
// dispatched to the model tree.
//
// Its two responsibilities:
//  1. Translate kitty keyboard protocol sequences that bubbletea v1 does not
//     natively understand into the tea.KeyMsg equivalents that the bubbles input
//     key maps already bind.  These sequences arrive as the unexported
//     tea.unknownCSISequenceMsg type (a []byte alias) when kitty protocol flag 1
//     is active via enableKittyKeyboard.
//  2. Optionally log every tea.Msg type and value to /tmp/substrate-keys.log
//     when SUBSTRATE_KEY_DEBUG=1, for diagnosing unexpected terminal sequences.
func macOSKeyFilter(_ tea.Model, msg tea.Msg) tea.Msg {
	if os.Getenv("SUBSTRATE_KEY_DEBUG") == "1" {
		if f, err := os.OpenFile("/tmp/substrate-keys.log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
			fmt.Fprintf(f, "%T %+v\n", msg, msg)
			f.Close()
		}
	}

	if translated, ok := translateKittySequence(msg); ok {
		return translated
	}
	return msg
}

// translateKittySequence detects kitty keyboard protocol CSI sequences inside
// bubbletea's unknownCSISequenceMsg and returns the equivalent tea.KeyMsg.
//
// unknownCSISequenceMsg is an unexported type in bubbletea defined as []byte.
// We access it via reflection to avoid a dependency on package internals.  The
// type name check makes the match precise: any future bubbletea refactor that
// renames the type will cause translateKittySequence to silently return false
// rather than misinterpreting an unrelated []byte message.
func translateKittySequence(msg tea.Msg) (tea.KeyMsg, bool) {
	rv := reflect.ValueOf(msg)
	if rv.Kind() != reflect.Slice || rv.Type().String() != "tea.unknownCSISequenceMsg" {
		return tea.KeyMsg{}, false
	}
	switch string(rv.Bytes()) {
	case "\x1b[127;3u":
		// Kitty: ⌥+Backspace  →  alt+backspace  →  DeleteWordBackward
		return tea.KeyMsg{Type: tea.KeyBackspace, Alt: true}, true
	case "\x1b[127;5u":
		// Kitty: Ctrl+Backspace  →  ctrl+w  →  DeleteWordBackward
		return tea.KeyMsg{Type: tea.KeyCtrlW}, true
	case "\x1b[127;9u":
		// Kitty: ⌘+Backspace (Super)  →  ctrl+u  →  DeleteBeforeCursor
		return tea.KeyMsg{Type: tea.KeyCtrlU}, true
	}
	return tea.KeyMsg{}, false
}

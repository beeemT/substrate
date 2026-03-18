package views

import (
	"fmt"
	"os"
	"reflect"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
)

// kittyOnce ensures the kitty keyboard protocol enable sequence is written
// exactly once, on the first message that passes through macOSKeyFilter.
// That first message is always received after bubbletea has entered the alt
// screen, which is important: Warp (like xterm) maintains separate kitty
// keyboard stacks for the main screen and the alt screen.  Writing the
// enable sequence before p.Run() pushes onto the main-screen stack; alt
// screen entry starts with an empty stack and the flags are not inherited.
// Writing from within the filter guarantees we push onto the alt-screen stack.
var kittyOnce sync.Once

// macOSKeyFilter is a tea.WithFilter hook that runs before every message is
// dispatched to the model tree.
//
// Its three responsibilities:
//  1. Enable kitty keyboard protocol flag 1 the first time it is called.
//     This fires after bubbletea has entered the alt screen so the flag is
//     pushed onto the alt-screen kitty stack where it takes effect.
//  2. Translate kitty keyboard protocol sequences that bubbletea v1 does not
//     natively understand into tea.KeyMsg equivalents that the bubbles input
//     key maps already bind.  These sequences arrive as the unexported
//     tea.unknownCSISequenceMsg type (a []byte alias).
//  3. Optionally log every tea.Msg type and value to /tmp/substrate-keys.log
//     when SUBSTRATE_KEY_DEBUG=1, for diagnosing unexpected terminal sequences.
func macOSKeyFilter(_ tea.Model, msg tea.Msg) tea.Msg {
	// Enable kitty keyboard protocol flag 1 (disambiguate escape codes) on the
	// first message.  Flag 1 is the minimal safe subset: it only adds new
	// encodings for previously ambiguous modifier+backspace sequences without
	// changing anything bubbletea already parses.  Terminals that do not
	// support the kitty protocol silently ignore this sequence.
	kittyOnce.Do(func() {
		// Bubbletea's default output is os.Stdout.  Write there so the
		// sequence reaches the same terminal fd the renderer uses.
		os.Stdout.WriteString("\x1b[>1u") //nolint:errcheck
	})

	// Pop the kitty stack before bubbletea exits the alt screen so the
	// main-screen keyboard encoding is left clean.
	if _, ok := msg.(tea.QuitMsg); ok {
		os.Stdout.WriteString("\x1b[<u") //nolint:errcheck
		return msg
	}

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
// renames the type degrades silently to a no-op rather than misinterpreting
// an unrelated []byte message.
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

package views

import (
	"fmt"
	"os"
	"reflect"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
)

// Kitty keyboard protocol state.
//
// The protocol requires a query→response→enable handshake: the terminal only
// honours a push if it has already answered a query, confirming support.
// (Terminals that do not support kitty simply never respond to the query and
// nothing changes.)
//
// All variables below are accessed exclusively from bubbletea's event-loop
// goroutine (the only goroutine that calls macOSKeyFilter), so no atomics are
// needed.  kittyQueryOnce is the exception: it may be called re-entrantly via
// sync.Once, but Once is already safe for that.
var (
	// kittyQueryOnce sends \x1b[?u on the very first message through the
	// filter, which is guaranteed to arrive after bubbletea has entered the
	// alt screen (so the eventual push lands on the alt-screen kitty stack).
	kittyQueryOnce sync.Once

	// kittyActive is true once the terminal has responded to our query and
	// we have pushed flag 1 (disambiguate escape codes).
	kittyActive bool
)

// macOSKeyFilter is a tea.WithFilter hook that runs before every message is
// dispatched to the model tree.
//
// Responsibilities:
//  1. Negotiate the kitty keyboard protocol with the terminal:
//     - On the first message, send \x1b[?u (query current flags).
//     - When the terminal responds (\x1b[?<n>u, confirming support), push
//     flag 1 via \x1b[>1u.  Flag 1 (disambiguate escape codes) causes the
//     terminal to send \x1b[127;3u for ⌥+Backspace instead of stripping
//     the modifier and sending plain \x7f.
//  2. Translate kitty CSI sequences into tea.KeyMsg equivalents.
//  3. Optionally log every tea.Msg to /tmp/substrate-keys.log when
//     SUBSTRATE_KEY_DEBUG=1 (also logs kitty handshake state transitions).
func macOSKeyFilter(_ tea.Model, msg tea.Msg) tea.Msg {
	// Step 1: on first message, send the kitty protocol query.
	// This fires after bubbletea has entered the alt screen.
	kittyQueryOnce.Do(func() {
		// Ask the terminal for its current kitty keyboard flags.
		// Terminals that support kitty respond with \x1b[?<n>u.
		// Terminals that do not support kitty silently ignore this.
		os.Stdout.WriteString("\x1b[?u") //nolint:errcheck
		if os.Getenv("SUBSTRATE_KEY_DEBUG") == "1" {
			appendKeyLog("kitty: query sent \\x1b[?u")
		}
	})

	// Step 2: if waiting for the kitty response, detect it here.
	// The response (\x1b[?<n>u) arrives as an unknownCSISequenceMsg since
	// bubbletea does not natively parse kitty protocol status reports.
	if !kittyActive && isKittyQueryResponse(msg) {
		kittyActive = true
		// Push flag 1: disambiguate escape codes.  This is the minimal safe
		// subset — it only adds new encodings for previously ambiguous sequences
		// (modifier+backspace) and leaves all existing bubbletea-parsed
		// sequences byte-for-byte unchanged.
		os.Stdout.WriteString("\x1b[>1u") //nolint:errcheck
		if os.Getenv("SUBSTRATE_KEY_DEBUG") == "1" {
			rv := reflect.ValueOf(msg)
			appendKeyLog(fmt.Sprintf("kitty: response received %q, pushed flag 1", string(rv.Bytes())))
		}
		// Let the response fall through to the model; Update() will ignore
		// the unknown unknownCSISequenceMsg silently.
		return msg
	}

	// Pop the kitty stack before bubbletea exits the alt screen so the
	// main-screen keyboard encoding is left clean.
	if _, ok := msg.(tea.QuitMsg); ok {
		if kittyActive {
			os.Stdout.WriteString("\x1b[<u") //nolint:errcheck
			if os.Getenv("SUBSTRATE_KEY_DEBUG") == "1" {
				appendKeyLog("kitty: stack popped on quit")
			}
		}
		return msg
	}

	if os.Getenv("SUBSTRATE_KEY_DEBUG") == "1" {
		if f, err := os.OpenFile("/tmp/substrate-keys.log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
			fmt.Fprintf(f, "%T %+v\n", msg, msg)
			f.Close()
		}
	}

	// Step 3: translate kitty sequences to tea.KeyMsg once the protocol is
	// active.  Only relevant after the handshake completes.
	if kittyActive {
		if translated, ok := translateKittySequence(msg); ok {
			return translated
		}
	}

	return msg
}

// isKittyQueryResponse returns true when msg is the terminal's response to a
// kitty keyboard protocol query.  The response has the form \x1b[?<n>u where
// n is zero or more decimal digits.
func isKittyQueryResponse(msg tea.Msg) bool {
	rv := reflect.ValueOf(msg)
	if rv.Kind() != reflect.Slice || rv.Type().String() != "tea.unknownCSISequenceMsg" {
		return false
	}
	b := rv.Bytes()
	// Minimum: ESC [ ? u  (4 bytes, n=empty treated as 0 by some terminals)
	// Typical: ESC [ ? 0 u  (5 bytes)
	if len(b) < 4 || b[0] != '\x1b' || b[1] != '[' || b[2] != '?' {
		return false
	}
	if b[len(b)-1] != 'u' {
		return false
	}
	// All bytes between '?' and 'u' must be ASCII digits.
	for _, c := range b[3 : len(b)-1] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// translateKittySequence detects kitty keyboard protocol CSI sequences inside
// bubbletea's unknownCSISequenceMsg and returns the equivalent tea.KeyMsg.
//
// unknownCSISequenceMsg is an unexported type in bubbletea defined as []byte.
// We access it via reflection to avoid a dependency on package internals.  The
// type-name check makes the match precise: a bubbletea refactor that renames
// the type degrades silently to a no-op.
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

// appendKeyLog writes a single timestamped line to the debug key log.
func appendKeyLog(line string) {
	if f, err := os.OpenFile("/tmp/substrate-keys.log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
		fmt.Fprintln(f, line)
		f.Close()
	}
}

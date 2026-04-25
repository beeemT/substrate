package components

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// DefaultGrowingTextAreaMaxLines is the cap used when no MaxLines is set.
const DefaultGrowingTextAreaMaxLines = 6

// GrowingTextAreaScrollMsg is emitted by GrowingTextArea.Update when SGR mouse
// wheel fragments leak into the textarea while it is focused (terminals that
// keep emitting CSI sequences after mouse reporting was disabled, or before
// the host had a chance to disable it). Hosts forward Delta to their viewport:
// negative scrolls up, positive scrolls down, in viewport rows.
type GrowingTextAreaScrollMsg struct {
	// Source identifies the GrowingTextArea instance that emitted the message
	// when a host owns more than one. Hosts that own a single instance can
	// ignore it.
	Source string
	// Delta is the number of viewport rows to scroll. Negative = up, positive = down.
	Delta int
}

// GrowingTextArea is a single-purpose component: a textarea that starts at one
// row and grows up to MaxLines as the wrapped content gets taller, then caps.
//
// Usage:
//   - Construct with NewGrowingTextArea(); set Placeholder, MaxLines, CharLimit
//     before first SetWidth/Focus as needed.
//   - Call SetWidth on every layout change.
//   - Call Focus()/Blur() — both return tea.Cmd that toggle terminal mouse
//     reporting (Disable on Focus, Enable on Blur). Batch with any other host
//     command.
//   - Forward all key/mouse events that the host does not consume itself to
//     Update; Update returns a tea.Cmd that may carry GrowingTextAreaScrollMsg
//     for the host to route to its viewport.
//   - Reset() clears the value, returns the height to 1, blurs, and re-enables
//     mouse reporting. The returned cmd MUST be batched into the host's reply.
//
// Submission semantics are owned by the host. The host MUST intercept
// `enter` (KeyEnter) before forwarding events; the embedded textarea's
// keymap (see NewTextArea / macOSTextAreaKeyMap) only treats `alt+enter`,
// `shift+enter`, and `ctrl+j` as newline insertion, so plain `enter` is safe
// to use as submit upstream.
type GrowingTextArea struct {
	id       string
	model    textarea.Model
	maxLines int
	height   int

	// pendingBracket holds a buffered '[' rune that may start an SGR mouse
	// fragment. Resolved on the next event: real fragment → discarded as
	// scroll; unrelated input → flushed into the textarea.
	pendingBracket bool
}

// NewGrowingTextArea returns a GrowingTextArea ready to use. id labels the
// instance for hosts that route GrowingTextAreaScrollMsg from multiple
// components; pass "" if you only have one.
func NewGrowingTextArea(id string) GrowingTextArea {
	ta := NewTextArea()
	ta.ShowLineNumbers = false
	ta.Prompt = ""
	ta.EndOfBufferCharacter = 0
	ta.MaxHeight = DefaultGrowingTextAreaMaxLines
	ta.SetHeight(1)

	return GrowingTextArea{
		id:       id,
		model:    ta,
		maxLines: DefaultGrowingTextAreaMaxLines,
		height:   1,
	}
}

// SetMaxLines sets the upper bound on visual rows. Values < 1 are clamped to 1.
// MaxHeight on the underlying textarea is updated to match.
func (g *GrowingTextArea) SetMaxLines(n int) {
	if n < 1 {
		n = 1
	}
	g.maxLines = n
	g.model.MaxHeight = n
	if g.height > n {
		g.height = n
		g.model.SetHeight(n)
	}
}

// SetPlaceholder updates the placeholder text shown when empty.
func (g *GrowingTextArea) SetPlaceholder(s string) { g.model.Placeholder = s }

// SetCharLimit sets the maximum total characters; 0 = unlimited.
func (g *GrowingTextArea) SetCharLimit(n int) { g.model.CharLimit = n }

// SetWidth recomputes the textarea inner width and resyncs the visual height
// (a width change can change wrapped line count).
func (g *GrowingTextArea) SetWidth(w int) {
	if w < 1 {
		w = 1
	}
	g.model.SetWidth(w)
	g.syncHeight()
}

// Width returns the configured inner width.
func (g GrowingTextArea) Width() int { return g.model.Width() }

// Height returns the current visual row count (1..MaxLines).
func (g GrowingTextArea) Height() int { return g.height }

// Focused reports whether the textarea is focused.
func (g GrowingTextArea) Focused() bool { return g.model.Focused() }

// Focus focuses the textarea and disables terminal mouse reporting so wheel
// events do not leak into the textarea as text. Batch the returned cmd with
// any host commands.
func (g *GrowingTextArea) Focus() tea.Cmd {
	c := g.model.Focus()
	return tea.Batch(c, tea.DisableMouse)
}

// Blur blurs the textarea and re-enables cell-motion mouse reporting.
func (g *GrowingTextArea) Blur() tea.Cmd {
	g.model.Blur()
	g.pendingBracket = false
	return tea.EnableMouseCellMotion
}

// Value returns the current textarea value.
func (g GrowingTextArea) Value() string { return g.model.Value() }

// SetValue replaces the textarea content and re-syncs visual height.
func (g *GrowingTextArea) SetValue(s string) {
	g.model.SetValue(s)
	g.syncHeight()
}

// Reset clears value, returns height to 1, blurs, drops any buffered SGR
// fragment state, and returns a cmd that re-enables cell-motion mouse
// reporting. Batch the cmd with any host reply.
func (g *GrowingTextArea) Reset() tea.Cmd {
	g.model.SetValue("")
	g.height = 1
	g.model.SetHeight(1)
	g.model.Blur()
	g.pendingBracket = false
	return tea.EnableMouseCellMotion
}

// View renders the textarea.
func (g GrowingTextArea) View() string { return g.model.View() }

// Update forwards the message to the embedded textarea and grows the visual
// height to fit. SGR mouse fragments are intercepted: matched wheel events
// become GrowingTextAreaScrollMsg and never reach the textarea. Returns the
// updated component and any cmd produced (textarea cmd or scroll-intent).
func (g GrowingTextArea) Update(msg tea.Msg) (GrowingTextArea, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		if cmd, handled := g.handleSGRFragment(key); handled {
			return g, cmd
		}
	}

	var cmd tea.Cmd
	g.model, cmd = g.model.Update(msg)
	g.syncHeight()
	return g, cmd
}

// Flush resolves any buffered SGR fragment state by inserting the buffered
// '[' (if any) into the textarea. Hosts that intercept Enter (or any other
// terminal key) before forwarding to Update MUST call Flush before reading
// Value(), so a trailing '[' typed by the user is preserved on submit.
func (g *GrowingTextArea) Flush() {
	if !g.pendingBracket {
		return
	}
	g.pendingBracket = false
	g.model, _ = g.model.Update(
		tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'['}},
	)
	g.syncHeight()
}

// syncHeight updates the visual row count from the wrapped content, capped
// at maxLines. SetValue is re-issued after SetHeight so the textarea's
// internal viewport resets to row 0 and content stays visible as it grows
// (textarea.SetHeight alone does not move the viewport).
func (g *GrowingTextArea) syncHeight() {
	width := g.model.Width()
	if width <= 0 {
		return
	}
	want := wrappedRowCount(g.model.Value(), width)
	if want < 1 {
		want = 1
	}
	if want > g.maxLines {
		want = g.maxLines
	}
	if want == g.height {
		return
	}
	g.height = want
	g.model.SetHeight(want)
	// SetHeight only changes the viewport box; SetValue forces the internal
	// viewport to reset to row 0 and re-insert content with the cursor at end.
	g.model.SetValue(g.model.Value())
}

// wrappedRowCount returns the number of visual rows occupied by value when
// soft-wrapped at width display columns. Empty input wraps to one row.
// Tabs are expanded to four spaces for width measurement only.
func wrappedRowCount(value string, width int) int {
	if width <= 0 {
		return 1
	}
	logical := strings.Split(value, "\n")
	total := 0
	for _, line := range logical {
		total += wrappedLineRowCount(line, width)
	}
	if total < 1 {
		return 1
	}
	return total
}

// wrappedLineRowCount counts soft-wrapped rows for a single logical line.
// Wrapping mirrors the textarea's word-greedy behavior: words are placed on
// the current row when they fit; otherwise a new row starts. Words wider
// than width consume ceil(width-of-word / width) rows on their own.
func wrappedLineRowCount(line string, width int) int {
	if strings.TrimSpace(line) == "" {
		return 1
	}
	expanded := strings.ReplaceAll(line, "\t", "    ")
	words := strings.Fields(expanded)
	if len(words) == 0 {
		return 1
	}
	rows := 1
	current := 0
	for _, w := range words {
		wordWidth := ansi.StringWidth(w)
		if current == 0 {
			// First word on a row: occupy as many rows as needed if it overflows.
			if wordWidth > width {
				rows += (wordWidth - 1) / width
				current = wordWidth % width
				if current == 0 {
					current = width
				}
				continue
			}
			current = wordWidth
			continue
		}
		candidate := current + 1 + wordWidth
		if candidate > width {
			rows++
			if wordWidth > width {
				rows += (wordWidth - 1) / width
				current = wordWidth % width
				if current == 0 {
					current = width
				}
				continue
			}
			current = wordWidth
			continue
		}
		current = candidate
	}
	return rows
}

// handleSGRFragment intercepts key events that look like SGR mouse escape
// sequence bodies leaking into the textarea. Returns (cmd, true) when the
// event was consumed (either buffered, discarded, or converted to a scroll
// intent); (nil, false) when the host should proceed with normal Update.
func (g *GrowingTextArea) handleSGRFragment(msg tea.KeyMsg) (tea.Cmd, bool) {
	if msg.Type != tea.KeyRunes {
		if g.pendingBracket {
			// Non-rune key with pending bracket — flush the buffered '['
			// into the textarea, then let the caller continue with msg.
			g.pendingBracket = false
			g.model, _ = g.model.Update(
				tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'['}},
			)
		}
		return nil, false
	}

	// Discard Alt-modified runes that are entirely SGR body chars. These
	// come from \x1b being parsed together with the next byte as Alt+key
	// (e.g. \x1b[ → Alt+[).
	if msg.Alt && allSGRBodyRunes(msg.Runes) {
		g.pendingBracket = false
		return nil, true
	}

	// Resolve a pending '[' against the current runes.
	if g.pendingBracket {
		g.pendingBracket = false
		if len(msg.Runes) > 0 && msg.Runes[0] == '<' && allSGRBodyRunes(msg.Runes) {
			combined := append([]rune{'['}, msg.Runes...)
			delta := extractSGRScrollLines(combined)
			if delta == 0 {
				return nil, true
			}
			id := g.id
			return func() tea.Msg { return GrowingTextAreaScrollMsg{Source: id, Delta: delta} }, true
		}
		// Not a fragment — flush the buffered '[' and fall through to
		// process the current msg normally below.
		g.model, _ = g.model.Update(
			tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'['}},
		)
	}

	// Buffer a lone '[' as a potential SGR fragment start.
	if len(msg.Runes) == 1 && msg.Runes[0] == '[' {
		g.pendingBracket = true
		return nil, true
	}

	// Intercept multi-rune SGR mouse fragments.
	if isLikelySGRMouseFragment(msg.Runes) {
		delta := extractSGRScrollLines(msg.Runes)
		if delta == 0 {
			return nil, true
		}
		id := g.id
		return func() tea.Msg { return GrowingTextAreaScrollMsg{Source: id, Delta: delta} }, true
	}

	return nil, false
}

// sgrMouseFragRe extracts button codes from complete SGR mouse escape sequence
// bodies. It is non-anchored so it can find multiple fragments concatenated in
// a single KeyRunes event (common under heavy scroll when ESC bytes between
// sequences are stripped or split off by the terminal).
var sgrMouseFragRe = regexp.MustCompile(`\[<(\d+);\d+;\d+[Mm]`)

// allSGRBodyRunes reports whether every rune could appear in the body of an
// SGR mouse escape sequence: [ < 0-9 ; M m. The empty input is vacuously true.
func allSGRBodyRunes(runes []rune) bool {
	for _, r := range runes {
		switch {
		case r >= '0' && r <= '9':
		case r == '[', r == '<', r == ';', r == 'M', r == 'm':
		default:
			return false
		}
	}
	return true
}

// isLikelySGRMouseFragment reports whether runes look like the body (or
// concatenated bodies) of SGR mouse escape sequences stripped of their
// leading ESC byte. Single-rune events are not flagged, to avoid blocking
// legitimate single-character typing.
func isLikelySGRMouseFragment(runes []rune) bool {
	return len(runes) >= 2 && allSGRBodyRunes(runes)
}

// extractSGRScrollLines scans runes for complete SGR mouse sequence bodies
// and returns the total viewport lines to scroll: negative for up, positive
// for down. Fragments without a complete [<btn;col;rowM pattern are ignored.
func extractSGRScrollLines(runes []rune) int {
	const linesPerTick = 3
	matches := sgrMouseFragRe.FindAllStringSubmatch(string(runes), -1)
	scroll := 0
	for _, m := range matches {
		btn, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		// SGR button encoding: bit 6 (value 64) = wheel flag, bit 0 = direction.
		const wheelBit = 0b0100_0000
		if btn&wheelBit == 0 {
			continue // non-wheel mouse event (click/drag)
		}
		if btn&1 == 0 {
			scroll -= linesPerTick // scroll up
		} else {
			scroll += linesPerTick // scroll down
		}
	}
	return scroll
}

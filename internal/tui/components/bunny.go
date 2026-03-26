package components

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// BunnySide determines on which corner of the box the bunny sits.
// The value is chosen randomly at startup and held for the lifetime of the model.
type BunnySide int

const (
	BunnySideLeft  BunnySide = iota // bunny sits on the top-left corner  (╭)
	BunnySideRight                  // bunny sits on the top-right corner (╮)
)

// BunnyBlinkMsg is dispatched when the blink animation state should change.
// Phase 0 = eyes open, phase 1 = eyes closed.
type BunnyBlinkMsg struct{ Phase int }

// BunnyOpenCmd schedules the next blink 4 seconds from now.
// It fires after the bunny's eyes are already open, signalling time to blink.
func BunnyOpenCmd() tea.Cmd {
	return tea.Tick(4*time.Second, func(time.Time) tea.Msg {
		return BunnyBlinkMsg{Phase: 1}
	})
}

// BunnyCloseCmd schedules the eye-open 200 ms from now.
// It fires after the bunny has blinked, signalling time to reopen.
func BunnyCloseCmd() tea.Cmd {
	return tea.Tick(200*time.Millisecond, func(time.Time) tea.Msg {
		return BunnyBlinkMsg{Phase: 0}
	})
}

// bunnyFrames[side][phase] holds the three display lines of the ASCII bunny.
//
// Left side (sits at ╭, feet flush-left):
//
//	  (\(\
//	 ( ^ω^)   or   ( -ω-)
//	o_(")(")
//
// Right side (sits at ╮, feet flush-right):
//
//	/)/)
//	(^ω^ )    or   (-ω- )
//	(")(")_o
//
// The right-side lines carry trailing spaces so that lipgloss.JoinVertical
// with Right alignment produces the same breathing room as the left side:
//   - ears end 2 columns before the right edge (matching the left's 2 leading spaces)
//   - face ends 1 column before the right edge (matching the left's 1 leading space)
var bunnyFrames = [2][2][3]string{
	BunnySideLeft: {
		{`  (\(\`, ` ( ^ω^)`, `o_(")(")`}, // phase 0: eyes open
		{`  (\(\`, ` ( -ω-)`, `o_(")(")`}, // phase 1: eyes closed
	},
	BunnySideRight: {
		{"/)/)  ", "(^ω^ ) ", `(")(")_o`}, // phase 0: eyes open
		{"/)/)  ", "(-ω- ) ", `(")(")_o`}, // phase 1: eyes closed
	},
}

// RenderBunny returns the 3-line ASCII bunny art for the given blink phase and
// corner side. The bottom line is the feet; callers should join this string
// above a bordered box using the matching lipgloss alignment so the foot
// character touches the box corner.
func RenderBunny(phase int, side BunnySide) string {
	f := bunnyFrames[side][phase%2]
	return f[0] + "\n" + f[1] + "\n" + f[2]
}

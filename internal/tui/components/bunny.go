package components

import (
	"math/rand"
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

// BunnyHopTriggerMsg begins a hop sequence.
// Hops is the number of mid-air frames (2 or 3), chosen randomly at fire time.
type BunnyHopTriggerMsg struct{ Hops int }

// BunnyHopStepMsg advances the hop by one frame.
type BunnyHopStepMsg struct{}

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

// BunnyHopIdleCmd waits 10–25 seconds and then fires a hop sequence.
// The hop count (2 or 3) is chosen at fire time so that multiple instances
// started at the same wall-clock second don't all hop in sync.
func BunnyHopIdleCmd() tea.Cmd {
	delay := time.Duration(10+rand.Intn(16)) * time.Second
	return tea.Tick(delay, func(time.Time) tea.Msg {
		return BunnyHopTriggerMsg{Hops: 2 + rand.Intn(2)}
	})
}

// BunnyHopStepCmd advances the hop animation by one frame after 150 ms.
func BunnyHopStepCmd() tea.Cmd {
	return tea.Tick(150*time.Millisecond, func(time.Time) tea.Msg {
		return BunnyHopStepMsg{}
	})
}

// bunnyFrames[side][phase] holds the three display lines of the stationary bunny.
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

// bunnyHopFrames[phase] holds the three lines for the mid-hop pose.
//
// The feet line uses two leading spaces instead of o_ / _o to suggest the
// bunny is airborne; actual horizontal position is handled by the caller.
// The widest line is `  (")(")` at 8 display columns, matching stationary art.
var bunnyHopFrames = [2][3]string{
	{`  (\(\`, ` ( ^ω^)`, `  (")(")`}, // phase 0: eyes open
	{`  (\(\`, ` ( -ω-)`, `  (")(")`}, // phase 1: eyes closed
}

// RenderBunny returns the 3-line ASCII bunny art for the given blink phase and
// corner side. The bottom line is the feet; callers should join this string
// above a bordered box using the matching lipgloss alignment so the foot
// character touches the box corner.
func RenderBunny(phase int, side BunnySide) string {
	f := bunnyFrames[side][phase%2]
	return f[0] + "\n" + f[1] + "\n" + f[2]
}

// RenderBunnyHop returns the 3-line mid-hop bunny art for the given blink phase.
// Lines have no intrinsic horizontal position: the caller is responsible for
// padding each line to the desired offset within the container width.
func RenderBunnyHop(phase int) string {
	f := bunnyHopFrames[phase%2]
	return f[0] + "\n" + f[1] + "\n" + f[2]
}

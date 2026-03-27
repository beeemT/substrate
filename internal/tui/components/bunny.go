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
// Hops is the number of individual hops (2 or 3), chosen randomly at fire time.
// A 2-hop sequence touches the box once; a 3-hop sequence touches it twice.
type BunnyHopTriggerMsg struct{ Hops int }

// BunnyHopStepMsg advances the hop animation by one frame.
// It is reused for both in-hop frame advances and between-hop pauses.
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

// BunnyHopTick schedules a BunnyHopStepMsg after the given duration.
func BunnyHopTick(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg {
		return BunnyHopStepMsg{}
	})
}

// BunnyHopPauseTick schedules a BunnyHopStepMsg after the between-hop pause.
// This gives the bunny a moment on the box before launching again.
func BunnyHopPauseTick() tea.Cmd {
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

// bunnyHopFrames[phase] holds the three lines for the airborne pose.
//
// The feet line uses two leading spaces instead of o_ / _o to suggest the
// bunny is airborne; actual horizontal position is handled by the caller.
// The widest line is `  (")(")` at 8 display columns, matching stationary art.
var bunnyHopFrames = [2][3]string{
	{`  (\(\`, ` ( ^ω^)`, `  (")(")`}, // phase 0: eyes open
	{`  (\(\`, ` ( -ω-)`, `  (")(")`}, // phase 1: eyes closed
}

// bunnyCrouchFrames[phase] holds the three lines for the crouched pose.
// Used at the start and end of each individual hop to show the bunny
// compressing on the box before launching and on impact after landing.
//
// The face is lowered (2-space indent matching ears) and feet are spread
// wide with paw indicators (o) on both sides to convey downward pressure.
var bunnyCrouchFrames = [2][3]string{
	{`  (\(\`, `  ( ^ω^)`, `o_(")(")`}, // phase 0: eyes open
	{`  (\(\`, `  ( -ω-)`, `o_(")(")`}, // phase 1: eyes closed
}

// RenderBunny returns the 3-line ASCII bunny art for the given blink phase and
// corner side. The bottom line is the feet; callers should join this string
// above a bordered box using the matching lipgloss alignment so the foot
// character touches the box corner.
func RenderBunny(phase int, side BunnySide) string {
	f := bunnyFrames[side][phase%2]
	return f[0] + "\n" + f[1] + "\n" + f[2]
}

// RenderBunnyHop returns the 3-line airborne bunny art for the given blink phase.
// Lines have no intrinsic horizontal position: the caller is responsible for
// padding each line to the desired offset within the container width.
func RenderBunnyHop(phase int) string {
	f := bunnyHopFrames[phase%2]
	return f[0] + "\n" + f[1] + "\n" + f[2]
}

// RenderBunnyCrouch returns the 3-line crouched bunny art for the given blink phase.
// Like RenderBunnyHop, lines have no intrinsic horizontal position.
func RenderBunnyCrouch(phase int) string {
	f := bunnyCrouchFrames[phase%2]
	return f[0] + "\n" + f[1] + "\n" + f[2]
}

// FramesPerHop is the number of animation frames in a single hop.
// Frame sequence: crouch(0), rise(1), peak(2), fall(3), land(4).
const FramesPerHop = 5

// HopFrameGap returns the number of blank lines between the bunny and the box
// for the given frame index within a hop. This creates the vertical arc:
// crouch/land on the box (0 gaps), rise/fall at low height (1 gap), peak at max height (2 gaps).
func HopFrameGap(frame int) int {
	switch frame {
	case 0, 4:
		return 0 // crouch, land — on the box
	case 1, 3:
		return 1 // rise, fall — low air
	case 2:
		return 2 // peak — max height
	default:
		return 0
	}
}

// HopFrameProgress returns the horizontal progress (0.0–1.0) through the current
// individual hop for the given frame. The bunny accelerates during the first half
// and decelerates during the second half, mimicking a natural parabolic arc.
func HopFrameProgress(frame int) float64 {
	switch frame {
	case 0:
		return 0.0 // crouch — at hop start
	case 1:
		return 0.25 // rise — launching forward
	case 2:
		return 0.55 // peak — maximum forward speed
	case 3:
		return 0.82 // fall — decelerating
	case 4:
		return 1.0 // land — at hop end
	default:
		return 0.0
	}
}

// HopFrameDuration returns the tick duration for each frame within a hop.
// Crouch/land are short (impact), rise/fall are medium, peak is longest (apex hang).
func HopFrameDuration(frame int) time.Duration {
	switch frame {
	case 0, 4:
		return 80 * time.Millisecond // crouch, land
	case 1, 3:
		return 100 * time.Millisecond // rise, fall
	case 2:
		return 120 * time.Millisecond // peak
	default:
		return 100 * time.Millisecond
	}
}

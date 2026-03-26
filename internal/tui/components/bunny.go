package components

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
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

// bunnyFrames holds the three display lines for each blink phase.
// Index 0 = eyes open, index 1 = eyes closed.
var bunnyFrames = [2][3]string{
	{`  (\(\`, ` ( ^ω^)`, `o_(")(")`},
	{`  (\(\`, ` ( -ω-)`, `o_(")(")`},
}

// RenderBunny returns the 3-line ASCII bunny art for the given blink phase.
// The bottom line is the feet; place it directly above the top border of a box
// so the bunny appears to sit on the box's top-left corner.
func RenderBunny(phase int) string {
	f := bunnyFrames[phase%2]
	return f[0] + "\n" + f[1] + "\n" + f[2]
}

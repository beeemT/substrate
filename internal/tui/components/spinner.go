package components

import (
	"time"

	"github.com/charmbracelet/bubbles/spinner"

	"github.com/beeemT/substrate/internal/tui/styles"
)

// NewSpinner returns the shared activity spinner used across the TUI.
func NewSpinner(st styles.Styles) spinner.Model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = st.Active
	return sp
}

// SpinnerFrame returns the shared activity spinner frame for a zero-based index.
func SpinnerFrame(frame int) string {
	frames := spinner.Dot.Frames
	if len(frames) == 0 {
		return ""
	}
	idx := frame % len(frames)
	if idx < 0 {
		idx += len(frames)
	}
	return frames[idx]
}

// SpinnerFrames returns a copy of the shared activity spinner frames.
func SpinnerFrames() []string {
	frames := spinner.Dot.Frames
	out := make([]string, len(frames))
	copy(out, frames)
	return out
}

// SpinnerFrameInterval returns the shared spinner cadence.
func SpinnerFrameInterval() time.Duration {
	return spinner.Dot.FPS
}

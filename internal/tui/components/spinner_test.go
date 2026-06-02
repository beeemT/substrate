package components

import (
	"testing"

	"github.com/beeemT/substrate/internal/tui/styles"
)

func TestSpinnerFrameUsesSharedSpinnerFrames(t *testing.T) {
	frames := SpinnerFrames()
	if len(frames) == 0 {
		t.Fatal("shared spinner must expose at least one frame")
	}
	if got := SpinnerFrame(0); got != frames[0] {
		t.Fatalf("frame 0 = %q, want %q", got, frames[0])
	}
	if got := SpinnerFrame(len(frames)); got != frames[0] {
		t.Fatalf("wrapped frame = %q, want %q", got, frames[0])
	}
	if got := SpinnerFrame(-1); got != frames[len(frames)-1] {
		t.Fatalf("negative frame = %q, want %q", got, frames[len(frames)-1])
	}
}

func TestSpinnerFramesAreClockwiseWithTwoEmptyDotSlots(t *testing.T) {
	want := []string{"⣼ ", "⣹ ", "⢻ ", "⠿ ", "⡟ ", "⣏ ", "⣧ ", "⣶ "}
	frames := SpinnerFrames()
	if len(frames) != len(want) {
		t.Fatalf("frame count = %d, want %d", len(frames), len(want))
	}
	for i, wantFrame := range want {
		if frames[i] != wantFrame {
			t.Fatalf("frame %d = %q, want %q", i, frames[i], wantFrame)
		}
	}
}

func TestNewSpinnerUsesSharedSpinnerDefinition(t *testing.T) {
	model := NewSpinner(styles.NewStyles(styles.DefaultTheme))
	if len(model.Spinner.Frames) == 0 {
		t.Fatal("spinner model has no frames")
	}
	if got := model.Spinner.Frames[0]; got != SpinnerFrame(0) {
		t.Fatalf("model first frame = %q, want %q", got, SpinnerFrame(0))
	}
	if got := model.Spinner.FPS; got != SpinnerFrameInterval() {
		t.Fatalf("model FPS = %s, want %s", got, SpinnerFrameInterval())
	}
	if SpinnerFrameInterval() <= 0 {
		t.Fatalf("spinner interval = %s, want positive", SpinnerFrameInterval())
	}
}

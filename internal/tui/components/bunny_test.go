package components_test

import (
	"strings"
	"testing"

	"github.com/beeemT/substrate/internal/tui/components"
)

// sideNames maps BunnySide values to human-readable labels for sub-test names.
var sideNames = map[components.BunnySide]string{
	components.BunnySideLeft:  "left",
	components.BunnySideRight: "right",
}

func TestRenderBunnyOpenEyes(t *testing.T) {
	for side, name := range sideNames {
		t.Run(name, func(t *testing.T) {
			s := components.RenderBunny(0, side)
			if !strings.Contains(s, "^ω^") {
				t.Fatalf("phase 0: expected open eyes (^ω^), got: %q", s)
			}
		})
	}
}

func TestRenderBunnyClosedEyes(t *testing.T) {
	for side, name := range sideNames {
		t.Run(name, func(t *testing.T) {
			s := components.RenderBunny(1, side)
			if strings.Contains(s, "^ω^") {
				t.Fatalf("phase 1: should not have open eyes, got: %q", s)
			}
			if !strings.Contains(s, "-ω-") {
				t.Fatalf("phase 1: expected closed eyes (-ω-), got: %q", s)
			}
		})
	}
}

func TestRenderBunnyAlwaysThreeLines(t *testing.T) {
	for side, sname := range sideNames {
		for phase := 0; phase <= 1; phase++ {
			s := components.RenderBunny(phase, side)
			lines := strings.Split(s, "\n")
			if len(lines) != 3 {
				t.Errorf("side=%s phase=%d: expected 3 lines, got %d: %q", sname, phase, len(lines), s)
			}
		}
	}
}

func TestRenderBunnyPhaseWraps(t *testing.T) {
	for side, name := range sideNames {
		t.Run(name, func(t *testing.T) {
			if components.RenderBunny(2, side) != components.RenderBunny(0, side) {
				t.Fatal("phase 2 should produce same output as phase 0")
			}
		})
	}
}

func TestRenderBunnyLeftEarsAndFeet(t *testing.T) {
	for phase := 0; phase <= 1; phase++ {
		s := components.RenderBunny(phase, components.BunnySideLeft)
		if !strings.Contains(s, `(\(\`) {
			t.Errorf("left phase %d: missing ears (\\(\\, got: %q", phase, s)
		}
		if !strings.Contains(s, `o_(")(")`) {
			t.Errorf("left phase %d: missing feet o_(\")(\"): got: %q", phase, s)
		}
	}
}

func TestRenderBunnyRightEarsAndFeet(t *testing.T) {
	for phase := 0; phase <= 1; phase++ {
		s := components.RenderBunny(phase, components.BunnySideRight)
		if !strings.Contains(s, `/)/)`) {
			t.Errorf("right phase %d: missing ears /)/)): got: %q", phase, s)
		}
		if !strings.Contains(s, `(")(")_o`) {
			t.Errorf("right phase %d: missing feet (\")(\"_o: got: %q", phase, s)
		}
	}
}

func TestRenderBunnySidesAreMirrored(t *testing.T) {
	// Structural mirror check: left feet start with 'o', right feet end with 'o'.
	leftFeet := strings.Split(components.RenderBunny(0, components.BunnySideLeft), "\n")[2]
	rightFeet := strings.Split(components.RenderBunny(0, components.BunnySideRight), "\n")[2]
	if leftFeet[0] != 'o' {
		t.Errorf("left feet should start with 'o', got %q", leftFeet)
	}
	if rightFeet[len(rightFeet)-1] != 'o' {
		t.Errorf("right feet should end with 'o', got %q", rightFeet)
	}
}

// --- Crouch frame tests ---

func TestRenderBunnyCrouchAlwaysThreeLines(t *testing.T) {
	for phase := 0; phase <= 1; phase++ {
		s := components.RenderBunnyCrouch(phase)
		lines := strings.Split(s, "\n")
		if len(lines) != 3 {
			t.Errorf("phase %d: expected 3 lines, got %d: %q", phase, len(lines), s)
		}
	}
}

func TestRenderBunnyCrouchPhaseWraps(t *testing.T) {
	if components.RenderBunnyCrouch(2) != components.RenderBunnyCrouch(0) {
		t.Fatal("crouch phase 2 should produce same output as phase 0")
	}
}

func TestRenderBunnyCrouchHasEars(t *testing.T) {
	s := components.RenderBunnyCrouch(0)
	if !strings.Contains(s, `(\(\`) {
		t.Fatalf("crouch frame missing ears: %q", s)
	}
}

func TestRenderBunnyCrouchFeetAreSpread(t *testing.T) {
	// Crouch feet should be wider than hop feet — spread with paw indicators.
	for phase := 0; phase <= 1; phase++ {
		feet := strings.Split(components.RenderBunnyCrouch(phase), "\n")[2]
		if len(feet) == 0 {
			t.Fatalf("phase %d: empty feet line", phase)
		}
		// Crouch feet should be wider than normal with paw indicators on both sides.
		if !strings.Contains(feet, "o") {
			t.Errorf("phase %d: crouch feet should have paw indicators (o): %q", phase, feet)
		}
	}
}

func TestRenderBunnyCrouchEyesMatchPhase(t *testing.T) {
	if !strings.Contains(components.RenderBunnyCrouch(0), "^ω^") {
		t.Fatal("crouch phase 0: expected open eyes ^ω^")
	}
	if !strings.Contains(components.RenderBunnyCrouch(1), "-ω-") {
		t.Fatal("crouch phase 1: expected closed eyes -ω-")
	}
}

// --- Hop frame helper tests ---

func TestHopFrameGap(t *testing.T) {
	tests := []struct{ frame, want int }{
		{0, 0}, {1, 1}, {2, 2}, {3, 1}, {4, 0},
	}
	for _, tc := range tests {
		if got := components.HopFrameGap(tc.frame); got != tc.want {
			t.Errorf("HopFrameGap(%d) = %d, want %d", tc.frame, got, tc.want)
		}
	}
}

func TestHopFrameProgress(t *testing.T) {
	tests := []struct {
		frame int
		want  float64
	}{
		{0, 0.0}, {1, 0.25}, {2, 0.55}, {3, 0.82}, {4, 1.0},
	}
	for _, tc := range tests {
		if got := components.HopFrameProgress(tc.frame); got != tc.want {
			t.Errorf("HopFrameProgress(%d) = %v, want %v", tc.frame, got, tc.want)
		}
	}
}

func TestHopFrameDuration(t *testing.T) {
	// Just verify they return positive durations without panicking.
	for frame := 0; frame < components.FramesPerHop; frame++ {
		d := components.HopFrameDuration(frame)
		if d <= 0 {
			t.Errorf("HopFrameDuration(%d) = %v, want positive", frame, d)
		}
	}
}

// --- Hop frame tests ---

func TestRenderBunnyHopAlwaysThreeLines(t *testing.T) {
	for phase := 0; phase <= 1; phase++ {
		s := components.RenderBunnyHop(phase)
		lines := strings.Split(s, "\n")
		if len(lines) != 3 {
			t.Errorf("phase %d: expected 3 lines, got %d: %q", phase, len(lines), s)
		}
	}
}

func TestRenderBunnyHopPhaseWraps(t *testing.T) {
	if components.RenderBunnyHop(2) != components.RenderBunnyHop(0) {
		t.Fatal("hop phase 2 should produce same output as phase 0")
	}
}

func TestRenderBunnyHopHasEars(t *testing.T) {
	s := components.RenderBunnyHop(0)
	if !strings.Contains(s, `(\(\`) {
		t.Fatalf("hop frame missing ears: %q", s)
	}
}

func TestRenderBunnyHopFeetAreAirborne(t *testing.T) {
	// Hop feet must not start with 'o' (left-side ground contact indicator) or
	// end with 'o' (right-side ground contact indicator); the bunny is in the air.
	for phase := 0; phase <= 1; phase++ {
		feet := strings.Split(components.RenderBunnyHop(phase), "\n")[2]
		if len(feet) == 0 {
			t.Fatalf("phase %d: empty feet line", phase)
		}
		if feet[0] == 'o' {
			t.Errorf("phase %d: hop feet must not start with 'o' (would indicate ground contact): %q", phase, feet)
		}
		if feet[len(feet)-1] == 'o' {
			t.Errorf("phase %d: hop feet must not end with 'o' (would indicate ground contact): %q", phase, feet)
		}
	}
}

func TestRenderBunnyHopEyesMatchPhase(t *testing.T) {
	if !strings.Contains(components.RenderBunnyHop(0), "^ω^") {
		t.Fatal("hop phase 0: expected open eyes ^ω^")
	}
	if !strings.Contains(components.RenderBunnyHop(1), "-ω-") {
		t.Fatal("hop phase 1: expected closed eyes -ω-")
	}
}

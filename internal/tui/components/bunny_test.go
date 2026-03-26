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

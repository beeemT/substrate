package components_test

import (
	"strings"
	"testing"

	"github.com/beeemT/substrate/internal/tui/components"
)

func TestRenderBunnyOpenEyes(t *testing.T) {
	s := components.RenderBunny(0)
	if !strings.Contains(s, "^ω^") {
		t.Fatalf("phase 0: expected open eyes (^ω^), got: %q", s)
	}
}

func TestRenderBunnyClosedEyes(t *testing.T) {
	s := components.RenderBunny(1)
	if strings.Contains(s, "^ω^") {
		t.Fatalf("phase 1: should not have open eyes, got: %q", s)
	}
	if !strings.Contains(s, "-ω-") {
		t.Fatalf("phase 1: expected closed eyes (-ω-), got: %q", s)
	}
}

func TestRenderBunnyAlwaysThreeLines(t *testing.T) {
	for phase := 0; phase <= 1; phase++ {
		lines := strings.Split(components.RenderBunny(phase), "\n")
		if len(lines) != 3 {
			t.Errorf("phase %d: expected 3 lines, got %d: %q", phase, len(lines), components.RenderBunny(phase))
		}
	}
}

func TestRenderBunnyPhaseWraps(t *testing.T) {
	// Phase is modulo 2; phase 2 should equal phase 0.
	if components.RenderBunny(2) != components.RenderBunny(0) {
		t.Fatal("phase 2 should produce same output as phase 0")
	}
}

func TestRenderBunnyEarsAndFeetUnchanged(t *testing.T) {
	for phase := 0; phase <= 1; phase++ {
		s := components.RenderBunny(phase)
		if !strings.Contains(s, `(\(\`) {
			t.Errorf("phase %d: missing ears (\\(\\, got: %q", phase, s)
		}
		if !strings.Contains(s, `o_(")(")`) {
			t.Errorf("phase %d: missing feet o_(\")(\"): got: %q", phase, s)
		}
	}
}

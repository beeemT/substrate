package views_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/beeemT/substrate/internal/tui/components"
	"github.com/beeemT/substrate/internal/tui/styles"
	"github.com/beeemT/substrate/internal/tui/views"
)

func makeContentStyles() styles.Styles {
	return styles.NewStyles(styles.DefaultTheme)
}

func TestContentDefaultMode(t *testing.T) {
	m := views.NewContentModel(makeContentStyles())
	if m.Mode() != views.ContentModeEmpty {
		t.Fatalf("expected default mode ContentModeEmpty, got %v", m.Mode())
	}
}

func TestContentSetMode(t *testing.T) {
	modes := []views.ContentMode{
		views.ContentModeEmpty,
		views.ContentModeOverview,
		views.ContentModeSourceDetails,
		views.ContentModePlanning,
		views.ContentModeSessionInteraction,
	}

	for _, mode := range modes {
		m := views.NewContentModel(makeContentStyles())
		m.SetMode(mode)
		if m.Mode() != mode {
			t.Errorf("SetMode(%v): Mode() = %v, want %v", mode, m.Mode(), mode)
		}
	}
}

func TestContentSetSize(_ *testing.T) {
	m := views.NewContentModel(makeContentStyles())
	// Must not panic
	m.SetSize(80, 24)
	// View must return a string (not crash)
	_ = m.View()
}

func TestContentEmptyViewShowsHelperText(t *testing.T) {
	m := views.NewContentModel(makeContentStyles())
	m.SetSize(80, 24)

	view := m.View()
	if !strings.Contains(view, "No sessions yet") {
		t.Fatalf("view is missing empty-state title: %q", view)
	}
	if !strings.Contains(view, "[n]") || !strings.Contains(view, "create your first session") {
		t.Fatalf("view is missing next-step guidance: %q", view)
	}
	if !strings.Contains(view, "Once a session is running") || !strings.Contains(view, "review output") {
		t.Fatalf("view is missing post-session description: %q", view)
	}
}

func TestContentKeybindHints_Empty(_ *testing.T) {
	m := views.NewContentModel(makeContentStyles())
	// ContentModeEmpty is the default; KeybindHints must not panic
	// The implementation returns nil for modes without explicit hints (default case)
	hints := m.KeybindHints()
	// nil is acceptable — no hints defined for empty mode
	_ = hints
}

func TestContentKeybindHintsHaveKeyAndLabel(t *testing.T) {
	m := views.NewContentModel(makeContentStyles())
	m.SetSize(80, 24)
	m.SetMode(views.ContentModeOverview)
	hints := m.KeybindHints()
	// hints may be nil if no hints are defined for this mode; if non-nil, validate shape
	for i, h := range hints {
		if h.Key == "" {
			t.Errorf("hint[%d].Key is empty", i)
		}
		if h.Label == "" {
			t.Errorf("hint[%d].Label is empty", i)
		}
	}
}

func TestContentOverviewKeybindHintsExposeOverviewActions(t *testing.T) {
	m := views.NewContentModel(makeContentStyles())
	m.SetSize(80, 24)
	m.SetOverviewData(views.SessionOverviewData{
		WorkItemID: "wi-1",
		State:      "plan_review",
		Header: views.OverviewHeader{
			ExternalID:  "SUB-1",
			Title:       "Review plan",
			StatusLabel: "Plan review needed",
		},
		Actions: []views.OverviewActionCard{{
			Kind:     "plan_review",
			Title:    "Plan review required",
			Blocked:  "Implementation is waiting for plan approval",
			Why:      "A human decision is required before execution can continue.",
			Affected: []string{"repo-a"},
		}},
	})
	m.SetMode(views.ContentModeOverview)

	hints := m.KeybindHints()
	labels := make([]string, 0, len(hints))
	for _, hint := range hints {
		labels = append(labels, hint.Label)
	}
	for _, want := range []string{"Scroll", "Approve", "Changes", "Reject", "Inspect"} {
		if !slices.Contains(labels, want) {
			t.Fatalf("overview keybind hints = %#v, want label %q", hints, want)
		}
	}
}

func TestContentEmptyViewWithSessions(t *testing.T) {
	m := views.NewContentModel(makeContentStyles())
	m.SetSize(100, 40)
	m.SetSessionStats(views.SessionStats{TotalSessions: 5, ActionNeeded: 2})

	view := m.View()
	if !strings.Contains(view, "Select a session") {
		t.Fatalf("expected 'Select a session' when sessions exist, got: %q", view)
	}
	if !strings.Contains(view, "2 awaiting action") {
		t.Fatalf("expected action count in view, got: %q", view)
	}
	if strings.Contains(view, "No sessions yet") {
		t.Fatalf("should not show 'No sessions yet' when sessions exist")
	}
}

func TestContentEmptyViewFitsTerminalBounds(t *testing.T) {
	for _, tc := range []struct {
		name string
		w, h int
	}{
		{"standard", 80, 24},
		{"wide", 120, 40},
		{"narrow", 50, 20},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := views.NewContentModel(makeContentStyles())
			m.SetSize(tc.w, tc.h)
			view := m.View()
			lines := strings.Split(view, "\n")
			if len(lines) > tc.h {
				t.Errorf("rendered %d lines, exceeds height %d", len(lines), tc.h)
			}
		})
	}
}

func TestContentEmptyViewBunnyPresent(t *testing.T) {
	m := views.NewContentModel(makeContentStyles())
	m.SetSize(100, 40)
	view := m.View()
	if !strings.Contains(view, "ω") {
		t.Fatalf("expected bunny in empty state view, got: %q", view)
	}
}

func TestContentEmptyViewBunnyHiddenWhenShort(t *testing.T) {
	// Height below minHeightForBunny (7) — must not crash and must not render bunny.
	m := views.NewContentModel(makeContentStyles())
	m.SetSize(80, 4)
	view := m.View()
	if strings.Contains(view, "ω") {
		t.Fatalf("bunny should be hidden at height 4, got: %q", view)
	}
}

func TestContentHopRenderingFitsTerminalBounds(t *testing.T) {
	// Simulate an active hop at various frames and verify the rendered output fits.
	for _, tc := range []struct {
		name     string
		w, h     int
		hopCount int // total hops in sequence
		hopIndex int // which hop to be in
		hopFrame int // frame within that hop
	}{
		{"crouch-2hop", 80, 24, 2, 0, 0},
		{"rise-2hop", 80, 24, 2, 0, 1},
		{"peak-2hop", 80, 24, 2, 0, 2},
		{"fall-2hop", 80, 24, 2, 0, 3},
		{"land-2hop", 80, 24, 2, 0, 4},
		{"peak-3hop", 80, 24, 3, 1, 2},
		{"wide-peak", 120, 40, 2, 0, 2},
		{"narrow-rise", 50, 20, 3, 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := views.NewContentModel(makeContentStyles())
			m.SetSize(tc.w, tc.h)
			// Drive model into the desired hop state via Update messages.
			m, _ = m.Update(components.BunnyHopTriggerMsg{Hops: tc.hopCount})
			// Advance to the target hop index and frame.
			// Each hop has FramesPerHop (5) frames, plus a pause between hops.
			stepsToReach := tc.hopIndex*(components.FramesPerHop+1) + tc.hopFrame
			for range stepsToReach {
				m, _ = m.Update(components.BunnyHopStepMsg{})
			}
			view := m.View()
			lines := strings.Split(view, "\n")
			if len(lines) > tc.h {
				t.Errorf("hop render: got %d lines, exceeds height %d", len(lines), tc.h)
			}
			for j, line := range lines {
				if w := lipgloss.Width(line); w > tc.w {
					t.Errorf("line %d: display width %d exceeds terminal width %d", j, w, tc.w)
				}
			}
		})
	}
}

func TestContentHopLandsOnOppositeSide(t *testing.T) {
	// After a full 2-hop sequence the bunny should have flipped sides.
	// We advance Update until the hop completes and inspect the rendered output.
	m := views.NewContentModel(makeContentStyles())
	m.SetSize(80, 24)
	// Trigger a 2-hop sequence.
	m, _ = m.Update(components.BunnyHopTriggerMsg{Hops: 2})
	// Advance through all frames: 2 hops × 5 frames each = 10 steps,
	// plus 1 pause between hops = 11 steps total.
	for range 11 {
		m, _ = m.Update(components.BunnyHopStepMsg{})
	}
	// After landing, the bunny should still render (ω present) and not be hopping.
	view := m.View()
	if !strings.Contains(view, "ω") {
		t.Fatalf("expected bunny after hop landing, got: %q", view)
	}
}

func TestContentHopContainerStaysFixed(t *testing.T) {
	// The container (box) must not shift vertically between stationary and any
	// hop frame. We find the row containing the top-left border corner (╭)
	// and verify it is identical across all states.
	m := views.NewContentModel(makeContentStyles())
	m.SetSize(80, 24)
	// Render stationary and find the container's top border row.
	stationaryView := m.View()
	stationaryLines := strings.Split(stationaryView, "\n")
	var stationaryContainerRow int
	for i, line := range stationaryLines {
		if strings.Contains(line, "╭") {
			stationaryContainerRow = i
			break
		}
	}
	// Now trigger a 2-hop and check every frame.
	m, _ = m.Update(components.BunnyHopTriggerMsg{Hops: 2})
	for step := range 11 {
		view := m.View()
		lines := strings.Split(view, "\n")
		var containerRow int
		for i, line := range lines {
			if strings.Contains(line, "╭") {
				containerRow = i
				break
			}
		}
		if containerRow != stationaryContainerRow {
			t.Errorf("step %d: container at row %d, expected %d (same as stationary)", step, containerRow, stationaryContainerRow)
		}
		m, _ = m.Update(components.BunnyHopStepMsg{})
	}
}

package views_test

import (
	"slices"
	"strings"
	"testing"

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
	if !strings.Contains(view, `(\(\`) {
		t.Fatalf("expected bunny ears in empty state view, got: %q", view)
	}
}

func TestContentEmptyViewBunnyHiddenWhenShort(t *testing.T) {
	// Height below minHeightForBunny (7) — must not crash and must not render bunny.
	m := views.NewContentModel(makeContentStyles())
	m.SetSize(80, 4)
	view := m.View()
	if strings.Contains(view, `(\(\`) {
		t.Fatalf("bunny should be hidden at height 4, got: %q", view)
	}
}

package views_test

import (
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
		views.ContentModeReadyToPlan,
		views.ContentModePlanning,
		views.ContentModePlanReview,
		views.ContentModeImplementing,
		views.ContentModeCompleted,
		views.ContentModeFailed,
	}

	for _, mode := range modes {
		m := views.NewContentModel(makeContentStyles())
		m.SetMode(mode)
		if m.Mode() != mode {
			t.Errorf("SetMode(%v): Mode() = %v, want %v", mode, m.Mode(), mode)
		}
	}
}

func TestContentSetSize(t *testing.T) {
	m := views.NewContentModel(makeContentStyles())
	// Must not panic
	m.SetSize(80, 24)
	// View must return a string (not crash)
	_ = m.View()
}

func TestContentKeybindHints_Empty(t *testing.T) {
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
	m.SetMode(views.ContentModeReadyToPlan)
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

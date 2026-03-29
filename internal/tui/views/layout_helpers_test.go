package views

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/beeemT/substrate/internal/tui/styles"
)

func TestOverlaySpinnerPlacedAtBottomRight(t *testing.T) {
	t.Parallel()

	st := styles.NewStyles(styles.DefaultTheme)
	body := "line 1\nline 2\nline 3"
	result := overlaySpinner(body, "⠋", st, 40)
	lines := strings.Split(result, "\n")

	if len(lines) != 3 {
		t.Fatalf("line count = %d, want 3", len(lines))
	}
	// Spinner should appear on the last line.
	if !strings.Contains(lines[2], "⠋") {
		t.Fatalf("last line = %q, want spinner frame", lines[2])
	}
	// Preceding lines should not contain spinner.
	for i, line := range lines[:2] {
		if strings.Contains(line, "⠋") {
			t.Errorf("line %d = %q, spinner should only be on last line", i, line)
		}
	}
}

func TestOverlaySpinnerRespectsWidth(t *testing.T) {
	t.Parallel()

	st := styles.NewStyles(styles.DefaultTheme)
	body := "short\nmedium line\nthis is a longer line of text"
	result := overlaySpinner(body, "⠙", st, 40)
	lines := strings.Split(result, "\n")

	for i, line := range lines {
		if got := ansi.StringWidth(line); got > 40 {
			t.Errorf("line %d width = %d, want <= 40: %q", i, got, line)
		}
	}
}

func TestOverlaySpinnerEmptyBody(t *testing.T) {
	t.Parallel()

	st := styles.NewStyles(styles.DefaultTheme)
	result := overlaySpinner("", "⠋", st, 40)
	// Should not panic or produce garbage.
	if result == "" {
		t.Fatal("overlaySpinner on empty body should not return empty")
	}
}

func TestOverlaySpinnerZeroWidth(t *testing.T) {
	t.Parallel()

	st := styles.NewStyles(styles.DefaultTheme)
	body := "some content"
	result := overlaySpinner(body, "⠋", st, 0)
	if result != body {
		t.Fatalf("overlaySpinner with zero width should return body unchanged, got %q", result)
	}
}

func TestOverlaySpinnerPlacedOnLastLine(t *testing.T) {
	t.Parallel()

	st := styles.NewStyles(styles.DefaultTheme)
	// Body has content followed by trailing blank lines (simulates viewport
	// padding when content doesn't fill the viewport height).
	body := "content line\n\n\n"
	result := overlaySpinner(body, "⠋", st, 40)
	lines := strings.Split(result, "\n")

	// Spinner must be on the very last line (bottom-right corner of the
	// viewport window), not on the last non-empty content line.
	lastIdx := len(lines) - 1
	if !strings.Contains(lines[lastIdx], "⠋") {
		t.Fatalf("spinner must be on last line (index %d), got lines: %v", lastIdx, lines)
	}
	for i := 0; i < lastIdx; i++ {
		if strings.Contains(lines[i], "⠋") {
			t.Errorf("line %d = %q, spinner should only be on the last line", i, lines[i])
		}
	}
}

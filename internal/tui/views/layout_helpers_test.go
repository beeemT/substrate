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

func TestOverlaySpinnerSkipsTrailingBlankLines(t *testing.T) {
	t.Parallel()

	st := styles.NewStyles(styles.DefaultTheme)
	body := "content line\n\n\n"
	result := overlaySpinner(body, "⠋", st, 40)
	lines := strings.Split(result, "\n")

	// Spinner should be on the first line (only non-empty one), not on trailing blanks.
	if !strings.Contains(lines[0], "⠋") {
		t.Fatalf("spinner should be on first non-empty line, lines: %v", lines)
	}
	for i := 1; i < len(lines); i++ {
		if strings.Contains(lines[i], "⠋") {
			t.Errorf("line %d = %q, spinner should not be on trailing blank line", i, lines[i])
		}
	}
}

package views_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/beeemT/substrate/internal/tui/styles"
	"github.com/beeemT/substrate/internal/tui/views"
)

var statusBarBackgroundPattern = regexp.MustCompile(`\x1b\[[0-9;]*48;`)

func makeStatusBarStyles() styles.Styles {
	return styles.NewStyles(styles.DefaultTheme)
}

func TestStatusBarHasNoBorderOrSeparator(t *testing.T) {
	t.Parallel()

	m := views.NewStatusBarModel(makeStatusBarStyles())
	rendered := m.View(views.DefaultHints(), "0 sessions", 40)
	plain := stripANSI(rendered)
	lines := strings.Split(plain, "\n")

	if len(lines) != 1 {
		t.Fatalf("status bar line count = %d, want 1; plain=%q", len(lines), plain)
	}
	if got := len([]rune(lines[0])); got != 40 {
		t.Fatalf("content width = %d, want 40; line=%q", got, lines[0])
	}
	if strings.Contains(lines[0], "─") {
		t.Fatalf("status bar should not render a separator border: %q", lines[0])
	}
	if !strings.Contains(lines[0], "0 sessions") {
		t.Fatalf("content = %q, want right-aligned session count", lines[0])
	}
}

func TestStatusBarHasNoBackgroundColor(t *testing.T) {
	t.Parallel()

	m := views.NewStatusBarModel(makeStatusBarStyles())
	rendered := m.View(views.DefaultHints(), "0 sessions", 40)
	if statusBarBackgroundPattern.MatchString(rendered) {
		t.Fatalf("rendered = %q, want no background color escape sequences", rendered)
	}
}

func TestStatusBarDropsHintsBeforeWrappingRightText(t *testing.T) {
	t.Parallel()

	m := views.NewStatusBarModel(makeStatusBarStyles())
	rendered := m.View(views.DefaultHints(), "0 sessions", 12)
	plain := stripANSI(rendered)
	lines := strings.Split(plain, "\n")

	if len(lines) != 1 {
		t.Fatalf("status bar line count = %d, want 1; plain=%q", len(lines), plain)
	}
	if got := len([]rune(lines[0])); got != 12 {
		t.Fatalf("content width = %d, want 12; line=%q", got, lines[0])
	}
	if !strings.Contains(lines[0], "0 sessions") {
		t.Fatalf("content = %q, want session count preserved", lines[0])
	}
	if strings.Contains(lines[0], "New session") {
		t.Fatalf("content = %q, want keybind hints dropped before the session count wraps", lines[0])
	}
}

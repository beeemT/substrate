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

func TestStatusBarPreservesLeadingContextualHintWhenSpaceIsTight(t *testing.T) {
	t.Parallel()

	m := views.NewStatusBarModel(makeStatusBarStyles())
	hints := []views.KeybindHint{{Key: "d", Label: "Delete session"}, {Key: "↑/↓", Label: "Tasks"}, {Key: "→", Label: "Content"}}
	rendered := m.View(hints, "workspace-with-a-very-long-name · 1 active sessions", 32)
	plain := stripANSI(rendered)
	lines := strings.Split(plain, "\n")

	if len(lines) != 1 {
		t.Fatalf("status bar line count = %d, want 1; plain=%q", len(lines), plain)
	}
	if got := len([]rune(lines[0])); got != 32 {
		t.Fatalf("content width = %d, want 32; line=%q", got, lines[0])
	}
	if !strings.Contains(lines[0], "Delete session") {
		t.Fatalf("content = %q, want leading contextual delete hint preserved", lines[0])
	}
	if strings.Contains(lines[0], "Tasks") {
		t.Fatalf("content = %q, want lower-priority hints dropped first", lines[0])
	}
}

func TestStatusBarKeepsDeleteKeyVisibleWhenHintTextMustTruncate(t *testing.T) {
	t.Parallel()

	m := views.NewStatusBarModel(makeStatusBarStyles())
	hints := []views.KeybindHint{{Key: "d", Label: "Delete session"}, {Key: "↑/↓", Label: "Tasks"}, {Key: "→", Label: "Content"}}
	rendered := m.View(hints, "workspace-with-a-very-long-name · 1 active sessions", 12)
	plain := stripANSI(rendered)
	lines := strings.Split(plain, "\n")

	if len(lines) != 1 {
		t.Fatalf("status bar line count = %d, want 1; plain=%q", len(lines), plain)
	}
	if got := len([]rune(lines[0])); got != 12 {
		t.Fatalf("content width = %d, want 12; line=%q", got, lines[0])
	}
	if !strings.Contains(lines[0], "[d]") {
		t.Fatalf("content = %q, want delete key preserved even when the label truncates", lines[0])
	}
	if strings.Contains(lines[0], "active sessions") {
		t.Fatalf("content = %q, want right-side metadata dropped before the delete action", lines[0])
	}
}

func TestStatusBarPlacesDeleteHintBetweenNewAndSearch(t *testing.T) {
	t.Parallel()

	m := views.NewStatusBarModel(makeStatusBarStyles())
	hints := []views.KeybindHint{
		{Key: "d", Label: "Delete session"},
		{Key: "↑/↓", Label: "Tasks"},
		{Key: "→", Label: "Content"},
		{Key: "n", Label: "New session"},
		{Key: "/", Label: "Search sessions"},
	}
	rendered := m.View(hints, "workspace · 1 active sessions", 120)
	plain := stripANSI(rendered)
	lines := strings.Split(plain, "\n")

	if len(lines) != 1 {
		t.Fatalf("status bar line count = %d, want 1; plain=%q", len(lines), plain)
	}
	if got := len([]rune(lines[0])); got != 120 {
		t.Fatalf("content width = %d, want 120; line=%q", got, lines[0])
	}

	newIndex := strings.Index(lines[0], "New session")
	deleteIndex := strings.Index(lines[0], "Delete session")
	searchIndex := strings.Index(lines[0], "Search sessions")
	if newIndex == -1 || deleteIndex == -1 || searchIndex == -1 {
		t.Fatalf("content = %q, want new, delete, and search hints visible", lines[0])
	}
	if !(newIndex < deleteIndex && deleteIndex < searchIndex) {
		t.Fatalf("content = %q, want delete hint between new and search", lines[0])
	}
}

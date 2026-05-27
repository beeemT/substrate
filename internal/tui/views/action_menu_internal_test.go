package views

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/beeemT/substrate/internal/tui/styles"
)

func TestActionMenuViewRendersActionRowsWithSharedChrome(t *testing.T) {
	t.Parallel()

	st := styles.NewStyles(styles.DefaultTheme)
	model := NewActionMenuModel(st)
	model.SetSize(48, 14)
	model.actions = []Action{
		{ID: "open", Label: "Open selected session with a very long label", Shortcut: "Enter"},
		{ID: "close", Label: "Close", Shortcut: "Esc"},
	}
	model.matches = []int{0, 1}
	model.query = " " // non-empty to trigger label truncation, not search placeholder ellipsis

	view := model.View()
	lines := strings.Split(view, "\n")
	if len(lines) > 14 {
		t.Fatalf("view has %d lines, want <= 14", len(lines))
	}
	for i, line := range lines {
		if got := ansi.StringWidth(line); got > 48 {
			t.Fatalf("line %d width = %d, want <= 48: %q", i+1, got, line)
		}
	}

	plain := ansi.Strip(view)
	for _, want := range []string{"╭", "Actions", "Search:", "›", "[Enter]", "[Esc]"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("view missing %q\n%s", want, plain)
		}
	}
	if !strings.Contains(plain, "…") {
		t.Fatalf("view should truncate the long action label with an ellipsis\n%s", plain)
	}
}

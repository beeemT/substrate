package views

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/beeemT/substrate/internal/tui/components"
	"github.com/beeemT/substrate/internal/tui/styles"
)

func componentHints(hints []KeybindHint) []components.KeyHint {
	converted := make([]components.KeyHint, 0, len(hints))
	for _, hint := range hints {
		converted = append(converted, components.KeyHint{Key: hint.Key, Label: hint.Label})
	}
	return converted
}

// renderOverlayHintsRow renders a keybind hints row sized to width, padded with
// one space on each side and truncated to fit.
func renderOverlayHintsRow(st styles.Styles, hints []KeybindHint, width int) string {
	if width <= 2 {
		return ""
	}
	parts := make([]string, 0, len(hints))
	for _, h := range hints {
		key := "[" + h.Key + "]"
		label := " " + h.Label
		parts = append(parts, st.KeybindAccent.Render(key)+st.Hint.Render(label))
	}
	raw := strings.Join(parts, "  ")

	contentWidth := width - 2
	return lipgloss.NewStyle().
		Width(width).
		Padding(0, 1).
		Render(ansi.Truncate(raw, contentWidth, ""))
}


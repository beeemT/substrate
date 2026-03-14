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

// renderOverlayHintsRow renders keybind hints with the overlay background applied
// to each segment so inline style resets don't bleed through the outer frame's
// background. Padding(0,1) ensures at least one space between the coloured
// background edge and the first/last visible text character on either side.
func renderOverlayHintsRow(st styles.Styles, hints []KeybindHint, width int) string {
	if width <= 2 {
		return ""
	}
	bg := lipgloss.Color(st.Theme.OverlayBg)
	accentStyle := st.KeybindAccent.Copy().Background(bg)
	hintStyle := st.Hint.Copy().Background(bg)
	sep := lipgloss.NewStyle().Background(bg).Render("  ")

	parts := make([]string, 0, len(hints))
	for _, h := range hints {
		key := "[" + h.Key + "]"
		label := " " + h.Label
		parts = append(parts, accentStyle.Render(key)+hintStyle.Render(label))
	}
	raw := strings.Join(parts, sep)

	contentWidth := width - 2
	return lipgloss.NewStyle().
		Background(bg).
		Width(width).
		Padding(0, 1).
		Render(ansi.Truncate(raw, contentWidth, ""))
}

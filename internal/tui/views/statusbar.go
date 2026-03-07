package views

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/beeemT/substrate/internal/tui/styles"
)

// StatusBarModel renders the 1-line status bar at the bottom.
type StatusBarModel struct {
	styles styles.Styles
}

// NewStatusBarModel creates a StatusBarModel with the given styles.
func NewStatusBarModel(st styles.Styles) StatusBarModel {
	return StatusBarModel{styles: st}
}

// View renders the status bar with the given keybind hints, right-aligned text, and total width.
func (s StatusBarModel) View(hints []KeybindHint, rightText string, width int) string {
	var parts []string
	for _, h := range hints {
		key := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#5b8def")).
			Bold(true).
			Render("[" + h.Key + "]")
		label := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#a0a0a0")).
			Render(" " + h.Label)
		parts = append(parts, key+label)
	}
	left := strings.Join(parts, "  ")
	right := lipgloss.NewStyle().Foreground(lipgloss.Color("#6b7280")).Render(rightText)

	leftLen := lipgloss.Width(left)
	rightLen := lipgloss.Width(right)
	padLen := width - leftLen - rightLen - 2
	if padLen < 0 {
		padLen = 0
	}
	pad := strings.Repeat(" ", padLen)

	line := fmt.Sprintf(" %s%s%s", left, pad, right)
	barStyle := lipgloss.NewStyle().
		Background(lipgloss.Color("#16213e")).
		Foreground(lipgloss.Color("#a0a0a0")).
		Width(width).
		Padding(0, 1)
	return barStyle.Render(line)
}

// DefaultHints returns the global keybind hints always shown in the status bar.
func DefaultHints() []KeybindHint {
	return []KeybindHint{
		{Key: "n", Label: "New session"},
		{Key: "c", Label: "Settings"},
		{Key: "?", Label: "Help"},
		{Key: "q", Label: "Quit"},
	}
}

package components

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// RenderProgressBar renders a text progress bar of the given width.
// done is the number completed, total is the total count.
// activeFg is unused but kept for API symmetry with callers that may pass a current-item color.
func RenderProgressBar(done, total, width int, activeFg, doneFg, pendingFg string) string {
	if total <= 0 || width <= 0 {
		return ""
	}
	countStr := fmt.Sprintf(" %d/%d", done, total)
	barWidth := width - len(countStr) - 1
	if barWidth <= 0 {
		return fmt.Sprintf("%d/%d", done, total)
	}
	filled := barWidth * done / total
	if filled > barWidth {
		filled = barWidth
	}
	empty := barWidth - filled
	bar := lipgloss.NewStyle().Foreground(lipgloss.Color(doneFg)).Render(strings.Repeat("█", filled)) +
		lipgloss.NewStyle().Foreground(lipgloss.Color(pendingFg)).Render(strings.Repeat("░", empty))
	return bar + lipgloss.NewStyle().Foreground(lipgloss.Color("#6b7280")).Render(countStr)
}

package components

import (
	"fmt"
	"strings"

	"github.com/beeemT/substrate/internal/tui/styles"
)

// RenderProgressBar renders a semantic text progress bar of the given width.
func RenderProgressBar(st styles.Styles, done, total, width int) string {
	if total <= 0 || width <= 0 {
		return ""
	}
	countStr := fmt.Sprintf(" %d/%d", done, total)
	barWidth := width - len(countStr) - 1
	if barWidth <= 0 {
		return fmt.Sprintf("%d/%d", done, total)
	}
	filled := barWidth * done / total
	filled = min(filled, barWidth)
	empty := barWidth - filled
	bar := st.Active.Render(strings.Repeat("█", filled)) + st.Divider.Render(strings.Repeat("░", empty))

	return bar + st.Muted.Render(countStr)
}

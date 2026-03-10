package views

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/beeemT/substrate/internal/tui/components"
	"github.com/beeemT/substrate/internal/tui/styles"
)

// statusBarHeight is the single footer row at the bottom of the main page.
const statusBarHeight = 1

// StatusBarModel renders the footer content at the bottom.
type StatusBarModel struct {
	styles styles.Styles
}

// NewStatusBarModel creates a StatusBarModel with the given styles.
func NewStatusBarModel(st styles.Styles) StatusBarModel {
	return StatusBarModel{styles: st}
}

// View renders the keybind hints and right-aligned metadata within one footer row.
func (s StatusBarModel) View(hints []KeybindHint, rightText string, width int) string {
	if width <= 0 {
		return ""
	}

	horizontalPadding := 0
	innerWidth := width
	if width >= 2 {
		horizontalPadding = 1
		innerWidth = width - 2
	}

	parts := components.RenderKeyHintFragments(s.styles, componentHints(hints))

	rightText = truncate(rightText, innerWidth)
	right := s.styles.Muted.Render(rightText)
	rightLen := lipgloss.Width(rightText)

	requiredGap := 0
	if rightLen > 0 {
		requiredGap = 1
	}

	leftRawParts := make([]string, 0, len(parts))
	leftRenderedParts := make([]string, 0, len(parts))
	for _, part := range parts {
		leftRawParts = append(leftRawParts, part.Raw)
		leftRenderedParts = append(leftRenderedParts, part.Rendered)
	}

	leftRaw := strings.Join(leftRawParts, "  ")
	for len(leftRawParts) > 0 && lipgloss.Width(leftRaw)+rightLen+requiredGap > innerWidth {
		leftRawParts = leftRawParts[:len(leftRawParts)-1]
		leftRenderedParts = leftRenderedParts[:len(leftRenderedParts)-1]
		leftRaw = strings.Join(leftRawParts, "  ")
	}

	left := strings.Join(leftRenderedParts, "  ")
	leftLen := lipgloss.Width(leftRaw)
	gapLen := innerWidth - leftLen - rightLen
	if gapLen < 0 {
		gapLen = 0
	}

	line := left + strings.Repeat(" ", gapLen) + right
	lineStyle := s.styles.StatusBar.Copy().Padding(0, horizontalPadding)
	return lineStyle.Render(line)
}

// DefaultHints returns the global keybind hints always shown in the status bar.
func DefaultHints() []KeybindHint {
	return []KeybindHint{
		{Key: "n", Label: "New session"},
		{Key: "/", Label: "Search sessions"},
		{Key: "c", Label: "Settings"},
		{Key: "?", Label: "Help"},
		{Key: "q", Label: "Quit"},
	}
}

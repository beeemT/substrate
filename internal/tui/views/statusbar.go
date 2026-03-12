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
	preserveLeading := hasContextualLeadingHint(hints) && len(hints) > 0
	if preserveLeading && len(parts) > 0 {
		parts[0] = renderHintFragment(s.styles, hints[0], innerWidth)
	}

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
		if part.Raw == "" {
			continue
		}
		leftRawParts = append(leftRawParts, part.Raw)
		leftRenderedParts = append(leftRenderedParts, part.Rendered)
	}

	minParts := 0
	if preserveLeading && len(leftRawParts) > 0 {
		minParts = 1
	}
	leftRaw := strings.Join(leftRawParts, "  ")
	for len(leftRawParts) > minParts && lipgloss.Width(leftRaw)+rightLen+requiredGap > innerWidth {
		leftRawParts = leftRawParts[:len(leftRawParts)-1]
		leftRenderedParts = leftRenderedParts[:len(leftRenderedParts)-1]
		leftRaw = strings.Join(leftRawParts, "  ")
	}
	if preserveLeading && len(leftRawParts) == 1 {
		leftLen := lipgloss.Width(leftRaw)
		if leftLen+rightLen+requiredGap > innerWidth {
			availableRight := innerWidth - leftLen
			if rightLen > 0 {
				availableRight--
			}
			if availableRight <= 0 {
				rightText = ""
			} else {
				rightText = truncate(rightText, availableRight)
			}
			right = s.styles.Muted.Render(rightText)
			rightLen = lipgloss.Width(rightText)
			requiredGap = 0
			if rightLen > 0 {
				requiredGap = 1
			}
		}
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

func renderHintFragment(st styles.Styles, hint KeybindHint, maxWidth int) components.RenderedKeyHint {
	if maxWidth <= 0 {
		return components.RenderedKeyHint{}
	}
	keyRaw := "[" + hint.Key + "]"
	keyWidth := lipgloss.Width(keyRaw)
	if keyWidth >= maxWidth {
		keyRaw = truncate(keyRaw, maxWidth)
		return components.RenderedKeyHint{Raw: keyRaw, Rendered: st.KeybindAccent.Render(keyRaw)}
	}
	labelRaw := truncate(" "+hint.Label, maxWidth-keyWidth)
	raw := keyRaw + labelRaw
	return components.RenderedKeyHint{
		Raw:      raw,
		Rendered: st.KeybindAccent.Render(keyRaw) + st.Hint.Render(labelRaw),
	}
}

func hasContextualLeadingHint(hints []KeybindHint) bool {
	if len(hints) == 0 {
		return false
	}
	for _, hint := range DefaultHints() {
		if hint == hints[0] {
			return false
		}
	}
	return true
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

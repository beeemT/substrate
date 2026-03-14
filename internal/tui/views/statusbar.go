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

	preserveLeading := hasContextualLeadingHint(hints) && len(hints) > 0
	parts := renderStatusBarHintParts(s.styles, hints, innerWidth, preserveLeading)

	rightText = truncate(rightText, innerWidth)
	right := s.styles.Muted.Render(rightText)
	rightLen := lipgloss.Width(rightText)

	requiredGap := 0
	if rightLen > 0 {
		requiredGap = 1
	}

	minParts := 0
	if preserveLeading && len(parts) > 0 {
		minParts = 1
	}
	leftRaw := joinStatusBarHintRaw(parts)
	for len(parts) > minParts && lipgloss.Width(leftRaw)+rightLen+requiredGap > innerWidth {
		parts = parts[:len(parts)-1]
		leftRaw = joinStatusBarHintRaw(parts)
	}
	if preserveLeading && len(parts) == 1 {
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

	parts = reorderStatusBarHintParts(parts)
	leftRaw = joinStatusBarHintRaw(parts)
	left := joinStatusBarHintRendered(parts)
	leftLen := lipgloss.Width(leftRaw)
	gapLen := innerWidth - leftLen - rightLen
	if gapLen < 0 {
		gapLen = 0
	}

	line := left + strings.Repeat(" ", gapLen) + right
	lineStyle := s.styles.StatusBar.Copy().Padding(0, horizontalPadding)
	return lineStyle.Render(line)
}

type statusBarHintPart struct {
	hint KeybindHint
	part components.RenderedKeyHint
}

func renderStatusBarHintParts(st styles.Styles, hints []KeybindHint, maxWidth int, preserveLeading bool) []statusBarHintPart {
	rendered := components.RenderKeyHintFragments(st, componentHints(hints))
	if preserveLeading && len(rendered) > 0 {
		rendered[0] = renderHintFragment(st, hints[0], maxWidth)
	}

	parts := make([]statusBarHintPart, 0, len(rendered))
	for i, part := range rendered {
		if part.Raw == "" {
			continue
		}
		parts = append(parts, statusBarHintPart{hint: hints[i], part: part})
	}
	return parts
}

func joinStatusBarHintRaw(parts []statusBarHintPart) string {
	raw := make([]string, 0, len(parts))
	for _, part := range parts {
		raw = append(raw, part.part.Raw)
	}
	return strings.Join(raw, "  ")
}

func joinStatusBarHintRendered(parts []statusBarHintPart) string {
	rendered := make([]string, 0, len(parts))
	for _, part := range parts {
		rendered = append(rendered, part.part.Rendered)
	}
	return strings.Join(rendered, "  ")
}

func reorderStatusBarHintParts(parts []statusBarHintPart) []statusBarHintPart {
	deleteIndex := indexStatusBarHint(parts, KeybindHint{Key: "d", Label: "Delete session"})
	newIndex := indexStatusBarHint(parts, KeybindHint{Key: "n", Label: "New session"})
	if deleteIndex < 0 || newIndex < 0 || deleteIndex == newIndex+1 {
		return parts
	}

	deletePart := parts[deleteIndex]
	reordered := make([]statusBarHintPart, 0, len(parts))
	reordered = append(reordered, parts[:deleteIndex]...)
	reordered = append(reordered, parts[deleteIndex+1:]...)
	if deleteIndex < newIndex {
		newIndex--
	}
	insertIndex := newIndex + 1
	reordered = append(reordered, statusBarHintPart{})
	copy(reordered[insertIndex+1:], reordered[insertIndex:])
	reordered[insertIndex] = deletePart
	return reordered
}

func indexStatusBarHint(parts []statusBarHintPart, want KeybindHint) int {
	for i, part := range parts {
		if part.hint == want {
			return i
		}
	}
	return -1
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
		{Key: "s", Label: "Settings"},
		{Key: "?", Label: "Help"},
		{Key: "q", Label: "Quit"},
	}
}

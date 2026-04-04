package views

import (
	"slices"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/beeemT/substrate/internal/tui/components"
	"github.com/beeemT/substrate/internal/tui/styles"
)

// StatusBarModel renders the footer content at the bottom.
type StatusBarModel struct {
	styles styles.Styles

	// Render cache; pointer fields survive Bubble Tea's value-receiver copies.
	cachedView  *string
	cachedHints *string // fingerprint of last hints slice
	cachedText  *string
	cachedWidth *int
}

// NewStatusBarModel creates a StatusBarModel with the given styles.
func NewStatusBarModel(st styles.Styles) StatusBarModel {
	return StatusBarModel{
		styles:      st,
		cachedView:  new(string),
		cachedHints: new(string),
		cachedText:  new(string),
		cachedWidth: new(int),
	}
}

// View renders the keybind hints and right-aligned metadata within one or two footer rows.
func (s StatusBarModel) View(hints []KeybindHint, rightText string, width int) string {
	if width <= 0 {
		return ""
	}

	hintsFP := statusBarHintsFingerprint(hints)
	if *s.cachedView != "" && hintsFP == *s.cachedHints && rightText == *s.cachedText && width == *s.cachedWidth {
		return *s.cachedView
	}

	horizontalPadding := 0
	innerWidth := width
	if width >= 2 {
		horizontalPadding = 1
		innerWidth = width - 2
	}

	fitted, overflow, effectiveRight := statusBarFitLine1(s.styles, hints, rightText, width)

	reordered := reorderStatusBarHintParts(fitted)
	leftRaw := joinStatusBarHintRaw(reordered)
	left := joinStatusBarHintRendered(reordered)
	leftLen := lipgloss.Width(leftRaw)
	right := s.styles.Muted.Render(effectiveRight)
	rightLen := lipgloss.Width(effectiveRight)
	gapLen := max(innerWidth-leftLen-rightLen, 0)

	line1 := left + strings.Repeat(" ", gapLen) + right
	lineStyle := s.styles.StatusBar.Padding(0, horizontalPadding)
	result := lineStyle.Render(line1)

	if line2Parts := statusBarFitLine2(overflow, innerWidth); len(line2Parts) > 0 {
		line2 := joinStatusBarHintRendered(line2Parts)
		line2RawLen := lipgloss.Width(joinStatusBarHintRaw(line2Parts))
		line2PadLen := max(innerWidth-line2RawLen, 0)
		line2 += strings.Repeat(" ", line2PadLen)
		result += "\n" + lineStyle.Render(line2)
	}

	*s.cachedView = result
	*s.cachedHints = hintsFP
	*s.cachedText = rightText
	*s.cachedWidth = width

	return result
}

// ViewN renders the status bar into exactly n lines.
// When RequiredHeight < n, line 2 is padded as an empty status-bar-styled row.
// When n == 1 but overflow exists, the overflow hints are silently dropped.
// This guarantees the caller can allocate n rows for the footer and get exactly n rows back.
func (s StatusBarModel) ViewN(hints []KeybindHint, rightText string, width, n int) string {
	content := s.View(hints, rightText, width)
	lines := strings.Split(content, "\n")
	for len(lines) < n {
		// Pad with an empty styled row of the correct width.
		horizontalPadding := 0
		if width >= 2 {
			horizontalPadding = 1
		}
		lineStyle := s.styles.StatusBar.Padding(0, horizontalPadding)
		lines = append(lines, lineStyle.Render(""))
	}
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}

// RequiredHeight returns the number of terminal lines needed to render the status bar.
// It returns 2 when hints overflow line 1 and at least one overflow hint fits on line 2;
// otherwise 1.
func (s StatusBarModel) RequiredHeight(hints []KeybindHint, rightText string, width int) int {
	_, overflow, _ := statusBarFitLine1(s.styles, hints, rightText, width)
	if len(overflow) == 0 {
		return 1
	}
	innerWidth := width
	if width >= 2 {
		innerWidth = width - 2
	}
	if len(statusBarFitLine2(overflow, innerWidth)) > 0 {
		return 2
	}
	return 1
}

// statusBarFitLine1 determines which hints fit on line 1 alongside rightText
// and returns (fittedParts, overflowParts, effectiveRightText).
func statusBarFitLine1(st styles.Styles, hints []KeybindHint, rightText string, width int) (fitted, overflow []statusBarHintPart, effectiveRight string) {
	if width <= 0 {
		return nil, nil, ""
	}

	innerWidth := width
	if width >= 2 {
		innerWidth = width - 2
	}

	preserveLeading := hasContextualLeadingHint(hints) && len(hints) > 0
	allParts := renderStatusBarHintParts(st, hints, innerWidth, preserveLeading)

	rightText = truncate(rightText, innerWidth)
	rightLen := lipgloss.Width(rightText)

	requiredGap := 0
	if rightLen > 0 {
		requiredGap = 1
	}

	minParts := 0
	if preserveLeading && len(allParts) > 0 {
		minParts = 1
	}

	fittedCount := len(allParts)
	leftRaw := joinStatusBarHintRaw(allParts[:fittedCount])
	for fittedCount > minParts && lipgloss.Width(leftRaw)+rightLen+requiredGap > innerWidth {
		fittedCount--
		leftRaw = joinStatusBarHintRaw(allParts[:fittedCount])
	}

	effectiveRight = rightText
	if preserveLeading && fittedCount == 1 {
		leftLen := lipgloss.Width(leftRaw)
		if leftLen+rightLen+requiredGap > innerWidth {
			availableRight := innerWidth - leftLen
			if rightLen > 0 {
				availableRight--
			}
			if availableRight <= 0 {
				effectiveRight = ""
			} else {
				effectiveRight = truncate(rightText, availableRight)
			}
		}
	}

	return allParts[:fittedCount], allParts[fittedCount:], effectiveRight
}

// statusBarFitLine2 drops trailing overflow hints that don't fit on line 2.
func statusBarFitLine2(overflow []statusBarHintPart, innerWidth int) []statusBarHintPart {
	parts := overflow
	for len(parts) > 0 && lipgloss.Width(joinStatusBarHintRaw(parts)) > innerWidth {
		parts = parts[:len(parts)-1]
	}
	if len(parts) == 0 {
		return nil
	}
	return parts
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
	return !slices.Contains(DefaultHints(), hints[0])
}

// DefaultHints returns the global keybind hints always shown in the status bar.
func DefaultHints() []KeybindHint {
	return []KeybindHint{
		{Key: "n", Label: "New session"},
		{Key: "a", Label: "Add repo"},
		{Key: "/", Label: "Search sessions"},
		{Key: "s", Label: "Settings"},
		{Key: "?", Label: "Help"},
		{Key: "q", Label: "Quit"},
	}
}

// statusBarHintsFingerprint builds a cheap string fingerprint for cache comparison.
func statusBarHintsFingerprint(hints []KeybindHint) string {
	var b strings.Builder
	for _, h := range hints {
		b.WriteString(h.Key)
		b.WriteByte('\x00')
		b.WriteString(h.Label)
		b.WriteByte('\x00')
	}
	return b.String()
}

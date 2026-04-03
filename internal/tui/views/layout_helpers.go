package views

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/beeemT/substrate/internal/tui/styles"
)

func fitViewHeight(rendered string, height int) string {
	if height <= 0 {
		return ""
	}

	lines := strings.Split(rendered, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for len(lines) < height {
		lines = append(lines, "")
	}

	return strings.Join(lines, "\n")
}

func fitViewBox(rendered string, width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}

	lines := strings.Split(rendered, "\n")
	fitted := make([]string, 0, len(lines))
	lineStyle := lipgloss.NewStyle().Width(width)
	for _, line := range lines {
		// Fast path: if the line already fits within width, skip the
		// expensive lipgloss Render. ansi.StringWidth is ~6x faster than
		// lipgloss.Render for already-constrained content. This is the
		// dominant optimization for the session transcript view, where
		// viewport output is already width-padded and fitViewBox is
		// called on every View() frame during scrolling.
		if ansi.StringWidth(line) <= width {
			fitted = append(fitted, line)
		} else {
			fitted = append(fitted, lineStyle.Render(ansi.Truncate(line, width, "")))
		}
	}

	return fitViewHeight(strings.Join(fitted, "\n"), height)
}

// overlaySpinner places a styled spinner frame at the bottom-right corner of
// a multi-line body block. It replaces the trailing characters on the last line
// so the spinner sits flush against the right edge without growing the block.
func overlaySpinner(body, frame string, st styles.Styles, width int) string {
	if width <= 0 {
		return body
	}
	lines := strings.Split(body, "\n")
	if len(lines) == 0 {
		return body
	}
	idx := len(lines) - 1
	styledFrame := st.Active.Render(frame)
	frameWidth := ansi.StringWidth(styledFrame)
	lineWidth := ansi.StringWidth(lines[idx])

	if lineWidth+frameWidth+1 <= width {
		// There is room: pad to the right edge and append the spinner.
		pad := width - lineWidth - frameWidth
		lines[idx] = lines[idx] + strings.Repeat(" ", pad) + styledFrame
	} else {
		// Line is too wide: truncate to make room for " " + spinner.
		truncated := ansi.Truncate(lines[idx], width-frameWidth-1, "")
		pad := max(1, width-ansi.StringWidth(truncated)-frameWidth)
		lines[idx] = truncated + strings.Repeat(" ", pad) + styledFrame
	}
	return strings.Join(lines, "\n")
}

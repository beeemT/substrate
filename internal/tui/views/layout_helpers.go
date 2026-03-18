package views

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
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
	for _, line := range lines {
		fitted = append(fitted, lipgloss.NewStyle().Width(width).Render(ansi.Truncate(line, width, "")))
	}

	return fitViewHeight(strings.Join(fitted, "\n"), height)
}

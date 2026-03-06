package views

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/beeemT/substrate/internal/tui/styles"
)

// HeaderModel renders the 1-line application header.
type HeaderModel struct {
	WorkspaceName string
	status        string // e.g. "Planning", "Implementing" — empty when no session selected
	styles        styles.Styles
}

// NewHeaderModel creates a HeaderModel for the given workspace.
func NewHeaderModel(workspaceName string, st styles.Styles) HeaderModel {
	return HeaderModel{WorkspaceName: workspaceName, styles: st}
}

// SetStatus sets the right-aligned status badge text.
func (h *HeaderModel) SetStatus(s string) { h.status = s }

// View renders the header at the given terminal width.
func (h HeaderModel) View(width int) string {
	left := " Substrate"
	if h.WorkspaceName != "" {
		left += " ─ " + h.WorkspaceName
	}
	right := ""
	if h.status != "" {
		right = h.status + " "
	}
	leftLen := lipgloss.Width(left)
	rightLen := lipgloss.Width(right)
	pad := width - leftLen - rightLen
	if pad < 0 {
		pad = 0
	}
	line := left + strings.Repeat(" ", pad) + right
	headerStyle := lipgloss.NewStyle().
		Background(lipgloss.Color("#1a1a2e")).
		Foreground(lipgloss.Color("#e0e0e0")).
		Bold(true).
		Width(width)
	return headerStyle.Render(line)
}

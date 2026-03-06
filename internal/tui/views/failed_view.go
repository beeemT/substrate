package views

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/beeemT/substrate/internal/tui/styles"
)

// FailedModel shows failure details.
type FailedModel struct {
	title   string
	reason  string
	details string
	styles  styles.Styles
	width   int
	height  int
}

func NewFailedModel(st styles.Styles) FailedModel {
	return FailedModel{styles: st}
}

func (m *FailedModel) SetSize(w, h int) { m.width = w; m.height = h }
func (m *FailedModel) SetTitle(t string) { m.title = t }

func (m *FailedModel) SetFailure(reason, details string) {
	m.reason = reason
	m.details = details
}

func (m FailedModel) Update(_ tea.Msg) (FailedModel, tea.Cmd) {
	return m, nil
}

func (m FailedModel) View() string {
	divider := lipgloss.NewStyle().Foreground(lipgloss.Color("#2d2d44")).Render(strings.Repeat("─", m.width))
	header := m.styles.Title.Render(m.title) + "  " + m.styles.Error.Render("✗ Failed")
	lines := []string{header, divider, ""}
	if m.reason != "" {
		lines = append(lines, m.styles.Error.Render(m.reason), "")
	}
	if m.details != "" {
		lines = append(lines, m.styles.Subtitle.Render(m.details), "")
	}
	lines = append(lines, m.styles.Muted.Render("[↑↓] Scroll"))
	return strings.Join(lines, "\n")
}

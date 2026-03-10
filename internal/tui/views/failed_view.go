package views

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/beeemT/substrate/internal/tui/components"
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

func (m *FailedModel) SetSize(w, h int)  { m.width = w; m.height = h }
func (m *FailedModel) SetTitle(t string) { m.title = t }

func (m *FailedModel) SetFailure(reason, details string) {
	m.reason = reason
	m.details = details
}

func (m FailedModel) Update(_ tea.Msg) (FailedModel, tea.Cmd) {
	return m, nil
}

func (m FailedModel) View() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}
	header := m.styles.Title.Render(m.title) + "  " + m.styles.Error.Render("✗ Failed")
	divider := components.RenderDivider(m.styles, m.width)
	lines := []string{header, divider, ""}
	if m.reason != "" {
		lines = append(lines, m.styles.Error.Render(m.reason), "")
	}
	if m.details != "" {
		lines = append(lines, m.styles.Subtitle.Render(m.details), "")
	}
	lines = append(lines, components.RenderKeyHints(m.styles, []components.KeyHint{{Key: "↑↓", Label: "Scroll"}}, "  "))
	return fitViewBox(strings.Join(lines, "\n"), m.width, m.height)
}

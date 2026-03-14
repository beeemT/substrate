package views

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/beeemT/substrate/internal/tui/components"
	"github.com/beeemT/substrate/internal/tui/styles"
)

// MRInfo holds MR/PR link information for a repo.
type MRInfo struct {
	RepoName string
	MRURL    string
	MRRef    string
	State    string
	IsOpen   bool
}

// CompletedModel shows durable PR/MR completion details and review artifacts.
type CompletedModel struct {
	title        string
	statusLabel  string
	completedAt  time.Time
	mrLinks      []MRInfo
	warnings     []string
	styles       styles.Styles
	width        int
	height       int
	selectedLink int
}

func NewCompletedModel(st styles.Styles) CompletedModel {
	return CompletedModel{styles: st, statusLabel: "Review artifacts"}
}

func (m *CompletedModel) SetSize(w, h int)            { m.width = w; m.height = h }
func (m *CompletedModel) SetTitle(t string)           { m.title = t }
func (m *CompletedModel) SetStatusLabel(label string) { m.statusLabel = label }

func (m *CompletedModel) SetData(completedAt time.Time, mrLinks []MRInfo, warnings []string) {
	m.completedAt = completedAt
	m.mrLinks = mrLinks
	m.warnings = warnings
	if len(m.mrLinks) == 0 {
		m.selectedLink = 0
	} else if m.selectedLink >= len(m.mrLinks) {
		m.selectedLink = len(m.mrLinks) - 1
	}
}

func (m CompletedModel) KeybindHints() []KeybindHint {
	hints := []KeybindHint{{Key: "Esc", Label: "Close"}}
	if len(m.mrLinks) > 0 {
		hints = append([]KeybindHint{{Key: "↑↓", Label: "Select"}, {Key: "Enter", Label: "Open"}}, hints...)
	}
	return hints
}

func (m CompletedModel) Update(msg tea.Msg) (CompletedModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if len(m.mrLinks) > 0 && m.selectedLink > 0 {
				m.selectedLink--
			}
		case "down", "j":
			if len(m.mrLinks) > 0 && m.selectedLink < len(m.mrLinks)-1 {
				m.selectedLink++
			}
		case "enter":
			if len(m.mrLinks) > 0 && strings.TrimSpace(m.mrLinks[m.selectedLink].MRURL) != "" {
				url := m.mrLinks[m.selectedLink].MRURL
				return m, func() tea.Msg { return OpenExternalURLMsg{URL: url} }
			}
		}
	}
	return m, nil
}

func (m CompletedModel) View() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}
	statusLabel := strings.TrimSpace(m.statusLabel)
	if statusLabel == "" {
		statusLabel = "Review artifacts"
	}
	header := m.styles.Title.Render(m.title) + "  " + m.styles.Success.Render(statusLabel)
	divider := components.RenderDivider(m.styles, m.width)

	var lines []string
	lines = append(lines, header, divider, "")

	if !m.completedAt.IsZero() && strings.TrimSpace(m.statusLabel) == "✓ Completed" {
		lines = append(lines, m.styles.Subtitle.Render("Completed "+m.completedAt.Format("2006-01-02 15:04 MST")), "")
	}

	if len(m.mrLinks) > 0 {
		lines = append(lines, m.styles.SectionLabel.Render("Repos:"))
		for i, mr := range m.mrLinks {
			icon := m.styles.Success.Render("✓")
			status := ""
			if mr.MRRef != "" {
				status = "  " + m.styles.Active.Render(mr.MRRef)
			}
			if strings.TrimSpace(mr.State) != "" {
				status += "  " + m.styles.Muted.Render(strings.TrimSpace(mr.State))
			}
			prefix := "  "
			if i == m.selectedLink {
				prefix = m.styles.Active.Render("▶ ")
			}
			lines = append(lines, prefix+icon+" "+m.styles.Subtitle.Render(mr.RepoName)+status)
		}
	}

	for _, w := range m.warnings {
		lines = append(lines, m.styles.Warning.Render("⚠ "+w))
	}

	lines = append(lines, "", renderOverlayHintsRow(m.styles, m.KeybindHints(), m.width))
	return fitViewBox(strings.Join(lines, "\n"), m.width, m.height)
}


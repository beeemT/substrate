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
	IsOpen   bool
}

// CompletedModel shows the completion summary.
type CompletedModel struct {
	title       string
	completedAt time.Time
	mrLinks     []MRInfo
	warnings    []string
	styles      styles.Styles
	width       int
	height      int
}

func NewCompletedModel(st styles.Styles) CompletedModel {
	return CompletedModel{styles: st}
}

func (m *CompletedModel) SetSize(w, h int)  { m.width = w; m.height = h }
func (m *CompletedModel) SetTitle(t string) { m.title = t }

func (m *CompletedModel) SetData(completedAt time.Time, mrLinks []MRInfo, warnings []string) {
	m.completedAt = completedAt
	m.mrLinks = mrLinks
	m.warnings = warnings
}

func (m CompletedModel) Update(_ tea.Msg) (CompletedModel, tea.Cmd) {
	return m, nil
}

func (m CompletedModel) View() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}
	header := m.styles.Title.Render(m.title) + "  " + m.styles.Success.Render("✓ Completed")
	divider := components.RenderDivider(m.styles, m.width)

	var lines []string
	lines = append(lines, header, divider, "")

	if !m.completedAt.IsZero() {
		lines = append(lines, m.styles.Subtitle.Render("Completed "+m.completedAt.Format("2006-01-02 15:04 MST")), "")
	}

	if len(m.mrLinks) > 0 {
		lines = append(lines, m.styles.SectionLabel.Render("Repos:"))
		for _, mr := range m.mrLinks {
			icon := m.styles.Success.Render("✓")
			status := ""
			if mr.MRRef != "" {
				status = "  " + m.styles.Active.Render(mr.MRRef)
				if mr.IsOpen {
					status += m.styles.Muted.Render(" (open)")
				}
			}
			lines = append(lines, "  "+icon+" "+m.styles.Subtitle.Render(mr.RepoName)+status)
		}
	}

	for _, w := range m.warnings {
		lines = append(lines, m.styles.Warning.Render("⚠ "+w))
	}

	lines = append(lines, "", components.RenderKeyHints(m.styles, []components.KeyHint{{Key: "↑↓", Label: "Scroll"}}, "  "))
	return fitViewBox(strings.Join(lines, "\n"), m.width, m.height)
}

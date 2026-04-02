package views

import (
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
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
//
//nolint:recvcheck // Bubble Tea: Update returns value, View on value receiver
type CompletedModel struct {
	title         string
	statusLabel   string
	completedAt   time.Time
	mrLinks       []MRInfo
	warnings      []string
	styles        styles.Styles
	width         int
	height        int
	selectedLink  int
	workItemID    string
	feedbackInput textinput.Model
	inputActive   bool
}

func NewCompletedModel(st styles.Styles) CompletedModel {
	ti := components.NewTextInput()
	ti.Placeholder = "Describe what needs to change..."
	ti.CharLimit = 2000
	return CompletedModel{styles: st, statusLabel: "Review artifacts", feedbackInput: ti}
}

func (m *CompletedModel) SetSize(w, h int)            { m.width = w; m.height = h }
func (m *CompletedModel) SetTitle(t string)           { m.title = t }
func (m *CompletedModel) SetStatusLabel(label string) { m.statusLabel = label }
func (m *CompletedModel) SetWorkItemID(id string)     { m.workItemID = id }

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

func (m CompletedModel) InputCaptured() bool { return m.inputActive }

func (m CompletedModel) KeybindHints() []KeybindHint {
	if m.inputActive {
		return []KeybindHint{
			{Key: "Enter", Label: "Submit"},
			{Key: "Esc", Label: "Cancel"},
		}
	}
	hints := []KeybindHint{{Key: "Esc", Label: "Close"}}
	if m.workItemID != "" {
		hints = append(hints, KeybindHint{Key: "c", Label: "Changes"})
	}
	if len(m.mrLinks) > 0 {
		hints = append([]KeybindHint{{Key: "↑↓", Label: "Select"}, {Key: "Enter", Label: "Open"}}, hints...)
	}

	return hints
}

func (m CompletedModel) Update(msg tea.Msg) (CompletedModel, tea.Cmd) {
	if msg, ok := msg.(tea.KeyMsg); ok {
		if m.inputActive {
			switch msg.String() {
			case "enter":
				feedback := m.feedbackInput.Value()
				m.feedbackInput.SetValue("")
				m.inputActive = false
				m.feedbackInput.Blur()
				return m, func() tea.Msg {
					return FollowUpPlanMsg{WorkItemID: m.workItemID, Feedback: feedback}
				}
			case "esc":
				m.feedbackInput.SetValue("")
				m.inputActive = false
				m.feedbackInput.Blur()
				return m, nil
			default:
				var cmd tea.Cmd
				m.feedbackInput, cmd = m.feedbackInput.Update(msg)
				return m, cmd
			}
		}

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
		case "c":
			if m.workItemID != "" {
				m.inputActive = true
				return m, m.feedbackInput.Focus()
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

	if m.inputActive {
		lines = append(lines, "", components.RenderDivider(m.styles, m.width), m.feedbackInput.View())
	}

	return fitViewBox(strings.Join(lines, "\n"), m.width, m.height)
}

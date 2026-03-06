package views

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/tui/styles"
)

// RepoReviewResult holds per-repo review data.
type RepoReviewResult struct {
	RepoName  string
	Cycles    []domain.ReviewCycle
	Critiques []domain.Critique
}

// ReviewModel renders review output with critiques and severity.
type ReviewModel struct {
	workItemID string
	repos      []RepoReviewResult
	cursor     int // critique cursor within active repo
	activeRepo int
	title      string
	styles     styles.Styles
	width      int
	height     int
}

func NewReviewModel(st styles.Styles) ReviewModel {
	return ReviewModel{styles: st}
}

func (m *ReviewModel) SetSize(w, h int)        { m.width = w; m.height = h }
func (m *ReviewModel) SetTitle(t string)       { m.title = t }
func (m *ReviewModel) SetWorkItemID(id string) { m.workItemID = id }

func (m *ReviewModel) SetRepos(repos []RepoReviewResult) {
	m.repos = repos
	m.activeRepo = 0
	m.cursor = 0
}

func (m ReviewModel) KeybindHints() []KeybindHint {
	return []KeybindHint{
		{Key: "j/k", Label: "Navigate critiques"},
		{Key: "Tab", Label: "Switch repo"},
		{Key: "r", Label: "Re-implement"},
		{Key: "o", Label: "Override accept"},
	}
}

func critiqueSeverityStyle(sev domain.CritiqueSeverity, st styles.Styles) lipgloss.Style {
	switch sev {
	case domain.CritiqueCritical:
		return st.Error
	case domain.CritiqueMajor:
		return st.Warning
	case domain.CritiqueMinor:
		return st.Muted
	case domain.CritiqueNit:
		return st.Muted
	default:
		return st.Muted
	}
}

func (m ReviewModel) Update(msg tea.Msg) (ReviewModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "j", "down":
			if len(m.repos) > m.activeRepo {
				crits := m.repos[m.activeRepo].Critiques
				if m.cursor < len(crits)-1 {
					m.cursor++
				}
			}
		case "k", "up":
			if m.cursor > 0 {
				m.cursor--
			}
		case "tab":
			if len(m.repos) > 0 {
				m.activeRepo = (m.activeRepo + 1) % len(m.repos)
				m.cursor = 0
			}
		case "r":
			return m, func() tea.Msg { return ReimplementMsg{WorkItemID: m.workItemID} }
		case "o":
			return m, func() tea.Msg { return ConfirmOverrideAcceptMsg{WorkItemID: m.workItemID} }
		}
	}
	return m, nil
}

func (m ReviewModel) View() string {
	titleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#f0f0f0")).Bold(true)
	divider := lipgloss.NewStyle().Foreground(lipgloss.Color("#2d2d44")).Render(strings.Repeat("─", m.width))

	header := titleStyle.Render(m.title + " · Reviewing")

	// Repo tabs
	var tabs []string
	for i, r := range m.repos {
		if i == m.activeRepo {
			tabs = append(tabs, lipgloss.NewStyle().Foreground(lipgloss.Color("#f0f0f0")).Underline(true).Render(r.RepoName))
		} else {
			tabs = append(tabs, lipgloss.NewStyle().Foreground(lipgloss.Color("#6b7280")).Render(r.RepoName))
		}
	}
	tabRow := strings.Join(tabs, "  │  ")

	var body strings.Builder
	if len(m.repos) > m.activeRepo {
		repo := m.repos[m.activeRepo]
		if len(repo.Critiques) == 0 {
			body.WriteString(m.styles.Success.Render("✓ No critiques for this repo."))
		} else {
			body.WriteString(fmt.Sprintf("%d critique(s):\n", len(repo.Critiques)))
			for i, c := range repo.Critiques {
				sevStyle := critiqueSeverityStyle(c.Severity, m.styles)
				prefix := "  "
				if i == m.cursor {
					prefix = "▶ "
				}
				line := prefix + sevStyle.Render(fmt.Sprintf("[%s] ", strings.ToUpper(string(c.Severity))))
				line += lipgloss.NewStyle().Foreground(lipgloss.Color("#f0f0f0")).Render(c.Description)
				if c.FilePath != "" {
					line += lipgloss.NewStyle().Foreground(lipgloss.Color("#6b7280")).Render(" — " + c.FilePath)
				}
				if c.Suggestion != "" && i == m.cursor {
					line += "\n    " + lipgloss.NewStyle().Foreground(lipgloss.Color("#b0b0b0")).Render("Suggestion: "+c.Suggestion)
				}
				body.WriteString(line + "\n")
			}
		}
	}

	hints := lipgloss.NewStyle().Foreground(lipgloss.Color("#6b7280")).Render(
		"[j/k] Navigate  [Tab] Switch repo  [r] Re-implement  [o] Override accept")

	return strings.Join([]string{header, divider, tabRow, divider, body.String(), hints}, "\n")
}

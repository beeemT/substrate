package views

import (
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/beeemT/substrate/internal/tui/styles"
)

// PlanningViewModel renders live planning agent output (log tail).
type PlanningViewModel struct {
	viewport  viewport.Model
	lines     []string
	paused    bool
	title     string
	logPath   string
	sessionID string
	offset    int64
	styles    styles.Styles
	width     int
	height    int
}

func NewPlanningViewModel(st styles.Styles) PlanningViewModel {
	vp := viewport.New(0, 0)
	return PlanningViewModel{viewport: vp, styles: st}
}

func (m *PlanningViewModel) SetSize(width, height int) {
	m.width = width
	m.height = height
	m.viewport.Width = width
	m.viewport.Height = height - 3 // reserve header + divider + hints
}

func (m *PlanningViewModel) SetTitle(title string) { m.title = title }

func (m *PlanningViewModel) SetLogPath(sessionID, logPath string) {
	m.sessionID = sessionID
	m.logPath = logPath
	m.offset = 0
	m.lines = nil
	m.viewport.SetContent("")
}

func (m *PlanningViewModel) TailCmd() tea.Cmd {
	if m.logPath == "" {
		return nil
	}
	return TailSessionLogCmd(m.logPath, m.sessionID, m.offset)
}

func (m PlanningViewModel) Update(msg tea.Msg) (PlanningViewModel, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case SessionLogLinesMsg:
		if msg.SessionID != m.sessionID {
			return m, nil
		}
		m.offset = msg.NextOffset
		if len(msg.Lines) > 0 {
			m.lines = append(m.lines, msg.Lines...)
			m.viewport.SetContent(strings.Join(m.lines, "\n"))
			if !m.paused {
				m.viewport.GotoBottom()
			}
		}
		return m, TailSessionLogCmd(m.logPath, m.sessionID, m.offset)
	case tea.KeyMsg:
		switch msg.String() {
		case "p":
			m.paused = !m.paused
		default:
			m.viewport, cmd = m.viewport.Update(msg)
		}
	default:
		m.viewport, cmd = m.viewport.Update(msg)
	}
	return m, cmd
}

func (m PlanningViewModel) View() string {
	titleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#f0f0f0")).Bold(true)
	divider := lipgloss.NewStyle().Foreground(lipgloss.Color("#2d2d44")).Render(strings.Repeat("─", m.width))
	header := titleStyle.Render(m.title + " · Planning")
	pauseHint := ""
	if m.paused {
		pauseHint = lipgloss.NewStyle().Foreground(lipgloss.Color("#fbbf24")).Render(" [PAUSED]")
	}
	hints := lipgloss.NewStyle().Foreground(lipgloss.Color("#6b7280")).Render("[↑↓] Scroll  [p] Pause/unpause")
	return strings.Join([]string{header + pauseHint, divider, m.viewport.View(), hints}, "\n")
}

package views

import (
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/beeemT/substrate/internal/tui/styles"
)

// SessionLogModel renders either a live-tailing session log or a static interaction transcript.
type SessionLogModel struct {
	viewport  viewport.Model
	lines     []string
	paused    bool
	title     string
	modeLabel string
	meta      string
	logPath   string
	sessionID string
	offset    int64
	live      bool
	styles    styles.Styles
	width     int
	height    int
}

func NewSessionLogModel(st styles.Styles) SessionLogModel {
	vp := viewport.New(0, 0)
	return SessionLogModel{viewport: vp, styles: st, modeLabel: "Session interaction"}
}

func (m *SessionLogModel) SetSize(width, height int) {
	m.width = width
	m.height = height
	m.syncViewportSize()
}

func (m *SessionLogModel) syncViewportSize() {
	m.viewport.Width = m.width
	reservedRows := 3 // header + divider + hints
	if strings.TrimSpace(m.meta) != "" {
		reservedRows++
	}
	m.viewport.Height = max(1, m.height-reservedRows)
}

func (m *SessionLogModel) SetTitle(title string) { m.title = title }

func (m *SessionLogModel) SetModeLabel(label string) { m.modeLabel = label }

func (m *SessionLogModel) SetMeta(meta string) {
	m.meta = meta
	m.syncViewportSize()
}

func (m *SessionLogModel) SetLogPath(sessionID, logPath string) {
	m.sessionID = sessionID
	m.logPath = logPath
	m.live = true
	m.offset = 0
	m.lines = nil
	m.viewport.SetContent("")
}

func (m *SessionLogModel) SetStaticContent(lines []string) {
	m.live = false
	m.logPath = ""
	m.sessionID = ""
	m.offset = 0
	m.lines = append([]string(nil), lines...)
	m.viewport.SetContent(strings.Join(m.lines, "\n"))
	m.viewport.GotoTop()
}

func (m *SessionLogModel) TailCmd() tea.Cmd {
	if !m.live || m.logPath == "" {
		return nil
	}
	return TailSessionLogCmd(m.logPath, m.sessionID, m.offset)
}

func (m SessionLogModel) KeybindHints() []KeybindHint {
	return []KeybindHint{
		{Key: "↑↓", Label: "Scroll"},
		{Key: "p", Label: "Pause/unpause"},
	}
}

func (m SessionLogModel) Update(msg tea.Msg) (SessionLogModel, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case SessionLogLinesMsg:
		if !m.live || msg.SessionID != m.sessionID {
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

func (m SessionLogModel) View() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}

	titleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#f0f0f0")).Bold(true)
	divider := lipgloss.NewStyle().Foreground(lipgloss.Color("#2d2d44")).Render(strings.Repeat("─", m.width))
	headerText := m.title
	if m.modeLabel != "" {
		headerText += " · " + m.modeLabel
	}
	header := titleStyle.Render(headerText)
	pauseHint := ""
	if m.paused {
		pauseHint = lipgloss.NewStyle().Foreground(lipgloss.Color("#fbbf24")).Render(" [PAUSED]")
	}
	meta := ""
	if strings.TrimSpace(m.meta) != "" {
		meta = lipgloss.NewStyle().Foreground(lipgloss.Color("#94a3b8")).Render(m.meta)
	}
	body := m.viewport.View()
	if strings.TrimSpace(body) == "" {
		body = lipgloss.NewStyle().Foreground(lipgloss.Color("#6b7280")).Render("No session output captured.")
	}
	hints := lipgloss.NewStyle().Foreground(lipgloss.Color("#6b7280")).Render("[↑↓] Scroll  [p] Pause/unpause")
	parts := []string{header + pauseHint, divider}
	if meta != "" {
		parts = append(parts, meta)
	}
	parts = append(parts, body, hints)
	return fitViewBox(strings.Join(parts, "\n"), m.width, m.height)
}

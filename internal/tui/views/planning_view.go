package views

import (
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/beeemT/substrate/internal/tui/components"
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
	if m.live && m.sessionID == sessionID && m.logPath == logPath {
		return
	}
	m.sessionID = sessionID
	m.logPath = logPath
	m.live = true
	m.offset = 0
	m.lines = nil
	m.viewport.SetContent("")
	m.viewport.GotoTop()
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

	headerText := m.title
	if m.modeLabel != "" {
		headerText += " · " + m.modeLabel
	}
	header := components.RenderHeaderBlock(m.styles, components.HeaderBlockSpec{
		Title:   headerText,
		Meta:    m.meta,
		Width:   m.width,
		Divider: true,
	})
	if m.paused {
		headerLines := strings.Split(header, "\n")
		headerLines[0] += m.styles.Warning.Render(" [PAUSED]")
		header = strings.Join(headerLines, "\n")
	}

	body := m.viewport.View()
	if strings.TrimSpace(body) == "" {
		body = m.styles.Muted.Render("No session output captured.")
	}
	hints := components.RenderKeyHints(m.styles, componentHints(m.KeybindHints()), "  ")
	parts := append(strings.Split(header, "\n"), body, hints)
	return fitViewBox(strings.Join(parts, "\n"), m.width, m.height)
}

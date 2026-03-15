package views

import (
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/beeemT/substrate/internal/sessionlog"
	"github.com/beeemT/substrate/internal/tui/components"
	"github.com/beeemT/substrate/internal/tui/styles"
)

// SessionLogModel renders either a live-tailing session log or a static interaction transcript.
type SessionLogModel struct {
	viewport         viewport.Model
	entries          []sessionlog.Entry
	verbose          bool
	collapseThinking bool
	paused           bool
	title            string
	modeLabel        string
	meta             string
	notice           *sourceDetailsNotice
	logPath          string
	sessionID        string
	offset           int64
	live             bool
	styles           styles.Styles
	width            int
	height           int
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
	headerLines := len(strings.Split(m.header(), "\n"))
	m.viewport.Height = max(1, m.height-headerLines-1)
	if len(m.entries) > 0 {
		m.viewport.SetContent(RenderTranscript(m.styles, m.entries, m.width, m.verbose, m.collapseThinking))
	}
}

func (m *SessionLogModel) SetTitle(title string) { m.title = title }

func (m *SessionLogModel) SetModeLabel(label string) { m.modeLabel = label }

func (m *SessionLogModel) SetMeta(meta string) {
	m.meta = meta
	m.syncViewportSize()
}

func (m *SessionLogModel) SetNotice(notice *sourceDetailsNotice) {
	m.notice = notice
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
	m.entries = nil
	m.viewport.SetContent("")
	m.viewport.GotoTop()
}

func (m *SessionLogModel) SetStaticContent(entries []sessionlog.Entry) {
	m.live = false
	m.logPath = ""
	m.sessionID = ""
	m.offset = 0
	m.entries = append([]sessionlog.Entry(nil), entries...)
	m.viewport.SetContent(RenderTranscript(m.styles, m.entries, m.width, m.verbose, m.collapseThinking))
	m.viewport.GotoTop()
}

func (m *SessionLogModel) TailCmd() tea.Cmd {
	if !m.live || m.logPath == "" {
		return nil
	}
	return TailSessionLogCmd(m.logPath, m.sessionID, m.offset)
}

func (m SessionLogModel) KeybindHints() []KeybindHint {
	hints := []KeybindHint{
		{Key: "↑↓", Label: "Scroll"},
		{Key: "p", Label: "Pause/unpause"},
		{Key: "v", Label: "Verbose logs"},
		{Key: "t", Label: "Toggle thinking"},
	}
	if m.notice != nil {
		hints = append(hints, KeybindHint{Key: "Enter", Label: "Open overview"})
	}
	return hints
}

func (m SessionLogModel) Update(msg tea.Msg) (SessionLogModel, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case SessionLogLinesMsg:
		if !m.live || msg.SessionID != m.sessionID {
			return m, nil
		}
		m.offset = msg.NextOffset
		if len(msg.Entries) > 0 {
			m.entries = append(m.entries, msg.Entries...)
			m.viewport.SetContent(RenderTranscript(m.styles, m.entries, m.width, m.verbose, m.collapseThinking))
			if !m.paused {
				m.viewport.GotoBottom()
			}
		}
		return m, TailSessionLogCmd(m.logPath, m.sessionID, m.offset)
	case tea.KeyMsg:
		switch msg.String() {
		case "p":
			m.paused = !m.paused
		case "v":
			m.verbose = !m.verbose
			m.viewport.SetContent(RenderTranscript(m.styles, m.entries, m.width, m.verbose, m.collapseThinking))
		case "t":
			m.collapseThinking = !m.collapseThinking
			m.viewport.SetContent(RenderTranscript(m.styles, m.entries, m.width, m.verbose, m.collapseThinking))
		default:
			m.viewport, cmd = m.viewport.Update(msg)
		}
	default:
		m.viewport, cmd = m.viewport.Update(msg)
	}
	return m, cmd
}

func (m SessionLogModel) header() string {
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
	if notice := m.noticeView(); notice != "" {
		return header + "\n" + notice
	}
	return header
}

func (m SessionLogModel) noticeView() string {
	return renderTaskViewNotice(m.styles, m.width, m.notice)
}

func (m SessionLogModel) View() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}

	header := m.header()
	body := m.viewport.View()
	if strings.TrimSpace(body) == "" {
		body = m.styles.Muted.Render("No session output captured.")
	}
	hints := components.RenderKeyHints(m.styles, componentHints(m.KeybindHints()), "  ")
	parts := append(strings.Split(header, "\n"), body, hints)
	return fitViewBox(strings.Join(parts, "\n"), m.width, m.height)
}

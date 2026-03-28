package views

import (
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/beeemT/substrate/internal/sessionlog"
	"github.com/beeemT/substrate/internal/tui/components"
	"github.com/beeemT/substrate/internal/tui/styles"
)

const sessionLogSpinnerInterval = 100 * time.Millisecond

const sessionLogSilenceThreshold = 3 * time.Minute

// sessionLogSpinnerFrames are braille animation frames for the activity spinner.
var sessionLogSpinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// sessionLogSpinnerTickMsg drives the activity spinner in SessionLogModel.
type sessionLogSpinnerTickMsg struct{}

// SessionLogModel renders either a live-tailing session log or a static interaction transcript.
//
//nolint:recvcheck // Bubble Tea: Update returns value, View on value receiver
type SessionLogModel struct {
	viewport         viewport.Model
	entries          []sessionlog.Entry
	verbose          bool
	collapseThinking bool
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

	// Rebuild guard: track the parameters used in the last RenderTranscript call so
	// that syncViewportSize (called on every SetMeta / SetNotice / SetSize) can skip
	// the expensive rebuild when only the viewport height changed (header line count
	// differs) but the transcript content itself is unchanged.
	renderedEntryCount int
	renderedWidth      int
	renderedVerbose    bool
	renderedCollapse   bool

	steerInput         textinput.Model
	steerActive        bool
	failedSessionID    string // non-empty when viewing a failed session's log
	completedSessionID string // non-empty when viewing a completed session's log

	// Activity spinner: shown when an agent is actively running for this session.
	agentActive  bool
	spinnerFrame int

	// Silence detection: track last event arrival to surface no-output warnings.
	lastEventAt         time.Time
	silenceNoticeActive bool
}

func NewSessionLogModel(st styles.Styles) SessionLogModel {
	vp := viewport.New(0, 0)

	ti := components.NewTextInput()
	ti.Placeholder = "Send steering prompt to agent..."
	ti.CharLimit = 2000

	return SessionLogModel{viewport: vp, styles: st, modeLabel: "Session interaction", steerInput: ti}
}

func (m *SessionLogModel) SetSize(width, height int) {
	m.width = width
	m.height = height
	m.syncViewportSize()
}

func (m *SessionLogModel) syncViewportSize() {
	m.viewport.Width = m.width
	headerLines := len(strings.Split(m.header(), "\n"))
	reserved := headerLines
	if m.steerActive {
		reserved += 2 // divider + input row
	}
	m.viewport.Height = max(1, m.height-reserved)
	if len(m.entries) > 0 && m.transcriptNeedsRebuild() {
		m.doRebuildTranscript()
	}
}

// transcriptNeedsRebuild reports whether the rendered transcript is stale.
func (m *SessionLogModel) transcriptNeedsRebuild() bool {
	return len(m.entries) != m.renderedEntryCount ||
		m.width != m.renderedWidth ||
		m.verbose != m.renderedVerbose ||
		m.collapseThinking != m.renderedCollapse
}

// doRebuildTranscript unconditionally re-renders the full transcript and
// updates the rebuild-guard fields. Call this when content has definitely
// changed (new entries, flag toggle, width change); prefer syncViewportSize
// when only layout dimensions may have changed.
func (m *SessionLogModel) doRebuildTranscript() {
	m.viewport.SetContent(RenderTranscript(m.styles, m.entries, m.width, m.verbose, m.collapseThinking))
	m.renderedEntryCount = len(m.entries)
	m.renderedWidth = m.width
	m.renderedVerbose = m.verbose
	m.renderedCollapse = m.collapseThinking
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
	m.renderedEntryCount = 0
	m.viewport.SetContent("")
	m.viewport.GotoTop()
}

func (m *SessionLogModel) SetStaticContent(entries []sessionlog.Entry) {
	m.live = false
	m.logPath = ""
	m.sessionID = ""
	m.offset = 0
	m.agentActive = false
	m.silenceNoticeActive = false
	m.lastEventAt = time.Time{}
	m.entries = append([]sessionlog.Entry(nil), entries...)
	m.doRebuildTranscript()
	m.viewport.GotoBottom()
}

func (m *SessionLogModel) SetFailedSession(sessionID string) {
	m.failedSessionID = sessionID
	if sessionID != "" {
		m.steerInput.Placeholder = "Send follow-up to restart failed session..."
	} else {
		m.steerInput.Placeholder = "Send steering prompt to agent..."
	}
}

func (m *SessionLogModel) ClearFailedSession() {
	m.failedSessionID = ""
	m.steerInput.Placeholder = "Send steering prompt to agent..."
}

func (m *SessionLogModel) SetCompletedSession(sessionID string) {
	m.completedSessionID = sessionID
	if sessionID != "" {
		m.steerInput.Placeholder = "Send follow-up to completed session..."
	} else {
		m.steerInput.Placeholder = "Send steering prompt to agent..."
	}
}

func (m *SessionLogModel) ClearCompletedSession() {
	m.completedSessionID = ""
	m.steerInput.Placeholder = "Send steering prompt to agent..."
}

// SetAgentActive controls the activity spinner. It should be set to true when
// an agent is actively running for the displayed session (based on the session's
// Task status), independent of the work-item state.
func (m *SessionLogModel) SetAgentActive(active bool) tea.Cmd {
	if m.agentActive == active {
		return nil
	}
	m.agentActive = active
	m.spinnerFrame = 0
	if active {
		m.lastEventAt = time.Now()
		m.silenceNoticeActive = false
		return sessionLogSpinnerTickCmd()
	}
	// Warning occupies the divider slot; clearing it does not affect header
	// line count, so no viewport resize is needed.
	m.silenceNoticeActive = false
	return nil
}

func sessionLogSpinnerTickCmd() tea.Cmd {
	return tea.Tick(sessionLogSpinnerInterval, func(time.Time) tea.Msg {
		return sessionLogSpinnerTickMsg{}
	})
}

func (m *SessionLogModel) TailCmd() tea.Cmd {
	if !m.live || m.logPath == "" {
		return nil
	}

	return TailSessionLogCmd(m.logPath, m.sessionID, m.offset)
}

// SessionID returns the session ID being tailed (empty if static).
func (m SessionLogModel) SessionID() string { return m.sessionID }

func (m SessionLogModel) InputCaptured() bool { return m.steerActive }

func (m SessionLogModel) KeybindHints() []KeybindHint {
	if m.steerActive {
		return []KeybindHint{
			{Key: "Enter", Label: "Send"},
			{Key: "Esc", Label: "Cancel"},
		}
	}
	hints := []KeybindHint{
		{Key: "↑↓", Label: "Scroll"},
		{Key: "f", Label: "Follow tail"},
		{Key: "g", Label: "Go to start"},
		{Key: "v", Label: "Verbose logs"},
	}
	if hasThinkingBlocks(m.entries) {
		hints = append(hints, KeybindHint{Key: "t", Label: "Toggle thinking"})
	}
	if m.notice != nil {
		hints = append(hints, KeybindHint{Key: "Enter", Label: "Open overview"})
	}
	if m.failedSessionID != "" || m.completedSessionID != "" {
		hints = append(hints, KeybindHint{Key: "p", Label: "Follow up"})
	} else if m.live {
		hints = append(hints, KeybindHint{Key: "p", Label: "Prompt agent"})
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
			wasAtBottom := m.viewport.AtBottom()
			m.entries = append(m.entries, msg.Entries...)
			m.lastEventAt = time.Now()
			// Clear any silence warning and rebuild transcript. Warning replaces
			// the divider, so no viewport resize is needed on state change.
			m.silenceNoticeActive = false
			m.doRebuildTranscript()
			if wasAtBottom {
				m.viewport.GotoBottom()
			}
		}

		return m, TailSessionLogCmd(m.logPath, m.sessionID, m.offset)
	case tea.KeyMsg:
		if m.steerActive {
			switch msg.String() {
			case "enter":
				text := m.steerInput.Value()
				m.steerInput.SetValue("")
				m.steerActive = false
				m.steerInput.Blur()
				m.syncViewportSize()
				if text != "" {
					if m.failedSessionID != "" {
						fid := m.failedSessionID
						m.failedSessionID = ""
						return m, func() tea.Msg {
							return FollowUpFailedSessionMsg{TaskID: fid, Feedback: text}
						}
					}
					if m.completedSessionID != "" {
						cid := m.completedSessionID
						m.completedSessionID = ""
						return m, func() tea.Msg {
							return FollowUpSessionMsg{TaskID: cid, Feedback: text}
						}
					}
					sid := m.sessionID
					return m, func() tea.Msg {
						return SteerSessionMsg{SessionID: sid, Message: text}
					}
				}
			case "esc":
				if m.steerInput.Value() != "" {
					m.steerInput.SetValue("")
				} else {
					m.steerActive = false
					m.steerInput.Blur()
					m.syncViewportSize()
				}
			default:
				m.steerInput, cmd = m.steerInput.Update(msg)
			}
			return m, cmd
		}
		switch msg.String() {
		case "p":
			if m.live || m.failedSessionID != "" || m.completedSessionID != "" {
				m.steerActive = true
				m.steerInput.Focus()
				m.syncViewportSize()
				return m, m.steerInput.Focus()
			}
		case "f":
			m.viewport.GotoBottom()
		case "g":
			m.viewport.GotoTop()
		case "v":
			m.verbose = !m.verbose
			m.doRebuildTranscript()
		case "t":
			m.collapseThinking = !m.collapseThinking
			m.doRebuildTranscript()
		default:
			m.viewport, cmd = m.viewport.Update(msg)
		}
	case sessionLogSpinnerTickMsg:
		if !m.agentActive {
			return m, nil
		}
		m.spinnerFrame = (m.spinnerFrame + 1) % len(sessionLogSpinnerFrames)
		// Surface a warning after prolonged silence while agent is active.
		// The warning replaces the divider row in the header, so no viewport
		// resize is needed when the state changes.
		shouldWarn := !m.lastEventAt.IsZero() && time.Since(m.lastEventAt) >= sessionLogSilenceThreshold
		m.silenceNoticeActive = shouldWarn
		return m, sessionLogSpinnerTickCmd()
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
	// When rate-limit silence warning is active, render it in-place on the
	// divider row so the header line count stays constant and the viewport
	// content below does not shift down.
	var statusLine string
	if m.silenceNoticeActive && !m.lastEventAt.IsZero() {
		elapsed := time.Since(m.lastEventAt).Round(time.Second)
		text := "⏸ No output for " + elapsed.String() + " — may be rate limited"
		statusLine = m.styles.Warning.Render(ansi.Truncate(text, m.width, "…"))
	}
	header := components.RenderHeaderBlock(m.styles, components.HeaderBlockSpec{
		Title:      headerText,
		Meta:       m.meta,
		Width:      m.width,
		Divider:    true,
		StatusLine: statusLine,
	})
	out := header
	if notice := m.noticeView(); notice != "" {
		out = out + "\n" + notice
	}
	return out
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
		if m.agentActive {
			body = m.styles.Muted.Render("Waiting for agent output...")
		} else {
			body = m.styles.Muted.Render("No session output captured.")
		}
	}
	if m.agentActive {
		body = overlaySpinner(body, sessionLogSpinnerFrames[m.spinnerFrame]+" working", m.styles, m.width)
	}
	parts := append(strings.Split(header, "\n"), body)
	if m.steerActive {
		parts = append(parts, components.RenderDivider(m.styles, m.width), m.steerInput.View())
	}

	return fitViewBox(strings.Join(parts, "\n"), m.width, m.height)
}

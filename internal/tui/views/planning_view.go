package views

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/beeemT/substrate/internal/sessionlog"
	"github.com/beeemT/substrate/internal/tui/components"
	"github.com/beeemT/substrate/internal/tui/styles"
)

const (
	sessionLogSpinnerInterval  = 100 * time.Millisecond
	sessionLogSilenceThreshold = 3 * time.Minute

	sessionLogPlaceholderDefault   = "Send steering prompt to agent..."
	sessionLogPlaceholderFailed    = "Send follow-up to restart failed session..."
	sessionLogPlaceholderCompleted = "Send follow-up to completed session..."
)

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

	// Plan inspection overlay for planning sessions.
	planID       string
	planOverlay  bool
	planDocument string
	planViewport viewport.Model

	// Cached header: avoids re-rendering header on every View() frame.
	// Invalidated when any header input changes (title, modeLabel, meta,
	// width, notice, silenceNoticeActive, lastEventAt).
	cachedHeader    string
	cachedHeaderKey string
}

func NewSessionLogModel(st styles.Styles) SessionLogModel {
	vp := viewport.New(0, 0)

	ti := components.NewTextInput()
	ti.Placeholder = sessionLogPlaceholderDefault
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

func (m *SessionLogModel) SetPlanID(id string) {
	if m.planID == id {
		return
	}
	m.planID = id
	m.planOverlay = false
	m.planDocument = ""
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
		m.steerInput.Placeholder = sessionLogPlaceholderFailed
	} else {
		m.steerInput.Placeholder = sessionLogPlaceholderDefault
	}
}

func (m *SessionLogModel) ClearFailedSession() {
	m.failedSessionID = ""
	m.steerInput.Placeholder = sessionLogPlaceholderDefault
}

func (m *SessionLogModel) SetCompletedSession(sessionID string) {
	m.completedSessionID = sessionID
	if sessionID != "" {
		m.steerInput.Placeholder = sessionLogPlaceholderCompleted
	} else {
		m.steerInput.Placeholder = sessionLogPlaceholderDefault
	}
}

func (m *SessionLogModel) ClearCompletedSession() {
	m.completedSessionID = ""
	m.steerInput.Placeholder = sessionLogPlaceholderDefault
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
	if m.planOverlay {
		return []KeybindHint{
			{Key: "esc", Label: "Close"},
			{Key: "↑↓", Label: "Scroll"},
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
	if m.planID != "" {
		hints = append(hints, KeybindHint{Key: "i", Label: "Inspect plan"})
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
		if m.planOverlay {
			switch msg.String() {
			case "esc", "i", "q":
				m.planOverlay = false
				return m, nil
			default:
				m.planViewport, cmd = m.planViewport.Update(msg)
				return m, cmd
			}
		}
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
		case "i":
			if m.planID != "" && !m.planOverlay {
				m.planOverlay = true
				if m.planDocument != "" {
					// Already loaded; just show.
					return m, nil
				}
				return m, func() tea.Msg { return InspectPlanMsg{PlanID: m.planID} }
			}
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
	case InspectPlanLoadedMsg:
		if msg.PlanID != m.planID {
			return m, nil
		}
		if msg.Err != nil {
			// Close the overlay and surface the error as a toast.
			m.planOverlay = false
			return m, func() tea.Msg { return ErrMsg{Err: msg.Err} }
		}
		if msg.Document != "" {
			m.planDocument = msg.Document
			fw := m.styles.Chrome.OverlayFrame
			m.planViewport.Width = fw.InnerWidth(m.width)
			m.planViewport.Height = max(1, m.height-fw.VerticalFrame()-2) // header + footer
			m.planViewport.SetContent(msg.Document)
			m.planViewport.GotoTop()
		} else {
			m.planOverlay = false
		}
		return m, nil
	case tea.MouseMsg:
		// Only forward press events; ignore motion and release.
		if msg.Action != tea.MouseActionPress {
			return m, nil
		}
		if m.planOverlay {
			m.planViewport, cmd = m.planViewport.Update(msg)
			return m, cmd
		}
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	default:
		m.viewport, cmd = m.viewport.Update(msg)
	}

	return m, cmd
}

func (m SessionLogModel) header() string {
	// For static content (no silence warning), the header is deterministic
	// based on title, modeLabel, meta, width, and notice. Cache it to avoid
	// re-rendering on every View() frame during scrolling.
	var key string
	if !m.silenceNoticeActive {
		key = m.title + "|" + m.modeLabel + "|" + m.meta + "|" + fmt.Sprintf("%d", m.width)
		if m.notice != nil {
			key += fmt.Sprintf("|%p", m.notice)
		}
		if m.cachedHeaderKey == key {
			return m.cachedHeader
		}
	}

	headerText := m.title
	if m.modeLabel != "" {
		headerText += " · " + m.modeLabel
	}
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
	if !m.silenceNoticeActive {
		m.cachedHeader = out
		m.cachedHeaderKey = key
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

	if m.planOverlay && m.planDocument != "" {
		headerLine := m.styles.Muted.Render("Plan (read-only)")
		footerLine := m.styles.Muted.Render("esc close  ↑↓ scroll")
		body := m.planViewport.View()
		frameWidth := m.width
		innerHeight := max(1, m.height-m.styles.Chrome.OverlayFrame.VerticalFrame()-2) // header + footer
		body = fitViewHeight(body, innerHeight)
		frame := components.RenderOverlayFrame(m.styles, frameWidth, components.OverlayFrameSpec{
			HeaderLines: []string{headerLine},
			Body:        body,
			Footer:      footerLine,
			Focused:     true,
		})
		return fitViewBox(frame, m.width, m.height)
	}

	header := m.header()
	body := m.viewport.View()
	if strings.TrimSpace(body) == "" {
		if m.agentActive {
			// Pad body to the full viewport height so overlaySpinner places
			// the spinner at the bottom-right corner, not beside the message.
			rows := make([]string, max(1, m.viewport.Height))
			rows[0] = m.styles.Muted.Render("Waiting for agent output...")
			body = strings.Join(rows, "\n")
		} else {
			body = m.styles.Muted.Render("No session output captured.")
		}
	}
	if m.agentActive {
		body = overlaySpinner(body, sessionLogSpinnerFrames[m.spinnerFrame]+" working", m.styles, m.width)
	}

	// Apply width fitting only to the header; the viewport already constrains
	// its content to viewport dimensions via lipgloss Width/Height/MaxWidth.
	// This avoids re-processing the viewport body through fitViewBox on every
	// scroll frame, which was the dominant per-frame cost for large sessions.
	headerLines := strings.Split(header, "\n")
	fittedHeader := make([]string, 0, len(headerLines))
	lineStyle := lipgloss.NewStyle().Width(m.width)
	for _, line := range headerLines {
		if ansi.StringWidth(line) <= m.width {
			fittedHeader = append(fittedHeader, line)
		} else {
			fittedHeader = append(fittedHeader, lineStyle.Render(ansi.Truncate(line, m.width, "")))
		}
	}

	parts := append(fittedHeader, body)
	if m.steerActive {
		parts = append(parts, components.RenderDivider(m.styles, m.width), m.steerInput.View())
	}

	return fitViewHeight(strings.Join(parts, "\n"), m.height)
}

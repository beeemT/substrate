package views

import (
	"fmt"
	"math/rand"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/sessionlog"
	"github.com/beeemT/substrate/internal/tui/components"
	"github.com/beeemT/substrate/internal/tui/styles"
	tea "github.com/charmbracelet/bubbletea"
)

// SessionStats holds aggregate session counts for the empty state display.
type SessionStats struct {
	TotalSessions int
	ActionNeeded  int // sessions awaiting human action (plan review, questions, interrupted)
}

// ContentMode determines which view is rendered in the content panel.
type ContentMode int

const (
	ContentModeEmpty              ContentMode = iota // no session selected
	ContentModeOverview                              // canonical root-session overview/control surface
	ContentModeSourceDetails                         // task-pane source metadata for the selected work item
	ContentModePlanning                              // planning/task session log tailing
	ContentModeSessionInteraction                    // historical or task session interaction view
)

// KeybindHint is a label/key pair rendered by the status bar.
type KeybindHint struct {
	Key   string
	Label string
}

// ContentModel holds all content panel sub-models and routes to the active one.
type ContentModel struct { //nolint:recvcheck // Bubble Tea convention
	mode   ContentMode
	styles styles.Styles
	width  int
	height int

	// Per-mode sub-models
	overview      SessionOverviewModel
	sourceDetails SourceDetailsModel
	sessionLog    SessionLogModel

	// Current work item being displayed
	currentWorkItem *domain.Session

	sessionStats SessionStats

	// Bunny blink animation (empty state only).
	blinkPhase      int                  // 0 = eyes open, 1 = eyes closed
	blinkActive     bool                 // tick chain is running
	blinkNeedsStart bool                 // start tick chain on next Update
	blinkSide       components.BunnySide // left or right corner; chosen at construction

	// Bunny hop animation (empty state only).
	hopActive bool // hop sequence is in progress
	hopCount  int  // total individual hops (2 or 3)
	hopIndex  int  // current hop number (0..hopCount-1)
	hopFrame  int  // frame within current hop (0..FramesPerHop-1)
	hopPause  bool // between-hop pause on the box
}

func NewContentModel(st styles.Styles) ContentModel {
	return ContentModel{
		mode:            ContentModeEmpty,
		styles:          st,
		overview:        NewSessionOverviewModel(st),
		sourceDetails:   NewSourceDetailsModel(st),
		sessionLog:      NewSessionLogModel(st),
		blinkNeedsStart: true,
		blinkSide:       components.BunnySide(rand.Intn(2)),
	}
}

func (m *ContentModel) SetSize(width, height int) {
	m.width = width
	m.height = height
	m.overview.SetSize(width, height)
	m.sourceDetails.SetSize(width, height)
	m.sessionLog.SetSize(width, height)
}

func (m *ContentModel) SetTerminalSize(w, h int) {
	m.overview.SetTerminalSize(w, h)
}

func (m *ContentModel) SetMode(mode ContentMode) {
	if m.mode == mode {
		m.mode = mode
		return
	}
	prev := m.mode
	m.mode = mode

	// Manage blink animation lifecycle with empty-mode transitions.
	if mode == ContentModeEmpty && !m.blinkActive {
		m.blinkPhase = 0
		m.blinkNeedsStart = true
	}
	if mode != ContentModeEmpty {
		m.blinkActive = false
		m.blinkNeedsStart = false
		m.hopActive = false
		m.hopCount = 0
		m.hopIndex = 0
		m.hopFrame = 0
		m.hopPause = false
	}

	// When leaving planning/session-interaction, kill the spinner tick chain
	// so that re-entering restarts it cleanly via SetAgentActive(true).
	if prev == ContentModePlanning || prev == ContentModeSessionInteraction {
		m.sessionLog.SetAgentActive(false)
	}
}
func (m ContentModel) Mode() ContentMode { return m.mode }

func (m *ContentModel) SetWorkItem(wi *domain.Session) {
	m.currentWorkItem = wi
	if wi != nil {
		m.sessionLog.SetTitle(wi.Title)
		m.sourceDetails.SetSession(wi)
	}
}

// SetSessionStats updates the aggregate session counts shown in the empty state.
func (m *ContentModel) SetSessionStats(stats SessionStats) {
	m.sessionStats = stats
}

func (m *ContentModel) SetOverviewData(data SessionOverviewData) {
	m.overview.SetData(data)
}

func (m *ContentModel) UpdateQuestionProposal(q domain.Question, proposed string, uncertain bool) {
	m.overview.question.SetQuestion(q, proposed, uncertain)
}

func (m *ContentModel) SetSessionInteraction(title, meta string, entries []sessionlog.Entry) {
	m.currentWorkItem = nil
	m.sessionLog.SetTitle(title)
	m.sessionLog.SetModeLabel("Session interaction")
	m.sessionLog.SetMeta(meta)
	m.sessionLog.SetStaticContent(entries)
	m.mode = ContentModeSessionInteraction
}

func (m ContentModel) Update(msg tea.Msg) (ContentModel, tea.Cmd) {
	var cmds []tea.Cmd
	var cmd tea.Cmd

	// Start blink tick chain on first Update after entering empty mode.
	if m.blinkNeedsStart && m.mode == ContentModeEmpty {
		m.blinkNeedsStart = false
		m.blinkActive = true
		cmds = append(cmds, components.BunnyOpenCmd())
		cmds = append(cmds, components.BunnyHopIdleCmd())
	}

	if msg, ok := msg.(components.BunnyBlinkMsg); ok {
		if m.mode == ContentModeEmpty && m.blinkActive {
			m.blinkPhase = msg.Phase
			if msg.Phase == 1 {
				cmds = append(cmds, components.BunnyCloseCmd())
			} else {
				cmds = append(cmds, components.BunnyOpenCmd())
			}
		}
		return m, tea.Batch(cmds...)
	}

	if msg, ok := msg.(components.BunnyHopTriggerMsg); ok {
		if m.mode == ContentModeEmpty && m.blinkActive && !m.hopActive {
			m.hopActive = true
			m.hopCount = msg.Hops
			m.hopIndex = 0
			m.hopFrame = 0
			m.hopPause = false
			cmds = append(cmds, components.BunnyHopTick(components.HopFrameDuration(0)))
		}
		// Re-arm idle timer for the next hop regardless of whether this one started.
		cmds = append(cmds, components.BunnyHopIdleCmd())
		return m, tea.Batch(cmds...)
	}

	if _, ok := msg.(components.BunnyHopStepMsg); ok {
		if m.mode == ContentModeEmpty && m.hopActive {
			if m.hopPause {
				// Between-hop pause ended: start next hop or finish sequence.
				m.hopPause = false
				m.hopIndex++
				if m.hopIndex >= m.hopCount {
					// Final landing: flip to opposite corner, end sequence.
					m.blinkSide = 1 - m.blinkSide
					m.hopActive = false
					m.hopFrame = 0
				} else {
					// Start next hop from crouch frame.
					m.hopFrame = 0
					cmds = append(cmds, components.BunnyHopTick(components.HopFrameDuration(0)))
				}
			} else {
				m.hopFrame++
				if m.hopFrame >= components.FramesPerHop {
					// Hop complete: land on box.
					m.hopFrame = components.FramesPerHop - 1
					if m.hopIndex < m.hopCount-1 {
						// More hops to go: pause on the box.
						m.hopPause = true
						cmds = append(cmds, components.BunnyHopPauseTick())
					} else {
						// Final hop complete: flip side and end.
						m.blinkSide = 1 - m.blinkSide
						m.hopActive = false
						m.hopFrame = 0
					}
				} else {
					cmds = append(cmds, components.BunnyHopTick(components.HopFrameDuration(m.hopFrame)))
				}
			}
		}
		return m, tea.Batch(cmds...)
	}
	switch m.mode {
	case ContentModeOverview:
		m.overview, cmd = m.overview.Update(msg)
		cmds = append(cmds, cmd)
	case ContentModeSourceDetails:
		m.sourceDetails, cmd = m.sourceDetails.Update(msg)
		cmds = append(cmds, cmd)
	case ContentModePlanning, ContentModeSessionInteraction:
		m.sessionLog, cmd = m.sessionLog.Update(msg)
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

func (m ContentModel) View() string {
	switch m.mode {
	case ContentModeEmpty:
		return m.emptyStateView()
	case ContentModeOverview:
		return m.overview.View()
	case ContentModeSourceDetails:
		return m.sourceDetails.View()
	case ContentModePlanning, ContentModeSessionInteraction:
		return m.sessionLog.View()
	default:
		return ""
	}
}

func (m ContentModel) emptyStateView() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}

	panelWidth := min(max(1, m.width-4), 80)
	detailWidth := max(1, panelWidth-4)

	var parts []string
	if m.sessionStats.TotalSessions == 0 {
		// No sessions yet
		title := m.styles.Title.Render("No sessions yet")
		prompt := m.styles.Subtitle.Render("Press ") +
			m.styles.KeybindAccent.Render("[n]") +
			m.styles.Subtitle.Render(" to create your first session, or pick one from the sidebar.")
		detail := lipgloss.NewStyle().
			Foreground(lipgloss.Color(m.styles.Theme.Muted)).
			Width(detailWidth).
			Align(lipgloss.Left).
			Render("Once a session is running, this panel shows plans, agent progress, logs, review output, and searchable history.")

		parts = append(parts, title, "", prompt, "", detail)
	} else {
		// Sessions exist but none selected
		title := m.styles.Title.Render("Select a session")
		prompt := m.styles.Subtitle.Render("Use ") +
			m.styles.KeybindAccent.Render("[↑/↓]") +
			m.styles.Subtitle.Render(" to browse the sidebar, or press ") +
			m.styles.KeybindAccent.Render("[n]") +
			m.styles.Subtitle.Render(" for a new session.")

		summary := m.sessionStatsSummary(detailWidth)
		parts = append(parts, title, "", prompt, "", summary)
	}

	message := lipgloss.JoinVertical(lipgloss.Left, parts...)
	container := m.styles.Border.Padding(1, 2).Width(panelWidth).Render(message)
	// Only render bunny if there is room: 3 bunny lines + border (2) + at least 2 content lines.
	const minHeightForBunny = 7
	var placed string
	if m.height >= minHeightForBunny {
		if m.hopActive {
			containerWidth := lipgloss.Width(container)
			const bunnyWidth = 8
			startOffset := 0
			endOffset := containerWidth - bunnyWidth
			if m.blinkSide == components.BunnySideRight {
				startOffset, endOffset = endOffset, startOffset
			}
			// Calculate overall horizontal progress through the entire sequence.
			// Each hop moves the bunny by 1/hopCount of the total distance.
			// Within a hop, HopFrameProgress gives 0.0–1.0 for the current frame.
			hopFraction := float64(m.hopIndex)
			if !m.hopPause {
				hopFraction += components.HopFrameProgress(m.hopFrame)
			} else {
				// During pause, bunny sits at the landing position of the completed hop.
				hopFraction = float64(m.hopIndex + 1)
			}
			overallProgress := hopFraction / float64(m.hopCount)
			delta := endOffset - startOffset
			currentOffset := startOffset + int(float64(delta)*overallProgress)
			// Choose art based on frame: crouch(0) and land(4) use crouch pose,
			// rise(1), peak(2), fall(3) use airborne pose. During pause, use crouch.
			var art string
			if m.hopPause || m.hopFrame == 0 || m.hopFrame == components.FramesPerHop-1 {
				art = components.RenderBunnyCrouch(m.blinkPhase)
			} else {
				art = components.RenderBunnyHop(m.blinkPhase)
			}
			bunnyLines := strings.Split(art, "\n")
			for i, line := range bunnyLines {
				rPad := max(0, containerWidth-currentOffset-lipgloss.Width(line))
				bunnyLines[i] = strings.Repeat(" ", currentOffset) + line + strings.Repeat(" ", rPad)
			}
			// Vertical positioning: keep the container at the same row as the
			// stationary layout. Stationary places the container at row
			// (m.height - (3 + containerHeight)) / 2 + 3. For the hop, the bunny
			// plus gaps occupy 3 + gapCount rows, so we reduce top padding by
			// gapCount to keep the container pinned.
			containerLines := strings.Split(container, "\n")
			containerHeight := len(containerLines)
			gapCount := 0
			if !m.hopPause {
				gapCount = components.HopFrameGap(m.hopFrame)
			}
			stationaryTopPad := (m.height - (3 + containerHeight)) / 2
			if stationaryTopPad < 0 {
				stationaryTopPad = 0
			}
			hopTopPad := stationaryTopPad - gapCount
			if hopTopPad < 0 {
				hopTopPad = 0
			}
			// Horizontal centering: match lipgloss.Place(Center) behaviour.
			hPad := (m.width - containerWidth) / 2
			if hPad < 0 {
				hPad = 0
			}
			hPadStr := strings.Repeat(" ", hPad)
			var out []string
			for i := 0; i < hopTopPad; i++ {
				out = append(out, "")
			}
			for _, line := range bunnyLines {
				out = append(out, hPadStr+line)
			}
			for i := 0; i < gapCount; i++ {
				out = append(out, "")
			}
			for _, cLine := range containerLines {
				cw := lipgloss.Width(cLine)
				cpad := max(0, (m.width-cw)/2)
				out = append(out, strings.Repeat(" ", cpad)+cLine)
			}
			for len(out) < m.height {
				out = append(out, "")
			}
			placed = strings.Join(out, "\n")
		} else {
			bunny := components.RenderBunny(m.blinkPhase, m.blinkSide)
			var align lipgloss.Position
			if m.blinkSide == components.BunnySideLeft {
				align = lipgloss.Left
			} else {
				align = lipgloss.Right
			}
			combined := lipgloss.JoinVertical(align, bunny, container)
			placed = lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, combined)
		}
	} else {
		placed = lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, container)
	}
	return fitViewHeight(placed, m.height)
}

// sessionStatsSummary renders session count info for the empty state.
func (m ContentModel) sessionStatsSummary(width int) string {
	stats := m.sessionStats
	var line string
	if stats.ActionNeeded > 0 {
		line = fmt.Sprintf("%d session%s  ·  %d awaiting action",
			stats.TotalSessions, pluralS(stats.TotalSessions),
			stats.ActionNeeded)
	} else {
		line = fmt.Sprintf("%d session%s", stats.TotalSessions, pluralS(stats.TotalSessions))
	}

	return lipgloss.NewStyle().
		Foreground(lipgloss.Color(m.styles.Theme.Muted)).
		Width(width).
		Align(lipgloss.Left).
		Render(line)
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// KeybindHints returns keybind hints for the active mode (passed to the status bar).
func (m ContentModel) KeybindHints() []KeybindHint {
	switch m.mode {
	case ContentModeOverview:
		return m.overview.KeybindHints()
	case ContentModeSourceDetails:
		return m.sourceDetails.KeybindHints()
	case ContentModePlanning, ContentModeSessionInteraction:
		return m.sessionLog.KeybindHints()
	default:
		return nil
	}
}

func (m ContentModel) InputCaptured() bool {
	switch m.mode {
	case ContentModePlanning, ContentModeSessionInteraction:
		return m.sessionLog.InputCaptured()
	default:
		return false
	}
}

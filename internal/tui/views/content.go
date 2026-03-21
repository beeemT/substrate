package views

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/sessionlog"
	"github.com/beeemT/substrate/internal/tui/styles"
	tea "github.com/charmbracelet/bubbletea"
)

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
}

func NewContentModel(st styles.Styles) ContentModel {
	return ContentModel{
		mode:          ContentModeEmpty,
		styles:        st,
		overview:      NewSessionOverviewModel(st),
		sourceDetails: NewSourceDetailsModel(st),
		sessionLog:    NewSessionLogModel(st),
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

func (m *ContentModel) SetMode(mode ContentMode) { m.mode = mode }
func (m ContentModel) Mode() ContentMode         { return m.mode }

func (m *ContentModel) SetWorkItem(wi *domain.Session) {
	m.currentWorkItem = wi
	if wi != nil {
		m.sessionLog.SetTitle(wi.Title)
		m.sourceDetails.SetSession(wi)
	}
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

	title := m.styles.Title.Render("No sessions yet")
	prompt := m.styles.Subtitle.Render("Press ") +
		m.styles.KeybindAccent.Render("[n]") +
		m.styles.Subtitle.Render(" to create your first session, or pick one from the sidebar.")
	detail := lipgloss.NewStyle().
		Foreground(lipgloss.Color(m.styles.Theme.Muted)).
		Width(detailWidth).
		Align(lipgloss.Left).
		Render("Once a session is running, this panel shows plans, agent progress, logs, review output, and searchable history.")

	message := lipgloss.JoinVertical(lipgloss.Left, title, "", prompt, "", detail)

	container := m.styles.Border.Padding(1, 2).Width(panelWidth).Render(message)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, container)
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

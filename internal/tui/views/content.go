package views

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/tui/styles"
	tea "github.com/charmbracelet/bubbletea"
)

// ContentMode determines which view is rendered in the content panel.
type ContentMode int

const (
	ContentModeEmpty              ContentMode = iota // no session selected
	ContentModeReadyToPlan                           // ingested: work item ready for planning
	ContentModeSourceDetails                         // task-pane source metadata for the selected work item
	ContentModePlanning                              // planning: agent running, log tailing
	ContentModeSessionInteraction                    // historical session interaction view
	ContentModePlanReview                            // plan_review: awaiting human review
	ContentModeAwaitingImpl                          // approved: plan approved, awaiting impl start
	ContentModeImplementing                          // implementing: agents running
	ContentModeReviewing                             // reviewing: review agent running
	ContentModeCompleted                             // completed: all repos passed review
	ContentModeFailed                                // failed: unrecoverable error
	ContentModeInterrupted                           // sub-mode: session interrupted
	ContentModeQuestion                              // sub-mode: waiting for human answer
)

// KeybindHint is a label/key pair rendered by the status bar.
type KeybindHint struct {
	Key   string
	Label string
}

// ContentModel holds all content panel sub-models and routes to the active one.
type ContentModel struct {
	mode   ContentMode
	styles styles.Styles
	width  int
	height int

	// Per-mode sub-models
	readyToPlan   ReadyToPlanModel
	sourceDetails SourceDetailsModel
	sessionLog    SessionLogModel
	planReview    PlanReviewModel
	awaitingImpl  AwaitingImplModel
	implementing  ImplementingModel
	reviewing     ReviewModel
	completed     CompletedModel
	failed        FailedModel
	interrupted   InterruptedModel
	question      QuestionModel

	// Current work item being displayed
	currentWorkItem *domain.WorkItem
}

func NewContentModel(st styles.Styles) ContentModel {
	return ContentModel{
		mode:          ContentModeEmpty,
		styles:        st,
		readyToPlan:   NewReadyToPlanModel(st),
		sourceDetails: NewSourceDetailsModel(st),
		sessionLog:    NewSessionLogModel(st),
		planReview:    NewPlanReviewModel(st),
		awaitingImpl:  NewAwaitingImplModel(st),
		implementing:  NewImplementingModel(st),
		reviewing:     NewReviewModel(st),
		completed:     NewCompletedModel(st),
		failed:        NewFailedModel(st),
		interrupted:   NewInterruptedModel(st),
		question:      NewQuestionModel(st),
	}
}

func (m *ContentModel) SetSize(width, height int) {
	m.width = width
	m.height = height
	m.readyToPlan.SetSize(width, height)
	m.sourceDetails.SetSize(width, height)
	m.sessionLog.SetSize(width, height)
	m.planReview.SetSize(width, height)
	m.awaitingImpl.SetSize(width, height)
	m.implementing.SetSize(width, height)
	m.reviewing.SetSize(width, height)
	m.completed.SetSize(width, height)
	m.failed.SetSize(width, height)
	m.interrupted.SetSize(width, height)
	m.question.SetSize(width, height)
}

func (m *ContentModel) SetMode(mode ContentMode) { m.mode = mode }
func (m ContentModel) Mode() ContentMode         { return m.mode }

func (m *ContentModel) SetWorkItem(wi *domain.WorkItem) {
	m.currentWorkItem = wi
	if wi != nil {
		title := wi.ExternalID + " · " + wi.Title
		m.planReview.SetTitle(title)
		m.implementing.SetTitle(title)
		m.reviewing.SetTitle(title)
		m.completed.SetTitle(title)
		m.failed.SetTitle(title)
		m.interrupted.SetTitle(title)
		m.question.SetTitle(title)
		m.sessionLog.SetTitle(title)
		m.readyToPlan.SetWorkItem(wi)
		m.sourceDetails.SetWorkItem(wi)
		m.awaitingImpl.SetWorkItem(wi)
	}
}

func (m *ContentModel) SetSessionInteraction(title, meta string, lines []string) {
	m.currentWorkItem = nil
	m.sessionLog.SetTitle(title)
	m.sessionLog.SetModeLabel("Session interaction")
	m.sessionLog.SetMeta(meta)
	m.sessionLog.SetStaticContent(lines)
	m.mode = ContentModeSessionInteraction
}

func (m ContentModel) Update(msg tea.Msg) (ContentModel, tea.Cmd) {
	var cmds []tea.Cmd
	var cmd tea.Cmd

	switch m.mode {
	case ContentModeReadyToPlan:
		m.readyToPlan, cmd = m.readyToPlan.Update(msg)
		cmds = append(cmds, cmd)
	case ContentModeSourceDetails:
		m.sourceDetails, cmd = m.sourceDetails.Update(msg)
		cmds = append(cmds, cmd)
	case ContentModePlanning, ContentModeSessionInteraction:
		m.sessionLog, cmd = m.sessionLog.Update(msg)
		cmds = append(cmds, cmd)
	case ContentModePlanReview:
		m.planReview, cmd = m.planReview.Update(msg)
		cmds = append(cmds, cmd)
	case ContentModeImplementing:
		m.implementing, cmd = m.implementing.Update(msg)
		cmds = append(cmds, cmd)
	case ContentModeQuestion:
		m.question, cmd = m.question.Update(msg)
		cmds = append(cmds, cmd)
	case ContentModeReviewing:
		m.reviewing, cmd = m.reviewing.Update(msg)
		cmds = append(cmds, cmd)
	case ContentModeCompleted:
		m.completed, cmd = m.completed.Update(msg)
		cmds = append(cmds, cmd)
	case ContentModeFailed:
		m.failed, cmd = m.failed.Update(msg)
		cmds = append(cmds, cmd)
	case ContentModeInterrupted:
		m.interrupted, cmd = m.interrupted.Update(msg)
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

func (m ContentModel) View() string {
	switch m.mode {
	case ContentModeEmpty:
		return m.emptyStateView()
	case ContentModeReadyToPlan:
		return m.readyToPlan.View()
	case ContentModeSourceDetails:
		return m.sourceDetails.View()
	case ContentModePlanning, ContentModeSessionInteraction:
		return m.sessionLog.View()
	case ContentModePlanReview:
		return m.planReview.View()
	case ContentModeAwaitingImpl:
		return m.awaitingImpl.View()
	case ContentModeImplementing:
		return m.implementing.View()
	case ContentModeReviewing:
		return m.reviewing.View()
	case ContentModeCompleted:
		return m.completed.View()
	case ContentModeFailed:
		return m.failed.View()
	case ContentModeInterrupted:
		return m.interrupted.View()
	case ContentModeQuestion:
		return m.question.View()
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

	container := m.styles.Border.Copy().
		Padding(1, 2).
		Width(panelWidth).
		Render(message)

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, container)
}

// KeybindHints returns keybind hints for the active mode (passed to the status bar).
func (m ContentModel) KeybindHints() []KeybindHint {
	switch m.mode {
	case ContentModeSourceDetails:
		return m.sourceDetails.KeybindHints()
	case ContentModePlanning, ContentModeSessionInteraction:
		return m.sessionLog.KeybindHints()
	case ContentModePlanReview:
		return m.planReview.KeybindHints()
	case ContentModeImplementing:
		return m.implementing.KeybindHints()
	case ContentModeQuestion:
		return m.question.KeybindHints()
	case ContentModeReviewing:
		return m.reviewing.KeybindHints()
	case ContentModeInterrupted:
		return m.interrupted.KeybindHints()
	default:
		return nil
	}
}

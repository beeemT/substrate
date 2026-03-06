package views

import (
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/tui/styles"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

// ContentMode determines which view is rendered in the content panel.
type ContentMode int

const (
	ContentModeEmpty        ContentMode = iota // no session selected
	ContentModeReadyToPlan                     // ingested: work item ready for planning
	ContentModePlanning                        // planning: agent running, log tailing
	ContentModePlanReview                      // plan_review: awaiting human review
	ContentModeAwaitingImpl                    // approved: plan approved, awaiting impl start
	ContentModeImplementing                    // implementing: agents running
	ContentModeReviewing                       // reviewing: review agent running
	ContentModeCompleted                       // completed: all repos passed review
	ContentModeFailed                          // failed: unrecoverable error
	ContentModeInterrupted                     // sub-mode: session interrupted
	ContentModeQuestion                        // sub-mode: waiting for human answer
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
	emptyView    viewport.Model
	readyToPlan  ReadyToPlanModel
	planOutput   PlanningViewModel
	planReview   PlanReviewModel
	awaitingImpl AwaitingImplModel
	implementing ImplementingModel
	reviewing    ReviewModel
	completed    CompletedModel
	failed       FailedModel
	interrupted  InterruptedModel
	question     QuestionModel

	// Current work item being displayed
	currentWorkItem *domain.WorkItem
}

func NewContentModel(st styles.Styles) ContentModel {
	return ContentModel{
		mode:         ContentModeEmpty,
		styles:       st,
		emptyView:    viewport.New(0, 0),
		readyToPlan:  NewReadyToPlanModel(st),
		planOutput:   NewPlanningViewModel(st),
		planReview:   NewPlanReviewModel(st),
		awaitingImpl: NewAwaitingImplModel(st),
		implementing: NewImplementingModel(st),
		reviewing:    NewReviewModel(st),
		completed:    NewCompletedModel(st),
		failed:       NewFailedModel(st),
		interrupted:  NewInterruptedModel(st),
		question:     NewQuestionModel(st),
	}
}

func (m *ContentModel) SetSize(width, height int) {
	m.width = width
	m.height = height
	m.readyToPlan.SetSize(width, height)
	m.planOutput.SetSize(width, height)
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
		m.planOutput.SetTitle(title)
		m.readyToPlan.SetWorkItem(wi)
		m.awaitingImpl.SetWorkItem(wi)
	}
}

func (m ContentModel) Update(msg tea.Msg) (ContentModel, tea.Cmd) {
	var cmds []tea.Cmd
	var cmd tea.Cmd

	switch m.mode {
	case ContentModeReadyToPlan:
		m.readyToPlan, cmd = m.readyToPlan.Update(msg)
		cmds = append(cmds, cmd)
	case ContentModePlanning:
		m.planOutput, cmd = m.planOutput.Update(msg)
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
		return m.emptyView.View()
	case ContentModeReadyToPlan:
		return m.readyToPlan.View()
	case ContentModePlanning:
		return m.planOutput.View()
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

// KeybindHints returns keybind hints for the active mode (passed to status bar).
func (m ContentModel) KeybindHints() []KeybindHint {
	switch m.mode {
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

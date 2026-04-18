package views

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/tui/components"
	"github.com/beeemT/substrate/internal/tui/styles"
)

type OverviewActionKind string

const (
	overviewActionPlanReview  OverviewActionKind = "plan_review"
	overviewActionQuestion    OverviewActionKind = "question"
	overviewActionInterrupted OverviewActionKind = "interrupted"
	overviewActionReviewing   OverviewActionKind = "reviewing"
	overviewActionFailed      OverviewActionKind = "failed"
	overviewActionCompleted   OverviewActionKind = "completed"

	providerGitlab       = "gitlab"
	labelReviewArtifacts = "Review artifacts"
)

type overviewOverlayKind int

const (
	overviewOverlayNone overviewOverlayKind = iota
	overviewOverlayPlan
	overviewOverlayQuestion
	overviewOverlayInterrupted
	overviewOverlayCompleted
	overviewOverlayReviewing
)

type SessionOverviewData struct {
	WorkItemID string
	State      domain.SessionState
	Header     OverviewHeader
	Actions    []OverviewActionCard
	Sources    []OverviewSourceItem
	Plan       OverviewPlan
	Tasks      []OverviewTaskRow
	External   OverviewExternalLifecycle
	Activity   []OverviewActivityItem
}

type OverviewHeader struct {
	ExternalID   string
	Title        string
	StatusLabel  string
	UpdatedAt    time.Time
	ProgressText string
	Badges       []string
}

type OverviewActionCard struct {
	Kind           OverviewActionKind
	Title          string
	Blocked        string
	Why            string
	Affected       []string
	Context        []string
	Plan           *domain.Plan
	Question       *domain.Question
	QuestionRepo   string
	QuestionTask   string
	QuestionAsked  time.Time
	ProposedAnswer string
	Session        *domain.Task
	ReviewRepos    []RepoReviewResult
	CanAct         bool
}

type OverviewSourceItem struct {
	Provider string
	Ref      string
	Title    string
	Excerpt  string
	URL      string
}

type OverviewPlan struct {
	StateLabel     string
	Exists         bool
	Version        int
	UpdatedAt      time.Time
	RepoCount      int
	FAQCount       int
	Excerpt        []string
	ActionText     string
	DraftSession   string
	DraftUpdatedAt time.Time
	DraftPath      string
	Document       *domain.Plan
	FullDocument   string
}

type OverviewTaskRow struct {
	RepoName       string
	TaskPlanStatus string
	SessionTitle   string
	SessionStatus  string
	HarnessName    string
	UpdatedAt      time.Time
	Note           string
	SessionID      string
}

type OverviewReviewRow struct {
	Kind     string
	RepoName string
	Ref      string
	URL      string
	State    string
	Branch   string
}
type OverviewExternalLifecycle struct {
	TrackerRefs []string
	Reviews     []OverviewReviewRow
}

type ArtifactItem struct {
	Provider  string
	Kind      string // "PR" or "MR"
	RepoName  string
	Ref       string // "#42" or "!7"
	URL       string
	State     string // "draft" | "open" | "merged" | "closed"
	Branch    string
	Draft     bool
	MergedAt  *time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
}

type OverviewActivityItem struct {
	Summary   string
	Timestamp time.Time
}

type SessionOverviewModel struct { //nolint:recvcheck // Bubble Tea: Update returns value, View on value receiver
	styles         styles.Styles
	width          int
	height         int
	termWidth      int
	termHeight     int
	viewport       viewport.Model
	data           SessionOverviewData
	selectedAction int
	overlay        overviewOverlayKind
	planReview     PlanReviewModel
	question       QuestionModel
	interrupted    InterruptedModel
	completed      CompletedModel
	reviewing      ReviewModel
}

func NewSessionOverviewModel(st styles.Styles) SessionOverviewModel {
	return SessionOverviewModel{
		styles:      st,
		viewport:    viewport.New(0, 0),
		planReview:  NewPlanReviewModel(st),
		question:    NewQuestionModel(st),
		interrupted: NewInterruptedModel(st),
		completed:   NewCompletedModel(st),
		reviewing:   NewReviewModel(st),
	}
}

func (m *SessionOverviewModel) planOverlayInnerSize() (innerWidth, innerHeight int) {
	frameWidth := min(max(72, m.termWidth-12), 220)
	innerHeight = max(12, m.termHeight-2)
	innerWidth = m.styles.Chrome.OverlayFrame.InnerWidth(frameWidth)

	return
}

func (m *SessionOverviewModel) defaultOverlayInnerSize() (innerWidth, innerHeight int) {
	frameWidth := min(max(48, m.termWidth-6), 112)
	innerHeight = max(10, min(m.termHeight-4, 26))
	innerWidth = m.styles.Chrome.OverlayFrame.InnerWidth(frameWidth)

	return
}

func (m *SessionOverviewModel) SetTerminalSize(w, h int) {
	m.termWidth = w
	m.termHeight = h
	pw, ph := m.planOverlayInnerSize()
	m.planReview.SetSize(pw, ph)
	// completed also uses the plan-overlay size: it now shows the full plan in a
	// scrollable viewport and needs the wider/taller frame.
	m.completed.SetSize(pw, ph)
	dw, dh := m.defaultOverlayInnerSize()
	m.question.SetSize(dw, dh)
	m.interrupted.SetSize(dw, dh)
	m.reviewing.SetSize(dw, dh)
}

func (m *SessionOverviewModel) SetSize(width, height int) {
	m.width = width
	m.height = height
	m.syncViewport(false)
}

func (m *SessionOverviewModel) SetData(data SessionOverviewData) {
	resetViewport := m.data.WorkItemID != data.WorkItemID
	if resetViewport {
		m.selectedAction = 0
	}
	m.data = data
	if len(m.data.Actions) == 0 {
		m.selectedAction = 0
	} else if m.selectedAction >= len(m.data.Actions) {
		m.selectedAction = len(m.data.Actions) - 1
	}
	m.syncActionModels()
	// Skip the expensive overview document re-render when an overlay covers
	// the viewport. syncViewport runs when the overlay closes (line 269),
	// so the viewport catches up before it becomes visible again.
	if m.overlay == overviewOverlayNone || resetViewport {
		m.syncViewport(resetViewport)
	}
}

func (m SessionOverviewModel) KeybindHints() []KeybindHint {
	if m.overlay != overviewOverlayNone {
		switch m.overlay {
		case overviewOverlayPlan:
			return m.planReview.KeybindHints()
		case overviewOverlayQuestion:
			return m.question.KeybindHints()
		case overviewOverlayInterrupted:
			return m.interrupted.KeybindHints()
		case overviewOverlayCompleted:
			return m.completed.KeybindHints()
		}
	}
	hints := []KeybindHint{{Key: "↑↓", Label: "Scroll"}}
	if len(m.data.Actions) > 1 {
		hints = append(hints, KeybindHint{Key: "Tab", Label: "Next action"})
	}
	if action := m.selectedActionCard(); action != nil {
		hints = append(hints, actionKeybindHints(*action)...)
	} else if len(m.data.Sources) > 0 || len(m.data.External.Reviews) > 0 {
		hints = append(hints, KeybindHint{Key: "o", Label: "Links"})
	} else if m.data.State == domain.SessionIngested {
		hints = append(hints, KeybindHint{Key: "Enter", Label: "Start planning"})
	} else if m.data.Plan.Exists {
		hints = append(hints, KeybindHint{Key: "i", Label: "View full plan"})
	}

	return hints
}

func (m SessionOverviewModel) Update(msg tea.Msg) (SessionOverviewModel, tea.Cmd) {
	if m.overlay != overviewOverlayNone {
		if key, ok := msg.(tea.KeyMsg); ok && (key.String() == panelLeft || key.String() == keyEsc) {
			if key.String() == keyEsc {
				switch m.overlay {
				case overviewOverlayQuestion:
					var cmd tea.Cmd
					m.question, cmd = m.question.Update(msg)

					return m, cmd
				case overviewOverlayPlan:
					if m.planReview.inputMode != planReviewNormal {
						var cmd tea.Cmd
						m.planReview, cmd = m.planReview.Update(msg)

						return m, cmd
					}
				}
			}
			m.overlay = overviewOverlayNone
			m.syncViewport(false)

			// If the plan review had mouse reporting disabled (feedback
			// textarea was focused), restore it now that the overlay is closing.
			if m.planReview.inputMode != planReviewNormal {
				m.planReview.inputMode = planReviewNormal
				m.planReview.feedbackHeight = 1
				m.planReview.feedbackInput.SetHeight(1)
				m.planReview.feedbackInput.SetValue("")
				m.planReview.feedbackInput.Blur()
				return m, tea.EnableMouseCellMotion
			}

			return m, nil
		}
		switch m.overlay {
		case overviewOverlayPlan:
			if key, ok := msg.(tea.KeyMsg); ok && key.String() == keyEnter && m.planReview.inputMode != planReviewNormal {
				var cmd tea.Cmd
				m.planReview, cmd = m.planReview.Update(msg)
				m.overlay = overviewOverlayNone
				m.syncViewport(false)

				return m, cmd
			}
			var cmd tea.Cmd
			m.planReview, cmd = m.planReview.Update(msg)

			return m, cmd
		case overviewOverlayQuestion:
			var cmd tea.Cmd
			m.question, cmd = m.question.Update(msg)

			return m, cmd
		case overviewOverlayInterrupted:
			var cmd tea.Cmd
			m.interrupted, cmd = m.interrupted.Update(msg)

			return m, cmd
		case overviewOverlayCompleted:
			var cmd tea.Cmd
			m.completed, cmd = m.completed.Update(msg)

			return m, cmd
		case overviewOverlayReviewing:
			var cmd tea.Cmd
			m.reviewing, cmd = m.reviewing.Update(msg)

			return m, cmd
		}
	}

	var cmd tea.Cmd
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case keyTab:
			if len(m.data.Actions) > 1 {
				m.selectedAction = (m.selectedAction + 1) % len(m.data.Actions)
				m.syncActionModels()
				m.syncViewport(false)
			}

			return m, nil
		case keyEnter:
			if action := m.selectedActionCard(); action != nil {
				switch action.Kind {
				case overviewActionQuestion:
					m.overlay = overviewOverlayQuestion

					return m, nil
				case overviewActionFailed:
					if len(action.ReviewRepos) > 0 {
						m.overlay = overviewOverlayReviewing

						return m, nil
					}
				}
			} else if m.data.State == domain.SessionIngested {
				return m, func() tea.Msg { return StartPlanMsg{WorkItemID: m.data.WorkItemID} }
			}
		case "i":
			if action := m.selectedActionCard(); action != nil {
				switch action.Kind {
				case overviewActionPlanReview:
					m.openPlanOverlayForChanges()

					return m, nil
				case overviewActionQuestion:
					m.overlay = overviewOverlayQuestion

					return m, nil
				case overviewActionInterrupted:
					m.overlay = overviewOverlayInterrupted

					return m, nil
				case overviewActionReviewing:
					m.overlay = overviewOverlayReviewing

					return m, nil
				case overviewActionCompleted:
					m.overlay = overviewOverlayCompleted

					return m, nil
				case overviewActionFailed:
					if len(action.ReviewRepos) > 0 {
						m.overlay = overviewOverlayReviewing

						return m, nil
					}
				}
			} else if m.data.Plan.Exists && m.data.Plan.Document != nil {
				m.overlay = overviewOverlayPlan

				return m, nil
			}
		case "o":
			if action := m.selectedActionCard(); action != nil && action.Kind == overviewActionReviewing {
				return m, func() tea.Msg { return ConfirmOverrideAcceptMsg{WorkItemID: m.data.WorkItemID} }
			}
			if len(m.data.Sources) > 0 || len(m.data.External.Reviews) > 0 {
				srcs := m.data.Sources
				reviews := m.data.External.Reviews
				return m, func() tea.Msg { return OpenOverviewLinksMsg{Sources: srcs, Reviews: reviews} }
			}
		case "a":
			if action := m.selectedActionCard(); action != nil {
				switch action.Kind {
				case overviewActionPlanReview:
					if action.Plan != nil {
						return m, func() tea.Msg { return PlanApproveMsg{PlanID: action.Plan.ID, WorkItemID: m.data.WorkItemID} }
					}
				case overviewActionInterrupted:
					if action.Session != nil && action.CanAct {
						sessionID := action.Session.ID

						return m, func() tea.Msg { return ConfirmAbandonMsg{SessionID: sessionID} }
					}
				}
			}
		case "A":
			if action := m.selectedActionCard(); action != nil && action.Kind == overviewActionQuestion && action.Question != nil {
				answer := strings.TrimSpace(action.ProposedAnswer)
				if answer == "" {
					m.overlay = overviewOverlayQuestion

					return m, nil
				}
				questionID := action.Question.ID

				return m, func() tea.Msg { return AnswerQuestionMsg{QuestionID: questionID, Answer: answer, AnsweredBy: "human"} }
			}
		case "c":
			if action := m.selectedActionCard(); action != nil {
				switch action.Kind {
				case overviewActionPlanReview:
					m.openPlanOverlayForChanges()

					return m, nil
				case overviewActionCompleted:
					return m, m.openCompletedOverlayForChanges()
				}
			}
		case "r":
			if action := m.selectedActionCard(); action != nil {
				switch action.Kind {
				case overviewActionInterrupted:
					if action.Session != nil && action.CanAct {
						if action.Session.Phase == domain.TaskPhasePlanning {
							wID := action.Session.WorkItemID
							return m, func() tea.Msg { return RestartPlanMsg{WorkItemID: wID} }
						}
						oldSessionID := action.Session.ID
						subPlanID := action.Session.SubPlanID
						return m, func() tea.Msg {
							return ResumeSessionMsg{OldSessionID: oldSessionID, SubPlanID: subPlanID}
						}
					}
				case overviewActionReviewing:
					return m, func() tea.Msg { return ReimplementMsg{WorkItemID: m.data.WorkItemID} }
				case overviewActionFailed:
					return m, func() tea.Msg { return RetryFailedMsg{WorkItemID: m.data.WorkItemID} }
				}
			}
		case "up", keyDown, "j", "k", "pgup", "pgdown", "home", "end":
			m.viewport, cmd = m.viewport.Update(msg)

			return m, cmd
		}
	case tea.MouseMsg:
		if msg.Action == tea.MouseActionPress {
			switch msg.Button {
			case tea.MouseButtonWheelUp, tea.MouseButtonWheelDown:
				m.viewport, cmd = m.viewport.Update(msg)

				return m, cmd
			}
		}
	case tea.WindowSizeMsg:
		m.SetSize(msg.Width, msg.Height)

		return m, nil
	}

	return m, nil
}

func (m SessionOverviewModel) View() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}

	return fitViewBox(m.viewport.View(), m.width, m.height)
}

func (m *SessionOverviewModel) syncViewport(reset bool) {
	if m.width <= 0 || m.height <= 0 {
		return
	}
	m.viewport.Width = m.width
	m.viewport.Height = m.height
	m.viewport.SetContent(m.renderDocument())
	if reset {
		m.viewport.GotoTop()
	}
}

// openPlanOverlayForChanges resets the feedback input, enters request-changes
// mode, and opens the plan overlay. Both the [c] and [i] shortcuts for a
// plan-review action call this so the overlay always opens with the prompt ready.
func (m *SessionOverviewModel) openPlanOverlayForChanges() {
	m.planReview.feedbackHeight = 1
	m.planReview.feedbackInput.SetHeight(1)
	m.planReview.feedbackInput.SetValue("")
	m.planReview.inputMode = planReviewChanges
	m.planReview.feedbackInput.Placeholder = "Describe the changes needed\u2026"
	m.planReview.feedbackInput.Focus()
	m.planReview.syncViewportSize()
	m.overlay = overviewOverlayPlan
}

// openCompletedOverlayForChanges resets the follow-up feedback input,
// activates it, and opens the completed overlay.
func (m *SessionOverviewModel) openCompletedOverlayForChanges() tea.Cmd {
	m.completed.feedbackInput.SetValue("")
	m.completed.inputActive = true
	cmd := m.completed.feedbackInput.Focus()
	m.overlay = overviewOverlayCompleted
	return cmd
}

func (m *SessionOverviewModel) syncActionModels() {
	if m.data.Header.Title == "" && m.data.Header.ExternalID == "" {
		return
	}
	title := firstNonEmptyString(m.data.Header.Title, m.data.Header.ExternalID)
	m.planReview.SetTitle(title)
	m.question.SetTitle(title)
	m.interrupted.SetTitle(title)
	m.completed.SetTitle(title)
	m.completed.SetWorkItemID(m.data.WorkItemID)
	m.completed.SetStatusLabel(reviewArtifactOverlayLabel(m.data.State))
	// Pass the full plan document so the completed overlay shows what was implemented.
	m.completed.SetPlan(m.data.Plan.FullDocument)
	m.reviewing.SetTitle(title)
	m.reviewing.SetWorkItemID(m.data.WorkItemID)
	if m.data.Plan.Document != nil {
		m.planReview.SetPlanDocument(m.data.Plan.Document.ID, m.data.Plan.FullDocument)
		m.planReview.SetWorkItemID(m.data.WorkItemID)
	}
	action := m.selectedActionCard()
	if action == nil {
		return
	}
	switch action.Kind {
	case overviewActionQuestion:
		if action.Question != nil {
			m.question.SetQuestion(*action.Question, action.ProposedAnswer, action.ProposedAnswer == "")
		}
	case overviewActionInterrupted:
		if action.Session != nil {
			isPlanningPhase := action.Session.Phase == domain.TaskPhasePlanning
			m.interrupted.SetSession(action.Session.ID, action.Session.SubPlanID, action.Session.RepositoryName, action.Session.WorktreePath, action.Session.WorkItemID, isPlanningPhase, action.CanAct)
		}
	case overviewActionReviewing:
		m.reviewing.SetRepos(action.ReviewRepos)
	case overviewActionFailed:
		m.reviewing.SetRepos(action.ReviewRepos)
	}
}

func (m SessionOverviewModel) selectedActionCard() *OverviewActionCard {
	if len(m.data.Actions) == 0 || m.selectedAction < 0 || m.selectedAction >= len(m.data.Actions) {
		return nil
	}
	action := m.data.Actions[m.selectedAction]

	return &action
}

func (m SessionOverviewModel) renderDocument() string {
	sections := []string{
		m.renderHeader(),
		m.renderSummarySection(),
		m.renderActionSection(),
		m.renderSourceSection(),
		m.renderPlanSection(),
		m.renderTasksSection(),
		m.renderExternalSection(),
		m.renderActivitySection(),
	}
	filtered := make([]string, 0, len(sections))
	for _, section := range sections {
		if strings.TrimSpace(section) != "" {
			filtered = append(filtered, section)
		}
	}

	return fitViewBox(strings.Join(filtered, "\n\n"), m.width, max(1, len(strings.Split(strings.Join(filtered, "\n\n"), "\n"))))
}

func (m SessionOverviewModel) renderHeader() string {
	metaParts := []string{m.data.Header.StatusLabel}
	if !m.data.Header.UpdatedAt.IsZero() {
		metaParts = append(metaParts, "Updated "+timeAgo(m.data.Header.UpdatedAt))
	}

	return components.RenderHeaderBlock(m.styles, components.HeaderBlockSpec{
		Title:   firstNonEmptyString(m.data.Header.Title, m.data.Header.ExternalID),
		Meta:    strings.Join(filterEmptyStrings(metaParts), " · "),
		Width:   m.width,
		Divider: true,
	})
}

func (m SessionOverviewModel) renderSummarySection() string {
	innerWidth := components.CalloutInnerWidth(m.styles, m.width)
	rows := []string{
		renderKeyValueLine(m.styles, innerWidth, "External ID", firstNonEmptyString(m.data.Header.ExternalID, "—")),
		renderKeyValueLine(m.styles, innerWidth, "State", firstNonEmptyString(m.data.Header.StatusLabel, "—")),
		renderKeyValueLine(m.styles, innerWidth, "Last updated", formatAbsoluteTime(m.data.Header.UpdatedAt)),
	}
	if strings.TrimSpace(m.data.Header.ProgressText) != "" {
		rows = append(rows, renderKeyValueLine(m.styles, innerWidth, "Progress", m.data.Header.ProgressText))
	}
	if len(m.data.Header.Badges) > 0 {
		rows = append(rows, renderKeyValueLine(m.styles, innerWidth, "Blockers", strings.Join(m.data.Header.Badges, ", ")))
	}
	body := components.RenderCallout(m.styles, components.CalloutSpec{Body: strings.Join(rows, "\n"), Width: m.width, Variant: components.CalloutCard})

	return renderOverviewSection(m.styles, "Summary", body)
}

func (m SessionOverviewModel) renderActionSection() string {
	if len(m.data.Actions) == 0 {
		return ""
	}
	cards := make([]string, 0, len(m.data.Actions))
	for i, action := range m.data.Actions {
		cards = append(cards, renderOverviewActionCard(m.styles, m.width, action, i == m.selectedAction))
	}

	return renderOverviewSection(m.styles, "Action required", strings.Join(cards, "\n\n"))
}

func (m SessionOverviewModel) renderSourceSection() string {
	if len(m.data.Sources) == 0 {
		return renderOverviewSection(m.styles, "Source", m.styles.Muted.Render("No durable source summary is available."))
	}
	items := make([]string, 0, len(m.data.Sources))
	for _, item := range m.data.Sources {
		items = append(items, renderOverviewSourceItem(m.styles, m.width, item))
	}

	return renderOverviewSection(m.styles, "Source", strings.Join(items, "\n\n"))
}

func (m SessionOverviewModel) renderPlanSection() string {
	innerWidth := components.CalloutInnerWidth(m.styles, m.width)
	rows := []string{renderKeyValueLine(m.styles, innerWidth, "Status", firstNonEmptyString(m.data.Plan.StateLabel, "No plan yet"))}
	if m.data.Plan.Exists {
		rows = append(rows,
			renderKeyValueLine(m.styles, innerWidth, "Version", fmt.Sprintf("v%d", m.data.Plan.Version)),
			renderKeyValueLine(m.styles, innerWidth, "Updated", formatAbsoluteTime(m.data.Plan.UpdatedAt)),
			renderKeyValueLine(m.styles, innerWidth, "Repos", strconv.Itoa(m.data.Plan.RepoCount)),
		)
		if m.data.Plan.FAQCount > 0 {
			rows = append(rows, renderKeyValueLine(m.styles, innerWidth, "FAQ", strconv.Itoa(m.data.Plan.FAQCount)))
		}
	}
	if m.data.Plan.DraftSession != "" {
		rows = append(rows, renderKeyValueLine(m.styles, innerWidth, "Planning session", m.data.Plan.DraftSession))
		if !m.data.Plan.DraftUpdatedAt.IsZero() {
			rows = append(rows, renderKeyValueLine(m.styles, innerWidth, "Draft updated", formatAbsoluteTime(m.data.Plan.DraftUpdatedAt)))
		}
	}
	if strings.TrimSpace(m.data.Plan.ActionText) != "" {
		rows = append(rows, "", ansi.Hardwrap(m.styles.Muted.Render(m.data.Plan.ActionText), innerWidth, true))
	}
	if len(m.data.Plan.Excerpt) > 0 {
		rows = append(rows, "", m.styles.Subtitle.Render("Excerpt"), strings.Join(m.data.Plan.Excerpt, "\n"))
	}
	body := components.RenderCallout(m.styles, components.CalloutSpec{Body: strings.Join(rows, "\n"), Width: m.width, Variant: components.CalloutCard})

	return renderOverviewSection(m.styles, "Plan", body)
}

func (m SessionOverviewModel) renderTasksSection() string {
	if len(m.data.Tasks) == 0 {
		return ""
	}
	rows := make([]string, 0, len(m.data.Tasks))
	for _, row := range m.data.Tasks {
		rows = append(rows, renderOverviewTaskRow(m.styles, m.width, row))
	}

	return renderOverviewSection(m.styles, "Tasks", strings.Join(rows, "\n\n"))
}

func (m SessionOverviewModel) renderExternalSection() string {
	lines := make([]string, 0, len(m.data.External.TrackerRefs)+len(m.data.External.Reviews))
	if len(m.data.External.TrackerRefs) > 0 {
		trackerLines := make([]string, 0, len(m.data.External.TrackerRefs))
		for _, ref := range m.data.External.TrackerRefs {
			trackerLines = append(trackerLines, ansi.Hardwrap(m.styles.SettingsText.Render("• "+ref), components.CalloutInnerWidth(m.styles, m.width), true))
		}
		lines = append(lines, m.styles.Subtitle.Render("Source refs"), strings.Join(trackerLines, "\n"))
	}
	if len(m.data.External.Reviews) > 0 {
		reviewLines := make([]string, 0, len(m.data.External.Reviews))
		for _, row := range m.data.External.Reviews {
			reviewLines = append(reviewLines, renderOverviewReviewRow(m.styles, components.CalloutInnerWidth(m.styles, m.width), row))
		}
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, m.styles.Subtitle.Render(labelReviewArtifacts), strings.Join(reviewLines, "\n"))
	}
	if len(lines) == 0 {
		return ""
	}
	body := components.RenderCallout(m.styles, components.CalloutSpec{Body: strings.Join(lines, "\n"), Width: m.width, Variant: components.CalloutCard})

	return renderOverviewSection(m.styles, "External lifecycle", body)
}

func (m SessionOverviewModel) renderActivitySection() string {
	if len(m.data.Activity) == 0 {
		return ""
	}
	lines := make([]string, 0, len(m.data.Activity))
	for _, item := range m.data.Activity {
		lines = append(lines, ansi.Hardwrap(m.styles.SettingsText.Render("• "+item.Summary+" · "+timeAgo(item.Timestamp)), components.CalloutInnerWidth(m.styles, m.width), true))
	}
	body := components.RenderCallout(m.styles, components.CalloutSpec{Body: strings.Join(lines, "\n"), Width: m.width, Variant: components.CalloutCard})

	return renderOverviewSection(m.styles, "Recent activity", body)
}

func (m SessionOverviewModel) overlayView(width, height int) string {
	frameWidth := min(max(48, width-6), 112)
	innerHeight := max(10, min(height-4, 26))
	// completed uses the same large frame as plan review: both display a
	// scrollable plan document and need the extra width and height.
	if m.overlay == overviewOverlayPlan || m.overlay == overviewOverlayCompleted {
		frameWidth = min(max(72, width-12), 220)
		innerHeight = max(12, height-2)
	}
	var body string
	switch m.overlay {
	case overviewOverlayPlan:
		body = m.planReview.View()
	case overviewOverlayQuestion:
		body = m.question.View()
	case overviewOverlayInterrupted:
		body = m.interrupted.View()
	case overviewOverlayCompleted:
		body = m.completed.View()
	case overviewOverlayReviewing:
		body = m.reviewing.View()
	default:
		return ""
	}

	// Each overlay model's View() already applies fitViewBox with identical
	// width (set via SetSize from planOverlayInnerSize / defaultOverlayInnerSize),
	// so per-line ANSI truncation and width-padding are already done. Only the
	// cheap height constraint (split, slice, pad) is needed here.
	return components.RenderOverlayFrame(m.styles, frameWidth, components.OverlayFrameSpec{Body: fitViewHeight(body, innerHeight), Focused: true})
}

func actionKeybindHints(action OverviewActionCard) []KeybindHint {
	switch action.Kind {
	case overviewActionPlanReview:
		return []KeybindHint{{Key: "a", Label: "Approve"}, {Key: "c", Label: "Changes"}, {Key: "i", Label: "Inspect"}}
	case overviewActionQuestion:
		return []KeybindHint{{Key: "A", Label: "Approve answer"}, {Key: "Enter", Label: "Answer"}, {Key: "i", Label: "Inspect"}}
	case overviewActionInterrupted:
		hints := []KeybindHint{{Key: "i", Label: "Inspect"}}
		if action.CanAct {
			resumeLabel := "Resume"
			if action.Session != nil && action.Session.Phase == domain.TaskPhasePlanning {
				resumeLabel = "Restart planning"
			}
			hints = append([]KeybindHint{{Key: "r", Label: resumeLabel}, {Key: "a", Label: "Abandon"}}, hints...)
		}

		return hints
	case overviewActionReviewing:
		return []KeybindHint{{Key: "r", Label: "Re-implement"}, {Key: "o", Label: "Override accept"}, {Key: "i", Label: "Inspect review"}}
	case overviewActionFailed:
		return []KeybindHint{{Key: "r", Label: "Retry"}, {Key: "i", Label: "Inspect"}}
	case overviewActionCompleted:
		return []KeybindHint{{Key: "c", Label: "Changes"}, {Key: "i", Label: "Inspect"}}
	default:
		return nil
	}
}

func renderOverviewSection(st styles.Styles, label, body string) string {
	if strings.TrimSpace(body) == "" {
		return ""
	}

	return st.SectionLabel.Render(label) + "\n" + body
}

func renderOverviewActionCard(st styles.Styles, width int, action OverviewActionCard, selected bool) string {
	innerWidth := components.CalloutInnerWidth(st, width)
	lines := []string{}
	if selected {
		lines = append(lines, ansi.Hardwrap(st.Active.Render("▶ Selected action"), innerWidth, true), "")
	}
	lines = append(lines,
		ansi.Hardwrap(st.Title.Render(action.Title), innerWidth, true),
		renderKeyValueLine(st, innerWidth, "Blocked", action.Blocked),
		renderKeyValueLine(st, innerWidth, "Why", action.Why),
	)
	if len(action.Affected) > 0 {
		lines = append(lines, renderKeyValueLine(st, innerWidth, "Affected", strings.Join(action.Affected, ", ")))
	}
	// Append plan excerpt at render time so that buildOverviewActions stays free
	// of any layout dependency. The Plan field carries the raw text; excerpt
	// truncation uses the same innerWidth computed here for the rest of the card.
	contextLines := append([]string(nil), action.Context...)
	if action.Plan != nil {
		if excerpt := excerptLines(stripPlanPrelude(action.Plan.OrchestratorPlan), innerWidth, 4); len(excerpt) > 0 {
			contextLines = append(contextLines, excerpt...)
		}
	}
	if len(contextLines) > 0 {
		lines = append(lines, "", st.Subtitle.Render("Context"))
		for _, line := range contextLines {
			lines = append(lines, ansi.Hardwrap(st.SettingsText.Render(line), innerWidth, true))
		}
	}
	hintLabels := make([]string, 0, len(actionKeybindHints(action)))
	for _, hint := range actionKeybindHints(action) {
		hintLabels = append(hintLabels, hint.Key+" "+hint.Label)
	}
	if len(hintLabels) > 0 {
		lines = append(lines, "", ansi.Hardwrap(st.Muted.Render(strings.Join(hintLabels, " · ")), innerWidth, true))
	}
	variant := components.CalloutCard
	if selected {
		variant = components.CalloutWarning
	}

	return components.RenderCallout(st, components.CalloutSpec{Body: strings.Join(filterEmptyStringsPreserveBlanks(lines), "\n"), Width: width, Variant: variant})
}

func renderOverviewSourceItem(st styles.Styles, width int, item OverviewSourceItem) string {
	innerWidth := components.CalloutInnerWidth(st, width)
	lines := []string{
		renderKeyValueLine(st, innerWidth, "Provider", firstNonEmptyString(item.Provider, "—")),
		renderKeyValueLine(st, innerWidth, "Ref", firstNonEmptyString(item.Ref, "—")),
	}
	if strings.TrimSpace(item.Title) != "" {
		lines = append(lines, renderKeyValueLine(st, innerWidth, "Title", item.Title))
	}
	if strings.TrimSpace(item.Excerpt) != "" {
		lines = append(lines, "", st.Subtitle.Render("Excerpt"), item.Excerpt)
	}
	if strings.TrimSpace(item.URL) != "" {
		lines = append(lines, renderKeyValueLine(st, innerWidth, "URL", item.URL))
	}

	return components.RenderCallout(st, components.CalloutSpec{Body: strings.Join(lines, "\n"), Width: width, Variant: components.CalloutCard})
}

func renderOverviewTaskRow(st styles.Styles, width int, row OverviewTaskRow) string {
	innerWidth := components.CalloutInnerWidth(st, width)
	lines := []string{
		renderKeyValueLine(st, innerWidth, "Repo", row.RepoName),
		renderKeyValueLine(st, innerWidth, "Sub-plan", row.TaskPlanStatus),
	}
	if strings.TrimSpace(row.SessionTitle) != "" {
		lines = append(lines, renderKeyValueLine(st, innerWidth, "Task", row.SessionTitle))
	}
	if strings.TrimSpace(row.SessionStatus) != "" {
		lines = append(lines, renderKeyValueLine(st, innerWidth, "Session", row.SessionStatus))
	}
	if strings.TrimSpace(row.HarnessName) != "" {
		lines = append(lines, renderKeyValueLine(st, innerWidth, "Harness", row.HarnessName))
	}
	if !row.UpdatedAt.IsZero() {
		lines = append(lines, renderKeyValueLine(st, innerWidth, "Updated", formatAbsoluteTime(row.UpdatedAt)))
	}
	if strings.TrimSpace(row.Note) != "" {
		lines = append(lines, renderKeyValueLine(st, innerWidth, "Note", row.Note))
	}

	return components.RenderCallout(st, components.CalloutSpec{Body: strings.Join(lines, "\n"), Width: width, Variant: components.CalloutCard})
}

func renderOverviewReviewRow(st styles.Styles, width int, row OverviewReviewRow) string {
	parts := []string{firstNonEmptyString(row.Kind, "Review"), firstNonEmptyString(row.RepoName, "repo")}
	if row.Ref != "" {
		parts = append(parts, row.Ref)
	}
	if row.State != "" {
		parts = append(parts, row.State)
	}
	line := strings.Join(parts, " · ")
	if row.URL != "" {
		line += " · " + row.URL
	} else if row.Branch != "" {
		line += " · branch " + row.Branch
	}

	return ansi.Hardwrap(st.SettingsText.Render("• "+line), width, true)
}

func renderKeyValueLine(st styles.Styles, width int, key, value string) string {
	if strings.TrimSpace(value) == "" {
		value = "—"
	}

	return ansi.Hardwrap(st.SectionLabel.Render(key+": ")+st.SettingsText.Render(value), width, true)
}

func filterEmptyStrings(values []string) []string {
	filtered := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			continue
		}
		filtered = append(filtered, value)
	}

	return filtered
}

func filterEmptyStringsPreserveBlanks(values []string) []string {
	filtered := make([]string, 0, len(values))
	previousBlank := false
	for _, value := range values {
		blank := strings.TrimSpace(value) == ""
		if blank && previousBlank {
			continue
		}
		if !blank || len(filtered) > 0 {
			filtered = append(filtered, value)
		}
		previousBlank = blank
	}

	return filtered
}

func formatAbsoluteTime(ts time.Time) string {
	if ts.IsZero() {
		return "—"
	}

	return ts.Local().Format("2006-01-02 15:04 MST")
}

func timeAgo(ts time.Time) string {
	if ts.IsZero() {
		return "unknown"
	}
	delta := time.Since(ts)
	if delta < 0 {
		delta = -delta
	}
	switch {
	case delta < time.Minute:
		return fmt.Sprintf("%ds ago", int(delta.Seconds()))
	case delta < time.Hour:
		return fmt.Sprintf("%dm ago", int(delta.Minutes()))
	case delta < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(delta.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(delta.Hours()/24))
	}
}

func excerptLines(content string, width, maxLines int) []string {
	if strings.TrimSpace(content) == "" || maxLines <= 0 {
		return nil
	}
	rendered := renderMarkdownDocument(content, max(20, width))
	lines := strings.Split(strings.TrimSpace(rendered), "\n")
	excerpt := make([]string, 0, maxLines)
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		excerpt = append(excerpt, ansi.Truncate(line, max(1, width), "…"))
		if len(excerpt) == maxLines {
			break
		}
	}

	return excerpt
}

func stripPlanPrelude(content string) string {
	trimmed := strings.TrimSpace(content)
	const prefix = "```substrate-plan"
	if !strings.HasPrefix(trimmed, prefix) {
		return trimmed
	}
	if idx := strings.Index(trimmed[len(prefix):], "```"); idx >= 0 {
		body := strings.TrimSpace(trimmed[len(prefix)+idx+3:])

		return body
	}

	return trimmed
}

func sessionSourceSummaries(metadata map[string]any) []domain.SourceSummary {
	if len(metadata) == 0 {
		return nil
	}
	raw, ok := metadata["source_summaries"]
	if !ok || raw == nil {
		return nil
	}
	if typed, ok := raw.([]domain.SourceSummary); ok {
		return append([]domain.SourceSummary(nil), typed...)
	}
	payload, err := json.Marshal(raw)
	if err != nil {
		slog.Warn("failed to marshal source summaries metadata", "error", err)
		return nil
	}
	var summaries []domain.SourceSummary
	if err := json.Unmarshal(payload, &summaries); err != nil {
		slog.Warn("failed to unmarshal source summaries", "error", err)
		return nil
	}

	return summaries
}

func sessionURL(metadata map[string]any) string {
	for _, key := range []string{"url", "sentry_permalink"} {
		if value := sessionMetadataString(metadata, key); value != "" {
			return value
		}
	}

	return ""
}

func (a *App) buildOverviewData(wi *domain.Session) SessionOverviewData {
	if wi == nil {
		return SessionOverviewData{}
	}
	entry := a.sidebarEntryFromWorkItem(*wi)
	plan := a.plans[wi.ID]
	subPlans := []domain.TaskPlan(nil)
	if plan != nil {
		subPlans = append(subPlans, a.subPlans[plan.ID]...)
	}

	return SessionOverviewData{
		WorkItemID: wi.ID,
		State:      wi.State,
		Header:     buildOverviewHeader(*wi, entry),
		Actions:    a.buildOverviewActions(wi, plan, subPlans),
		Sources:    buildOverviewSources(*wi, a.widthForInnerContent()),
		Plan:       a.buildOverviewPlan(wi, plan, subPlans),
		Tasks:      a.buildOverviewTasks(wi, subPlans),
		External:   a.buildOverviewExternalLifecycle(wi),
		Activity:   a.buildOverviewActivity(wi, plan),
	}
}

func buildOverviewHeader(wi domain.Session, entry SidebarEntry) OverviewHeader {
	badges := make([]string, 0, 4)
	if wi.State == domain.SessionPlanReview {
		badges = append(badges, "waiting for approval")
	}
	if entry.HasOpenQuestion {
		badges = append(badges, "waiting for answer")
	}
	if entry.HasInterrupted {
		badges = append(badges, "interrupted")
	}
	if wi.State == domain.SessionFailed {
		badges = append(badges, "failed")
	}
	progressText := ""
	if entry.TotalSubPlans > 0 {
		progressText = fmt.Sprintf("%d/%d repos complete", entry.DoneSubPlans, entry.TotalSubPlans)
	}

	return OverviewHeader{
		ExternalID:   wi.ExternalID,
		Title:        wi.Title,
		StatusLabel:  entry.Subtitle(),
		UpdatedAt:    entry.LastActivity,
		ProgressText: progressText,
		Badges:       badges,
	}
}

func buildOverviewSources(wi domain.Session, width int) []OverviewSourceItem {
	summaries := sessionSourceSummaries(wi.Metadata)
	if len(wi.SourceItemIDs) <= 1 {
		var ref string
		if refs := sessionTrackerRefs(wi.Metadata); len(refs) > 0 {
			ref = formatTrackerRef(refs[0])
		} else if len(wi.SourceItemIDs) == 1 {
			ref = wi.SourceItemIDs[0]
		} else {
			ref = wi.ExternalID
		}
		excerpt := ""
		if lines := excerptLines(wi.Description, max(20, width), 4); len(lines) > 0 {
			excerpt = strings.Join(lines, "\n")
		}

		return []OverviewSourceItem{{
			Provider: detailProviderLabel(wi.Source),
			Ref:      ref,
			Title:    wi.Title,
			Excerpt:  excerpt,
			URL:      sessionURL(wi.Metadata),
		}}
	}
	if len(summaries) > 0 {
		items := make([]OverviewSourceItem, 0, len(summaries))
		for _, summary := range summaries {
			items = append(items, OverviewSourceItem{
				Provider: detailProviderLabel(firstNonEmptyString(summary.Provider, wi.Source)),
				Ref:      summary.Ref,
				Title:    summary.Title,
				Excerpt:  strings.Join(excerptLines(summary.Excerpt, max(20, width), 3), "\n"),
				URL:      summary.URL,
			})
		}

		return items
	}
	refs := sessionTrackerRefs(wi.Metadata)
	items := make([]OverviewSourceItem, 0, max(len(refs), len(wi.SourceItemIDs)))
	if len(refs) > 0 {
		for _, ref := range refs {
			items = append(items, OverviewSourceItem{Provider: detailProviderLabel(firstNonEmptyString(ref.Provider, wi.Source)), Ref: formatTrackerRef(ref)})
		}

		return items
	}
	for _, id := range wi.SourceItemIDs {
		items = append(items, OverviewSourceItem{Provider: detailProviderLabel(wi.Source), Ref: id})
	}

	return items
}

func (a *App) buildOverviewPlan(wi *domain.Session, plan *domain.Plan, subPlans []domain.TaskPlan) OverviewPlan {
	overview := OverviewPlan{StateLabel: planStateLabel(wi.State)}
	if plan == nil {
		overview.ActionText = noPlanActionText(wi.State)
		if wi.State == domain.SessionPlanning {
			if planningSession := a.latestPlanningSession(wi.ID); planningSession != nil {
				overview.DraftSession = taskSidebarSessionTitle(planningSession)
				overview.DraftPath = filepath.Join(a.sessionsDir, planningSession.ID, "plan-draft.md")
				if info, excerpt := readPlanningDraftExcerpt(overview.DraftPath, a.widthForInnerContent()); !info.IsZero() || len(excerpt) > 0 {
					overview.DraftUpdatedAt = info
					overview.Excerpt = excerpt
				}
			}
		}

		return overview
	}
	overview.Exists = true
	overview.Document = plan
	overview.FullDocument = domain.ComposePlanDocument(*plan, subPlans)
	overview.Version = plan.Version
	overview.UpdatedAt = plan.UpdatedAt
	overview.RepoCount = len(subPlans)
	overview.FAQCount = len(plan.FAQ)
	overview.ActionText = planActionText(wi.State)
	excerptWidth := components.CalloutInnerWidth(a.statusBar.styles, max(20, a.widthForInnerContent()))
	overview.Excerpt = excerptLines(stripPlanPrelude(plan.OrchestratorPlan), excerptWidth, 6)

	return overview
}

func (a *App) buildOverviewTasks(wi *domain.Session, subPlans []domain.TaskPlan) []OverviewTaskRow {
	if len(subPlans) == 0 {
		return nil
	}
	rows := make([]OverviewTaskRow, 0, len(subPlans))
	for _, sp := range subPlans {
		latest, waiting, interrupted := a.latestTaskForSubPlan(wi.ID, sp.ID)
		row := OverviewTaskRow{
			RepoName:       sp.RepositoryName,
			TaskPlanStatus: humanTaskPlanStatus(sp.Status),
			UpdatedAt:      sp.UpdatedAt,
		}
		if latest != nil {
			row.SessionID = latest.ID
			row.SessionTitle = taskSidebarSessionTitle(latest)
			row.SessionStatus = sessionStatusLabel(latest.Status)
			row.HarnessName = latest.HarnessName
			if latest.UpdatedAt.After(row.UpdatedAt) {
				row.UpdatedAt = latest.UpdatedAt
			}
		}
		switch {
		case waiting != nil:
			row.Note = buildQuestionNote(a.questions[waiting.ID])
		case interrupted != nil:
			row.Note = statusInterrupted
		case latest != nil && latest.Status == domain.AgentSessionFailed:
			row.Note = statusFailed
		case wi.State == domain.SessionReviewing:
			row.Note = a.buildOverviewReviewNote(wi.ID, sp.ID)
		case sp.Status == domain.SubPlanCompleted:
			row.Note = statusCompleted
		}
		rows = append(rows, row)
	}

	return rows
}

func (a *App) latestTaskForSubPlan(workItemID, subPlanID string) (latest, waiting, interrupted *domain.Task) {
	tasks := make([]domain.Task, 0, len(a.sessionsForWorkItem(workItemID)))
	for _, session := range a.sessionsForWorkItem(workItemID) {
		if session.SubPlanID == subPlanID {
			tasks = append(tasks, session)
		}
	}
	if len(tasks) == 0 {
		return nil, nil, nil
	}
	sort.SliceStable(tasks, func(i, j int) bool {
		if !tasks[i].UpdatedAt.Equal(tasks[j].UpdatedAt) {
			return tasks[i].UpdatedAt.After(tasks[j].UpdatedAt)
		}

		return tasks[i].CreatedAt.After(tasks[j].CreatedAt)
	})
	for i := range tasks {
		task := tasks[i]
		if latest == nil {
			latest = &task
		}
		if waiting == nil && task.Status == domain.AgentSessionWaitingForAnswer && hasEscalatedQuestion(a.questions[task.ID]) {
			waiting = &task
		}
		if interrupted == nil && task.Status == domain.AgentSessionInterrupted {
			interrupted = &task
		}
	}

	return latest, waiting, interrupted
}

func latestOverviewReviewCycle(review ReviewsLoadedMsg) (*domain.ReviewCycle, []domain.Critique) {
	if len(review.Cycles) == 0 {
		return nil, nil
	}
	latest := review.Cycles[0]
	for _, cycle := range review.Cycles[1:] {
		if cycle.CycleNumber > latest.CycleNumber || (cycle.CycleNumber == latest.CycleNumber && cycle.UpdatedAt.After(latest.UpdatedAt)) {
			latest = cycle
		}
	}
	critiques := append([]domain.Critique(nil), review.Critiques[latest.ID]...)

	return &latest, critiques
}

func (a *App) buildOverviewReviewNote(workItemID, subPlanID string) string {
	implSession := a.latestImplementationSession(workItemID, subPlanID)
	if implSession == nil {
		return "Under review"
	}
	cycle, critiques := latestOverviewReviewCycle(a.reviews[implSession.ID])
	if len(critiques) > 0 {
		return fmt.Sprintf("%d critique(s)", len(critiques))
	}
	if cycle != nil {
		return humanReviewCycleStatus(cycle.Status)
	}

	return "Under review"
}

func humanReviewCycleStatus(status domain.ReviewCycleStatus) string {
	switch status {
	case domain.ReviewCycleReviewing:
		return "Reviewing"
	case domain.ReviewCycleCritiquesFound:
		return "Critiques found"
	case domain.ReviewCycleReimplementing:
		return "Re-implementing"
	case domain.ReviewCyclePassed:
		return "Passed"
	case domain.ReviewCycleFailed:
		return statusFailed
	default:
		return strings.TrimSpace(string(status))
	}
}

func buildQuestionNote(questions []domain.Question) string {
	for _, question := range questions {
		if question.Status == domain.QuestionEscalated {
			return "Waiting for answer: " + summarizeText(question.Content, 72)
		}
	}

	return "Waiting for answer"
}

func hasEscalatedQuestion(questions []domain.Question) bool {
	for _, question := range questions {
		if question.Status == domain.QuestionEscalated {
			return true
		}
	}

	return false
}

func humanTaskPlanStatus(status domain.TaskPlanStatus) string {
	switch status {
	case domain.SubPlanPending:
		return "Pending"
	case domain.SubPlanInProgress:
		return "In progress"
	case domain.SubPlanCompleted:
		return statusCompleted
	case domain.SubPlanFailed:
		return statusFailed
	default:
		return string(status)
	}
}

func planStateLabel(state domain.SessionState) string {
	switch state {
	case domain.SessionIngested:
		return "No plan yet"
	case domain.SessionPlanning:
		return "Plan in progress"
	case domain.SessionPlanReview:
		return "Plan review needed"
	case domain.SessionApproved:
		return "Approved"
	case domain.SessionImplementing:
		return "Approved plan"
	case domain.SessionReviewing:
		return "Final plan"
	case domain.SessionCompleted:
		return "Final plan"
	case domain.SessionFailed:
		return "Last known plan"
	default:
		return "Plan"
	}
}

func noPlanActionText(state domain.SessionState) string {
	switch state {
	case domain.SessionIngested:
		return "Press [Enter] to start planning."
	case domain.SessionPlanning:
		return "Planning is in progress. The overview shows a bounded draft snapshot when available."
	case domain.SessionFailed:
		return "No persisted plan is available for this failed session."
	default:
		return "No plan is available yet."
	}
}

func planActionText(state domain.SessionState) string {
	switch state {
	case domain.SessionPlanReview:
		return "Review the bounded excerpt here, or press [i] for the full plan with approval controls."
	case domain.SessionApproved, domain.SessionImplementing, domain.SessionReviewing, domain.SessionCompleted:
		return "Press [i] to inspect the full plan in an overlay."
	case domain.SessionFailed:
		return "This is the last known plan snapshot before the failure."
	default:
		return ""
	}
}

func readPlanningDraftExcerpt(path string, width int) (time.Time, []string) {
	if strings.TrimSpace(path) == "" {
		return time.Time{}, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return info.ModTime(), nil
	}

	return info.ModTime(), excerptLines(stripPlanPrelude(string(data)), width, 5)
}

func (a *App) buildOverviewActions(wi *domain.Session, plan *domain.Plan, subPlans []domain.TaskPlan) []OverviewActionCard {
	actions := make([]OverviewActionCard, 0, 4)
	if wi.State == domain.SessionPlanReview && plan != nil {
		affected := make([]string, 0, len(subPlans))
		for _, subPlan := range subPlans {
			affected = append(affected, subPlan.RepositoryName)
		}
		context := []string{}
		if plan.Version > 0 {
			context = append(context, fmt.Sprintf("Version: v%d", plan.Version))
		}
		if !plan.UpdatedAt.IsZero() {
			context = append(context, "Updated: "+formatAbsoluteTime(plan.UpdatedAt))
		}
		if len(subPlans) > 0 {
			context = append(context, fmt.Sprintf("Affected repos: %d", len(subPlans)))
		}
		if len(plan.FAQ) > 0 {
			context = append(context, fmt.Sprintf("Open FAQ items: %d", len(plan.FAQ)))
		}
		actions = append(actions, OverviewActionCard{
			Kind:     overviewActionPlanReview,
			Title:    "Plan review required",
			Blocked:  "Implementation is waiting for plan approval",
			Why:      "The plan must be approved, revised, or rejected before implementation can continue.",
			Affected: affected,
			Context:  context,
			Plan:     plan,
		})
	}
	if reviewAction := a.buildReviewActionCard(wi, subPlans); reviewAction != nil {
		actions = append(actions, *reviewAction)
	}
	if failedAction := a.buildFailedActionCard(wi, subPlans); failedAction != nil {
		actions = append(actions, *failedAction)
	}
	if completedAction := a.buildCompletedActionCard(wi, subPlans); completedAction != nil {
		actions = append(actions, *completedAction)
	}
	// Build a set of sub-plan IDs (or work-item-scoped planning markers)
	// that have an active (non-interrupted, non-terminal) session, so we
	// can skip stale interrupted sessions that have been replaced.
	superseded := make(map[string]bool)
	wiSessions := a.sessionsForWorkItem(wi.ID)
	{
		activeSubPlans := make(map[string]bool)
		hasPlanningActive := false
		for _, s := range wiSessions {
			if s.Status == domain.AgentSessionRunning ||
				s.Status == domain.AgentSessionPending ||
				s.Status == domain.AgentSessionCompleted ||
				s.Status == domain.AgentSessionWaitingForAnswer {
				if s.Phase == domain.TaskPhasePlanning {
					hasPlanningActive = true
				} else if s.SubPlanID != "" {
					activeSubPlans[s.SubPlanID] = true
				}
			}
		}
		for _, s := range wiSessions {
			if s.Status != domain.AgentSessionInterrupted {
				continue
			}
			if s.Phase == domain.TaskPhasePlanning && hasPlanningActive {
				superseded[s.ID] = true
			} else if s.SubPlanID != "" && activeSubPlans[s.SubPlanID] {
				superseded[s.ID] = true
			}
		}
	}
	for _, session := range wiSessions {
		if session.Status == domain.AgentSessionWaitingForAnswer {
			for _, question := range a.questions[session.ID] {
				if question.Status != domain.QuestionEscalated {
					continue
				}
				context := []string{
					"Asked: " + formatAbsoluteTime(question.CreatedAt),
					summarizeText(question.Content, 160),
				}
				if strings.TrimSpace(question.Context) != "" {
					context = append(context, summarizeText(question.Context, 160))
				}
				actions = append(actions, OverviewActionCard{
					Kind:           overviewActionQuestion,
					Title:          "Question waiting for answer",
					Blocked:        summarizeText(question.Content, 120),
					Why:            "This repo task is paused until a human answers the escalated question.",
					Affected:       []string{fmt.Sprintf("%s (%s)", firstNonEmptyString(session.RepositoryName, taskSessionDisplayName(&session)), taskSidebarSessionTitle(&session))},
					Context:        context,
					Question:       ptrQuestion(question),
					QuestionRepo:   session.RepositoryName,
					QuestionTask:   session.ID,
					QuestionAsked:  question.CreatedAt,
					ProposedAnswer: question.ProposedAnswer,
				})

				break
			}
		}
		if session.Status == domain.AgentSessionInterrupted {
			if superseded[session.ID] {
				continue
			}
			card := OverviewActionCard{
				Kind:     overviewActionInterrupted,
				Blocked:  firstNonEmptyString(session.RepositoryName, taskSessionDisplayName(&session)),
				Affected: []string{firstNonEmptyString(session.RepositoryName, taskSessionDisplayName(&session))},
				Context: []string{
					"Last update: " + formatAbsoluteTime(session.UpdatedAt),
					"Cause: previous substrate owner stopped heartbeating while the agent was running",
				},
				Session: &session,
				CanAct:  a.canActOnSession(session),
			}
			if session.Phase == domain.TaskPhasePlanning {
				card.Title = "Planning was interrupted"
				card.Why = "The planning agent stopped unexpectedly. Restart will begin a fresh planning session."
				card.Blocked = "Planning"
				card.Affected = nil
			} else {
				card.Title = "Interrupted task needs recovery"
				card.Why = "This task was interrupted and cannot continue until it is resumed or abandoned."
				card.Context = append(card.Context, "Task: "+taskSidebarSessionTitle(&session))
			}
			actions = append(actions, card)
		}
	}

	return actions
}

func (a *App) buildReviewActionCard(wi *domain.Session, subPlans []domain.TaskPlan) *OverviewActionCard {
	if wi.State != domain.SessionReviewing {
		return nil
	}
	reviewRepos := a.reviewResultsForOverview(wi.ID, subPlans)
	affected := make([]string, 0, len(reviewRepos))
	critiqueCount := 0
	firstCritique := ""
	for _, repo := range reviewRepos {
		if len(repo.Critiques) == 0 {
			continue
		}
		affected = append(affected, repo.RepoName)
		critiqueCount += len(repo.Critiques)
		if firstCritique == "" {
			firstCritique = summarizeText(repo.Critiques[0].Description, 160)
		}
	}
	if critiqueCount == 0 {
		return nil
	}
	context := []string{fmt.Sprintf("Affected repos: %d", len(affected)), fmt.Sprintf("Open critiques: %d", critiqueCount)}
	if firstCritique != "" {
		context = append(context, "First critique: "+firstCritique)
	}

	return &OverviewActionCard{
		Kind:        overviewActionReviewing,
		Title:       "Review requires decision",
		Blocked:     "Critiques are waiting for a human decision",
		Why:         "You can re-implement the reviewed work or override accept from the overview.",
		Affected:    affected,
		Context:     context,
		ReviewRepos: reviewRepos,
	}
}

func (a *App) buildFailedActionCard(wi *domain.Session, subPlans []domain.TaskPlan) *OverviewActionCard {
	if wi.State != domain.SessionFailed {
		return nil
	}
	var affected []string
	for _, subPlan := range subPlans {
		if subPlan.Status != domain.SubPlanFailed {
			continue
		}
		affected = append(affected, subPlan.RepositoryName)
	}
	if len(affected) == 0 {
		// No failed sub-plans found; show a generic card.
		affected = []string{"(unknown)"}
	}
	context := []string{fmt.Sprintf("Failed repos: %d of %d", len(affected), len(subPlans))}
	// Check for review critiques on failed sessions.
	reviewRepos := a.reviewResultsForOverview(wi.ID, subPlans)
	critiqueCount := 0
	for _, repo := range reviewRepos {
		critiqueCount += len(repo.Critiques)
	}
	if critiqueCount > 0 {
		context = append(context, fmt.Sprintf("Outstanding critiques: %d", critiqueCount))
	}
	return &OverviewActionCard{
		Kind:        overviewActionFailed,
		Title:       "Implementation failed",
		Blocked:     fmt.Sprintf("%d repo(s) failed during implementation or review", len(affected)),
		Why:         "You can retry the failed repos or inspect their session logs for details.",
		Affected:    affected,
		Context:     context,
		ReviewRepos: reviewRepos,
	}
}

func (a *App) buildCompletedActionCard(wi *domain.Session, subPlans []domain.TaskPlan) *OverviewActionCard {
	if wi.State != domain.SessionCompleted {
		return nil
	}
	var affected []string
	for _, subPlan := range subPlans {
		if subPlan.Status == domain.SubPlanCompleted {
			affected = append(affected, subPlan.RepositoryName)
		}
	}
	context := []string{fmt.Sprintf("Completed repos: %d of %d", len(affected), len(subPlans))}
	return &OverviewActionCard{
		Kind:     overviewActionCompleted,
		Title:    "Implementation completed",
		Why:      "The implementation is done. You can request follow-up changes or inspect the results.",
		Affected: affected,
		Context:  context,
	}
}

func (a *App) reviewResultsForOverview(workItemID string, subPlans []domain.TaskPlan) []RepoReviewResult {
	results := make([]RepoReviewResult, 0, len(subPlans))
	for _, subPlan := range subPlans {
		implSession := a.latestImplementationSession(workItemID, subPlan.ID)
		if implSession == nil {
			continue
		}
		review := a.reviews[implSession.ID]
		latestCycle, critiques := latestOverviewReviewCycle(review)
		repoResult := RepoReviewResult{RepoName: subPlan.RepositoryName, Critiques: critiques}
		if latestCycle != nil {
			repoResult.Cycles = []domain.ReviewCycle{*latestCycle}
		}
		results = append(results, repoResult)
	}

	return results
}

func ptrQuestion(question domain.Question) *domain.Question {
	q := question

	return &q
}

func summarizeText(text string, limit int) string {
	trimmed := strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if trimmed == "" || limit <= 0 {
		return trimmed
	}
	if ansi.StringWidth(trimmed) <= limit {
		return trimmed
	}

	return ansi.Truncate(trimmed, limit, "…")
}

func (a *App) buildOverviewExternalLifecycle(wi *domain.Session) OverviewExternalLifecycle {
	external := OverviewExternalLifecycle{}
	for _, ref := range sessionTrackerRefs(wi.Metadata) {
		external.TrackerRefs = append(external.TrackerRefs, formatTrackerRef(ref))
	}
	if wi.WorkspaceID == "" {
		return external
	}
	ctx := context.Background()
	// Query from indexed tables when available.
	if a.svcs.SessionArtifacts != nil {
		if links, err := a.svcs.SessionArtifacts.ListByWorkItemID(ctx, wi.ID); err == nil {
			for _, link := range links {
				switch link.Provider {
				case "github":
					if a.svcs.GithubPRs != nil {
						if pr, err := a.svcs.GithubPRs.Get(ctx, link.ProviderArtifactID); err == nil {
							external.Reviews = append(external.Reviews, OverviewReviewRow{
								Kind:     "PR",
								RepoName: pr.Owner + "/" + pr.Repo,
								Ref:      fmt.Sprintf("#%d", pr.Number),
								URL:      pr.HTMLURL,
								State:    pr.State,
								Branch:   pr.HeadBranch,
							})
						}
					}
				case providerGitlab:
					if a.svcs.GitlabMRs != nil {
						if mr, err := a.svcs.GitlabMRs.Get(ctx, link.ProviderArtifactID); err == nil {
							external.Reviews = append(external.Reviews, OverviewReviewRow{
								Kind:     "MR",
								RepoName: mr.ProjectPath,
								Ref:      fmt.Sprintf("!%d", mr.IID),
								URL:      mr.WebURL,
								State:    mr.State,
								Branch:   mr.SourceBranch,
							})
						}
					}
				}
			}
		}
	} else if a.svcs.Events != nil {
		// Fallback to event replay if repos not available (e.g. tests).
		events, err := a.svcs.Events.ListByWorkspaceID(ctx, wi.WorkspaceID, 0)
		if err == nil {
			latestByKey := make(map[string]domain.ReviewArtifact)
			for _, event := range events {
				if domain.EventType(event.EventType) != domain.EventReviewArtifactRecorded {
					continue
				}
				var payload domain.ReviewArtifactEventPayload
				if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil {
					continue
				}
				if payload.WorkItemID != wi.ID {
					continue
				}
				artifact := payload.Artifact
				if artifact.UpdatedAt.IsZero() {
					artifact.UpdatedAt = event.CreatedAt
				}
				key := strings.Join([]string{strings.TrimSpace(artifact.Provider), strings.TrimSpace(artifact.RepoName), strings.TrimSpace(artifact.Branch)}, ":")
				if current, ok := latestByKey[key]; ok && !artifact.UpdatedAt.After(current.UpdatedAt) {
					continue
				}
				latestByKey[key] = artifact
			}
			for _, artifact := range latestByKey {
				external.Reviews = append(external.Reviews, OverviewReviewRow{
					Kind:     firstNonEmptyString(artifact.Kind, reviewKindForProvider(artifact.Provider)),
					RepoName: artifact.RepoName,
					Ref:      artifact.Ref,
					URL:      artifact.URL,
					State:    artifact.State,
					Branch:   artifact.Branch,
				})
			}
		}
	}
	sort.SliceStable(external.Reviews, func(i, j int) bool {
		if external.Reviews[i].RepoName != external.Reviews[j].RepoName {
			return external.Reviews[i].RepoName < external.Reviews[j].RepoName
		}
		if external.Reviews[i].Branch != external.Reviews[j].Branch {
			return external.Reviews[i].Branch < external.Reviews[j].Branch
		}
		return external.Reviews[i].Ref < external.Reviews[j].Ref
	})
	return external
}

func (a *App) buildArtifactItems(wi *domain.Session) []ArtifactItem {
	if wi == nil || wi.WorkspaceID == "" {
		return nil
	}
	ctx := context.Background()
	var items []ArtifactItem
	if a.svcs.SessionArtifacts != nil {
		links, err := a.svcs.SessionArtifacts.ListByWorkItemID(ctx, wi.ID)
		if err != nil {
			slog.Error("failed to list session artifacts", "error", err, "workItemID", wi.ID)
			return nil
		}
		for _, link := range links {
			switch link.Provider {
			case "github":
				if a.svcs.GithubPRs != nil {
					pr, err := a.svcs.GithubPRs.Get(ctx, link.ProviderArtifactID)
					if err != nil {
						slog.Warn("failed to get github PR", "error", err, "id", link.ProviderArtifactID)
						continue
					}
					state := pr.State
					if pr.Draft && state != "merged" && state != "closed" {
						state = "draft"
					}
					items = append(items, ArtifactItem{
						Provider:  "github",
						Kind:      "PR",
						RepoName:  pr.Owner + "/" + pr.Repo,
						Ref:       fmt.Sprintf("#%d", pr.Number),
						URL:       pr.HTMLURL,
						State:     state,
						Branch:    pr.HeadBranch,
						Draft:     pr.Draft,
						MergedAt:  pr.MergedAt,
						CreatedAt: pr.CreatedAt,
						UpdatedAt: pr.UpdatedAt,
					})
				}
			case providerGitlab:
				if a.svcs.GitlabMRs != nil {
					mr, err := a.svcs.GitlabMRs.Get(ctx, link.ProviderArtifactID)
					if err != nil {
						slog.Warn("failed to get gitlab MR", "error", err, "id", link.ProviderArtifactID)
						continue
					}
					state := mr.State
					if mr.Draft && state != "merged" && state != "closed" {
						state = "draft"
					}
					items = append(items, ArtifactItem{
						Provider:  "gitlab",
						Kind:      "MR",
						RepoName:  mr.ProjectPath,
						Ref:       fmt.Sprintf("!%d", mr.IID),
						URL:       mr.WebURL,
						State:     state,
						Branch:    mr.SourceBranch,
						Draft:     mr.Draft,
						CreatedAt: mr.CreatedAt,
						UpdatedAt: mr.UpdatedAt,
					})
				}
			}
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].RepoName != items[j].RepoName {
			return items[i].RepoName < items[j].RepoName
		}
		return items[i].Ref < items[j].Ref
	})
	return items
}

func reviewKindForProvider(provider string) string {
	switch provider {
	case providerGitlab:
		return "MR"
	default:
		return "PR"
	}
}

func reviewArtifactOverlayLabel(state domain.SessionState) string {
	if state == domain.SessionCompleted {
		return "✓ Completed"
	}

	return labelReviewArtifacts
}

func (a *App) buildOverviewActivity(wi *domain.Session, plan *domain.Plan) []OverviewActivityItem {
	items := make([]OverviewActivityItem, 0, 8)
	if plan != nil && !plan.UpdatedAt.IsZero() {
		items = append(items, OverviewActivityItem{Summary: fmt.Sprintf("Plan v%d updated", plan.Version), Timestamp: plan.UpdatedAt})
	}
	for _, session := range a.sessionsForWorkItem(wi.ID) {
		summary := ""
		switch session.Status {
		case domain.AgentSessionWaitingForAnswer:
			if hasEscalatedQuestion(a.questions[session.ID]) {
				summary = firstNonEmptyString(session.RepositoryName, taskSessionDisplayName(&session)) + " asked a question"
			}
		case domain.AgentSessionInterrupted:
			summary = firstNonEmptyString(session.RepositoryName, taskSessionDisplayName(&session)) + " interrupted"
		case domain.AgentSessionFailed:
			summary = firstNonEmptyString(session.RepositoryName, taskSessionDisplayName(&session)) + " failed"
		case domain.AgentSessionCompleted:
			summary = firstNonEmptyString(session.RepositoryName, taskSessionDisplayName(&session)) + " completed"
		}
		if summary != "" {
			items = append(items, OverviewActivityItem{Summary: summary, Timestamp: session.UpdatedAt})
		}
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].Timestamp.After(items[j].Timestamp) })
	if len(items) > 3 {
		items = items[:3]
	}

	return items
}

func (a *App) widthForInnerContent() int {
	if a.windowWidth <= 0 || a.windowHeight <= 0 {
		return 72
	}
	layout := styles.ComputeMainPageLayout(a.windowWidth, a.windowHeight, SidebarWidth, a.statusBar.styles.Chrome, a.statusBar.RequiredHeight(a.currentHints(), a.statusBarText(), a.windowWidth))
	if layout.ContentInnerWidth > 0 {
		return layout.ContentInnerWidth
	}

	return 72
}

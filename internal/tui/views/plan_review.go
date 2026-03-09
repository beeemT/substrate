package views

import (
	"os"
	"os/exec"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/tui/styles"
)

type planReviewInputMode int

const (
	planReviewNormal  planReviewInputMode = iota
	planReviewChanges                     // user pressed [c]: request changes
	planReviewReject                      // user pressed [r]: rejection reason
)

// PlanReviewModel renders the plan and handles approval flow.
type PlanReviewModel struct {
	viewport      viewport.Model
	feedbackInput textinput.Model
	inputMode     planReviewInputMode
	title         string
	planID        string
	workItemID    string
	planContent   string
	styles        styles.Styles
	width         int
	height        int
}

func NewPlanReviewModel(st styles.Styles) PlanReviewModel {
	ti := textinput.New()
	ti.CharLimit = 500
	return PlanReviewModel{
		viewport:      viewport.New(0, 0),
		feedbackInput: ti,
		styles:        st,
	}
}

func (m *PlanReviewModel) SetSize(width, height int) {
	m.width = width
	m.height = height
	m.viewport.Width = width
	m.viewport.Height = height - 4 // header + divider + feedback row + hints
}

func (m *PlanReviewModel) SetTitle(title string) { m.title = title }

func (m *PlanReviewModel) SetPlan(plan domain.Plan) {
	m.planID = plan.ID
	m.planContent = plan.OrchestratorPlan
	m.viewport.SetContent(plan.OrchestratorPlan)
	m.viewport.GotoTop()
}

func (m *PlanReviewModel) SetWorkItemID(id string) { m.workItemID = id }

func (m *PlanReviewModel) KeybindHints() []KeybindHint {
	return []KeybindHint{
		{Key: "a", Label: "Approve"},
		{Key: "c", Label: "Request changes"},
		{Key: "e", Label: "Edit in $EDITOR"},
		{Key: "r", Label: "Reject"},
		{Key: "↑↓", Label: "Scroll"},
	}
}

func (m PlanReviewModel) Update(msg tea.Msg) (PlanReviewModel, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.inputMode != planReviewNormal {
			switch msg.String() {
			case "enter":
				text := m.feedbackInput.Value()
				m.feedbackInput.SetValue("")
				m.feedbackInput.Blur()
				if m.inputMode == planReviewChanges {
					m.inputMode = planReviewNormal
					return m, func() tea.Msg {
						return PlanRequestChangesMsg{PlanID: m.planID, Feedback: text}
					}
				}
				m.inputMode = planReviewNormal
				return m, func() tea.Msg {
					return PlanRejectMsg{PlanID: m.planID, Reason: text, WorkItemID: m.workItemID}
				}
			case "esc":
				m.inputMode = planReviewNormal
				m.feedbackInput.SetValue("")
				m.feedbackInput.Blur()
			default:
				m.feedbackInput, cmd = m.feedbackInput.Update(msg)
			}
			return m, cmd
		}
		switch msg.String() {
		case "a":
			return m, func() tea.Msg {
				return PlanApproveMsg{PlanID: m.planID, WorkItemID: m.workItemID}
			}
		case "c":
			m.inputMode = planReviewChanges
			m.feedbackInput.Placeholder = "Describe the changes needed…"
			m.feedbackInput.Focus()
		case "r":
			m.inputMode = planReviewReject
			m.feedbackInput.Placeholder = "Reason for rejection…"
			m.feedbackInput.Focus()
		case "e":
			return m, editPlanInEditorCmd(m.planID, m.planContent)
		case "up", "k", "down", "j", "pgup", "pgdown":
			m.viewport, cmd = m.viewport.Update(msg)
		}
	case tea.WindowSizeMsg:
		m.viewport, cmd = m.viewport.Update(msg)
	default:
		m.viewport, cmd = m.viewport.Update(msg)
	}
	return m, cmd
}

func (m PlanReviewModel) View() string {
	titleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#f0f0f0")).Bold(true)
	divider := lipgloss.NewStyle().Foreground(lipgloss.Color("#2d2d44")).Render(strings.Repeat("─", m.width))
	header := titleStyle.Render(m.title + " · Plan Review")

	body := m.viewport.View()

	var feedbackRow string
	if m.inputMode != planReviewNormal {
		label := "Request changes: "
		if m.inputMode == planReviewReject {
			label = "Rejection reason: "
		}
		feedbackRow = lipgloss.NewStyle().Foreground(lipgloss.Color("#fbbf24")).Render(label) + m.feedbackInput.View()
	}

	hints := lipgloss.NewStyle().Foreground(lipgloss.Color("#2d2d44")).Render(strings.Repeat("─", m.width)) + "\n"
	hints += lipgloss.NewStyle().Foreground(lipgloss.Color("#6b7280")).Render(
		"[" + lipgloss.NewStyle().Foreground(lipgloss.Color("#5b8def")).Bold(true).Render("a") + "] Approve  " +
			"[" + lipgloss.NewStyle().Foreground(lipgloss.Color("#5b8def")).Bold(true).Render("c") + "] Changes  " +
			"[" + lipgloss.NewStyle().Foreground(lipgloss.Color("#5b8def")).Bold(true).Render("e") + "] Editor  " +
			"[" + lipgloss.NewStyle().Foreground(lipgloss.Color("#5b8def")).Bold(true).Render("r") + "] Reject")

	parts := []string{header, divider, body}
	if feedbackRow != "" {
		parts = append(parts, feedbackRow)
	}
	parts = append(parts, hints)
	return strings.Join(parts, "\n")
}

// editPlanInEditorCmd opens the plan content in $EDITOR.
func editPlanInEditorCmd(planID, content string) tea.Cmd {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}
	f, err := os.CreateTemp("", "substrate-plan-*.md")
	if err != nil {
		return func() tea.Msg { return ErrMsg{Err: err} }
	}
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		os.Remove(f.Name())
		return func() tea.Msg { return ErrMsg{Err: err} }
	}
	f.Close()
	tmpFile := f.Name()
	return tea.ExecProcess(exec.Command(editor, tmpFile), func(err error) tea.Msg {
		if err != nil {
			os.Remove(tmpFile)
			return ErrMsg{Err: err}
		}
		data, readErr := os.ReadFile(tmpFile)
		os.Remove(tmpFile)
		if readErr != nil {
			return ErrMsg{Err: readErr}
		}
		return PlanEditedMsg{PlanID: planID, NewContent: string(data)}
	})
}

// ReadyToPlanModel shows work item details when state is "ingested".
type ReadyToPlanModel struct {
	workItem *domain.WorkItem
	styles   styles.Styles
	width    int
	height   int
}

func NewReadyToPlanModel(st styles.Styles) ReadyToPlanModel {
	return ReadyToPlanModel{styles: st}
}

func (m *ReadyToPlanModel) SetSize(w, h int) { m.width = w; m.height = h }

func (m *ReadyToPlanModel) SetWorkItem(wi *domain.WorkItem) { m.workItem = wi }

func (m ReadyToPlanModel) Update(msg tea.Msg) (ReadyToPlanModel, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok && key.String() == "enter" && m.workItem != nil {
		id := m.workItem.ID
		return m, func() tea.Msg { return StartPlanMsg{WorkItemID: id} }
	}
	return m, nil
}

func (m ReadyToPlanModel) View() string {
	if m.workItem == nil || m.width <= 0 || m.height <= 0 {
		return ""
	}

	description := strings.TrimSpace(m.workItem.Description)
	if description == "" {
		description = "_No description provided._"
	}

	sectionLabelStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#94a3b8"))
	descriptionBoxStyle := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#334155")).
		Padding(0, 1)
	descriptionInnerWidth := m.width - descriptionBoxStyle.GetHorizontalFrameSize()
	if descriptionInnerWidth < 1 {
		descriptionInnerWidth = 1
	}

	nextStepBoxStyle := lipgloss.NewStyle().
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("#2d2d44")).
		Padding(0, 1)
	nextStepInnerWidth := m.width - nextStepBoxStyle.GetHorizontalFrameSize()
	if nextStepInnerWidth < 1 {
		nextStepInnerWidth = 1
	}

	nextStep := m.styles.Muted.Render("Press ") +
		m.styles.KeybindAccent.Render("[Enter]") +
		m.styles.Muted.Render(" to start planning.")

	descriptionContent := strings.Trim(renderMarkdownDocument(description, descriptionInnerWidth), "\n")
	nextStepContent := ansi.Hardwrap(nextStep, nextStepInnerWidth, true)

	topBlocks := []string{
		m.styles.Title.Render(m.workItem.ExternalID + " · " + m.workItem.Title),
		sectionLabelStyle.Render("Description"),
		descriptionBoxStyle.Render(descriptionContent),
	}
	bottomBlocks := []string{
		lipgloss.PlaceHorizontal(m.width, lipgloss.Right, sectionLabelStyle.Render("Next step")),
		lipgloss.PlaceHorizontal(m.width, lipgloss.Right, nextStepBoxStyle.Render(nextStepContent)),
	}

	topLineCount := 0
	for _, block := range topBlocks {
		topLineCount += len(strings.Split(block, "\n"))
	}
	bottomLineCount := 0
	for _, block := range bottomBlocks {
		bottomLineCount += len(strings.Split(block, "\n"))
	}
	gapLines := m.height - topLineCount - bottomLineCount
	if gapLines < 0 {
		gapLines = 0
	}

	renderedLines := make([]string, 0, topLineCount+gapLines+bottomLineCount)
	for _, block := range topBlocks {
		renderedLines = append(renderedLines, strings.Split(block, "\n")...)
	}
	for i := 0; i < gapLines; i++ {
		renderedLines = append(renderedLines, "")
	}
	for _, block := range bottomBlocks {
		renderedLines = append(renderedLines, strings.Split(block, "\n")...)
	}

	return fitViewBox(strings.Join(renderedLines, "\n"), m.width, m.height)
}

// AwaitingImplModel shows plan summary when state is "approved".
type AwaitingImplModel struct {
	workItem *domain.WorkItem
	styles   styles.Styles
	width    int
	height   int
}

func NewAwaitingImplModel(st styles.Styles) AwaitingImplModel {
	return AwaitingImplModel{styles: st}
}

func (m *AwaitingImplModel) SetSize(w, h int) { m.width = w; m.height = h }

func (m *AwaitingImplModel) SetWorkItem(wi *domain.WorkItem) { m.workItem = wi }

func (m AwaitingImplModel) View() string {
	if m.workItem == nil {
		return ""
	}
	return strings.Join([]string{
		m.styles.Title.Render(m.workItem.ExternalID + " · " + m.workItem.Title),
		m.styles.Muted.Render(strings.Repeat("─", m.width)),
		"",
		m.styles.Active.Render("Plan approved."),
		m.styles.Subtitle.Render("Implementation will begin shortly."),
	}, "\n")
}

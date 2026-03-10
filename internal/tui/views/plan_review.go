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
	"github.com/beeemT/substrate/internal/tui/components"
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
	m.syncViewportSize()
}

func (m *PlanReviewModel) syncViewportSize() {
	reservedRows := 4 // header block + divider above hints + hint row
	if m.inputMode != planReviewNormal {
		reservedRows++
	}
	m.viewport.Width = m.width
	m.viewport.Height = max(1, m.height-reservedRows)
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
					m.syncViewportSize()
					return m, func() tea.Msg {
						return PlanRequestChangesMsg{PlanID: m.planID, Feedback: text}
					}
				}
				m.inputMode = planReviewNormal
				m.syncViewportSize()
				return m, func() tea.Msg {
					return PlanRejectMsg{PlanID: m.planID, Reason: text, WorkItemID: m.workItemID}
				}
			case "esc":
				m.inputMode = planReviewNormal
				m.feedbackInput.SetValue("")
				m.feedbackInput.Blur()
				m.syncViewportSize()
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
			m.syncViewportSize()
		case "r":
			m.inputMode = planReviewReject
			m.feedbackInput.Placeholder = "Reason for rejection…"
			m.feedbackInput.Focus()
			m.syncViewportSize()
		case "e":
			return m, editPlanInEditorCmd(m.planID, m.planContent)
		case "up", "k", "down", "j", "pgup", "pgdown":
			m.viewport, cmd = m.viewport.Update(msg)
		}
	case tea.WindowSizeMsg:
		m.syncViewportSize()
		m.viewport, cmd = m.viewport.Update(msg)
	default:
		m.viewport, cmd = m.viewport.Update(msg)
	}
	return m, cmd
}

func (m PlanReviewModel) View() string {
	header := components.RenderHeaderBlock(m.styles, components.HeaderBlockSpec{
		Title:   m.title + " · Plan Review",
		Width:   m.width,
		Divider: true,
	})

	body := m.viewport.View()

	var feedbackRow string
	if m.inputMode != planReviewNormal {
		label := "Request changes: "
		if m.inputMode == planReviewReject {
			label = "Rejection reason: "
		}
		feedbackRow = m.styles.Warning.Render(label) + m.feedbackInput.View()
	}

	hints := components.RenderKeyHints(m.styles, []components.KeyHint{
		{Key: "a", Label: "Approve"},
		{Key: "c", Label: "Changes"},
		{Key: "e", Label: "Editor"},
		{Key: "r", Label: "Reject"},
	}, "  ")

	parts := append(strings.Split(header, "\n"), body)
	if feedbackRow != "" {
		parts = append(parts, feedbackRow)
	}
	parts = append(parts, components.RenderDivider(m.styles, m.width), hints)
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

	descriptionInnerWidth := components.CalloutInnerWidth(m.styles, m.width)
	nextStepInset := 2
	nextStepWidth := max(1, m.width-(nextStepInset*2))
	nextStepInnerWidth := components.CalloutInnerWidth(m.styles, nextStepWidth)
	nextStep := m.styles.Muted.Render("Press ") +
		m.styles.KeybindAccent.Render("[Enter]") +
		m.styles.Muted.Render(" to start planning.")

	headingInset := lipgloss.NewStyle().PaddingLeft(2)
	nextStepInsetStyle := lipgloss.NewStyle().Padding(0, nextStepInset, 1, nextStepInset)
	descriptionContent := strings.Trim(renderMarkdownDocument(description, descriptionInnerWidth), "\n")
	nextStepContent := ansi.Hardwrap(nextStep, nextStepInnerWidth, true)
	nextStepCard := components.RenderCallout(m.styles, components.CalloutSpec{Body: nextStepContent, Width: nextStepWidth, Variant: components.CalloutCard})
	nextStepBlock := nextStepInsetStyle.Render(nextStepCard)

	topBlocks := []string{
		headingInset.Render(m.styles.Title.Render(m.workItem.ExternalID + " · " + m.workItem.Title)),
		headingInset.Render(m.styles.SectionLabel.Render("Details")),
		components.RenderCallout(m.styles, components.CalloutSpec{Body: descriptionContent, Width: m.width}),
	}

	footerLineCount := len(strings.Split(nextStepBlock, "\n"))
	bodyHeight := max(0, m.height-footerLineCount)
	body := fitViewHeight(strings.Join(topBlocks, "\n"), bodyHeight)
	if body == "" {
		return fitViewBox(nextStepBlock, m.width, m.height)
	}

	return fitViewBox(body+"\n"+nextStepBlock, m.width, m.height)
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
		components.RenderDivider(m.styles, m.width),
		"",
		m.styles.Active.Render("Plan approved."),
		m.styles.Subtitle.Render("Implementation will begin shortly."),
	}, "\n")
}

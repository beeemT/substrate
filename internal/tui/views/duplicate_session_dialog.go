package views

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/beeemT/substrate/internal/domain"
)

type duplicateSessionDialogState struct {
	RequestedWorkItem domain.WorkItem
	ExistingWorkItem  domain.WorkItem
	Selected          int
}

type duplicateSessionDialogOption struct {
	Action       WorkItemDuplicateAction
	Title        string
	CompactTitle string
}

func (a *App) showDuplicateSessionDialog(requested, existing domain.WorkItem) {
	a.duplicateSession = duplicateSessionDialogState{
		RequestedWorkItem: requested,
		ExistingWorkItem:  existing,
		Selected:          1,
	}
	a.duplicateSessionActive = true
}

func (a *App) closeDuplicateSessionDialog() {
	a.duplicateSession = duplicateSessionDialogState{}
	a.duplicateSessionActive = false
}

func (a App) duplicateSessionOptions() []duplicateSessionDialogOption {
	return []duplicateSessionDialogOption{
		{Action: WorkItemDuplicateCancel, Title: "Cancel creation", CompactTitle: "Cancel"},
		{Action: WorkItemDuplicateOpenExisting, Title: "Go to existing work item", CompactTitle: "Open existing"},
		{Action: WorkItemDuplicateCreateSession, Title: "Start planning with existing work item", CompactTitle: "Start planning"},
	}
}

func (a *App) cycleDuplicateSessionOption(delta int) {
	options := a.duplicateSessionOptions()
	if len(options) == 0 {
		a.duplicateSession.Selected = 0
		return
	}
	a.duplicateSession.Selected = (a.duplicateSession.Selected + delta + len(options)) % len(options)
}

func (a App) selectedDuplicateSessionAction() WorkItemDuplicateAction {
	options := a.duplicateSessionOptions()
	if len(options) == 0 {
		return WorkItemDuplicateCancel
	}
	index := a.duplicateSession.Selected
	if index < 0 || index >= len(options) {
		index = 0
	}
	return options[index].Action
}

func (a App) handleDuplicateSessionKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k", "shift+tab":
		a.cycleDuplicateSessionOption(-1)
		return a, nil
	case "down", "j", "tab":
		a.cycleDuplicateSessionOption(1)
		return a, nil
	case "esc", "c":
		return a, func() tea.Msg { return WorkItemDuplicateActionMsg{Action: WorkItemDuplicateCancel} }
	case "o", "g":
		return a, func() tea.Msg { return WorkItemDuplicateActionMsg{Action: WorkItemDuplicateOpenExisting} }
	case "s", "n":
		return a, func() tea.Msg { return WorkItemDuplicateActionMsg{Action: WorkItemDuplicateCreateSession} }
	case "enter":
		action := a.selectedDuplicateSessionAction()
		return a, func() tea.Msg { return WorkItemDuplicateActionMsg{Action: action} }
	default:
		return a, nil
	}
}

func (a App) duplicateSessionDialogView() string {
	styles := a.statusBar.styles
	availableWidth := a.windowWidth - 4
	if availableWidth <= 0 {
		availableWidth = a.windowWidth
	}
	if availableWidth <= 0 {
		availableWidth = 60
	}
	finalWidth := min(72, availableWidth)
	if a.windowWidth > 0 {
		finalWidth = min(finalWidth, a.windowWidth)
	}
	frame := styles.OverlayFrame.Copy().Width(finalWidth)
	innerWidth := finalWidth - frame.GetHorizontalFrameSize()
	if innerWidth < 1 {
		innerWidth = 1
	}
	content := lipgloss.NewStyle().Width(innerWidth)
	compact := innerWidth < 40 || (a.windowHeight > 0 && a.windowHeight <= 14)

	requested := workItemSummaryLabel(a.duplicateSession.RequestedWorkItem)
	existing := workItemSummaryLabel(a.duplicateSession.ExistingWorkItem)
	rows := []string{
		styles.Title.Render("Work item already exists"),
		"",
		content.Render(styles.Label.Render("Existing work item: ") + styles.Accent.Render(existing)),
	}
	if !compact {
		rows = append(rows,
			content.Render(styles.Subtitle.Render("This selection already exists in this workspace.")),
		)
		if requested != "" && requested != existing {
			rows = append(rows, content.Render(styles.Label.Render("Requested selection: ")+styles.Subtitle.Render(requested)))
		}
	}
	rows = append(rows, "")
	for i, option := range a.duplicateSessionOptions() {
		title := option.Title
		if compact && strings.TrimSpace(option.CompactTitle) != "" {
			title = option.CompactTitle
		}
		prefix := "  "
		line := styles.Subtitle.Render(title)
		if i == a.duplicateSession.Selected {
			prefix = styles.KeybindAccent.Render("› ")
			line = styles.Accent.Render(title)
		}
		rows = append(rows, content.Render(prefix+line))
	}
	rows = append(rows, "")
	if compact {
		rows = append(rows, content.Render(styles.Hint.Render("Enter confirm • Esc cancel")))
	} else {
		rows = append(rows, content.Render(styles.Hint.Render("↑/↓ choose • Enter confirm • Esc cancel")))
	}
	return frame.Render(strings.Join(rows, "\n"))
}

func workItemSummaryLabel(wi domain.WorkItem) string {
	label := workItemDisplayLabel(wi)
	title := strings.TrimSpace(wi.Title)
	if title != "" && title != label {
		return label + " · " + title
	}
	return label
}

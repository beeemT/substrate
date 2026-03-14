package views

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/beeemT/substrate/internal/domain"
)

type duplicateSessionDialogState struct {
	RequestedSession domain.Session
	ExistingSession  domain.Session
	Selected         int
}

type duplicateSessionDialogOption struct {
	Action       SessionDuplicateAction
	Title        string
	CompactTitle string
}

func (a *App) showDuplicateSessionDialog(requested, existing domain.Session) {
	a.duplicateSession = duplicateSessionDialogState{
		RequestedSession: requested,
		ExistingSession:  existing,
		Selected:         1,
	}
	a.duplicateSessionActive = true
}

func (a *App) closeDuplicateSessionDialog() {
	a.duplicateSession = duplicateSessionDialogState{}
	a.duplicateSessionActive = false
}

func (a App) duplicateSessionOptions() []duplicateSessionDialogOption {
	return []duplicateSessionDialogOption{
		{Action: SessionDuplicateCancel, Title: "Cancel creation", CompactTitle: "Cancel"},
		{Action: SessionDuplicateOpenExisting, Title: "Go to existing session", CompactTitle: "Open existing"},
		{Action: SessionDuplicateCreateSession, Title: "Start planning with existing session", CompactTitle: "Start planning"},
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

func (a App) selectedDuplicateSessionAction() SessionDuplicateAction {
	options := a.duplicateSessionOptions()
	if len(options) == 0 {
		return SessionDuplicateCancel
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
		return a, func() tea.Msg { return SessionDuplicateActionMsg{Action: SessionDuplicateCancel} }
	case "o", "g":
		return a, func() tea.Msg { return SessionDuplicateActionMsg{Action: SessionDuplicateOpenExisting} }
	case "s", "n":
		return a, func() tea.Msg { return SessionDuplicateActionMsg{Action: SessionDuplicateCreateSession} }
	case "enter":
		action := a.selectedDuplicateSessionAction()
		return a, func() tea.Msg { return SessionDuplicateActionMsg{Action: action} }
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
	titleStyle := styles.Title
	subtitleStyle := styles.Subtitle
	labelStyle := styles.Label
	accentStyle := styles.Accent
	hintStyle := styles.Hint
	keybindStyle := styles.KeybindAccent
	compact := innerWidth < 40 || (a.windowHeight > 0 && a.windowHeight <= 14)

	requested := sessionSummaryLabel(a.duplicateSession.RequestedSession)
	existing := sessionSummaryLabel(a.duplicateSession.ExistingSession)
	rows := []string{
		titleStyle.Render("Session already exists"),
		"",
		content.Render(labelStyle.Render("Existing session: ") + accentStyle.Render(existing)),
	}
	if !compact {
		rows = append(rows, content.Render(subtitleStyle.Render("This selection already exists in this workspace.")))
		if requested != "" && requested != existing {
			rows = append(rows, content.Render(labelStyle.Render("Requested selection: ")+subtitleStyle.Render(requested)))
		}
	}
	rows = append(rows, "")
	for i, option := range a.duplicateSessionOptions() {
		title := option.Title
		if compact && strings.TrimSpace(option.CompactTitle) != "" {
			title = option.CompactTitle
		}
		prefix := "  "
		line := subtitleStyle.Render(title)
		if i == a.duplicateSession.Selected {
			prefix = keybindStyle.Render("› ")
			line = accentStyle.Render(title)
		}
		rows = append(rows, content.Render(prefix+line))
	}
	rows = append(rows, "")
	if compact {
		rows = append(rows, content.Render(hintStyle.Render("Enter confirm • Esc cancel")))
	} else {
		rows = append(rows, content.Render(hintStyle.Render("↑/↓ choose • Enter confirm • Esc cancel")))
	}
	return frame.Render(strings.Join(rows, "\n"))
}

func sessionSummaryLabel(session domain.Session) string {
	label := workItemDisplayLabel(session)
	title := strings.TrimSpace(session.Title)
	if title != "" && title != label {
		return label + " · " + title
	}
	return label
}

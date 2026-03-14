package views

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/beeemT/substrate/internal/tui/components"
	"github.com/beeemT/substrate/internal/tui/styles"
)

// InterruptedModel handles the interrupted session display.
type InterruptedModel struct {
	title     string
	subPlanID string
	sessionID string
	worktree  string
	repoName  string
	canAct    bool // true if current instance owns or owner is dead
	styles    styles.Styles
	width     int
	height    int
}

func NewInterruptedModel(st styles.Styles) InterruptedModel {
	return InterruptedModel{styles: st}
}

func (m *InterruptedModel) SetSize(w, h int)  { m.width = w; m.height = h }
func (m *InterruptedModel) SetTitle(t string) { m.title = t }

func (m *InterruptedModel) SetSession(sessionID, subPlanID, repoName, worktree string, canAct bool) {
	m.sessionID = sessionID
	m.subPlanID = subPlanID
	m.repoName = repoName
	m.worktree = worktree
	m.canAct = canAct
}

func (m InterruptedModel) KeybindHints() []KeybindHint {
	if m.canAct {
		return []KeybindHint{
			{Key: "r", Label: "Resume"},
			{Key: "a", Label: "Abandon"},
		}
	}
	return []KeybindHint{{Key: "↑↓", Label: "Scroll"}}
}

func (m InterruptedModel) Update(msg tea.Msg) (InterruptedModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if !m.canAct {
			return m, nil
		}
		switch msg.String() {
		case "r":
			sID, spID := m.sessionID, m.subPlanID
			return m, func() tea.Msg {
				return ResumeSessionMsg{OldSessionID: sID, SubPlanID: spID}
			}
		case "a":
			sID := m.sessionID
			return m, func() tea.Msg {
				return ConfirmAbandonMsg{SessionID: sID}
			}
		}
	}
	return m, nil
}

func (m InterruptedModel) View() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}
	header := components.RenderHeaderBlock(m.styles, components.HeaderBlockSpec{
		Title:   m.title + " · Interrupted",
		Width:   m.width,
		Divider: true,
	})

	lines := append(strings.Split(header, "\n"), "")
	lines = append(lines, m.styles.Interrupted.Render("⊘ Session interrupted (previous substrate owner stopped heartbeating while the agent was running)"), "")

	if m.repoName != "" {
		lines = append(lines,
			m.styles.Subtitle.Render(m.repoName+": partial changes in worktree "+m.worktree),
			m.styles.Muted.Render("Run `git status` in the worktree to inspect state."),
			"",
		)
	}

	lines = append(lines,
		m.styles.Subtitle.Render("Resume will start a new agent session in the same worktree with context about"),
		m.styles.Subtitle.Render("the interruption and the original sub-plan."),
		"",
	)

	if !m.canAct {
		lines = append(lines, m.styles.Muted.Render("(Owned by another instance — take over not yet available)"))
	}
	lines = append(lines, components.RenderKeyHints(m.styles, componentHints(m.KeybindHints()), "  "))
	return fitViewBox(strings.Join(lines, "\n"), m.width, m.height)
}

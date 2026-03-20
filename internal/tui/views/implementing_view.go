package views

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/sessionlog"
	"github.com/beeemT/substrate/internal/tui/components"
	"github.com/beeemT/substrate/internal/tui/styles"
)

const (
	keyPgDown = "pgdown"
	keyPgUp   = "pgup"
)

// RepoProgress tracks one repo's implementation status.
type RepoProgress struct {
	Name      string
	SubPlanID string
	SessionID string
	Status    domain.TaskPlanStatus
	LogPath   string
}

// repoStatusIcon returns the display icon for a repo's sub-plan status.
func repoStatusIcon(status domain.TaskPlanStatus, st styles.Styles) string {
	switch status {
	case domain.SubPlanCompleted:
		return st.Success.Render("✓")
	case domain.SubPlanInProgress:
		return st.Active.Render("●")
	case domain.SubPlanFailed:
		return st.Error.Render("✗")
	default:
		return st.Muted.Render("◌")
	}
}

// ImplementingModel renders multi-repo implementation progress.
type ImplementingModel struct { //nolint:recvcheck // Bubble Tea: Update returns value, View on value receiver
	repos            []RepoProgress
	selectedRepo     int
	entryBuffers     map[string][]sessionlog.Entry
	verbose          bool
	collapseThinking bool
	viewports        map[string]viewport.Model
	offsets          map[string]int64
	title            string
	styles           styles.Styles
	width            int
	height           int
	steerInput       textinput.Model
	steerActive      bool
}

// NewImplementingModel constructs an ImplementingModel with the given styles.
func NewImplementingModel(st styles.Styles) ImplementingModel {
	ti := components.NewTextInput()
	ti.Placeholder = "Prompt agent / Follow up..."
	ti.CharLimit = 2000
	return ImplementingModel{
		entryBuffers:     make(map[string][]sessionlog.Entry),
		viewports:        make(map[string]viewport.Model),
		offsets:          make(map[string]int64),
		styles:           st,
		collapseThinking: true,
		steerInput:       ti,
	}
}

// SetSize updates the dimensions of the model and all repo viewports.
func (m *ImplementingModel) SetSize(width, height int) {
	m.width = width
	m.height = height
	vpH := m.viewportHeight()
	for k, vp := range m.viewports {
		vp.Width = width
		vp.Height = vpH
		if entries, ok := m.entryBuffers[k]; ok && len(entries) > 0 {
			vp.SetContent(RenderTranscript(m.styles, entries, width, m.verbose, m.collapseThinking))
			vp.GotoBottom()
		}
		m.viewports[k] = vp
	}
}

func (m ImplementingModel) viewportHeight() int {
	reserved := 4 // header + divider + repo row + repo header
	if m.steerActive {
		reserved += 2 // divider + input row
	}
	return max(1, m.height-reserved)
}

// SetTitle sets the work-item title shown in the implementing view.
func (m *ImplementingModel) SetTitle(t string) { m.title = t }

// SetRepos updates the repo list, initialising viewports for any new repos.
func (m *ImplementingModel) SetRepos(repos []RepoProgress) {
	m.repos = repos
	vpH := m.viewportHeight()
	for _, r := range repos {
		if _, ok := m.viewports[r.Name]; !ok {
			m.viewports[r.Name] = viewport.New(m.width, vpH)
			m.entryBuffers[r.Name] = nil
			m.offsets[r.Name] = 0
		}
	}
	if m.selectedRepo >= len(repos) && len(repos) > 0 {
		m.selectedRepo = 0
	}
}

// selectedRepoName returns the name of the currently selected repo, or empty
// string when there are no repos.
func (m ImplementingModel) selectedRepoName() string {
	if len(m.repos) == 0 || m.selectedRepo >= len(m.repos) {
		return ""
	}

	return m.repos[m.selectedRepo].Name
}

// selectedRepoSessionID returns the session ID of the currently selected repo.
func (m ImplementingModel) selectedRepoSessionID() string {
	if len(m.repos) == 0 || m.selectedRepo >= len(m.repos) {
		return ""
	}
	return m.repos[m.selectedRepo].SessionID
}

// selectedRepoIsRunning returns true if the selected repo's session is in progress.
func (m ImplementingModel) selectedRepoIsRunning() bool {
	if len(m.repos) == 0 || m.selectedRepo >= len(m.repos) {
		return false
	}
	return m.repos[m.selectedRepo].Status == domain.SubPlanInProgress
}

// selectedRepoIsCompleted returns true if the selected repo's session has completed.
func (m ImplementingModel) selectedRepoIsCompleted() bool {
	if len(m.repos) == 0 || m.selectedRepo >= len(m.repos) {
		return false
	}
	return m.repos[m.selectedRepo].Status == domain.SubPlanCompleted
}

func (m ImplementingModel) InputCaptured() bool { return m.steerActive }

// KeybindHints returns the keybind hints for the status bar.
func (m ImplementingModel) KeybindHints() []KeybindHint {
	if m.steerActive {
		return []KeybindHint{
			{Key: "Enter", Label: "Send"},
			{Key: "Esc", Label: "Cancel"},
		}
	}
	hints := []KeybindHint{
		{Key: "Tab", Label: "Cycle repos"},
		{Key: "↑↓", Label: "Scroll"},
		{Key: "f", Label: "Follow tail"},
		{Key: "g", Label: "Go to start"},
		{Key: "v", Label: "Verbose logs"},
	}
	for _, entries := range m.entryBuffers {
		if hasThinkingBlocks(entries) {
			hints = append(hints, KeybindHint{Key: "t", Label: "Toggle thinking"})
			break
		}
	}
	if m.selectedRepoIsRunning() {
		hints = append(hints, KeybindHint{Key: "p", Label: "Prompt agent"})
	} else if m.selectedRepoIsCompleted() {
		hints = append(hints, KeybindHint{Key: "p", Label: "Follow up"})
	}
	return hints
}

// Update handles messages and input for ImplementingModel.
func (m ImplementingModel) Update(msg tea.Msg) (ImplementingModel, tea.Cmd) {
	var cmds []tea.Cmd
	switch msg := msg.(type) {
	case SessionLogLinesMsg:
		for _, r := range m.repos {
			if r.SessionID != msg.SessionID {
				continue
			}
			m.offsets[r.Name] = msg.NextOffset
			m.entryBuffers[r.Name] = append(m.entryBuffers[r.Name], msg.Entries...)
			vp := m.viewports[r.Name]
			wasAtBottom := vp.AtBottom()
			vp.SetContent(RenderTranscript(m.styles, m.entryBuffers[r.Name], m.width, m.verbose, m.collapseThinking))
			if wasAtBottom {
				vp.GotoBottom()
			}
			m.viewports[r.Name] = vp
			if r.LogPath != "" {
				cmds = append(cmds, TailSessionLogCmd(r.LogPath, r.SessionID, msg.NextOffset))
			}

			break
		}

	case tea.KeyMsg:
		if m.steerActive {
			switch msg.String() {
			case "enter":
				text := m.steerInput.Value()
				m.steerInput.SetValue("")
				m.steerActive = false
				m.steerInput.Blur()
				if text != "" {
					sid := m.selectedRepoSessionID()
					if m.selectedRepoIsRunning() {
						cmds = append(cmds, func() tea.Msg {
							return SteerSessionMsg{SessionID: sid, Message: text}
						})
					} else if m.selectedRepoIsCompleted() {
						cmds = append(cmds, func() tea.Msg {
							return FollowUpSessionMsg{TaskID: sid, Feedback: text}
						})
					}
				}
			case "esc":
				if m.steerInput.Value() != "" {
					m.steerInput.SetValue("")
				} else {
					m.steerActive = false
					m.steerInput.Blur()
				}
			default:
				var cmd tea.Cmd
				m.steerInput, cmd = m.steerInput.Update(msg)
				cmds = append(cmds, cmd)
			}
			return m, tea.Batch(cmds...)
		}
		switch msg.String() {
		case "p":
			if m.selectedRepoIsRunning() || m.selectedRepoIsCompleted() {
				m.steerActive = true
				cmds = append(cmds, m.steerInput.Focus())
			}
		case keyTab:
			if len(m.repos) > 0 {
				m.selectedRepo = (m.selectedRepo + 1) % len(m.repos)
			}
		case "g":
			name := m.selectedRepoName()
			if vp, ok := m.viewports[name]; ok {
				vp.GotoTop()
				m.viewports[name] = vp
			}
		case "f":
			name := m.selectedRepoName()
			if vp, ok := m.viewports[name]; ok {
				vp.GotoBottom()
				m.viewports[name] = vp
			}
		case "v":
			m.verbose = !m.verbose
			for name, entries := range m.entryBuffers {
				if len(entries) > 0 {
					vp := m.viewports[name]
					vp.SetContent(RenderTranscript(m.styles, entries, m.width, m.verbose, m.collapseThinking))
					m.viewports[name] = vp
				}
			}
		case "t":
			m.collapseThinking = !m.collapseThinking
			for name, entries := range m.entryBuffers {
				if len(entries) > 0 {
					vp := m.viewports[name]
					vp.SetContent(RenderTranscript(m.styles, entries, m.width, m.verbose, m.collapseThinking))
					m.viewports[name] = vp
				}
			}
		case "up", "k", keyDown, "j", keyPgUp, keyPgDown:
			name := m.selectedRepoName()
			if name != "" {
				vp := m.viewports[name]
				var cmd tea.Cmd
				vp, cmd = vp.Update(msg)
				m.viewports[name] = vp
				cmds = append(cmds, cmd)
			}
		}

	case tea.MouseMsg:
		if msg.Action == tea.MouseActionPress {
			switch msg.Button {
			case tea.MouseButtonWheelUp, tea.MouseButtonWheelDown:
				name := m.selectedRepoName()
				if name != "" {
					vp := m.viewports[name]
					var cmd tea.Cmd
					vp, cmd = vp.Update(msg)
					m.viewports[name] = vp
					cmds = append(cmds, cmd)
				}
			}
		}
	}

	return m, tea.Batch(cmds...)
}

// View renders the implementing view.
func (m ImplementingModel) View() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}

	header := components.RenderHeaderBlock(m.styles, components.HeaderBlockSpec{
		Title:   m.title + " · Implementing",
		Width:   m.width,
		Divider: true,
	})

	var repoParts []string
	for i, r := range m.repos {
		icon := repoStatusIcon(r.Status, m.styles)
		label := m.styles.TabInactive.Render(r.Name)
		if i == m.selectedRepo {
			label = m.styles.TabActive.Render(r.Name)
		}
		repoParts = append(repoParts, icon+" "+label)
	}
	repoRow := m.styles.Label.Render("Repos:  ") + strings.Join(repoParts, "   ")

	selectedName := m.selectedRepoName()
	outputBlock := strings.Repeat("\n", max(0, m.viewportHeight()-1))
	if selectedName != "" {
		dashCount := m.width - len(selectedName) - 5
		dashCount = max(dashCount, 0)
		repoHeader := m.styles.Divider.Render(fmt.Sprintf("─── %s ", selectedName) + strings.Repeat("─", dashCount))
		vp := m.viewports[selectedName]
		outputBlock = repoHeader + "\n" + vp.View()
	}

	parts := append(strings.Split(header, "\n"), repoRow, outputBlock)
	if m.steerActive {
		parts = append(parts, components.RenderDivider(m.styles, m.width), m.steerInput.View())
	}

	return fitViewBox(strings.Join(parts, "\n"), m.width, m.height)
}

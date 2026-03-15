package views

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/sessionlog"
	"github.com/beeemT/substrate/internal/tui/components"
	"github.com/beeemT/substrate/internal/tui/styles"
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
type ImplementingModel struct {
	repos            []RepoProgress
	selectedRepo     int
	entryBuffers     map[string][]sessionlog.Entry
	verbose          bool
	collapseThinking bool
	viewports        map[string]viewport.Model
	offsets          map[string]int64
	paused           bool
	title            string
	styles           styles.Styles
	width            int
	height           int
}

// NewImplementingModel constructs an ImplementingModel with the given styles.
func NewImplementingModel(st styles.Styles) ImplementingModel {
	return ImplementingModel{
		entryBuffers:     make(map[string][]sessionlog.Entry),
		viewports:        make(map[string]viewport.Model),
		offsets:          make(map[string]int64),
		styles:           st,
		collapseThinking: true,
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
		}
		m.viewports[k] = vp
	}
}

func (m ImplementingModel) viewportHeight() int {
	return max(1, m.height-5) // header + divider + repo row + repo header + hints
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

// KeybindHints returns the keybind hints for the status bar.
func (m ImplementingModel) KeybindHints() []KeybindHint {
	return []KeybindHint{
		{Key: "Tab", Label: "Cycle repos"},
		{Key: "↑↓", Label: "Scroll"},
		{Key: "p", Label: "Pause/unpause"},
		{Key: "v", Label: "Verbose logs"},
		{Key: "t", Label: "Toggle thinking"},
	}
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
			vp.SetContent(RenderTranscript(m.styles, m.entryBuffers[r.Name], m.width, m.verbose, m.collapseThinking))
			if !m.paused || r.Name == m.selectedRepoName() {
				vp.GotoBottom()
			}
			m.viewports[r.Name] = vp
			if r.LogPath != "" {
				cmds = append(cmds, TailSessionLogCmd(r.LogPath, r.SessionID, msg.NextOffset))
			}
			break
		}

	case tea.KeyMsg:
		switch msg.String() {
		case "tab":
			if len(m.repos) > 0 {
				m.selectedRepo = (m.selectedRepo + 1) % len(m.repos)
			}
		case "p":
			m.paused = !m.paused
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
		case "up", "k", "down", "j", "pgup", "pgdown":
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
		if dashCount < 0 {
			dashCount = 0
		}
		repoHeader := m.styles.Divider.Render(fmt.Sprintf("─── %s ", selectedName) + strings.Repeat("─", dashCount))
		vp := m.viewports[selectedName]
		outputBlock = repoHeader + "\n" + vp.View()
	}

	hints := components.RenderKeyHints(m.styles, componentHints(m.KeybindHints()), "  ")
	if m.paused {
		hints += m.styles.Warning.Render(" [PAUSED]")
	}

	parts := append(strings.Split(header, "\n"), repoRow, outputBlock, hints)
	return fitViewBox(strings.Join(parts, "\n"), m.width, m.height)
}

package views

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/tui/styles"
)

// RepoProgress tracks one repo's implementation status.
type RepoProgress struct {
	Name      string
	SubPlanID string
	SessionID string
	Status    domain.SubPlanStatus
	LogPath   string
}

// repoStatusIcon returns the display icon for a repo's sub-plan status.
func repoStatusIcon(status domain.SubPlanStatus, st styles.Styles) string {
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
	repos        []RepoProgress
	selectedRepo int
	lineBuffers  map[string][]string
	viewports    map[string]viewport.Model
	offsets      map[string]int64
	paused       bool
	title        string
	styles       styles.Styles
	width        int
	height       int
}

// NewImplementingModel constructs an ImplementingModel with the given styles.
func NewImplementingModel(st styles.Styles) ImplementingModel {
	return ImplementingModel{
		lineBuffers: make(map[string][]string),
		viewports:   make(map[string]viewport.Model),
		offsets:     make(map[string]int64),
		styles:      st,
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
			m.lineBuffers[r.Name] = nil
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
			m.lineBuffers[r.Name] = append(m.lineBuffers[r.Name], msg.Lines...)
			vp := m.viewports[r.Name]
			vp.SetContent(strings.Join(m.lineBuffers[r.Name], "\n"))
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
	}
	return m, tea.Batch(cmds...)
}

// View renders the implementing view.
func (m ImplementingModel) View() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}

	titleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#f0f0f0")).Bold(true)
	dividerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#2d2d44"))
	dimLine := dividerStyle.Render(strings.Repeat("─", m.width))

	header := titleStyle.Render(m.title + " · Implementing")

	// Repo status row.
	var repoParts []string
	for i, r := range m.repos {
		icon := repoStatusIcon(r.Status, m.styles)
		label := r.Name
		if i == m.selectedRepo {
			label = lipgloss.NewStyle().Underline(true).Render(label)
		}
		repoParts = append(repoParts, icon+" "+label)
	}
	repoRow := lipgloss.NewStyle().Foreground(lipgloss.Color("#b0b0b0")).Render("Repos:  ") +
		strings.Join(repoParts, "   ")

	// Output block for the selected repo.
	selectedName := m.selectedRepoName()
	outputBlock := lipgloss.NewStyle().Height(m.viewportHeight()).Render("")
	if selectedName != "" {
		// Guard against negative repeat count from very narrow terminals.
		dashCount := m.width - len(selectedName) - 5
		if dashCount < 0 {
			dashCount = 0
		}
		repoHeader := dividerStyle.Render(
			fmt.Sprintf("─── %s ", selectedName) + strings.Repeat("─", dashCount),
		)
		vp := m.viewports[selectedName]
		outputBlock = repoHeader + "\n" + vp.View()
	}

	pauseLabel := ""
	if m.paused {
		pauseLabel = lipgloss.NewStyle().Foreground(lipgloss.Color("#fbbf24")).Render(" [PAUSED]")
	}
	hints := lipgloss.NewStyle().Foreground(lipgloss.Color("#6b7280")).Render(
		"[Tab] Cycle repos  [↑↓] Scroll  [p] Pause") + pauseLabel

	return fitViewBox(strings.Join([]string{header, dimLine, repoRow, outputBlock, hints}, "\n"), m.width, m.height)
}

package views

import (
	"context"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/gitwork"
	"github.com/beeemT/substrate/internal/service"
	"github.com/beeemT/substrate/internal/tui/styles"
)

// WorkspaceInitModal is shown at startup when no workspace is found.
type WorkspaceInitModal struct {
	cwd          string
	check        domain.WorkspaceHealthCheck
	loading      bool // scanning in progress
	active       bool
	styles       styles.Styles
	width        int
	height       int
	workspaceSvc *service.WorkspaceService
}

// NewWorkspaceInitModal creates a WorkspaceInitModal for the given cwd.
func NewWorkspaceInitModal(cwd string, st styles.Styles, workspaceSvc *service.WorkspaceService) WorkspaceInitModal {
	return WorkspaceInitModal{
		cwd:          cwd,
		loading:      true,
		active:       true,
		styles:       st,
		workspaceSvc: workspaceSvc,
	}
}

// Active reports whether the modal is still visible.
func (m WorkspaceInitModal) Active() bool { return m.active }

// SetSize updates the terminal dimensions for layout.
func (m *WorkspaceInitModal) SetSize(w, h int) { m.width = w; m.height = h }

// ScanCmd returns the Cmd that scans the workspace directory for repos.
func (m WorkspaceInitModal) ScanCmd() tea.Cmd {
	return WorkspaceHealthCheckCmd(m.cwd)
}

// Update handles incoming messages for the modal.
func (m WorkspaceInitModal) Update(msg tea.Msg) (WorkspaceInitModal, tea.Cmd) {
	switch msg := msg.(type) {
	case WorkspaceHealthCheckMsg:
		if msg.Error != nil {
			m.loading = false
			return m, func() tea.Msg { return ErrMsg{Err: msg.Error} }
		}
		m.check = msg.Check
		m.loading = false

	case tea.KeyMsg:
		if m.loading {
			return m, nil
		}
		switch msg.String() {
		case "y", "enter":
			return m, initWorkspaceCmd(m.cwd, m.workspaceSvc)
		case "n", "esc":
			return m, func() tea.Msg { return WorkspaceCancelMsg{} }
		}

	case WorkspaceInitDoneMsg:
		m.active = false
	}
	return m, nil
}

// initWorkspaceCmd creates the .substrate-workspace file and registers in DB.
func initWorkspaceCmd(cwd string, workspaceSvc *service.WorkspaceService) tea.Cmd {
	return func() tea.Msg {
		name := filepath.Base(cwd)
		wsFile, err := gitwork.InitWorkspace(cwd, name)
		if err != nil {
			return ErrMsg{Err: err}
		}
		ws := domain.Workspace{
			ID:       wsFile.ID,
			Name:     wsFile.Name,
			RootPath: cwd,
		}
		if err := workspaceSvc.Create(context.Background(), ws); err != nil {
			return ErrMsg{Err: err}
		}
		return WorkspaceInitDoneMsg{
			WorkspaceID:   wsFile.ID,
			WorkspaceName: wsFile.Name,
			WorkspaceDir:  cwd,
		}
	}
}

// View renders the modal, centered within the terminal window.
func (m WorkspaceInitModal) View() string {
	if !m.active {
		return ""
	}

	w := m.width - 4
	if w < 50 {
		w = 50
	}
	if w > 82 {
		w = 82
	}

	var lines []string
	lines = append(lines,
		m.styles.Title.Render("Initialize Workspace"),
		"",
	)

	if m.loading {
		lines = append(lines,
			m.styles.Muted.Render("Scanning for git-work repos…"),
		)
	} else {
		lines = append(lines,
			m.styles.Subtitle.Render("No workspace found at:"),
			m.styles.Muted.Render("  "+m.cwd),
			"",
			m.styles.Subtitle.Render("Initialize this directory as a Substrate workspace?"),
			"",
			m.styles.Subtitle.Render("This will:"),
			m.styles.Muted.Render("  • Create .substrate-workspace  (workspace identity file)"),
			m.styles.Muted.Render("  • Scan for git-work repos      (directories with .bare/)"),
			m.styles.Muted.Render("  • Warn about plain git clones  (require gw init conversion)"),
			m.styles.Muted.Render("  • Register workspace in        ~/.substrate/state.db"),
			"",
		)

		if len(m.check.GitWorkRepos) > 0 {
			repoNames := make([]string, len(m.check.GitWorkRepos))
			for i, r := range m.check.GitWorkRepos {
				repoNames[i] = filepath.Base(r) + "/"
			}
			lines = append(lines,
				m.styles.Success.Render("git-work repos detected: ")+
					m.styles.Subtitle.Render(strings.Join(repoNames, ", ")),
			)
		} else {
			lines = append(lines, m.styles.Muted.Render("No git-work repos detected."))
		}

		if len(m.check.PlainGitClones) > 0 {
			cloneNames := make([]string, len(m.check.PlainGitClones))
			for i, c := range m.check.PlainGitClones {
				cloneNames[i] = filepath.Base(c) + "/"
			}
			lines = append(lines,
				m.styles.Warning.Render("⚠ Plain git clones (need conversion): ")+
					m.styles.Subtitle.Render(strings.Join(cloneNames, ", ")),
			)
		}

		lines = append(lines,
			"",
			m.styles.KeybindAccent.Render("[y]")+m.styles.Subtitle.Render(" Initialize  ")+
				m.styles.KeybindAccent.Render("[n]")+m.styles.Subtitle.Render(" Cancel"),
		)
	}

	content := strings.Join(lines, "\n")
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#2d2d44")).
		Background(lipgloss.Color("#1a1a2e")).
		Padding(1, 2).
		Width(w)
	return boxStyle.Render(content)
}

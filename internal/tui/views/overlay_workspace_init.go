package views

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/gitwork"
	"github.com/beeemT/substrate/internal/service"
	"github.com/beeemT/substrate/internal/tui/components"
	"github.com/beeemT/substrate/internal/tui/styles"
)

type workspaceInitMode int

const (
	workspaceInitModeCreate workspaceInitMode = iota
	workspaceInitModeNewRepos
)

// WorkspaceInitModal is shown at startup when no workspace is found.
//
//nolint:recvcheck // Bubble Tea convention
type WorkspaceInitModal struct {
	cwd             string
	check           domain.WorkspaceHealthCheck
	loading         bool // scanning in progress
	active          bool
	styles          styles.Styles
	width           int
	height          int
	mode            workspaceInitMode
	gitClient       *gitwork.Client
	workspaceSvc    *service.WorkspaceService
	workspaceClient WorkspaceClient

	// errorText holds an error to display inline instead of dismissing the modal.
	errorText string

	// initProgress tracks repo initialization progress during batch init.
	initProgress struct {
		initialized int
		total       int
		active      bool // true while init is running
	}
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

func (m *WorkspaceInitModal) SetWorkspaceClient(client WorkspaceClient) {
	m.workspaceClient = client
}

// SetMode switches the modal between the create-workspace and new-repos
// modes. In daemon mode the new-repos flow routes through
// WorkspaceClient instead of the local gitwork client.
func (m *WorkspaceInitModal) SetMode(mode workspaceInitMode) {
	m.mode = mode
}

// NewNewReposModal creates a WorkspaceInitModal for the new-repos-detected flow.
func NewNewReposModal(workspaceDir string, st styles.Styles, gitClient *gitwork.Client) WorkspaceInitModal {
	return WorkspaceInitModal{
		cwd:       workspaceDir,
		loading:   true,
		active:    true,
		styles:    st,
		mode:      workspaceInitModeNewRepos,
		gitClient: gitClient,
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
		if m.mode == workspaceInitModeNewRepos && len(msg.Check.PlainGitClones) == 0 {
			return m, func() tea.Msg { return CloseOverlayMsg{} }
		}

	case tea.KeyMsg:
		if m.loading {
			return m, nil
		}
		switch msg.String() {
		case "y", keyEnter:
			if m.mode == workspaceInitModeNewRepos {
				return m, initNewReposCmd(m.gitClient, m.check.PlainGitClones, m.workspaceClient)
			}
			return m, initWorkspaceCmd(m.cwd, m.workspaceSvc, m.workspaceClient)
		case "n", keyEsc:
			if m.mode == workspaceInitModeNewRepos {
				return m, func() tea.Msg { return CloseOverlayMsg{} }
			}
			return m, func() tea.Msg { return WorkspaceCancelMsg{} }
		}

	case WorkspaceInitDoneMsg:
		m.active = false

	case RepoInitProgressMsg:
		m.initProgress.active = true
		m.initProgress.initialized = msg.Initialized
		m.initProgress.total = msg.Total

	case NewReposInitDoneMsg:
		m.active = false

	case ErrMsg:
		// Reset progress state but keep modal open to show the error.
		m.initProgress.active = false
		m.errorText = "Failed to initialize workspace: " + msg.Err.Error()
	}

	return m, nil
}

// initWorkspaceCmd initializes plain git repos, creates the .substrate-workspace file, and registers in DB.
func initWorkspaceCmd(cwd string, workspaceSvc *service.WorkspaceService, clients ...WorkspaceClient) tea.Cmd {
	return func() tea.Msg {
		if len(clients) > 0 && clients[0] != nil {
			name := filepath.Base(cwd)
			workspace, err := clients[0].InitializeWorkspace(context.Background(), cwd, name)
			if err != nil {
				return ErrMsg{Err: err}
			}
			return WorkspaceInitDoneMsg{
				WorkspaceID:   workspace.ID,
				WorkspaceName: workspace.Name,
				WorkspaceDir:  workspace.Dir,
			}
		}
		if workspaceSvc == nil {
			return ErrMsg{Err: errors.New("workspace service is unavailable")}
		}
		name := filepath.Base(cwd)
		wsFile, err := gitwork.InitWorkspace(cwd, name)
		createdWorkspaceFile := err == nil
		if err != nil {
			if !gitwork.IsWorkspaceExists(err) {
				return ErrMsg{Err: err}
			}
			wsFile, err = gitwork.ReadWorkspaceFile(cwd)
			if err != nil {
				return ErrMsg{Err: err}
			}
			if _, getErr := workspaceSvc.Get(context.Background(), wsFile.ID); getErr == nil {
				return WorkspaceInitDoneMsg{
					WorkspaceID:   wsFile.ID,
					WorkspaceName: wsFile.Name,
					WorkspaceDir:  cwd,
				}
			}
		}
		ws := domain.Workspace{
			ID:       wsFile.ID,
			Name:     wsFile.Name,
			RootPath: cwd,
		}
		if err := workspaceSvc.Create(context.Background(), ws); err != nil {
			if createdWorkspaceFile {
				if removeErr := os.Remove(filepath.Join(cwd, gitwork.WorkspaceFileName)); removeErr != nil {
					slog.Warn("failed to remove workspace file on rollback", "error", removeErr)
				}
			}

			return ErrMsg{Err: err}
		}

		if err := workspaceSvc.MarkReady(context.Background(), ws.ID); err != nil {
			slog.Warn("failed to mark workspace ready", "error", err)
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
	w = max(w, 50)
	w = min(w, 82)

	var lines []string

	if m.mode == workspaceInitModeNewRepos {
		lines = append(lines,
			m.styles.Title.Render("New Repositories Detected"),
			"",
		)
		if m.loading {
			lines = append(lines,
				m.styles.Muted.Render("Scanning for new repos…"),
			)
		} else if len(m.check.PlainGitClones) == 0 {
			lines = append(lines, m.styles.Muted.Render("No uninitialized repositories found."))
		} else {
			lines = append(lines,
				m.styles.Subtitle.Render("New plain git repositories were added to this workspace:"),
				"",
			)
			for _, c := range m.check.PlainGitClones {
				lines = append(lines, m.styles.Muted.Render("  • "+filepath.Base(c)+"/"))
			}
			lines = append(lines,
				"",
				m.styles.Muted.Render("Existing git-work repos will not be touched."),
				"",
				m.styles.KeybindAccent.Render("[y]")+m.styles.Subtitle.Render(" Initialize  ")+
					m.styles.KeybindAccent.Render("[n]")+m.styles.Subtitle.Render(" Skip"),
			)
			// Render progress bar if init is running
			if m.initProgress.active {
				lines = append(lines,
					"",
					components.RenderProgressBar(m.styles, m.initProgress.initialized, m.initProgress.total, w-4),
				)
			}
		}
	} else {
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
				m.styles.Muted.Render("  • Detect git-work repos        (directories with .bare/)"),
				m.styles.Muted.Render("  • Convert plain git repos      (child dirs with .git/)"),
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
					m.styles.Warning.Render("⚠ Plain git repos to initialize: ")+
						m.styles.Subtitle.Render(strings.Join(cloneNames, ", ")),
				)
			}

			lines = append(lines,
				"",
				m.styles.KeybindAccent.Render("[y]")+m.styles.Subtitle.Render(" Initialize  ")+
					m.styles.KeybindAccent.Render("[n]")+m.styles.Subtitle.Render(" Cancel"),
			)
		}
	}

	// Render error text if set (e.g., after failed init).
	if m.errorText != "" {
		lines = append(lines, "", m.styles.Muted.Render(m.errorText))
	}

	content := strings.Join(lines, "\n")
	boxStyle := m.styles.OverlayFrame.Padding(1, 2).
		Width(m.styles.Chrome.OverlayFrame.InnerWidth(w))

	return boxStyle.Render(content)
}

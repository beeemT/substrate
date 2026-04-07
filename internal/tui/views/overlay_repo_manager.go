package views

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/beeemT/substrate/internal/gitwork"
	"github.com/beeemT/substrate/internal/tui/components"
	"github.com/beeemT/substrate/internal/tui/styles"
)

// repoManagerSizingSpec uses the shared browse sizing so all overlays align identically.
var repoManagerSizingSpec = browseSizingSpec

// repoManagerFocusArea tracks which pane is focused inside the overlay.
type repoManagerFocusArea int

const (
	repoManagerFocusList    repoManagerFocusArea = iota
	repoManagerFocusDetails                      // right-pane viewport is focused
)

// repoManagerItem adapts managedRepo for the bubbles list widget.
type repoManagerItem struct{ repo managedRepo }

func (i repoManagerItem) Title() string { return i.repo.Name }

func (i repoManagerItem) Description() string {
	switch i.repo.Kind {
	case repoKindPlainGit:
		return filepath.Dir(i.repo.Path) + " · plain git (not usable by substrate)"
	default:
		return filepath.Dir(i.repo.Path)
	}
}

func (i repoManagerItem) FilterValue() string { return i.repo.Name }

// RepoManagerOverlay is the overlay for viewing and managing workspace repositories.
//
// Layout: split screen (left = repo list, right = worktree details or contextual info).
// Key bindings when active:
//
//	r/a        – open Add Repo overlay
//	d          – confirm-delete selected repo
//	i          – init plain git repo with git-work (only for plain git repos)
//	Tab        – toggle focus between list and detail viewport
//	↑/k ↓/j   – navigate list (list focused) or scroll viewport (detail focused)
//	Esc        – close overlay (when no confirm pending)
type RepoManagerOverlay struct { //nolint:recvcheck
	workspaceDir string
	gitClient    *gitwork.Client

	repos    []managedRepo
	repoList list.Model

	// Worktree state for the currently selected repo.
	worktrees       []gitwork.Worktree
	worktreeErr     error
	worktreeLoading bool
	// worktreeReqID is the sequence number for the most recently fired LoadWorktreesCmd.
	// Responses with a different ID are stale and discarded.
	worktreeReqID  int
	detailViewport viewport.Model

	// pendingDelete is non-nil while the delete confirmation prompt is shown.
	pendingDelete *managedRepo
	// pendingInit is non-nil while InitRepoCmd is in flight.
	pendingInit *managedRepo

	focus   repoManagerFocusArea
	loading bool

	styles        styles.Styles
	width, height int
	active        bool
}

// NewRepoManagerOverlay constructs a RepoManagerOverlay with sane defaults.
func NewRepoManagerOverlay(workspaceDir string, gitClient *gitwork.Client, st styles.Styles) RepoManagerOverlay {
	delegate := list.NewDefaultDelegate()
	delegate.ShowDescription = true
	rl := list.New([]list.Item{}, delegate, 60, 10)
	rl.SetShowTitle(false)
	rl.SetShowStatusBar(false)
	rl.SetShowPagination(false)
	rl.SetFilteringEnabled(false)
	rl.SetShowHelp(false)
	rl = components.ApplyOverlayListStyles(rl, st)

	return RepoManagerOverlay{
		workspaceDir:   workspaceDir,
		gitClient:      gitClient,
		repoList:       rl,
		detailViewport: viewport.New(0, 0),
		styles:         st,
	}
}

// Open activates the overlay and triggers an initial workspace scan.
func (m *RepoManagerOverlay) Open() tea.Cmd {
	m.active = true
	m.repos = nil
	m.repoList.SetItems(nil)
	m.repoList.ResetSelected()
	m.worktrees = nil
	m.worktreeErr = nil
	m.worktreeLoading = false
	m.pendingDelete = nil
	m.pendingInit = nil
	m.loading = true
	m.focus = repoManagerFocusList
	return LoadManagedReposCmd(m.workspaceDir)
}

// Close deactivates the overlay and resets transient state.
func (m *RepoManagerOverlay) Close() {
	m.active = false
	m.repos = nil
	m.repoList.SetItems(nil)
	m.repoList.ResetSelected()
	m.worktrees = nil
	m.worktreeErr = nil
	m.worktreeLoading = false
	m.pendingDelete = nil
	m.pendingInit = nil
	m.loading = false
}

// Active reports whether the overlay is currently shown.
func (m RepoManagerOverlay) Active() bool { return m.active }

// SetSize stores the available terminal dimensions and resizes sub-components.
func (m *RepoManagerOverlay) SetSize(w, h int) {
	m.width = w
	m.height = h
	m.syncSizes()
}

func (m *RepoManagerOverlay) syncSizes() {
	layout := m.repoManagerLayout()
	m.repoList.SetWidth(layout.LeftInnerWidth)
	m.repoList.SetHeight(layout.ListHeight)
	m.detailViewport.Width = layout.ViewportWidth
	m.detailViewport.Height = layout.ViewportHeight
}

// repoManagerDefaultHintParts returns the footer hint parts used for both the
// 2-pass layout chrome calculation and the actual footer rendering.
// The [i] init action is shown inline in the detail pane, not in the footer, so
// the footer set is fixed at 4 items regardless of which repo kind is selected.
func repoManagerDefaultHintParts() []string {
	return []string{"[d] delete", "[a] add repo", "[Tab] switch focus", "[Esc] close"}
}

// repoManagerChromeLines returns the number of terminal lines occupied by fixed
// chrome outside the split-pane body (frame borders, header, blank separator, footer).
// renderWidth is the available width for hint text wrapping.
func (m RepoManagerOverlay) repoManagerChromeLines(renderWidth int) int {
	// Frame top border = 1
	// Frame bottom border = 1
	// Header ("Manage Repositories") = 1
	// Blank separator inserted by RenderOverlayFrame after header = 1
	// Footer (hint line, may wrap) = hintLines
	hintLines := hintLineCountForParts(repoManagerDefaultHintParts(), renderWidth)
	return 4 + hintLines
}

// repoManagerLayout returns the split overlay geometry using a 2-pass computation.
// The first pass establishes content width; the second accounts for hint-line wrapping.
func (m RepoManagerOverlay) repoManagerLayout() components.SplitOverlayLayout {
	baseLayout := components.ComputeSplitOverlayLayout(m.width, m.height, 0, repoManagerSizingSpec)
	renderWidth := maxInt(1, baseLayout.ContentWidth-4)
	chromeLines := m.repoManagerChromeLines(renderWidth)
	return components.ComputeSplitOverlayLayout(m.width, m.height, chromeLines, repoManagerSizingSpec)
}

// nextWorktreeRequestID increments the sequence counter and returns the new ID.
func (m *RepoManagerOverlay) nextWorktreeRequestID() int {
	m.worktreeReqID++
	return m.worktreeReqID
}

// selectedRepo returns the currently highlighted repo and whether one exists.
func (m RepoManagerOverlay) selectedRepo() (managedRepo, bool) {
	if len(m.repos) == 0 {
		return managedRepo{}, false
	}
	if item, ok := m.repoList.SelectedItem().(repoManagerItem); ok {
		return item.repo, true
	}
	idx := m.repoList.Index()
	if idx >= 0 && idx < len(m.repos) {
		return m.repos[idx], true
	}
	return managedRepo{}, false
}

// maybeLoadWorktrees fires LoadWorktreesCmd for the currently selected repo.
// It clears any stale worktree data and marks the pane as loading.
func (m *RepoManagerOverlay) maybeLoadWorktrees() tea.Cmd {
	repo, ok := m.selectedRepo()
	if !ok {
		return nil
	}
	m.worktreeLoading = true
	m.worktrees = nil
	m.worktreeErr = nil
	return LoadWorktreesCmd(m.gitClient, repo, m.nextWorktreeRequestID())
}

// Update handles messages and key events for the overlay.
func (m *RepoManagerOverlay) Update(msg tea.Msg) (RepoManagerOverlay, tea.Cmd) {
	if !m.active {
		return *m, nil
	}
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case ManagedReposLoadedMsg:
		m.loading = false
		if msg.Err != nil {
			slog.Error("repo manager: failed to load managed repos", "error", msg.Err)
			return *m, nil
		}
		m.repos = msg.Repos
		items := make([]list.Item, len(m.repos))
		for i, r := range m.repos {
			items[i] = repoManagerItem{repo: r}
		}
		m.repoList.SetItems(items)
		m.repoList.ResetSelected()
		m.worktrees = nil
		m.worktreeErr = nil
		// Load worktrees for the first repo immediately.
		if cmd := m.maybeLoadWorktrees(); cmd != nil {
			cmds = append(cmds, cmd)
		}
		m.syncDetailViewport()
		return *m, tea.Batch(cmds...)

	case WorktreesLoadedMsg:
		// Discard stale responses from a previous selection.
		if msg.RequestID != m.worktreeReqID {
			return *m, nil
		}
		m.worktreeLoading = false
		m.worktrees = msg.Worktrees
		m.worktreeErr = msg.Err
		if msg.Err != nil {
			slog.Error("repo manager: failed to list worktrees", "repo", msg.RepoPath, "error", msg.Err)
		}
		m.syncDetailViewport()
		return *m, nil

	case RepoRemovedMsg:
		if msg.Err != nil {
			// Error toast is shown by app.go; stay open so the user can retry or close.
			return *m, nil
		}
		// Reload to reflect the deletion.
		m.pendingDelete = nil
		m.loading = true
		return *m, LoadManagedReposCmd(m.workspaceDir)

	case RepoInitializedMsg:
		m.pendingInit = nil
		if msg.Err != nil {
			slog.Error("repo manager: failed to initialize repo", "repo", msg.RepoPath, "error", msg.Err)
			return *m, nil
		}
		// Reload so the repo appears as git-work managed.
		m.loading = true
		return *m, LoadManagedReposCmd(m.workspaceDir)

	case tea.KeyMsg:
		return m.handleKey(msg)

	case tea.MouseMsg:
		if msg.Action != tea.MouseActionPress {
			return *m, nil
		}
		switch msg.Button {
		case tea.MouseButtonWheelUp, tea.MouseButtonWheelDown:
			if m.focus == repoManagerFocusList {
				prevIdx := m.repoList.Index()
				var listCmd tea.Cmd
				m.repoList, listCmd = m.repoList.Update(msg)
				cmds = append(cmds, listCmd)
				if m.repoList.Index() != prevIdx {
					if cmd := m.maybeLoadWorktrees(); cmd != nil {
						cmds = append(cmds, cmd)
					}
				}
			} else {
				m.detailViewport, _ = m.detailViewport.Update(msg)
			}
		}
		return *m, tea.Batch(cmds...)
	}

	return *m, nil
}

// handleKey processes keyboard events inside the overlay.
func (m *RepoManagerOverlay) handleKey(msg tea.KeyMsg) (RepoManagerOverlay, tea.Cmd) {
	// Confirm dialog captures all key events while pending.
	if m.pendingDelete != nil {
		switch msg.String() {
		case "y":
			repo := *m.pendingDelete
			m.pendingDelete = nil
			return *m, RemoveRepoCmd(repo.Path)
		default:
			// Any other key (including n, esc) cancels the confirm.
			m.pendingDelete = nil
			return *m, nil
		}
	}

	switch msg.String() {
	case "esc":
		return *m, func() tea.Msg { return CloseOverlayMsg{} }

	case "tab":
		if m.focus == repoManagerFocusList {
			m.focus = repoManagerFocusDetails
		} else {
			m.focus = repoManagerFocusList
		}
		return *m, nil

	case "a":
		// Transition to the Add Repo overlay; app.go handles ShowAddRepoMsg.
		return *m, func() tea.Msg { return ShowAddRepoMsg{} }

	case "d":
		if repo, ok := m.selectedRepo(); ok {
			repo := repo // capture
			m.pendingDelete = &repo
		}
		return *m, nil

	case "i":
		// Initialize a plain git repo with git-work.
		if repo, ok := m.selectedRepo(); ok && repo.Kind == repoKindPlainGit && m.pendingInit == nil {
			repo := repo // capture
			m.pendingInit = &repo
			return *m, InitRepoCmd(m.gitClient, repo.Path)
		}
		return *m, nil

	case "up", "k":
		if m.focus == repoManagerFocusList {
			prevIdx := m.repoList.Index()
			var listCmd tea.Cmd
			m.repoList, listCmd = m.repoList.Update(msg)
			var wtCmd tea.Cmd
			if m.repoList.Index() != prevIdx {
				wtCmd = m.maybeLoadWorktrees()
			}
			return *m, tea.Batch(listCmd, wtCmd)
		}
		m.detailViewport.LineUp(1)
		return *m, nil

	case "down", "j":
		if m.focus == repoManagerFocusList {
			prevIdx := m.repoList.Index()
			var listCmd tea.Cmd
			m.repoList, listCmd = m.repoList.Update(msg)
			var wtCmd tea.Cmd
			if m.repoList.Index() != prevIdx {
				wtCmd = m.maybeLoadWorktrees()
			}
			return *m, tea.Batch(listCmd, wtCmd)
		}
		m.detailViewport.LineDown(1)
		return *m, nil
	}

	// Forward remaining keys to the focused component.
	if m.focus == repoManagerFocusList {
		prevIdx := m.repoList.Index()
		var listCmd tea.Cmd
		m.repoList, listCmd = m.repoList.Update(msg)
		var wtCmd tea.Cmd
		if m.repoList.Index() != prevIdx {
			wtCmd = m.maybeLoadWorktrees()
		}
		return *m, tea.Batch(listCmd, wtCmd)
	}
	var vpCmd tea.Cmd
	m.detailViewport, vpCmd = m.detailViewport.Update(msg)
	return *m, vpCmd
}

// syncDetailViewport rebuilds the right-pane viewport content.
// Call after any state change that affects the detail pane.
func (m *RepoManagerOverlay) syncDetailViewport() {
	layout := m.repoManagerLayout()
	m.detailViewport.Width = layout.ViewportWidth
	m.detailViewport.Height = layout.ViewportHeight
	m.detailViewport.SetContent(m.renderDetailContent(layout.ViewportWidth))
	m.detailViewport.GotoTop()
}

// renderDetailContent produces the text content for the right-pane viewport.
func (m RepoManagerOverlay) renderDetailContent(width int) string {
	if width < 10 {
		width = 10
	}

	repo, hasRepo := m.selectedRepo()

	// Confirm-delete prompt replaces the detail content.
	if m.pendingDelete != nil {
		return m.renderConfirmContent(width, *m.pendingDelete)
	}

	if !hasRepo {
		if m.loading {
			return m.styles.Muted.Render("Loading repositories…")
		}
		return m.styles.Muted.Render("No repositories are managed by substrate in this workspace.\n\nPress [a] to add a repository.")
	}

	// Plain git repos get a dedicated warning view.
	if repo.Kind == repoKindPlainGit {
		return m.renderPlainGitContent(width, repo)
	}

	// Git-work repo: show worktrees.
	return m.renderWorktreesContent(width, repo)
}

// renderConfirmContent renders the delete confirmation prompt.
func (m RepoManagerOverlay) renderConfirmContent(width int, repo managedRepo) string {
	var lines []string

	title := m.styles.Error.Render("Delete Repository")
	lines = append(lines, title, "")

	msg := fmt.Sprintf("Delete %q and all its worktrees?", repo.Name)
	lines = append(lines, ansi.Hardwrap(m.styles.Subtitle.Render(msg), width, true))
	lines = append(lines, "")
	lines = append(lines, m.styles.Muted.Render("This cannot be undone. All local changes will be lost."))
	lines = append(lines, "")
	lines = append(lines,
		m.styles.KeybindAccent.Render("[y]")+m.styles.Hint.Render(" Delete")+
			"   "+
			m.styles.KeybindAccent.Render("[n/Esc]")+m.styles.Hint.Render(" Cancel"),
	)

	return strings.Join(lines, "\n")
}

// renderPlainGitContent renders the info panel for a plain git repository.
func (m RepoManagerOverlay) renderPlainGitContent(width int, repo managedRepo) string {
	var lines []string

	warning := m.styles.Warning.Render("⚠  Not managed by substrate")
	lines = append(lines, warning, "")

	desc := "This is a plain git repository. It has not been initialized with git-work and cannot be used by substrate for session management."
	lines = append(lines, ansi.Hardwrap(m.styles.Muted.Render(desc), width, true))
	lines = append(lines, "")

	label := m.styles.Label.Render("Path: ")
	lines = append(lines, label+m.styles.SettingsText.Render(repo.Path))
	lines = append(lines, "")

	if m.pendingInit != nil && m.pendingInit.Path == repo.Path {
		lines = append(lines, m.styles.Muted.Render("Initializing with git-work…"))
	} else {
		lines = append(lines,
			m.styles.KeybindAccent.Render("[i]")+m.styles.Hint.Render(" Initialize with git-work")+
				"  "+
				m.styles.KeybindAccent.Render("[d]")+m.styles.Hint.Render(" Delete"),
		)
	}

	return strings.Join(lines, "\n")
}

// renderWorktreesContent renders the worktree list for a git-work repository.
func (m RepoManagerOverlay) renderWorktreesContent(width int, repo managedRepo) string {
	var lines []string

	label := m.styles.Label.Render("Path: ")
	lines = append(lines, label+ansi.Truncate(m.styles.SettingsText.Render(repo.Path), width, "…"))
	lines = append(lines, "")

	switch {
	case m.worktreeLoading:
		lines = append(lines, m.styles.Muted.Render("Loading worktrees…"))
	case m.worktreeErr != nil:
		errMsg := "Failed to list worktrees: " + m.worktreeErr.Error()
		lines = append(lines, ansi.Hardwrap(m.styles.Error.Render(errMsg), width, true))
	case len(m.worktrees) == 0:
		lines = append(lines, m.styles.Muted.Render("No worktrees found."))
	default:
		heading := m.styles.Subtitle.Render(fmt.Sprintf("Worktrees (%d):", len(m.worktrees)))
		lines = append(lines, heading, "")
		for _, wt := range m.worktrees {
			branchLine := m.renderWorktreeBranch(wt, width)
			lines = append(lines, branchLine)
			pathLine := "  " + ansi.Truncate(m.styles.Muted.Render(wt.Path), maxInt(1, width-2), "…")
			lines = append(lines, pathLine, "")
		}
	}

	return strings.Join(lines, "\n")
}

// renderWorktreeBranch renders a single worktree branch line with optional "(main)" badge.
func (m RepoManagerOverlay) renderWorktreeBranch(wt gitwork.Worktree, width int) string {
	branch := m.styles.Accent.Render(wt.Branch)
	if wt.IsMain {
		badge := m.styles.Muted.Render(" (main)")
		available := maxInt(1, width-lipgloss.Width(ansi.Strip(badge)))
		return ansi.Truncate(branch, available, "…") + badge
	}
	return ansi.Truncate(branch, width, "…")
}

// hintText returns the footer hint string.
// The [i] init action for plain git repos is surfaced in the detail pane, not the footer,
// so the footer always uses the same fixed parts (matching repoManagerDefaultHintParts).
// The confirm-delete state overrides the footer with a short prompt.
func (m RepoManagerOverlay) hintText(width int) string {
	if m.pendingDelete != nil {
		return wrapHintParts([]string{"[y] confirm delete", "[n/Esc] cancel"}, width)
	}
	return wrapHintParts(repoManagerDefaultHintParts(), width)
}

// View renders the full overlay; returns empty when inactive.
func (m *RepoManagerOverlay) View() string {
	if !m.active {
		return ""
	}

	layout := m.repoManagerLayout()
	m.syncSizes()
	m.syncDetailViewport()

	renderWidth := maxInt(1, layout.ContentWidth-4)

	// Left pane content.
	var leftContent string
	if m.loading && len(m.repos) == 0 {
		leftContent = m.styles.Muted.Render("Loading…")
	} else {
		leftContent = m.repoList.View()
	}

	// Right pane title.
	rightTitle := "Details"
	if m.pendingDelete != nil {
		rightTitle = "Confirm Delete"
	} else if repo, ok := m.selectedRepo(); ok {
		if repo.Kind == repoKindPlainGit {
			rightTitle = "Plain Git Repository"
		} else {
			rightTitle = repo.Name
		}
	}

	body := components.RenderSplitOverlayBody(m.styles, layout, components.SplitOverlaySpec{
		LeftPane: components.OverlayPaneSpec{
			Title:        "Repositories",
			Body:         leftContent,
			DividerWidth: layout.LeftInnerWidth,
			Focused:      m.focus == repoManagerFocusList,
		},
		RightPane: components.OverlayPaneSpec{
			Title:        rightTitle,
			Body:         m.detailViewport.View(),
			DividerWidth: layout.RightInnerWidth,
			Focused:      m.focus == repoManagerFocusDetails,
		},
	})

	footer := m.styles.Hint.Render(m.hintText(renderWidth))

	return components.RenderOverlayFrame(m.styles, layout.FrameWidth, components.OverlayFrameSpec{
		HeaderLines: []string{m.styles.Title.Render("Manage Repositories")},
		Body:        body,
		Footer:      footer,
		Focused:     true,
	})
}

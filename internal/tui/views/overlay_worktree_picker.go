package views

import (
	"log/slog"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/beeemT/substrate/internal/gitwork"
	"github.com/beeemT/substrate/internal/tui/components"
	"github.com/beeemT/substrate/internal/tui/styles"
)

// worktreePickerSizingSpec uses the shared browse sizing so all overlays align identically.
var worktreePickerSizingSpec = browseSizingSpec

// worktreePickerItem adapts gitwork.Worktree for the bubbles list widget.
type worktreePickerItem struct{ worktree gitwork.Worktree }

func (i worktreePickerItem) Title() string { return i.worktree.Branch }

func (i worktreePickerItem) Description() string {
	if i.worktree.IsMain {
		return i.worktree.Path + " · main"
	}
	return i.worktree.Path
}

func (i worktreePickerItem) FilterValue() string { return i.worktree.Branch }

// WorktreePickerOverlay is a split-pane picker for selecting a worktree to open in terminal.
type WorktreePickerOverlay struct {
	workspaceDir string
	gitClient    *gitwork.Client

	repos        []managedRepo
	repoList     list.Model
	worktrees    []gitwork.Worktree
	worktreeList list.Model

	picker components.SplitListPicker

	worktreeErr     error
	worktreeLoading bool
	worktreeReqID   int

	loading bool
	styles  styles.Styles
	width   int
	height  int
	active  bool
}

// NewWorktreePickerOverlay constructs a WorktreePickerOverlay with sane defaults.
func NewWorktreePickerOverlay(workspaceDir string, gitClient *gitwork.Client, st styles.Styles) WorktreePickerOverlay {
	delegate := list.NewDefaultDelegate()
	delegate.ShowDescription = true
	rl := list.New([]list.Item{}, delegate, 60, 10)
	rl.SetShowTitle(false)
	rl.SetShowStatusBar(false)
	rl.SetShowPagination(false)
	rl.SetFilteringEnabled(false)
	rl.SetShowHelp(false)
	rl = components.ApplyOverlayListStyles(rl, st)

	wl := list.New([]list.Item{}, delegate, 60, 10)
	wl.SetShowTitle(false)
	wl.SetShowStatusBar(false)
	wl.SetShowPagination(false)
	wl.SetFilteringEnabled(false)
	wl.SetShowHelp(false)
	wl = components.ApplyOverlayListStyles(wl, st)

	return WorktreePickerOverlay{
		workspaceDir: workspaceDir,
		gitClient:    gitClient,
		repoList:     rl,
		worktreeList: wl,
		picker:       components.NewSplitListPicker(worktreePickerSizingSpec),
		styles:       st,
	}
}

// Open activates the overlay and triggers an initial workspace scan.
func (m *WorktreePickerOverlay) Open() tea.Cmd {
	m.active = true
	m.repos = nil
	m.repoList.SetItems(nil)
	m.repoList.ResetSelected()
	m.worktrees = nil
	m.worktreeList.SetItems(nil)
	m.worktreeList.ResetSelected()
	m.worktreeErr = nil
	m.worktreeLoading = false
	m.loading = true
	m.picker.FocusLeft()
	return LoadManagedReposCmd(m.workspaceDir)
}

// Close deactivates the overlay and resets transient state.
func (m *WorktreePickerOverlay) Close() {
	m.active = false
	m.repos = nil
	m.repoList.SetItems(nil)
	m.repoList.ResetSelected()
	m.worktrees = nil
	m.worktreeList.SetItems(nil)
	m.worktreeList.ResetSelected()
	m.worktreeErr = nil
	m.worktreeLoading = false
}

// Active reports whether the overlay is currently shown.
func (m WorktreePickerOverlay) Active() bool { return m.active }

// SetSize stores the available terminal dimensions and resizes sub-components.
func (m *WorktreePickerOverlay) SetSize(w, h int) {
	m.width = w
	m.height = h
	m.syncSizes()
}

func (m *WorktreePickerOverlay) syncSizes() {
	layout := m.worktreePickerLayout()
	m.picker.SetSize(m.width, m.height, m.worktreePickerChromeLines(layout.ContentWidth-4))
	layout = m.picker.Layout()
	m.repoList.SetWidth(layout.LeftInnerWidth)
	m.repoList.SetHeight(layout.ListHeight)
	m.worktreeList.SetWidth(layout.RightInnerWidth)
	m.worktreeList.SetHeight(layout.ListHeight)
}

// worktreePickerDefaultHintParts returns the footer hint parts used for both the
// 2-pass layout chrome calculation and the actual footer rendering.
func worktreePickerDefaultHintParts() []string {
	return []string{"[t/Enter] open terminal", "[Tab] switch focus", "[Esc] close"}
}

// worktreePickerChromeLines returns the number of terminal lines occupied by fixed
// chrome outside the split-pane body (frame borders, header, blank separator, footer).
// renderWidth is the available width for hint text wrapping.
func (m WorktreePickerOverlay) worktreePickerChromeLines(renderWidth int) int {
	// Frame top border = 1
	// Frame bottom border = 1
	// Header ("Open Terminal") = 1
	// Blank separator inserted by RenderOverlayFrame after header = 1
	// Footer (hint line, may wrap) = hintLines
	hintLines := hintLineCountForParts(worktreePickerDefaultHintParts(), renderWidth)
	return 4 + hintLines
}

// worktreePickerLayout returns the split overlay geometry using a 2-pass computation.
// The first pass establishes content width; the second accounts for hint-line wrapping.
func (m WorktreePickerOverlay) worktreePickerLayout() components.SplitOverlayLayout {
	baseLayout := components.ComputeSplitOverlayLayout(m.width, m.height, 0, worktreePickerSizingSpec)
	renderWidth := maxInt(1, baseLayout.ContentWidth-4)
	chromeLines := m.worktreePickerChromeLines(renderWidth)
	return components.ComputeSplitOverlayLayout(m.width, m.height, chromeLines, worktreePickerSizingSpec)
}

// nextWorktreeRequestID increments the sequence counter and returns the new ID.
func (m *WorktreePickerOverlay) nextWorktreeRequestID() int {
	m.worktreeReqID++
	return m.worktreeReqID
}

// maybeLoadWorktrees fires LoadWorktreesCmd for the currently selected repo.
func (m *WorktreePickerOverlay) maybeLoadWorktrees() tea.Cmd {
	idx := m.repoList.Index()
	if idx < 0 || idx >= len(m.repos) {
		return nil
	}
	repo := m.repos[idx]
	m.worktrees = nil
	m.worktreeList.SetItems(nil)
	m.worktreeErr = nil
	m.worktreeLoading = true
	return LoadWorktreesCmd(m.gitClient, repo, m.nextWorktreeRequestID(), WorktreeLoadTargetPicker)
}

// Update handles messages and key events for the overlay.
func (m *WorktreePickerOverlay) Update(msg tea.Msg) (WorktreePickerOverlay, tea.Cmd) {
	if !m.active {
		return *m, nil
	}
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case ManagedReposLoadedMsg:
		m.loading = false
		if msg.Err != nil {
			slog.Error("worktree picker: failed to load managed repos", "error", msg.Err)
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
		m.worktreeList.SetItems(nil)
		m.worktreeErr = nil
		// Load worktrees for the first repo immediately.
		if cmd := m.maybeLoadWorktrees(); cmd != nil {
			cmds = append(cmds, cmd)
		}
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
			slog.Error("worktree picker: failed to list worktrees", "repo", msg.RepoPath, "error", msg.Err)
		}
		// Update the worktree list
		wtItems := make([]list.Item, len(m.worktrees))
		for i, wt := range m.worktrees {
			wtItems[i] = worktreePickerItem{worktree: wt}
		}
		m.worktreeList.SetItems(wtItems)
		m.worktreeList.ResetSelected()
		return *m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case tea.MouseMsg:
		if msg.Action != tea.MouseActionPress {
			return *m, nil
		}
		switch msg.Button {
		case tea.MouseButtonWheelUp, tea.MouseButtonWheelDown:
			if m.picker.IsFocusLeft() {
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
				m.worktreeList, _ = m.worktreeList.Update(msg)
			}
		}
		return *m, tea.Batch(cmds...)
	}

	return *m, nil
}

// handleKey processes keyboard events inside the overlay.
func (m *WorktreePickerOverlay) handleKey(msg tea.KeyMsg) (WorktreePickerOverlay, tea.Cmd) {
	switch msg.String() {
	case "esc":
		return *m, func() tea.Msg { return CloseOverlayMsg{} }

	case "tab", "left", "right":
		m.picker.HandleFocusKey(msg.String())
		return *m, nil

	case "t", "enter":
		return *m, m.openTerminalCmd()

	case "up":
		if m.picker.IsFocusLeft() {
			prevIdx := m.repoList.Index()
			var listCmd tea.Cmd
			m.repoList, listCmd = m.repoList.Update(msg)
			var wtCmd tea.Cmd
			if m.repoList.Index() != prevIdx {
				wtCmd = m.maybeLoadWorktrees()
			}
			return *m, tea.Batch(listCmd, wtCmd)
		}
		m.worktreeList, _ = m.worktreeList.Update(msg)
		return *m, nil

	case "down":
		if m.picker.IsFocusLeft() {
			prevIdx := m.repoList.Index()
			var listCmd tea.Cmd
			m.repoList, listCmd = m.repoList.Update(msg)
			var wtCmd tea.Cmd
			if m.repoList.Index() != prevIdx {
				wtCmd = m.maybeLoadWorktrees()
			}
			return *m, tea.Batch(listCmd, wtCmd)
		}
		m.worktreeList, _ = m.worktreeList.Update(msg)
		return *m, nil
	}

	// Forward remaining keys to the focused component.
	if m.picker.IsFocusLeft() {
		prevIdx := m.repoList.Index()
		var listCmd tea.Cmd
		m.repoList, listCmd = m.repoList.Update(msg)
		var wtCmd tea.Cmd
		if m.repoList.Index() != prevIdx {
			wtCmd = m.maybeLoadWorktrees()
		}
		return *m, tea.Batch(listCmd, wtCmd)
	}
	var wtCmd tea.Cmd
	m.worktreeList, wtCmd = m.worktreeList.Update(msg)
	return *m, wtCmd
}

// openTerminalCmd returns the command to open a terminal in the selected worktree.
func (m *WorktreePickerOverlay) openTerminalCmd() tea.Cmd {
	if m.worktreeLoading || len(m.worktrees) == 0 {
		return nil
	}
	selectedIdx := m.worktreeList.Index()
	if selectedIdx < 0 || selectedIdx >= len(m.worktrees) {
		return nil
	}
	path := m.worktrees[selectedIdx].Path
	return func() tea.Msg {
		return OpenTerminalInWorktreeMsg{WorktreePath: path}
	}
}

// hintText returns the footer hint string.
func (m WorktreePickerOverlay) hintText(width int) string {
	return wrapHintParts(worktreePickerDefaultHintParts(), width)
}

// View renders the full overlay; returns empty when inactive.
func (m *WorktreePickerOverlay) View() string {
	if !m.active {
		return ""
	}

	layout := m.worktreePickerLayout()
	m.syncSizes()

	renderWidth := maxInt(1, layout.ContentWidth-4)

	// Left pane content.
	var leftContent string
	if m.loading && len(m.repos) == 0 {
		leftContent = m.styles.Muted.Render("Loading…")
	} else if len(m.repos) == 0 {
		leftContent = m.styles.Muted.Render("No repositories found.\n\nAdd a repository first.")
	} else {
		leftContent = m.repoList.View()
	}

	// Right pane content.
	var rightContent string
	if m.worktreeLoading {
		rightContent = m.styles.Muted.Render("Loading worktrees…")
	} else if m.worktreeErr != nil {
		rightContent = m.styles.Error.Render("Failed to load worktrees:\n" + m.worktreeErr.Error())
	} else if len(m.worktrees) == 0 {
		rightContent = m.styles.Muted.Render("No worktrees found.")
	} else {
		rightContent = m.worktreeList.View()
	}

	body := m.picker.View(m.styles, components.SplitListPaneSpec{
		Title: "Repositories",
		Body:  leftContent,
	}, components.SplitListPaneSpec{
		Title: "Worktrees",
		Body:  rightContent,
	})

	footer := m.styles.Hint.Render(m.hintText(renderWidth))

	return components.RenderOverlayFrame(m.styles, layout.FrameWidth, components.OverlayFrameSpec{
		HeaderLines: []string{m.styles.Title.Render("Open Terminal")},
		Body:        body,
		Footer:      footer,
		Focused:     true,
	})
}

package views

import (
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/charmbracelet/x/ansi"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/gitwork"
	"github.com/beeemT/substrate/internal/tui/components"
	"github.com/beeemT/substrate/internal/tui/styles"
)

// addRepoFocusArea tracks which sub-control is active inside the overlay.
type addRepoFocusArea int

const (
	addRepoFocusControls addRepoFocusArea = iota
	addRepoFocusList
	addRepoFocusDetails
)

const (
	addRepoPageSize = 30
)

// addRepoControl tracks which specific control within addRepoFocusControls is active.
type addRepoControl int

const (
	addRepoControlSource addRepoControl = iota
	addRepoControlSearch
)

var addRepoSizingSpec = components.SplitOverlaySizingSpec{
	MaxOverlayWidth:   0,
	LeftMinWidth:      36,
	RightMinWidth:     44,
	LeftWeight:        2,
	RightWeight:       3,
	MinBodyHeight:     8,
	DefaultBodyHeight: 24,
	HeightRatioNum:    3,
	HeightRatioDen:    5,
	InputWidthOffset:  20,
}

// AddRepoOverlay is the overlay for browsing and cloning remote repositories.
type AddRepoOverlay struct { //nolint:recvcheck
	sources        []adapter.RepoSource
	sourceIndex    int
	gitClient      *gitwork.Client
	workspaceDir   string
	searchInput    textinput.Model
	repoList       list.Model
	allRepos       []adapter.RepoItem
	loading        bool
	hasMore        bool
	manualURL      textinput.Model
	showManual     bool
	cloning        bool
	cloneError     string
	detailViewport viewport.Model
	detailRepo     *adapter.RepoItem
	focus          addRepoFocusArea
	addRepoCtrl    addRepoControl
	styles         styles.Styles
	width          int
	height         int
	active         bool
}

// repoItem adapts adapter.RepoItem for the bubbles list widget.
type repoItem struct {
	item adapter.RepoItem
}

func (i repoItem) Title() string { return i.item.FullName }

func (i repoItem) Description() string {
	parts := make([]string, 0, 2)
	if i.item.IsPrivate {
		parts = append(parts, "Private")
	}
	if i.item.DefaultBranch != "" {
		parts = append(parts, i.item.DefaultBranch)
	}
	return strings.Join(parts, " · ")
}

func (i repoItem) FilterValue() string {
	return strings.Join([]string{i.item.FullName, i.item.Description, i.item.Owner}, " ")
}

// NewAddRepoOverlay constructs an AddRepoOverlay with sane defaults.
func NewAddRepoOverlay(sources []adapter.RepoSource, workspaceDir string, gitClient *gitwork.Client, st styles.Styles) AddRepoOverlay {
	si := components.NewTextInput()
	si.Placeholder = "Search repositories…"
	si.CharLimit = 200

	mu := components.NewTextInput()
	mu.Placeholder = "Paste git clone URL (https:// or git@)…"
	mu.CharLimit = 500

	delegate := list.NewDefaultDelegate()
	delegate.ShowDescription = true
	rl := list.New([]list.Item{}, delegate, 60, 10)
	rl.Title = "Repositories"
	rl.SetShowTitle(false)
	rl.SetShowStatusBar(false)
	rl.SetShowPagination(false)
	rl.SetFilteringEnabled(true)
	rl.SetShowHelp(false)
	rl = components.ApplyOverlayListStyles(rl, st)

	return AddRepoOverlay{
		sources:        sources,
		gitClient:      gitClient,
		workspaceDir:   workspaceDir,
		searchInput:    si,
		repoList:       rl,
		manualURL:      mu,
		detailViewport: viewport.New(0, 0),
		focus:          addRepoFocusControls,
		styles:         st,
	}
}

// Open activates the overlay and resets transient state.
func (m *AddRepoOverlay) Open() tea.Cmd {
	m.active = true
	m.showManual = false
	m.searchInput.SetValue("")
	m.manualURL.SetValue("")
	m.cloneError = ""
	m.cloning = false
	m.manualURL.Blur()
	m.setAddRepoControlFocus(addRepoControlSearch)
	return m.reloadRepos()
}

// Close deactivates the overlay and clears all transient state.
func (m *AddRepoOverlay) Close() {
	m.active = false
	m.searchInput.SetValue("")
	m.searchInput.Blur()
	m.manualURL.SetValue("")
	m.manualURL.Blur()
	m.allRepos = nil
	m.repoList.ResetSelected()
	m.repoList.SetItems(nil)
	m.loading = false
	m.hasMore = false
	m.cloning = false
	m.cloneError = ""
	m.showManual = false
	m.detailViewport.SetContent("")
	m.detailViewport.YOffset = 0
	m.detailRepo = nil
	m.focus = addRepoFocusControls
	m.addRepoCtrl = addRepoControlSearch
}

// Active reports whether the overlay is currently shown.
func (m AddRepoOverlay) Active() bool { return m.active }

// SetSize stores the available terminal dimensions for responsive rendering.
func (m *AddRepoOverlay) SetSize(w, h int) {
	m.width = w
	m.height = h
	m.syncDetailViewport(false)
}

// currentListItem returns the currently selected repo, if any.
func (m AddRepoOverlay) currentListItem() (adapter.RepoItem, bool) {
	if len(m.allRepos) == 0 {
		return adapter.RepoItem{}, false
	}
	if selected, ok := m.repoList.SelectedItem().(repoItem); ok {
		return selected.item, true
	}
	index := m.repoList.Index()
	if index >= 0 && index < len(m.allRepos) {
		return m.allRepos[index], true
	}
	return m.allRepos[0], true
}

// syncListItems rebuilds the bubbles list from allRepos.
func (m *AddRepoOverlay) syncListItems() {
	items := make([]list.Item, len(m.allRepos))
	for i, r := range m.allRepos {
		items[i] = repoItem{item: r}
	}
	m.repoList.SetItems(items)
}

// syncDetailViewport updates the detail pane content based on the selected repo.
func (m *AddRepoOverlay) syncDetailViewport(forceTop bool) {
	if m.showManual {
		return
	}
	layout := m.browserLayout()
	m.resizeInputs(layout.InputWidth)
	m.detailViewport.Width = layout.ViewportWidth
	m.detailViewport.Height = layout.ViewportHeight

	item, ok := m.currentListItem()
	if !ok {
		content := m.styles.Muted.Render(
			"No repository selected yet.\n\nUse the list to browse available repos, switch to details for more context, or press Ctrl+N to paste a clone URL.")
		m.detailViewport.SetContent(ansi.Hardwrap(content, layout.ViewportWidth, true))
		m.detailViewport.GotoTop()
		m.detailRepo = nil
		return
	}
	if !forceTop && m.detailRepo != nil && item.FullName == m.detailRepo.FullName {
		return
	}
	content := m.renderDetailContent(item, layout.ViewportWidth)
	m.detailViewport.SetContent(content)
	m.detailViewport.GotoTop()
	m.detailRepo = &item
}

// renderDetailContent formats repository metadata for the detail viewport.
func (m AddRepoOverlay) renderDetailContent(item adapter.RepoItem, width int) string {
	if width < 20 {
		width = 20
	}

	labelStyle := m.styles.Label
	valueStyle := m.styles.SettingsText
	linkStyle := m.styles.Link

	var access string
	if item.IsPrivate {
		access = "Private"
	} else {
		access = "Public"
	}

	rows := make([]string, 0, 7)
	add := func(label, value string, style lipgloss.Style) {
		if strings.TrimSpace(value) == "" {
			return
		}
		line := labelStyle.Render(label+": ") + style.Render(value)
		rows = append(rows, ansi.Hardwrap(line, width, true))
	}

	add("Name", item.FullName, valueStyle)
	add("Source", item.Source, valueStyle)
	add("Branch", item.DefaultBranch, valueStyle)
	add("Access", access, valueStyle)
	add("Clone", item.URL, linkStyle)
	add("SSH", item.SSHURL, linkStyle)

	var desc string
	if strings.TrimSpace(item.Description) != "" {
		desc = "\n" + ansi.Hardwrap(item.Description, width, true)
	}

	return strings.Join(rows, "\n") + desc
}

// reloadRepos triggers a fresh repo listing from the current source.
func (m *AddRepoOverlay) reloadRepos() tea.Cmd {
	m.loading = true
	m.allRepos = nil
	m.repoList.SetItems(nil)
	return LoadReposCmd(m.sources, m.sourceIndex, m.searchInput.Value(), addRepoPageSize)
}

// browserLayout computes the split overlay geometry (2-pass like NewSession).
func (m AddRepoOverlay) browserLayout() components.SplitOverlayLayout {
	baseLayout := components.ComputeSplitOverlayLayout(m.width, m.height, 0, addRepoSizingSpec)
	chromeLines := m.browserChromeLines(maxInt(1, baseLayout.ContentWidth-4))
	return components.ComputeSplitOverlayLayout(m.width, m.height, chromeLines, addRepoSizingSpec)
}

// browserChromeLines counts all lines outside the pane body.
func (m AddRepoOverlay) browserChromeLines(renderWidth int) int {
	// Frame borders (top + bottom) = 2
	// Blank separator between header and body (added by RenderOverlayFrame) = 1
	// Header: title + source labels = 2 lines
	// Search row at top of browserView = 1
	// Divider line below search row = 1
	// Hint footer = 1+ lines
	hintLines := addRepoHintLineCount(renderWidth)
	return 2 + 1 + 2 + 1 + 1 + hintLines
}

// resizeInputs sets the input widths to the layout input width.
func (m *AddRepoOverlay) resizeInputs(inputWidth int) {
	inputWidth = maxInt(1, inputWidth)
	m.searchInput.Width = inputWidth
	m.manualURL.Width = inputWidth
}

// isAddRepoControlFocused reports whether a specific control within addRepoFocusControls is active.
func (m AddRepoOverlay) isAddRepoControlFocused(control addRepoControl) bool {
	return m.focus == addRepoFocusControls && m.addRepoCtrl == control
}

// controlLabel renders the label string with the focused style when the given control is active,
// matching the same visual convention used by NewSessionOverlay.
func (m AddRepoOverlay) controlLabel(label string, control addRepoControl) string {
	style := m.styles.Label
	if m.isAddRepoControlFocused(control) {
		style = m.styles.Title
	}
	return style.Render(label)
}

// setAddRepoControlFocus transitions focus into the controls area and activates the specified sub-control.
func (m *AddRepoOverlay) setAddRepoControlFocus(control addRepoControl) {
	m.focus = addRepoFocusControls
	m.addRepoCtrl = control
	switch control {
	case addRepoControlSearch:
		m.searchInput.Focus()
	default:
		m.searchInput.Blur()
	}
}

// moveAddRepoFocus advances or retreats focus by delta steps through the controls→list chain.
// Returns true when it consumed the event (caller should not forward to list/detail).
func (m *AddRepoOverlay) moveAddRepoFocus(delta int) bool {
	controls := []addRepoControl{addRepoControlSource, addRepoControlSearch}
	if m.focus == addRepoFocusList {
		// At the top of the list, retreat back to the last control.
		if delta < 0 && m.repoList.Index() == 0 {
			m.setAddRepoControlFocus(controls[len(controls)-1])
			return true
		}
		return false
	}
	if m.focus == addRepoFocusDetails {
		return false
	}
	// Currently in controls — find current position.
	currentIndex := 0
	for i, c := range controls {
		if c == m.addRepoCtrl {
			currentIndex = i
			break
		}
	}
	nextIndex := currentIndex + delta
	switch {
	case nextIndex < 0:
		// Clamp at the first control rather than wrapping past the top.
		m.setAddRepoControlFocus(controls[0])
	case nextIndex >= len(controls):
		// Move into the list only when there is something to show.
		if len(m.allRepos) > 0 {
			m.focus = addRepoFocusList
			m.searchInput.Blur()
		} else {
			m.setAddRepoControlFocus(controls[len(controls)-1])
		}
	default:
		m.setAddRepoControlFocus(controls[nextIndex])
	}
	return true
}

// Update handles incoming messages for the overlay.
func (m AddRepoOverlay) Update(msg tea.Msg) (AddRepoOverlay, tea.Cmd) {
	var cmds []tea.Cmd
	var cmd tea.Cmd
	forceDetailTop := false

	switch msg := msg.(type) {
	case RepoListLoadedMsg:
		m.loading = false
		m.allRepos = msg.Repos
		m.hasMore = msg.HasMore
		m.syncListItems()
		forceDetailTop = true
		for _, e := range msg.Errs {
			err := e
			cmds = append(cmds, func() tea.Msg { return ErrMsg{Err: err} })
		}

	case RepoClonedMsg:
		m.cloning = false
		if msg.Err != nil {
			m.cloneError = msg.Err.Error()
			cmds = append(cmds, func() tea.Msg {
				return ErrMsg{Err: fmt.Errorf("clone failed: %w", msg.Err)}
			})
			return m, tea.Batch(cmds...)
		}
		return m, func() tea.Msg {
			return ActionDoneMsg{Message: "Repository cloned: " + msg.RepoPath}
		}

	case tea.MouseMsg:
		if !m.showManual && msg.Action == tea.MouseActionPress {
			switch msg.Button {
			case tea.MouseButtonWheelUp, tea.MouseButtonWheelDown:
				switch m.focus {
				case addRepoFocusList:
					m.repoList, cmd = m.repoList.Update(msg)
					cmds = append(cmds, cmd)
				case addRepoFocusDetails:
					m.detailViewport, cmd = m.detailViewport.Update(msg)
					return m, cmd
				}
			}
		}

	case tea.KeyMsg:
		if m.showManual {
			switch msg.String() {
			case keyEsc:
				return m, func() tea.Msg { return CloseOverlayMsg{} }
			case keyEnter:
				url := strings.TrimSpace(m.manualURL.Value())
				if url == "" {
					break
				}
				if m.gitClient == nil {
					cmds = append(cmds, func() tea.Msg {
						return ErrMsg{Err: errors.New("git client not configured")}
					})
					break
				}
				m.cloning = true
				m.cloneError = ""
				return m, func() tea.Msg {
					return AddRepoCloneMsg{Repo: adapter.RepoItem{URL: url}, CloneDir: m.workspaceDir, CloneURL: url}
				}
			case "ctrl+n":
				m.showManual = false
				m.manualURL.Blur()
				m.setAddRepoControlFocus(addRepoControlSearch)
			default:
				m.manualURL, cmd = m.manualURL.Update(msg)
				cmds = append(cmds, cmd)
			}
			break
		}

		switch msg.String() {
		case keyEsc:
			return m, func() tea.Msg { return CloseOverlayMsg{} }
		case keyTab:
			if len(m.sources) > 0 {
				m.sourceIndex = (m.sourceIndex + 1) % len(m.sources)
				cmds = append(cmds, m.reloadRepos())
			}
		case "ctrl+n":
			m.showManual = true
			m.addRepoCtrl = addRepoControlSearch
			m.searchInput.Blur()
			m.manualURL.Focus()
		case "ctrl+r":
			m.searchInput.SetValue("")
			cmds = append(cmds, m.reloadRepos())
		case keyEnter:
			if m.cloning {
				break
			}
			item, ok := m.currentListItem()
			if !ok {
				break
			}
			cloneURL := item.URL
			if cloneURL == "" {
				cloneURL = item.SSHURL
			}
			if cloneURL == "" {
				break
			}
			if m.gitClient == nil {
				cmds = append(cmds, func() tea.Msg {
					return ErrMsg{Err: errors.New("git client not configured")}
				})
				break
			}
			m.cloning = true
			m.cloneError = ""
			return m, func() tea.Msg {
				return AddRepoCloneMsg{Repo: item, CloneDir: m.workspaceDir, CloneURL: cloneURL}
			}
		case "up":
			if m.focus == addRepoFocusDetails {
				m.detailViewport, cmd = m.detailViewport.Update(msg)
				return m, cmd
			}
			if m.moveAddRepoFocus(-1) {
				break
			}
			m.repoList, cmd = m.repoList.Update(msg)
			cmds = append(cmds, cmd)
		case keyDown:
			if m.focus == addRepoFocusDetails {
				m.detailViewport, cmd = m.detailViewport.Update(msg)
				return m, cmd
			}
			if m.moveAddRepoFocus(1) {
				break
			}
			m.repoList, cmd = m.repoList.Update(msg)
			cmds = append(cmds, cmd)
		case panelLeft:
			switch m.focus {
			case addRepoFocusDetails:
				m.focus = addRepoFocusList
			case addRepoFocusControls:
				if m.addRepoCtrl == addRepoControlSource && len(m.sources) > 0 {
					m.sourceIndex = (m.sourceIndex - 1 + len(m.sources)) % len(m.sources)
					cmds = append(cmds, m.reloadRepos())
				} else {
					// Forward to search input for cursor movement.
					m.searchInput, cmd = m.searchInput.Update(msg)
					cmds = append(cmds, cmd)
				}
				// addRepoFocusList: left does nothing (up from index 0 returns to controls).
			}
		case panelRight:
			switch m.focus {
			case addRepoFocusList:
				m.focus = addRepoFocusDetails
			case addRepoFocusControls:
				if m.addRepoCtrl == addRepoControlSource && len(m.sources) > 0 {
					m.sourceIndex = (m.sourceIndex + 1) % len(m.sources)
					cmds = append(cmds, m.reloadRepos())
				} else {
					// Forward to search input for cursor movement.
					m.searchInput, cmd = m.searchInput.Update(msg)
					cmds = append(cmds, cmd)
				}
				// addRepoFocusDetails: right does nothing.
			}
		default:
			switch m.focus {
			case addRepoFocusControls:
				// Only forward to search input when search is the active sub-control.
				if m.addRepoCtrl == addRepoControlSearch {
					m.searchInput, cmd = m.searchInput.Update(msg)
					cmds = append(cmds, cmd)
				}
			case addRepoFocusList:
				m.repoList, cmd = m.repoList.Update(msg)
				cmds = append(cmds, cmd)
			case addRepoFocusDetails:
				m.detailViewport, cmd = m.detailViewport.Update(msg)
				return m, cmd
			}
		}
	}

	m.syncDetailViewport(forceDetailTop)
	return m, tea.Batch(cmds...)
}

// View renders the overlay, or empty string when inactive.
func (m *AddRepoOverlay) View() string {
	if !m.active {
		return ""
	}

	layout := m.browserLayout()
	renderWidth := maxInt(1, layout.ContentWidth-4)
	m.resizeInputs(layout.InputWidth)
	m.syncDetailViewport(false)

	activeLabelStyle := m.styles.Accent
	inactiveLabelStyle := m.styles.Hint

	// Source labels with cycling indicator.
	sourceLabels := make([]string, 0, len(m.sources))
	for i, s := range m.sources {
		name := s.Name()
		if i == m.sourceIndex {
			sourceLabels = append(sourceLabels, activeLabelStyle.Render("[► "+name+" ◄]"))
		} else {
			sourceLabels = append(sourceLabels, inactiveLabelStyle.Render(name))
		}
	}
	sourceLine := ""
	if len(sourceLabels) > 0 {
		sourceLine = m.controlLabel("Source: ", addRepoControlSource) + strings.Join(sourceLabels, "  ")
	}

	header := []string{
		m.styles.Title.Render("Browse Repositories"),
		sourceLine,
	}

	var body string
	var footer string
	if m.showManual {
		body = m.manualView()
		footer = m.styles.Hint.Render(m.wrappedManualHintText(renderWidth))
	} else {
		body = m.browserView(layout)
		footer = m.styles.Hint.Render(m.wrappedBrowserHintText(renderWidth))
	}

	return components.RenderOverlayFrame(m.styles, layout.FrameWidth, components.OverlayFrameSpec{
		HeaderLines: header,
		Body:        body,
		Footer:      footer,
		Focused:     true,
	})
}

// browserView renders the split-pane browse view with list and detail.
func (m *AddRepoOverlay) browserView(layout components.SplitOverlayLayout) string {
	lines := make([]string, 0, 3)
	searchRow := m.controlLabel("Search: ", addRepoControlSearch) + m.searchInput.View()
	lines = append(lines, searchRow)
	lines = append(lines, components.RenderOverlayDivider(m.styles, maxInt(1, layout.ContentWidth-4)))

	m.repoList.SetWidth(layout.LeftInnerWidth)
	m.repoList.SetHeight(layout.ViewportHeight)
	m.syncDetailViewport(false)

	leftContent := m.repoList.View()
	if m.loading && len(m.allRepos) == 0 {
		leftContent = m.styles.Muted.Render("Loading…")
	}

	leftPaneTitle := "Repositories"
	rightPaneTitle := "Details"
	if item, ok := m.currentListItem(); ok {
		rightPaneTitle = item.FullName
	}

	panes := components.RenderSplitOverlayBody(m.styles, layout, components.SplitOverlaySpec{
		LeftPane: components.OverlayPaneSpec{
			Title:   leftPaneTitle,
			Body:    leftContent,
			Focused: m.focus != addRepoFocusDetails,
		},
		RightPane: components.OverlayPaneSpec{
			Title:   rightPaneTitle,
			Body:    m.detailViewport.View(),
			Focused: m.focus == addRepoFocusDetails,
		},
	})
	lines = append(lines, panes)
	return strings.Join(lines, "\n")
}

// manualView renders the manual clone URL input view.
func (m *AddRepoOverlay) manualView() string {
	urlLabel := m.styles.Label.Render("Clone URL:   ")
	hints := m.styles.Hint.Render(
		"Enter a git clone URL to clone into the workspace.\n" +
			"Supports HTTPS (https://github.com/…) and SSH (git@github.com:…).")
	cloneStatus := ""
	if m.cloning {
		cloneStatus = "\n" + m.styles.Muted.Render("Cloning…")
	} else if m.cloneError != "" {
		cloneStatus = "\n" + m.styles.Error.Render("Error: "+m.cloneError)
	}
	return strings.Join([]string{
		urlLabel + m.manualURL.View(),
		"",
		hints,
		cloneStatus,
	}, "\n")
}

// addRepoBrowserHintParts returns the hint keybindings for browser mode.
func addRepoBrowserHintParts() []string {
	return []string{"Ctrl+N manual", "Ctrl+R clear", "Enter clone", "Tab source", "Esc cancel"}
}

// addRepoManualHintParts returns the hint keybindings for manual mode.
func addRepoManualHintParts() []string {
	return []string{"Ctrl+N browse", "Enter clone", "Esc cancel"}
}

func wrapHintParts(parts []string, width int) string {
	if width <= 0 {
		return ""
	}
	lines := make([]string, 0, len(parts))
	current := ""
	for _, part := range parts {
		if part == "" {
			continue
		}
		if ansi.StringWidth(part) > width {
			if current != "" {
				lines = append(lines, current)
				current = ""
			}
			lines = append(lines, strings.Split(ansi.Hardwrap(part, width, true), "\n")...)
			continue
		}
		candidate := part
		if current != "" {
			candidate = current + "  " + part
		}
		if current != "" && ansi.StringWidth(candidate) > width {
			lines = append(lines, current)
			current = part
			continue
		}
		current = candidate
	}
	if current != "" {
		lines = append(lines, current)
	}
	return strings.Join(lines, "\n")
}

func hintLineCountForParts(parts []string, width int) int {
	wrapped := wrapHintParts(parts, width)
	if wrapped == "" {
		return 1
	}
	return strings.Count(wrapped, "\n") + 1
}

func (m AddRepoOverlay) wrappedBrowserHintText(width int) string {
	return wrapHintParts(addRepoBrowserHintParts(), width)
}

func (m AddRepoOverlay) wrappedManualHintText(width int) string {
	return wrapHintParts(addRepoManualHintParts(), width)
}

func addRepoHintLineCount(width int) int {
	return hintLineCountForParts(addRepoBrowserHintParts(), width)
}

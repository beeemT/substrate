package views

import (
	"slices"
	"sort"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/tui/components"
	"github.com/beeemT/substrate/internal/tui/styles"
)

const (
	keyBackspace                = "backspace"
	actionMenuMaxOverlayWidth   = 72
	actionMenuMaxVisibleActions = 12
)

// ActionContext identifies the context in which the action menu was opened.
type ActionContext int

const (
	ContextGlobal ActionContext = iota
	ContextEmpty
	ContextModalExclusive
	ContextWorkspaceInit
	ContextSessionSearch
	ContextSettings
	ContextLogs
	ContextNewSession
	ContextNewSessionAutonomous
	ContextAddRepo
	ContextRepoManager
	ContextWorktreePicker
	ContextOverview
	ContextPlanReview
	ContextQuestion
	ContextInterrupted
	ContextReviewing
	ContextCompleted
	ContextAgentSessionLog
	ContextSessionInteractionLog
	ContextArtifacts
	ContextSourceDetails
	ContextSourceItems
	ContextOverviewLinks
	ContextReviewFollowupLoading
	ContextReviewFollowupPicker
	ContextReviewFollowupSelector
	ContextReviewFollowupConfirm
)

// Action represents a single actionable item in the action menu.
type Action struct {
	ID        string
	Label     string
	Shortcut  string
	Priority  int
	Condition func(*App) bool
	Handler   func(*App) tea.Cmd
}

func actionKey(key string) tea.KeyMsg {
	switch key {
	case "ctrl+s":
		return tea.KeyMsg{Type: tea.KeyCtrlS}
	case "ctrl+v":
		return tea.KeyMsg{Type: tea.KeyCtrlV}
	case "ctrl+t":
		return tea.KeyMsg{Type: tea.KeyCtrlT}
	case "ctrl+r":
		return tea.KeyMsg{Type: tea.KeyCtrlR}
	case "ctrl+f":
		return tea.KeyMsg{Type: tea.KeyCtrlF}
	case "ctrl+g":
		return tea.KeyMsg{Type: tea.KeyCtrlG}
	case "ctrl+l":
		return tea.KeyMsg{Type: tea.KeyCtrlL}
	case "ctrl+n":
		return tea.KeyMsg{Type: tea.KeyCtrlN}
	case "ctrl+o":
		return tea.KeyMsg{Type: tea.KeyCtrlO}
	case keyEnter:
		return tea.KeyMsg{Type: tea.KeyEnter}
	case " ":
		return tea.KeyMsg{Type: tea.KeySpace}
	default:
		runes := []rune(key)
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: runes}
	}
}

func updateContentWithKey(a *App, key string) tea.Cmd {
	var cmd tea.Cmd
	a.content, cmd = a.content.Update(actionKey(key))
	return cmd
}

// ActionMenuModel is the Bubble Tea model for the action menu overlay.
type ActionMenuModel struct {
	st     styles.Styles
	app    *App
	width  int
	height int

	context ActionContext // source context captured before overlayActionMenu is activated
	actions []Action      // all available actions for context
	query   string        // search query
	matches []int         // indices into actions that match query
	cursor  int           // position within matches
}

// NewActionMenuModel creates a new ActionMenuModel.
func NewActionMenuModel(st styles.Styles) ActionMenuModel {
	return ActionMenuModel{st: st}
}

// Open opens the action menu with the given context.
func (m *ActionMenuModel) Open(app *App, ctx ActionContext) {
	m.app = app
	m.context = ctx
	m.query = ""
	m.cursor = 0
	m.refresh()
}

// Refresh rebuilds the action list for the current context without losing the query.
func (m *ActionMenuModel) Refresh() {
	m.refresh()
}

// SetSize sets the dimensions for the action menu.
func (m *ActionMenuModel) SetSize(width, height int) {
	m.width = width
	m.height = height
}

// refresh rebuilds the action list and recalculates matches.
func (m *ActionMenuModel) refresh() {
	m.actions = m.app.BuildActionRegistry(m.context)
	m.updateMatches()
}

// updateMatches recalculates which actions match the current query.
func (m *ActionMenuModel) updateMatches() {
	m.matches = nil
	for i := range m.actions {
		if FuzzyMatch(m.query, m.actions[i].Label) {
			m.matches = append(m.matches, i)
		}
	}
	// Clamp cursor to valid range
	if m.cursor >= len(m.matches) {
		m.cursor = max(0, len(m.matches)-1)
	}
}

// FuzzyMatch returns true if query matches label using fuzzy matching.
func FuzzyMatch(query, label string) bool {
	if query == "" {
		return true
	}
	query = strings.ToLower(query)
	label = strings.ToLower(label)

	// Substring match (fast path)
	if strings.Contains(label, query) {
		return true
	}

	// Character-by-character match using runes to handle Unicode correctly
	queryRunes := []rune(query)
	qi := 0
	for _, c := range label {
		if qi < len(queryRunes) && queryRunes[qi] == c {
			qi++
		}
	}
	return qi == len(queryRunes)
}

// Update handles messages for the action menu.
func (m ActionMenuModel) Update(msg tea.Msg) (ActionMenuModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKeyMsg(msg)
	}
	return m, nil
}

func (m ActionMenuModel) handleKeyMsg(msg tea.KeyMsg) (ActionMenuModel, tea.Cmd) {
	switch msg.String() {
	case keyEsc:
		m.app.closeActionMenu()
		return m, nil

	case keyEnter:
		if len(m.matches) > 0 && m.cursor < len(m.matches) {
			idx := m.matches[m.cursor]
			action := m.actions[idx]
			m.app.closeActionMenu()
			return m, action.Handler(m.app)
		}
		return m, nil

	case "up":
		if m.cursor > 0 {
			m.cursor--
		}
		return m, nil

	case keyDown:
		if m.cursor < len(m.matches)-1 {
			m.cursor++
		}
		return m, nil

	case keyBackspace:
		if len(m.query) > 0 {
			m.query = m.query[:len(m.query)-1]
			m.updateMatches()
		}
		return m, nil

	default:
		// Handle printable characters for search
		if isPrintableKey(msg) {
			m.query += strings.ToLower(msg.String())
			m.updateMatches()
		}
		return m, nil
	}
}

// isPrintableKey returns true if the key message represents a printable character
// that should be added to the search query.
func isPrintableKey(msg tea.KeyMsg) bool {
	if msg.String() == " " {
		return true
	}
	// Ignore control keys
	if msg.Type != tea.KeyRunes {
		return false
	}

	runes := []rune(msg.String())
	if len(runes) != 1 {
		return false
	}

	r := runes[0]

	// Ignore special characters that shouldn't be in search
	switch r {
	case ' ', '-', '_':
		return true
	default:
		return unicode.IsLetter(r) || unicode.IsNumber(r)
	}
}

// View renders the action menu.
func (m ActionMenuModel) View() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}

	frameWidth := actionMenuFrameWidth(m.width)
	contentWidth := max(1, m.st.Chrome.OverlayFrame.InnerWidth(frameWidth))
	visibleRows := m.visibleActionRows()

	header := []string{
		m.st.Title.Render("Actions"),
		m.searchLine(contentWidth),
		components.RenderOverlayDivider(m.st, contentWidth),
	}
	body := m.actionsBody(contentWidth, visibleRows)
	footer := ansi.Truncate(components.RenderKeyHints(m.st, []components.KeyHint{
		{Key: "Enter", Label: "Select"},
		{Key: "Esc", Label: "Close"},
	}, "  "), contentWidth, "")

	return components.RenderOverlayFrame(m.st, frameWidth, components.OverlayFrameSpec{
		HeaderLines: header,
		Body:        body,
		Footer:      footer,
		Focused:     true,
	})
}

func actionMenuFrameWidth(termWidth int) int {
	if termWidth <= 2 {
		return 1
	}
	availableWidth := termWidth - 2
	minHalfWidth := (termWidth + 1) / 2
	return min(availableWidth, max(actionMenuMaxOverlayWidth-2, minHalfWidth))
}

func (m ActionMenuModel) visibleActionRows() int {
	const fixedRows = 7 // frame borders + title + search + divider + body gap + footer
	availableRows := max(1, m.height-fixedRows)
	minHalfHeightRows := max(1, (m.height+1)/2-fixedRows)
	desiredRows := max(actionMenuMaxVisibleActions, minHalfHeightRows)
	return min(desiredRows, availableRows)
}

func (m ActionMenuModel) searchLine(width int) string {
	if m.query == "" {
		return ansi.Truncate(m.st.Label.Render("Search: ")+m.st.Muted.Render(" Type to search…"), width, "")
	}
	return ansi.Truncate(m.st.Label.Render("Search: ")+m.st.Subtitle.Render(" "+m.query), width, "")
}

func (m ActionMenuModel) actionsBody(width, visibleRows int) string {
	visibleRows = max(1, visibleRows)
	rows := make([]string, 0, visibleRows)
	if len(m.matches) == 0 {
		rows = append(rows, m.st.Muted.Render(centerText("No matching actions", width)))
	} else {
		startIdx := 0
		if len(m.matches) > visibleRows && m.cursor >= visibleRows {
			startIdx = m.cursor - visibleRows/2
		}
		startIdx = min(startIdx, max(0, len(m.matches)-visibleRows))
		endIdx := min(startIdx+visibleRows, len(m.matches))

		for i := startIdx; i < endIdx; i++ {
			idx := m.matches[i]
			rows = append(rows, formatActionRow(m.actions[idx], width, i == m.cursor, m.st))
		}
	}
	for len(rows) < visibleRows {
		rows = append(rows, "")
	}
	return strings.Join(rows, "\n")
}

// formatActionRow formats a single action row with shortcut right-aligned.
func formatActionRow(action Action, width int, selected bool, st styles.Styles) string {
	width = max(1, width)
	markerRaw := "› "
	keyRaw := "[" + action.Shortcut + "]"
	labelWidth := max(1, width-ansi.StringWidth(markerRaw)-ansi.StringWidth(keyRaw)-1)
	label := ansi.Truncate(action.Label, labelWidth, "…")
	labelPad := max(0, labelWidth-ansi.StringWidth(label))

	marker := strings.Repeat(" ", ansi.StringWidth(markerRaw))
	labelStyle := st.Subtitle
	if selected {
		marker = st.Accent.Render(markerRaw)
		labelStyle = st.Title
	}

	return marker + labelStyle.Render(label) + strings.Repeat(" ", labelPad+1) + st.KeybindAccent.Render(keyRaw)
}

// centerText centers text within a given width.
func centerText(text string, width int) string {
	textWidth := ansi.StringWidth(text)
	if textWidth >= width {
		return ansi.Truncate(text, max(1, width), "")
	}
	padding := (width - textWidth) / 2
	return strings.Repeat(" ", padding) + text
}

// BuildActionRegistry builds the list of available actions for the given context.
func (a *App) BuildActionRegistry(ctx ActionContext) []Action {
	var actions []Action

	// Global actions are included for every non-modal action-menu context.
	actions = append(actions, globalActions(a)...)

	// Context-specific actions.
	switch ctx {
	case ContextOverview:
		actions = append(actions, overviewActions(a)...)
	case ContextPlanReview:
		actions = append(actions, planReviewActions(a)...)
	case ContextQuestion:
		actions = append(actions, questionActions(a)...)
	case ContextInterrupted:
		actions = append(actions, interruptedActions(a)...)
	case ContextReviewing:
		actions = append(actions, reviewingActions(a)...)
	case ContextCompleted:
		actions = append(actions, completedActions(a)...)
	case ContextAgentSessionLog, ContextSessionInteractionLog:
		actions = append(actions, sessionLogActions(a, ctx)...)
	case ContextArtifacts:
		actions = append(actions, artifactsActions(a)...)
	case ContextSourceDetails:
		actions = append(actions, sourceDetailsActions(a)...)
	case ContextSourceItems:
		actions = append(actions, sourceItemsActions(a)...)
	case ContextOverviewLinks:
		actions = append(actions, overviewLinksActions(a)...)
	case ContextWorkspaceInit:
		actions = append(actions, workspaceInitActions(a)...)
	case ContextNewSession:
		actions = append(actions, newSessionActions(a)...)
	case ContextNewSessionAutonomous:
		actions = append(actions, newSessionAutonomousActions(a)...)
	case ContextAddRepo:
		actions = append(actions, addRepoActions(a)...)
	case ContextRepoManager:
		actions = append(actions, repoManagerActions(a)...)
	case ContextWorktreePicker:
		actions = append(actions, worktreePickerActions(a)...)
	case ContextSettings:
		actions = append(actions, settingsActions(a)...)
	case ContextLogs:
		actions = append(actions, logsActions(a)...)
	case ContextSessionSearch:
		actions = append(actions, sessionSearchActions(a)...)
	case ContextReviewFollowupPicker:
		actions = append(actions, reviewFollowupPickerActions(a)...)
	case ContextReviewFollowupSelector:
		actions = append(actions, reviewFollowupSelectorActions(a)...)
	case ContextReviewFollowupConfirm:
		actions = append(actions, reviewFollowupConfirmActions(a)...)
		// ContextReviewFollowupLoading has no local actions
	}

	actions = filterAvailableActions(a, actions)

	// Remove actions that are pure navigation, overlay-close, or Enter-confirm operations.
	// These are intuitive enough that showing them clutters the menu.
	actions = slices.DeleteFunc(actions, func(a Action) bool {
		switch a.Shortcut {
		case "↑", "↓", "←", "→", "←/Esc", "Enter", "Enter/o", "Esc", "Space":
			return true
		}
		return false
	})

	sort.Slice(actions, func(i, j int) bool {
		if actions[i].Priority == actions[j].Priority {
			return actions[i].Label < actions[j].Label
		}
		return actions[i].Priority < actions[j].Priority
	})
	return actions
}

// filterAvailableActions filters actions based on their conditions.
func filterAvailableActions(a *App, actions []Action) []Action {
	filtered := make([]Action, 0, len(actions))
	for _, action := range actions {
		if action.Condition(a) {
			filtered = append(filtered, action)
		}
	}
	return filtered
}

// currentActionContext determines the action context based on current app state.
func (a *App) currentActionContext() ActionContext {
	// The action menu replaces activeOverlay while open, so use the captured
	// return overlay when recomputing actions from inside the menu.
	activeOverlay := a.activeOverlay
	if activeOverlay == overlayActionMenu {
		activeOverlay = a.actionMenuReturnOverlay
	}

	// Modal confirmations and duplicate-session dialogs keep exclusive input.
	// Do not open the action menu over them.
	if a.confirmActive || a.duplicateSessionActive {
		return ContextModalExclusive
	}

	// App-level overlays first.
	switch activeOverlay {
	case overlayWorkspaceInit:
		return ContextWorkspaceInit
	case overlayNewSession:
		return ContextNewSession
	case overlayNewSessionAutonomous:
		return ContextNewSessionAutonomous
	case overlaySessionSearch:
		return ContextSessionSearch
	case overlaySettings:
		return ContextSettings
	case overlaySourceItems:
		return ContextSourceItems
	case overlayLogs:
		return ContextLogs
	case overlayAddRepo:
		return ContextAddRepo
	case overlayRepoManager:
		return ContextRepoManager
	case overlayWorktreePicker:
		return ContextWorktreePicker
	case overlayOverviewLinks:
		return ContextOverviewLinks
	case overlayReviewFollowup:
		return a.reviewFollowupContext()
	}

	// Overview sub-overlays.
	if a.content.Mode() == ContentModeOverview {
		switch a.content.overview.overlay {
		case overviewOverlayPlan:
			return ContextPlanReview
		case overviewOverlayQuestion:
			return ContextQuestion
		case overviewOverlayInterrupted:
			return ContextInterrupted
		case overviewOverlayReviewing:
			return ContextReviewing
		case overviewOverlayCompleted:
			return ContextCompleted
		}
		return ContextOverview
	}

	switch a.content.Mode() {
	case ContentModeEmpty:
		return ContextEmpty
	case ContentModeAgentSession:
		return ContextAgentSessionLog
	case ContentModeSessionInteraction:
		return ContextSessionInteractionLog
	case ContentModeArtifacts:
		return ContextArtifacts
	case ContentModeSourceDetails:
		return ContextSourceDetails
	}
	return ContextGlobal
}

// reviewFollowupContext determines the context for the review followup overlay.
func (a *App) reviewFollowupContext() ActionContext {
	switch a.reviewFollowupOverlay.Stage() {
	case reviewFollowupStageLoading:
		return ContextReviewFollowupLoading
	case reviewFollowupStagePicker:
		return ContextReviewFollowupPicker
	case reviewFollowupStageSelector:
		return ContextReviewFollowupSelector
	case reviewFollowupStageConfirm:
		return ContextReviewFollowupConfirm
	}
	return ContextReviewFollowupLoading
}

// --- Action builders ---

func globalActions(a *App) []Action {
	return []Action{
		{ID: "new_session", Label: "New session", Shortcut: "n", Priority: 10, Condition: func(a *App) bool { return true }, Handler: func(a *App) tea.Cmd { return a.openNewSession() }},
		{ID: "new_autonomous", Label: "New autonomous session", Shortcut: "A", Priority: 11, Condition: func(a *App) bool { return true }, Handler: func(a *App) tea.Cmd { return a.openNewSessionAutonomousOverlay() }},
		{ID: "repo_manager", Label: "Open repo manager", Shortcut: "R", Priority: 20, Condition: func(a *App) bool { return true }, Handler: func(a *App) tea.Cmd { return a.openRepoManager() }},
		{ID: "settings", Label: "Open settings", Shortcut: "s", Priority: 30, Condition: func(a *App) bool { return true }, Handler: func(a *App) tea.Cmd { a.activeOverlay = overlaySettings; a.settingsPage.Open(); return nil }},
		{ID: "logs", Label: "Open logs", Shortcut: "L", Priority: 40, Condition: func(a *App) bool { return true }, Handler: func(a *App) tea.Cmd {
			a.logsOverlay.SetSize(a.windowWidth, a.windowHeight)
			a.logsOverlay.Open()
			a.activeOverlay = overlayLogs
			return nil
		}},
		{ID: "search", Label: "Search sessions", Shortcut: "/", Priority: 50, Condition: func(a *App) bool { return true }, Handler: func(a *App) tea.Cmd { return a.openSessionSearch() }},
		{ID: "open_worktree_picker", Label: "Open terminal in worktree", Shortcut: "t", Priority: 55, Condition: func(a *App) bool {
			return a.content.Mode() == ContentModeOverview
		}, Handler: func(a *App) tea.Cmd { return func() tea.Msg { return OpenWorktreePickerMsg{} } }},
		{ID: "open_terminal", Label: "Open terminal", Shortcut: "t", Priority: 56, Condition: func(a *App) bool {
			return a.focusedTerminalAgentSession() != nil
		}, Handler: func(a *App) tea.Cmd {
			if session := a.focusedTerminalAgentSession(); session != nil {
				return OpenTerminalCmd(session.WorktreePath)
			}
			return nil
		}},
		{ID: "delete_session", Label: "Delete session", Shortcut: "d", Priority: 60, Condition: func(a *App) bool { return a.deletableSessionID() != "" }, Handler: func(a *App) tea.Cmd { a.showDeleteSessionConfirm(a.deletableSessionID()); return nil }},
		{ID: "archive_session", Label: "Archive session", Shortcut: "a", Priority: 70, Condition: func(a *App) bool { return a.archivablSessionID() != "" && a.unarchivablSessionID() == "" }, Handler: func(a *App) tea.Cmd { a.showArchiveConfirm(a.archivablSessionID()); return nil }},
		{ID: "unarchive_session", Label: "Unarchive session", Shortcut: "a", Priority: 71, Condition: func(a *App) bool { return a.unarchivablSessionID() != "" }, Handler: func(a *App) tea.Cmd { a.showUnarchiveConfirm(a.unarchivablSessionID()); return nil }},
		{ID: "interrupt", Label: "Interrupt sessions", Shortcut: "I", Priority: 80, Condition: func(a *App) bool { return len(a.interruptibleFocusedSessionIDs()) > 0 }, Handler: func(a *App) tea.Cmd {
			ids := a.interruptibleFocusedSessionIDs()
			return func() tea.Msg { return ConfirmInterruptSessionsMsg{SessionIDs: ids} }
		}},
		{ID: "retry_focused_session", Label: "Retry focused session", Shortcut: "r", Priority: 81, Condition: func(a *App) bool {
			return a.retryableFocusedSessionID() != ""
		}, Handler: func(a *App) tea.Cmd {
			sessionID := a.retryableFocusedSessionID()
			if sessionID == "" {
				return nil
			}
			ctx := a.pipelineCtxForSession(sessionID)
			return RetrySessionCmd(ctx, a.provider.Task(), a.provider.Implementation(), a.sendAsyncMsg, sessionID, a.runtimeCtx.InstanceID)
		}},
		{ID: "quit", Label: "Quit", Shortcut: "q", Priority: 90, Condition: func(a *App) bool { return true }, Handler: func(a *App) tea.Cmd { _, cmd := a.handleQuitRequest(); return cmd }},
		{ID: "sidebar_up", Label: "Move sidebar selection up", Shortcut: "↑", Priority: 91, Condition: func(a *App) bool { return a.mainFocus == mainFocusSidebar }, Handler: func(a *App) tea.Cmd { a.sidebar.MoveUp(); return a.onSidebarMove() }},
		{ID: "sidebar_down", Label: "Move sidebar selection down", Shortcut: "↓", Priority: 92, Condition: func(a *App) bool { return a.mainFocus == mainFocusSidebar }, Handler: func(a *App) tea.Cmd { a.sidebar.MoveDown(); return a.onSidebarMove() }},
		{ID: "sidebar_enter", Label: "Enter tasks/content", Shortcut: "→", Priority: 93, Condition: func(a *App) bool { return a.mainFocus == mainFocusSidebar && a.sidebarMode == sidebarPaneSessions }, Handler: func(a *App) tea.Cmd { return a.enterTaskSidebar() }},
		{ID: "sidebar_exit", Label: "Exit tasks/content", Shortcut: "←/Esc", Priority: 94, Condition: func(a *App) bool { return a.mainFocus == mainFocusContent || a.sidebarMode == sidebarPaneTasks }, Handler: func(a *App) tea.Cmd {
			if a.mainFocus == mainFocusContent {
				a.mainFocus = mainFocusSidebar
				return nil
			}
			return a.exitTaskSidebar()
		}},
		{ID: "cycle_filter", Label: "Cycle sidebar filter", Shortcut: "f", Priority: 95, Condition: func(a *App) bool { return a.mainFocus == mainFocusSidebar && a.sidebarMode == sidebarPaneSessions }, Handler: func(a *App) tea.Cmd { a.sidebar.CycleFilter(); a.rebuildSidebar(); return nil }},
		{ID: "cycle_group", Label: "Cycle sidebar grouping", Shortcut: "g", Priority: 96, Condition: func(a *App) bool { return a.mainFocus == mainFocusSidebar && a.sidebarMode == sidebarPaneSessions }, Handler: func(a *App) tea.Cmd { a.sidebar.CycleDimension(); a.rebuildSidebar(); return nil }},
		{ID: "toggle_sort", Label: "Toggle sidebar sort direction", Shortcut: "o", Priority: 97, Condition: func(a *App) bool { return a.mainFocus == mainFocusSidebar && a.sidebarMode == sidebarPaneSessions }, Handler: func(a *App) tea.Cmd { a.sidebar.ToggleDirection(); a.rebuildSidebar(); return nil }},
		{ID: "jump_to_overview", Label: "Open overview from task notice", Shortcut: "Enter", Priority: 98, Condition: func(a *App) bool {
			return a.sidebarMode == sidebarPaneTasks && a.selectedTaskSessionID() != "" && a.sourceDetailsNoticeForWorkItem(a.workItemByID(a.currentWorkItemID)) != nil
		}, Handler: func(a *App) tea.Cmd { return a.jumpFromSourceDetailsToOverview() }},
	}
}

func overviewActions(a *App) []Action {
	var actions []Action

	// Cycle action cards - delegate to overview via Tab key
	if len(a.content.overview.data.Actions) > 1 {
		actions = append(actions, Action{
			ID: "cycle_actions", Label: "Cycle action cards", Shortcut: "Tab", Priority: 100,
			Condition: func(a *App) bool { return len(a.content.overview.data.Actions) > 1 },
			Handler: func(a *App) tea.Cmd {
				a.content.overview.selectedAction++
				if a.content.overview.selectedAction >= len(a.content.overview.data.Actions) {
					a.content.overview.selectedAction = 0
				}
				return nil
			},
		})
	}

	// Execute/inspect - delegate via key
	actions = append(actions, Action{
		ID: "execute_action", Label: "Execute action", Shortcut: "Enter", Priority: 115,
		Condition: func(a *App) bool {
			return a.content.Mode() == ContentModeOverview && a.content.overview.selectedActionCard() != nil
		},
		Handler: func(a *App) tea.Cmd { return updateContentWithKey(a, keyEnter) },
	})

	for _, spec := range []struct {
		id       string
		label    string
		shortcut string
		priority int
	}{
		{"overview_links", "Open links", "o", 116},
		{"overview_approve_or_abandon", "Approve / abandon selected action", "a", 117},
		{"overview_resume_or_retry", "Resume / retry selected action", "r", 118},
		{"overview_finalize", "Finalize work item", "f", 119},
		{"overview_inspect_or_changes", "Inspect / request changes", "i", 121},
	} {
		spec := spec
		actions = append(actions, Action{
			ID: spec.id, Label: spec.label, Shortcut: spec.shortcut, Priority: spec.priority,
			Condition: func(a *App) bool {
				return a.content.Mode() == ContentModeOverview && a.content.overview.overlay == overviewOverlayNone && a.content.overview.selectedActionCard() != nil
			},
			Handler: func(a *App) tea.Cmd { return updateContentWithKey(a, spec.shortcut) },
		})
	}

	// View full plan
	plan := a.plans[a.currentWorkItemID]
	if plan != nil {
		actions = append(actions, Action{
			ID: "view_plan", Label: "View full plan", Shortcut: "i", Priority: 120,
			Condition: func(a *App) bool {
				plan := a.plans[a.currentWorkItemID]
				return a.content.Mode() == ContentModeOverview && plan != nil && a.content.overview.selectedActionCard() == nil
			},
			Handler: func(a *App) tea.Cmd { a.content.overview.overlay = overviewOverlayPlan; return nil },
		})
	}

	return actions
}

func planReviewActions(a *App) []Action {
	return []Action{
		{ID: "plan_approve", Label: "Approve plan", Shortcut: "a", Priority: 200, Condition: func(a *App) bool {
			return a.content.Mode() == ContentModeOverview && a.content.overview.overlay == overviewOverlayPlan
		}, Handler: func(a *App) tea.Cmd { return func() tea.Msg { return PlanApproveMsg{WorkItemID: a.currentWorkItemID} } }},
		{ID: "plan_request_changes", Label: "Request changes", Shortcut: "i", Priority: 210, Condition: func(a *App) bool {
			return a.content.Mode() == ContentModeOverview && a.content.overview.overlay == overviewOverlayPlan
		}, Handler: func(a *App) tea.Cmd { return a.content.overview.openPlanOverlayForChanges() }},
		{ID: "plan_copy", Label: "Copy plan", Shortcut: "c", Priority: 220, Condition: func(a *App) bool {
			return a.content.Mode() == ContentModeOverview && a.content.overview.overlay == overviewOverlayPlan
		}, Handler: func(a *App) tea.Cmd { return updateContentWithKey(a, "c") }},
		{ID: "plan_edit", Label: "Edit plan in editor", Shortcut: "e", Priority: 230, Condition: func(a *App) bool {
			return a.content.Mode() == ContentModeOverview && a.content.overview.overlay == overviewOverlayPlan
		}, Handler: func(a *App) tea.Cmd { return updateContentWithKey(a, "e") }},
	}
}

func questionActions(a *App) []Action {
	return []Action{
		{ID: "send_answer", Label: "Send answer", Shortcut: "Enter", Priority: 400, Condition: func(a *App) bool {
			return a.content.Mode() == ContentModeOverview && a.content.overview.overlay == overviewOverlayQuestion && a.content.overview.question.inputActive
		}, Handler: func(a *App) tea.Cmd {
			q := a.content.overview.question
			q.input.Flush()
			answer := q.input.Value()
			qID := q.question.ID
			return func() tea.Msg { return AnswerQuestionMsg{QuestionID: qID, Answer: answer, AnsweredBy: "human"} }
		}},
	}
}

func interruptedActions(a *App) []Action {
	return []Action{
		{ID: "resume", Label: "Resume", Shortcut: "r", Priority: 410, Condition: func(a *App) bool {
			card := a.content.overview.selectedActionCard()
			return card != nil && card.CanAct && !a.resumeInFlight[a.currentWorkItemID]
		}, Handler: func(a *App) tea.Cmd {
			return func() tea.Msg { return ResumeSessionMsg{WorkItemID: a.currentWorkItemID} }
		}},
		{ID: "abandon", Label: "Abandon", Shortcut: "a", Priority: 420, Condition: func(a *App) bool {
			card := a.content.overview.selectedActionCard()
			return card != nil && card.CanAct && len(card.InterruptedSessions) == 1
		}, Handler: func(a *App) tea.Cmd {
			sessionID := ""
			if card := a.content.overview.selectedActionCard(); card != nil && len(card.InterruptedSessions) > 0 {
				sessionID = card.InterruptedSessions[0].ID
			}
			return func() tea.Msg { return ConfirmAbandonMsg{SessionID: sessionID} }
		}},
	}
}

func reviewingActions(a *App) []Action {
	return []Action{
		{ID: "extend_review", Label: "Extend review", Shortcut: "r", Priority: 450, Condition: func(a *App) bool {
			card := a.content.overview.selectedActionCard()
			return card != nil && card.Kind == overviewActionReviewing
		}, Handler: func(a *App) tea.Cmd { return func() tea.Msg { return ReimplementMsg{WorkItemID: a.currentWorkItemID} } }},
		{ID: "override_accept", Label: "Override accept", Shortcut: "o", Priority: 460, Condition: func(a *App) bool {
			card := a.content.overview.selectedActionCard()
			return card != nil && card.Kind == overviewActionReviewing
		}, Handler: func(a *App) tea.Cmd {
			return func() tea.Msg { return ConfirmOverrideAcceptMsg{WorkItemID: a.currentWorkItemID} }
		}},
		{ID: "fail_review", Label: "Fail session", Shortcut: "f", Priority: 470, Condition: func(a *App) bool {
			card := a.content.overview.selectedActionCard()
			return card != nil && card.Kind == overviewActionReviewing
		}, Handler: func(a *App) tea.Cmd {
			return func() tea.Msg { return ConfirmFailReviewMsg{WorkItemID: a.currentWorkItemID} }
		}},
	}
}

func completedActions(a *App) []Action {
	return []Action{
		{ID: "revise_plan_completed", Label: "Revise plan", Shortcut: "i", Priority: 480, Condition: func(a *App) bool {
			return a.content.Mode() == ContentModeOverview && a.content.overview.overlay == overviewOverlayCompleted
		}, Handler: func(a *App) tea.Cmd { return a.content.overview.openCompletedOverlayForPlanFollowUp() }},
		{ID: "request_code_changes_completed", Label: "Request code changes", Shortcut: "p", Priority: 481, Condition: func(a *App) bool {
			return a.content.Mode() == ContentModeOverview &&
				a.content.overview.overlay == overviewOverlayCompleted &&
				a.completedCodeFollowUpSessionID(a.currentWorkItemID) != ""
		}, Handler: func(a *App) tea.Cmd {
			a.content.overview.completed.SetCodeFollowUpSessionID(a.completedCodeFollowUpSessionID(a.currentWorkItemID))
			return a.content.overview.openCompletedOverlayForCodeFollowUp()
		}},
		{ID: "submit_feedback", Label: "Submit feedback", Shortcut: "Enter", Priority: 490, Condition: func(a *App) bool {
			return a.content.Mode() == ContentModeOverview && a.content.overview.overlay == overviewOverlayCompleted && a.content.overview.completed.inputActive
		}, Handler: func(a *App) tea.Cmd { return updateContentWithKey(a, "enter") }},
		{ID: "copy_completed_plan", Label: "Copy plan", Shortcut: "c", Priority: 491, Condition: func(a *App) bool {
			return a.content.Mode() == ContentModeOverview && a.content.overview.overlay == overviewOverlayCompleted
		}, Handler: func(a *App) tea.Cmd { return updateContentWithKey(a, "c") }},
	}
}

func sessionLogPromptActionLabel(a *App) string {
	mode := a.content.Mode()
	if mode != ContentModeAgentSession && mode != ContentModeSessionInteraction {
		return ""
	}
	sessionID := a.content.sessionLog.SessionID()
	session := a.workItemTaskSession(a.currentWorkItemID, sessionID)
	if session == nil {
		return ""
	}
	switch session.Status {
	case domain.AgentSessionRunning:
		return "Prompt agent"
	case domain.AgentSessionFailed:
		if a.content.sessionLog.failedSessionID == session.ID {
			return "Retry with feedback"
		}
	case domain.AgentSessionCompleted:
		if a.content.sessionLog.completedSessionID == session.ID {
			return "Request code changes"
		}
	}
	return ""
}

func sessionLogActions(a *App, ctx ActionContext) []Action {
	var actions []Action

	promptLabel := sessionLogPromptActionLabel(a)
	if promptLabel == "" {
		promptLabel = "Prompt agent"
	}
	actions = append(actions, Action{
		ID: "steer", Label: promptLabel, Shortcut: "p", Priority: 300,
		Condition: func(a *App) bool {
			return sessionLogPromptActionLabel(a) != ""
		},
		Handler: func(a *App) tea.Cmd {
			a.content.sessionLog.steerActive = true
			a.content.sessionLog.steerInput.Focus()
			return nil
		},
	})
	if a.content.sessionLog.notice != nil {
		actions = append(actions, Action{
			ID: "open_overview_log", Label: "Open overview", Shortcut: "Enter", Priority: 305,
			Condition: func(a *App) bool { return a.content.sessionLog.notice != nil },
			Handler:   func(a *App) tea.Cmd { return a.jumpFromSourceDetailsToOverview() },
		})
	}
	actions = append(actions, Action{
		ID: "goto_bottom", Label: "Follow tail / go to bottom", Shortcut: "f", Priority: 310,
		Condition: func(a *App) bool { return true },
		Handler:   func(a *App) tea.Cmd { a.content.sessionLog.viewport.GotoBottom(); return nil },
	})
	actions = append(actions, Action{
		ID: "goto_top", Label: "Go to top", Shortcut: "g", Priority: 311,
		Condition: func(a *App) bool { return true },
		Handler:   func(a *App) tea.Cmd { a.content.sessionLog.viewport.GotoTop(); return nil },
	})
	actions = append(actions, Action{
		ID: "toggle_thinking", Label: "Toggle thinking", Shortcut: "Ctrl+T", Priority: 312,
		Condition: func(a *App) bool { return hasThinkingBlocks(a.content.sessionLog.entries) },
		Handler:   func(a *App) tea.Cmd { return updateContentWithKey(a, "ctrl+t") },
	})
	actions = append(actions, Action{
		ID: "toggle_verbose", Label: "Toggle verbose", Shortcut: "v", Priority: 330,
		Condition: func(a *App) bool { return true },
		Handler:   func(a *App) tea.Cmd { a.content.sessionLog.verbose = !a.content.sessionLog.verbose; return nil },
	})
	actions = append(actions, Action{
		ID: "open_plan", Label: "Open plan", Shortcut: "i", Priority: 345,
		Condition: func(a *App) bool {
			plan := a.plans[a.currentWorkItemID]
			return plan != nil && a.content.Mode() == ContentModeAgentSession && a.content.overview.overlay == overviewOverlayNone
		},
		Handler: func(a *App) tea.Cmd {
			return func() tea.Msg {
				if p := a.plans[a.currentWorkItemID]; p != nil {
					return InspectPlanMsg{PlanID: p.ID}
				}
				return nil
			}
		},
	})
	actions = append(actions, Action{
		ID: "copy_plan_overlay", Label: "Copy plan", Shortcut: "c", Priority: 346,
		Condition: func(a *App) bool { return a.content.sessionLog.planOverlay && a.content.sessionLog.planDocument != "" },
		Handler:   func(a *App) tea.Cmd { return updateContentWithKey(a, "c") },
	})

	return actions
}

func artifactsActions(a *App) []Action {
	return []Action{
		{ID: "open_artifact", Label: "Open artifact", Shortcut: "o", Priority: 620, Condition: func(a *App) bool {
			return len(a.content.artifacts.items) > 0 && a.content.artifacts.items[a.content.artifacts.cursor].URL != ""
		}, Handler: func(a *App) tea.Cmd {
			return func() tea.Msg {
				return OpenExternalURLMsg{URL: a.content.artifacts.items[a.content.artifacts.cursor].URL}
			}
		}},
		{ID: "open_artifact_links", Label: "Open artifact links", Shortcut: "O", Priority: 630, Condition: func(a *App) bool {
			return len(a.content.artifacts.items) > 0
		}, Handler: func(a *App) tea.Cmd { return updateContentWithKey(a, "O") }},
		{ID: "start_review_followup", Label: "Start review followup", Shortcut: "f", Priority: 640, Condition: func(a *App) bool { return a.content.artifacts.reviewFollowupEnabled() }, Handler: func(a *App) tea.Cmd {
			return func() tea.Msg {
				return FetchReviewCommentsMsg{WorkItemID: a.currentWorkItemID, Items: a.content.artifacts.items}
			}
		}},
	}
}

func sourceDetailsActions(a *App) []Action {
	return []Action{
		{ID: "back_to_overview", Label: "Back to overview", Shortcut: "Enter", Priority: 930, Condition: func(a *App) bool { return a.content.sourceDetails.notice != nil }, Handler: func(a *App) tea.Cmd { return a.jumpFromSourceDetailsToOverview() }},
		{ID: "open_src_browser", Label: "Open source in browser", Shortcut: "o", Priority: 950, Condition: func(a *App) bool {
			return len(a.content.sourceDetails.items) > 0 && a.content.sourceDetails.items[a.content.sourceDetails.cursor].URL != ""
		}, Handler: func(a *App) tea.Cmd {
			return func() tea.Msg {
				return OpenExternalURLMsg{URL: a.content.sourceDetails.items[a.content.sourceDetails.cursor].URL}
			}
		}},
	}
}

func sourceItemsActions(a *App) []Action {
	return []Action{
		{ID: "open_source_urls", Label: "Open selected source URLs", Shortcut: "o", Priority: 870, Condition: func(a *App) bool { return true }, Handler: func(a *App) tea.Cmd {
			urls := a.sourceItemsOverlay.selectedURLs()
			if len(urls) > 0 {
				return func() tea.Msg { return openSourceItemURLsMsg{URLs: urls} }
			}
			return nil
		}},
		{ID: "close_src_items", Label: "Close source URLs", Shortcut: "Esc", Priority: 885, Condition: func(a *App) bool { return true }, Handler: func(a *App) tea.Cmd { return func() tea.Msg { return CloseOverlayMsg{} } }},
	}
}

func overviewLinksActions(a *App) []Action {
	return []Action{
		{ID: "open_focused_link", Label: "Open focused link", Shortcut: "o", Priority: 890, Condition: func(a *App) bool { return true }, Handler: func(a *App) tea.Cmd {
			if url := a.overviewLinksOverlay.selectedURL(); url != "" {
				return func() tea.Msg { return OpenExternalURLMsg{URL: url} }
			}
			return nil
		}},
		{ID: "open_all_links", Label: "Open all links", Shortcut: "a", Priority: 891, Condition: func(a *App) bool { return true }, Handler: func(a *App) tea.Cmd {
			return a.overviewLinksOverlay.openAllCmd()
		}},
		{ID: "close_links", Label: "Close links", Shortcut: "Esc", Priority: 900, Condition: func(a *App) bool { return true }, Handler: func(a *App) tea.Cmd {
			if a.overviewLinksReturnOverlay != overlayNone {
				a.activeOverlay = a.overviewLinksReturnOverlay
				a.overviewLinksReturnOverlay = overlayNone
			} else {
				a.activeOverlay = overlayNone
			}
			a.overviewLinksOverlay.Close()
			return nil
		}},
	}
}

func workspaceInitActions(a *App) []Action {
	return []Action{
		{ID: "cancel_workspace", Label: "Cancel workspace init", Shortcut: "Esc", Priority: 925, Condition: func(a *App) bool { return true }, Handler: func(a *App) tea.Cmd { return func() tea.Msg { return WorkspaceCancelMsg{} } }},
	}
}

func newSessionActions(a *App) []Action {
	return []Action{
		{ID: "continue_selected", Label: "Continue selected items", Shortcut: "Enter", Priority: 735, Condition: func(a *App) bool { return len(a.newSession.selectedIDs) > 0 }, Handler: func(a *App) tea.Cmd { return func() tea.Msg { return NewSessionBrowseMsg{} } }},
		{ID: "start_manual", Label: "Start manual session", Shortcut: "Enter", Priority: 736, Condition: func(a *App) bool {
			return a.newSession.showManual && a.newSession.manualTitle.Value() != "" && a.newSession.manualDesc.Value() != ""
		}, Handler: func(a *App) tea.Cmd { return func() tea.Msg { return NewSessionManualMsg{} } }},
		{ID: "close_extra_context", Label: "Close extra-context modal", Shortcut: "Esc", Priority: 738, Condition: func(a *App) bool { return a.newSession.showExtraContext }, Handler: func(a *App) tea.Cmd { a.newSession.showExtraContext = false; return nil }},
		{ID: "cycle_new_session_scope", Label: "Cycle scope", Shortcut: "Ctrl+S", Priority: 739, Condition: func(a *App) bool { return true }, Handler: func(a *App) tea.Cmd {
			var cmd tea.Cmd
			a.newSession, cmd = a.newSession.Update(actionKey("ctrl+s"))
			return cmd
		}},
		{ID: "cycle_new_session_view", Label: "Cycle view", Shortcut: "Ctrl+V", Priority: 740, Condition: func(a *App) bool { return true }, Handler: func(a *App) tea.Cmd {
			var cmd tea.Cmd
			a.newSession, cmd = a.newSession.Update(actionKey("ctrl+v"))
			return cmd
		}},
		{ID: "cycle_new_session_state", Label: "Cycle state", Shortcut: "Ctrl+T", Priority: 741, Condition: func(a *App) bool { return true }, Handler: func(a *App) tea.Cmd {
			var cmd tea.Cmd
			a.newSession, cmd = a.newSession.Update(actionKey("ctrl+t"))
			return cmd
		}},
		{ID: "reset_new_session_filters", Label: "Reset filters", Shortcut: "Ctrl+R", Priority: 742, Condition: func(a *App) bool { return true }, Handler: func(a *App) tea.Cmd {
			var cmd tea.Cmd
			a.newSession, cmd = a.newSession.Update(actionKey("ctrl+r"))
			return cmd
		}},
		{ID: "save_new_session_filter", Label: "Save filter", Shortcut: "Ctrl+F", Priority: 743, Condition: func(a *App) bool { return true }, Handler: func(a *App) tea.Cmd {
			var cmd tea.Cmd
			a.newSession, cmd = a.newSession.Update(actionKey("ctrl+f"))
			return cmd
		}},
		{ID: "load_new_session_filter", Label: "Load filter", Shortcut: "Ctrl+L", Priority: 744, Condition: func(a *App) bool { return true }, Handler: func(a *App) tea.Cmd {
			var cmd tea.Cmd
			a.newSession, cmd = a.newSession.Update(actionKey("ctrl+l"))
			return cmd
		}},
		{ID: "manual_new_session", Label: "Manual session", Shortcut: "Ctrl+N", Priority: 745, Condition: func(a *App) bool { return true }, Handler: func(a *App) tea.Cmd {
			var cmd tea.Cmd
			a.newSession, cmd = a.newSession.Update(actionKey("ctrl+n"))
			return cmd
		}},
		{ID: "open_new_session_item", Label: "Open current item in browser", Shortcut: "Ctrl+O", Priority: 746, Condition: func(a *App) bool {
			_, ok := a.newSession.currentListItem()
			return ok
		}, Handler: func(a *App) tea.Cmd {
			return a.newSession.openCurrentItemInBrowserCmd()
		}},
		{ID: "delete_saved_filter", Label: "Delete saved filter", Shortcut: "d", Priority: 747, Condition: func(a *App) bool {
			return a.newSession.filterModalMode == newSessionFilterModalLoadPicker && a.newSession.loadFilterList.Index() >= 0 && a.newSession.loadFilterList.Index() < len(a.newSession.loadFilterChoices)
		}, Handler: func(a *App) tea.Cmd {
			var cmd tea.Cmd
			a.newSession, cmd = a.newSession.Update(actionKey("d"))
			return cmd
		}},
	}
}

func newSessionAutonomousActions(a *App) []Action {
	return []Action{
		{ID: "start_autonomous", Label: "Start autonomous mode", Shortcut: "Enter", Priority: 740, Condition: func(a *App) bool { return len(a.newSessionAutonomousOverlay.selectedFilterIDs()) > 0 }, Handler: func(a *App) tea.Cmd { return func() tea.Msg { return StartNewSessionAutonomousModeMsg{} } }},
		{ID: "stop_autonomous", Label: "Stop autonomous mode", Shortcut: "S", Priority: 742, Condition: func(a *App) bool { return a.newSessionAutonomousOverlay.running }, Handler: func(a *App) tea.Cmd { return func() tea.Msg { return StopNewSessionAutonomousModeMsg{} } }},
	}
}

func addRepoActions(a *App) []Action {
	return []Action{
		{ID: "confirm_manual_url", Label: "Confirm manual URL", Shortcut: "Enter", Priority: 775, Condition: func(a *App) bool { return a.addRepo.showManual }, Handler: func(a *App) tea.Cmd { return func() tea.Msg { return AddRepoCloneMsg{} } }},
		{ID: "manual_clone_url", Label: "Manual clone URL", Shortcut: "Ctrl+N", Priority: 776, Condition: func(a *App) bool { return !a.addRepo.showManual }, Handler: func(a *App) tea.Cmd {
			var cmd tea.Cmd
			a.addRepo, cmd = a.addRepo.Update(actionKey("ctrl+n"))
			return cmd
		}},
		{ID: "back_to_repo_search", Label: "Back to repo search", Shortcut: "Ctrl+N", Priority: 777, Condition: func(a *App) bool { return a.addRepo.showManual }, Handler: func(a *App) tea.Cmd {
			var cmd tea.Cmd
			a.addRepo, cmd = a.addRepo.Update(actionKey("ctrl+n"))
			return cmd
		}},
		{ID: "reload_repos", Label: "Reset search and reload", Shortcut: "Ctrl+R", Priority: 778, Condition: func(a *App) bool { return !a.addRepo.showManual }, Handler: func(a *App) tea.Cmd {
			var cmd tea.Cmd
			a.addRepo, cmd = a.addRepo.Update(actionKey("ctrl+r"))
			return cmd
		}},
		{ID: "toggle_owned_repos", Label: "Toggle owned/all filter", Shortcut: "Ctrl+G", Priority: 779, Condition: func(a *App) bool { return !a.addRepo.showManual }, Handler: func(a *App) tea.Cmd {
			var cmd tea.Cmd
			a.addRepo, cmd = a.addRepo.Update(actionKey("ctrl+g"))
			return cmd
		}},
		{ID: "close_add_repo", Label: "Close add repo", Shortcut: "Esc", Priority: 780, Condition: func(a *App) bool { return true }, Handler: func(a *App) tea.Cmd { return func() tea.Msg { return CloseOverlayMsg{} } }},
	}
}

func repoManagerActions(a *App) []Action {
	return []Action{
		{ID: "add_repo_rm", Label: "Add repo", Shortcut: "a", Priority: 650, Condition: func(a *App) bool { return true }, Handler: func(a *App) tea.Cmd { return a.openAddRepo() }},
		{ID: "delete_repo", Label: "Delete repo", Shortcut: "d", Priority: 651, Condition: func(a *App) bool {
			_, ok := a.repoManager.selectedRepo()
			return ok
		}, Handler: func(a *App) tea.Cmd {
			var cmd tea.Cmd
			a.repoManager, cmd = a.repoManager.Update(actionKey("d"))
			return cmd
		}},
		{ID: "init_repo", Label: "Initialize git-work repo", Shortcut: "i", Priority: 652, Condition: func(a *App) bool {
			repo, ok := a.repoManager.selectedRepo()
			return ok && repo.Kind == repoKindPlainGit && a.repoManager.pendingInit == nil
		}, Handler: func(a *App) tea.Cmd {
			var cmd tea.Cmd
			a.repoManager, cmd = a.repoManager.Update(actionKey("i"))
			return cmd
		}},
	}
}

func worktreePickerActions(a *App) []Action {
	return []Action{
		{ID: "open_selected_worktree_terminal", Label: "Open selected worktree terminal", Shortcut: "t", Priority: 660, Condition: func(a *App) bool {
			return a.worktreePicker.active && !a.worktreePicker.worktreeLoading && len(a.worktreePicker.worktrees) > 0
		}, Handler: func(a *App) tea.Cmd {
			return a.worktreePicker.openTerminalCmd()
		}},
		{ID: "switch_worktree_picker_focus", Label: "Switch picker focus", Shortcut: "Tab", Priority: 670, Condition: func(a *App) bool {
			return a.worktreePicker.active
		}, Handler: func(a *App) tea.Cmd {
			a.worktreePicker.picker.SwitchFocus()
			return nil
		}},
	}
}

func settingsActions(a *App) []Action {
	return []Action{
		{ID: "edit_setting", Label: "Edit setting", Shortcut: "e", Priority: 805, Condition: func(a *App) bool { return a.settingsPage.fieldsFocused() }, Handler: func(a *App) tea.Cmd {
			a.settingsPage.openFieldEditor()
			return nil
		}},
		{ID: "test_provider", Label: "Test provider", Shortcut: "t", Priority: 806, Condition: func(a *App) bool { return true }, Handler: func(a *App) tea.Cmd { return a.settingsPage.testProviderCmd() }},
		{ID: "login_provider", Label: "Login provider", Shortcut: "g", Priority: 807, Condition: func(a *App) bool {
			return providerSupportsLogin(providerForSection(a.settingsPage.currentSection()))
		}, Handler: func(a *App) tea.Cmd { return a.settingsPage.loginProviderCmd(*a.provider.GetServices()) }},
		{ID: "reveal_secrets", Label: "Reveal secrets", Shortcut: "r", Priority: 810, Condition: func(a *App) bool { return true }, Handler: func(a *App) tea.Cmd { a.settingsPage.revealSecrets = !a.settingsPage.revealSecrets; return nil }},
	}
}

func logsActions(a *App) []Action {
	return []Action{
		{ID: "copy_log", Label: "Copy log", Shortcut: "c", Priority: 899, Condition: func(a *App) bool { return true }, Handler: func(a *App) tea.Cmd {
			var cmd tea.Cmd
			a.logsOverlay, cmd = a.logsOverlay.Update(actionKey("c"))
			return cmd
		}},
		{ID: "close_logs", Label: "Close logs", Shortcut: "Esc", Priority: 900, Condition: func(a *App) bool { return true }, Handler: func(a *App) tea.Cmd { return func() tea.Msg { return CloseOverlayMsg{} } }},
	}
}

func sessionSearchActions(a *App) []Action {
	return []Action{
		{ID: "toggle_search_scope", Label: "Toggle workspace/global scope", Shortcut: "Ctrl+S", Priority: 849, Condition: func(a *App) bool { return true }, Handler: func(a *App) tea.Cmd { return a.sessionSearch.toggleScope() }},
		{ID: "open_selected_session", Label: "Open selected session", Shortcut: "Enter", Priority: 850, Condition: func(a *App) bool { return a.sessionSearch.Selected() != nil }, Handler: func(a *App) tea.Cmd {
			entry := a.sessionSearch.Selected()
			if entry != nil {
				return func() tea.Msg { return OpenSessionHistoryMsg{Entry: *entry} }
			}
			return nil
		}},
		{ID: "delete_selected_session", Label: "Delete selected session", Shortcut: "d", Priority: 851, Condition: func(a *App) bool {
			entry := a.sessionSearch.Selected()
			return entry != nil && strings.TrimSpace(entry.WorkItemID) != ""
		}, Handler: func(a *App) tea.Cmd {
			entry := a.sessionSearch.Selected()
			if entry != nil {
				return func() tea.Msg { return ConfirmDeleteSessionMsg{SessionID: entry.WorkItemID} }
			}
			return nil
		}},
		{ID: "close_search", Label: "Close search", Shortcut: "Esc", Priority: 865, Condition: func(a *App) bool { return true }, Handler: func(a *App) tea.Cmd { return func() tea.Msg { return CloseOverlayMsg{} } }},
	}
}

func reviewFollowupPickerActions(a *App) []Action {
	return []Action{
		{ID: "nav_picker_up", Label: "Navigate up", Shortcut: "↑", Priority: 500, Condition: func(a *App) bool { return true }, Handler: func(a *App) tea.Cmd {
			if a.reviewFollowupOverlay.pickerCursor > 0 {
				a.reviewFollowupOverlay.pickerCursor--
			}
			return nil
		}},
		{ID: "nav_picker_down", Label: "Navigate down", Shortcut: "↓", Priority: 501, Condition: func(a *App) bool { return true }, Handler: func(a *App) tea.Cmd {
			if a.reviewFollowupOverlay.pickerCursor < len(a.reviewFollowupOverlay.pickerItems)-1 {
				a.reviewFollowupOverlay.pickerCursor++
			}
			return nil
		}},
		{ID: "toggle_picker", Label: "Toggle selection", Shortcut: "Space", Priority: 510, Condition: func(a *App) bool { return true }, Handler: func(a *App) tea.Cmd {
			id := a.reviewFollowupOverlay.pickerItems[a.reviewFollowupOverlay.pickerCursor].ID
			a.reviewFollowupOverlay.pickerSelected[id] = !a.reviewFollowupOverlay.pickerSelected[id]
			return nil
		}},
		{ID: "select_all_picker", Label: "Select all", Shortcut: "a", Priority: 520, Condition: func(a *App) bool { return true }, Handler: func(a *App) tea.Cmd {
			for _, it := range a.reviewFollowupOverlay.pickerItems {
				a.reviewFollowupOverlay.pickerSelected[it.ID] = true
			}
			return nil
		}},
		{ID: "deselect_all_picker", Label: "Deselect all", Shortcut: "n", Priority: 530, Condition: func(a *App) bool { return true }, Handler: func(a *App) tea.Cmd { a.reviewFollowupOverlay.pickerSelected = make(map[string]bool); return nil }},
		{ID: "confirm_picker", Label: "Confirm", Shortcut: "Enter", Priority: 540, Condition: func(a *App) bool { return a.reviewFollowupOverlay.pickerHasAnySelected() }, Handler: func(a *App) tea.Cmd { a.reviewFollowupOverlay.applyPickerSelection(); return nil }},
	}
}

func reviewFollowupSelectorActions(a *App) []Action {
	return []Action{
		{ID: "nav_selector_up", Label: "Navigate up", Shortcut: "↑", Priority: 500, Condition: func(a *App) bool { return true }, Handler: func(a *App) tea.Cmd { a.reviewFollowupOverlay.moveCursor(-1); return nil }},
		{ID: "nav_selector_down", Label: "Navigate down", Shortcut: "↓", Priority: 501, Condition: func(a *App) bool { return true }, Handler: func(a *App) tea.Cmd { a.reviewFollowupOverlay.moveCursor(1); return nil }},
		{ID: "toggle_selector", Label: "Toggle selection", Shortcut: "Space", Priority: 510, Condition: func(a *App) bool { return true }, Handler: func(a *App) tea.Cmd { a.reviewFollowupOverlay.toggleAtCursor(); return nil }},
		{ID: "select_all_selector", Label: "Select all", Shortcut: "a", Priority: 520, Condition: func(a *App) bool { return true }, Handler: func(a *App) tea.Cmd { a.reviewFollowupOverlay.selectAll(); return nil }},
		{ID: "deselect_all_selector", Label: "Deselect all", Shortcut: "n", Priority: 530, Condition: func(a *App) bool { return true }, Handler: func(a *App) tea.Cmd { a.reviewFollowupOverlay.selectNone(); return nil }},
		{ID: "focus_list_selector", Label: "Focus list", Shortcut: "←", Priority: 550, Condition: func(a *App) bool { return true }, Handler: func(a *App) tea.Cmd { a.reviewFollowupOverlay.focus = reviewSelectorFocusList; return nil }},
		{ID: "focus_preview_selector", Label: "Focus preview", Shortcut: "→", Priority: 560, Condition: func(a *App) bool { return true }, Handler: func(a *App) tea.Cmd { a.reviewFollowupOverlay.focus = reviewSelectorFocusPreview; return nil }},
		{ID: "address_critique", Label: "Address critique", Shortcut: "A", Priority: 570, Condition: func(a *App) bool { return a.reviewFollowupOverlay.HasAnySelection() }, Handler: func(a *App) tea.Cmd { return a.reviewFollowupOverlay.dispatchAddress() }},
		{ID: "replan_selected_critiques", Label: "Replan selected critiques", Shortcut: "p", Priority: 571, Condition: func(a *App) bool { return a.reviewFollowupOverlay.HasAnySelection() }, Handler: func(a *App) tea.Cmd {
			a.reviewFollowupOverlay.stage = reviewFollowupStageConfirm
			return nil
		}},
	}
}

func reviewFollowupConfirmActions(a *App) []Action {
	return []Action{
		{ID: "confirm_replan", Label: "Confirm replan", Shortcut: "y", Priority: 580, Condition: func(a *App) bool { return true }, Handler: func(a *App) tea.Cmd { return a.reviewFollowupOverlay.dispatchReplan() }},
	}
}

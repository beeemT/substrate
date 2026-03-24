package views

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/repository"
	"github.com/beeemT/substrate/internal/service"
	"github.com/beeemT/substrate/internal/sessionlog"
	"github.com/beeemT/substrate/internal/tui/components"
	"github.com/beeemT/substrate/internal/tui/styles"
)

// overlayKind identifies which overlay is active.
type overlayKind int

const (
	overlayNone overlayKind = iota
	overlayNewSession
	overlaySessionSearch
	overlaySettings
	overlayWorkspaceInit
	overlayHelp
	overlaySourceItems
	overlayLogs
)

type sessionHistoryScope int

const (
	sessionHistoryScopeWorkspace sessionHistoryScope = iota
	sessionHistoryScopeGlobal
)

func (s sessionHistoryScope) Label() string {
	if s == sessionHistoryScopeGlobal {
		return "global"
	}
	return "workspace"
}

type sidebarPaneMode int

const (
	sidebarPaneSessions sidebarPaneMode = iota
	sidebarPaneTasks
)

const taskSidebarSourceDetailsID = "__source_details__"

type mainFocusArea int

const (
	mainFocusSidebar mainFocusArea = iota
	mainFocusContent
)

const appContentHorizontalPadding = 1

func appContentBodyWidth(width int) int {
	if width > appContentHorizontalPadding*2 {
		return width - (appContentHorizontalPadding * 2)
	}
	return width
}

// App is the top-level bubbletea model.
type App struct { //nolint:recvcheck // Bubble Tea convention
	svcs Services

	// Layout sub-models
	sidebar   SidebarModel
	content   ContentModel
	statusBar StatusBarModel

	// Overlays
	activeOverlay      overlayKind
	newSession         NewSessionOverlay
	sessionSearch      SessionSearchOverlay
	settingsPage       SettingsPage
	workspaceModal     WorkspaceInitModal
	helpOverlay        HelpOverlay
	sourceItemsOverlay SourceItemsOverlay
	logsOverlay        LogsOverlay
	hasWorkspace       bool

	// Toasts
	toasts components.ToastModel

	// State cache (refreshed by DB poll)
	workItems []domain.Session
	sessions  []domain.Task
	subPlans  map[string][]domain.TaskPlan // keyed by planID
	plans     map[string]*domain.Plan      // keyed by workItemID
	questions map[string][]domain.Question // keyed by sessionID
	reviews   map[string]ReviewsLoadedMsg  // keyed by sessionID

	// Log tailing deduplication
	tailingSessionIDs map[string]bool
	// reviewSessionLogs maps implementation session ID → review agent log path.
	reviewSessionLogs map[string]string

	// Live instance cache for dead-owner detection
	liveInstanceIDs map[string]bool

	// Confirm dialog
	confirm       components.ConfirmDialog
	confirmActive bool

	// Duplicate-session dialog
	duplicateSession       duplicateSessionDialogState
	duplicateSessionActive bool

	// Current selection
	currentWorkItemID              string
	currentHistorySessionID        string
	currentHistoryEntry            SidebarEntry
	sidebarMode                    sidebarPaneMode
	mainFocus                      mainFocusArea
	overviewOverlayPrevFocus       mainFocusArea
	overviewOverlayFocusSaved      bool
	taskSessionSelectionByWorkItem map[string]string

	// Session log tailing
	sessionsDir string

	// Terminal size
	windowWidth  int
	windowHeight int

	// Foreman lifecycle
	foremanPlanID string // plan ID the Foreman was last started for

	// Pipeline cancellation: maps workItemID → cancel func for the running
	// orchestrator goroutine (implementation/planning). Used to tear down
	// agent processes when the session is deleted.
	pipelineCancels map[string]context.CancelFunc
}

// NewApp creates a new App from the given Services.
func NewApp(svcs Services) App {
	st := styles.NewStyles(styles.DefaultTheme)
	sessionsDir, _ := config.SessionsDir()
	cwd, _ := os.Getwd()

	app := App{
		svcs:                           svcs,
		sidebar:                        NewSidebarModel(st),
		content:                        NewContentModel(st),
		statusBar:                      NewStatusBarModel(st),
		newSession:                     NewNewSessionOverlay(svcs.Adapters, svcs.WorkspaceID, st),
		sessionSearch:                  NewSessionSearchOverlay(st),
		settingsPage:                   NewSettingsPage(svcs.Settings, svcs.SettingsData, st),
		helpOverlay:                    NewHelpOverlay(st),
		sourceItemsOverlay:             NewSourceItemsOverlay(st),
		logsOverlay:                    NewLogsOverlay(svcs.LogStore, st),
		toasts:                         components.NewToastModel(st),
		subPlans:                       make(map[string][]domain.TaskPlan),
		plans:                          make(map[string]*domain.Plan),
		questions:                      make(map[string][]domain.Question),
		reviews:                        make(map[string]ReviewsLoadedMsg),
		tailingSessionIDs:              make(map[string]bool),
		liveInstanceIDs:                make(map[string]bool),
		reviewSessionLogs:              make(map[string]string),
		taskSessionSelectionByWorkItem: make(map[string]string),
		pipelineCancels:                make(map[string]context.CancelFunc),
		sessionsDir:                    sessionsDir,
		hasWorkspace:                   svcs.WorkspaceID != "",
	}

	if !app.hasWorkspace {
		app.workspaceModal = NewWorkspaceInitModal(cwd, st, svcs.Workspace)
		app.activeOverlay = overlayWorkspaceInit
	}
	return app
}

// RunTUI launches the bubbletea program.
func RunTUI(svcs Services) error {
	app := NewApp(svcs)
	p := tea.NewProgram(app, tea.WithMouseCellMotion(), tea.WithFilter(macOSKeyFilter))

	// Intercept SIGTERM so the quit-confirmation modal can run before exit.
	// SIGINT is delivered by bubbletea as a ctrl+c key event; only SIGTERM needs
	// explicit handling here.
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM)
	go func() {
		for range sigs {
			p.Send(QuitRequestMsg{})
		}
	}()

	_, err := p.Run()
	signal.Stop(sigs)
	close(sigs)
	return err
}

// Init returns the initial set of commands.
func (a App) Init() tea.Cmd {
	var cmds []tea.Cmd

	cmds = append(cmds, tea.ClearScreen, PollTickCmd(), HeartbeatTickCmd(), components.ToastTickCmd(), WaitForAdapterErrorCmd(a.svcs.AdapterErrors), WaitForLogToastCmd(a.svcs.LogToasts), StartupWarningsCmd(a.svcs.StartupWarnings))

	if a.svcs.WorkspaceID != "" {
		cmds = append(cmds,
			LoadSessionsCmd(a.svcs.Session, a.svcs.WorkspaceID),
			LoadTasksCmd(a.svcs.Task, a.svcs.WorkspaceID),
			LoadLiveInstancesCmd(a.svcs.Instance, a.svcs.WorkspaceID),
			ReconcileOrphanedTasksCmd(a.svcs.Task, a.svcs.Instance, a.svcs.WorkspaceID, a.svcs.InstanceID),
		)
	}

	if a.activeOverlay == overlayWorkspaceInit {
		cmds = append(cmds, a.workspaceModal.ScanCmd())
	}

	return tea.Batch(cmds...)
}

func (a *App) applyServicesReload(reload viewsServicesReload) {
	a.svcs = reload.Services
	a.newSession = NewNewSessionOverlay(a.svcs.Adapters, a.svcs.WorkspaceID, a.statusBar.styles)
	a.settingsPage.SetSnapshot(reload.SettingsData)
	a.sessionsDir = reload.SessionsDir
	a.hasWorkspace = a.svcs.WorkspaceID != ""
}

func sameSessionHistoryFilter(current, incoming domain.SessionHistoryFilter) bool {
	if strings.TrimSpace(current.Search) != strings.TrimSpace(incoming.Search) {
		return false
	}
	switch {
	case current.WorkspaceID == nil && incoming.WorkspaceID == nil:
		return true
	case current.WorkspaceID != nil && incoming.WorkspaceID != nil:
		return *current.WorkspaceID == *incoming.WorkspaceID
	default:
		return false
	}
}

func (a App) sessionSearchScope() sessionHistoryScope {
	if a.hasWorkspace {
		return sessionHistoryScopeWorkspace
	}
	return sessionHistoryScopeGlobal
}

func (a App) sessionSearchFilter() domain.SessionHistoryFilter {
	return a.sessionSearch.Filter(a.svcs.WorkspaceID)
}

func sessionHistoryEntryKey(entry domain.SessionHistoryEntry) string {
	if entry.WorkItemID != "" {
		return "work-item:" + entry.WorkspaceID + ":" + entry.WorkItemID
	}
	if entry.SessionID != "" {
		return "session:" + entry.WorkspaceID + ":" + entry.SessionID
	}
	return fmt.Sprintf("fallback:%s:%s:%s", entry.WorkspaceID, entry.WorkItemExternalID, entry.WorkItemTitle)
}

func mergeSessionHistoryEntries(primary, secondary []domain.SessionHistoryEntry) []domain.SessionHistoryEntry {
	merged := make([]domain.SessionHistoryEntry, 0, len(primary)+len(secondary))
	seen := make(map[string]struct{}, len(primary)+len(secondary))
	appendUnique := func(entries []domain.SessionHistoryEntry) {
		for _, entry := range entries {
			key := sessionHistoryEntryKey(entry)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			merged = append(merged, entry)
		}
	}
	appendUnique(primary)
	appendUnique(secondary)
	return merged
}

func sessionHistoryEntryMatches(entry domain.SessionHistoryEntry, search string) bool {
	search = strings.TrimSpace(strings.ToLower(search))
	if search == "" {
		return true
	}
	fields := []string{
		entry.WorkItemID,
		entry.SessionID,
		entry.WorkspaceName,
		entry.WorkItemExternalID,
		entry.WorkItemTitle,
		entry.RepositoryName,
		entry.HarnessName,
		string(entry.WorkItemState),
		string(entry.Status),
	}
	for _, field := range fields {
		if strings.Contains(strings.ToLower(field), search) {
			return true
		}
	}
	return false
}

func (a App) localSessionSearchEntry(wi domain.Session) (domain.SessionHistoryEntry, bool) {
	workspaceName := ""
	if wi.WorkspaceID == a.svcs.WorkspaceID {
		workspaceName = strings.TrimSpace(a.svcs.WorkspaceName)
	}
	entry := domain.SessionHistoryEntry{
		WorkspaceID:        wi.WorkspaceID,
		WorkspaceName:      workspaceName,
		WorkItemID:         wi.ID,
		WorkItemExternalID: wi.ExternalID,
		WorkItemTitle:      wi.Title,
		WorkItemState:      wi.State,
		CreatedAt:          wi.CreatedAt,
		UpdatedAt:          wi.UpdatedAt,
	}
	sessions := a.sessionsForWorkItem(wi.ID)
	if len(sessions) == 0 {
		return entry, true
	}

	latest := sessions[0]
	hasOpenQuestion := false
	hasInterrupted := false
	for _, session := range sessions {
		if session.UpdatedAt.After(latest.UpdatedAt) || (session.UpdatedAt.Equal(latest.UpdatedAt) && (session.CreatedAt.After(latest.CreatedAt) || (session.CreatedAt.Equal(latest.CreatedAt) && session.ID > latest.ID))) {
			latest = session
		}
		if session.Status == domain.AgentSessionWaitingForAnswer {
			hasOpenQuestion = true
		}
		if session.Status == domain.AgentSessionInterrupted {
			hasInterrupted = true
		}
	}
	if latest.UpdatedAt.After(entry.UpdatedAt) {
		entry.UpdatedAt = latest.UpdatedAt
	}
	entry.SessionID = latest.ID
	entry.RepositoryName = latest.RepositoryName
	entry.HarnessName = latest.HarnessName
	entry.Status = latest.Status
	entry.AgentSessionCount = len(sessions)
	entry.HasOpenQuestion = hasOpenQuestion
	entry.HasInterrupted = hasInterrupted
	entry.CompletedAt = latest.CompletedAt
	return entry, true
}

func (a App) localSessionSearchEntries(filter domain.SessionHistoryFilter) []domain.SessionHistoryEntry {
	if filter.WorkspaceID != nil && *filter.WorkspaceID != a.svcs.WorkspaceID {
		return nil
	}
	entries := make([]domain.SessionHistoryEntry, 0, len(a.workItems))
	for _, wi := range a.workItems {
		entry, ok := a.localSessionSearchEntry(wi)
		if !ok || !sessionHistoryEntryMatches(entry, filter.Search) {
			continue
		}
		entries = append(entries, entry)
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if !entries[i].UpdatedAt.Equal(entries[j].UpdatedAt) {
			return entries[i].UpdatedAt.After(entries[j].UpdatedAt)
		}
		if !entries[i].CreatedAt.Equal(entries[j].CreatedAt) {
			return entries[i].CreatedAt.After(entries[j].CreatedAt)
		}
		return firstNonEmptyString(entries[i].WorkItemID, entries[i].SessionID) < firstNonEmptyString(entries[j].WorkItemID, entries[j].SessionID)
	})
	if filter.Offset >= len(entries) {
		return nil
	}
	if filter.Offset > 0 {
		entries = entries[filter.Offset:]
	}
	if filter.Limit > 0 && len(entries) > filter.Limit {
		entries = entries[:filter.Limit]
	}
	return entries
}

func (a *App) refreshSessionSearchEntriesFromLocalState() {
	if !a.sessionSearch.Active() {
		return
	}
	a.sessionSearch.SetEntries(mergeSessionHistoryEntries(a.localSessionSearchEntries(a.sessionSearchFilter()), a.sessionSearch.entries))
}

func (a *App) runSessionSearch(showLoading bool) tea.Cmd {
	if !a.sessionSearch.Active() {
		return nil
	}
	filter := a.sessionSearchFilter()
	var spinnerCmd tea.Cmd
	if showLoading {
		spinnerCmd = a.sessionSearch.SetLoading(true)
	}
	a.sessionSearch.SetEntries(a.localSessionSearchEntries(filter))
	return tea.Batch(spinnerCmd, SearchSessionHistoryCmd(a.svcs.Task, filter))
}

func (a *App) openSessionSearch() tea.Cmd {
	a.activeOverlay = overlaySessionSearch
	a.sessionSearch.Open(a.sessionSearchScope(), a.hasWorkspace)
	return a.runSessionSearch(true)
}

func (a *App) openNewSession() tea.Cmd {
	a.activeOverlay = overlayNewSession
	a.newSession.Open()
	return a.newSession.reloadItems()
}

func (a App) currentHints() []KeybindHint {
	global := DefaultHints()
	prependDelete := func(hints []KeybindHint) []KeybindHint {
		if a.deletableSessionID() == "" {
			return hints
		}
		return append([]KeybindHint{{Key: "d", Label: "Delete session"}}, hints...)
	}
	if a.mainFocus == mainFocusContent {
		hints := append([]KeybindHint{{Key: "←/Esc", Label: "Back"}}, a.content.KeybindHints()...)
		return append(prependDelete(hints), global...)
	}
	if a.sidebarMode == sidebarPaneTasks {
		hints := []KeybindHint{{Key: "↑/↓", Label: "Tasks"}, {Key: "→", Label: "Content"}, {Key: "←/Esc", Label: "Sessions"}}
		if a.selectedTaskSessionID() != "" && a.sourceDetailsNoticeForWorkItem(a.workItemByID(a.currentWorkItemID)) != nil {
			hints = append([]KeybindHint{{Key: "Enter", Label: "Open overview"}}, hints...)
		}
		return append(prependDelete(hints), global...)
	}
	return append(prependDelete([]KeybindHint{{Key: "↑/↓", Label: "Sessions"}, {Key: "→", Label: "Tasks"}}), global...)
}

func (a App) overviewOverlayOpen() bool {
	return a.content.mode == ContentModeOverview && a.content.overview.overlay != overviewOverlayNone
}

func (a *App) syncOverviewOverlayFocus(wasOpen bool, previousFocus mainFocusArea) {
	isOpen := a.overviewOverlayOpen()
	switch {
	case !wasOpen && isOpen:
		a.overviewOverlayPrevFocus = previousFocus
		a.overviewOverlayFocusSaved = true
		a.mainFocus = mainFocusContent
	case wasOpen && !isOpen:
		if a.overviewOverlayFocusSaved {
			a.mainFocus = a.overviewOverlayPrevFocus
			a.overviewOverlayFocusSaved = false
		}
	case isOpen:
		a.mainFocus = mainFocusContent
	}
}

func (a *App) updateContentForKey(msg tea.KeyMsg, wasOverviewOverlayOpen bool, previousFocus mainFocusArea) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	a.content, cmd = a.content.Update(msg)
	a.syncOverviewOverlayFocus(wasOverviewOverlayOpen, previousFocus)
	return *a, cmd
}

func (a App) historyEntryTitle(entry SidebarEntry) string {
	if entry.Title != "" {
		return entry.Title
	}
	if entry.ExternalID != "" {
		return entry.ExternalID
	}
	if entry.WorkItemID != "" {
		return entry.WorkItemID
	}
	return entry.SessionID
}

func (a App) historyEntryMeta(entry SidebarEntry) string {
	parts := []string{"Work item " + firstNonEmptyString(entry.ExternalID, entry.WorkItemID)}
	if entry.WorkspaceName != "" {
		parts = append(parts, entry.WorkspaceName)
	}
	if entry.RepositoryName != "" {
		parts = append(parts, entry.RepositoryName)
	}
	if entry.SessionID != "" {
		parts = append(parts, "latest agent session "+entry.SessionID)
	}
	return strings.Join(parts, " · ")
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func (a App) workItemByID(workItemID string) *domain.Session {
	for i := range a.workItems {
		if a.workItems[i].ID == workItemID {
			return &a.workItems[i]
		}
	}
	return nil
}

func (a *App) upsertWorkItem(workItem domain.Session) {
	for i := range a.workItems {
		if a.workItems[i].ID == workItem.ID {
			a.workItems[i] = workItem
			return
		}
	}
	a.workItems = append(a.workItems, workItem)
}

func (a *App) focusWorkItemOverview(workItemID string) tea.Cmd {
	a.sidebarMode = sidebarPaneSessions
	a.mainFocus = mainFocusSidebar
	a.taskSessionSelectionByWorkItem[workItemID] = ""
	a.rebuildSidebar()
	a.sidebar.SelectWorkItem(workItemID)
	a.currentHistorySessionID = ""
	a.currentHistoryEntry = SidebarEntry{}
	a.currentWorkItemID = workItemID
	return a.updateContentFromState()
}

func (a App) selectedTaskSessionID() string {
	if a.currentWorkItemID == "" {
		return ""
	}
	return a.taskSessionSelectionByWorkItem[a.currentWorkItemID]
}

func (a *App) setSelectedTaskSessionID(sessionID string) {
	if a.currentWorkItemID == "" {
		return
	}
	a.taskSessionSelectionByWorkItem[a.currentWorkItemID] = sessionID
}

func (a App) deletableSessionID() string {
	if a.currentHistoryEntry.WorkItemID != "" {
		return a.currentHistoryEntry.WorkItemID
	}
	if a.currentWorkItemID != "" {
		return a.currentWorkItemID
	}
	return ""
}

func (a *App) enterTaskSidebar() tea.Cmd {
	if a.currentWorkItemID == "" {
		return nil
	}
	a.sidebarMode = sidebarPaneTasks
	a.mainFocus = mainFocusSidebar
	if a.selectedTaskSessionID() == "" {
		a.setSelectedTaskSessionID(a.defaultTaskSessionID(a.currentWorkItemID))
	}
	a.rebuildSidebar()
	if !a.sidebar.SelectEntry(a.currentWorkItemID, a.selectedTaskSessionID()) {
		a.sidebar.SelectEntry(a.currentWorkItemID, "")
	}
	a.tailingSessionIDs = make(map[string]bool)
	a.currentHistorySessionID = ""
	a.currentHistoryEntry = SidebarEntry{}
	if sel := a.sidebar.Selected(); sel != nil {
		a.currentWorkItemID = sel.WorkItemID
		a.setSelectedTaskSessionID(sel.SessionID)
	}
	return a.updateContentFromState()
}

func (a *App) exitTaskSidebar() tea.Cmd {
	a.sidebarMode = sidebarPaneSessions
	a.mainFocus = mainFocusSidebar
	a.rebuildSidebar()
	a.sidebar.SelectWorkItem(a.currentWorkItemID)
	a.tailingSessionIDs = make(map[string]bool)
	a.currentHistorySessionID = ""
	a.currentHistoryEntry = SidebarEntry{}
	return a.updateContentFromState()
}

func (a App) workItemTaskSession(workItemID, sessionID string) *domain.Task {
	for _, session := range a.sessionsForWorkItem(workItemID) {
		if session.ID == sessionID {
			s := session
			return &s
		}
	}
	return nil
}

func (a App) defaultTaskSessionID(workItemID string) string {
	_ = workItemID
	return ""
}

func (a App) latestPlanningSession(workItemID string) *domain.Task {
	for _, session := range a.sessionsForWorkItem(workItemID) {
		if session.Phase == domain.TaskPhasePlanning {
			s := session
			return &s
		}
	}
	return nil
}

func (a App) latestImplementationSession(workItemID, subPlanID string) *domain.Task {
	for _, session := range a.sessionsForWorkItem(workItemID) {
		if session.Phase == domain.TaskPhaseImplementation && session.SubPlanID == subPlanID {
			s := session
			return &s
		}
	}
	return nil
}

func taskSessionPhaseRank(phase domain.TaskPhase) int {
	switch phase {
	case domain.TaskPhasePlanning:
		return 0
	case domain.TaskPhaseImplementation:
		return 1
	case domain.TaskPhaseReview:
		return 2
	default:
		return 3
	}
}

func taskSessionModeLabel(session *domain.Task) string {
	switch session.Phase {
	case domain.TaskPhasePlanning:
		return "Planning"
	case domain.TaskPhaseReview:
		return "Review"
	default:
		return "Task"
	}
}

func taskSidebarSessionTitle(session *domain.Task) string {
	switch session.Phase {
	case domain.TaskPhasePlanning:
		return "Planning session " + shortSessionID(session.ID)
	case domain.TaskPhaseReview:
		return "Review session " + shortSessionID(session.ID)
	default:
		return "Session " + shortSessionID(session.ID)
	}
}

func taskSessionDisplayName(session *domain.Task) string {
	switch session.Phase {
	case domain.TaskPhasePlanning:
		return "Planning"
	case domain.TaskPhaseReview:
		return firstNonEmptyString(session.RepositoryName, "Review")
	default:
		return firstNonEmptyString(session.RepositoryName, "Task")
	}
}

func (a App) historyEntrySummaryLines(entry SidebarEntry) []sessionlog.Entry {
	entries := []sessionlog.Entry{
		{Kind: sessionlog.KindPlain, Text: "No agent-session log is available for this work item yet."},
		{Kind: sessionlog.KindPlain, Text: "State: " + entry.Subtitle()},
	}
	if entry.WorkspaceName != "" {
		entries = append(entries, sessionlog.Entry{Kind: sessionlog.KindPlain, Text: "Workspace: " + entry.WorkspaceName})
	}
	if entry.RepositoryName != "" {
		entries = append(entries, sessionlog.Entry{Kind: sessionlog.KindPlain, Text: "Latest repo: " + entry.RepositoryName})
	}
	return entries
}

func (a App) sidebarEntryFromHistory(entry domain.SessionHistoryEntry) SidebarEntry {
	return SidebarEntry{
		Kind:            SidebarEntrySessionHistory,
		WorkItemID:      entry.WorkItemID,
		SessionID:       entry.SessionID,
		WorkspaceID:     entry.WorkspaceID,
		WorkspaceName:   entry.WorkspaceName,
		ExternalID:      entry.WorkItemExternalID,
		Title:           entry.WorkItemTitle,
		State:           entry.WorkItemState,
		SessionStatus:   entry.Status,
		RepositoryName:  entry.RepositoryName,
		LastActivity:    entry.UpdatedAt,
		HasOpenQuestion: entry.HasOpenQuestion,
		HasInterrupted:  entry.HasInterrupted,
	}
}

func (a App) historyEntryIsLocal(entry SidebarEntry) bool {
	if entry.WorkspaceID == "" || entry.WorkspaceID != a.svcs.WorkspaceID || entry.WorkItemID == "" {
		return false
	}
	for _, workItem := range a.workItems {
		if workItem.ID == entry.WorkItemID {
			return true
		}
	}
	return false
}

func (a App) historyEntryIsReadOnly(entry SidebarEntry) bool {
	return entry.WorkspaceID != "" && entry.WorkspaceID != a.svcs.WorkspaceID
}

func (a App) readOnlyToast() (components.Toast, bool) {
	if !a.historyEntryIsReadOnly(a.currentHistoryEntry) {
		return components.Toast{}, false
	}
	return components.Toast{Message: "Read only", Level: components.ToastWarning}, true
}

func (a App) harnessWarningToast() (components.Toast, bool) {
	warning := strings.TrimSpace(a.svcs.SettingsData.HarnessWarning)
	if warning == "" {
		return components.Toast{}, false
	}
	return components.Toast{Message: warning, Level: components.ToastWarning}, true
}

func (a App) pinnedToasts() []components.Toast {
	pinned := make([]components.Toast, 0, 2)
	if readOnlyToast, ok := a.readOnlyToast(); ok {
		pinned = append(pinned, readOnlyToast)
	}
	if harnessWarning, ok := a.harnessWarningToast(); ok {
		pinned = append(pinned, harnessWarning)
	}
	return pinned
}

func (a *App) loadHistoryEntry(entry SidebarEntry) tea.Cmd {
	a.tailingSessionIDs = make(map[string]bool)
	a.currentHistoryEntry = SidebarEntry{}
	a.sidebarMode = sidebarPaneSessions
	a.mainFocus = mainFocusSidebar
	a.rebuildSidebar()
	if a.historyEntryIsLocal(entry) {
		a.currentHistorySessionID = ""
		a.currentWorkItemID = entry.WorkItemID
		a.sidebar.SelectWorkItem(entry.WorkItemID)
		return a.updateContentFromState()
	}
	a.currentWorkItemID = ""
	a.currentHistoryEntry = entry
	if entry.SessionID == "" {
		a.currentHistorySessionID = ""
		a.content.SetSessionInteraction(a.historyEntryTitle(entry), a.historyEntryMeta(entry), a.historyEntrySummaryLines(entry))
		return nil
	}
	a.currentHistorySessionID = entry.SessionID
	a.content.SetSessionInteraction(a.historyEntryTitle(entry), a.historyEntryMeta(entry), nil)
	return LoadSessionInteractionCmd(a.sessionsDir, entry.SessionID)
}

// Update is the bubbletea message handler.
func (a App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.windowWidth = msg.Width
		a.windowHeight = msg.Height
		a.toasts.SetWidth(msg.Width)
		layout := styles.ComputeMainPageLayout(msg.Width, msg.Height, SidebarWidth, a.statusBar.styles.Chrome)
		a.sidebar.SetWidth(layout.SidebarInnerWidth)
		a.sidebar.SetHeight(layout.PaneInnerHeight)
		a.content.SetSize(appContentBodyWidth(layout.ContentInnerWidth), layout.PaneInnerHeight)
		a.content.SetTerminalSize(msg.Width, msg.Height)
		a.workspaceModal.SetSize(msg.Width, msg.Height)
		a.newSession.SetSize(msg.Width, msg.Height)
		a.sessionSearch.SetSize(msg.Width, msg.Height)
		a.settingsPage.SetSize(msg.Width, msg.Height)
		a.sourceItemsOverlay.SetSize(msg.Width, msg.Height)
		a.logsOverlay.SetSize(msg.Width, msg.Height)
		return a, nil

	case WorkspaceHealthCheckMsg:
		if a.activeOverlay == overlayWorkspaceInit {
			a.workspaceModal, cmd = a.workspaceModal.Update(msg)
			cmds = append(cmds, cmd)
		}
		return a, tea.Batch(cmds...)

	case WorkspaceInitDoneMsg:
		cmds = append(cmds, initializeWorkspaceServicesCmd(
			a.svcs.Settings,
			a.svcs,
			msg.WorkspaceID,
			msg.WorkspaceName,
			msg.WorkspaceDir,
		))
		return a, tea.Batch(cmds...)

	case WorkspaceServicesReloadedMsg:
		a.applyServicesReload(msg.Reload)
		a.activeOverlay = overlayNone
		a.toasts.AddToast(msg.Message, components.ToastSuccess)
		if a.svcs.WorkspaceID != "" {
			cmds = append(cmds,
				LoadSessionsCmd(a.svcs.Session, a.svcs.WorkspaceID),
				LoadTasksCmd(a.svcs.Task, a.svcs.WorkspaceID),
				LoadLiveInstancesCmd(a.svcs.Instance, a.svcs.WorkspaceID),
				ReconcileOrphanedTasksCmd(a.svcs.Task, a.svcs.Instance, a.svcs.WorkspaceID, a.svcs.InstanceID),
			)
		}
		cmds = append(cmds, WaitForAdapterErrorCmd(a.svcs.AdapterErrors))
		return a, tea.Batch(cmds...)

	case QuitRequestMsg:
		return a.handleQuitRequest()

	case QuitConfirmedMsg:
		a.teardownAllPipelines()
		return a, a.quitCmd()

	case WorkspaceCancelMsg:
		return a, tea.Quit

	case CloseOverlayMsg:
		a.activeOverlay = overlayNone
		a.newSession.Close()
		a.sessionSearch.Close()
		a.settingsPage.Close()
		a.sourceItemsOverlay.Close()
		return a, nil

	case PollTickMsg:
		a.toasts.Prune()
		if a.svcs.WorkspaceID != "" {
			cmds = append(cmds,
				LoadSessionsCmd(a.svcs.Session, a.svcs.WorkspaceID),
				LoadTasksCmd(a.svcs.Task, a.svcs.WorkspaceID),
				LoadLiveInstancesCmd(a.svcs.Instance, a.svcs.WorkspaceID),
			)
		}
		if a.activeOverlay == overlaySessionSearch {
			cmds = append(cmds, a.runSessionSearch(false))
		}
		cmds = append(cmds, PollTickCmd())
		return a, tea.Batch(cmds...)

	case HeartbeatTickMsg:
		if a.svcs.InstanceID != "" {
			cmds = append(cmds, HeartbeatCmd(a.svcs.Instance, a.svcs.InstanceID))
		}
		cmds = append(cmds, HeartbeatTickCmd())
		return a, tea.Batch(cmds...)

	case components.ToastTickMsg:
		a.toasts.Prune()
		cmds = append(cmds, components.ToastTickCmd())
		return a, tea.Batch(cmds...)

	case SessionsLoadedMsg:
		if msg.WorkspaceID != a.svcs.WorkspaceID {
			return a, nil
		}
		a.workItems = msg.Items
		a.rebuildSidebar()
		a.refreshSessionSearchEntriesFromLocalState()
		return a, nil

	case TasksLoadedMsg:
		if msg.WorkspaceID != a.svcs.WorkspaceID {
			return a, nil
		}
		a.sessions = msg.Sessions
		a.rebuildSidebar()
		a.refreshSessionSearchEntriesFromLocalState()
		for _, wi := range a.workItems {
			cmds = append(cmds, LoadPlanCmd(a.svcs.Plan, wi.ID))
		}
		for _, s := range msg.Sessions {
			if s.Status == domain.AgentSessionWaitingForAnswer {
				cmds = append(cmds, LoadQuestionsCmd(a.svcs.Question, s.ID))
			}
			if s.Status == domain.AgentSessionCompleted {
				cmds = append(cmds, LoadReviewsCmd(a.svcs.Review, s.ID))
			}
		}
		cmds = append(cmds, a.updateContentFromState())
		return a, tea.Batch(cmds...)

	case SessionHistorySearchRequestedMsg:
		cmds = append(cmds, a.runSessionSearch(true))
		return a, tea.Batch(cmds...)

	case SessionHistoryLoadedMsg:
		if a.activeOverlay != overlaySessionSearch || !sameSessionHistoryFilter(a.sessionSearchFilter(), msg.Filter) {
			return a, nil
		}
		a.sessionSearch.SetLoading(false)
		a.sessionSearch.SetEntries(mergeSessionHistoryEntries(a.localSessionSearchEntries(msg.Filter), msg.Entries))
		return a, nil

	case OpenSessionHistoryMsg:
		a.activeOverlay = overlayNone
		a.sessionSearch.Close()
		cmds = append(cmds, a.loadHistoryEntry(a.sidebarEntryFromHistory(msg.Entry)))
		return a, tea.Batch(cmds...)

	case SessionInteractionLoadedMsg:
		if msg.SessionID != a.currentHistorySessionID || a.currentHistoryEntry.SessionID != msg.SessionID {
			return a, nil
		}
		a.content.SetSessionInteraction(a.historyEntryTitle(a.currentHistoryEntry), a.historyEntryMeta(a.currentHistoryEntry), msg.Entries)
		return a, nil

	case PlanLoadedMsg:
		a.plans[msg.WorkItemID] = msg.Plan
		if msg.Plan != nil {
			a.subPlans[msg.Plan.ID] = msg.SubPlans
		}
		a.rebuildSidebar()
		a.refreshSessionSearchEntriesFromLocalState()
		if a.currentWorkItemID == msg.WorkItemID {
			cmds = append(cmds, a.updateContentFromState())
		}
		return a, tea.Batch(cmds...)
	case QuestionsLoadedMsg:
		a.questions[msg.SessionID] = msg.Questions
		a.rebuildSidebar()
		a.refreshSessionSearchEntriesFromLocalState()
		if a.currentWorkItemID != "" {
			cmds = append(cmds, a.updateContentFromState())
		}
		return a, tea.Batch(cmds...)

	case ReviewsLoadedMsg:
		a.reviews[msg.SessionID] = msg
		if a.currentWorkItemID != "" {
			cmds = append(cmds, a.updateContentFromState())
		}
		return a, tea.Batch(cmds...)

	case PlanEditedMsg:
		cmds = append(cmds, SaveReviewedPlanCmd(a.svcs.Planning, msg.PlanID, msg.NewContent))
		return a, tea.Batch(cmds...)

	case PlanSavedMsg:
		plan := msg.Plan
		a.plans[msg.WorkItemID] = &plan
		a.subPlans[msg.Plan.ID] = msg.SubPlans
		a.rebuildSidebar()
		a.refreshSessionSearchEntriesFromLocalState()
		a.toasts.AddToast(msg.Message, components.ToastSuccess)
		if a.currentWorkItemID == msg.WorkItemID {
			cmds = append(cmds, a.updateContentFromState())
		}
		return a, tea.Batch(cmds...)

	case LiveInstancesLoadedMsg:
		a.liveInstanceIDs = msg.AliveIDs
		return a, nil

	case ConfirmAbandonMsg:
		sID := msg.SessionID
		a.showConfirm("Abandon Session",
			"This will mark the session as failed. Continue?",
			func() tea.Msg { return AbandonSessionMsg{SessionID: sID} },
		)
		return a, nil

	case ConfirmDeleteSessionMsg:
		a.showDeleteSessionConfirm(msg.SessionID)
		return a, nil

	case ConfirmOverrideAcceptMsg:
		a.showConfirm("Override Accept",
			"Accept this work item despite outstanding critiques? This cannot be undone.",
			func() tea.Msg { return OverrideAcceptMsg(msg) },
		)
		return a, nil

	case StartPlanMsg:
		if a.svcs.Planning != nil {
			cmds = append(cmds, StartPlanningCmd(a.registerPipelineCancel(msg.WorkItemID), a.svcs.Planning, msg.WorkItemID))
		} else {
			a.toasts.AddToast("Planning service not configured", components.ToastError)
		}
		return a, tea.Batch(cmds...)

	case RestartPlanMsg:
		if a.svcs.Planning != nil && a.svcs.Session != nil {
			cmds = append(cmds, RestartPlanningCmd(a.registerPipelineCancel(msg.WorkItemID), a.svcs.Session, a.svcs.Planning, a.svcs.Task, msg.WorkItemID))
		} else {
			a.toasts.AddToast("Planning service not configured", components.ToastError)
		}
		return a, tea.Batch(cmds...)

	case PlanApproveMsg:
		cmds = append(cmds, ApprovePlanCmd(a.svcs.Session, a.svcs.Plan, a.svcs.Bus, msg.PlanID, msg.WorkItemID))
		return a, tea.Batch(cmds...)

	case PlanApprovedMsg:
		if a.svcs.Implementation != nil {
			cmds = append(cmds, RunImplementationCmd(a.registerPipelineCancel(msg.WorkItemID), a.svcs.Implementation, msg.PlanID))
		}
		if a.svcs.Foreman != nil {
			a.foremanPlanID = msg.PlanID
			cmds = append(cmds, StartForemanCmd(a.svcs.Foreman, msg.PlanID))
		}
		return a, tea.Batch(cmds...)

	case PlanRequestChangesMsg:
		if a.svcs.Planning != nil {
			cmds = append(cmds, PlanWithFeedbackCmd(a.registerPipelineCancel(a.currentWorkItemID), a.svcs.Planning, a.currentWorkItemID, msg.PlanID, msg.Feedback))
		} else {
			a.toasts.AddToast("Plan revision requested (no planning service)", components.ToastInfo)
		}
		return a, tea.Batch(cmds...)

	case PlanRejectMsg:
		cmds = append(cmds, RejectPlanCmd(a.svcs.Session, a.svcs.Plan, msg.WorkItemID, msg.PlanID, msg.Reason))
		return a, tea.Batch(cmds...)

	case AnswerQuestionMsg:
		cmds = append(cmds, AnswerQuestionCmd(a.svcs.Question, a.svcs.Foreman, msg.QuestionID, msg.Answer, msg.AnsweredBy))
		return a, tea.Batch(cmds...)

	case SendToForemanMsg:
		if a.svcs.Foreman != nil {
			cmds = append(cmds, SendToForemanCmd(a.svcs.Foreman, msg.QuestionID, msg.Message))
		} else {
			a.toasts.AddToast("Foreman not configured", components.ToastError)
		}
		return a, tea.Batch(cmds...)

	case SteerSessionMsg:
		if a.svcs.SessionRegistry != nil && msg.SessionID != "" && msg.Message != "" {
			cmds = append(cmds, SteerSessionCmd(a.svcs.SessionRegistry, msg.SessionID, msg.Message))
		}
		return a, tea.Batch(cmds...)

	case SteerSessionSentMsg:
		a.toasts.AddToast("Steering prompt sent", components.ToastSuccess)
		return a, nil

	case FollowUpSessionMsg:
		if a.svcs.Resumption != nil && a.svcs.Task != nil && msg.TaskID != "" && msg.Feedback != "" {
			ctx := a.pipelineCtxForTask(msg.TaskID)
			cmds = append(cmds, FollowUpSessionCmd(ctx, a.svcs.Resumption, a.svcs.Task, msg.TaskID, msg.Feedback, a.svcs.InstanceID))
		}
		return a, tea.Batch(cmds...)

	case FollowUpSessionSentMsg:
		a.toasts.AddToast("Follow-up session started", components.ToastSuccess)
		return a, nil

	case FollowUpFailedSessionMsg:
		if a.svcs.Resumption != nil && a.svcs.Task != nil && msg.TaskID != "" && msg.Feedback != "" {
			ctx := a.pipelineCtxForTask(msg.TaskID)
			cmds = append(cmds, FollowUpFailedSessionCmd(ctx, a.svcs.Resumption, a.svcs.Task, msg.TaskID, msg.Feedback, a.svcs.InstanceID))
		}
		return a, tea.Batch(cmds...)

	case FollowUpFailedSessionSentMsg:
		a.toasts.AddToast("Follow-up session started for failed task", components.ToastSuccess)
		return a, nil

	case FollowUpPlanMsg:
		if a.svcs.Planning == nil {
			return a, nil
		}
		return a, FollowUpPlanCmd(a.registerPipelineCancel(msg.WorkItemID), a.svcs.Planning, msg.WorkItemID, msg.Feedback)

	case FollowUpPlanResultMsg:
		if msg.Err != nil {
			a.toasts.AddToast(fmt.Sprintf("Follow-up planning failed: %v", msg.Err), components.ToastError)
			return a, nil
		}
		a.toasts.AddToast("Follow-up planning started", components.ToastSuccess)
		return a, nil

	case SkipQuestionMsg:
		cmds = append(cmds, SkipQuestionCmd(a.svcs.Question, a.svcs.Foreman, msg.QuestionID))
		return a, tea.Batch(cmds...)

	case ResumeSessionMsg:
		if a.svcs.Resumption != nil {
			ctx := a.pipelineCtxForTask(msg.OldSessionID)
			cmds = append(cmds, ResumeSessionCmd(ctx, a.svcs.Resumption, a.svcs.Task, msg.OldSessionID, a.svcs.InstanceID))
		} else {
			a.toasts.AddToast("Resume not available (no resumption service)", components.ToastError)
		}
		return a, tea.Batch(cmds...)

	case AbandonSessionMsg:
		cmds = append(cmds, abandonSessionCmd(a.svcs.Task, msg.SessionID))
		return a, tea.Batch(cmds...)

	case DeleteSessionMsg:
		// Cancel the orchestrator goroutine (implementation/planning) for this
		// work item. This cascades through executeWave → executeSubPlan → Wait
		// → Abort on every running agent session owned by the pipeline.
		a.cancelPipeline(msg.SessionID)

		// Stop the Foreman if it is running for this work item's plan.
		if a.svcs.Foreman != nil {
			if plan := a.plans[msg.SessionID]; plan != nil && plan.ID == a.foremanPlanID {
				cmds = append(cmds, StopForemanCmd(a.svcs.Foreman))
				a.foremanPlanID = ""
			}
		}

		// Abort any remaining running sessions via the registry. This covers
		// resumed and follow-up sessions that use fire-and-forget goroutines
		// without a stored cancel handle. AbortAndDeregister is idempotent —
		// sessions already torn down by the context cancel above are a no-op.
		if a.svcs.SessionRegistry != nil {
			for _, task := range a.sessions {
				if task.WorkItemID == msg.SessionID {
					a.svcs.SessionRegistry.AbortAndDeregister(context.Background(), task.ID)
				}
			}
		}

		cmds = append(cmds, deleteSessionCmd(a.svcs, a.sessionsDir, msg.SessionID, a.reviewSessionLogs))
		return a, tea.Batch(cmds...)

	case ReimplementMsg:
		if a.svcs.Implementation != nil {
			if plan := a.plans[msg.WorkItemID]; plan != nil {
				cmds = append(cmds, RunImplementationCmd(a.registerPipelineCancel(msg.WorkItemID), a.svcs.Implementation, plan.ID))
				if a.svcs.Foreman != nil {
					a.foremanPlanID = plan.ID
					cmds = append(cmds, StartForemanCmd(a.svcs.Foreman, plan.ID))
				}
			} else {
				a.toasts.AddToast("Plan not found for re-implementation", components.ToastError)
			}
		} else {
			a.toasts.AddToast("Implementation service not configured", components.ToastError)
		}
		return a, tea.Batch(cmds...)

	case RetryFailedMsg:
		a.toasts.AddToast("Retrying failed repos...", components.ToastInfo)
		if a.svcs.Implementation != nil {
			if plan := a.plans[msg.WorkItemID]; plan != nil {
				cmds = append(cmds, RetryFailedCmd(a.registerPipelineCancel(msg.WorkItemID), a.svcs.Session, a.svcs.Implementation, plan.ID, msg.WorkItemID))
				if a.svcs.Foreman != nil {
					a.foremanPlanID = plan.ID
					cmds = append(cmds, StartForemanCmd(a.svcs.Foreman, plan.ID))
				}
			} else {
				a.toasts.AddToast("Plan not found for retry", components.ToastError)
			}
		} else {
			a.toasts.AddToast("Implementation service not configured", components.ToastError)
		}
		return a, tea.Batch(cmds...)

	case OverrideAcceptMsg:
		cmds = append(cmds, OverrideAcceptCmd(a.svcs.Session, a.svcs.Plan, a.svcs.Task, a.svcs.Bus, msg.WorkItemID))
		return a, tea.Batch(cmds...)

	case NewSessionManualMsg:
		a.activeOverlay = overlayNone
		a.newSession.Close()
		cmds = append(cmds, createManualSessionCmd(a.svcs, msg))
		return a, tea.Batch(cmds...)

	case NewSessionBrowseMsg:
		a.activeOverlay = overlayNone
		a.newSession.Close()
		cmds = append(cmds, createBrowseSessionCmd(a.svcs, msg))
		return a, tea.Batch(cmds...)

	case SettingsSavedMsg:
		a.toasts.AddToast(msg.Message, components.ToastSuccess)
		if a.activeOverlay == overlaySettings {
			a.settingsPage, cmd = a.settingsPage.Update(msg, a.svcs)
			cmds = append(cmds, cmd)
		}
		return a, tea.Batch(cmds...)
	case SettingsAppliedMsg:
		a.applyServicesReload(msg.Reload)
		a.toasts.AddToast(msg.Message, components.ToastSuccess)
		if a.activeOverlay == overlaySettings {
			a.settingsPage, cmd = a.settingsPage.Update(msg, a.svcs)
			cmds = append(cmds, cmd)
		}
		return a, tea.Batch(cmds...)
	case SettingsProviderTestedMsg:
		if a.activeOverlay == overlaySettings {
			a.settingsPage, cmd = a.settingsPage.Update(msg, a.svcs)
			cmds = append(cmds, cmd)
		}
		a.toasts.AddToast(msg.Provider+" connection verified", components.ToastSuccess)
		return a, tea.Batch(cmds...)
	case SettingsLoginCompletedMsg:
		if a.activeOverlay == overlaySettings {
			a.settingsPage, cmd = a.settingsPage.Update(msg, a.svcs)
			cmds = append(cmds, cmd)
		}
		a.toasts.AddToast(msg.Message, components.ToastSuccess)
		return a, tea.Batch(cmds...)

	case ForemanReplyMsg:
		// Find the question in the session-keyed map and refresh the model.
	questionLoop:
		for _, qs := range a.questions {
			for _, q := range qs {
				if q.ID == msg.QuestionID {
					a.content.UpdateQuestionProposal(q, msg.NewProposal, msg.Uncertain)
					break questionLoop
				}
			}
		}
		return a, nil

	case SessionCreatedMsg:
		a.toasts.AddToast(msg.Message, components.ToastSuccess)
		if a.svcs.Planning != nil {
			msg.Session.State = domain.SessionPlanning
		}
		a.upsertWorkItem(msg.Session)
		cmds = append(cmds, a.focusWorkItemOverview(msg.Session.ID))
		if a.svcs.Planning != nil {
			cmds = append(cmds, StartPlanningCmd(a.registerPipelineCancel(msg.Session.ID), a.svcs.Planning, msg.Session.ID))
		} else {
			a.toasts.AddToast("Planning service not configured", components.ToastError)
		}
		return a, tea.Batch(cmds...)

	case SessionDuplicatePromptMsg:
		a.showDuplicateSessionDialog(msg.RequestedSession, msg.ExistingSession)
		return a, nil
	case SessionDuplicateActionMsg:
		if !a.duplicateSessionActive {
			return a, nil
		}
		existing := a.duplicateSession.ExistingSession
		label := workItemDisplayLabel(existing)
		a.closeDuplicateSessionDialog()
		switch msg.Action {
		case SessionDuplicateCancel:
			return a, nil
		case SessionDuplicateOpenExisting:
			a.toasts.AddToast("Opened existing item "+label, components.ToastInfo)
			a.upsertWorkItem(existing)
			return a, a.focusWorkItemOverview(existing.ID)
		case SessionDuplicateCreateSession:
			a.toasts.AddToast("Starting planning with existing item "+label, components.ToastInfo)
			if a.svcs.Planning != nil && existing.State == domain.SessionIngested {
				existing.State = domain.SessionPlanning
			}
			a.upsertWorkItem(existing)
			cmds = append(cmds, a.focusWorkItemOverview(existing.ID))
			if a.svcs.Planning != nil {
				cmds = append(cmds, StartPlanningCmd(a.registerPipelineCancel(existing.ID), a.svcs.Planning, existing.ID))
			} else {
				a.toasts.AddToast("Planning service not configured", components.ToastError)
			}
			return a, tea.Batch(cmds...)
		default:
			return a, nil
		}

	case SessionResumedMsg:
		a.toasts.AddToast(msg.Message, components.ToastSuccess)
		if a.currentWorkItemID != "" {
			cmds = append(cmds, a.updateContentFromState())
		}
		return a, tea.Batch(cmds...)

	case PlanningRestartedMsg:
		a.toasts.AddToast(msg.Message, components.ToastSuccess)
		if a.currentWorkItemID != "" {
			cmds = append(cmds, a.updateContentFromState())
		}
		return a, tea.Batch(cmds...)
	case ActionDoneMsg:
		a.toasts.AddToast(msg.Message, components.ToastSuccess)
		return a, nil

	case OpenExternalURLMsg:
		return a, OpenBrowserCmd(msg.URL)

	case OpenSourceItemsOverlayMsg:
		a.activeOverlay = overlaySourceItems
		a.sourceItemsOverlay.Open(msg.Items)
		return a, nil

	case openSourceItemURLsMsg:
		a.activeOverlay = overlayNone
		a.sourceItemsOverlay.Close()
		var browserCmds []tea.Cmd
		for _, url := range msg.URLs {
			browserCmds = append(browserCmds, OpenBrowserCmd(url))
		}
		return a, tea.Batch(browserCmds...)

	case ImplementationCompleteMsg:
		a.cancelPipeline(msg.WorkItemID)
		a.toasts.AddToast("Implementation complete", components.ToastSuccess)
		if a.currentWorkItemID != "" {
			cmds = append(cmds, a.updateContentFromState())
		}
		if a.svcs.Foreman != nil {
			cmds = append(cmds, StopForemanCmd(a.svcs.Foreman))
		}
		return a, tea.Batch(cmds...)

	case SessionDeletedMsg:
		if plan := a.plans[msg.SessionID]; plan != nil {
			delete(a.subPlans, plan.ID)
		}
		delete(a.plans, msg.SessionID)
		delete(a.taskSessionSelectionByWorkItem, msg.SessionID)

		taskIDSet := make(map[string]struct{}, len(msg.TaskIDs))
		for _, taskID := range msg.TaskIDs {
			taskIDSet[taskID] = struct{}{}
			delete(a.questions, taskID)
			delete(a.reviews, taskID)
			delete(a.reviewSessionLogs, taskID)
			delete(a.tailingSessionIDs, taskID)
			delete(a.tailingSessionIDs, "review-"+taskID)
		}

		filteredTasks := a.sessions[:0]
		for _, task := range a.sessions {
			if _, ok := taskIDSet[task.ID]; ok {
				continue
			}
			filteredTasks = append(filteredTasks, task)
		}
		a.sessions = filteredTasks

		filteredSessions := a.workItems[:0]
		for _, session := range a.workItems {
			if session.ID == msg.SessionID {
				continue
			}
			filteredSessions = append(filteredSessions, session)
		}
		a.workItems = filteredSessions

		deletedCurrentSelection := a.currentWorkItemID == msg.SessionID || a.currentHistoryEntry.WorkItemID == msg.SessionID
		if deletedCurrentSelection {
			a.currentWorkItemID = ""
			a.currentHistoryEntry = SidebarEntry{}
			a.currentHistorySessionID = ""
			a.sidebarMode = sidebarPaneSessions
			a.content.SetMode(ContentModeEmpty)
		}

		a.rebuildSidebar()
		a.toasts.AddToast(msg.Message, components.ToastSuccess)
		if warning := strings.TrimSpace(msg.Warning); warning != "" {
			a.toasts.AddToast(warning, components.ToastWarning)
		}
		if a.svcs.WorkspaceID != "" {
			cmds = append(cmds,
				LoadSessionsCmd(a.svcs.Session, a.svcs.WorkspaceID),
				LoadTasksCmd(a.svcs.Task, a.svcs.WorkspaceID),
			)
		}
		if a.currentWorkItemID != "" {
			cmds = append(cmds, a.updateContentFromState())
		}
		if a.activeOverlay == overlaySessionSearch {
			cmds = append(cmds, a.runSessionSearch(false))
		}
		return a, tea.Batch(cmds...)

	case ErrMsg:
		// Silently drop context cancellations — these fire when a pipeline is
		// intentionally torn down on session delete or quit, not from real errors.
		if errors.Is(msg.Err, context.Canceled) || errors.Is(msg.Err, context.DeadlineExceeded) {
			return a, nil
		}
		a.toasts.AddToast("Error: "+msg.Err.Error(), components.ToastError)
		return a, nil

	case AdapterErrorMsg:
		toastMsg := formatAdapterErrorToast(msg)
		a.toasts.AddToast(toastMsg, components.ToastWarning)
		return a, WaitForAdapterErrorCmd(a.svcs.AdapterErrors)

	case LogToastMsg:
		level := components.ToastWarning
		if msg.Level == "ERROR" {
			level = components.ToastError
		}
		a.toasts.AddToast(msg.Message, level)
		return a, WaitForLogToastCmd(a.svcs.LogToasts)

	case StartupWarningsMsg:
		for _, warning := range msg.Warnings {
			a.toasts.AddToast(warning, components.ToastWarning)
		}
		return a, nil

	case tea.KeyMsg:
		return a.handleKeyMsg(msg)
	}

	// Route to active overlay or content.
	if a.activeOverlay == overlayWorkspaceInit { //nolint:staticcheck // simple condition, switch not warranted
		a.workspaceModal, cmd = a.workspaceModal.Update(msg)
		cmds = append(cmds, cmd)
	} else if a.activeOverlay == overlayNewSession {
		a.newSession, cmd = a.newSession.Update(msg)
		cmds = append(cmds, cmd)
	} else if a.activeOverlay == overlaySessionSearch {
		a.sessionSearch, cmd = a.sessionSearch.Update(msg)
		cmds = append(cmds, cmd)
	} else if a.activeOverlay == overlaySettings {
		a.settingsPage, cmd = a.settingsPage.Update(msg, a.svcs)
		cmds = append(cmds, cmd)
	} else if a.activeOverlay == overlaySourceItems {
		a.sourceItemsOverlay, cmd = a.sourceItemsOverlay.Update(msg)
		cmds = append(cmds, cmd)
	} else {
		a.content, cmd = a.content.Update(msg)
		cmds = append(cmds, cmd)
	}

	return a, tea.Batch(cmds...)
}

func (a App) handleKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	// Confirm dialog captures all key input when active.
	if a.confirmActive {
		switch msg.String() {
		case "y", "enter", "ctrl+c":
			// ctrl+c while a confirm is open acts as "yes": if this is the quit
			// confirm, ctrl+c immediately confirms; for other confirms it prevents
			// the user from getting stuck needing two ctrl+c presses to exit.
			onYes := a.confirm.OnYes
			a.confirm = components.ConfirmDialog{}
			a.confirmActive = false
			return a, onYes
		default:
			a.confirm = components.ConfirmDialog{}
			a.confirmActive = false
			return a, nil
		}
	}

	if a.duplicateSessionActive {
		return a.handleDuplicateSessionKey(msg)
	}

	if msg.String() == "ctrl+c" {
		return a.handleQuitRequest()
	}

	if a.activeOverlay == overlayWorkspaceInit {
		a.workspaceModal, cmd = a.workspaceModal.Update(msg)
		return a, cmd
	}
	if a.activeOverlay == overlayNewSession {
		a.newSession, cmd = a.newSession.Update(msg)
		return a, cmd
	}
	if a.activeOverlay == overlaySessionSearch {
		a.sessionSearch, cmd = a.sessionSearch.Update(msg)
		return a, cmd
	}
	if a.activeOverlay == overlaySettings {
		a.settingsPage, cmd = a.settingsPage.Update(msg, a.svcs)
		return a, cmd
	}
	if a.activeOverlay == overlayHelp {
		a.activeOverlay = overlayNone
		return a, nil
	}
	if a.activeOverlay == overlayLogs {
		a.logsOverlay, cmd = a.logsOverlay.Update(msg)
		return a, cmd
	}
	if a.activeOverlay == overlaySourceItems {
		a.sourceItemsOverlay, cmd = a.sourceItemsOverlay.Update(msg)
		return a, cmd
	}
	previousFocus := a.mainFocus
	wasOverviewOverlayOpen := a.overviewOverlayOpen()
	if wasOverviewOverlayOpen {
		return a.updateContentForKey(msg, wasOverviewOverlayOpen, previousFocus)
	}
	// When a sub-model is capturing text input (e.g. steering prompt),
	// bypass global shortcuts and route directly to content.
	if a.content.InputCaptured() {
		return a.updateContentForKey(msg, wasOverviewOverlayOpen, previousFocus)
	}
	if msg.String() == "enter" && a.sidebarMode == sidebarPaneTasks && a.selectedTaskSessionID() != "" {
		if a.sourceDetailsNoticeForWorkItem(a.workItemByID(a.currentWorkItemID)) != nil {
			return a, a.jumpFromSourceDetailsToOverview()
		}
	}

	switch msg.String() {
	case "q":
		return a.handleQuitRequest()
	case "n":
		return a, a.openNewSession()
	case "s":
		a.activeOverlay = overlaySettings
		a.settingsPage.Open()
		return a, nil
	case "/":
		return a, a.openSessionSearch()
	case "d":
		if sessionID := a.deletableSessionID(); sessionID != "" {
			a.showDeleteSessionConfirm(sessionID)
			return a, nil
		}
	case "esc", "left":
		if a.mainFocus == mainFocusContent {
			a.mainFocus = mainFocusSidebar
			return a, nil
		}
		if a.sidebarMode == sidebarPaneTasks {
			return a, a.exitTaskSidebar()
		}
	case "right":
		if a.mainFocus == mainFocusContent {
			break
		}
		if a.sidebarMode == sidebarPaneSessions {
			return a, a.enterTaskSidebar()
		}
		a.mainFocus = mainFocusContent
		return a, nil
	case "up", "k":
		if a.mainFocus == mainFocusContent {
			return a.updateContentForKey(msg, wasOverviewOverlayOpen, previousFocus)
		}
		a.sidebar.MoveUp()
		cmd = a.onSidebarMove()
		return a, cmd
	case "down", "j":
		if a.mainFocus == mainFocusContent {
			return a.updateContentForKey(msg, wasOverviewOverlayOpen, previousFocus)
		}
		a.sidebar.MoveDown()
		cmd = a.onSidebarMove()
		return a, cmd
	case "g":
		if a.mainFocus == mainFocusContent {
			return a.updateContentForKey(msg, wasOverviewOverlayOpen, previousFocus)
		}
		a.sidebar.GotoTop()
		cmd = a.onSidebarMove()
		return a, cmd
	case "G":
		if a.mainFocus == mainFocusContent {
			return a.updateContentForKey(msg, wasOverviewOverlayOpen, previousFocus)
		}
		a.sidebar.GotoBottom()
		cmd = a.onSidebarMove()
		return a, cmd
	case "?":
		a.activeOverlay = overlayHelp
		return a, nil
	case "L":
		a.logsOverlay.SetSize(a.windowWidth, a.windowHeight)
		a.logsOverlay.Open()
		a.activeOverlay = overlayLogs
		return a, nil
	}

	return a.updateContentForKey(msg, wasOverviewOverlayOpen, previousFocus)
}

func (a *App) onSidebarMove() tea.Cmd {
	sel := a.sidebar.Selected()
	if sel == nil {
		a.content.SetMode(ContentModeEmpty)
		a.currentWorkItemID = ""
		a.currentHistorySessionID = ""
		a.currentHistoryEntry = SidebarEntry{}
		return nil
	}
	if a.sidebarMode == sidebarPaneTasks {
		if sel.WorkItemID == a.currentWorkItemID && sel.SessionID == a.selectedTaskSessionID() && a.currentHistorySessionID == "" {
			return nil
		}
		a.tailingSessionIDs = make(map[string]bool)
		a.currentHistorySessionID = ""
		a.currentHistoryEntry = SidebarEntry{}
		a.currentWorkItemID = sel.WorkItemID
		a.setSelectedTaskSessionID(sel.SessionID)
		return a.updateContentFromState()
	}
	if sel.WorkItemID == a.currentWorkItemID && a.currentHistorySessionID == "" {
		return nil
	}
	a.tailingSessionIDs = make(map[string]bool)
	a.currentHistorySessionID = ""
	a.currentHistoryEntry = SidebarEntry{}
	a.currentWorkItemID = sel.WorkItemID
	return a.updateContentFromState()
}

func (a *App) updateContentFromState() tea.Cmd {
	prevMode := a.content.mode
	if a.currentWorkItemID == "" {
		a.content.SetMode(ContentModeEmpty)
		return nil
	}

	wi := a.workItemByID(a.currentWorkItemID)
	if wi == nil {
		a.content.SetMode(ContentModeEmpty)
		return nil
	}

	a.content.SetWorkItem(wi)
	a.content.sourceDetails.SetNotice(nil)
	a.content.sessionLog.SetNotice(nil)
	if a.sidebarMode == sidebarPaneTasks {
		if taskSessionID := a.selectedTaskSessionID(); taskSessionID != "" {
			if taskSessionID == taskSidebarSourceDetailsID {
				a.content.sourceDetails.SetNotice(a.sourceDetailsNoticeForWorkItem(wi))
				a.content.SetMode(ContentModeSourceDetails)
				if prevMode != a.content.mode && (prevMode == ContentModePlanning || prevMode == ContentModeSessionInteraction) {
					a.tailingSessionIDs = make(map[string]bool)
				}
				return nil
			}
			if session := a.workItemTaskSession(a.currentWorkItemID, taskSessionID); session != nil {
				return a.showTaskContent(wi, session)
			}
			a.setSelectedTaskSessionID("")
		}
	}

	a.content.SetMode(ContentModeOverview)
	a.content.SetOverviewData(a.buildOverviewData(wi))
	if prevMode != a.content.mode && (prevMode == ContentModePlanning || prevMode == ContentModeSessionInteraction) {
		a.tailingSessionIDs = make(map[string]bool)
	}
	return nil
}

func (a *App) jumpFromSourceDetailsToOverview() tea.Cmd {
	if a.currentWorkItemID == "" {
		return nil
	}
	a.tailingSessionIDs = make(map[string]bool)
	a.currentHistorySessionID = ""
	a.currentHistoryEntry = SidebarEntry{}
	a.setSelectedTaskSessionID("")
	a.sidebar.SelectEntry(a.currentWorkItemID, "")
	a.mainFocus = mainFocusContent
	return a.updateContentFromState()
}

func (a *App) sourceDetailsNoticeForWorkItem(wi *domain.Session) *sourceDetailsNotice {
	if wi == nil {
		return nil
	}
	var plan *domain.Plan
	var subPlans []domain.TaskPlan
	if currentPlan := a.plans[wi.ID]; currentPlan != nil {
		plan = currentPlan
		subPlans = a.subPlans[currentPlan.ID]
	}
	if actions := a.buildOverviewActions(wi, plan, subPlans); len(actions) > 0 {
		return sourceDetailsNoticeFromOverviewAction(actions[0])
	}
	switch wi.State {
	case domain.SessionReviewing:
		return &sourceDetailsNotice{
			Title:   "Review in progress",
			Body:    "This work item moved into review while you were focused on a task view.",
			Hint:    "Press [Enter] to open the overview and inspect the current review status.",
			Variant: components.CalloutCard,
		}
	case domain.SessionCompleted:
		return &sourceDetailsNotice{
			Title:   "Work item completed",
			Body:    "This work item completed while you were focused on a task view.",
			Hint:    "Press [Enter] to open the overview and inspect the final status or review artifacts.",
			Variant: components.CalloutCard,
		}
	default:
		return nil
	}
}

func sourceDetailsNoticeFromOverviewAction(action OverviewActionCard) *sourceDetailsNotice {
	notice := &sourceDetailsNotice{
		Title:   firstNonEmptyString(strings.TrimSpace(action.Title), "Attention required"),
		Hint:    "Press [Enter] to open the overview.",
		Variant: components.CalloutWarning,
	}
	switch action.Kind {
	case overviewActionPlanReview:
		notice.Body = "Implementation is paused until the plan is approved, revised, or rejected."
		if len(action.Affected) > 0 {
			notice.Body = fmt.Sprintf("%s Affected repos: %d.", notice.Body, len(action.Affected))
		}
	case overviewActionQuestion:
		target := firstNonEmptyString(strings.TrimSpace(action.QuestionRepo), firstSourceDetailsAffected(action.Affected), "A repo task")
		notice.Body = target + " is paused until someone answers the escalated question."
		if question := strings.TrimSpace(action.Blocked); question != "" {
			notice.Body += " Question: " + question
		}
	case overviewActionInterrupted:
		target := firstNonEmptyString(strings.TrimSpace(action.Blocked), firstSourceDetailsAffected(action.Affected), "A repo task")
		notice.Body = target + " was interrupted and cannot continue until it is resumed or abandoned."
	case overviewActionReviewing:
		if len(action.Affected) > 0 {
			notice.Body = fmt.Sprintf("Review critiques are waiting for a human decision in %s.", strings.Join(action.Affected, ", "))
		} else {
			notice.Body = "Review critiques are waiting for a human decision."
		}
		notice.Hint = "Press [Enter] to open the overview and inspect the review."
	default:
		notice.Body = firstNonEmptyString(strings.TrimSpace(action.Why), strings.TrimSpace(action.Blocked))
	}
	return notice
}

func firstSourceDetailsAffected(affected []string) string {
	if len(affected) == 0 {
		return ""
	}
	return strings.TrimSpace(affected[0])
}

func (a *App) showTaskContent(wi *domain.Session, session *domain.Task) tea.Cmd {
	title := firstNonEmptyString(wi.ExternalID, wi.ID) + " · " + taskSidebarSessionTitle(session)
	metaParts := []string{sessionStatusLabel(session.Status)}
	if session.HarnessName != "" {
		metaParts = append(metaParts, session.HarnessName)
	}
	metaParts = append(metaParts, taskSessionDisplayName(session))
	a.content.sessionLog.SetNotice(a.sourceDetailsNoticeForWorkItem(wi))
	if session.Phase == domain.TaskPhasePlanning {
		a.content.SetMode(ContentModePlanning)
	} else {
		a.content.SetMode(ContentModeSessionInteraction)
	}
	a.content.sessionLog.SetTitle(title)
	a.content.sessionLog.SetModeLabel(taskSessionModeLabel(session))
	a.content.sessionLog.SetMeta(strings.Join(metaParts, " · "))
	logPath := filepath.Join(a.sessionsDir, session.ID+".log")
	resumeOffset := int64(0)
	if a.content.sessionLog.live && a.content.sessionLog.sessionID == session.ID && a.content.sessionLog.logPath == logPath {
		resumeOffset = a.content.sessionLog.offset
	}
	a.content.sessionLog.SetLogPath(session.ID, logPath)
	if session.Status == domain.AgentSessionFailed {
		a.content.sessionLog.ClearCompletedSession()
		a.content.sessionLog.SetFailedSession(session.ID)
	} else if session.Status == domain.AgentSessionCompleted {
		a.content.sessionLog.ClearFailedSession()
		a.content.sessionLog.SetCompletedSession(session.ID)
	} else {
		a.content.sessionLog.ClearFailedSession()
		a.content.sessionLog.ClearCompletedSession()
	}
	agentActive := session.Status == domain.AgentSessionPending ||
		session.Status == domain.AgentSessionRunning ||
		session.Status == domain.AgentSessionWaitingForAnswer
	spinnerCmd := a.content.sessionLog.SetAgentActive(agentActive)
	if !a.tailingSessionIDs[session.ID] {
		a.tailingSessionIDs[session.ID] = true
		return tea.Batch(spinnerCmd, TailSessionLogCmd(logPath, session.ID, resumeOffset))
	}
	return spinnerCmd
}

func (a *App) canActOnSession(s domain.Task) bool {
	if a.svcs.InstanceID == "" || s.OwnerInstanceID == nil {
		return true
	}
	if *s.OwnerInstanceID == a.svcs.InstanceID {
		return true
	}
	if len(a.liveInstanceIDs) == 0 {
		return false
	}
	return !a.liveInstanceIDs[*s.OwnerInstanceID]
}

func (a *App) showConfirm(title, message string, onYes tea.Cmd) {
	a.confirm = components.NewConfirmDialog(a.statusBar.styles, title, message, onYes)
	a.confirmActive = true
}

func (a *App) showDeleteSessionConfirm(sessionID string) {
	sID := sessionID
	var running int
	for _, task := range a.sessions {
		if task.WorkItemID == sID {
			switch task.Status {
			case domain.AgentSessionRunning, domain.AgentSessionWaitingForAnswer:
				running++
			}
		}
	}
	msg := "Delete this full session and all related data?"
	if running > 0 {
		sessionWord := "sessions"
		if running == 1 {
			sessionWord = "session"
		}
		msg = fmt.Sprintf("%d agent %s running and will be killed. %s", running, sessionWord, msg)
	}
	a.showConfirm("Delete Session", msg,
		func() tea.Msg { return DeleteSessionMsg{SessionID: sID} },
	)
}

// registerPipelineCancel creates a cancellable context for a work item's
// pipeline goroutine and stores the cancel function. If a previous pipeline
// context exists for this work item it is cancelled first.
func (a *App) registerPipelineCancel(workItemID string) context.Context {
	if cancel, ok := a.pipelineCancels[workItemID]; ok {
		cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.pipelineCancels[workItemID] = cancel
	return ctx
}

// cancelPipeline cancels the orchestrator goroutine for a work item and
// removes the cancel function from the map.
func (a *App) cancelPipeline(workItemID string) {
	if cancel, ok := a.pipelineCancels[workItemID]; ok {
		cancel()
		delete(a.pipelineCancels, workItemID)
	}
}

// pipelineCtxForTask looks up the WorkItemID for a task and returns a
// cancellable pipeline context for it. If the task is not found in the
// sessions list, returns context.Background() as a safe fallback.
func (a *App) pipelineCtxForTask(taskID string) context.Context {
	for _, s := range a.sessions {
		if s.ID == taskID {
			return a.registerPipelineCancel(s.WorkItemID)
		}
	}
	return context.Background()
}

// teardownAllPipelines cancels every active pipeline context, stops the
// Foreman, and aborts all sessions tracked by the registry. This is the
// shared teardown path for both quit and (potentially) batch-delete.
func (a *App) teardownAllPipelines() {
	for id, cancel := range a.pipelineCancels {
		cancel()
		delete(a.pipelineCancels, id)
	}

	if a.svcs.Foreman != nil && a.foremanPlanID != "" {
		_ = a.svcs.Foreman.Stop(context.Background())
		a.foremanPlanID = ""
	}

	if a.svcs.SessionRegistry != nil {
		for _, task := range a.sessions {
			switch task.Status {
			case domain.AgentSessionRunning, domain.AgentSessionWaitingForAnswer:
				a.svcs.SessionRegistry.AbortAndDeregister(context.Background(), task.ID)
			}
		}
	}
}

// handleQuitRequest checks for running agent sessions and shows a confirmation
// dialog before quitting. When no sessions are running it tears down pipelines
// and quits immediately. Otherwise a confirm dialog is shown; on acceptance
// QuitConfirmedMsg performs the teardown.
func (a App) handleQuitRequest() (tea.Model, tea.Cmd) {
	n := a.activeSessionCount()
	if n > 0 {
		sessionWord := "sessions are"
		if n == 1 {
			sessionWord = "session is"
		}
		a.showConfirm(
			"Quit",
			fmt.Sprintf("%d agent %s running and will be killed. Quit anyway?", n, sessionWord),
			func() tea.Msg { return QuitConfirmedMsg{} },
		)
		return a, nil
	}
	a.teardownAllPipelines()
	return a, a.quitCmd()
}

// quitCmd returns the command that exits the program, cleaning up the instance
// record when one exists.
func (a App) quitCmd() tea.Cmd {
	if a.svcs.InstanceID != "" {
		return tea.Batch(DeleteInstanceCmd(a.svcs.Instance, a.svcs.InstanceID), tea.Quit)
	}
	return tea.Quit
}

func (a App) sidebarEntryFromWorkItem(wi domain.Session) SidebarEntry {
	entry := SidebarEntry{
		Kind:         SidebarEntryWorkItem,
		WorkItemID:   wi.ID,
		ExternalID:   wi.ExternalID,
		Source:       wi.Source,
		Title:        wi.Title,
		State:        wi.State,
		LastActivity: wi.UpdatedAt,
	}
	if plan := a.plans[wi.ID]; plan != nil {
		sps := a.subPlans[plan.ID]
		entry.TotalSubPlans = len(sps)
		for _, sp := range sps {
			if sp.UpdatedAt.After(entry.LastActivity) {
				entry.LastActivity = sp.UpdatedAt
			}
			if sp.Status == domain.SubPlanCompleted {
				entry.DoneSubPlans++
			}
			for _, s := range a.sessions {
				if s.SubPlanID == sp.ID {
					if s.UpdatedAt.After(entry.LastActivity) {
						entry.LastActivity = s.UpdatedAt
					}
					if s.Status == domain.AgentSessionWaitingForAnswer {
						for _, q := range a.questions[s.ID] {
							if q.Status == domain.QuestionEscalated {
								entry.HasOpenQuestion = true
							}
						}
					}
					if s.Status == domain.AgentSessionInterrupted {
						entry.HasInterrupted = true
					}
				}
			}
		}
	}
	return entry
}

func (a App) sessionSidebarEntries() []SidebarEntry {
	entries := make([]SidebarEntry, 0, len(a.workItems))
	for _, wi := range a.workItems {
		entries = append(entries, a.sidebarEntryFromWorkItem(wi))
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if !entries[i].LastActivity.Equal(entries[j].LastActivity) {
			return entries[i].LastActivity.After(entries[j].LastActivity)
		}
		return entries[i].WorkItemID < entries[j].WorkItemID
	})
	return entries
}

func (a App) sessionsForWorkItem(workItemID string) []domain.Task {
	plan := a.plans[workItemID]
	subPlanOrder := make(map[string]int)
	if plan != nil {
		for i, sp := range a.subPlans[plan.ID] {
			subPlanOrder[sp.ID] = i
		}
	}
	sessions := make([]domain.Task, 0)
	for _, s := range a.sessions {
		if s.WorkItemID == workItemID {
			sessions = append(sessions, s)
		}
	}
	sort.SliceStable(sessions, func(i, j int) bool {
		rankI := taskSessionPhaseRank(sessions[i].Phase)
		rankJ := taskSessionPhaseRank(sessions[j].Phase)
		if rankI != rankJ {
			return rankI < rankJ
		}
		if rankI != taskSessionPhaseRank(domain.TaskPhasePlanning) {
			orderI, okI := subPlanOrder[sessions[i].SubPlanID]
			orderJ, okJ := subPlanOrder[sessions[j].SubPlanID]
			if okI && okJ && orderI != orderJ {
				return orderI < orderJ
			}
			if okI != okJ {
				return okI
			}
		}
		if !sessions[i].UpdatedAt.Equal(sessions[j].UpdatedAt) {
			return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
		}
		if !sessions[i].CreatedAt.Equal(sessions[j].CreatedAt) {
			return sessions[i].CreatedAt.After(sessions[j].CreatedAt)
		}
		return sessions[i].ID > sessions[j].ID
	})
	return sessions
}

func (a App) taskSidebarEntries(workItemID string) []SidebarEntry {
	wi := a.workItemByID(workItemID)
	if wi == nil {
		return nil
	}
	overview := a.sidebarEntryFromWorkItem(*wi)
	overview.Kind = SidebarEntryTaskOverview
	entries := []SidebarEntry{overview}
	if sessionHasSourceDetails(wi) {
		entries = append(entries, SidebarEntry{
			Kind:         SidebarEntryTaskSourceDetails,
			WorkItemID:   workItemID,
			SessionID:    taskSidebarSourceDetailsID,
			ExternalID:   wi.ExternalID,
			Title:        "Source details",
			SubtitleText: sessionSourceSidebarSubtitle(wi),
			LastActivity: wi.UpdatedAt,
		})
	}
	for _, session := range a.sessionsForWorkItem(workItemID) {
		entryRepo := session.RepositoryName
		switch session.Phase {
		case domain.TaskPhasePlanning:
			entryRepo = "Planning"
		case domain.TaskPhaseReview:
			entryRepo = firstNonEmptyString(entryRepo, "Review")
		}
		entries = append(entries, SidebarEntry{
			Kind:           SidebarEntryTaskSession,
			WorkItemID:     workItemID,
			SessionID:      session.ID,
			Title:          taskSidebarSessionTitle(&session),
			State:          wi.State,
			SessionStatus:  session.Status,
			RepositoryName: entryRepo,
			LastActivity:   session.UpdatedAt,
		})
	}
	return entries
}

func (a *App) rebuildSidebar() {
	if a.sidebarMode == sidebarPaneTasks && a.currentWorkItemID != "" && a.workItemByID(a.currentWorkItemID) != nil {
		wi := a.workItemByID(a.currentWorkItemID)
		a.sidebar.SetTitle(firstNonEmptyString(wi.Title, wi.ExternalID, wi.ID) + " \u00b7 Tasks")
		a.sidebar.SetEntries(a.taskSidebarEntries(a.currentWorkItemID))
		selectedSessionID := a.selectedTaskSessionID()
		if selectedSessionID == "" {
			selectedSessionID = a.defaultTaskSessionID(a.currentWorkItemID)
			a.setSelectedTaskSessionID(selectedSessionID)
		}
		if !a.sidebar.SelectEntry(a.currentWorkItemID, selectedSessionID) {
			a.setSelectedTaskSessionID("")
			if !a.sidebar.SelectEntry(a.currentWorkItemID, "") {
				a.sidebar.ClearSelection()
			}
		}
		return
	}
	a.sidebarMode = sidebarPaneSessions
	a.sidebar.SetTitle("Sessions")
	a.sidebar.SetEntries(a.sessionSidebarEntries())
	if a.currentWorkItemID == "" {
		a.sidebar.ClearSelection()
		return
	}
	if !a.sidebar.SelectWorkItem(a.currentWorkItemID) {
		a.sidebar.ClearSelection()
	}
}

// View renders the full terminal UI.
func (a App) View() string {
	if a.windowWidth == 0 {
		return "Initializing…"
	}

	if a.activeOverlay == overlayWorkspaceInit {
		return renderCentered(a.workspaceModal.View(), a.windowWidth, a.windowHeight)
	}

	if a.confirmActive {
		return renderOverlay(a.confirm.View(), a.windowWidth, a.windowHeight)
	}

	if a.duplicateSessionActive {
		return renderOverlay(a.duplicateSessionDialogView(), a.windowWidth, a.windowHeight)
	}

	layout := styles.ComputeMainPageLayout(a.windowWidth, a.windowHeight, SidebarWidth, a.statusBar.styles.Chrome)
	overlayActive := a.overviewOverlayOpen()

	sidebarContent := lipgloss.NewStyle().
		Width(layout.SidebarInnerWidth).
		Height(layout.PaneInnerHeight).
		Render(fitViewBox(a.sidebar.View(), layout.SidebarInnerWidth, layout.PaneInnerHeight))
	sidebarPane := components.RenderPane(a.statusBar.styles, components.PaneSpec{
		Content: sidebarContent,
		Width:   layout.SidebarPaneWidth,
		Height:  layout.BodyHeight,
		Focused: !overlayActive && a.mainFocus == mainFocusSidebar,
	})

	contentWidth := appContentBodyWidth(layout.ContentInnerWidth)
	contentStyle := lipgloss.NewStyle().
		Width(layout.ContentInnerWidth).
		Height(layout.PaneInnerHeight)
	if contentWidth < layout.ContentInnerWidth {
		contentStyle = contentStyle.Padding(0, appContentHorizontalPadding)
	}
	contentContent := contentStyle.Render(fitViewBox(a.content.View(), contentWidth, layout.PaneInnerHeight))
	contentPane := components.RenderPane(a.statusBar.styles, components.PaneSpec{
		Content: contentContent,
		Width:   layout.ContentPaneWidth,
		Height:  layout.BodyHeight,
		Focused: !overlayActive && a.mainFocus == mainFocusContent,
	})

	bodyParts := make([]string, 0, 3)
	if layout.SidebarPaneWidth > 0 {
		bodyParts = append(bodyParts, sidebarPane)
	}
	if layout.PaneGapWidth > 0 && layout.SidebarPaneWidth > 0 && layout.ContentPaneWidth > 0 {
		bodyParts = append(bodyParts, strings.Repeat(" ", layout.PaneGapWidth))
	}
	if layout.ContentPaneWidth > 0 {
		bodyParts = append(bodyParts, contentPane)
	}
	body := lipgloss.JoinHorizontal(lipgloss.Top, bodyParts...)

	hints := a.currentHints()
	statusBar := a.statusBar.View(hints, a.statusBarText(), a.windowWidth)

	base := lipgloss.JoinVertical(lipgloss.Left, body, statusBar)

	var toastView string
	if pinned := a.pinnedToasts(); len(pinned) > 0 {
		toastView = a.toasts.StackView(pinned...)
	} else {
		toastView = a.toasts.View()
	}
	if toastView != "" {
		placement := styles.ComputeToastPlacement(a.statusBar.styles.Chrome)
		base = renderTopRightOverlay(base, toastView, a.windowWidth, placement.TopInset, placement.BottomInset)
	}

	if a.activeOverlay == overlayNewSession {
		return renderOverlay(a.newSession.View(), a.windowWidth, a.windowHeight)
	}
	if a.activeOverlay == overlaySessionSearch {
		return renderOverlay(a.sessionSearch.View(), a.windowWidth, a.windowHeight)
	}
	if a.activeOverlay == overlaySettings {
		return a.settingsPage.View()
	}
	if a.activeOverlay == overlayHelp {
		return renderOverlay(a.helpOverlay.View(), a.windowWidth, a.windowHeight)
	}
	if a.activeOverlay == overlaySourceItems {
		return renderOverlay(a.sourceItemsOverlay.View(), a.windowWidth, a.windowHeight)
	}
	if a.activeOverlay == overlayLogs {
		return renderOverlay(a.logsOverlay.View(), a.windowWidth, a.windowHeight)
	}
	if a.content.mode == ContentModeOverview && a.content.overview.overlay != overviewOverlayNone {
		return renderCenteredOverlay(base, a.content.overview.overlayView(a.windowWidth, a.windowHeight), a.windowWidth, a.windowHeight)
	}

	return base
}

func (a App) statusBarText() string {
	parts := make([]string, 0, 2)
	if a.svcs.WorkspaceName != "" {
		parts = append(parts, a.svcs.WorkspaceName)
	}
	parts = append(parts, fmt.Sprintf("%d active sessions", a.activeSessionCount()))
	return strings.Join(parts, " · ")
}

func (a App) activeSessionCount() int {
	count := 0
	for _, session := range a.sessions {
		switch session.Status {
		case domain.AgentSessionPending, domain.AgentSessionRunning, domain.AgentSessionWaitingForAnswer:
			count++
		}
	}
	return count
}

// renderOverlay centers an overlay in the terminal window.
func renderOverlay(overlay string, w, h int) string {
	return lipgloss.Place(w, h,
		lipgloss.Center, lipgloss.Center,
		overlay,
		lipgloss.WithWhitespaceChars(" "),
	)
}

// renderCenteredOverlay overlays centered content on top of an existing base view without replacing the background.
func renderCenteredOverlay(base, overlay string, width, height int) string {
	if overlay == "" || width <= 0 || height <= 0 {
		return base
	}
	if base == "" {
		return renderOverlay(overlay, width, height)
	}

	baseLines := strings.Split(base, "\n")
	if len(baseLines) < height {
		baseLines = append(baseLines, make([]string, height-len(baseLines))...)
	} else if len(baseLines) > height {
		baseLines = baseLines[:height]
	}
	overlayLines := strings.Split(overlay, "\n")
	if len(overlayLines) > height {
		overlayLines = overlayLines[:height]
	}
	blockWidth := 0
	for _, overlayLine := range overlayLines {
		blockWidth = max(blockWidth, ansi.StringWidth(overlayLine))
	}
	if blockWidth <= 0 {
		return base
	}
	if blockWidth > width {
		blockWidth = width
	}
	startRow := max(0, (height-len(overlayLines))/2)
	if startRow+len(overlayLines) > height {
		startRow = max(0, height-len(overlayLines))
	}
	startCol := max(0, (width-blockWidth)/2)
	for i, overlayLine := range overlayLines {
		target := startRow + i
		if target < 0 || target >= len(baseLines) {
			break
		}
		line := overlayLine
		if ansi.StringWidth(line) > blockWidth {
			line = ansi.Truncate(line, blockWidth, "")
		}
		overlayWidth := ansi.StringWidth(line)
		prefix := ansi.Cut(baseLines[target], 0, startCol)
		if got := ansi.StringWidth(prefix); got < startCol {
			prefix += strings.Repeat(" ", startCol-got)
		}
		overlaySegment := line
		if overlayWidth < blockWidth {
			overlaySegment += strings.Repeat(" ", blockWidth-overlayWidth)
		}
		suffix := ""
		if startCol+blockWidth < width {
			suffix = ansi.Cut(baseLines[target], startCol+blockWidth, width)
		}
		baseLines[target] = prefix + overlaySegment + suffix
	}
	return strings.Join(baseLines, "\n")
}

// renderCentered centers content in the terminal window.
func renderCentered(content string, w, h int) string {
	return lipgloss.Place(w, h,
		lipgloss.Center, lipgloss.Center,
		content,
	)
}

// renderTopRightOverlay overlays a widget into the upper-right of the base view without changing its height.
func renderTopRightOverlay(base, overlay string, width, topInset, bottomInset int) string {
	if base == "" || overlay == "" || width <= 0 {
		return base
	}

	baseLines := strings.Split(base, "\n")
	overlayLines := strings.Split(overlay, "\n")
	maxOverlayBottom := len(baseLines) - bottomInset
	if maxOverlayBottom <= 0 {
		return base
	}
	if len(overlayLines) > maxOverlayBottom {
		overlayLines = overlayLines[:maxOverlayBottom]
	}

	start := topInset
	maxStart := maxOverlayBottom - len(overlayLines)
	maxStart = max(maxStart, 0)
	start = min(start, maxStart)
	start = max(start, 0)

	const overlayRightInset = 2
	blockWidth := 0
	for _, overlayLine := range overlayLines {
		blockWidth = max(blockWidth, ansi.StringWidth(overlayLine))
	}
	if blockWidth <= 0 {
		return base
	}
	if blockWidth > width {
		blockWidth = width
	}
	for i, overlayLine := range overlayLines {
		target := start + i
		if target < 0 || target >= maxOverlayBottom {
			break
		}
		overlayWidth := ansi.StringWidth(overlayLine)
		if overlayWidth <= 0 {
			continue
		}
		if overlayWidth > blockWidth {
			overlayLine = ansi.Truncate(overlayLine, blockWidth, "")
			overlayWidth = ansi.StringWidth(overlayLine)
		}
		rightInset := min(overlayRightInset, max(0, width-blockWidth))
		if blockWidth+rightInset >= width {
			rightInset = 0
		}
		prefixWidth := width - blockWidth - rightInset
		prefix := ansi.Cut(baseLines[target], 0, prefixWidth)
		if got := ansi.StringWidth(prefix); got < prefixWidth {
			prefix += strings.Repeat(" ", prefixWidth-got)
		}
		leftPad := max(0, blockWidth-overlayWidth)
		if blockWidth == width {
			leftPad = min(leftPad, max(0, width-rightInset-overlayWidth))
		}
		overlaySegment := strings.Repeat(" ", leftPad) + overlayLine
		if segmentWidth := ansi.StringWidth(overlaySegment); segmentWidth < blockWidth {
			overlaySegment += strings.Repeat(" ", blockWidth-segmentWidth)
		}
		suffix := ansi.Cut(baseLines[target], prefixWidth+blockWidth, width)
		if got := ansi.StringWidth(suffix); got < rightInset {
			suffix += strings.Repeat(" ", rightInset-got)
		}
		baseLines[target] = prefix + overlaySegment + suffix
	}

	return strings.Join(baseLines, "\n")
}

// --- Command helpers ---

func abandonSessionCmd(svc *service.TaskService, sessionID string) tea.Cmd {
	return func() tea.Msg {
		if err := svc.Fail(context.Background(), sessionID, nil); err != nil {
			return ErrMsg{Err: err}
		}
		return ActionDoneMsg{Message: "Session abandoned"}
	}
}

type sessionDeleteResult struct {
	TaskIDs        []string
	CleanupWarning error
}

func deleteSessionCmd(svcs Services, sessionsDir, sessionID string, reviewSessionLogs map[string]string) tea.Cmd {
	return func() tea.Msg {
		result, err := deleteSessionTasksAndArtifacts(context.Background(), svcs, sessionsDir, sessionID, reviewSessionLogs)
		if err != nil {
			return ErrMsg{Err: err}
		}
		msg := SessionDeletedMsg{SessionID: sessionID, TaskIDs: result.TaskIDs, Message: "Session deleted"}
		if result.CleanupWarning != nil {
			msg.Warning = "Session deleted, but some session logs could not be removed: " + result.CleanupWarning.Error()
		}
		return msg
	}
}

func deleteSessionTasksAndArtifacts(ctx context.Context, svcs Services, sessionsDir, sessionID string, reviewSessionLogs map[string]string) (sessionDeleteResult, error) {
	if svcs.Session == nil {
		return sessionDeleteResult{}, errors.New("session service not configured")
	}
	if svcs.Plan == nil {
		return sessionDeleteResult{}, errors.New("plan service not configured")
	}
	if svcs.Task == nil {
		return sessionDeleteResult{}, errors.New("task service not configured")
	}

	result := sessionDeleteResult{TaskIDs: make([]string, 0)}
	artifactDeletes := make([]struct {
		taskID        string
		reviewLogPath string
	}, 0)

	plan, err := svcs.Plan.GetPlanByWorkItemID(ctx, sessionID)
	var notFound service.ErrNotFound
	if err != nil && !errors.As(err, &notFound) {
		return sessionDeleteResult{}, err
	}
	if err == nil {
		taskPlans, err := svcs.Plan.ListSubPlansByPlanID(ctx, plan.ID)
		if err != nil {
			return sessionDeleteResult{}, err
		}
		for _, taskPlan := range taskPlans {
			tasks, err := svcs.Task.ListBySubPlanID(ctx, taskPlan.ID)
			if err != nil {
				return sessionDeleteResult{}, err
			}
			for _, task := range tasks {
				result.TaskIDs = append(result.TaskIDs, task.ID)
				artifactDeletes = append(artifactDeletes, struct {
					taskID        string
					reviewLogPath string
				}{taskID: task.ID, reviewLogPath: reviewSessionLogs[task.ID]})
				if err := svcs.Task.Delete(ctx, task.ID); err != nil {
					return sessionDeleteResult{}, err
				}
			}
			if err := svcs.Plan.DeleteSubPlan(ctx, taskPlan.ID); err != nil {
				return sessionDeleteResult{}, err
			}
		}
		if err := svcs.Plan.DeletePlan(ctx, plan.ID); err != nil {
			return sessionDeleteResult{}, err
		}
	}

	if err := svcs.Session.Delete(ctx, sessionID); err != nil {
		return sessionDeleteResult{}, err
	}

	var cleanupErrs []error
	for _, artifactDelete := range artifactDeletes {
		if err := deleteTaskArtifacts(sessionsDir, artifactDelete.taskID, artifactDelete.reviewLogPath); err != nil {
			cleanupErrs = append(cleanupErrs, err)
		}
	}
	result.CleanupWarning = errors.Join(cleanupErrs...)
	return result, nil
}

func deleteTaskArtifacts(sessionsDir, taskID, reviewLogPath string) error {
	var errs []error
	for _, deleteID := range deleteTaskArtifactIDs(sessionsDir, taskID, reviewLogPath) {
		paths, err := sessionInteractionPaths(sessionsDir, deleteID)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, path := range paths {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				errs = append(errs, fmt.Errorf("remove session log %s: %w", path, err))
			}
		}
	}
	return errors.Join(errs...)
}

func deleteTaskArtifactIDs(sessionsDir, taskID, reviewLogPath string) []string {
	if strings.TrimSpace(sessionsDir) == "" || strings.TrimSpace(taskID) == "" {
		return nil
	}
	ids := []string{taskID}
	if strings.TrimSpace(reviewLogPath) == "" {
		return ids
	}
	reviewLogPath = filepath.Clean(reviewLogPath)
	if filepath.Dir(reviewLogPath) != filepath.Clean(sessionsDir) {
		return ids
	}
	base := filepath.Base(reviewLogPath)
	if reviewID, ok := strings.CutSuffix(base, ".log"); ok {
		if reviewID != "" && reviewID != taskID {
			ids = append(ids, reviewID)
		}
	}
	return ids
}

func workItemDisplayLabel(wi domain.Session) string {
	label := strings.TrimSpace(wi.ExternalID)
	if label == "" {
		label = strings.TrimSpace(wi.Title)
	}
	if label == "" {
		label = wi.ID
	}
	return label
}

func existingWorkItemByExternalID(svc *service.SessionService, workspaceID, externalID string) (domain.Session, error) {
	trimmedWorkspaceID := strings.TrimSpace(workspaceID)
	if trimmedWorkspaceID == "" {
		return domain.Session{}, errors.New("workspace not configured")
	}
	trimmedExternalID := strings.TrimSpace(externalID)
	if trimmedExternalID == "" {
		return domain.Session{}, errors.New("work item external_id is required")
	}
	items, err := svc.List(context.Background(), repository.SessionFilter{
		WorkspaceID: &trimmedWorkspaceID,
		ExternalID:  &trimmedExternalID,
		Limit:       1,
	})
	if err != nil {
		return domain.Session{}, err
	}
	if len(items) > 0 {
		return items[0], nil
	}
	return domain.Session{}, service.ErrNotFound{Entity: "work item", ID: trimmedExternalID}
}

func persistCreatedWorkItemMsg(svcs Services, wi domain.Session) tea.Msg {
	if svcs.Session == nil {
		return ErrMsg{Err: errors.New("work item service not configured")}
	}
	if strings.TrimSpace(svcs.WorkspaceID) == "" {
		return ErrMsg{Err: errors.New("workspace not configured")}
	}
	wi.WorkspaceID = svcs.WorkspaceID
	label := workItemDisplayLabel(wi)
	if err := svcs.Session.Create(context.Background(), wi); err != nil {
		var alreadyExists service.ErrAlreadyExists
		if errors.As(err, &alreadyExists) {
			duplicateID := strings.TrimSpace(alreadyExists.ID)
			if duplicateID == "" {
				duplicateID = wi.ExternalID
			}
			existing, lookupErr := existingWorkItemByExternalID(svcs.Session, svcs.WorkspaceID, duplicateID)
			if lookupErr == nil {
				return SessionDuplicatePromptMsg{
					RequestedSession: wi,
					ExistingSession:  existing,
				}
			}
			return ErrMsg{Err: fmt.Errorf("work item already exists in this workspace: %s", label)}
		}
		return ErrMsg{Err: err}
	}
	return SessionCreatedMsg{Session: wi, Message: "Session created: " + label}
}

func createManualSessionCmd(svcs Services, msg NewSessionManualMsg) tea.Cmd {
	return func() tea.Msg {
		if msg.Adapter == nil {
			return ErrMsg{Err: errors.New("no adapter available")}
		}
		wi, err := msg.Adapter.Resolve(context.Background(), adapter.Selection{
			Scope:  domain.ScopeManual,
			Manual: &adapter.ManualInput{Title: msg.Title, Description: msg.Desc},
		})
		if err != nil {
			return ErrMsg{Err: err}
		}
		return persistCreatedWorkItemMsg(svcs, wi)
	}
}

func createBrowseSessionCmd(svcs Services, msg NewSessionBrowseMsg) tea.Cmd {
	return func() tea.Msg {
		if msg.Adapter == nil {
			return ErrMsg{Err: errors.New("no adapter available")}
		}
		wi, err := msg.Adapter.Resolve(context.Background(), msg.Selection)
		if err != nil {
			return ErrMsg{Err: err}
		}
		return persistCreatedWorkItemMsg(svcs, wi)
	}
}

// formatAdapterErrorToast formats an adapter error for display as a toast.
// Output is max 4 lines to fit the toast display constraint.
func formatAdapterErrorToast(msg AdapterErrorMsg) string {
	errStr := msg.Err.Error()
	if len(errStr) > 80 {
		errStr = errStr[:77] + "..."
	}
	return fmt.Sprintf("%s: %s failed\n%s\nRetried %dx", msg.Adapter, msg.EventType, errStr, msg.Retries)
}

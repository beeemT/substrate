package views

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/event"
	"github.com/beeemT/substrate/internal/orchestrator"
	"github.com/beeemT/substrate/internal/repository"
	"github.com/beeemT/substrate/internal/service"
	"github.com/beeemT/substrate/internal/sessionlog"
	"github.com/beeemT/substrate/internal/tui/components"
	"github.com/beeemT/substrate/internal/tui/styles"
	"github.com/beeemT/substrate/internal/tuilog"
)

// overlayKind identifies which overlay is active.
type overlayKind int

const (
	overlayNone overlayKind = iota
	overlayNewSession
	overlayNewSessionAutonomous
	overlaySessionSearch
	overlaySettings
	overlayWorkspaceInit
	overlayActionMenu
	overlaySourceItems
	overlayLogs
	overlayAddRepo
	overlayRepoManager
	overlayOverviewLinks
	overlayReviewFollowup
	overlayWorktreePicker
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

const (
	taskSidebarSourceDetailsID = "__source_details__"
	taskSidebarArtifactsID     = "__artifacts__"
)

type mainFocusArea int

const (
	mainFocusSidebar mainFocusArea = iota
	mainFocusContent
)

const (
	appContentHorizontalPadding = 1

	autonomousLifecycleStartedToast = "New Session autonomous mode started"
	autonomousLifecycleStoppedToast = "New Session autonomous mode stopped"
)

func appContentBodyWidth(width int) int {
	if width > appContentHorizontalPadding*2 {
		return width - (appContentHorizontalPadding * 2)
	}
	return width
}

// App is the top-level bubbletea model.
type App struct { //nolint:recvcheck // Bubble Tea convention

	provider   ServiceProvider
	runtimeCtx RuntimeContext

	// Bus subscription for event-driven updates
	busSub        *event.Subscriber
	eventConsumer *EventConsumer

	// Layout sub-models
	sidebar   SidebarModel
	content   ContentModel
	statusBar StatusBarModel

	// Overlays
	activeOverlay               overlayKind
	newSession                  NewSessionOverlay
	newSessionAutonomousOverlay NewSessionAutonomousOverlay
	sessionSearch               SessionSearchOverlay
	settingsPage                SettingsPage
	workspaceModal              WorkspaceInitModal
	actionMenu                  ActionMenuModel
	actionMenuReturnOverlay     overlayKind
	sourceItemsOverlay          SourceItemsOverlay
	overviewLinksOverlay        OverviewLinksOverlay
	overviewLinksReturnOverlay  overlayKind
	reviewFollowupOverlay       ReviewFollowupModel
	logsOverlay                 LogsOverlay
	addRepo                     AddRepoOverlay
	repoManager                 RepoManagerOverlay
	worktreePicker              WorktreePickerOverlay
	hasWorkspace                bool
	styles                      styles.Styles

	// Toasts
	toasts                             components.ToastModel
	startupIntegrationsInProgress      bool
	startupIntegrationSpinner          spinner.Model
	startupIntegrationSpinnerFrameOnly bool
	inputBlocked                       bool

	// State cache (refreshed by DB poll)
	workItems []domain.Session
	sessions  []domain.AgentSession
	subPlans  map[string][]domain.TaskPlan          // keyed by planID
	plans     map[string]*domain.Plan               // keyed by workItemID
	questions map[string]map[string]domain.Question // sessionID → questionID → Question
	reviews   map[string]ReviewsLoadedMsg           // keyed by sessionID

	savedNewSessionFilters   []domain.NewSessionFilter
	newSessionAutonomous     *NewSessionAutonomousRuntime
	newSessionAutonomousChan <-chan tea.Msg

	// Log tailing deduplication
	tailingSessionIDs map[string]bool
	// reviewSessionLogs maps implementation session ID → review agent log path.
	reviewSessionLogs map[string]string

	// Live instance cache for dead-owner detection
	liveInstanceIDs map[string]bool

	// Confirm dialog
	confirm       components.ConfirmDialog
	confirmActive bool

	// managedRepoSlugs is the set of lowercase owner/repo slugs for repos currently
	// in the workspace. Rebuilt on every ManagedReposLoadedMsg and updated on clone success.
	managedRepoSlugs map[string]bool
	// pendingCloneSlug is the slug of the repo currently being cloned; cleared on RepoClonedMsg.
	pendingCloneSlug string

	// addRepoOpenedFromRepoManager tracks whether the add-repo overlay was
	// opened from inside the repo manager so Escape returns there instead of home.
	addRepoOpenedFromRepoManager bool

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
	// sbHeight is the number of footer rows allocated for the status bar.
	// Computed on WindowSizeMsg and used by both Update (for SetSize) and View (for layout).
	// Keeping it stable across focus changes prevents body height from flickering.
	sbHeight int

	// Cached base render (sidebar + content + status bar) for overlay
	// compositing. Pointer so the value survives Bubble Tea's value-receiver
	// View() copies; allocated once in NewApp.
	cachedBase *string

	// Pipeline cancellation: maps workItemID → cancel func for the running
	// orchestrator goroutine (implementation/planning). Used to tear down
	// agent processes when the session is deleted.
	pipelineCancels map[string]context.CancelFunc

	// program is the bubble tea program instance, used to send messages from goroutines.
	program *tea.Program
}

// NewApp creates a new App from the given ServiceProvider and RuntimeContext.
func NewApp(provider ServiceProvider, runtimeCtx RuntimeContext) *App {
	st := styles.NewStyles(styles.DefaultTheme)
	sessionsDir, _ := config.SessionsDir()
	cwd, _ := os.Getwd()

	app := App{
		provider:                       provider,
		runtimeCtx:                     runtimeCtx,
		sidebar:                        NewSidebarModel(st),
		content:                        NewContentModel(st),
		statusBar:                      NewStatusBarModel(st),
		newSession:                     NewNewSessionOverlay(provider.Adapters(), runtimeCtx.WorkspaceID, st),
		newSessionAutonomousOverlay:    NewNewSessionAutonomousOverlay(st),
		sessionSearch:                  NewSessionSearchOverlay(st),
		settingsPage:                   NewSettingsPage(provider.Settings(), st),
		actionMenu:                     NewActionMenuModel(st),
		sourceItemsOverlay:             NewSourceItemsOverlay(st),
		overviewLinksOverlay:           NewOverviewLinksOverlay(st),
		reviewFollowupOverlay:          NewReviewFollowupModel(st),
		logsOverlay:                    NewLogsOverlay(runtimeCtx.LogStore, st),
		addRepo:                        NewAddRepoOverlay(provider.RepoSources(), runtimeCtx.WorkspaceDir, provider.GitClient(), st),
		repoManager:                    NewRepoManagerOverlay(runtimeCtx.WorkspaceDir, provider.GitClient(), st),
		worktreePicker:                 NewWorktreePickerOverlay(runtimeCtx.WorkspaceDir, provider.GitClient(), st),
		toasts:                         components.NewToastModel(st),
		startupIntegrationsInProgress:  runtimeCtx.StartupIntegrationsInProgress,
		startupIntegrationSpinner:      components.NewSpinner(st),
		subPlans:                       make(map[string][]domain.TaskPlan),
		plans:                          make(map[string]*domain.Plan),
		questions:                      make(map[string]map[string]domain.Question),
		reviews:                        make(map[string]ReviewsLoadedMsg),
		tailingSessionIDs:              make(map[string]bool),
		liveInstanceIDs:                make(map[string]bool),
		reviewSessionLogs:              make(map[string]string),
		taskSessionSelectionByWorkItem: make(map[string]string),
		pipelineCancels:                make(map[string]context.CancelFunc),
		sessionsDir:                    sessionsDir,
		hasWorkspace:                   runtimeCtx.WorkspaceID != "",
		styles:                         st,
		cachedBase:                     new(string),
		sbHeight:                       1, // conservative default; updated on first WindowSizeMsg
	}

	app.syncNewSessionFilterOverlays()

	// Apply config-based defaults for the home view.
	app.sidebar.filter = sidebarFilterFromString(runtimeCtx.Cfg.UI.DefaultFilter)
	app.sidebar.dimension = sidebarDimFromString(runtimeCtx.Cfg.UI.DefaultGroup)
	*app.sidebar.viewDirty = true
	tuilog.SetDefaultLevel(logLevelFromConfig(runtimeCtx.Cfg))

	if !app.hasWorkspace {
		app.workspaceModal = NewWorkspaceInitModal(cwd, st, provider.Workspace())
		app.activeOverlay = overlayWorkspaceInit
	}
	return &app
}

// RunTUI launches the bubbletea program.
func RunTUI(provider ServiceProvider, runtimeCtx RuntimeContext) error {
	app := NewApp(provider, runtimeCtx)
	p := tea.NewProgram(app, tea.WithMouseCellMotion(), tea.WithFilter(macOSKeyFilter))
	app.program = p

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
func (a *App) Init() tea.Cmd {
	var cmds []tea.Cmd

	cmds = append(cmds, tea.ClearScreen, PollTickCmd(), HeartbeatTickCmd(), components.ToastTickCmd(), WaitForLogToastCmd(a.runtimeCtx.LogToasts), StartupWarningsCmd(a.provider.StartupWarnings()))
	if a.runtimeCtx.StartupIntegrationsInProgress {
		a.inputBlocked = true
		cmds = append(cmds, StartupIntegrationsStartCmd(), StartupIntegrationsSpinnerTickCmd(a.startupIntegrationSpinner))
	} else {
		// Non-workspace startup: schedule diagnostics if still pending.
		snapshot := a.provider.Settings().Snapshot()
		if snapshot.DiagnosticsState == SettingsDiagnosticsPending {
			cmds = append(cmds, SettingsDiagnosticsStartCmd())
		}
	}

	if a.runtimeCtx.WorkspaceID != "" {
		cmds = append(cmds,
			LoadSessionsCmd(a.provider.Session(), a.runtimeCtx.WorkspaceID),
			LoadTasksCmd(a.provider.Task(), a.runtimeCtx.WorkspaceID),
			LoadLiveInstancesCmd(a.provider.Instance(), a.runtimeCtx.WorkspaceID),
			LoadNewSessionFiltersCmd(a.provider.NewSessionFilters(), a.runtimeCtx.WorkspaceID),
			ReconcileOrphanedTasksCmd(a.provider.Task(), a.provider.Instance(), a.runtimeCtx.WorkspaceID, a.runtimeCtx.InstanceID),
		)

		// Subscribe to event bus for event-driven updates
		if a.provider.Bus() != nil {
			var err error
			a.busSub, err = a.provider.Bus().Subscribe("tui:"+a.runtimeCtx.WorkspaceID, eventSubscriptionTopics()...)
			if err != nil {
				slog.Error("failed to subscribe TUI to event bus", "error", err)
			} else {
				// Bridge: forward events from subscriber channel to the update loop via EventConsumer.
				a.eventConsumer = NewEventConsumer(a, a.busSub)
				cmds = append(cmds, a.eventConsumer.BridgeCmd())
			}
		}
	}

	if a.activeOverlay == overlayWorkspaceInit {
		cmds = append(cmds, a.workspaceModal.ScanCmd())
	} else if a.runtimeCtx.WorkspaceID != "" && a.runtimeCtx.WorkspaceDir != "" {
		// Scan for plain-git repos that were manually added since the workspace was initialised.
		cmds = append(cmds, WorkspaceHealthCheckCmd(a.runtimeCtx.WorkspaceDir))
	}

	return tea.Batch(cmds...)
}

func (a *App) applyServicesReload(reload viewsServicesReload) {
	// runtimeCtx is immutable after construction; update workspace fields from reload.
	a.runtimeCtx.WorkspaceID = reload.Services.WorkspaceID
	a.runtimeCtx.WorkspaceName = reload.Services.WorkspaceName
	a.runtimeCtx.WorkspaceDir = reload.Services.WorkspaceDir

	a.newSession = NewNewSessionOverlay(reload.Services.Adapters, a.runtimeCtx.WorkspaceID, a.statusBar.styles)
	a.newSession.SetSize(a.windowWidth, a.windowHeight)
	a.newSessionAutonomousOverlay = NewNewSessionAutonomousOverlay(a.statusBar.styles)
	a.newSessionAutonomousOverlay.SetSize(a.windowWidth, a.windowHeight)
	a.addRepo = NewAddRepoOverlay(reload.Services.RepoSources, a.runtimeCtx.WorkspaceDir, reload.Services.GitClient, a.statusBar.styles)
	a.addRepo.SetSize(a.windowWidth, a.windowHeight)
	a.addRepo.SetPresentSlugs(a.managedRepoSlugs)
	a.repoManager = NewRepoManagerOverlay(a.runtimeCtx.WorkspaceDir, reload.Services.GitClient, a.statusBar.styles)
	a.repoManager.SetSize(a.windowWidth, a.windowHeight)
	a.worktreePicker = NewWorktreePickerOverlay(a.runtimeCtx.WorkspaceDir, reload.Services.GitClient, a.statusBar.styles)
	a.worktreePicker.SetSize(a.windowWidth, a.windowHeight)
	a.settingsPage.RefreshFromService()
	a.sessionsDir = reload.SessionsDir
	a.hasWorkspace = a.runtimeCtx.WorkspaceID != ""
	a.syncNewSessionFilterOverlays()

	// Update log level filter.
	tuilog.SetDefaultLevel(logLevelFromConfig(reload.Cfg))
}

func (a *App) commandsAfterServiceReload(oldWorkspaceID string) []tea.Cmd {
	cmds := make([]tea.Cmd, 0, 6)
	if a.provider.Bus() != nil {
		if a.busSub != nil {
			a.provider.Bus().Unsubscribe("tui:" + oldWorkspaceID)
		}
		var err error
		a.busSub, err = a.provider.Bus().Subscribe("tui:"+a.runtimeCtx.WorkspaceID, eventSubscriptionTopics()...)
		if err != nil {
			slog.Error("failed to resubscribe TUI to event bus", "error", err)
		} else {
			a.eventConsumer = NewEventConsumer(a, a.busSub)
			cmds = append(cmds, a.eventConsumer.BridgeCmd())
		}
	}
	if a.runtimeCtx.WorkspaceID != "" {
		cmds = append(cmds,
			LoadSessionsCmd(a.provider.Session(), a.runtimeCtx.WorkspaceID),
			LoadTasksCmd(a.provider.Task(), a.runtimeCtx.WorkspaceID),
			LoadLiveInstancesCmd(a.provider.Instance(), a.runtimeCtx.WorkspaceID),
			LoadNewSessionFiltersCmd(a.provider.NewSessionFilters(), a.runtimeCtx.WorkspaceID),
			ReconcileOrphanedTasksCmd(a.provider.Task(), a.provider.Instance(), a.runtimeCtx.WorkspaceID, a.runtimeCtx.InstanceID),
		)
	}
	return cmds
}

func eventSubscriptionTopics() []string {
	return []string{
		string(domain.EventWorkItemIngested),
		string(domain.EventWorkItemPlanning),
		string(domain.EventWorkItemPlanReview),
		string(domain.EventWorkItemApproved),
		string(domain.EventWorkItemImplementing),
		string(domain.EventWorkItemReviewing),
		string(domain.EventWorkItemCompleted),
		string(domain.EventWorkItemFailed),
		string(domain.EventWorkItemMerged),
		string(domain.EventWorkItemArchived),
		string(domain.EventAgentSessionStarted),
		string(domain.EventAgentSessionCompleted),
		string(domain.EventAgentSessionFailed),
		string(domain.EventAgentSessionInterrupted),
		string(domain.EventAgentSessionResumed),
		string(domain.EventAgentSessionFollowUp),
		string(domain.EventAgentSessionWaitingForAnswer),
		string(domain.EventPlanGenerated),
		string(domain.EventPlanApproved),
		string(domain.EventPlanRejected),
		string(domain.EventPlanRevised),
		string(domain.EventPlanSubmitted),
		string(domain.EventPlanFailed),
		string(domain.EventSubPlanStarted),
		string(domain.EventSubPlanCompleted),
		string(domain.EventSubPlanFailed),
		string(domain.EventSubPlanPRReady),
		string(domain.EventReviewStarted),
		string(domain.EventReviewCompleted),
		string(domain.EventCritiquesFound),
		string(domain.EventReimplementationStarted),
		string(domain.EventPRMerged),
		string(domain.EventPRReviewStateChanged),
		string(domain.EventAgentQuestionRaised),
		string(domain.EventAgentQuestionAnswered),
		string(domain.EventAdapterError),
		string(domain.EventForemanStarted),
		string(domain.EventForemanStopped),
	}
}

func logLevelFromConfig(cfg *config.Config) slog.Level {
	if cfg.UI.LogLevel == "" {
		return slog.LevelInfo
	}
	switch cfg.UI.LogLevel {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
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
	return a.sessionSearch.Filter(a.runtimeCtx.WorkspaceID)
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
	if wi.WorkspaceID == a.runtimeCtx.WorkspaceID {
		workspaceName = strings.TrimSpace(a.runtimeCtx.WorkspaceName)
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
	for _, agentSession := range sessions {
		if agentSession.UpdatedAt.After(latest.UpdatedAt) || (agentSession.UpdatedAt.Equal(latest.UpdatedAt) && (agentSession.CreatedAt.After(latest.CreatedAt) || (agentSession.CreatedAt.Equal(latest.CreatedAt) && agentSession.ID > latest.ID))) {
			latest = agentSession
		}
	}
	// Interrupted/open-question projections come from the graph leaves so
	// that an old interrupted/waiting session whose work has already moved
	// on (retry, follow-up) is not surfaced to search results.
	hasOpenQuestion := false
	hasInterrupted := false
	for _, leaf := range leafAgentSessions(sessions) {
		if leaf.Status == domain.AgentSessionWaitingForAnswer {
			hasOpenQuestion = true
		}
		if leaf.Status == domain.AgentSessionInterrupted {
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
	if filter.WorkspaceID != nil && *filter.WorkspaceID != a.runtimeCtx.WorkspaceID {
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
	return tea.Batch(spinnerCmd, SearchSessionHistoryCmd(a.provider.Task(), filter))
}

func (a *App) openSessionSearch() tea.Cmd {
	a.activeOverlay = overlaySessionSearch
	a.sessionSearch.Open(a.sessionSearchScope(), a.hasWorkspace)
	return a.runSessionSearch(true)
}

func (a *App) openNewSession() tea.Cmd {
	a.activeOverlay = overlayNewSession
	return a.newSession.Open()
}

func (a *App) syncNewSessionFilterOverlays() {
	a.newSession.SetSavedNewSessionFilters(a.savedNewSessionFilters)
	activeFilterIDs := []string(nil)
	if a.newSessionAutonomous != nil {
		activeFilterIDs = a.newSessionAutonomous.activeFilterIDsSnapshot()
	}
	a.newSessionAutonomousOverlay.SetSavedFilters(a.savedNewSessionFilters)
	a.newSessionAutonomousOverlay.SetRuntimeState(a.newSessionAutonomous != nil, activeFilterIDs)
}

func (a *App) openNewSessionAutonomousOverlay() tea.Cmd {
	a.syncNewSessionFilterOverlays()
	a.newSessionAutonomousOverlay.Open()
	a.activeOverlay = overlayNewSessionAutonomous
	return nil
}

func (a *App) openAddRepo() tea.Cmd {
	a.activeOverlay = overlayAddRepo
	cmds := []tea.Cmd{a.addRepo.Open(a.managedRepoSlugs)}
	// If no scan has run yet but we know the workspace dir, trigger one now.
	// The result will arrive as ManagedReposLoadedMsg and update the overlay via SetPresentSlugs.
	if a.managedRepoSlugs == nil && a.runtimeCtx.WorkspaceDir != "" {
		cmds = append(cmds, LoadManagedReposCmd(a.runtimeCtx.WorkspaceDir))
	}
	return tea.Batch(cmds...)
}

func (a *App) openRepoManager() tea.Cmd {
	a.activeOverlay = overlayRepoManager
	return a.repoManager.Open()
}

func (a *App) openActionMenu() tea.Cmd {
	ctx := a.currentActionContext()
	if ctx == ContextModalExclusive {
		return nil
	}
	a.actionMenuReturnOverlay = a.activeOverlay
	a.actionMenu.Open(a, ctx)
	a.actionMenu.SetSize(a.windowWidth, a.windowHeight)
	a.activeOverlay = overlayActionMenu
	return nil
}

func (a *App) closeActionMenu() {
	a.activeOverlay = a.actionMenuReturnOverlay
	a.actionMenuReturnOverlay = overlayNone
}

// anyInputCaptured returns true if any text input is currently capturing keys.
func (a *App) anyInputCaptured() bool {
	// Check content-level inputs (session log steer, etc.)
	if a.content.InputCaptured() {
		return true
	}
	// Check plan review feedback
	if a.content.overview.planReview.IsFeedbackActive() {
		return true
	}
	// Check completed view feedback
	if a.content.overview.completed.InputCaptured() {
		return true
	}
	// Check new session overlays for manual/extra-context inputs
	if a.newSession.showManual || a.newSession.showExtraContext {
		return true
	}
	// Check add repo overlay for manual URL input
	if a.addRepo.showManual {
		return true
	}
	// Check session search overlay for search input
	if a.sessionSearch.Active() && a.sessionSearch.focus == sessionSearchFocusInput {
		return true
	}
	// Check settings overlay for field editor
	if a.settingsPage.editing {
		return true
	}
	return false
}

func (a App) currentHints() []KeybindHint {
	global := DefaultHints()
	prependDelete := func(hints []KeybindHint) []KeybindHint {
		if a.deletableSessionID() == "" {
			return hints
		}
		return append([]KeybindHint{{Key: "d", Label: "Delete session"}}, hints...)
	}
	prependArchive := func(hints []KeybindHint) []KeybindHint {
		if sessionID := a.archivablSessionID(); sessionID != "" {
			return append([]KeybindHint{{Key: "a", Label: "Archive session"}}, hints...)
		}
		if sessionID := a.unarchivablSessionID(); sessionID != "" {
			return append([]KeybindHint{{Key: "a", Label: "Unarchive session"}}, hints...)
		}
		return hints
	}
	prependInterrupt := func(hints []KeybindHint) []KeybindHint {
		if len(a.interruptibleFocusedSessionIDs()) == 0 {
			return hints
		}
		return append([]KeybindHint{{Key: "I", Label: "Interrupt"}}, hints...)
	}
	if a.mainFocus == mainFocusContent {
		hints := a.content.KeybindHints()
		return append(prependDelete(prependInterrupt(prependArchive(hints))), global...)
	}
	if a.sidebarMode == sidebarPaneTasks {
		hints := []KeybindHint{}
		if a.selectedTaskSessionID() != "" && a.sourceDetailsNoticeForWorkItem(a.workItemByID(a.currentWorkItemID)) != nil {
			hints = append([]KeybindHint{{Key: "Enter", Label: "Open overview"}}, hints...)
		}
		// Add 'r' hint when a failed session is selected for direct retry.
		if a.retryableFocusedSessionID() != "" {
			hints = append([]KeybindHint{{Key: "r", Label: "Retry"}}, hints...)
		}
		return append(prependDelete(prependInterrupt(prependArchive(hints))), global...)
	}
	return append(prependDelete(prependInterrupt(prependArchive([]KeybindHint{{Key: "f", Label: "Filter"}, {Key: "g", Label: "Group"}, {Key: "o", Label: "Sort"}}))), global...)
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
	return a, cmd
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

func summarizeQuestionText(text string, limit int) string {
	trimmed := strings.Join(strings.Fields(text), " ")
	if limit <= 0 || trimmed == "" {
		return trimmed
	}
	runes := []rune(trimmed)
	if len(runes) <= limit {
		return trimmed
	}
	if limit == 1 {
		return string(runes[:1])
	}
	return string(runes[:limit-1]) + "…"
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

func (a *App) upsertSession(agentSession domain.AgentSession) {
	for i := range a.sessions {
		if a.sessions[i].ID == agentSession.ID {
			a.sessions[i] = agentSession
			return
		}
	}
	a.sessions = append(a.sessions, agentSession)
}

// upsertSubPlan adds or updates a sub-plan in the subPlans map keyed by planID.
func (a *App) upsertSubPlan(planID string, sp domain.TaskPlan) {
	if planID == "" || sp.ID == "" {
		return
	}
	if a.subPlans == nil {
		a.subPlans = make(map[string][]domain.TaskPlan)
	}
	sps, exists := a.subPlans[planID]
	if !exists {
		a.subPlans[planID] = []domain.TaskPlan{sp}
		return
	}
	// Update or append
	for i := range sps {
		if sps[i].ID == sp.ID {
			sps[i] = sp
			a.subPlans[planID] = sps
			return
		}
	}
	a.subPlans[planID] = append(sps, sp)
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

// archivablSessionID returns the work item ID if the currently selected session
// is in a terminal state (completed, merged, failed) and can be archived.
func (a App) archivablSessionID() string {
	if id := a.archivablSessionIDFromHistoryEntry(); id != "" {
		return id
	}
	if id := a.archivablSessionIDFromWorkItem(); id != "" {
		return id
	}
	return ""
}

// archivablSessionIDFromHistoryEntry returns the work item ID from the current history entry
// if it is in a terminal state and can be archived.
func (a App) archivablSessionIDFromHistoryEntry() string {
	if a.currentHistoryEntry.WorkItemID == "" {
		return ""
	}
	switch a.currentHistoryEntry.State {
	case domain.SessionCompleted, domain.SessionMerged, domain.SessionFailed:
		return a.currentHistoryEntry.WorkItemID
	}
	return ""
}

// archivablSessionIDFromWorkItem returns the work item ID from the current work item
// if it is in a terminal state and can be archived.
func (a App) archivablSessionIDFromWorkItem() string {
	if a.currentWorkItemID == "" {
		return ""
	}
	wi := a.workItemByID(a.currentWorkItemID)
	if wi == nil {
		return ""
	}
	switch wi.State {
	case domain.SessionCompleted, domain.SessionMerged, domain.SessionFailed:
		return a.currentWorkItemID
	}
	return ""
}

// unarchivablSessionID returns the work item ID if the currently selected session
// is archived and can be unarchived.
func (a App) unarchivablSessionID() string {
	if id := a.unarchivablSessionIDFromHistoryEntry(); id != "" {
		return id
	}
	if id := a.unarchivablSessionIDFromWorkItem(); id != "" {
		return id
	}
	return ""
}

func (a App) unarchivablSessionIDFromHistoryEntry() string {
	if a.currentHistoryEntry.WorkItemID == "" {
		return ""
	}
	if a.currentHistoryEntry.State == domain.SessionArchived {
		return a.currentHistoryEntry.WorkItemID
	}
	return ""
}

func (a App) unarchivablSessionIDFromWorkItem() string {
	if a.currentWorkItemID == "" {
		return ""
	}
	wi := a.workItemByID(a.currentWorkItemID)
	if wi != nil && wi.State == domain.SessionArchived {
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

func (a App) workItemTaskSession(workItemID, sessionID string) *domain.AgentSession {
	for _, agentSession := range a.sessionsForWorkItem(workItemID) {
		if agentSession.ID == sessionID {
			s := agentSession
			return &s
		}
	}
	return nil
}

func (a App) defaultTaskSessionID(workItemID string) string {
	_ = workItemID
	return ""
}

func (a App) latestPlanningSession(workItemID string) *domain.AgentSession {
	for _, agentSession := range a.sessionsForWorkItem(workItemID) {
		if agentSession.Kind == domain.AgentSessionKindPlanning {
			s := agentSession
			return &s
		}
	}
	return nil
}

func (a App) latestImplementationSession(workItemID, subPlanID string) *domain.AgentSession {
	for _, agentSession := range a.sessionsForWorkItem(workItemID) {
		if agentSession.Kind == domain.AgentSessionKindImplementation && agentSession.SubPlanID == subPlanID {
			s := agentSession
			return &s
		}
	}
	return nil
}

func taskSessionPhaseRank(kind domain.AgentSessionKind) int {
	switch kind {
	case domain.AgentSessionKindPlanning:
		return 0
	case domain.AgentSessionKindImplementation:
		return 1
	case domain.AgentSessionKindReview:
		return 2
	default:
		return 3
	}
}

func taskSessionModeLabel(agentSession *domain.AgentSession) string {
	switch agentSession.Kind {
	case domain.AgentSessionKindPlanning:
		return "Planning"
	case domain.AgentSessionKindReview:
		return "Review"
	default:
		return "Task"
	}
}

func taskSidebarSessionTitle(agentSession *domain.AgentSession) string {
	switch agentSession.Kind {
	case domain.AgentSessionKindPlanning:
		// Don't prefix with "Planning" - we're already in the Planning group
		return "Session " + shortSessionID(agentSession.ID)
	case domain.AgentSessionKindReview:
		return "Review " + shortSessionID(agentSession.ID)
	default:
		return "Implementation " + shortSessionID(agentSession.ID)
	}
}

func taskSessionDisplayName(agentSession *domain.AgentSession) string {
	switch agentSession.Kind {
	case domain.AgentSessionKindPlanning:
		return "Planning"
	case domain.AgentSessionKindReview:
		return firstNonEmptyString(agentSession.RepositoryName, "Review")
	default:
		return firstNonEmptyString(agentSession.RepositoryName, "Task")
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
	if entry.WorkspaceID == "" || entry.WorkspaceID != a.runtimeCtx.WorkspaceID || entry.WorkItemID == "" {
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
	return entry.WorkspaceID != "" && entry.WorkspaceID != a.runtimeCtx.WorkspaceID
}

func (a App) readOnlyToast() (components.Toast, bool) {
	if !a.historyEntryIsReadOnly(a.currentHistoryEntry) {
		return components.Toast{}, false
	}
	return components.Toast{Message: "Read only", Level: components.ToastWarning}, true
}

func (a App) harnessWarningToast() (components.Toast, bool) {
	snapshot := a.provider.Settings().Snapshot()
	if snapshot.DiagnosticsState != SettingsDiagnosticsReady {
		return components.Toast{}, false
	}
	warning := strings.TrimSpace(snapshot.HarnessWarning)
	if warning == "" {
		return components.Toast{}, false
	}
	return components.Toast{Message: warning, Level: components.ToastWarning}, true
}

func (a App) startupIntegrationsToast() (components.Toast, bool) {
	if !a.startupIntegrationsInProgress {
		return components.Toast{}, false
	}
	return components.Toast{Message: a.startupIntegrationSpinner.View() + "Starting integrations…", Level: components.ToastInfo}, true
}

func (a App) pinnedToasts() []components.Toast {
	pinned := make([]components.Toast, 0, 3)
	if startupToast, ok := a.startupIntegrationsToast(); ok {
		pinned = append(pinned, startupToast)
	}
	if readOnlyToast, ok := a.readOnlyToast(); ok {
		pinned = append(pinned, readOnlyToast)
	}
	if harnessWarning, ok := a.harnessWarningToast(); ok {
		pinned = append(pinned, harnessWarning)
	}
	return pinned
}

func isAutonomousLifecycleToastMessage(message string) bool {
	switch strings.TrimSpace(message) {
	case autonomousLifecycleStartedToast, autonomousLifecycleStoppedToast:
		return true
	default:
		return false
	}
}

func shouldSuppressAutonomousStatusToast(level components.ToastLevel, message string) bool {
	return level == components.ToastInfo && isAutonomousLifecycleToastMessage(message)
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
func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	var cmd tea.Cmd

	a.startupIntegrationSpinnerFrameOnly = false

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.windowWidth = msg.Width
		a.windowHeight = msg.Height
		*a.cachedBase = ""
		a.toasts.SetWidth(msg.Width)
		sbHeight := a.statusBar.RequiredHeight(a.currentHints(), a.statusBarText(), msg.Width)
		a.sbHeight = sbHeight
		layout := styles.ComputeMainPageLayout(msg.Width, msg.Height, SidebarWidth, a.statusBar.styles.Chrome, sbHeight)
		a.sidebar.SetWidth(layout.SidebarInnerWidth)
		a.sidebar.SetHeight(layout.PaneInnerHeight)
		a.content.SetSize(appContentBodyWidth(layout.ContentInnerWidth), layout.PaneInnerHeight)
		a.content.SetTerminalSize(msg.Width, msg.Height)
		a.workspaceModal.SetSize(msg.Width, msg.Height)
		a.newSession.SetSize(msg.Width, msg.Height)
		a.sessionSearch.SetSize(msg.Width, msg.Height)
		a.settingsPage.SetSize(msg.Width, msg.Height)
		a.sourceItemsOverlay.SetSize(msg.Width, msg.Height)
		a.overviewLinksOverlay.SetSize(msg.Width, msg.Height)
		a.reviewFollowupOverlay.SetSize(msg.Width, msg.Height)
		a.logsOverlay.SetSize(msg.Width, msg.Height)
		a.addRepo.SetSize(msg.Width, msg.Height)
		a.repoManager.SetSize(msg.Width, msg.Height)
		a.worktreePicker.SetSize(msg.Width, msg.Height)
		a.newSessionAutonomousOverlay.SetSize(msg.Width, msg.Height)
		a.actionMenu.SetSize(msg.Width, msg.Height)
		return a, nil

	case WorkspaceHealthCheckMsg:
		if msg.Error != nil && a.activeOverlay != overlayWorkspaceInit {
			// Existing-workspace background scan failure: log and skip.
			// Workspace-init overlay handles its own error display.
			slog.Error("workspace health check failed", "error", msg.Error)
			return a, nil
		}
		if a.activeOverlay == overlayWorkspaceInit {
			a.workspaceModal, cmd = a.workspaceModal.Update(msg)
			cmds = append(cmds, cmd)
		} else if a.hasWorkspace && a.activeOverlay == overlayNone && len(msg.Check.PlainGitClones) > 0 {
			// New uninitialized repos detected in an existing workspace.
			a.workspaceModal = NewNewReposModal(a.runtimeCtx.WorkspaceDir, a.styles, a.provider.GitClient())
			a.workspaceModal.SetSize(a.windowWidth, a.windowHeight)
			a.workspaceModal, cmd = a.workspaceModal.Update(msg)
			cmds = append(cmds, cmd)
			a.activeOverlay = overlayWorkspaceInit
		}
		return a, tea.Batch(cmds...)

	case WorkspaceInitDoneMsg:
		cmds = append(cmds, initializeWorkspaceServicesCmd(
			a.provider,
			a.runtimeCtx,
			msg.WorkspaceID,
			msg.WorkspaceName,
			msg.WorkspaceDir,
		))
		return a, tea.Batch(cmds...)

	case NewReposInitDoneMsg:
		a.workspaceModal, cmd = a.workspaceModal.Update(msg)
		cmds = append(cmds, cmd)
		a.activeOverlay = overlayNone
		a.toasts.AddToast(fmt.Sprintf("%d repo(s) initialized with git-work", msg.Count), components.ToastSuccess)
		return a, tea.Batch(cmds...)

	case StartupIntegrationsStartMsg:
		if a.startupIntegrationsInProgress {
			cmds = append(cmds, StartupIntegrationsCmd(a.provider, a.runtimeCtx))
		}
		return a, tea.Batch(cmds...)

	case SettingsDiagnosticsStartMsg:
		snapshot := a.provider.Settings().Snapshot()
		if snapshot.DiagnosticsState == SettingsDiagnosticsPending {
			cmds = append(cmds, SettingsDiagnosticsCmd(a.provider.Settings(), a.runtimeCtx.Cfg))
		}
		return a, tea.Batch(cmds...)

	case SettingsDiagnosticsReadyMsg:
		if msg.Err != nil {
			slog.Warn("settings diagnostics failed", "error", msg.Err)
		}
		a.settingsPage.RefreshFromService()
		return a, nil

	case WorkspaceServicesReloadedMsg:
		oldWorkspaceID := a.runtimeCtx.WorkspaceID
		a.applyServicesReload(msg.Reload)
		a.activeOverlay = overlayNone
		a.toasts.AddToast(msg.Message, components.ToastSuccess)

		cmds = append(cmds, a.commandsAfterServiceReload(oldWorkspaceID)...)
		return a, tea.Batch(cmds...)

	case StartupIntegrationsReadyMsg:
		a.startupIntegrationsInProgress = false
		a.runtimeCtx.StartupIntegrationsInProgress = false
		a.inputBlocked = false
		if msg.Err != nil {
			slog.Warn("startup integrations failed", "error", msg.Err)
			a.toasts.AddToast("Startup integrations failed", components.ToastWarning)
			// Mark diagnostics as failed so the settings page doesn't show stale "checking" status.
			a.provider.Settings().SetDiagnosticsState(SettingsDiagnosticsFailed)
			return a, nil
		}
		oldWorkspaceID := a.runtimeCtx.WorkspaceID
		a.applyServicesReload(msg.Reload)
		cmds = append(cmds, a.commandsAfterServiceReload(oldWorkspaceID)...)
		for _, warning := range a.provider.StartupWarnings() {
			a.toasts.AddToast(warning, components.ToastWarning)
		}
		return a, tea.Batch(cmds...)

	case QuitRequestMsg:
		return a.handleQuitRequest()

	case QuitConfirmedMsg:
		if err := a.interruptActiveAgentSessions(context.Background()); err != nil {
			slog.Error("failed to interrupt agent sessions during quit", "error", err)
		}
		a.teardownAllPipelines()
		if a.busSub != nil {
			a.provider.Bus().Unsubscribe("tui:" + a.runtimeCtx.WorkspaceID)
		}
		return a, a.quitCmd()

	case WorkspaceCancelMsg:
		return a, tea.Quit

	case CloseOverlayMsg:
		// If add-repo was opened from the repo manager, return there instead of home.
		if a.activeOverlay == overlayAddRepo && a.addRepoOpenedFromRepoManager {
			a.addRepo.Close()
			a.addRepoOpenedFromRepoManager = false
			return a, a.openRepoManager()
		}
		if a.activeOverlay == overlayOverviewLinks && a.overviewLinksReturnOverlay != overlayNone {
			returnOverlay := a.overviewLinksReturnOverlay
			a.overviewLinksReturnOverlay = overlayNone
			a.overviewLinksOverlay.Close()
			a.activeOverlay = returnOverlay
			return a, nil
		}
		a.activeOverlay = overlayNone
		a.addRepoOpenedFromRepoManager = false
		a.newSession.Close()
		a.newSessionAutonomousOverlay.Close()
		a.sessionSearch.Close()
		a.settingsPage.Close()
		a.sourceItemsOverlay.Close()
		a.addRepo.Close()
		a.repoManager.Close()
		a.worktreePicker.Close()
		a.overviewLinksOverlay.Close()
		a.overviewLinksReturnOverlay = overlayNone
		a.reviewFollowupOverlay.Close()
		return a, nil

	case PollTickMsg:
		a.toasts.Prune()
		if a.runtimeCtx.InstanceID != "" {
			cmds = append(cmds, HeartbeatCmd(a.provider.Instance(), a.runtimeCtx.InstanceID))
		}
		if a.activeOverlay == overlaySessionSearch {
			cmds = append(cmds, a.runSessionSearch(false))
		}
		cmds = append(cmds, PollTickCmd())
		return a, tea.Batch(cmds...)

	case DomainEventMsg:
		// Keep the bridge alive: reschedule the channel reader.
		if a.busSub != nil {
			cmds = append(cmds, a.eventConsumer.BridgeCmd())
		}

		// Defensive: ignore events not for this workspace.
		if msg.Event.WorkspaceID != "" && msg.Event.WorkspaceID != a.runtimeCtx.WorkspaceID {
			return a, nil
		}

		if a.eventConsumer != nil {
			if typedMsg := a.eventConsumer.toMsg(msg.Event); typedMsg != nil {
				cmds = append(cmds, func() tea.Msg { return typedMsg })
			}
		}

		switch domain.EventType(msg.Event.EventType) {
		// Work item state changes → targeted load of work item and its tasks
		case domain.EventWorkItemIngested:
			// Handled by WorkItemIngestedMsg below; no action needed here.
		case domain.EventWorkItemPlanning,
			domain.EventWorkItemPlanReview,
			domain.EventWorkItemApproved,
			domain.EventWorkItemImplementing,
			domain.EventWorkItemReviewing,
			domain.EventWorkItemCompleted,
			domain.EventWorkItemFailed,
			domain.EventWorkItemMerged,
			domain.EventWorkItemArchived:
			workItemID := extractWorkItemID(msg.Event.Payload)
			if workItemID != "" {
				cmds = append(cmds,
					LoadSessionCmd(a.provider.Session(), workItemID),
					LoadTasksForSessionCmd(a.provider.Task(), workItemID),
					LoadPlanForSessionCmd(a.provider.Plan(), workItemID),
				)
			}
			if a.currentWorkItemID != "" {
				cmds = append(cmds, a.updateContentFromState())
			}

		// Session lifecycle — new sessions: load work item AND tasks so the sidebar
		// transitions from "Ingested" to "Planning/Implementing" immediately.
		case domain.EventAgentSessionStarted:
			workItemID := extractWorkItemID(msg.Event.Payload)
			if workItemID != "" {
				cmds = append(cmds,
					LoadSessionCmd(a.provider.Session(), workItemID),
					LoadTasksForSessionCmd(a.provider.Task(), workItemID),
				)
			}
			if a.currentWorkItemID != "" {
				cmds = append(cmds, a.updateContentFromState())
			}

		// Session lifecycle — running/stopped sessions: only reload tasks.
		case domain.EventAgentSessionCompleted,
			domain.EventAgentSessionFailed,
			domain.EventAgentSessionInterrupted,
			domain.EventAgentSessionResumed,
			domain.EventAgentSessionFollowUp,
			domain.EventAgentSessionWaitingForAnswer:
			workItemID := extractWorkItemID(msg.Event.Payload)
			if workItemID != "" {
				cmds = append(cmds, LoadTasksForSessionCmd(a.provider.Task(), workItemID))
			}
			if a.currentWorkItemID != "" {
				cmds = append(cmds, a.updateContentFromState())
			}
		case domain.EventPlanGenerated,
			domain.EventPlanApproved,
			domain.EventPlanRejected,
			domain.EventPlanRevised,
			domain.EventPlanSubmitted:
			// Typed handlers (PlanGeneratedMsg, PlanUpdatedMsg) upsert the full plan and sub-plans directly;
			// no targeted DB reload needed.

		// WorkItemImplementing is handled by typed WorkItemUpdatedMsg below

		// Question events → handled by typed messages below
		case domain.EventAgentQuestionRaised,
			domain.EventAgentQuestionAnswered,
			domain.EventReviewStarted,
			domain.EventReviewCompleted,
			domain.EventCritiquesFound,
			domain.EventReimplementationStarted,
			domain.EventAdapterError:
			// Handled by typed message cases below; no action needed here.

			// Higher-level events that don't need targeted reload (all other untyped events)
		}

		return a, tea.Batch(cmds...)

	case HeartbeatTickMsg:
		if a.runtimeCtx.InstanceID != "" {
			cmds = append(cmds, HeartbeatCmd(a.provider.Instance(), a.runtimeCtx.InstanceID))
		}
		cmds = append(cmds, HeartbeatTickCmd())
		return a, tea.Batch(cmds...)

	case components.ToastTickMsg:
		a.toasts.Prune()
		cmds = append(cmds, components.ToastTickCmd())
		return a, tea.Batch(cmds...)

	case StartupIntegrationsSpinnerTickMsg:
		if !a.startupIntegrationsInProgress {
			return a, nil
		}
		a.startupIntegrationSpinner, cmd = a.startupIntegrationSpinner.Update(msg.Tick)
		a.startupIntegrationSpinnerFrameOnly = true
		return a, wrapStartupIntegrationsSpinnerTickCmd(cmd)

	case SessionsLoadedMsg:
		if msg.WorkspaceID != a.runtimeCtx.WorkspaceID {
			return a, nil
		}
		a.workItems = msg.Items
		a.rebuildSidebar()
		a.refreshSessionSearchEntriesFromLocalState()
		// Load plans for all work items so that actions like retry are available
		// immediately, regardless of whether TasksLoadedMsg arrives first.
		for _, wi := range msg.Items {
			cmds = append(cmds, LoadPlanCmd(a.provider.Plan(), wi.ID))
		}
		cmds = append(cmds, a.updateContentFromState())
		return a, tea.Batch(cmds...)

	case TasksLoadedMsg:
		slog.Debug("TasksLoadedMsg received", "workspaceID", msg.WorkspaceID, "sessionCount", len(msg.Sessions))
		if msg.WorkspaceID != a.runtimeCtx.WorkspaceID {
			slog.Debug("TasksLoadedMsg ignored (workspace mismatch)", "msgWorkspaceID", msg.WorkspaceID, "runtimeWorkspaceID", a.runtimeCtx.WorkspaceID)
			return a, nil
		}
		a.sessions = msg.Sessions
		slog.Debug("TasksLoadedMsg processed", "totalSessions", len(a.sessions))
		a.rebuildSidebar()
		a.refreshSessionSearchEntriesFromLocalState()
		for _, wi := range a.workItems {
			cmds = append(cmds, LoadPlanCmd(a.provider.Plan(), wi.ID))
		}
		for _, s := range msg.Sessions {
			if s.Status == domain.AgentSessionWaitingForAnswer {
				cmds = append(cmds, LoadQuestionsCmd(a.provider.Question(), s.ID))
			}
			if s.Status == domain.AgentSessionCompleted {
				cmds = append(cmds, LoadReviewsCmd(a.provider.Review(), s.ID))
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

	case SessionLoadedMsg:
		// Upsert the work item
		if idx := slices.IndexFunc(a.workItems, func(w domain.Session) bool {
			return w.ID == msg.WorkItem.ID
		}); idx >= 0 {
			a.workItems[idx] = msg.WorkItem
		} else {
			a.workItems = append(a.workItems, msg.WorkItem)
		}
		a.rebuildSidebar()
		if a.currentWorkItemID == msg.WorkItem.ID {
			cmds = append(cmds, a.updateContentFromState())
		}
		// Cascade: load tasks and plan for the work item
		cmds = append(cmds,
			LoadTasksForSessionCmd(a.provider.Task(), msg.WorkItem.ID),
			LoadPlanForSessionCmd(a.provider.Plan(), msg.WorkItem.ID),
		)
		return a, tea.Batch(cmds...)

	case TasksForSessionLoadedMsg:
		// Remove old sessions for this work item, add new ones
		filtered := make([]domain.AgentSession, 0, len(a.sessions))
		for _, s := range a.sessions {
			if s.WorkItemID != msg.WorkItemID {
				filtered = append(filtered, s)
			}
		}
		a.sessions = append(filtered, msg.Sessions...)
		slog.Debug("TasksForSessionLoadedMsg processed", "totalSessions", len(a.sessions))
		a.rebuildSidebar()
		if a.currentWorkItemID == msg.WorkItemID {
			cmds = append(cmds, a.updateContentFromState())
		}
		return a, tea.Batch(cmds...)

	case PlanForSessionLoadedMsg:
		if msg.Plan != nil {
			a.plans[msg.WorkItemID] = msg.Plan
			a.subPlans[msg.Plan.ID] = msg.SubPlans
			a.rebuildSidebar()
			a.refreshSessionSearchEntriesFromLocalState()
		}
		if a.currentWorkItemID == msg.WorkItemID {
			cmds = append(cmds, a.updateContentFromState())
		}
		return a, tea.Batch(cmds...)

	case InspectPlanMsg:
		if msg.PlanID != "" {
			return a, LoadPlanByIDCmd(a.provider.Plan(), msg.PlanID)
		}
		return a, nil
	case QuestionsLoadedMsg:
		if a.questions[msg.SessionID] == nil {
			a.questions[msg.SessionID] = make(map[string]domain.Question)
		}
		for _, q := range msg.Questions {
			a.questions[msg.SessionID][q.ID] = q
		}
		a.rebuildSidebar()
		a.refreshSessionSearchEntriesFromLocalState()
		if a.currentWorkItemID != "" {
			cmds = append(cmds, a.updateContentFromState())
		}
		return a, tea.Batch(cmds...)

	// --- Event-driven typed message handlers ---

	case WorkItemIngestedMsg:
		if msg.Session.ID != "" {
			a.upsertWorkItem(msg.Session)
		}
		a.rebuildSidebar()
		a.refreshSessionSearchEntriesFromLocalState()
		if msg.WorkItemID != "" {
			cmds = append(cmds, LoadSessionCmd(a.provider.Session(), msg.WorkItemID))
		}
		if a.currentWorkItemID != "" {
			cmds = append(cmds, a.updateContentFromState())
		}
		return a, tea.Batch(cmds...)

	case WorkItemUpdatedMsg:
		if msg.Session.ID != "" {
			a.upsertWorkItem(msg.Session)
		}
		a.rebuildSidebar()
		a.refreshSessionSearchEntriesFromLocalState()

		// Run completion side effects when work item is completed
		if msg.Session.State == domain.SessionCompleted {
			a.cancelPipeline(msg.WorkItemID)
			a.toasts.AddToast("Work item completed", components.ToastSuccess)
			cmds = append(cmds, EndForemanOrchestratedCmd(a.provider.Implementation(), msg.WorkItemID))
		}
		if msg.Session.State == domain.SessionFailed || msg.Session.State == domain.SessionArchived {
			cmds = append(cmds, EndForemanOrchestratedCmd(a.provider.Implementation(), msg.WorkItemID))
		}

		// Only issue DB loads if the payload lacks data
		if msg.Session.ID == "" && msg.WorkItemID != "" {
			cmds = append(cmds, LoadSessionCmd(a.provider.Session(), msg.WorkItemID))
		}
		if a.currentWorkItemID != "" {
			cmds = append(cmds, a.updateContentFromState())
		}
		return a, tea.Batch(cmds...)

	case PlanGeneratedMsg:
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

	case PlanUpdatedMsg:
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

	case SessionStartedMsg:
		a.sessions = append(a.sessions, msg.AgentSession)
		a.rebuildSidebar()
		a.refreshSessionSearchEntriesFromLocalState()
		if a.currentWorkItemID != "" {
			cmds = append(cmds, a.updateContentFromState())
		}
		return a, tea.Batch(cmds...)

	case SessionUpdatedMsg:
		for i := range a.sessions {
			if a.sessions[i].ID == msg.AgentSession.ID {
				a.sessions[i] = msg.AgentSession
				break
			}
		}
		a.rebuildSidebar()
		a.refreshSessionSearchEntriesFromLocalState()
		if a.currentWorkItemID != "" {
			cmds = append(cmds, a.updateContentFromState())
		}
		return a, tea.Batch(cmds...)

	case TaskStartedMsg:
		a.upsertSession(msg.AgentSession)
		a.rebuildSidebar()
		a.refreshSessionSearchEntriesFromLocalState()
		if a.currentWorkItemID == msg.WorkItemID {
			cmds = append(cmds, a.updateContentFromState())
		}
		return a, tea.Batch(cmds...)

	case TaskUpdatedMsg:
		a.upsertSession(msg.AgentSession)
		a.rebuildSidebar()
		a.rebuildSidebar()
		if a.currentWorkItemID == msg.WorkItemID {
			cmds = append(cmds, a.updateContentFromState())
		}
		return a, tea.Batch(cmds...)

	case SubPlanStatusChangedMsg:
		if msg.SubPlan.ID != "" {
			planID := msg.PlanID
			if planID == "" {
				planID = msg.SubPlan.PlanID
			}
			if planID != "" {
				a.upsertSubPlan(planID, msg.SubPlan)
			} else if msg.WorkItemID != "" {
				cmds = append(cmds, LoadPlanForSessionCmd(a.provider.Plan(), msg.WorkItemID))
			}
		}
		a.rebuildSidebar()
		a.refreshSessionSearchEntriesFromLocalState()
		if a.currentWorkItemID == msg.WorkItemID {
			cmds = append(cmds, a.updateContentFromState())
		}
		return a, tea.Batch(cmds...)

	case SubPlanPRReadyMsg:
		// Refresh session and plan data when a PR/MR becomes ready.
		if msg.WorkItemID != "" {
			cmds = append(cmds, LoadSessionCmd(a.provider.Session(), msg.WorkItemID))
			cmds = append(cmds, LoadPlanForSessionCmd(a.provider.Plan(), msg.WorkItemID))
			cmds = append(cmds, LoadTasksForSessionCmd(a.provider.Task(), msg.WorkItemID))
		}
		a.rebuildSidebar()
		a.refreshSessionSearchEntriesFromLocalState()
		if a.currentWorkItemID == msg.WorkItemID {
			cmds = append(cmds, a.updateContentFromState())
		}
		return a, tea.Batch(cmds...)

	case QuestionRaisedMsg:
		a.upsertQuestion(msg.SessionID, msg.Question)
		a.rebuildSidebar()

		stageLabel := "Question"
		if msg.Question.Stage == domain.AgentSessionKindPlanning {
			stageLabel = "Planning question"
		}
		a.toasts.AddToast(
			fmt.Sprintf("%s: %s", stageLabel, summarizeQuestionText(msg.Question.Content, 60)),
			components.ToastInfo,
		)

		if a.currentWorkItemID == msg.WorkItemID {
			cmds = append(cmds, a.updateContentFromState())
		}
		return a, tea.Batch(cmds...)

	case QuestionAnsweredMsg:
		a.removeQuestion(msg.SessionID, msg.QuestionID)
		a.rebuildSidebar()
		if a.currentWorkItemID != "" {
			cmds = append(cmds, a.updateContentFromState())
		}
		return a, tea.Batch(cmds...)

	case ReviewStartedMsg, ReviewCompletedMsg, CritiquesFoundMsg, ReimplementationStartedMsg:
		sessionID := extractReviewSessionID(msg)
		cmds = append(cmds, LoadReviewsCmd(a.provider.Review(), sessionID))
		if a.currentWorkItemID != "" {
			cmds = append(cmds, a.updateContentFromState())
		}
		return a, tea.Batch(cmds...)

	case AdapterErrorMsg:
		a.toasts.AddToast(fmt.Sprintf("Adapter error (%s): %v", msg.Adapter, msg.Err), components.ToastWarning)
		return a, nil

	// ImplementationStartedMsg removed — use EventWorkItemImplementing via WorkItemUpdatedMsg

	case PRReviewStateChangedMsg:
		if msg.WorkItemID != "" {
			cmds = append(cmds, LoadSessionCmd(a.provider.Session(), msg.WorkItemID))
		}
		if a.currentWorkItemID != "" {
			cmds = append(cmds, a.updateContentFromState())
		}
		return a, tea.Batch(cmds...)

	case PRMergedMsg:
		if msg.WorkItemID != "" {
			cmds = append(cmds,
				LoadSessionCmd(a.provider.Session(), msg.WorkItemID),
				LoadTasksForSessionCmd(a.provider.Task(), msg.WorkItemID),
			)
		}
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
		cmds = append(cmds, SaveReviewedPlanCmd(a.provider.Planning(), msg.PlanID, msg.NewContent))
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

	case ConfirmInterruptSessionsMsg:
		ids := append([]string(nil), msg.SessionIDs...)
		if len(ids) == 0 {
			return a, nil
		}
		title := "Interrupt agent session?"
		body := "This will stop the harness process, save resume state, and leave the session resumable."
		if len(ids) > 1 {
			title = "Interrupt agent sessions?"
			body = fmt.Sprintf("This will stop %d harness processes, save resume state, and leave the sessions resumable.", len(ids))
		}
		a.showConfirm(title, body, func() tea.Msg { return InterruptSessionsMsg{SessionIDs: ids} })
		return a, nil

	case InterruptSessionsMsg:
		ids := append([]string(nil), msg.SessionIDs...)
		// Snapshot sessions before spawning goroutine to avoid racing with concurrent
		// event-loop writes (upsertSession, SessionsLoadedMsg, etc.).
		sessions := append([]domain.AgentSession(nil), a.sessions...)
		// Cancel pipelines synchronously to avoid racing with the event loop.
		for _, id := range ids {
			for _, session := range sessions {
				if session.ID == id && isInterruptibleAgentSession(session) {
					a.cancelPipeline(session.WorkItemID)
				}
			}
		}
		cmds = append(cmds, func() tea.Msg {
			go func() {
				err := a.interruptAgentSessionsByID(context.Background(), ids, sessions)
				a.program.Send(SessionsInterruptedMsg{SessionIDs: ids, Err: err})
			}()
			return nil
		})
		return a, tea.Batch(cmds...)

	case SessionsInterruptedMsg:
		if msg.Err != nil {
			a.toasts.AddToast(fmt.Sprintf("Interrupt failed: %v", msg.Err), components.ToastError)
			return a, nil
		}
		for i := range a.sessions {
			for _, id := range msg.SessionIDs {
				if a.sessions[i].ID == id {
					a.sessions[i].Status = domain.AgentSessionInterrupted
				}
			}
		}
		a.rebuildSidebar()
		cmds = append(cmds, a.updateContentFromState())
		n := len(msg.SessionIDs)
		toastMsg := "Agent session interrupted"
		if n > 1 {
			toastMsg = fmt.Sprintf("%d agent sessions interrupted", n)
		}
		a.toasts.AddToast(toastMsg, components.ToastSuccess)
		return a, tea.Batch(cmds...)

	case ConfirmDeleteSessionMsg:
		a.showDeleteSessionConfirm(msg.SessionID)
		return a, nil

	case ConfirmArchiveMsg:
		a.showArchiveConfirm(msg.WorkItemID)
		return a, nil

	case ConfirmUnarchiveMsg:
		a.showUnarchiveConfirm(msg.WorkItemID)
		return a, nil

	case ConfirmOverrideAcceptMsg:
		a.showConfirm("Override Accept",
			"Accept this work item despite outstanding critiques? This cannot be undone.",
			func() tea.Msg { return OverrideAcceptMsg(msg) },
		)
		return a, nil

	case StartPlanMsg:
		if a.provider.Planning() != nil {
			cmds = append(cmds, StartPlanningCmd(a.registerPipelineCancel(msg.WorkItemID), a.provider.Planning(), msg.WorkItemID))
		} else {
			a.toasts.AddToast("Planning service not configured", components.ToastError)
		}
		return a, tea.Batch(cmds...)

	case RestartPlanMsg:
		if a.provider.Planning() != nil && a.provider.Session() != nil {
			cmds = append(cmds, RestartPlanningCmd(a.registerPipelineCancel(msg.WorkItemID), a.provider.Session(), a.provider.Planning(), a.provider.Task(), msg.WorkItemID, msg.Prompt))
		} else {
			a.toasts.AddToast("Planning service not configured", components.ToastError)
		}
		return a, tea.Batch(cmds...)

	case PlanApproveMsg:
		cmds = append(cmds, ApprovePlanCmd(a.provider.Session(), a.provider.Plan(), a.runtimeCtx.Cfg, a.provider.Bus(), msg.PlanID, msg.WorkItemID))
		return a, tea.Batch(cmds...)

	case PlanApprovedMsg:
		if a.provider.Implementation() != nil {
			cmds = append(cmds, RunImplementationCmd(a.registerPipelineCancel(msg.WorkItemID), a.provider.Implementation(), msg.PlanID))
		}
		// Start foreman for the work item
		cmds = append(cmds, BeginForemanOrchestratedCmd(a.provider.Implementation(), msg.WorkItemID, msg.PlanID))
		return a, tea.Batch(cmds...)

	case PlanRequestChangesMsg:
		if a.provider.Planning() != nil {
			cmds = append(cmds, PlanWithFeedbackCmd(a.registerPipelineCancel(a.currentWorkItemID), a.provider.Planning(), a.currentWorkItemID, msg.PlanID, msg.Feedback))
		} else {
			a.toasts.AddToast("Plan revision requested (no planning service)", components.ToastInfo)
		}
		return a, tea.Batch(cmds...)

	case AnswerQuestionMsg:
		cmds = append(cmds, AnswerQuestionCmd(a.provider.AnswerRouter(), msg.QuestionID, msg.Answer, msg.AnsweredBy))
		return a, tea.Batch(cmds...)

	case SteerSessionMsg:
		if a.provider.SessionRegistry() != nil && msg.SessionID != "" && msg.Message != "" {
			cmds = append(cmds, SteerSessionCmd(a.provider.SessionRegistry(), msg.SessionID, msg.Message))
		}
		return a, tea.Batch(cmds...)

	case SteerSessionSentMsg:
		a.toasts.AddToast("Steering prompt sent", components.ToastSuccess)
		return a, nil

	case FollowUpSessionMsg:
		if a.provider.Resumption() != nil && a.provider.Task() != nil && msg.TaskID != "" && msg.Feedback != "" {
			// Find workItemID for this task
			workItemID := ""
			for _, s := range a.sessions {
				if s.ID == msg.TaskID {
					workItemID = s.WorkItemID
					break
				}
			}
			if workItemID != "" {
				ctx := a.pipelineCtxForTask(msg.TaskID)
				cmds = append(cmds, FollowUpSessionCmd(ctx, a.provider.Resumption(), a.provider.Task(), msg.TaskID, msg.Feedback, a.runtimeCtx.InstanceID))
				// Restart foreman with follow-up context
				cmds = append(cmds, FollowUpOrchestratedCmd(a.provider.ReviewFollowup(), workItemID, msg.Feedback))
			}
			a.toasts.AddToast("Follow-up session started", components.ToastSuccess)
		}
		return a, tea.Batch(cmds...)

	case FollowUpFailedSessionMsg:
		if a.provider.Resumption() != nil && a.provider.Task() != nil && msg.TaskID != "" && msg.Feedback != "" {
			// Find workItemID for this task
			workItemID := ""
			for _, s := range a.sessions {
				if s.ID == msg.TaskID {
					workItemID = s.WorkItemID
					break
				}
			}
			if workItemID != "" {
				ctx := a.pipelineCtxForTask(msg.TaskID)
				cmds = append(cmds, FollowUpFailedSessionCmd(ctx, a.provider.Resumption(), a.provider.Task(), msg.TaskID, msg.Feedback, a.runtimeCtx.InstanceID))
				// Restart foreman with failed follow-up context
				cmds = append(cmds, FollowUpFailedOrchestratedCmd(a.provider.ReviewFollowup(), workItemID, msg.Feedback))
			}
			a.toasts.AddToast("Follow-up session started for failed task", components.ToastSuccess)
		}
		return a, tea.Batch(cmds...)

	case FollowUpSessionCompleteMsg:
		a.cancelPipeline(msg.WorkItemID)
		a.toasts.AddToast("Follow-up session complete", components.ToastSuccess)
		// Reload tasks for the specific work item
		cmds = append(cmds, LoadTasksForSessionCmd(a.provider.Task(), msg.WorkItemID))
		return a, tea.Batch(cmds...)

	case FollowUpPlanMsg:
		if a.provider.Planning() == nil {
			return a, nil
		}
		return a, FollowUpPlanCmd(a.registerPipelineCancel(msg.WorkItemID), a.provider.Planning(), msg.WorkItemID, msg.Feedback)

	case FollowUpPlanResultMsg:
		if msg.Err != nil {
			a.toasts.AddToast(fmt.Sprintf("Follow-up planning failed: %v", msg.Err), components.ToastError)
			return a, nil
		}
		a.toasts.AddToast("Follow-up plan ready for review", components.ToastSuccess)
		// Reload the session and plan for the work item
		cmds = append(cmds,
			LoadSessionCmd(a.provider.Session(), msg.WorkItemID),
			LoadPlanForSessionCmd(a.provider.Plan(), msg.WorkItemID),
		)
		return a, tea.Batch(cmds...)

	case FetchReviewCommentsMsg:
		if a.provider.ReviewComments() == nil || len(msg.Items) == 0 {
			return a, nil
		}
		a.activeOverlay = overlayReviewFollowup
		a.reviewFollowupOverlay.SetSize(a.windowWidth, a.windowHeight)
		gen, spinnerCmd := a.reviewFollowupOverlay.OpenLoading(msg.WorkItemID, msg.Items)
		return a, tea.Batch(spinnerCmd, FetchReviewCommentsCmd(a.provider.ReviewComments(), msg.WorkItemID, msg.Items, "", gen))

	case ReviewCommentsFetchedMsg:
		// Drop results from a previous overlay session (user cancelled or reopened).
		if msg.Generation != a.reviewFollowupOverlay.Generation() || !a.reviewFollowupOverlay.Active() {
			return a, nil
		}
		if msg.Err != nil && len(msg.Result) == 0 {
			a.toasts.AddToast(fmt.Sprintf("Failed to fetch review comments: %v", msg.Err), components.ToastError)
			a.activeOverlay = overlayNone
			a.reviewFollowupOverlay.Close()
			return a, nil
		}
		if msg.Err != nil {
			a.toasts.AddToast(fmt.Sprintf("Some review comments unavailable: %v", msg.Err), components.ToastWarning)
		}
		if keep := a.reviewFollowupOverlay.ApplyFetchResult(msg.Result, msg.FetchedAt); !keep {
			a.toasts.AddToast("No outstanding review comments", components.ToastInfo)
			a.activeOverlay = overlayNone
			a.reviewFollowupOverlay.Close()
		}
		return a, nil

	case ReviewFollowupRefetchMsg:
		if a.provider.ReviewComments() == nil {
			return a, nil
		}
		return a, FetchReviewCommentsCmd(a.provider.ReviewComments(), msg.WorkItemID, msg.Items, msg.Mode, a.reviewFollowupOverlay.Generation())

	case ReviewCommentsRefetchedMsg:
		// Drop refetch results from a previous overlay session.
		if msg.Generation != a.reviewFollowupOverlay.Generation() || !a.reviewFollowupOverlay.Active() {
			return a, nil
		}
		if msg.Err != nil && len(msg.Result) == 0 {
			a.toasts.AddToast(fmt.Sprintf("Failed to refresh review comments: %v", msg.Err), components.ToastError)
			a.activeOverlay = overlayNone
			a.reviewFollowupOverlay.Close()
			return a, nil
		}
		if msg.Err != nil {
			a.toasts.AddToast(fmt.Sprintf("Some review comments unavailable: %v", msg.Err), components.ToastWarning)
		}
		dropped := a.reviewFollowupOverlay.MergeRefetch(msg.Result, msg.FetchedAt)
		if dropped > 0 {
			a.toasts.AddToast(fmt.Sprintf("%d selected comment(s) no longer available", dropped), components.ToastWarning)
		}
		// Resume dispatch via the requested mode.
		switch msg.Mode {
		case "address":
			perRepo := a.reviewFollowupOverlay.FormatPerRepo()
			return a, func() tea.Msg {
				return FollowUpFromReviewAddressMsg{WorkItemID: msg.WorkItemID, PerRepo: perRepo}
			}
		case "replan":
			feedback := a.reviewFollowupOverlay.FormatAllSelected()
			return a, func() tea.Msg {
				return FollowUpFromReviewReplanMsg{WorkItemID: msg.WorkItemID, Feedback: feedback}
			}
		}
		return a, nil

	case ReviewFollowupCancelMsg:
		a.activeOverlay = overlayNone
		a.reviewFollowupOverlay.Close()
		return a, nil

	case FollowUpFromReviewAddressMsg:
		a.activeOverlay = overlayNone
		a.reviewFollowupOverlay.Close()
		return a, a.dispatchReviewAddress(msg)

	case ReviewAddressDispatchResultMsg:
		return a, a.applyReviewAddressDispatchResult(msg)

	case FollowUpFromReviewReplanMsg:
		a.activeOverlay = overlayNone
		a.reviewFollowupOverlay.Close()
		if a.provider.Planning() == nil || strings.TrimSpace(msg.Feedback) == "" {
			a.toasts.AddToast("Re-plan not dispatched: missing planning service or feedback", components.ToastWarning)
			return a, nil
		}
		return a, FollowUpPlanCmd(a.registerPipelineCancel(msg.WorkItemID), a.provider.Planning(), msg.WorkItemID, msg.Feedback)

	case SkipQuestionMsg:
		cmds = append(cmds, SkipQuestionCmd(a.provider.AnswerRouter(), msg.QuestionID))
		return a, tea.Batch(cmds...)

	case ResumeSessionMsg:
		if a.provider.Resumption() != nil {
			// Restart foreman with current plan if it exists
			if plan := a.plans[msg.WorkItemID]; plan != nil {
				cmds = append(cmds, RestartForemanWithPlanOrchestratedCmd(a.provider.Implementation(), msg.WorkItemID, plan.ID))
			}
			cmds = append(cmds, ResumeAllSessionsForWorkItemCmd(
				context.Background(),
				a.provider.Session(),
				a.provider.Planning(),
				a.provider.Resumption(),
				a.provider.Task(),
				a.provider.Plan(),
				a.provider.Implementation(),
				msg.WorkItemID,
				a.runtimeCtx.InstanceID,
			))
		} else {
			a.toasts.AddToast("Resume not available (no resumption service)", components.ToastError)
		}
		return a, tea.Batch(cmds...)

	case AbandonSessionMsg:
		cmds = append(cmds, abandonSessionCmd(a.provider.Task(), msg.SessionID))
		return a, tea.Batch(cmds...)

	case DeleteSessionMsg:
		// Cancel the orchestrator goroutine (implementation/planning) for this
		// work item. This cascades through executeWave → executeSubPlan → Wait
		// → Abort on every running agent session owned by the pipeline.
		a.cancelPipeline(msg.SessionID)

		// Stop the Foreman for this work item.
		cmds = append(cmds, EndForemanOrchestratedCmd(a.provider.Implementation(), msg.SessionID))

		// Abort any remaining running sessions via the registry. This covers
		// resumed and follow-up sessions that use fire-and-forget goroutines
		// without a stored cancel handle. AbortAndDeregister is idempotent —
		// sessions already torn down by the context cancel above are a no-op.
		if a.provider.SessionRegistry() != nil {
			for _, agentSession := range a.sessions {
				if agentSession.WorkItemID == msg.SessionID {
					a.provider.SessionRegistry().AbortAndDeregister(context.Background(), agentSession.ID)
				}
			}
		}

		cmds = append(cmds, deleteSessionCmd(a.provider, a.sessionsDir, msg.SessionID, a.reviewSessionLogs))
		return a, tea.Batch(cmds...)

	case ArchiveSessionMsg:
		focusAfterArchive := a.currentWorkItemID == msg.WorkItemID || a.currentHistoryEntry.WorkItemID == msg.WorkItemID
		focusWorkItemID := ""
		if focusAfterArchive {
			focusWorkItemID = a.workItemFocusTargetAfterRemoval(msg.WorkItemID)
		}
		cmds = append(cmds, archiveSessionCmd(a.provider.Session(), msg.WorkItemID, focusAfterArchive, focusWorkItemID))
		return a, tea.Batch(cmds...)

	case UnarchiveSessionMsg:
		cmds = append(cmds, unarchiveSessionCmd(a.provider.Session(), msg.WorkItemID))
		return a, tea.Batch(cmds...)

	case SessionArchivedMsg:
		updated, err := a.provider.Session().Get(context.Background(), msg.WorkItemID)
		if err != nil {
			slog.Error("failed to fetch archived work item", "error", err, "work_item_id", msg.WorkItemID)
		} else {
			a.upsertWorkItem(updated)
			if msg.FocusAfterArchive {
				a.currentWorkItemID = msg.FocusWorkItemID
				a.currentHistorySessionID = ""
				a.currentHistoryEntry = SidebarEntry{}
				a.sidebarMode = sidebarPaneSessions
				a.mainFocus = mainFocusSidebar
			}
			a.rebuildSidebar()
			a.refreshSessionSearchEntriesFromLocalState()
			if msg.FocusAfterArchive || a.currentWorkItemID == msg.WorkItemID {
				cmds = append(cmds, a.updateContentFromState())
			}
		}
		a.toasts.AddToast(msg.Message, components.ToastSuccess)
		return a, tea.Batch(cmds...)

	case SessionUnarchivedMsg:
		updated, err := a.provider.Session().Get(context.Background(), msg.WorkItemID)
		if err != nil {
			slog.Error("failed to fetch unarchived work item", "error", err, "work_item_id", msg.WorkItemID)
		} else {
			a.upsertWorkItem(updated)
			a.rebuildSidebar()
			a.refreshSessionSearchEntriesFromLocalState()
			if a.currentWorkItemID == msg.WorkItemID {
				cmds = append(cmds, a.updateContentFromState())
			}
		}
		a.toasts.AddToast(msg.Message, components.ToastSuccess)
		return a, tea.Batch(cmds...)

	case ReimplementMsg:
		if a.provider.Implementation() != nil {
			if plan := a.plans[msg.WorkItemID]; plan != nil {
				cmds = append(cmds, RunImplementationCmd(a.registerPipelineCancel(msg.WorkItemID), a.provider.Implementation(), plan.ID))
				// Restart foreman with new plan
				cmds = append(cmds, RestartForemanWithPlanOrchestratedCmd(a.provider.Implementation(), msg.WorkItemID, plan.ID))
			} else {
				a.toasts.AddToast("Plan not found for re-implementation", components.ToastError)
			}
		} else {
			a.toasts.AddToast("Implementation service not configured", components.ToastError)
		}
		return a, tea.Batch(cmds...)

	case RetryFailedMsg:
		a.toasts.AddToast("Retrying failed or interrupted repos...", components.ToastInfo)
		if a.provider.Implementation() != nil {
			if plan := a.plans[msg.WorkItemID]; plan != nil {
				cmds = append(cmds, RetryFailedCmd(a.registerPipelineCancel(msg.WorkItemID), a.provider.Session(), a.provider.Implementation(), plan.ID, msg.WorkItemID))
				// Restart foreman with new plan
				cmds = append(cmds, RestartForemanWithPlanOrchestratedCmd(a.provider.Implementation(), msg.WorkItemID, plan.ID))
			} else {
				a.toasts.AddToast("Plan not found for retry", components.ToastError)
			}
		} else {
			a.toasts.AddToast("Implementation service not configured", components.ToastError)
		}
		return a, tea.Batch(cmds...)

	case FinalizeWorkItemMsg:
		a.toasts.AddToast("Finalizing completed work item...", components.ToastInfo)
		if a.provider.Implementation() != nil {
			cmds = append(cmds, FinalizeWorkItemCmd(a.registerPipelineCancel(msg.WorkItemID), a.provider.Implementation(), msg.WorkItemID))
		} else {
			a.toasts.AddToast("Implementation service not configured", components.ToastError)
		}
		return a, tea.Batch(cmds...)

	case OverrideAcceptMsg:
		cmds = append(cmds, OverrideAcceptCmd(a.provider.Session(), msg.WorkItemID))
		return a, tea.Batch(cmds...)

	case LoadNewSessionFiltersMsg:
		if strings.TrimSpace(msg.WorkspaceID) == "" {
			return a, nil
		}
		return a, LoadNewSessionFiltersCmd(a.provider.NewSessionFilters(), msg.WorkspaceID)

	case NewSessionFiltersLoadedMsg:
		if msg.WorkspaceID != a.runtimeCtx.WorkspaceID {
			return a, nil
		}
		a.savedNewSessionFilters = append([]domain.NewSessionFilter(nil), msg.Filters...)
		a.syncNewSessionFilterOverlays()
		return a, nil

	case DeleteNewSessionFilterMsg:
		if strings.TrimSpace(msg.WorkspaceID) == "" || msg.WorkspaceID != a.runtimeCtx.WorkspaceID {
			return a, nil
		}
		return a, DeleteNewSessionFilterCmd(a.provider.NewSessionFilters(), msg)

	case NewSessionFilterDeletedMsg:
		if strings.TrimSpace(msg.Message) != "" {
			a.toasts.AddToast(msg.Message, components.ToastSuccess)
		}
		if a.runtimeCtx.WorkspaceID == "" {
			return a, nil
		}
		return a, LoadNewSessionFiltersCmd(a.provider.NewSessionFilters(), a.runtimeCtx.WorkspaceID)

	case SaveNewSessionFilterMsg:
		return a, SaveNewSessionFilterCmd(a.provider.NewSessionFilters(), msg)

	case NewSessionFilterSavedMsg:
		a.toasts.AddToast(msg.Message, components.ToastSuccess)
		if a.runtimeCtx.WorkspaceID == "" {
			return a, nil
		}
		return a, LoadNewSessionFiltersCmd(a.provider.NewSessionFilters(), a.runtimeCtx.WorkspaceID)

	case StartNewSessionAutonomousModeMsg:
		if a.newSessionAutonomous != nil {
			a.toasts.AddToast("New Session autonomous mode is already running", components.ToastInfo)
			return a, nil
		}
		return a, StartNewSessionAutonomousModeCmd(
			a.runtimeCtx.WorkspaceID,
			a.runtimeCtx.InstanceID,
			a.provider.NewSessionFilterLocks(),
			a.provider.Adapters(),
			a.savedNewSessionFilters,
			msg.SelectedFilterIDs,
		)

	case StopNewSessionAutonomousModeMsg:
		runtime := msg.Runtime
		if runtime == nil {
			runtime = a.newSessionAutonomous
		}
		if runtime == nil {
			a.toasts.AddToast("New Session autonomous mode is not running", components.ToastInfo)
			return a, nil
		}
		return a, StopNewSessionAutonomousModeCmd(runtime)

	case NewSessionAutonomousStartedMsg:
		a.newSessionAutonomous = msg.Runtime
		a.newSessionAutonomousChan = msg.Events
		a.syncNewSessionFilterOverlays()
		if message := strings.TrimSpace(msg.Message); message != "" {
			a.toasts.AddToast(message, components.ToastSuccess)
		}
		return a, WaitForNewSessionAutonomousEventCmd(a.newSessionAutonomousChan)

	case NewSessionAutonomousStoppedMsg:
		wasRunning := a.newSessionAutonomous != nil || a.newSessionAutonomousChan != nil
		a.newSessionAutonomous = nil
		a.newSessionAutonomousChan = nil
		a.syncNewSessionFilterOverlays()
		if message := strings.TrimSpace(msg.Message); message != "" {
			if !(message == autonomousLifecycleStoppedToast && !wasRunning) {
				a.toasts.AddToast(message, components.ToastInfo)
			}
		}
		return a, nil

	case NewSessionAutonomousStatusMsg:
		level := components.ToastInfo
		switch strings.ToLower(strings.TrimSpace(msg.Level)) {
		case "error":
			level = components.ToastError
		case "warning", "warn":
			level = components.ToastWarning
		}
		if message := strings.TrimSpace(msg.Message); message != "" && !shouldSuppressAutonomousStatusToast(level, message) {
			a.toasts.AddToast(message, level)
		}
		if a.newSessionAutonomousChan != nil {
			cmds = append(cmds, WaitForNewSessionAutonomousEventCmd(a.newSessionAutonomousChan))
		}
		return a, tea.Batch(cmds...)

	case NewSessionAutonomousDetectedWorkItemMsg:
		cmds = append(cmds, func() tea.Msg { return persistCreatedWorkItemMsg(a.provider, msg.WorkItem) })
		if a.newSessionAutonomousChan != nil {
			cmds = append(cmds, WaitForNewSessionAutonomousEventCmd(a.newSessionAutonomousChan))
		}
		return a, tea.Batch(cmds...)

	case NewSessionManualMsg:
		a.activeOverlay = overlayNone
		a.newSession.Close()
		cmds = append(cmds, createManualSessionCmd(a.provider, msg))
		return a, tea.Batch(cmds...)

	case NewSessionBrowseMsg:
		a.activeOverlay = overlayNone
		a.newSession.Close()
		cmds = append(cmds, createBrowseSessionCmd(a.provider, msg))
		return a, tea.Batch(cmds...)

	case RepoClonedMsg:
		if msg.Err != nil {
			cmds = append(cmds, func() tea.Msg { return ErrMsg{Err: msg.Err} })
		} else {
			cmds = append(cmds, func() tea.Msg { return ActionDoneMsg{Message: "Repository cloned to workspace"} })
			// Trigger workspace rescan
			cmds = append(cmds, LoadSessionsCmd(a.provider.Session(), a.runtimeCtx.WorkspaceID))
			// Update the in-memory slug set so a re-opened add-repo overlay reflects the new repo.
			if a.pendingCloneSlug != "" {
				if a.managedRepoSlugs == nil {
					a.managedRepoSlugs = make(map[string]bool)
				}
				a.managedRepoSlugs[a.pendingCloneSlug] = true
			}
		}
		a.pendingCloneSlug = ""
		return a, tea.Batch(cmds...)

	case AddRepoCloneMsg:
		a.activeOverlay = overlayNone
		a.addRepo.Close()
		a.addRepoOpenedFromRepoManager = false
		// Track the slug so we can update managedRepoSlugs when the clone succeeds.
		if msg.Repo.FullName != "" {
			a.pendingCloneSlug = strings.ToLower(msg.Repo.FullName)
		}
		cmds = append(cmds, CloneRepoCmd(a.provider.GitClient(), msg.CloneDir, msg.CloneURL))
		return a, tea.Batch(cmds...)

	case ShowAddRepoMsg:
		a.repoManager.Close()
		a.addRepoOpenedFromRepoManager = true
		return a, a.openAddRepo()

	case OpenWorktreePickerMsg:
		a.activeOverlay = overlayWorktreePicker
		a.worktreePicker.SetSize(a.windowWidth, a.windowHeight)
		return a, a.worktreePicker.Open()

	case OpenTerminalInWorktreeMsg:
		a.worktreePicker.Close()
		a.activeOverlay = overlayNone
		return a, OpenTerminalCmd(msg.WorktreePath)

	case ManagedReposLoadedMsg:
		// Rebuild the workspace slug set from the fresh scan result.
		if msg.Err == nil {
			slugs := make(map[string]bool, len(msg.Repos))
			for _, r := range msg.Repos {
				if slug := repoSlugFromURL(r.RemoteURL); slug != "" {
					slugs[slug] = true
				}
			}
			a.managedRepoSlugs = slugs
			// If the add-repo overlay is open, push the fresh set to it immediately.
			if a.activeOverlay == overlayAddRepo {
				a.addRepo.SetPresentSlugs(a.managedRepoSlugs)
			}
		}
		// Route to the active overlay that cares about managed repos.
		switch a.activeOverlay {
		case overlayWorktreePicker:
			a.worktreePicker, cmd = a.worktreePicker.Update(msg)
			cmds = append(cmds, cmd)
		default:
			a.repoManager, cmd = a.repoManager.Update(msg)
			cmds = append(cmds, cmd)
		}
		return a, tea.Batch(cmds...)

	case WorktreesLoadedMsg:
		switch msg.Target {
		case WorktreeLoadTargetPicker:
			a.worktreePicker, cmd = a.worktreePicker.Update(msg)
		default:
			a.repoManager, cmd = a.repoManager.Update(msg)
		}
		cmds = append(cmds, cmd)
		return a, tea.Batch(cmds...)

	case RepoRemovedMsg:
		a.repoManager, cmd = a.repoManager.Update(msg)
		cmds = append(cmds, cmd)
		if msg.Err != nil {
			slog.Error("failed to remove repository", "path", msg.RepoPath, "error", msg.Err)
			a.toasts.AddToast("Failed to delete repository: "+msg.Err.Error(), components.ToastError)
		} else {
			a.toasts.AddToast("Repository deleted: "+filepath.Base(msg.RepoPath), components.ToastSuccess)
		}
		return a, tea.Batch(cmds...)

	case RepoInitializedMsg:
		a.repoManager, cmd = a.repoManager.Update(msg)
		cmds = append(cmds, cmd)
		if msg.Err != nil {
			slog.Error("failed to initialize repository", "path", msg.RepoPath, "error", msg.Err)
			a.toasts.AddToast("Failed to initialize repository: "+msg.Err.Error(), components.ToastError)
		} else {
			a.toasts.AddToast("Repository initialized: "+filepath.Base(msg.RepoPath), components.ToastSuccess)
		}
		return a, tea.Batch(cmds...)

	case SettingsAppliedMsg:
		a.applyServicesReload(msg.Reload)
		a.toasts.AddToast(msg.Message, components.ToastSuccess)
		if a.activeOverlay == overlaySettings {
			a.settingsPage, cmd = a.settingsPage.Update(msg, *a.provider.GetServices())
			cmds = append(cmds, cmd)
		}
		if a.runtimeCtx.WorkspaceID != "" {
			cmds = append(cmds, LoadNewSessionFiltersCmd(a.provider.NewSessionFilters(), a.runtimeCtx.WorkspaceID))
		}
		return a, tea.Batch(cmds...)
	case SettingsProviderTestedMsg:
		if a.activeOverlay == overlaySettings {
			a.settingsPage, cmd = a.settingsPage.Update(msg, *a.provider.GetServices())
			cmds = append(cmds, cmd)
		}
		if msg.Err != nil {
			slog.Error("provider test failed",
				"provider", msg.Provider,
				"error", msg.Err,
			)
			a.toasts.AddToast("Error: "+msg.Err.Error(), components.ToastError)
		} else {
			a.toasts.AddToast(msg.Provider+" connection verified", components.ToastSuccess)
		}
		return a, tea.Batch(cmds...)
	case SettingsLoginCompletedMsg:
		if a.activeOverlay == overlaySettings {
			a.settingsPage, cmd = a.settingsPage.Update(msg, *a.provider.GetServices())
			cmds = append(cmds, cmd)
		}
		a.toasts.AddToast(msg.Message, components.ToastSuccess)
		return a, tea.Batch(cmds...)

	case SessionCreatedMsg:
		a.toasts.AddToast(msg.Message, components.ToastSuccess)
		if a.provider.Planning() != nil {
			msg.Session.State = domain.SessionPlanning
		}
		a.upsertWorkItem(msg.Session)
		cmds = append(cmds, a.focusWorkItemOverview(msg.Session.ID))
		if a.provider.Planning() != nil {
			cmds = append(cmds, StartPlanningCmd(a.registerPipelineCancel(msg.Session.ID), a.provider.Planning(), msg.Session.ID))
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
			if a.provider.Planning() != nil && existing.State == domain.SessionIngested {
				existing.State = domain.SessionPlanning
			}
			a.upsertWorkItem(existing)
			cmds = append(cmds, a.focusWorkItemOverview(existing.ID))
			if a.provider.Planning() != nil {
				cmds = append(cmds, StartPlanningCmd(a.registerPipelineCancel(existing.ID), a.provider.Planning(), existing.ID))
			} else {
				a.toasts.AddToast("Planning service not configured", components.ToastError)
			}
			return a, tea.Batch(cmds...)
		default:
			return a, nil
		}

	case SessionResumedMsg:
		if msg.Message != "" {
			a.toasts.AddToast(msg.Message, components.ToastSuccess)
		}
		if msg.AgentSession.ID != "" {
			a.upsertSession(msg.AgentSession)
			a.rebuildSidebar()
			a.refreshSessionSearchEntriesFromLocalState()
		} else if msg.WorkItemID != "" {
			// Fallback: targeted reload when full payload is not available.
			cmds = append(cmds, LoadTasksForSessionCmd(a.provider.Task(), msg.WorkItemID))
		} else if a.runtimeCtx.WorkspaceID != "" {
			// Command-driven: full reload when no routing info available.
			cmds = append(cmds,
				LoadSessionsCmd(a.provider.Session(), a.runtimeCtx.WorkspaceID),
				LoadTasksCmd(a.provider.Task(), a.runtimeCtx.WorkspaceID),
			)
		}
		if a.currentWorkItemID != "" {
			cmds = append(cmds, a.updateContentFromState())
		}
		return a, tea.Batch(cmds...)

	case PlanningRestartedMsg:
		a.toasts.AddToast(msg.Message, components.ToastSuccess)
		cmds = append(cmds,
			LoadSessionCmd(a.provider.Session(), msg.WorkItemID),
			LoadTasksForSessionCmd(a.provider.Task(), msg.WorkItemID),
			LoadPlanForSessionCmd(a.provider.Plan(), msg.WorkItemID),
		)
		if a.currentWorkItemID != "" {
			cmds = append(cmds, a.updateContentFromState())
		}
		return a, tea.Batch(cmds...)
	case ActionDoneMsg:
		a.toasts.AddToast(msg.Message, components.ToastSuccess)
		return a, nil

	case OpenExternalURLMsg:
		return a, OpenBrowserCmd(msg.URL)

	case OpenOverviewLinksMsg:
		if a.activeOverlay != overlayOverviewLinks {
			a.overviewLinksReturnOverlay = a.activeOverlay
		}
		a.activeOverlay = overlayOverviewLinks
		a.overviewLinksOverlay.Open(msg.Sources, msg.Reviews)
		return a, nil

	case OpenArtifactLinksMsg:
		a.activeOverlay = overlayOverviewLinks
		a.overviewLinksOverlay.OpenFromArtifacts(msg.Items)
		return a, nil

	case openSourceItemURLsMsg:
		a.activeOverlay = overlayNone
		a.sourceItemsOverlay.Close()
		var browserCmds []tea.Cmd
		for _, url := range msg.URLs {
			browserCmds = append(browserCmds, OpenBrowserCmd(url))
		}
		return a, tea.Batch(browserCmds...)

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
		for _, agentSession := range a.sessions {
			if _, ok := taskIDSet[agentSession.ID]; ok {
				continue
			}
			filteredTasks = append(filteredTasks, agentSession)
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
		if a.runtimeCtx.WorkspaceID != "" {
			cmds = append(cmds,
				LoadSessionsCmd(a.provider.Session(), a.runtimeCtx.WorkspaceID),
				LoadTasksCmd(a.provider.Task(), a.runtimeCtx.WorkspaceID),
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
		// Route workspace init errors to the modal for cleanup (overlay close + progress reset).
		if a.activeOverlay == overlayWorkspaceInit {
			a.workspaceModal, cmd = a.workspaceModal.Update(msg)
			cmds = append(cmds, cmd)
			slog.Error("operation failed", "toast", false, "error", msg.Err)
			a.toasts.AddToast(formatOperationErrorToast(msg.Err), components.ToastError)
			return a, tea.Batch(cmds...)
		}
		// Silently drop context cancellations — these fire when a pipeline is
		// intentionally torn down on session delete or quit, not from real errors.
		if errors.Is(msg.Err, context.Canceled) || errors.Is(msg.Err, context.DeadlineExceeded) {
			return a, nil
		}
		slog.Error("operation failed", "toast", false, "error", msg.Err)
		a.toasts.AddToast(formatOperationErrorToast(msg.Err), components.ToastError)
		return a, nil

	case LogToastMsg:
		level := components.ToastWarning
		if msg.Level == "ERROR" {
			level = components.ToastError
		}
		a.toasts.AddToast(msg.Message, level)
		return a, WaitForLogToastCmd(a.runtimeCtx.LogToasts)

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
	} else if a.activeOverlay == overlayNewSessionAutonomous {
		a.newSessionAutonomousOverlay, cmd = a.newSessionAutonomousOverlay.Update(msg)
		cmds = append(cmds, cmd)
	} else if a.activeOverlay == overlayAddRepo {
		a.addRepo, cmd = a.addRepo.Update(msg)
		cmds = append(cmds, cmd)
	} else if a.activeOverlay == overlayRepoManager {
		a.repoManager, cmd = a.repoManager.Update(msg)
		cmds = append(cmds, cmd)
	} else if a.activeOverlay == overlayWorktreePicker {
		a.worktreePicker, cmd = a.worktreePicker.Update(msg)
		cmds = append(cmds, cmd)
	} else if a.activeOverlay == overlaySessionSearch {
		a.sessionSearch, cmd = a.sessionSearch.Update(msg)
		cmds = append(cmds, cmd)
	} else if a.activeOverlay == overlaySettings {
		a.settingsPage, cmd = a.settingsPage.Update(msg, *a.provider.GetServices())
		cmds = append(cmds, cmd)
	} else if a.activeOverlay == overlaySourceItems {
		a.sourceItemsOverlay, cmd = a.sourceItemsOverlay.Update(msg)
		cmds = append(cmds, cmd)
	} else if a.activeOverlay == overlayOverviewLinks {
		a.overviewLinksOverlay, cmd = a.overviewLinksOverlay.Update(msg)
		cmds = append(cmds, cmd)
	} else if a.activeOverlay == overlayReviewFollowup {
		a.reviewFollowupOverlay, cmd = a.reviewFollowupOverlay.Update(msg)
		cmds = append(cmds, cmd)
	} else {
		a.content, cmd = a.content.Update(msg)
		cmds = append(cmds, cmd)
	}

	return a, tea.Batch(cmds...)
}

func (a *App) handleKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	// Block all key input during deferred startup.
	if a.inputBlocked {
		return a, nil
	}

	// Confirm dialog captures all key input when active.
	if a.confirmActive {
		switch msg.String() {
		case "y", keyEnter, "ctrl+c":
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
	if a.activeOverlay == overlayNewSessionAutonomous {
		a.newSessionAutonomousOverlay, cmd = a.newSessionAutonomousOverlay.Update(msg)
		return a, cmd
	}
	if a.activeOverlay == overlaySessionSearch {
		a.sessionSearch, cmd = a.sessionSearch.Update(msg)
		return a, cmd
	}
	if a.activeOverlay == overlaySettings {
		a.settingsPage, cmd = a.settingsPage.Update(msg, *a.provider.GetServices())
		return a, cmd
	}
	if a.activeOverlay == overlayActionMenu {
		a.actionMenu, cmd = a.actionMenu.Update(msg)
		return a, cmd
	}
	if a.activeOverlay == overlayLogs {
		a.logsOverlay, cmd = a.logsOverlay.Update(msg)
		return a, cmd
	}
	if a.activeOverlay == overlayRepoManager {
		a.repoManager, cmd = a.repoManager.Update(msg)
		return a, cmd
	}
	if a.activeOverlay == overlayWorktreePicker {
		a.worktreePicker, cmd = a.worktreePicker.Update(msg)
		return a, cmd
	}
	if a.activeOverlay == overlayAddRepo {
		a.addRepo, cmd = a.addRepo.Update(msg)
		return a, cmd
	}
	if a.activeOverlay == overlaySourceItems {
		a.sourceItemsOverlay, cmd = a.sourceItemsOverlay.Update(msg)
		return a, cmd
	}
	if a.activeOverlay == overlayOverviewLinks {
		a.overviewLinksOverlay, cmd = a.overviewLinksOverlay.Update(msg)
		return a, cmd
	}
	if a.activeOverlay == overlayReviewFollowup {
		a.reviewFollowupOverlay, cmd = a.reviewFollowupOverlay.Update(msg)
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
	if msg.String() == keyEnter && a.sidebarMode == sidebarPaneTasks && a.selectedTaskSessionID() != "" {
		if a.sourceDetailsNoticeForWorkItem(a.workItemByID(a.currentWorkItemID)) != nil {
			return a, a.jumpFromSourceDetailsToOverview()
		}
	}

	switch msg.String() {
	case "q":
		return a.handleQuitRequest()
	case "n":
		return a, a.openNewSession()
	case "A":
		return a, a.openNewSessionAutonomousOverlay()
	case "R":
		return a, a.openRepoManager()
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
	case "a":
		if sessionID := a.archivablSessionID(); sessionID != "" {
			a.showArchiveConfirm(sessionID)
			return a, nil
		}
		if sessionID := a.unarchivablSessionID(); sessionID != "" {
			a.showUnarchiveConfirm(sessionID)
			return a, nil
		}
	case "I":
		ids := a.interruptibleFocusedSessionIDs()
		if len(ids) > 0 {
			return a, func() tea.Msg { return ConfirmInterruptSessionsMsg{SessionIDs: ids} }
		}
	case "r":
		if sessionID := a.retryableFocusedSessionID(); sessionID != "" {
			ctx := a.pipelineCtxForSession(sessionID)
			return a, RetrySessionCmd(ctx, a.provider.Resumption(), a.provider.Task(), a.provider.Implementation(), sessionID, a.runtimeCtx.InstanceID)
		}
	case keyEsc, "left":
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
	case "up":
		if a.mainFocus == mainFocusContent {
			return a.updateContentForKey(msg, wasOverviewOverlayOpen, previousFocus)
		}
		a.sidebar.MoveUp()
		cmd = a.onSidebarMove()
		return a, cmd
	case keyDown:
		if a.mainFocus == mainFocusContent {
			return a.updateContentForKey(msg, wasOverviewOverlayOpen, previousFocus)
		}
		a.sidebar.MoveDown()
		cmd = a.onSidebarMove()
		return a, cmd
	case "f":
		if a.mainFocus == mainFocusContent {
			break
		}
		if a.sidebarMode == sidebarPaneSessions {
			a.sidebar.CycleFilter()
			a.rebuildSidebar()
			return a, nil
		}
	case "g":
		if a.mainFocus == mainFocusContent {
			break
		}
		if a.sidebarMode == sidebarPaneSessions {
			a.sidebar.CycleDimension()
			a.rebuildSidebar()
			return a, nil
		}
	case "t":
		// Open terminal in worktree when in session view.
		if a.mainFocus == mainFocusContent && a.content.Mode() == ContentModeAgentSession {
			if sessionID := a.content.sessionLog.SessionID(); sessionID != "" {
				if session := a.workItemTaskSession(a.currentWorkItemID, sessionID); session != nil && session.WorktreePath != "" {
					return a, OpenTerminalCmd(session.WorktreePath)
				}
			}
			break
		}
	case "o":
		if a.sidebarMode == sidebarPaneSessions {
			a.sidebar.ToggleDirection()
			a.rebuildSidebar()
			return a, nil
		}
	case "x":
		if !a.anyInputCaptured() {
			return a, a.openActionMenu()
		}
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
				if prevMode != a.content.mode && (prevMode == ContentModeAgentSession || prevMode == ContentModeSessionInteraction) {
					a.tailingSessionIDs = make(map[string]bool)
				}
				return nil
			}
			if taskSessionID == taskSidebarArtifactsID {
				a.content.artifacts.SetItems(a.buildArtifactItems(wi))
				a.content.artifacts.SetWorkItem(wi.ID, wi.State)
				a.content.SetMode(ContentModeArtifacts)
				if prevMode != a.content.mode && (prevMode == ContentModeAgentSession || prevMode == ContentModeSessionInteraction) {
					a.tailingSessionIDs = make(map[string]bool)
				}
				return nil
			}
			if session := a.workItemTaskSession(a.currentWorkItemID, taskSessionID); session != nil {
				if session.Kind == domain.AgentSessionKindForeman {
					return a.showForemanContent(wi, session)
				}
				return a.showTaskContent(wi, session)
			}
			a.setSelectedTaskSessionID("")
		}
	}

	a.content.SetMode(ContentModeOverview)
	a.content.SetOverviewData(a.buildOverviewData(wi))
	if prevMode != a.content.mode && (prevMode == ContentModeAgentSession || prevMode == ContentModeSessionInteraction) {
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
	case domain.SessionMerged:
		return &sourceDetailsNotice{
			Title:   "Work item merged",
			Body:    "All pull requests for this work item have been merged.",
			Hint:    "Press [Enter] to open the overview and inspect the final status.",
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

		notice.Body = target + " is paused until someone answers the question."
		if question := strings.TrimSpace(action.Blocked); question != "" {
			notice.Body += " Question: " + question
		}
	case overviewActionInterrupted:
		if len(action.InterruptedSessions) > 1 {
			notice.Body = fmt.Sprintf("%d repo tasks were interrupted and cannot continue until they are resumed: %s.", len(action.InterruptedSessions), strings.Join(action.Affected, ", "))
			break
		}
		target := firstNonEmptyString(strings.TrimSpace(action.Blocked), firstSourceDetailsAffected(action.Affected), "A repo task")
		notice.Body = target + " was interrupted and cannot continue until it is resumed or abandoned."
	case overviewActionReviewing:
		if len(action.Affected) > 0 {
			notice.Body = fmt.Sprintf("Review critiques are waiting for a human decision in %s.", strings.Join(action.Affected, ", "))
		} else {
			notice.Body = "Review critiques are waiting for a human decision."
		}
		notice.Hint = "Press [Enter] to open the overview and inspect the review."
	case overviewActionFinalize:
		notice.Body = "Repo tasks are complete, but final commit/push/completion did not finish. Finalize from the overview to retry without rerunning agents."
		notice.Hint = "Press [Enter] to open the overview and finalize."
	case overviewActionCompleted:
		notice.Title = "Work item completed"
		notice.Body = "This work item completed while you were focused on a task view."
		notice.Hint = "Press [Enter] to open the overview and inspect the final status or review artifacts."
		notice.Variant = components.CalloutCard
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

func (a *App) showTaskContent(wi *domain.Session, agentSession *domain.AgentSession) tea.Cmd {
	title := firstNonEmptyString(wi.ExternalID, wi.ID) + " · " + taskSidebarSessionTitle(agentSession)
	metaParts := []string{sessionStatusLabel(agentSession.Status)}
	if agentSession.HarnessName != "" {
		metaParts = append(metaParts, agentSession.HarnessName)
	}
	metaParts = append(metaParts, taskSessionDisplayName(agentSession))
	a.content.sessionLog.SetNotice(a.sourceDetailsNoticeForWorkItem(wi))
	if agentSession.Kind == domain.AgentSessionKindPlanning {
		a.content.SetMode(ContentModeAgentSession)
	} else {
		a.content.SetMode(ContentModeSessionInteraction)
	}
	a.content.sessionLog.SetTitle(title)
	a.content.sessionLog.SetModeLabel(taskSessionModeLabel(agentSession))
	a.content.sessionLog.SetMeta(strings.Join(metaParts, " · "))
	logPath := filepath.Join(a.sessionsDir, agentSession.ID+".log")
	resumeOffset := int64(0)
	if a.content.sessionLog.live && a.content.sessionLog.sessionID == agentSession.ID && a.content.sessionLog.logPath == logPath {
		resumeOffset = a.content.sessionLog.offset
	}
	a.content.sessionLog.SetLogPath(agentSession.ID, logPath)
	a.content.sessionLog.SetPlanID(agentSession.PlanID)
	switch agentSession.Status {
	case domain.AgentSessionFailed:
		a.content.sessionLog.ClearCompletedSession()
		a.content.sessionLog.SetFailedSession(agentSession.ID)
	case domain.AgentSessionCompleted:
		a.content.sessionLog.ClearFailedSession()
		a.content.sessionLog.SetCompletedSession(agentSession.ID)
	default:
		a.content.sessionLog.ClearFailedSession()
		a.content.sessionLog.ClearCompletedSession()
	}
	agentActive := agentSession.Status == domain.AgentSessionPending ||
		agentSession.Status == domain.AgentSessionRunning ||
		agentSession.Status == domain.AgentSessionWaitingForAnswer
	spinnerCmd := a.content.sessionLog.SetAgentActive(agentActive)
	if !a.tailingSessionIDs[agentSession.ID] {
		a.tailingSessionIDs[agentSession.ID] = true
		return tea.Batch(spinnerCmd, TailSessionLogCmd(logPath, agentSession.ID, resumeOffset))
	}
	return spinnerCmd
}

// showForemanContent sets up the content panel to display the Foreman's session log.
// Status, spinner, and completed state are derived from the persisted AgentSession.
func (a *App) showForemanContent(wi *domain.Session, foremanSession *domain.AgentSession) tea.Cmd {
	var titlePrefix string
	if wi != nil {
		titlePrefix = firstNonEmptyString(wi.ExternalID, wi.ID) + " · "
	}
	title := titlePrefix + "Foreman session " + shortSessionID(foremanSession.ID)
	running := foremanSession.Status == domain.AgentSessionRunning
	statusLabel := sessionStatusLabel(foremanSession.Status)
	meta := strings.Join([]string{statusLabel, "Foreman"}, " · ")
	a.content.sessionLog.SetNotice(nil)
	a.content.SetMode(ContentModeSessionInteraction)
	a.content.sessionLog.SetTitle(title)
	a.content.sessionLog.SetModeLabel("Foreman")
	a.content.sessionLog.SetMeta(meta)
	a.content.sessionLog.ClearFailedSession()
	if foremanSession.Status == domain.AgentSessionCompleted {
		a.content.sessionLog.SetCompletedSession(foremanSession.ID)
	} else if foremanSession.Status == domain.AgentSessionFailed {
		a.content.sessionLog.SetFailedSession(foremanSession.ID)
	} else {
		a.content.sessionLog.ClearCompletedSession()
	}
	logPath := filepath.Join(a.sessionsDir, foremanSession.ID+".log")
	resumeOffset := int64(0)
	if a.content.sessionLog.live && a.content.sessionLog.sessionID == foremanSession.ID && a.content.sessionLog.logPath == logPath {
		resumeOffset = a.content.sessionLog.offset
	}
	a.content.sessionLog.SetLogPath(foremanSession.ID, logPath)
	spinnerCmd := a.content.sessionLog.SetAgentActive(running)
	if !a.tailingSessionIDs[foremanSession.ID] {
		a.tailingSessionIDs[foremanSession.ID] = true
		return tea.Batch(spinnerCmd, TailSessionLogCmd(logPath, foremanSession.ID, resumeOffset))
	}
	return spinnerCmd
}

func (a *App) canActOnSession(s domain.AgentSession) bool {
	if a.runtimeCtx.InstanceID == "" || s.OwnerInstanceID == nil {
		return true
	}
	if *s.OwnerInstanceID == a.runtimeCtx.InstanceID {
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

func (a App) interruptibleFocusedSessionIDs() []string {
	if a.currentWorkItemID == "" {
		return nil
	}

	// When focused on a specific agent session in content, interrupt only that session.
	if a.mainFocus == mainFocusContent && a.content.Mode() == ContentModeAgentSession {
		if sessionID := a.content.sessionLog.SessionID(); sessionID != "" {
			if session := a.workItemTaskSession(a.currentWorkItemID, sessionID); session != nil && isInterruptibleAgentSession(*session) {
				return []string{sessionID}
			}
		}
		return nil
	}

	// When focused on an agent session in the task sidebar, interrupt only that session.
	// Foreman sessions are not interruptible.
	if a.mainFocus == mainFocusSidebar && a.sidebarMode == sidebarPaneTasks {
		selectedID := a.selectedTaskSessionID()
		if selectedID != "" && selectedID != taskSidebarSourceDetailsID && selectedID != taskSidebarArtifactsID {
			if session := a.workItemTaskSession(a.currentWorkItemID, selectedID); session != nil && session.Kind != domain.AgentSessionKindForeman && isInterruptibleAgentSession(*session) {
				return []string{session.ID}
			}
			return nil
		}
	}

	// When focused on overview/sessions view, interrupt all interruptible sessions.
	ids := make([]string, 0, len(a.sessions))
	for _, session := range a.sessionsForWorkItem(a.currentWorkItemID) {
		if isInterruptibleAgentSession(session) {
			ids = append(ids, session.ID)
		}
	}
	return ids
}

func isInterruptibleAgentSession(session domain.AgentSession) bool {
	return session.Status == domain.AgentSessionRunning || session.Status == domain.AgentSessionWaitingForAnswer
}

// retryableFocusedSessionID returns the session ID to retry based on current focus context.
// Returns empty string if no retryable session is focused (use overview retry instead).
func (a App) retryableFocusedSessionID() string {
	if a.currentWorkItemID == "" {
		return ""
	}

	// When focused on a specific session in content, retry only that session if it's failed.
	if a.mainFocus == mainFocusContent && a.content.Mode() == ContentModeAgentSession {
		if sessionID := a.content.sessionLog.SessionID(); sessionID != "" {
			if session := a.workItemTaskSession(a.currentWorkItemID, sessionID); session != nil && session.Status == domain.AgentSessionFailed {
				return sessionID
			}
		}
		return ""
	}

	// When focused on a session in the task sidebar, retry only that session if it's failed.
	// Foreman sessions do not support retry.
	if a.mainFocus == mainFocusSidebar && a.sidebarMode == sidebarPaneTasks {
		selectedID := a.selectedTaskSessionID()
		if selectedID != "" && selectedID != taskSidebarSourceDetailsID && selectedID != taskSidebarArtifactsID {
			if session := a.workItemTaskSession(a.currentWorkItemID, selectedID); session != nil && session.Kind != domain.AgentSessionKindForeman && session.Status == domain.AgentSessionFailed {
				return session.ID
			}
		}
		return ""
	}

	// In overview/sessions mode, no direct session retry - use overview retry instead.
	return ""
}

// pipelineCtxForSession returns a cancellable pipeline context for the given session's work item.
func (a *App) pipelineCtxForSession(sessionID string) context.Context {
	for _, s := range a.sessions {
		if s.ID == sessionID {
			return a.registerPipelineCancel(s.WorkItemID)
		}
	}
	return context.Background()
}

func (a *App) interruptActiveAgentSessions(ctx context.Context) error {
	ids := make([]string, 0)
	for _, session := range a.sessions {
		if isInterruptibleAgentSession(session) {
			ids = append(ids, session.ID)
		}
	}
	return a.interruptAgentSessionsByID(ctx, ids, a.sessions)
}

func (a *App) interruptAgentSessionsByID(ctx context.Context, ids []string, sessions []domain.AgentSession) error {
	if len(ids) == 0 {
		return nil
	}
	taskSvc := a.provider.Task()
	if taskSvc == nil {
		return errors.New("agent session service is unavailable")
	}
	byID := make(map[string]domain.AgentSession, len(sessions))
	for _, session := range sessions {
		byID[session.ID] = session
	}
	for _, id := range ids {
		session, ok := byID[id]
		if !ok || !isInterruptibleAgentSession(session) {
			continue
		}
		if err := interruptAgentSession(ctx, taskSvc, a.provider.SessionRegistry(), session); err != nil {
			return err
		}
	}
	return nil
}

func interruptAgentSession(ctx context.Context, taskSvc *service.AgentSessionService, registry orchestrator.SessionRegistry, session domain.AgentSession) error {
	cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cleanupCancel()
	if registry != nil {
		if harnessSession, ok := registry.Registered(session.ID); ok {
			if info := harnessSession.ResumeInfo(); len(info) > 0 {
				if err := taskSvc.UpdateResumeInfo(cleanupCtx, session.ID, info); err != nil {
					return fmt.Errorf("update resume info for %s: %w", session.ID, err)
				}
			}
		}
	}
	if err := taskSvc.Interrupt(cleanupCtx, session.ID); err != nil {
		return fmt.Errorf("interrupt agent session %s: %w", session.ID, err)
	}
	if registry != nil {
		registry.AbortAndDeregister(cleanupCtx, session.ID)
	}
	return nil
}

func (a *App) showDeleteSessionConfirm(sessionID string) {
	sID := sessionID
	var running int
	for _, agentSession := range a.sessions {
		if agentSession.WorkItemID == sID {
			switch agentSession.Status {
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

func (a *App) showArchiveConfirm(workItemID string) {
	a.showConfirm("Archive Session",
		"Archive this session? It will be hidden from the default views. You can unarchive it later.",
		func() tea.Msg { return ArchiveSessionMsg{WorkItemID: workItemID} },
	)
}

func (a *App) showUnarchiveConfirm(workItemID string) {
	a.showConfirm("Unarchive Session",
		"Unarchive this session and restore it to the completed view.",
		func() tea.Msg { return UnarchiveSessionMsg{WorkItemID: workItemID} },
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

// dispatchReviewAddress kicks off an off-thread DB lookup that resolves each
// repo in msg.PerRepo to a completed Task. The actual FollowUpSessionMsg dispatch
// and toast surfacing happens when the resulting ReviewAddressDispatchResultMsg
// is received.
func (a *App) dispatchReviewAddress(msg FollowUpFromReviewAddressMsg) tea.Cmd {
	if a.provider.Task() == nil {
		a.toasts.AddToast("Task service unavailable; cannot dispatch review follow-up", components.ToastError)
		return nil
	}
	return ResolveReviewAddressDispatchCmd(context.Background(), a.provider.Task(), msg.WorkItemID, msg.PerRepo)
}

// applyReviewAddressDispatchResult emits per-task FollowUpSessionMsg commands and
// surfaces the success/skip toast. Runs on the UI thread (cheap; no IO).
func (a *App) applyReviewAddressDispatchResult(msg ReviewAddressDispatchResultMsg) tea.Cmd {
	if msg.Err != nil {
		slog.Error("list tasks for review follow-up failed", "work_item_id", msg.WorkItemID, "err", msg.Err)
		a.toasts.AddToast(fmt.Sprintf("Failed to dispatch review follow-up: %v", msg.Err), components.ToastError)
		return nil
	}
	dispatched := len(msg.Dispatched)
	cmds := make([]tea.Cmd, 0, dispatched)
	for taskID, feedback := range msg.Dispatched {
		taskID, feedback := taskID, feedback
		cmds = append(cmds, func() tea.Msg {
			return FollowUpSessionMsg{TaskID: taskID, Feedback: feedback}
		})
	}
	if msg.Total == 0 || dispatched == 0 {
		if len(msg.Skipped) > 0 {
			a.toasts.AddToast(fmt.Sprintf("No follow-up dispatched: no completed task for %s", strings.Join(msg.Skipped, ", ")), components.ToastWarning)
		} else {
			a.toasts.AddToast("No follow-up dispatched (no selection)", components.ToastWarning)
		}
		return nil
	}
	if len(msg.Skipped) == 0 {
		a.toasts.AddToast(fmt.Sprintf("Addressed %d of %d repo(s)", dispatched, msg.Total), components.ToastSuccess)
	} else {
		a.toasts.AddToast(fmt.Sprintf("Addressed %d of %d repos (skipped: %s)", dispatched, msg.Total, strings.Join(msg.Skipped, ", ")), components.ToastWarning)
	}
	return tea.Batch(cmds...)
}

// teardownAllPipelines cancels every active pipeline context and shuts
// down all services. This is the shared teardown path for both quit and
// (potentially) batch-delete.
func (a *App) teardownAllPipelines() {
	// Cancel all pipeline contexts first. This signals orchestrator goroutines
	// to stop before we shut down the services they depend on.
	for id, cancel := range a.pipelineCancels {
		cancel()
		delete(a.pipelineCancels, id)
	}

	// Stop autonomous mode runtime.
	if runtime := a.newSessionAutonomous; runtime != nil {
		if stopErr := runtime.Stop(); stopErr != nil {
			slog.Warn("failed to stop new session autonomous runtime on teardown", "error", stopErr)
		}
	}
	a.newSessionAutonomous = nil
	a.newSessionAutonomousChan = nil
	a.syncNewSessionFilterOverlays()

	// Shut down all services: stops foremen, aborts sessions, closes refresh
	// goroutines, and closes the event bus.
	a.provider.Close(context.Background())
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
			fmt.Sprintf("%d agent %s running. Quit will interrupt them so they can be resumed later. Quit anyway?", n, sessionWord),
			func() tea.Msg { return QuitConfirmedMsg{} },
		)
		return &a, nil
	}
	a.teardownAllPipelines()
	return &a, a.quitCmd()
}

// quitCmd returns the command that exits the program, cleaning up the instance
// record when one exists.
func (a *App) quitCmd() tea.Cmd {
	if a.runtimeCtx.InstanceID != "" {
		return tea.Batch(DeleteInstanceCmd(a.provider.Instance(), a.runtimeCtx.InstanceID), tea.Quit)
	}
	return tea.Quit
}

func (a App) sidebarEntryFromWorkItem(wi domain.Session) SidebarEntry {
	entry := SidebarEntry{
		Kind:           SidebarEntryWorkItem,
		WorkItemID:     wi.ID,
		ExternalID:     wi.ExternalID,
		Source:         wi.Source,
		Title:          wi.Title,
		State:          wi.State,
		LastActivity:   wi.UpdatedAt,
		CreatedAt:      wi.CreatedAt,
		WorkItemStatus: sessionExternalState(&wi),
	}
	// For GitLab sessions the canonical ExternalID encodes a numeric project ID
	// (e.g. "gl:issue:1234#42") which is meaningless to users. Derive a
	// human-readable label from the tracker reference (e.g. "acme/rocket#42")
	// without altering the stored ExternalID.
	if wi.Source == providerGitlab {
		if refs := sessionTrackerRefs(wi.Metadata); len(refs) > 0 {
			ref := refs[0]
			container := trackerRefContainer(ref)
			if container != "" && ref.Number > 0 {
				entry.ExternalLabel = fmt.Sprintf("%s#%d", container, ref.Number)
			}
		}
	}
	if plan := a.plans[wi.ID]; plan != nil {
		sps := a.subPlans[plan.ID]
		entry.TotalSubPlans = len(sps)
		// Activity timestamps and counts come from the full sub-plan/session
		// set, but interrupted/open-question flags must reflect only the
		// graph leaves so historical interrupted/waiting rows do not light
		// up labels after a retry/follow-up has replaced them.
		subPlanIDs := make(map[string]bool, len(sps))
		for _, sp := range sps {
			subPlanIDs[sp.ID] = true
			if sp.UpdatedAt.After(entry.LastActivity) {
				entry.LastActivity = sp.UpdatedAt
			}
			if sp.Status == domain.SubPlanCompleted {
				entry.DoneSubPlans++
			}
		}
		for _, s := range a.sessions {
			if !subPlanIDs[s.SubPlanID] {
				continue
			}
			if s.UpdatedAt.After(entry.LastActivity) {
				entry.LastActivity = s.UpdatedAt
			}
		}
	}
	graphSessions := make([]domain.AgentSession, 0, len(a.sessions))
	for _, s := range a.sessionsForWorkItem(wi.ID) {
		if s.Kind == domain.AgentSessionKindManual {
			continue
		}
		graphSessions = append(graphSessions, s)
	}
	for _, leaf := range leafAgentSessions(graphSessions) {
		if leaf.Status == domain.AgentSessionWaitingForAnswer {
			for _, q := range a.questions[leaf.ID] {
				if q.Status == domain.QuestionPending || q.Status == domain.QuestionEscalated {
					entry.HasOpenQuestion = true
				}
			}
		}
		if leaf.Status == domain.AgentSessionInterrupted {
			entry.HasInterrupted = true
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

func (a App) sessionsForWorkItem(workItemID string) []domain.AgentSession {
	plan := a.plans[workItemID]
	subPlanOrder := make(map[string]int)
	if plan != nil {
		for i, sp := range a.subPlans[plan.ID] {
			subPlanOrder[sp.ID] = i
		}
	}
	sessions := make([]domain.AgentSession, 0)
	for _, s := range a.sessions {
		if s.WorkItemID == workItemID {
			sessions = append(sessions, s)
		}
	}
	sort.SliceStable(sessions, func(i, j int) bool {
		rankI := taskSessionPhaseRank(sessions[i].Kind)
		rankJ := taskSessionPhaseRank(sessions[j].Kind)
		if rankI != rankJ {
			return rankI < rankJ
		}
		if rankI != taskSessionPhaseRank(domain.AgentSessionKindPlanning) {
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

	if artifactItems := a.buildArtifactItems(wi); len(artifactItems) > 0 {
		aggregateReview := aggregateReviewState(artifactItems)
		aggregateci := aggregateCIState(artifactItems)
		entries = append(entries, SidebarEntry{
			Kind:                         SidebarEntryTaskArtifacts,
			WorkItemID:                   workItemID,
			SessionID:                    taskSidebarArtifactsID,
			Title:                        "Pull requests & merge requests",
			SubtitleText:                 fmt.Sprintf("%d artifact%s", len(artifactItems), pluralS(len(artifactItems))),
			LastActivity:                 wi.UpdatedAt,
			ArtifactAggregateReviewState: aggregateReview,
			ArtifactAggregateCIState:     aggregateci,
		})
	}

	sessions := a.sessionsForWorkItem(workItemID)

	// Planning block: all planning sessions in temporal order (oldest first).
	var planningSessions []domain.AgentSession
	for _, s := range sessions {
		if s.Kind == domain.AgentSessionKindPlanning {
			planningSessions = append(planningSessions, s)
		}
	}
	slog.Debug("taskSidebarEntries planningSessions", "workItemID", workItemID, "count", len(planningSessions))
	sort.SliceStable(planningSessions, func(i, j int) bool {
		return planningSessions[i].CreatedAt.Before(planningSessions[j].CreatedAt)
	})
	if len(planningSessions) > 0 {
		entries = append(entries, SidebarEntry{
			Kind:       SidebarEntryGroupHeader,
			WorkItemID: workItemID,
			GroupTitle: "Planning",
		})
		for _, agentSession := range planningSessions {
			entries = append(entries, SidebarEntry{
				Kind:           SidebarEntryTaskSession,
				WorkItemID:     workItemID,
				SessionID:      agentSession.ID,
				Title:          taskSidebarSessionTitle(&agentSession),
				State:          wi.State,
				SessionStatus:  agentSession.Status,
				RepositoryName: "Planning",
				LastActivity:   agentSession.UpdatedAt,
			})
		}
	}

	// Foreman block: build from persisted AgentSessionKindForeman rows.
	var foremanSessions []domain.AgentSession
	for _, s := range sessions {
		if s.Kind == domain.AgentSessionKindForeman {
			foremanSessions = append(foremanSessions, s)
		}
	}
	// There should be at most one foreman session per work item.
	// Use the most recently updated one if multiple exist.
	if len(foremanSessions) > 1 {
		sort.SliceStable(foremanSessions, func(i, j int) bool {
			return foremanSessions[i].UpdatedAt.After(foremanSessions[j].UpdatedAt)
		})
	}
	var foremanEntry *SidebarEntry
	if len(foremanSessions) > 0 {
		foreman := foremanSessions[0]
		foremanEntry = &SidebarEntry{
			Kind:           SidebarEntryTaskSession,
			WorkItemID:     workItemID,
			SessionID:      foreman.ID,
			Title:          "Foreman session " + shortSessionID(foreman.ID),
			RepositoryName: "Foreman",
			SessionStatus:  foreman.Status,
			LastActivity:   foreman.UpdatedAt,
		}
	}
	if foremanEntry != nil {
		entries = append(entries, SidebarEntry{
			Kind:       SidebarEntryGroupHeader,
			WorkItemID: workItemID,
			GroupTitle: "Foreman",
		})
		entries = append(entries, *foremanEntry)
	}

	// Repository blocks: one group per repo, implementation/review sessions in temporal order (oldest first).
	repoSessions := make(map[string][]domain.AgentSession)
	for _, s := range sessions {
		if s.Kind == domain.AgentSessionKindImplementation || s.Kind == domain.AgentSessionKindReview {
			repo := firstNonEmptyString(s.RepositoryName, "Repository")
			repoSessions[repo] = append(repoSessions[repo], s)
		}
	}
	repoNames := make([]string, 0, len(repoSessions))
	for name := range repoSessions {
		repoNames = append(repoNames, name)
	}
	sort.Strings(repoNames)
	for _, repoName := range repoNames {
		repoItems := repoSessions[repoName]
		sort.SliceStable(repoItems, func(i, j int) bool {
			return repoItems[i].CreatedAt.Before(repoItems[j].CreatedAt)
		})
		entries = append(entries, SidebarEntry{
			Kind:       SidebarEntryGroupHeader,
			WorkItemID: workItemID,
			GroupTitle: repoName,
		})
		for _, agentSession := range repoItems {
			entryRepo := agentSession.RepositoryName
			if agentSession.Kind == domain.AgentSessionKindReview {
				entryRepo = firstNonEmptyString(entryRepo, "Review")
			}
			entries = append(entries, SidebarEntry{
				Kind:           SidebarEntryTaskSession,
				WorkItemID:     workItemID,
				SessionID:      agentSession.ID,
				Title:          taskSidebarSessionTitle(&agentSession),
				State:          wi.State,
				SessionStatus:  agentSession.Status,
				RepositoryName: entryRepo,
				LastActivity:   agentSession.UpdatedAt,
			})
		}
	}
	return entries
}

// aggregateReviewState derives the worst-case review state across all artifacts.
func aggregateReviewState(items []ArtifactItem) string {
	hasApproved := false
	for _, item := range items {
		for _, r := range item.Reviews {
			if r.State == "changes_requested" {
				return "changes_requested"
			}
			if r.State == "approved" {
				hasApproved = true
			}
		}
	}
	if hasApproved {
		return "approved"
	}
	return ""
}

func aggregateCIState(items []ArtifactItem) string {
	hasChecks := false
	for _, item := range items {
		for _, c := range item.Checks {
			hasChecks = true
			if c.Conclusion != "" && c.Conclusion != "success" && c.Conclusion != "neutral" && c.Conclusion != "skipped" {
				return "failure"
			}
		}
	}
	if !hasChecks {
		return ""
	}
	for _, item := range items {
		for _, c := range item.Checks {
			if c.Status == "in_progress" || c.Status == "queued" {
				return "in_progress"
			}
		}
	}
	return "success"
}

func (a App) workItemFocusTargetAfterRemoval(workItemID string) string {
	entries := a.sessionSidebarEntries()
	entries = FilterSidebarEntries(entries, a.sidebar.FilterMode())
	entries = ApplyDimensionAndDirection(entries, a.sidebar.DimensionMode(), a.sidebar.DirectionMode())

	removedIndex := -1
	for i, entry := range entries {
		if entry.Kind == SidebarEntryWorkItem && entry.WorkItemID == workItemID {
			removedIndex = i
			break
		}
	}
	if removedIndex < 0 {
		return ""
	}

	for i := removedIndex - 1; i >= 0; i-- {
		if entries[i].Kind == SidebarEntryWorkItem {
			return entries[i].WorkItemID
		}
	}
	for i := removedIndex + 1; i < len(entries); i++ {
		if entries[i].Kind == SidebarEntryWorkItem {
			return entries[i].WorkItemID
		}
	}
	return ""
}

func (a *App) rebuildSidebar() {
	if a.sidebarMode == sidebarPaneTasks && a.currentWorkItemID != "" && a.workItemByID(a.currentWorkItemID) != nil {
		wi := a.workItemByID(a.currentWorkItemID)
		a.sidebar.SetTitle(firstNonEmptyString(wi.Title, wi.ExternalID, wi.ID) + " \u00b7 Tasks")
		a.sidebar.SetPaneMode(sidebarPaneTasks)
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
	a.sidebar.SetPaneMode(sidebarPaneSessions)
	entries := a.sessionSidebarEntries()
	entries = FilterSidebarEntries(entries, a.sidebar.FilterMode())
	entries = ApplyDimensionAndDirection(entries, a.sidebar.DimensionMode(), a.sidebar.DirectionMode())
	a.sidebar.SetEntries(entries)
	a.content.SetSessionStats(a.computeSessionStats(entries))
	if a.currentWorkItemID == "" {
		a.sidebar.ClearSelection()
		return
	}
	if !a.sidebar.SelectWorkItem(a.currentWorkItemID) {
		a.sidebar.ClearSelection()
	}
}

// computeSessionStats derives aggregate counts from sidebar entries.
// Only work items and task overviews count toward totals; group headers are excluded.
func (a App) computeSessionStats(entries []SidebarEntry) SessionStats {
	total := 0
	for _, e := range entries {
		if e.Kind == SidebarEntryWorkItem || e.Kind == SidebarEntryTaskOverview {
			total++
		}
	}
	stats := SessionStats{TotalSessions: total}
	for _, e := range entries {
		if e.Kind != SidebarEntryWorkItem {
			continue
		}
		if e.State == domain.SessionPlanReview || e.HasOpenQuestion || e.HasInterrupted {
			stats.ActionNeeded++
		}
	}
	return stats
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

	// When an overview overlay is active, reuse the cached base render
	// instead of recomputing the full sidebar + content + status bar.
	// The base is only stale by at most one frame (cached on the last
	// non-overlay View call) and is invisible behind the near-fullscreen
	// overlay anyway. renderCenteredOverlay composites the overlay on
	// top so the background peeks through at the edges.
	if a.activeOverlay == overlayNone && a.content.mode == ContentModeOverview && a.content.overview.overlay != overviewOverlayNone {
		overlay := a.content.overview.overlayView(a.windowWidth, a.windowHeight)
		base := *a.cachedBase
		if base == "" {
			// First frame after overlay opened or after resize — no cached
			// base yet. Fall back to blank placement (one frame only).
			return a.applyToasts(renderOverlay(overlay, a.windowWidth, a.windowHeight))
		}
		return a.applyToasts(renderCenteredOverlay(base, overlay, a.windowWidth, a.windowHeight))
	}

	if a.startupIntegrationSpinnerFrameOnly && a.startupIntegrationsInProgress {
		base := *a.cachedBase
		if base != "" {
			return a.applyToasts(base)
		}
	}

	hints := a.currentHints()
	rightText := a.statusBarText()
	// Use the sbHeight stored on the last WindowSizeMsg so body height is stable
	// across focus changes that alter the hint set between resizes.
	sbHeight := max(1, a.sbHeight)
	layout := styles.ComputeMainPageLayout(a.windowWidth, a.windowHeight, SidebarWidth, a.statusBar.styles.Chrome, sbHeight)
	overlayActive := a.overviewOverlayOpen()

	// Each sub-model's View() already produces output sized to (width, height)
	// via internal fitViewBox / lipgloss.Place. The outer lipgloss wrapper
	// handles padding and acts as a size safety net for RenderPane.
	sidebarContent := lipgloss.NewStyle().
		Width(layout.SidebarInnerWidth).
		Height(layout.PaneInnerHeight).
		Render(a.sidebar.View())
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
	contentContent := contentStyle.Render(a.content.View())
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

	statusBar := a.statusBar.ViewN(hints, rightText, a.windowWidth, sbHeight)

	base := lipgloss.JoinVertical(lipgloss.Left, body, statusBar)
	*a.cachedBase = base

	var result string
	switch a.activeOverlay {
	case overlayNewSession:
		result = renderOverlay(a.newSession.View(), a.windowWidth, a.windowHeight)
	case overlayNewSessionAutonomous:
		result = renderOverlay(a.newSessionAutonomousOverlay.View(), a.windowWidth, a.windowHeight)
	case overlaySessionSearch:
		result = renderOverlay(a.sessionSearch.View(), a.windowWidth, a.windowHeight)
	case overlaySettings:
		result = a.settingsPage.View()
	case overlayActionMenu:
		result = renderOverlay(a.actionMenu.View(), a.windowWidth, a.windowHeight)
	case overlaySourceItems:
		result = renderOverlay(a.sourceItemsOverlay.View(), a.windowWidth, a.windowHeight)
	case overlayOverviewLinks:
		result = renderOverlay(a.overviewLinksOverlay.View(), a.windowWidth, a.windowHeight)
	case overlayReviewFollowup:
		result = renderOverlay(a.reviewFollowupOverlay.View(), a.windowWidth, a.windowHeight)
	case overlayLogs:
		result = renderOverlay(a.logsOverlay.View(), a.windowWidth, a.windowHeight)
	case overlayAddRepo:
		result = renderOverlay(a.addRepo.View(), a.windowWidth, a.windowHeight)
	case overlayRepoManager:
		result = renderOverlay(a.repoManager.View(), a.windowWidth, a.windowHeight)
	case overlayWorktreePicker:
		result = renderOverlay(a.worktreePicker.View(), a.windowWidth, a.windowHeight)
	default:
		result = base
	}

	return a.applyToasts(result)
}

// applyToasts renders the toast stack (if any) into the top-right corner of
// the given frame. Extracted so both the base and overlay-early-return paths
// share identical toast handling.
func (a App) applyToasts(result string) string {
	toastView := a.toasts.StackView(a.pinnedToasts()...)
	if toastView != "" {
		placement := styles.ComputeToastPlacement(a.statusBar.styles.Chrome)
		result = renderTopRightOverlay(result, toastView, a.windowWidth, placement.TopInset, placement.BottomInset)
	}
	return result
}

func (a App) statusBarText() string {
	parts := make([]string, 0, 2)
	if a.runtimeCtx.WorkspaceName != "" {
		parts = append(parts, a.runtimeCtx.WorkspaceName)
	}
	parts = append(parts, fmt.Sprintf("%d active sessions", a.activeSessionCount()))
	return strings.Join(parts, " · ")
}

func (a App) activeSessionCount() int {
	count := 0
	for _, agentSession := range a.sessions {
		switch agentSession.Status {
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

func abandonSessionCmd(svc *service.AgentSessionService, sessionID string) tea.Cmd {
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

func deleteSessionCmd(provider ServiceProvider, sessionsDir, sessionID string, reviewSessionLogs map[string]string) tea.Cmd {
	return func() tea.Msg {
		result, err := deleteSessionTasksAndArtifacts(context.Background(), provider, sessionsDir, sessionID, reviewSessionLogs)
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

func deleteSessionTasksAndArtifacts(ctx context.Context, provider ServiceProvider, sessionsDir, sessionID string, reviewSessionLogs map[string]string) (sessionDeleteResult, error) {
	svcs := provider.GetServices()
	gitClient := provider.GitClient()

	result := sessionDeleteResult{TaskIDs: make([]string, 0)}
	artifactDeletes := make([]struct {
		taskID        string
		reviewLogPath string
	}, 0)

	// Collect worktree paths before deleting sessions so we can remove them.
	// Worktree paths are needed for git-work rm and for emitting cleanup events.
	var worktreePaths []string
	var worktreeByRepo map[string]string // repo -> worktree path for git-work rm

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
			for _, agentSession := range tasks {
				result.TaskIDs = append(result.TaskIDs, agentSession.ID)
				artifactDeletes = append(artifactDeletes, struct {
					taskID        string
					reviewLogPath string
				}{taskID: agentSession.ID, reviewLogPath: reviewSessionLogs[agentSession.ID]})

				// Collect worktree paths before deletion
				if agentSession.WorktreePath != "" {
					worktreePaths = append(worktreePaths, agentSession.WorktreePath)
					// Dedupe by repo - last session wins, but paths should be same for same repo
					if worktreeByRepo == nil {
						worktreeByRepo = make(map[string]string)
					}
					worktreeByRepo[agentSession.RepositoryName] = agentSession.WorktreePath
				}

				if err := svcs.Task.Delete(ctx, agentSession.ID); err != nil {
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

	// Delete session review artifacts (links to MRs/PRs) and their associated records.
	// This must happen before deleting the work_item so we can still look up the work_item.
	if err := deleteSessionReviewArtifacts(ctx, svcs, sessionID); err != nil {
		return sessionDeleteResult{}, fmt.Errorf("delete session review artifacts: %w", err)
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

	// Remove worktrees using git-work rm.
	// Skip if gitClient is nil (e.g., in tests without a real client).
	for repo, worktreePath := range worktreeByRepo {
		if worktreePath == "" || gitClient == nil {
			continue
		}
		// Get the repo dir (parent of worktree)
		repoDir := filepath.Dir(worktreePath)
		// Get branch name from worktree path (last component)
		branch := filepath.Base(worktreePath)
		if err := gitClient.Remove(ctx, repoDir, branch); err != nil {
			slog.Warn("failed to remove worktree during session delete",
				"worktree", worktreePath, "repo", repo, "error", err)
			cleanupErrs = append(cleanupErrs, fmt.Errorf("remove worktree %s: %w", worktreePath, err))
		} else {
			slog.Debug("removed worktree during session delete", "worktree", worktreePath, "repo", repo)
		}
	}

	result.CleanupWarning = errors.Join(cleanupErrs...)
	return result, nil
}

// deleteSessionReviewArtifacts deletes all review artifact records for a work item,
// including linked MRs/PRs, reviews, and checks.
// If SessionArtifacts service is nil (e.g., in tests), this is a no-op.
func deleteSessionReviewArtifacts(ctx context.Context, svcs *Services, workItemID string) error {
	// Skip if SessionArtifacts is nil (e.g., in tests without full service setup)
	if svcs.SessionArtifacts == nil {
		return nil
	}

	// Get all session review artifacts for this work item
	artifacts, err := svcs.SessionArtifacts.ListByWorkItemID(ctx, workItemID)
	if err != nil {
		return fmt.Errorf("list session review artifacts: %w", err)
	}

	for _, artifact := range artifacts {
		switch artifact.Provider {
		case "gitlab":
			// Delete MR reviews and checks first (FK dependencies)
			if err := svcs.GitlabMRReviews.DeleteByMRID(ctx, artifact.ProviderArtifactID); err != nil {
				slog.Warn("failed to delete MR reviews during session delete",
					"artifact_id", artifact.ID, "mr_id", artifact.ProviderArtifactID, "error", err)
			}
			if err := svcs.GitlabMRChecks.DeleteByMRID(ctx, artifact.ProviderArtifactID); err != nil {
				slog.Warn("failed to delete MR checks during session delete",
					"artifact_id", artifact.ID, "mr_id", artifact.ProviderArtifactID, "error", err)
			}
			// Delete the MR itself
			if err := svcs.GitlabMRs.Delete(ctx, artifact.ProviderArtifactID); err != nil {
				slog.Warn("failed to delete MR during session delete",
					"artifact_id", artifact.ID, "mr_id", artifact.ProviderArtifactID, "error", err)
			}
		case "github":
			// Delete PR reviews and checks first (FK dependencies)
			if err := svcs.GithubPRReviews.DeleteByPRID(ctx, artifact.ProviderArtifactID); err != nil {
				slog.Warn("failed to delete PR reviews during session delete",
					"artifact_id", artifact.ID, "pr_id", artifact.ProviderArtifactID, "error", err)
			}
			if err := svcs.GithubPRChecks.DeleteByPRID(ctx, artifact.ProviderArtifactID); err != nil {
				slog.Warn("failed to delete PR checks during session delete",
					"artifact_id", artifact.ID, "pr_id", artifact.ProviderArtifactID, "error", err)
			}
			// Delete the PR itself
			if err := svcs.GithubPRs.Delete(ctx, artifact.ProviderArtifactID); err != nil {
				slog.Warn("failed to delete PR during session delete",
					"artifact_id", artifact.ID, "pr_id", artifact.ProviderArtifactID, "error", err)
			}
		}
	}

	// Delete the session review artifacts themselves
	if err := svcs.SessionArtifacts.DeleteByWorkItemID(ctx, workItemID); err != nil {
		return fmt.Errorf("delete session review artifacts: %w", err)
	}

	return nil
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

func persistCreatedWorkItemMsg(provider ServiceProvider, wi domain.Session) tea.Msg {
	svcs := provider.GetServices()
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

func createManualSessionCmd(provider ServiceProvider, msg NewSessionManualMsg) tea.Cmd {
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
		return persistCreatedWorkItemMsg(provider, wi)
	}
}

func createBrowseSessionCmd(provider ServiceProvider, msg NewSessionBrowseMsg) tea.Cmd {
	return func() tea.Msg {
		if msg.Adapter == nil {
			return ErrMsg{Err: errors.New("no adapter available")}
		}
		wi, err := msg.Adapter.Resolve(context.Background(), msg.Selection)
		if err != nil {
			return ErrMsg{Err: err}
		}
		wi.ExtraContext = msg.ExtraContext
		return persistCreatedWorkItemMsg(provider, wi)
	}
}

// formatOperationErrorToast converts operational errors into concise, actionable toast copy.
func formatOperationErrorToast(err error) string {
	// Check for CategorizedError first - this is the preferred path for typed errors.
	var catErr *adapter.CategorizedError
	if errors.As(err, &catErr) {
		return lookupMessage(catErr.Category, catErr.Provider, catErr.Resource, err.Error())
	}

	// Legacy: Check for GitHub-specific invalid search error.
	if IsGitHubInvalidSearchError(err) {
		return "Error: GitHub can't search one or more selected owners/repos.\nCheck the Owner/Repo filters or your repository access."
	}

	// Fallback: show the raw error.
	return "Error: " + err.Error()
}

// upsertQuestion adds or updates a question in the nested map.
func (a *App) upsertQuestion(sessionID string, question domain.Question) {
	if a.questions[sessionID] == nil {
		a.questions[sessionID] = make(map[string]domain.Question)
	}
	a.questions[sessionID][question.ID] = question
}

// removeQuestion removes a question from the cache. If the session has no more questions,
// the session key is removed from the map.
func (a *App) removeQuestion(sessionID, questionID string) {
	if sessionQuestions, ok := a.questions[sessionID]; ok {
		delete(sessionQuestions, questionID)
		if len(sessionQuestions) == 0 {
			delete(a.questions, sessionID)
		}
	}
}

// extractReviewSessionID extracts the session ID from review-related messages.
func extractReviewSessionID(msg tea.Msg) string {
	switch m := msg.(type) {
	case ReviewStartedMsg:
		return m.SessionID
	case ReviewCompletedMsg:
		return m.SessionID
	case CritiquesFoundMsg:
		return m.SessionID
	case ReimplementationStartedMsg:
		return m.SessionID
	}
	return ""
}

// extractWorkItemID extracts the work_item_id from an event payload.
func extractWorkItemID(payload string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(payload), &m); err != nil {
		return ""
	}
	if id, ok := m["work_item_id"].(string); ok {
		return id
	}
	return ""
}

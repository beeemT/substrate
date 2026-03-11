package views

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/repository"
	"github.com/beeemT/substrate/internal/service"
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

// App is the top-level bubbletea model.
type App struct {
	svcs Services

	// Layout sub-models
	sidebar   SidebarModel
	content   ContentModel
	statusBar StatusBarModel

	// Overlays
	activeOverlay  overlayKind
	newSession     NewSessionOverlay
	sessionSearch  SessionSearchOverlay
	settingsPage   SettingsPage
	workspaceModal WorkspaceInitModal
	helpOverlay    HelpOverlay
	hasWorkspace   bool

	// Toasts
	toasts components.ToastModel

	// State cache (refreshed by DB poll)
	workItems []domain.WorkItem
	sessions  []domain.AgentSession
	subPlans  map[string][]domain.SubPlan  // keyed by planID
	plans     map[string]*domain.Plan      // keyed by workItemID
	questions map[string][]domain.Question // keyed by sessionID
	reviews   map[string]ReviewsLoadedMsg  // keyed by sessionID

	// Log tailing deduplication
	tailingSessionIDs map[string]bool
	prevContentMode   ContentMode
	// reviewSessionLogs maps implementation session ID → review agent log path.
	// Populated when RunReviewSessionCmd returns a ReviewCompleteMsg.
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
	taskSessionSelectionByWorkItem map[string]string

	// Session log tailing
	sessionsDir string

	// Terminal size
	windowWidth  int
	windowHeight int

	// Foreman lifecycle
	foremanPlanID string // plan ID the Foreman was last started for
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
		toasts:                         components.NewToastModel(st),
		subPlans:                       make(map[string][]domain.SubPlan),
		plans:                          make(map[string]*domain.Plan),
		questions:                      make(map[string][]domain.Question),
		reviews:                        make(map[string]ReviewsLoadedMsg),
		tailingSessionIDs:              make(map[string]bool),
		liveInstanceIDs:                make(map[string]bool),
		reviewSessionLogs:              make(map[string]string),
		taskSessionSelectionByWorkItem: make(map[string]string),
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
	p := tea.NewProgram(app, tea.WithAltScreen())
	_, err := p.Run()
	return err
}

// Init returns the initial set of commands.
func (a App) Init() tea.Cmd {
	var cmds []tea.Cmd

	cmds = append(cmds, PollTickCmd(), HeartbeatTickCmd(), components.ToastTickCmd())

	if a.svcs.WorkspaceID != "" {
		cmds = append(cmds,
			LoadWorkItemsCmd(a.svcs.WorkItem, a.svcs.WorkspaceID),
			LoadSessionsCmd(a.svcs.Session, a.svcs.WorkspaceID),
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

func (a *App) runSessionSearch(showLoading bool) tea.Cmd {
	if !a.sessionSearch.Active() {
		return nil
	}
	if showLoading {
		a.sessionSearch.SetLoading(true)
	}
	return SearchSessionHistoryCmd(a.svcs.Session, a.sessionSearchFilter())
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
		hints := append([]KeybindHint{{Key: "←", Label: "Back"}}, a.content.KeybindHints()...)
		return append(prependDelete(hints), global...)
	}
	if a.sidebarMode == sidebarPaneTasks {
		return append(prependDelete([]KeybindHint{{Key: "↑/↓", Label: "Tasks"}, {Key: "→", Label: "Content"}, {Key: "←", Label: "Sessions"}}), global...)
	}
	return append(prependDelete([]KeybindHint{{Key: "↑/↓", Label: "Sessions"}, {Key: "→", Label: "Tasks"}}), global...)
}

func (a App) historyEntryTitle(entry SidebarEntry) string {
	if entry.ExternalID != "" && entry.Title != "" {
		return entry.ExternalID + " · " + entry.Title
	}
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

func (a App) workItemByID(workItemID string) *domain.WorkItem {
	for i := range a.workItems {
		if a.workItems[i].ID == workItemID {
			return &a.workItems[i]
		}
	}
	return nil
}

func (a *App) upsertWorkItem(workItem domain.WorkItem) {
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

func (a App) sessionByID(sessionID string) *domain.AgentSession {
	for i := range a.sessions {
		if a.sessions[i].ID == sessionID {
			return &a.sessions[i]
		}
	}
	return nil
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
	if a.currentHistorySessionID != "" {
		return a.currentHistorySessionID
	}
	if a.sidebarMode != sidebarPaneTasks {
		return ""
	}
	sessionID := a.selectedTaskSessionID()
	if sessionID == "" || sessionID == taskSidebarSourceDetailsID {
		return ""
	}
	if a.workItemTaskSession(a.currentWorkItemID, sessionID) == nil {
		return ""
	}
	return sessionID
}

func (a *App) enterTaskSidebar() tea.Cmd {
	if a.currentWorkItemID == "" {
		return nil
	}
	a.sidebarMode = sidebarPaneTasks
	a.mainFocus = mainFocusSidebar
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
	for _, session := range a.sessionsForWorkItem(workItemID) {
		if session.ID == sessionID {
			s := session
			return &s
		}
	}
	return nil
}

func (a App) historyEntrySummaryLines(entry SidebarEntry) []string {
	lines := []string{
		"No agent-session log is available for this work item yet.",
		"",
		"State: " + entry.Subtitle(),
	}
	if entry.WorkspaceName != "" {
		lines = append(lines, "Workspace: "+entry.WorkspaceName)
	}
	if entry.RepositoryName != "" {
		lines = append(lines, "Latest repo: "+entry.RepositoryName)
	}
	return lines
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
		layout := styles.ComputeMainPageLayout(msg.Width, msg.Height, SidebarWidth, a.statusBar.styles.Chrome)
		a.sidebar.SetWidth(layout.SidebarInnerWidth)
		a.sidebar.SetHeight(layout.PaneInnerHeight)
		a.content.SetSize(layout.ContentInnerWidth, layout.PaneInnerHeight)
		a.workspaceModal.SetSize(msg.Width, msg.Height)
		a.newSession.SetSize(msg.Width, msg.Height)
		a.sessionSearch.SetSize(msg.Width, msg.Height)
		a.settingsPage.SetSize(msg.Width, msg.Height)
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
				LoadWorkItemsCmd(a.svcs.WorkItem, a.svcs.WorkspaceID),
				LoadSessionsCmd(a.svcs.Session, a.svcs.WorkspaceID),
				LoadLiveInstancesCmd(a.svcs.Instance, a.svcs.WorkspaceID),
			)
		}
		return a, tea.Batch(cmds...)

	case WorkspaceCancelMsg:
		return a, tea.Quit

	case CloseOverlayMsg:
		a.activeOverlay = overlayNone
		a.newSession.Close()
		a.sessionSearch.Close()
		a.settingsPage.Close()
		return a, nil

	case PollTickMsg:
		a.toasts.Prune()
		if a.svcs.WorkspaceID != "" {
			cmds = append(cmds,
				LoadWorkItemsCmd(a.svcs.WorkItem, a.svcs.WorkspaceID),
				LoadSessionsCmd(a.svcs.Session, a.svcs.WorkspaceID),
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

	case WorkItemsLoadedMsg:
		if msg.WorkspaceID != a.svcs.WorkspaceID {
			return a, nil
		}
		a.workItems = msg.Items
		a.rebuildSidebar()
		return a, nil

	case SessionsLoadedMsg:
		if msg.WorkspaceID != a.svcs.WorkspaceID {
			return a, nil
		}
		a.sessions = msg.Sessions
		a.rebuildSidebar()
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
		a.sessionSearch.SetEntries(msg.Entries)
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
		a.content.SetSessionInteraction(a.historyEntryTitle(a.currentHistoryEntry), a.historyEntryMeta(a.currentHistoryEntry), msg.Lines)
		return a, nil

	case PlanLoadedMsg:
		a.plans[msg.WorkItemID] = msg.Plan
		if msg.Plan != nil {
			a.subPlans[msg.Plan.ID] = msg.SubPlans
		}
		a.rebuildSidebar()
		if a.currentWorkItemID == msg.WorkItemID {
			cmds = append(cmds, a.updateContentFromState())
		}
		return a, tea.Batch(cmds...)

	case QuestionsLoadedMsg:
		a.questions[msg.SessionID] = msg.Questions
		a.rebuildSidebar()
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
		cmds = append(cmds, SavePlanCmd(a.svcs.Plan, msg.PlanID, msg.NewContent))
		a.toasts.AddToast("Plan saved — review changes", components.ToastInfo)
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
			func() tea.Msg { return OverrideAcceptMsg{WorkItemID: msg.WorkItemID} },
		)
		return a, nil

	case StartPlanMsg:
		if a.svcs.Planning != nil {
			cmds = append(cmds, StartPlanningCmd(a.svcs.Planning, msg.WorkItemID))
		} else {
			a.toasts.AddToast("Planning service not configured", components.ToastError)
		}
		return a, tea.Batch(cmds...)

	case PlanApproveMsg:
		cmds = append(cmds, ApprovePlanCmd(a.svcs.WorkItem, a.svcs.Plan, a.svcs.Bus, msg.PlanID, msg.WorkItemID))
		return a, tea.Batch(cmds...)

	case PlanApprovedMsg:
		if a.svcs.Implementation != nil {
			cmds = append(cmds, RunImplementationCmd(a.svcs.Implementation, msg.PlanID))
		}
		if a.svcs.Foreman != nil {
			a.foremanPlanID = msg.PlanID
			cmds = append(cmds, StartForemanCmd(a.svcs.Foreman, msg.PlanID))
		}
		return a, tea.Batch(cmds...)

	case PlanRequestChangesMsg:
		if a.svcs.Planning != nil {
			cmds = append(cmds, PlanWithFeedbackCmd(a.svcs.Planning, a.currentWorkItemID, msg.PlanID, msg.Feedback))
		} else {
			a.toasts.AddToast("Plan revision requested (no planning service)", components.ToastInfo)
		}
		return a, tea.Batch(cmds...)

	case PlanRejectMsg:
		cmds = append(cmds, RejectPlanCmd(a.svcs.WorkItem, a.svcs.Plan, msg.WorkItemID, msg.PlanID, msg.Reason))
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

	case SkipQuestionMsg:
		cmds = append(cmds, SkipQuestionCmd(a.svcs.Question, a.svcs.Foreman, msg.QuestionID))
		return a, tea.Batch(cmds...)

	case ResumeSessionMsg:
		if a.svcs.Resumption != nil {
			cmds = append(cmds, ResumeSessionCmd(a.svcs.Resumption, a.svcs.Session, msg.OldSessionID, a.svcs.InstanceID))
		} else {
			a.toasts.AddToast("Resume not available (no resumption service)", components.ToastError)
		}
		return a, tea.Batch(cmds...)

	case AbandonSessionMsg:
		cmds = append(cmds, abandonSessionCmd(a.svcs.Session, msg.SessionID))
		return a, tea.Batch(cmds...)

	case DeleteSessionMsg:
		cmds = append(cmds, deleteSessionCmd(a.svcs.Session, a.sessionsDir, msg.SessionID, a.reviewSessionLogs[msg.SessionID]))
		return a, tea.Batch(cmds...)

	case ReimplementMsg:
		if a.svcs.Implementation != nil {
			if plan := a.plans[msg.WorkItemID]; plan != nil {
				cmds = append(cmds, RunImplementationCmd(a.svcs.Implementation, plan.ID))
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

	case OverrideAcceptMsg:
		cmds = append(cmds, OverrideAcceptCmd(a.svcs.WorkItem, a.svcs.Plan, a.svcs.Session, a.svcs.Bus, msg.WorkItemID))
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
	case SettingsSectionPatchedMsg:
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
					a.content.question.SetQuestion(q, msg.NewProposal, msg.Uncertain)
					break questionLoop
				}
			}
		}
		return a, nil

	case WorkItemCreatedMsg:
		a.toasts.AddToast(msg.Message, components.ToastSuccess)
		if a.svcs.Planning != nil {
			msg.WorkItem.State = domain.WorkItemPlanning
		}
		a.upsertWorkItem(msg.WorkItem)
		cmds = append(cmds, a.focusWorkItemOverview(msg.WorkItem.ID))
		if a.svcs.Planning != nil {
			cmds = append(cmds, StartPlanningCmd(a.svcs.Planning, msg.WorkItem.ID))
		} else {
			a.toasts.AddToast("Planning service not configured", components.ToastError)
		}
		return a, tea.Batch(cmds...)

	case WorkItemDuplicatePromptMsg:
		a.showDuplicateSessionDialog(msg.RequestedWorkItem, msg.ExistingWorkItem)
		return a, nil
	case WorkItemDuplicateActionMsg:
		if !a.duplicateSessionActive {
			return a, nil
		}
		existing := a.duplicateSession.ExistingWorkItem
		label := workItemDisplayLabel(existing)
		a.closeDuplicateSessionDialog()
		switch msg.Action {
		case WorkItemDuplicateCancel:
			return a, nil
		case WorkItemDuplicateOpenExisting:
			a.toasts.AddToast("Opened existing item "+label, components.ToastInfo)
			a.upsertWorkItem(existing)
			return a, a.focusWorkItemOverview(existing.ID)
		case WorkItemDuplicateCreateSession:
			a.toasts.AddToast("Starting planning with existing item "+label, components.ToastInfo)
			if a.svcs.Planning != nil && existing.State == domain.WorkItemIngested {
				existing.State = domain.WorkItemPlanning
			}
			a.upsertWorkItem(existing)
			cmds = append(cmds, a.focusWorkItemOverview(existing.ID))
			if a.svcs.Planning != nil {
				cmds = append(cmds, StartPlanningCmd(a.svcs.Planning, existing.ID))
			} else {
				a.toasts.AddToast("Planning service not configured", components.ToastError)
			}
			return a, tea.Batch(cmds...)
		default:
			return a, nil
		}

	case ActionDoneMsg:
		a.toasts.AddToast(msg.Message, components.ToastSuccess)
		return a, nil

	case ImplementationCompleteMsg:
		a.toasts.AddToast("Implementation complete", components.ToastSuccess)
		if a.svcs.ReviewPipeline != nil {
			for _, sID := range msg.SessionIDs {
				cmds = append(cmds, RunReviewSessionCmd(a.svcs.ReviewPipeline, a.svcs.Session, sID))
			}
		}
		if a.svcs.Foreman != nil {
			cmds = append(cmds, StopForemanCmd(a.svcs.Foreman))
		}
		return a, tea.Batch(cmds...)

	case ReviewCompleteMsg:
		// Store the review log path; WorkItemReviewing updateContentFromState will tail it.
		if msg.ReviewSessionID != "" && a.sessionsDir != "" {
			a.reviewSessionLogs[msg.ImplSessionID] = filepath.Join(a.sessionsDir, msg.ReviewSessionID+".log")
		}
		// Trigger a content refresh so tailing starts immediately if still on reviewing view.
		if a.currentWorkItemID != "" {
			cmds = append(cmds, a.updateContentFromState())
		}
		return a, tea.Batch(cmds...)

	case SessionDeletedMsg:
		delete(a.questions, msg.SessionID)
		delete(a.reviews, msg.SessionID)
		delete(a.reviewSessionLogs, msg.SessionID)
		delete(a.tailingSessionIDs, msg.SessionID)
		delete(a.tailingSessionIDs, "review-"+msg.SessionID)
		filtered := a.sessions[:0]
		for _, session := range a.sessions {
			if session.ID != msg.SessionID {
				filtered = append(filtered, session)
			}
		}
		a.sessions = filtered
		if a.selectedTaskSessionID() == msg.SessionID {
			a.setSelectedTaskSessionID("")
		}
		if a.currentHistorySessionID == msg.SessionID {
			a.currentHistorySessionID = ""
			a.currentHistoryEntry.SessionID = ""
			a.content.SetSessionInteraction(a.historyEntryTitle(a.currentHistoryEntry), a.historyEntryMeta(a.currentHistoryEntry), a.historyEntrySummaryLines(a.currentHistoryEntry))
		}
		a.toasts.AddToast(msg.Message, components.ToastSuccess)
		if a.svcs.WorkspaceID != "" {
			cmds = append(cmds,
				LoadWorkItemsCmd(a.svcs.WorkItem, a.svcs.WorkspaceID),
				LoadSessionsCmd(a.svcs.Session, a.svcs.WorkspaceID),
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
		a.toasts.AddToast("Error: "+msg.Err.Error(), components.ToastError)
		return a, nil

	case tea.KeyMsg:
		return a.handleKeyMsg(msg)
	}

	// Route to active overlay or content.
	if a.activeOverlay == overlayWorkspaceInit {
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
	} else {
		a.content, cmd = a.content.Update(msg)
		cmds = append(cmds, cmd)
	}

	return a, tea.Batch(cmds...)
}

func (a App) handleKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	var cmd tea.Cmd

	// Confirm dialog captures all key input when active.
	if a.confirmActive {
		switch msg.String() {
		case "y", "enter":
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
		if a.svcs.InstanceID != "" {
			return a, tea.Batch(DeleteInstanceCmd(a.svcs.Instance, a.svcs.InstanceID), tea.Quit)
		}
		return a, tea.Quit
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

	switch msg.String() {
	case "q":
		if a.svcs.InstanceID != "" {
			return a, tea.Batch(DeleteInstanceCmd(a.svcs.Instance, a.svcs.InstanceID), tea.Quit)
		}
		return a, tea.Quit
	case "n":
		return a, a.openNewSession()
	case "c":
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
	case "esc":
		if a.activeOverlay != overlayNone {
			a.activeOverlay = overlayNone
			a.newSession.Close()
			a.sessionSearch.Close()
			a.settingsPage.Close()
			return a, nil
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
	case "left":
		if a.mainFocus == mainFocusContent {
			a.mainFocus = mainFocusSidebar
			return a, nil
		}
		if a.sidebarMode == sidebarPaneTasks {
			return a, a.exitTaskSidebar()
		}
	case "up", "k":
		if a.mainFocus == mainFocusContent {
			a.content, cmd = a.content.Update(msg)
			return a, cmd
		}
		a.sidebar.MoveUp()
		cmd = a.onSidebarMove()
		return a, cmd
	case "down", "j":
		if a.mainFocus == mainFocusContent {
			a.content, cmd = a.content.Update(msg)
			return a, cmd
		}
		a.sidebar.MoveDown()
		cmd = a.onSidebarMove()
		return a, cmd
	case "g":
		if a.mainFocus == mainFocusContent {
			a.content, cmd = a.content.Update(msg)
			return a, cmd
		}
		a.sidebar.GotoTop()
		cmd = a.onSidebarMove()
		return a, cmd
	case "G":
		if a.mainFocus == mainFocusContent {
			a.content, cmd = a.content.Update(msg)
			return a, cmd
		}
		a.sidebar.GotoBottom()
		cmd = a.onSidebarMove()
		return a, cmd
	case "?":
		a.activeOverlay = overlayHelp
		return a, nil
	}

	a.content, cmd = a.content.Update(msg)
	cmds = append(cmds, cmd)
	return a, tea.Batch(cmds...)
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
	showSourceDetails := false
	if a.sidebarMode == sidebarPaneTasks {
		if taskSessionID := a.selectedTaskSessionID(); taskSessionID != "" {
			if taskSessionID == taskSidebarSourceDetailsID {
				showSourceDetails = true
			} else if session := a.workItemTaskSession(a.currentWorkItemID, taskSessionID); session != nil {
				return a.showTaskContent(wi, session)
			} else {
				a.setSelectedTaskSessionID("")
			}
		}
	}

	switch wi.State {
	case domain.WorkItemIngested:
		a.content.SetMode(ContentModeReadyToPlan)

	case domain.WorkItemPlanning:
		a.content.SetMode(ContentModePlanning)
		a.content.sessionLog.SetModeLabel("Planning")
		a.content.sessionLog.SetMeta("")
		a.content.sessionLog.SetStaticContent([]string{
			"Planning has started for this work item.",
			"",
			"Repository agent sessions appear after the plan is approved.",
		})
	case domain.WorkItemPlanReview:
		a.content.SetMode(ContentModePlanReview)
		if plan := a.plans[wi.ID]; plan != nil {
			a.content.planReview.SetPlan(*plan)
			a.content.planReview.SetWorkItemID(wi.ID)
		}

	case domain.WorkItemApproved:
		a.content.SetMode(ContentModeAwaitingImpl)

	case domain.WorkItemImplementing:
		plan := a.plans[wi.ID]
		var activeSessions []domain.AgentSession
		if plan != nil {
			for _, sp := range a.subPlans[plan.ID] {
				for _, s := range a.sessions {
					if s.SubPlanID == sp.ID {
						activeSessions = append(activeSessions, s)
					}
				}
			}
		}
		for _, s := range activeSessions {
			if s.Status == domain.AgentSessionWaitingForAnswer {
				for _, q := range a.questions[s.ID] {
					if q.Status == domain.QuestionEscalated {
						a.content.SetMode(ContentModeQuestion)
						a.content.question.SetQuestion(q, q.ProposedAnswer, q.ProposedAnswer == "")
						return nil
					}
				}
			}
		}
		for _, s := range activeSessions {
			if s.Status == domain.AgentSessionInterrupted {
				a.content.SetMode(ContentModeInterrupted)
				a.content.interrupted.SetSession(s.ID, s.SubPlanID, s.RepositoryName, s.WorktreePath, a.canActOnSession(s))
				return nil
			}
		}
		if showSourceDetails {
			a.content.SetMode(ContentModeSourceDetails)
			return nil
		}
		a.content.SetMode(ContentModeImplementing)
		if plan != nil {
			repos := a.buildRepoProgress(plan)
			a.content.implementing.SetRepos(repos)
			var tailCmds []tea.Cmd
			for _, r := range repos {
				if r.LogPath != "" && r.SessionID != "" && !a.tailingSessionIDs[r.SessionID] {
					a.tailingSessionIDs[r.SessionID] = true
					tailCmds = append(tailCmds, TailSessionLogCmd(r.LogPath, r.SessionID, 0))
				}
			}
			if len(tailCmds) > 0 {
				return tea.Batch(tailCmds...)
			}
		}

	case domain.WorkItemReviewing:
		a.content.SetMode(ContentModeReviewing)
		var repoResults []RepoReviewResult
		if plan := a.plans[wi.ID]; plan != nil {
			for _, sp := range a.subPlans[plan.ID] {
				var sessionID string
				for _, s := range a.sessions {
					if s.SubPlanID == sp.ID {
						sessionID = s.ID
					}
				}
				if sessionID == "" {
					continue
				}
				rev := a.reviews[sessionID]
				var crits []domain.Critique
				for _, cs := range rev.Critiques {
					crits = append(crits, cs...)
				}
				repoResults = append(repoResults, RepoReviewResult{
					RepoName:  sp.RepositoryName,
					Cycles:    rev.Cycles,
					Critiques: crits,
				})
			}
		}
		a.content.reviewing.SetRepos(repoResults)
		a.content.reviewing.SetWorkItemID(wi.ID)
		var tailCmds []tea.Cmd
		if plan := a.plans[wi.ID]; plan != nil {
			for _, sp := range a.subPlans[plan.ID] {
				for _, s := range a.sessions {
					if s.SubPlanID != sp.ID {
						continue
					}
					if logPath, ok := a.reviewSessionLogs[s.ID]; ok {
						reviewTailID := "review-" + s.ID
						if !a.tailingSessionIDs[reviewTailID] {
							a.tailingSessionIDs[reviewTailID] = true
							tailCmds = append(tailCmds, TailSessionLogCmd(logPath, reviewTailID, 0))
						}
					}
				}
			}
		}
		if len(tailCmds) > 0 {
			return tea.Batch(tailCmds...)
		}

	case domain.WorkItemCompleted:
		a.content.SetMode(ContentModeCompleted)
		a.content.completed.SetData(wi.UpdatedAt, nil, nil)

	case domain.WorkItemFailed:
		a.content.SetMode(ContentModeFailed)
		a.content.failed.SetFailure("Work item failed", "")
	}

	if prevMode != a.content.mode {
		if prevMode == ContentModePlanning || prevMode == ContentModeImplementing || prevMode == ContentModeSessionInteraction {
			a.tailingSessionIDs = make(map[string]bool)
		}
	}
	return nil
}

func (a *App) showTaskContent(wi *domain.WorkItem, session *domain.AgentSession) tea.Cmd {
	title := firstNonEmptyString(wi.ExternalID, wi.ID) + " · " + firstNonEmptyString(session.RepositoryName, "Task")
	metaParts := []string{sessionStatusLabel(session.Status)}
	if session.HarnessName != "" {
		metaParts = append(metaParts, session.HarnessName)
	}
	metaParts = append(metaParts, session.ID)
	a.content.SetMode(ContentModeSessionInteraction)
	a.content.sessionLog.SetTitle(title)
	a.content.sessionLog.SetModeLabel("Task")
	a.content.sessionLog.SetMeta(strings.Join(metaParts, " · "))
	logPath := filepath.Join(a.sessionsDir, session.ID+".log")
	a.content.sessionLog.SetLogPath(session.ID, logPath)
	if !a.tailingSessionIDs[session.ID] {
		a.tailingSessionIDs[session.ID] = true
		return TailSessionLogCmd(logPath, session.ID, 0)
	}
	return nil
}

func (a *App) buildRepoProgress(plan *domain.Plan) []RepoProgress {
	if plan == nil {
		return nil
	}
	var repos []RepoProgress
	for _, sp := range a.subPlans[plan.ID] {
		rp := RepoProgress{
			Name:      sp.RepositoryName,
			SubPlanID: sp.ID,
			Status:    sp.Status,
		}
		for _, s := range a.sessions {
			if s.SubPlanID == sp.ID && s.Status == domain.AgentSessionRunning {
				rp.SessionID = s.ID
				rp.LogPath = filepath.Join(a.sessionsDir, s.ID+".log")
			}
		}
		repos = append(repos, rp)
	}
	return repos
}

func (a *App) canActOnSession(s domain.AgentSession) bool {
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
	a.showConfirm("Delete Session",
		"Delete this agent session, its review data, questions, and logs?",
		func() tea.Msg { return DeleteSessionMsg{SessionID: sID} },
	)
}

func (a App) sidebarEntryFromWorkItem(wi domain.WorkItem) SidebarEntry {
	entry := SidebarEntry{
		Kind:         SidebarEntryWorkItem,
		WorkItemID:   wi.ID,
		ExternalID:   wi.ExternalID,
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

func (a App) sessionsForWorkItem(workItemID string) []domain.AgentSession {
	plan := a.plans[workItemID]
	if plan == nil {
		return nil
	}
	subPlanOrder := make(map[string]int, len(a.subPlans[plan.ID]))
	for i, sp := range a.subPlans[plan.ID] {
		subPlanOrder[sp.ID] = i
	}
	sessions := make([]domain.AgentSession, 0)
	for _, s := range a.sessions {
		if _, ok := subPlanOrder[s.SubPlanID]; ok {
			sessions = append(sessions, s)
		}
	}
	sort.SliceStable(sessions, func(i, j int) bool {
		orderI, okI := subPlanOrder[sessions[i].SubPlanID]
		orderJ, okJ := subPlanOrder[sessions[j].SubPlanID]
		if okI && okJ && orderI != orderJ {
			return orderI < orderJ
		}
		if okI != okJ {
			return okI
		}
		if !sessions[i].UpdatedAt.Equal(sessions[j].UpdatedAt) {
			return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
		}
		return sessions[i].ID < sessions[j].ID
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
	if workItemHasSourceDetails(wi) {
		entries = append(entries, SidebarEntry{
			Kind:         SidebarEntryTaskSourceDetails,
			WorkItemID:   workItemID,
			SessionID:    taskSidebarSourceDetailsID,
			ExternalID:   wi.ExternalID,
			Title:        "Source details",
			SubtitleText: workItemSourceSidebarSubtitle(wi),
			LastActivity: wi.UpdatedAt,
		})
	}
	for _, session := range a.sessionsForWorkItem(workItemID) {
		entries = append(entries, SidebarEntry{
			Kind:           SidebarEntryTaskSession,
			WorkItemID:     workItemID,
			SessionID:      session.ID,
			Title:          "Session " + shortSessionID(session.ID),
			State:          wi.State,
			SessionStatus:  session.Status,
			RepositoryName: session.RepositoryName,
			LastActivity:   session.UpdatedAt,
		})
	}
	return entries
}

func (a *App) rebuildSidebar() {
	if a.sidebarMode == sidebarPaneTasks && a.currentWorkItemID != "" && a.workItemByID(a.currentWorkItemID) != nil {
		wi := a.workItemByID(a.currentWorkItemID)
		a.sidebar.SetTitle(firstNonEmptyString(wi.ExternalID, wi.ID) + " · Tasks")
		a.sidebar.SetEntries(a.taskSidebarEntries(a.currentWorkItemID))
		if !a.sidebar.SelectEntry(a.currentWorkItemID, a.selectedTaskSessionID()) {
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

	sidebarContent := lipgloss.NewStyle().
		Width(layout.SidebarInnerWidth).
		Height(layout.PaneInnerHeight).
		Render(fitViewBox(a.sidebar.View(), layout.SidebarInnerWidth, layout.PaneInnerHeight))
	sidebarPane := components.RenderPane(a.statusBar.styles, components.PaneSpec{
		Content: sidebarContent,
		Width:   layout.SidebarPaneWidth,
		Height:  layout.BodyHeight,
	})

	contentContent := lipgloss.NewStyle().
		Width(layout.ContentInnerWidth).
		Height(layout.PaneInnerHeight).
		Render(fitViewBox(a.content.View(), layout.ContentInnerWidth, layout.PaneInnerHeight))
	contentPane := components.RenderPane(a.statusBar.styles, components.PaneSpec{
		Content: contentContent,
		Width:   layout.ContentPaneWidth,
		Height:  layout.BodyHeight,
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

	toastView := ""
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

	return base
}

func mainPageLayoutMetrics(totalWidth, totalHeight int) (sidebarPaneWidth, contentPaneWidth, bodyHeight, paneInnerHeight int) {
	layout := styles.ComputeMainPageLayout(totalWidth, totalHeight, SidebarWidth, styles.DefaultChromeMetrics)
	return layout.SidebarPaneWidth, layout.ContentPaneWidth, layout.BodyHeight, layout.PaneInnerHeight
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
	if maxStart < 0 {
		maxStart = 0
	}
	if start > maxStart {
		start = maxStart
	}
	if start < 0 {
		start = 0
	}

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

func abandonSessionCmd(svc *service.SessionService, sessionID string) tea.Cmd {
	return func() tea.Msg {
		if err := svc.Fail(context.Background(), sessionID, nil); err != nil {
			return ErrMsg{Err: err}
		}
		return ActionDoneMsg{Message: "Session abandoned"}
	}
}

func deleteSessionCmd(svc *service.SessionService, sessionsDir, sessionID, reviewLogPath string) tea.Cmd {
	return func() tea.Msg {
		if err := svc.Delete(context.Background(), sessionID); err != nil {
			return ErrMsg{Err: err}
		}
		message := "Session deleted"
		if err := deleteSessionArtifacts(sessionsDir, sessionID, reviewLogPath); err != nil {
			message = "Session deleted; some logs could not be removed"
		}
		return SessionDeletedMsg{SessionID: sessionID, Message: message}
	}
}

func deleteSessionArtifacts(sessionsDir, sessionID, reviewLogPath string) error {
	var errs []error
	for _, deleteID := range deleteSessionArtifactIDs(sessionsDir, sessionID, reviewLogPath) {
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

func deleteSessionArtifactIDs(sessionsDir, sessionID, reviewLogPath string) []string {
	if strings.TrimSpace(sessionsDir) == "" || strings.TrimSpace(sessionID) == "" {
		return nil
	}
	ids := []string{sessionID}
	if strings.TrimSpace(reviewLogPath) == "" {
		return ids
	}
	reviewLogPath = filepath.Clean(reviewLogPath)
	if filepath.Dir(reviewLogPath) != filepath.Clean(sessionsDir) {
		return ids
	}
	base := filepath.Base(reviewLogPath)
	if strings.HasSuffix(base, ".log") {
		reviewID := strings.TrimSuffix(base, ".log")
		if reviewID != "" && reviewID != sessionID {
			ids = append(ids, reviewID)
		}
	}
	return ids
}

func workItemDisplayLabel(wi domain.WorkItem) string {
	label := strings.TrimSpace(wi.ExternalID)
	if label == "" {
		label = strings.TrimSpace(wi.Title)
	}
	if label == "" {
		label = wi.ID
	}
	return label
}

func existingWorkItemByExternalID(svc *service.WorkItemService, workspaceID, externalID string) (domain.WorkItem, error) {
	trimmedWorkspaceID := strings.TrimSpace(workspaceID)
	if trimmedWorkspaceID == "" {
		return domain.WorkItem{}, fmt.Errorf("workspace not configured")
	}
	trimmedExternalID := strings.TrimSpace(externalID)
	if trimmedExternalID == "" {
		return domain.WorkItem{}, fmt.Errorf("work item external_id is required")
	}
	items, err := svc.List(context.Background(), repository.WorkItemFilter{
		WorkspaceID: &trimmedWorkspaceID,
		ExternalID:  &trimmedExternalID,
		Limit:       1,
	})
	if err != nil {
		return domain.WorkItem{}, err
	}
	if len(items) > 0 {
		return items[0], nil
	}
	return domain.WorkItem{}, service.ErrNotFound{Entity: "work item", ID: trimmedExternalID}
}

func persistCreatedWorkItemMsg(svcs Services, wi domain.WorkItem) tea.Msg {
	if svcs.WorkItem == nil {
		return ErrMsg{Err: fmt.Errorf("work item service not configured")}
	}
	if strings.TrimSpace(svcs.WorkspaceID) == "" {
		return ErrMsg{Err: fmt.Errorf("workspace not configured")}
	}
	wi.WorkspaceID = svcs.WorkspaceID
	label := workItemDisplayLabel(wi)
	if err := svcs.WorkItem.Create(context.Background(), wi); err != nil {
		var alreadyExists service.ErrAlreadyExists
		if errors.As(err, &alreadyExists) {
			duplicateID := strings.TrimSpace(alreadyExists.ID)
			if duplicateID == "" {
				duplicateID = wi.ExternalID
			}
			existing, lookupErr := existingWorkItemByExternalID(svcs.WorkItem, svcs.WorkspaceID, duplicateID)
			if lookupErr == nil {
				return WorkItemDuplicatePromptMsg{
					RequestedWorkItem: wi,
					ExistingWorkItem:  existing,
				}
			}
			return ErrMsg{Err: fmt.Errorf("work item already exists in this workspace: %s", label)}
		}
		return ErrMsg{Err: err}
	}
	return WorkItemCreatedMsg{WorkItem: wi, Message: "Session created: " + label}
}

func createManualSessionCmd(svcs Services, msg NewSessionManualMsg) tea.Cmd {
	return func() tea.Msg {
		if msg.Adapter == nil {
			return ErrMsg{Err: fmt.Errorf("no adapter available")}
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
			return ErrMsg{Err: fmt.Errorf("no adapter available")}
		}
		wi, err := msg.Adapter.Resolve(context.Background(), msg.Selection)
		if err != nil {
			return ErrMsg{Err: err}
		}
		return persistCreatedWorkItemMsg(svcs, wi)
	}
}

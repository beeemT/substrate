package views

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/service"
	"github.com/beeemT/substrate/internal/tui/components"
	"github.com/beeemT/substrate/internal/tui/styles"
)

// overlayKind identifies which overlay is active.
type overlayKind int

const (
	overlayNone overlayKind = iota
	overlayNewSession
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

	// Current selection
	currentWorkItemID       string
	currentHistorySessionID string

	// Session history search
	historySearchInput   textinput.Model
	historySearchScope   sessionHistoryScope
	historyResults       []domain.SessionHistoryEntry
	historySearchLoading bool

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
	historySearch := textinput.New()
	historySearch.Placeholder = "Search session history…"
	historySearch.CharLimit = 200
	historySearch.Blur()

	app := App{
		svcs:               svcs,
		sidebar:            NewSidebarModel(st),
		content:            NewContentModel(st),
		statusBar:          NewStatusBarModel(st),
		newSession:         NewNewSessionOverlay(svcs.Adapters, svcs.WorkspaceID, st),
		settingsPage:       NewSettingsPage(svcs.Settings, svcs.SettingsData, st),
		helpOverlay:        NewHelpOverlay(st),
		subPlans:           make(map[string][]domain.SubPlan),
		plans:              make(map[string]*domain.Plan),
		questions:          make(map[string][]domain.Question),
		reviews:            make(map[string]ReviewsLoadedMsg),
		tailingSessionIDs:  make(map[string]bool),
		liveInstanceIDs:    make(map[string]bool),
		reviewSessionLogs:  make(map[string]string),
		sessionsDir:        sessionsDir,
		hasWorkspace:       svcs.WorkspaceID != "",
		historySearchInput: historySearch,
	}

	if app.hasWorkspace {
		app.historySearchScope = sessionHistoryScopeWorkspace
	} else {
		app.historySearchScope = sessionHistoryScopeGlobal
		app.workspaceModal = NewWorkspaceInitModal(cwd, st, svcs.Workspace)
		app.activeOverlay = overlayWorkspaceInit
	}
	app.syncSidebarSearchPresentation()
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
		if a.historySearchActive() {
			cmds = append(cmds, SearchSessionHistoryCmd(a.svcs.Session, a.historySearchFilter()))
		}
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
	switch {
	case !a.hasWorkspace:
		a.historySearchScope = sessionHistoryScopeGlobal
	case a.historySearchScope == sessionHistoryScopeGlobal && !a.historySearchActive():
		a.historySearchScope = sessionHistoryScopeWorkspace
	}
	a.syncSidebarSearchPresentation()
}

func (a *App) syncSidebarSearchPresentation() {
	a.sidebar.SetSearchPresentation(SidebarSearchPresentation{
		QueryView:  a.historySearchInput.View(),
		ScopeLabel: a.historySearchScope.Label(),
		Focused:    a.historySearchInput.Focused(),
		Loading:    a.historySearchLoading,
	})
}

func (a App) historySearchActive() bool {
	return strings.TrimSpace(a.historySearchInput.Value()) != ""
}

func (a App) historySearchFilter() domain.SessionHistoryFilter {
	filter := domain.SessionHistoryFilter{
		Search: strings.TrimSpace(a.historySearchInput.Value()),
		Limit:  100,
	}
	if a.historySearchScope == sessionHistoryScopeWorkspace && a.svcs.WorkspaceID != "" {
		workspaceID := a.svcs.WorkspaceID
		filter.WorkspaceID = &workspaceID
	}
	return filter
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

func (a *App) runHistorySearch() tea.Cmd {
	if !a.historySearchActive() {
		a.historyResults = nil
		a.historySearchLoading = false
		a.syncSidebarSearchPresentation()
		a.rebuildSidebar()
		return a.onSidebarMove()
	}
	a.historySearchLoading = true
	a.syncSidebarSearchPresentation()
	return SearchSessionHistoryCmd(a.svcs.Session, a.historySearchFilter())
}

func (a *App) clearHistorySearch() tea.Cmd {
	a.historySearchInput.SetValue("")
	a.historySearchInput.Blur()
	a.currentHistorySessionID = ""
	return a.runHistorySearch()
}

func (a *App) toggleHistorySearchScope() tea.Cmd {
	if a.historySearchScope == sessionHistoryScopeWorkspace {
		a.historySearchScope = sessionHistoryScopeGlobal
	} else {
		a.historySearchScope = sessionHistoryScopeWorkspace
		if a.svcs.WorkspaceID == "" {
			a.historySearchScope = sessionHistoryScopeGlobal
		}
	}
	a.syncSidebarSearchPresentation()
	return a.runHistorySearch()
}

func (a App) currentHints() []KeybindHint {
	if a.historySearchInput.Focused() {
		return []KeybindHint{
			{Key: "Enter", Label: "Search results"},
			{Key: "Tab", Label: "Toggle scope"},
			{Key: "Esc", Label: "Clear search"},
		}
	}
	if a.historySearchActive() {
		return []KeybindHint{
			{Key: "/", Label: "Edit search"},
			{Key: "j/k", Label: "Navigate results"},
			{Key: "Esc", Label: "Clear search"},
		}
	}
	hints := a.content.KeybindHints()
	if len(hints) == 0 {
		return DefaultHints()
	}
	return hints
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
	return entry.SessionID
}

func (a App) historyEntryMeta(entry SidebarEntry) string {
	parts := []string{"Session " + entry.SessionID}
	if entry.WorkspaceName != "" {
		parts = append(parts, entry.WorkspaceName)
	}
	if entry.RepositoryName != "" {
		parts = append(parts, entry.RepositoryName)
	}
	return strings.Join(parts, " · ")
}

func (a App) sidebarEntryFromHistory(entry domain.SessionHistoryEntry) SidebarEntry {
	return SidebarEntry{
		Kind:           SidebarEntrySessionHistory,
		WorkItemID:     entry.WorkItemID,
		SessionID:      entry.SessionID,
		WorkspaceID:    entry.WorkspaceID,
		WorkspaceName:  entry.WorkspaceName,
		ExternalID:     entry.WorkItemExternalID,
		Title:          entry.WorkItemTitle,
		State:          entry.WorkItemState,
		SessionStatus:  entry.Status,
		RepositoryName: entry.RepositoryName,
	}
}

func (a App) historyEntryBySessionID(sessionID string) (SidebarEntry, bool) {
	for _, entry := range a.historyResults {
		if entry.SessionID == sessionID {
			return a.sidebarEntryFromHistory(entry), true
		}
	}
	return SidebarEntry{}, false
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

func (a *App) loadHistoryEntry(entry SidebarEntry) tea.Cmd {
	a.tailingSessionIDs = make(map[string]bool)
	if a.historyEntryIsLocal(entry) {
		a.currentHistorySessionID = ""
		a.currentWorkItemID = entry.WorkItemID
		return a.updateContentFromState()
	}
	a.currentWorkItemID = ""
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
		sidebarPaneWidth, contentPaneWidth, _, paneInnerHeight := mainPageLayoutMetrics(msg.Width, msg.Height)
		a.sidebar.SetWidth(max(0, sidebarPaneWidth-2))
		a.sidebar.SetHeight(paneInnerHeight)
		a.historySearchInput.Width = max(1, sidebarPaneWidth-12)
		a.syncSidebarSearchPresentation()
		a.content.SetSize(max(0, contentPaneWidth-2), paneInnerHeight)
		a.workspaceModal.SetSize(msg.Width, msg.Height)
		a.newSession.SetSize(msg.Width, msg.Height)
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
			if a.historySearchActive() {
				cmds = append(cmds, SearchSessionHistoryCmd(a.svcs.Session, a.historySearchFilter()))
			}
		}
		return a, tea.Batch(cmds...)

	case WorkspaceCancelMsg:
		return a, tea.Quit

	case CloseOverlayMsg:
		a.activeOverlay = overlayNone
		a.newSession.Close()
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
			if a.historySearchActive() {
				cmds = append(cmds, SearchSessionHistoryCmd(a.svcs.Session, a.historySearchFilter()))
			}
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
		a.workItems = msg.Items
		a.rebuildSidebar()
		return a, nil

	case SessionsLoadedMsg:
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
		if !a.historySearchActive() {
			cmds = append(cmds, a.updateContentFromState())
		}
		return a, tea.Batch(cmds...)

	case SessionHistoryLoadedMsg:
		if !sameSessionHistoryFilter(a.historySearchFilter(), msg.Filter) {
			return a, nil
		}
		a.historySearchLoading = false
		a.historyResults = msg.Entries
		a.syncSidebarSearchPresentation()
		a.rebuildSidebar()
		cmds = append(cmds, a.onSidebarMove())
		return a, tea.Batch(cmds...)

	case SessionInteractionLoadedMsg:
		if msg.SessionID != a.currentHistorySessionID {
			return a, nil
		}
		if entry, ok := a.historyEntryBySessionID(msg.SessionID); ok {
			a.content.SetSessionInteraction(a.historyEntryTitle(entry), a.historyEntryMeta(entry), msg.Lines)
		}
		return a, nil

	case PlanLoadedMsg:
		a.plans[msg.WorkItemID] = msg.Plan
		if msg.Plan != nil {
			a.subPlans[msg.Plan.ID] = msg.SubPlans
		}
		a.rebuildSidebar()
		if a.currentWorkItemID == msg.WorkItemID && !a.historySearchActive() {
			cmds = append(cmds, a.updateContentFromState())
		}
		return a, tea.Batch(cmds...)

	case QuestionsLoadedMsg:
		a.questions[msg.SessionID] = msg.Questions
		a.rebuildSidebar()
		if a.currentWorkItemID != "" && !a.historySearchActive() {
			cmds = append(cmds, a.updateContentFromState())
		}
		return a, tea.Batch(cmds...)

	case ReviewsLoadedMsg:
		a.reviews[msg.SessionID] = msg
		if a.currentWorkItemID != "" && !a.historySearchActive() {
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
		cmds = append(cmds, ApprovePlanCmd(a.svcs.WorkItem, a.svcs.Plan, msg.PlanID, msg.WorkItemID))
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
		cmds = append(cmds, OverrideAcceptCmd(a.svcs.WorkItem, msg.WorkItemID))
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
	if a.activeOverlay == overlaySettings {
		a.settingsPage, cmd = a.settingsPage.Update(msg, a.svcs)
		return a, cmd
	}
	if a.activeOverlay == overlayHelp {
		a.activeOverlay = overlayNone
		return a, nil
	}

	if a.historySearchInput.Focused() {
		switch msg.String() {
		case "esc":
			return a, a.clearHistorySearch()
		case "enter":
			a.historySearchInput.Blur()
			a.syncSidebarSearchPresentation()
			return a, nil
		case "tab", "shift+tab":
			return a, a.toggleHistorySearchScope()
		}
		before := a.historySearchInput.Value()
		a.historySearchInput, cmd = a.historySearchInput.Update(msg)
		a.syncSidebarSearchPresentation()
		if strings.TrimSpace(before) != strings.TrimSpace(a.historySearchInput.Value()) {
			return a, tea.Batch(cmd, a.runHistorySearch())
		}
		return a, cmd
	}

	if a.historySearchActive() && msg.String() == "esc" {
		return a, a.clearHistorySearch()
	}

	switch msg.String() {
	case "q":
		if a.svcs.InstanceID != "" {
			return a, tea.Batch(DeleteInstanceCmd(a.svcs.Instance, a.svcs.InstanceID), tea.Quit)
		}
		return a, tea.Quit
	case "n":
		a.activeOverlay = overlayNewSession
		a.newSession.Open()
		return a, nil
	case "c":
		a.activeOverlay = overlaySettings
		a.settingsPage.Open()
		return a, nil
	case "/":
		a.historySearchInput.Focus()
		a.syncSidebarSearchPresentation()
		return a, nil
	case "esc":
		if a.activeOverlay != overlayNone {
			a.activeOverlay = overlayNone
			a.newSession.Close()
			a.settingsPage.Close()
			return a, nil
		}
	case "up", "k":
		a.sidebar.MoveUp()
		cmd = a.onSidebarMove()
		return a, cmd
	case "down", "j":
		a.sidebar.MoveDown()
		cmd = a.onSidebarMove()
		return a, cmd
	case "g":
		a.sidebar.GotoTop()
		cmd = a.onSidebarMove()
		return a, cmd
	case "G":
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
		return nil
	}
	if sel.Kind == SidebarEntrySessionHistory {
		if sel.SessionID == a.currentHistorySessionID {
			return nil
		}
		return a.loadHistoryEntry(*sel)
	}
	if sel.WorkItemID == a.currentWorkItemID && a.currentHistorySessionID == "" {
		return nil
	}
	a.tailingSessionIDs = make(map[string]bool)
	a.currentHistorySessionID = ""
	a.currentWorkItemID = sel.WorkItemID
	return a.updateContentFromState()
}

func (a *App) updateContentFromState() tea.Cmd {
	prevMode := a.content.mode
	if a.currentWorkItemID == "" {
		a.content.SetMode(ContentModeEmpty)
		return nil
	}

	var wi *domain.WorkItem
	for i := range a.workItems {
		if a.workItems[i].ID == a.currentWorkItemID {
			wi = &a.workItems[i]
			break
		}
	}
	if wi == nil {
		a.content.SetMode(ContentModeEmpty)
		return nil
	}

	a.content.SetWorkItem(wi)

	switch wi.State {
	case domain.WorkItemIngested:
		a.content.SetMode(ContentModeReadyToPlan)

	case domain.WorkItemPlanning:
		a.content.SetMode(ContentModePlanning)
		a.content.sessionLog.SetModeLabel("Planning")
		a.content.sessionLog.SetMeta("")
		for _, s := range a.sessions {
			if s.Status == domain.AgentSessionRunning {
				logPath := filepath.Join(a.sessionsDir, s.ID+".log")
				a.content.sessionLog.SetLogPath(s.ID, logPath)
				if !a.tailingSessionIDs[s.ID] {
					a.tailingSessionIDs[s.ID] = true
					return TailSessionLogCmd(logPath, s.ID, 0)
				}
				break
			}
		}
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
		// Check for escalated question.
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
		// Check for interrupted session.
		for _, s := range activeSessions {
			if s.Status == domain.AgentSessionInterrupted {
				a.content.SetMode(ContentModeInterrupted)
				a.content.interrupted.SetSession(s.ID, s.SubPlanID, s.RepositoryName, s.WorktreePath, a.canActOnSession(s))
				return nil
			}
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
		// Tail the review agent log for each implementation session if available.
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

	// Reset tailing state when mode changes away from live-tailing modes.
	if prevMode != a.content.mode {
		if prevMode == ContentModePlanning || prevMode == ContentModeImplementing {
			a.tailingSessionIDs = make(map[string]bool)
		}
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
	// Owner is a different instance — take over only if it's dead (stale heartbeat >15s).
	// If we have no live instance data yet, be conservative and disallow.
	if len(a.liveInstanceIDs) == 0 {
		return false
	}
	return !a.liveInstanceIDs[*s.OwnerInstanceID]
}

func (a *App) showConfirm(title, message string, onYes tea.Cmd) {
	a.confirm = components.NewConfirmDialog(title, message, onYes)
	a.confirmActive = true
}

func (a App) sidebarEntryFromWorkItem(wi domain.WorkItem) SidebarEntry {
	entry := SidebarEntry{
		Kind:       SidebarEntryWorkItem,
		WorkItemID: wi.ID,
		ExternalID: wi.ExternalID,
		Title:      wi.Title,
		State:      wi.State,
	}
	if plan := a.plans[wi.ID]; plan != nil {
		sps := a.subPlans[plan.ID]
		entry.TotalSubPlans = len(sps)
		for _, sp := range sps {
			if sp.Status == domain.SubPlanCompleted {
				entry.DoneSubPlans++
			}
			for _, s := range a.sessions {
				if s.SubPlanID == sp.ID {
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

func (a *App) rebuildSidebar() {
	if a.historySearchActive() {
		entries := make([]SidebarEntry, 0, len(a.historyResults))
		for _, result := range a.historyResults {
			entries = append(entries, a.sidebarEntryFromHistory(result))
		}
		a.sidebar.SetEntries(entries)
		a.syncSidebarSearchPresentation()
		return
	}
	entries := make([]SidebarEntry, 0, len(a.workItems))
	for _, wi := range a.workItems {
		entries = append(entries, a.sidebarEntryFromWorkItem(wi))
	}
	a.sidebar.SetEntries(entries)
	a.syncSidebarSearchPresentation()
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

	sidebarPaneWidth, contentPaneWidth, _, paneInnerHeight := mainPageLayoutMetrics(a.windowWidth, a.windowHeight)
	borderColor := lipgloss.Color("#334155")

	sidebarContent := lipgloss.NewStyle().
		Width(max(0, sidebarPaneWidth-2)).
		Height(paneInnerHeight).
		Render(a.sidebar.View())
	sidebarPane := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Render(sidebarContent)

	contentContent := lipgloss.NewStyle().
		Width(max(0, contentPaneWidth-2)).
		Height(paneInnerHeight).
		Render(a.content.View())
	contentPane := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Render(contentContent)

	bodyParts := make([]string, 0, 2)
	if sidebarPaneWidth > 0 {
		bodyParts = append(bodyParts, sidebarPane)
	}
	if contentPaneWidth > 0 {
		bodyParts = append(bodyParts, contentPane)
	}
	body := lipgloss.JoinHorizontal(lipgloss.Top, bodyParts...)

	hints := a.currentHints()
	statusBar := a.statusBar.View(hints, a.statusBarText(), a.windowWidth)

	base := lipgloss.JoinVertical(lipgloss.Left, body, statusBar)

	if a.toasts.HasToasts() {
		toastView := a.toasts.View("", "")
		base = renderTopRightOverlay(base, toastView, a.windowWidth, 1, lipgloss.Height(statusBar))
	}

	if a.activeOverlay == overlayNewSession {
		return renderOverlay(a.newSession.View(), a.windowWidth, a.windowHeight)
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
	bodyHeight = max(0, totalHeight-statusBarHeight)
	paneInnerHeight = max(0, bodyHeight-2)

	sidebarPaneWidth = min(SidebarWidth+2, max(0, totalWidth))
	contentPaneWidth = max(0, totalWidth-sidebarPaneWidth)
	if contentPaneWidth > 0 && contentPaneWidth < 2 {
		shift := 2 - contentPaneWidth
		sidebarPaneWidth = max(0, sidebarPaneWidth-shift)
		contentPaneWidth = totalWidth - sidebarPaneWidth
	}

	return sidebarPaneWidth, contentPaneWidth, bodyHeight, paneInnerHeight
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

	for i, overlayLine := range overlayLines {
		target := start + i
		if target < 0 || target >= maxOverlayBottom {
			break
		}
		overlayWidth := ansi.StringWidth(overlayLine)
		if overlayWidth <= 0 {
			continue
		}
		if overlayWidth >= width {
			baseLines[target] = ansi.Truncate(overlayLine, width, "")
			continue
		}

		prefixWidth := width - overlayWidth
		prefix := ansi.Cut(baseLines[target], 0, prefixWidth)
		if got := ansi.StringWidth(prefix); got < prefixWidth {
			prefix += strings.Repeat(" ", prefixWidth-got)
		}
		baseLines[target] = prefix + overlayLine
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
		if err := svcs.WorkItem.Create(context.Background(), wi); err != nil {
			return ErrMsg{Err: err}
		}
		return ActionDoneMsg{Message: "Session created: " + wi.ExternalID}
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
		if err := svcs.WorkItem.Create(context.Background(), wi); err != nil {
			return ErrMsg{Err: err}
		}
		return ActionDoneMsg{Message: "Session created: " + wi.ExternalID}
	}
}

package views

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

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
	overlayConfig
	overlayWorkspaceInit
	overlayHelp
)

// App is the top-level bubbletea model.
type App struct {
	svcs Services

	// Layout sub-models
	sidebar   SidebarModel
	content   ContentModel
	header    HeaderModel
	statusBar StatusBarModel

	// Overlays
	activeOverlay  overlayKind
	newSession     NewSessionOverlay
	configOverlay  ConfigOverlay
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
	currentWorkItemID string

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
		svcs:              svcs,
		sidebar:           NewSidebarModel(st),
		content:           NewContentModel(st),
		header:            NewHeaderModel(svcs.WorkspaceName, st),
		statusBar:         NewStatusBarModel(st),
		newSession:        NewNewSessionOverlay(svcs.Adapters, svcs.WorkspaceID, st),
		configOverlay:     NewConfigOverlay(svcs.Cfg, st),
		helpOverlay:       NewHelpOverlay(st),
		subPlans:          make(map[string][]domain.SubPlan),
		plans:             make(map[string]*domain.Plan),
		questions:         make(map[string][]domain.Question),
		reviews:           make(map[string]ReviewsLoadedMsg),
		tailingSessionIDs: make(map[string]bool),
		liveInstanceIDs:   make(map[string]bool),
		reviewSessionLogs: make(map[string]string),
		sessionsDir:       sessionsDir,
		hasWorkspace:      svcs.WorkspaceID != "",
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

// Update is the bubbletea message handler.
func (a App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.windowWidth = msg.Width
		a.windowHeight = msg.Height
		bodyHeight := msg.Height - 2
		contentWidth := msg.Width - SidebarWidth - 1
		a.sidebar.SetHeight(bodyHeight)
		a.content.SetSize(contentWidth, bodyHeight)
		a.workspaceModal.SetSize(msg.Width, msg.Height)
		a.newSession.SetSize(msg.Width, msg.Height)
		a.configOverlay.SetSize(msg.Width, msg.Height)
		return a, nil

	case WorkspaceHealthCheckMsg:
		if a.activeOverlay == overlayWorkspaceInit {
			a.workspaceModal, cmd = a.workspaceModal.Update(msg)
			cmds = append(cmds, cmd)
		}
		return a, tea.Batch(cmds...)

	case WorkspaceInitDoneMsg:
		a.svcs.WorkspaceID = msg.WorkspaceID
		a.svcs.WorkspaceName = msg.WorkspaceName
		a.svcs.WorkspaceDir = msg.WorkspaceDir
		a.header.WorkspaceName = msg.WorkspaceName
		a.hasWorkspace = true
		a.activeOverlay = overlayNone
		a.toasts.AddToast("Workspace initialized", components.ToastSuccess)
		cmds = append(cmds,
			LoadWorkItemsCmd(a.svcs.WorkItem, a.svcs.WorkspaceID),
			LoadSessionsCmd(a.svcs.Session, a.svcs.WorkspaceID),
		)
		return a, tea.Batch(cmds...)

	case WorkspaceCancelMsg:
		return a, tea.Quit

	case CloseOverlayMsg:
		a.activeOverlay = overlayNone
		a.newSession.Close()
		a.configOverlay.Close()
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
		cmds = append(cmds, a.updateContentFromState())
		return a, tea.Batch(cmds...)

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

	case ConfirmOverrideAcceptMsg:
		a.showConfirm("Override Accept",
			"Accept this work item despite outstanding critiques? This cannot be undone.",
			func() tea.Msg { return OverrideAcceptMsg{WorkItemID: msg.WorkItemID} },
		)
		return a, nil

	case ConfirmCloseConfigMsg:
		a.showConfirm("Discard Changes",
			"You have unsaved configuration changes. Discard them?",
			func() tea.Msg { return CloseOverlayMsg{} },
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

	case NewSessionLinearMsg:
		a.activeOverlay = overlayNone
		a.newSession.Close()
		cmds = append(cmds, createLinearSessionCmd(a.svcs, msg))
		return a, tea.Batch(cmds...)

	case ConfigSaveMsg:
		if a.activeOverlay == overlayConfig {
			a.configOverlay, cmd = a.configOverlay.Update(msg)
			cmds = append(cmds, cmd)
		}
		a.toasts.AddToast("Configuration saved", components.ToastSuccess)
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
	} else if a.activeOverlay == overlayConfig {
		a.configOverlay, cmd = a.configOverlay.Update(msg)
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
		default: // n, esc, any other key cancels
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

	// Workspace init modal captures all input.
	if a.activeOverlay == overlayWorkspaceInit {
		a.workspaceModal, cmd = a.workspaceModal.Update(msg)
		return a, cmd
	}

	if a.activeOverlay == overlayNewSession {
		a.newSession, cmd = a.newSession.Update(msg)
		return a, cmd
	}
	if a.activeOverlay == overlayConfig {
		a.configOverlay, cmd = a.configOverlay.Update(msg)
		return a, cmd
	}
	// Help overlay: any key press dismisses it.
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
		a.activeOverlay = overlayNewSession
		a.newSession.Open()
		return a, nil
	case "c":
		a.activeOverlay = overlayConfig
		a.configOverlay.Open()
		return a, nil
	case "esc":
		if a.activeOverlay != overlayNone {
			a.activeOverlay = overlayNone
			a.newSession.Close()
			a.configOverlay.Close()
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
		return nil
	}
	if sel.WorkItemID == a.currentWorkItemID {
		return nil
	}
	a.tailingSessionIDs = make(map[string]bool)
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
		for _, s := range a.sessions {
			if s.Status == domain.AgentSessionRunning {
				logPath := filepath.Join(a.sessionsDir, s.ID+".log")
				a.content.planOutput.SetLogPath(s.ID, logPath)
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

func (a *App) rebuildSidebar() {
	var summaries []SessionSummary
	for _, wi := range a.workItems {
		ss := SessionSummary{
			WorkItemID: wi.ID,
			ExternalID: wi.ExternalID,
			Title:      wi.Title,
			State:      wi.State,
		}
		if plan := a.plans[wi.ID]; plan != nil {
			sps := a.subPlans[plan.ID]
			ss.TotalSubPlans = len(sps)
			for _, sp := range sps {
				if sp.Status == domain.SubPlanCompleted {
					ss.DoneSubPlans++
				}
				for _, s := range a.sessions {
					if s.SubPlanID == sp.ID {
						if s.Status == domain.AgentSessionWaitingForAnswer {
							for _, q := range a.questions[s.ID] {
								if q.Status == domain.QuestionEscalated {
									ss.HasOpenQuestion = true
								}
							}
						}
						if s.Status == domain.AgentSessionInterrupted {
							ss.HasInterrupted = true
						}
					}
				}
			}
		}
		summaries = append(summaries, ss)
	}
	a.sidebar.SetSessions(summaries)
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

	header := a.header.View(a.windowWidth)

	bodyHeight := a.windowHeight - 2
	sidebarView := a.sidebar.View()
	divider := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#2d2d44")).
		Render("|")
	contentView := lipgloss.NewStyle().
		Width(a.windowWidth - SidebarWidth - 1).
		Height(bodyHeight).
		Render(a.content.View())

	body := lipgloss.JoinHorizontal(lipgloss.Top, sidebarView, divider, contentView)

	hints := a.content.KeybindHints()
	if len(hints) == 0 {
		hints = DefaultHints()
	}
	statusBar := a.statusBar.View(hints, fmt.Sprintf("%d sessions", len(a.workItems)), a.windowWidth)

	base := lipgloss.JoinVertical(lipgloss.Left, header, body, statusBar)

	if a.toasts.HasToasts() {
		toastView := a.toasts.View("", "")
		base = renderBottomRight(base, toastView, a.windowWidth)
	}

	if a.activeOverlay == overlayNewSession {
		return renderOverlay(a.newSession.View(), a.windowWidth, a.windowHeight)
	}
	if a.activeOverlay == overlayConfig {
		return renderOverlay(a.configOverlay.View(), a.windowWidth, a.windowHeight)
	}
	if a.activeOverlay == overlayHelp {
		return renderOverlay(a.helpOverlay.View(), a.windowWidth, a.windowHeight)
	}

	return base
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

// renderBottomRight appends a toast widget right-aligned at the bottom.
func renderBottomRight(base, toast string, w int) string {
	return base + "\n" + lipgloss.NewStyle().Width(w).AlignHorizontal(lipgloss.Right).Render(toast)
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

func createLinearSessionCmd(svcs Services, msg NewSessionLinearMsg) tea.Cmd {
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

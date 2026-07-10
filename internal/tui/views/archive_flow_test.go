package views

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/repository"
	"github.com/beeemT/substrate/internal/service"
	"github.com/beeemT/substrate/internal/tui/styles"
)

// archFlowRepo is a minimal in-memory SessionRepository.
type archFlowRepo struct {
	items        map[string]domain.Session
	updateCalled int
}

func (r *archFlowRepo) Get(_ context.Context, id string) (domain.Session, error) {
	if item, ok := r.items[id]; ok {
		return item, nil
	}
	return domain.Session{}, repository.ErrNotFound
}

func (r *archFlowRepo) List(_ context.Context, _ repository.SessionFilter) ([]domain.Session, error) {
	result := make([]domain.Session, 0, len(r.items))
	for _, item := range r.items {
		result = append(result, item)
	}
	return result, nil
}
func (r *archFlowRepo) Create(_ context.Context, _ domain.Session) error { return nil }
func (r *archFlowRepo) Update(_ context.Context, item domain.Session) error {
	r.updateCalled++
	r.items[item.ID] = item
	return nil
}
func (r *archFlowRepo) Delete(_ context.Context, _ string) error { return nil }

// TestArchAppFlow verifies the archive and unarchive user flows:
//  1. Press 'a' on a completed session → confirm dialog opens
//  2. Confirm with Enter → ArchiveSessionMsg fires via onYes
//  3. Feed ArchiveSessionMsg back → handler calls archiveSessionCmd → service archives
//  4. State → archived, PreviousState → previous state
//
// Unarchive reverses: archived → completed (or merged/failed).

func TestArchAppFlow(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name      string
		workItem  domain.Session
		wantHint  string
		wantState domain.SessionState
		wantPrev  domain.SessionState // what PreviousState should hold after action
	}{
		{
			name:     "archive_completed",
			workItem: domain.Session{ID: "wi-1", WorkspaceID: "ws-local", State: domain.SessionCompleted, CreatedAt: now, UpdatedAt: now},
			wantHint: "Archive session",
			// After archiving: state=archived, previousState=completed (the state we transitioned from)
			wantState: domain.SessionArchived,
			wantPrev:  domain.SessionCompleted,
		},
		{
			name:     "unarchive_archived",
			workItem: domain.Session{ID: "wi-2", WorkspaceID: "ws-local", State: domain.SessionArchived, PreviousState: domain.SessionCompleted, CreatedAt: now, UpdatedAt: now},
			wantHint: "Unarchive session",
			// After unarchiving: state=completed (restored), previousState=archived (the state we transitioned from)
			wantState: domain.SessionCompleted,
			wantPrev:  domain.SessionArchived,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			repo := &archFlowRepo{
				items: map[string]domain.Session{tc.workItem.ID: tc.workItem},
			}
			svc := service.NewSessionService(repository.NoopTransacter{Res: repository.Resources{
				Sessions:      repo,
				AgentSessions: &mockTaskRepoForSession{tasks: map[string]domain.AgentSession{}},
			}}, NewNoopPublisher())

			app := newTestApp(Services{
				WorkspaceID:   "ws-local",
				WorkspaceName: "local",
				Session:       svc,
				Settings:      newTestSettingsService(),
			})
			app.workItems = []domain.Session{tc.workItem}
			app.content.SetSize(80, 20)
			app.currentWorkItemID = tc.workItem.ID

			// 1. Verify archive/unarchive hint appears with correct key binding.
			hints := app.currentHints()
			var hintKey string
			for _, h := range hints {
				if h.Label == tc.wantHint {
					hintKey = h.Key
					break
				}
			}
			if hintKey != "a" {
				t.Fatalf("hint key = %q, want \"a\"", hintKey)
			}

			// 2. Press 'a' to open confirm dialog (no cmd returned — dialog is inline).
			model, _ := app.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
			app = model.(*App)
			model, cmd := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
			app = model.(*App)
			if cmd != nil {
				t.Fatalf("'a' key should not return cmd, got %v", cmd)
			}
			if !app.confirmActive {
				t.Fatal("confirm dialog should be active after pressing 'a'")
			}

			// 3. Press Enter: onYes lambda in showArchiveConfirm/showUnarchiveConfirm fires
			//    ArchiveSessionMsg/UnarchiveSessionMsg directly (not a cmd).
			model, _ = app.Update(tea.KeyMsg{Type: tea.KeyEnter})
			app = model.(*App)

			// 4. Feed the message back to the app to trigger the handler.
			//    The handler appends archiveSessionCmd/unarchiveSessionCmd to tea.Batch.
			var completionMsg tea.Msg
			switch tc.wantHint {
			case "Archive session":
				model, cmd := app.Update(ArchiveSessionMsg{WorkItemID: tc.workItem.ID})
				if cmd != nil {
					completionMsg = cmd()
				}
				_ = model.(*App)
			case "Unarchive session":
				model, cmd := app.Update(UnarchiveSessionMsg{WorkItemID: tc.workItem.ID})
				if cmd != nil {
					completionMsg = cmd()
				}
				_ = model.(*App)
			}

			// 5. Verify service call succeeded and state is correct.
			item, _ := repo.Get(context.Background(), tc.workItem.ID)
			if repo.updateCalled == 0 {
				t.Fatal("repo.Update was never called — service call failed")
			}
			if item.State != tc.wantState {
				t.Errorf("state = %q, want %q", item.State, tc.wantState)
			}
			if item.PreviousState != tc.wantPrev {
				t.Errorf("previousState = %q, want %q", item.PreviousState, tc.wantPrev)
			}

			// 6. Verify the correct completion message was returned.
			switch tc.wantHint {
			case "Archive session":
				if _, ok := completionMsg.(SessionArchivedMsg); !ok {
					t.Errorf("completion msg = %T, want SessionArchivedMsg", completionMsg)
				}
			case "Unarchive session":
				if _, ok := completionMsg.(SessionUnarchivedMsg); !ok {
					t.Errorf("completion msg = %T, want SessionUnarchivedMsg", completionMsg)
				}
			}
		})
	}
}

func TestArchiveConfirmationTerminatesLegacyChildrenBeforeArchive(t *testing.T) {
	t.Parallel()

	const workItemID = "wi-confirm-legacy-archive"
	taskRepo := &mockTaskRepoForSession{tasks: map[string]domain.AgentSession{
		"pending": {
			ID:         "pending",
			WorkItemID: workItemID,
			Status:     domain.AgentSessionPending,
		},
		"running": {
			ID:         "running",
			WorkItemID: workItemID,
			Status:     domain.AgentSessionRunning,
		},
	}}
	workRepo := &archFlowRepo{items: map[string]domain.Session{
		workItemID: {
			ID:    workItemID,
			State: domain.SessionImplementing,
		},
	}}
	resources := repository.Resources{Sessions: workRepo, AgentSessions: taskRepo}
	sessionSvc := service.NewSessionService(repository.NoopTransacter{Res: resources}, NewNoopPublisher())
	taskSvc := service.NewAgentSessionService(repository.NoopTransacter{Res: resources}, NewNoopPublisher())
	app := newTestApp(Services{
		WorkspaceID: "ws-local",
		Session:     sessionSvc,
		Task:        taskSvc,
		Settings:    newTestSettingsService(),
	})
	app.workItems = []domain.Session{workRepo.items[workItemID]}
	app.sessions = []domain.AgentSession{taskRepo.tasks["pending"], taskRepo.tasks["running"]}
	app.currentWorkItemID = workItemID
	app.content.SetSize(80, 20)
	pipelineCtx := app.registerPipelineCancel(workItemID)

	model, cmd := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if cmd != nil {
		t.Fatalf("archive shortcut returned command before confirmation: %v", cmd)
	}
	app = model.(*App)
	if !app.confirmActive {
		t.Fatal("archive confirmation did not open")
	}
	if !strings.Contains(app.confirm.Message, "1 live agent session") {
		t.Fatalf("confirm message = %q, want live session count", app.confirm.Message)
	}

	model, cmd = app.Update(tea.KeyMsg{Type: tea.KeyEnter})
	app = model.(*App)
	archiveMsg := tea.Msg(ArchiveSessionMsg{WorkItemID: workItemID})
	if cmd != nil {
		archiveMsg = cmd()
		if archiveMsg == nil {
			t.Fatal("confirmation command returned nil message")
		}
	}
	model, cmd = app.Update(archiveMsg)
	app = model.(*App)
	if cmd == nil {
		t.Fatal("ArchiveSessionMsg returned nil command")
	}
	select {
	case <-pipelineCtx.Done():
	default:
		t.Fatal("pipeline context was not canceled before archive command dispatch")
	}

	msg := cmd()
	if _, ok := msg.(SessionArchivedMsg); !ok {
		t.Fatalf("archive command message = %T %#v, want SessionArchivedMsg", msg, msg)
	}
	if got := workRepo.items[workItemID].State; got != domain.SessionArchived {
		t.Fatalf("work item state = %q, want %q", got, domain.SessionArchived)
	}
	if got := taskRepo.tasks["pending"].Status; got != domain.AgentSessionFailed {
		t.Fatalf("pending status = %q, want %q", got, domain.AgentSessionFailed)
	}
	if got := taskRepo.tasks["running"].Status; got != domain.AgentSessionInterrupted {
		t.Fatalf("running status = %q, want %q", got, domain.AgentSessionInterrupted)
	}
}

func TestShowArchiveConfirmDescribesLiveAgentCount(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		statuses    []domain.AgentSessionStatus
		wantMessage string
	}{
		{
			name:        "no live sessions keeps reversible explanation",
			wantMessage: "Archive this session? It will be hidden from the default views. You can unarchive it later.",
		},
		{
			name:        "one live session uses singular copy",
			statuses:    []domain.AgentSessionStatus{domain.AgentSessionRunning},
			wantMessage: "1 live agent session before archive",
		},
		{
			name: "multiple live sessions use plural copy",
			statuses: []domain.AgentSessionStatus{
				domain.AgentSessionRunning,
				domain.AgentSessionWaitingForAnswer,
			},
			wantMessage: "2 live agent sessions before archive",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			app := App{
				statusBar: NewStatusBarModel(styles.NewStyles(styles.DefaultTheme)),
			}
			for i, status := range tc.statuses {
				app.sessions = append(app.sessions, domain.AgentSession{
					ID:         fmt.Sprintf("agent-%d", i),
					WorkItemID: "wi-archive",
					Status:     status,
				})
			}

			app.showArchiveConfirm("wi-archive")
			if !app.confirmActive {
				t.Fatal("confirmActive = false, want true")
			}
			if tc.statuses == nil {
				if app.confirm.Message != tc.wantMessage {
					t.Fatalf("confirm message = %q, want %q", app.confirm.Message, tc.wantMessage)
				}
			} else if !strings.Contains(app.confirm.Message, tc.wantMessage) ||
				!strings.Contains(app.confirm.Message, "Accepting will terminate") {
				t.Fatalf("confirm message = %q, want live-count termination copy", app.confirm.Message)
			}
		})
	}
}

func TestArchiveSessionCmdTerminatesLegacyChildrenBeforeArchive(t *testing.T) {
	t.Parallel()

	const workItemID = "wi-legacy-archive"
	taskRepo := &mockTaskRepoForSession{tasks: map[string]domain.AgentSession{
		"pending": {
			ID:         "pending",
			WorkItemID: workItemID,
			Status:     domain.AgentSessionPending,
		},
		"running": {
			ID:         "running",
			WorkItemID: workItemID,
			Status:     domain.AgentSessionRunning,
		},
		"waiting": {
			ID:         "waiting",
			WorkItemID: workItemID,
			Status:     domain.AgentSessionWaitingForAnswer,
		},
		"completed": {
			ID:         "completed",
			WorkItemID: workItemID,
			Status:     domain.AgentSessionCompleted,
		},
	}}
	workRepo := &archFlowRepo{items: map[string]domain.Session{
		workItemID: {
			ID:    workItemID,
			State: domain.SessionImplementing,
		},
	}}
	resources := repository.Resources{Sessions: workRepo, AgentSessions: taskRepo}
	sessionSvc := service.NewSessionService(repository.NoopTransacter{Res: resources}, NewNoopPublisher())
	taskSvc := service.NewAgentSessionService(repository.NoopTransacter{Res: resources}, NewNoopPublisher())

	msg := archiveSessionCmd(sessionSvc, nil, taskSvc, nil, workItemID, false, "")()
	archived, ok := msg.(SessionArchivedMsg)
	if !ok {
		t.Fatalf("archive command message = %T %#v, want SessionArchivedMsg", msg, msg)
	}
	if archived.WorkItemID != workItemID {
		t.Fatalf("archived work item ID = %q, want %q", archived.WorkItemID, workItemID)
	}
	if got := workRepo.items[workItemID].State; got != domain.SessionArchived {
		t.Fatalf("work item state = %q, want %q", got, domain.SessionArchived)
	}
	if got := taskRepo.tasks["pending"].Status; got != domain.AgentSessionFailed {
		t.Fatalf("pending status = %q, want %q", got, domain.AgentSessionFailed)
	}
	for _, id := range []string{"running", "waiting"} {
		if got := taskRepo.tasks[id].Status; got != domain.AgentSessionInterrupted {
			t.Fatalf("%s status = %q, want %q", id, got, domain.AgentSessionInterrupted)
		}
	}
	if got := taskRepo.tasks["completed"].Status; got != domain.AgentSessionCompleted {
		t.Fatalf("completed status = %q, want %q", got, domain.AgentSessionCompleted)
	}
}

type archiveFailTaskRepo struct {
	*mockTaskRepoForSession
	failID  string
	failErr error
}

func (r *archiveFailTaskRepo) Update(ctx context.Context, session domain.AgentSession) error {
	if session.ID == r.failID {
		return r.failErr
	}
	return r.mockTaskRepoForSession.Update(ctx, session)
}

func TestArchiveSessionCmdTerminationFailurePreventsArchive(t *testing.T) {
	t.Parallel()

	const workItemID = "wi-legacy-archive-failure"
	terminationErr := errors.New("interrupt failed")
	taskRepo := &archiveFailTaskRepo{
		mockTaskRepoForSession: &mockTaskRepoForSession{tasks: map[string]domain.AgentSession{
			"running": {
				ID:         "running",
				WorkItemID: workItemID,
				Status:     domain.AgentSessionRunning,
			},
		}},
		failID:  "running",
		failErr: terminationErr,
	}
	workRepo := &archFlowRepo{items: map[string]domain.Session{
		workItemID: {
			ID:    workItemID,
			State: domain.SessionImplementing,
		},
	}}
	resources := repository.Resources{Sessions: workRepo, AgentSessions: taskRepo}
	sessionSvc := service.NewSessionService(repository.NoopTransacter{Res: resources}, NewNoopPublisher())
	taskSvc := service.NewAgentSessionService(repository.NoopTransacter{Res: resources}, NewNoopPublisher())

	msg := archiveSessionCmd(sessionSvc, nil, taskSvc, nil, workItemID, false, "")()
	errMsg, ok := msg.(ErrMsg)
	if !ok {
		t.Fatalf("archive command message = %T %#v, want ErrMsg", msg, msg)
	}
	if !errors.Is(errMsg.Err, terminationErr) {
		t.Fatalf("archive error = %v, want termination error chain", errMsg.Err)
	}
	if got := workRepo.items[workItemID].State; got == domain.SessionArchived {
		t.Fatal("work item was archived after child termination failed")
	}
}

func TestArchivablSessionIDFromWorkItemEligibility(t *testing.T) {
	t.Parallel()

	const workItemID = "wi-eligibility"
	tests := []struct {
		name        string
		workItem    domain.SessionState
		childStatus domain.AgentSessionStatus
		wantID      string
	}{
		{
			name:        "implementing with completed child",
			workItem:    domain.SessionImplementing,
			childStatus: domain.AgentSessionCompleted,
			wantID:      workItemID,
		},
		{
			name:        "implementing with failed child",
			workItem:    domain.SessionImplementing,
			childStatus: domain.AgentSessionFailed,
			wantID:      workItemID,
		},
		{
			name:        "implementing with interrupted child",
			workItem:    domain.SessionImplementing,
			childStatus: domain.AgentSessionInterrupted,
			wantID:      workItemID,
		},
		{
			name:        "implementing with no child sessions",
			workItem:    domain.SessionImplementing,
			wantID:      workItemID,
		},
		{
			name:        "pending child",
			workItem:    domain.SessionImplementing,
			childStatus: domain.AgentSessionPending,
			wantID:      workItemID,
		},
		{
			name:        "running child",
			workItem:    domain.SessionImplementing,
			childStatus: domain.AgentSessionRunning,
			wantID:      workItemID,
		},
		{
			name:        "waiting child",
			workItem:    domain.SessionImplementing,
			childStatus: domain.AgentSessionWaitingForAnswer,
			wantID:      workItemID,
		},
		{
			name:        "completed work item remains archiveable",
			workItem:    domain.SessionCompleted,
			wantID:      workItemID,
		},
		{
			name:        "merged work item remains archiveable",
			workItem:    domain.SessionMerged,
			wantID:      workItemID,
		},
		{
			name:        "failed work item remains archiveable",
			workItem:    domain.SessionFailed,
			wantID:      workItemID,
		},
		{
			name:     "archived work item is not archiveable",
			workItem: domain.SessionArchived,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			app := App{
				currentWorkItemID: workItemID,
				workItems: []domain.Session{{
					ID:    workItemID,
					State: tc.workItem,
				}},
			}
			if tc.childStatus != "" {
				app.sessions = []domain.AgentSession{{
					ID:         "child-1",
					WorkItemID: workItemID,
					Status:     tc.childStatus,
				}}
			}

			if got := app.archivablSessionIDFromWorkItem(); got != tc.wantID {
				t.Fatalf("archivable work item ID = %q, want %q", got, tc.wantID)
			}
		})
	}
}

func TestArchiveActionRegistryIncludesActiveImplementingWorkItem(t *testing.T) {
	t.Parallel()

	const workItemID = "wi-active"
	app := &App{
		currentWorkItemID: workItemID,
		workItems: []domain.Session{{
			ID:    workItemID,
			State: domain.SessionImplementing,
		}},
		sessions: []domain.AgentSession{{
			ID:         "child-running",
			WorkItemID: workItemID,
			Status:     domain.AgentSessionRunning,
		}},
		content: NewContentModel(styles.NewStyles(styles.DefaultTheme)),
	}

	action := findAction(app.BuildActionRegistry(ContextOverview), "archive_session")
	if action == nil {
		t.Fatal("action registry missing archive_session for active implementing work item")
	}
	if action.Shortcut != "a" {
		t.Fatalf("archive shortcut = %q, want %q", action.Shortcut, "a")
	}
}

func TestArchivablSessionIDFromHistoryEntryRemainsTerminalOnly(t *testing.T) {
	t.Parallel()

	const workItemID = "wi-history"
	tests := []struct {
		name  string
		state domain.SessionState
		want  string
	}{
		{name: "completed", state: domain.SessionCompleted, want: workItemID},
		{name: "merged", state: domain.SessionMerged, want: workItemID},
		{name: "failed", state: domain.SessionFailed, want: workItemID},
		{name: "implementing", state: domain.SessionImplementing},
		{name: "archived", state: domain.SessionArchived},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			app := App{currentHistoryEntry: SidebarEntry{
				WorkItemID: workItemID,
				State:      tc.state,
			}}
			if got := app.archivablSessionIDFromHistoryEntry(); got != tc.want {
				t.Fatalf("archivable history work item ID = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestArchiveSelectedSessionFocusesVisibleNeighbor(t *testing.T) {
	now := time.Now()
	items := []domain.Session{
		{ID: "newer", WorkspaceID: "ws-local", State: domain.SessionCompleted, CreatedAt: now, UpdatedAt: now.Add(3 * time.Minute)},
		{ID: "middle", WorkspaceID: "ws-local", State: domain.SessionCompleted, CreatedAt: now, UpdatedAt: now.Add(2 * time.Minute)},
		{ID: "older", WorkspaceID: "ws-local", State: domain.SessionCompleted, CreatedAt: now, UpdatedAt: now.Add(time.Minute)},
	}

	cases := []struct {
		name      string
		currentID string
		wantID    string
	}{
		{
			name:      "previous visible session exists",
			currentID: "middle",
			wantID:    "newer",
		},
		{
			name:      "first visible session falls back to next",
			currentID: "newer",
			wantID:    "middle",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			repoItems := make(map[string]domain.Session, len(items))
			for _, item := range items {
				repoItems[item.ID] = item
			}
			repo := &archFlowRepo{items: repoItems}
			svc := service.NewSessionService(repository.NoopTransacter{Res: repository.Resources{
				Sessions:      repo,
				AgentSessions: &mockTaskRepoForSession{tasks: map[string]domain.AgentSession{}},
			}}, NewNoopPublisher())

			app := newTestApp(Services{
				WorkspaceID:   "ws-local",
				WorkspaceName: "local",
				Session:       svc,
				Settings:      newTestSettingsService(),
				SessionArtifacts: service.NewSessionReviewArtifactService(repository.NoopTransacter{Res: repository.Resources{
					SessionReviewArtifacts: emptySessionArtifactRepo{},
				}}),
				Events: service.NewEventService(repository.NoopTransacter{Res: repository.Resources{
					Events: emptyEventRepo{},
				}}),
			})
			app.workItems = append([]domain.Session(nil), items...)
			app.content.SetSize(80, 20)
			app.currentWorkItemID = tc.currentID
			app.rebuildSidebar()
			if !app.sidebar.SelectWorkItem(tc.currentID) {
				t.Fatalf("test setup: selected work item %q not visible", tc.currentID)
			}

			model, cmd := app.Update(ArchiveSessionMsg{WorkItemID: tc.currentID})
			app = model.(*App)
			if cmd == nil {
				t.Fatal("ArchiveSessionMsg returned nil command")
			}
			completionMsg := cmd()
			archivedMsg, ok := completionMsg.(SessionArchivedMsg)
			if !ok {
				t.Fatalf("completion msg = %T, want SessionArchivedMsg", completionMsg)
			}
			if !archivedMsg.FocusAfterArchive {
				t.Fatal("FocusAfterArchive = false, want true")
			}
			if archivedMsg.FocusWorkItemID != tc.wantID {
				t.Fatalf("FocusWorkItemID = %q, want %q", archivedMsg.FocusWorkItemID, tc.wantID)
			}

			model, _ = app.Update(archivedMsg)
			app = model.(*App)

			if app.currentWorkItemID != tc.wantID {
				t.Fatalf("currentWorkItemID = %q, want %q", app.currentWorkItemID, tc.wantID)
			}
			selected := app.sidebar.Selected()
			if selected == nil {
				t.Fatal("sidebar selection is nil")
			}
			if selected.WorkItemID != tc.wantID {
				t.Fatalf("selected WorkItemID = %q, want %q", selected.WorkItemID, tc.wantID)
			}
			if app.sidebarMode != sidebarPaneSessions {
				t.Fatalf("sidebarMode = %v, want %v", app.sidebarMode, sidebarPaneSessions)
			}
			if app.mainFocus != mainFocusSidebar {
				t.Fatalf("mainFocus = %v, want %v", app.mainFocus, mainFocusSidebar)
			}
		})
	}
}

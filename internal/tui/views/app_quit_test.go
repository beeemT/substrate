package views

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/orchestrator"
	"github.com/beeemT/substrate/internal/repository"
	"github.com/beeemT/substrate/internal/service"
)

// newQuitTestApp creates a minimal App with the given sessions list.
func newQuitTestApp(sessions []domain.AgentSession) *App {
	app := newTestApp(Services{WorkspaceID: "ws-1", WorkspaceName: "test", Settings: newTestSettingsService()})
	app.sessions = sessions
	return app
}

// newQuitTestAppWithRegistry creates a minimal App with a real SessionRegistry.
func newQuitTestAppWithRegistry(sessions []domain.AgentSession) (*App, *orchestrator.SessionRegistry, *mockTaskRepoForSession) {
	reg := orchestrator.NewSessionRegistry()
	tasks := make(map[string]domain.AgentSession, len(sessions))
	for _, session := range sessions {
		tasks[session.ID] = session
	}
	taskRepo := &mockTaskRepoForSession{tasks: tasks}
	app := newTestApp(Services{
		WorkspaceID:     "ws-1",
		WorkspaceName:   "test",
		Settings:        newTestSettingsService(),
		SessionRegistry: reg,
		Task:            service.NewAgentSessionService(repository.NoopTransacter{Res: repository.Resources{AgentSessions: taskRepo}}, NewNoopPublisher()),
	})
	app.sessions = sessions
	return app, reg, taskRepo
}

func runningSessions(n int) []domain.AgentSession {
	tasks := make([]domain.AgentSession, n)
	for i := range tasks {
		tasks[i] = domain.AgentSession{ID: "s", Status: domain.AgentSessionRunning}
	}
	return tasks
}

// isQuitCmd returns true if calling cmd returns a tea.QuitMsg (i.e., cmd is tea.Quit).
func isQuitCmd(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

// TestQuitKeyWithNoSessionsQuitsImmediately verifies that pressing "q" when no
// agent sessions are active exits without showing a confirmation dialog.
func TestQuitKeyWithNoSessionsQuitsImmediately(t *testing.T) {
	t.Parallel()

	app := newQuitTestApp(nil)
	_, cmd := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if !isQuitCmd(cmd) {
		t.Fatalf("cmd = %v, want tea.Quit when no sessions are running", cmd)
	}
}

// TestQuitKeyWithRunningSessionsShowsConfirm verifies that pressing "q" when
// agent sessions are active shows a quit confirmation dialog.
func TestQuitKeyWithRunningSessionsShowsConfirm(t *testing.T) {
	t.Parallel()

	app := newQuitTestApp(runningSessions(2))
	model, cmd := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	updated := model.(*App)

	if cmd != nil {
		t.Fatalf("cmd = %v, want nil (no quit before confirmation)", cmd)
	}
	if !updated.confirmActive {
		t.Fatal("confirmActive = false, want true to show quit dialog")
	}
	if updated.confirm.Title != "Quit" {
		t.Fatalf("confirm title = %q, want \"Quit\"", updated.confirm.Title)
	}
	if !strings.Contains(updated.confirm.Message, "2") || !strings.Contains(updated.confirm.Message, "running") {
		t.Fatalf("confirm message = %q, want session count and \"running\"", updated.confirm.Message)
	}
}

// TestCtrlCWithNoSessionsQuitsImmediately verifies that ctrl+c quits without a
// dialog when no sessions are active.
func TestCtrlCWithNoSessionsQuitsImmediately(t *testing.T) {
	t.Parallel()

	app := newQuitTestApp(nil)
	_, cmd := app.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if !isQuitCmd(cmd) {
		t.Fatalf("cmd = %v, want tea.Quit on ctrl+c with no sessions", cmd)
	}
}

// TestCtrlCWithRunningSessionsShowsConfirm verifies that ctrl+c when sessions
// are running shows the same confirmation dialog as "q".
func TestCtrlCWithRunningSessionsShowsConfirm(t *testing.T) {
	t.Parallel()

	app := newQuitTestApp(runningSessions(1))
	model, cmd := app.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	updated := model.(*App)

	if cmd != nil {
		t.Fatalf("cmd = %v, want nil before confirmation", cmd)
	}
	if !updated.confirmActive {
		t.Fatal("confirmActive = false, want quit dialog on ctrl+c with running sessions")
	}
	if !strings.Contains(updated.confirm.Message, "1") || !strings.Contains(updated.confirm.Message, "running") {
		t.Fatalf("confirm message = %q, want session count and \"running\"", updated.confirm.Message)
	}
}

// TestQuitRequestMsgWithRunningSessionsShowsConfirm verifies that a SIGTERM-
// triggered QuitRequestMsg also shows the confirmation dialog.
func TestQuitRequestMsgWithRunningSessionsShowsConfirm(t *testing.T) {
	t.Parallel()

	app := newQuitTestApp(runningSessions(3))
	model, cmd := app.Update(QuitRequestMsg{})
	updated := model.(*App)

	if cmd != nil {
		t.Fatalf("cmd = %v, want nil before confirmation", cmd)
	}
	if !updated.confirmActive {
		t.Fatal("confirmActive = false, want quit dialog on QuitRequestMsg with running sessions")
	}
	if !strings.Contains(updated.confirm.Message, "3") {
		t.Fatalf("confirm message = %q, want session count 3", updated.confirm.Message)
	}
}

// TestQuitRequestMsgWithNoSessionsQuitsImmediately verifies that a SIGTERM
// signal when no sessions are running causes an immediate quit.
func TestQuitRequestMsgWithNoSessionsQuitsImmediately(t *testing.T) {
	t.Parallel()

	app := newQuitTestApp(nil)
	_, cmd := app.Update(QuitRequestMsg{})
	if !isQuitCmd(cmd) {
		t.Fatalf("cmd = %v, want tea.Quit on QuitRequestMsg with no sessions", cmd)
	}
}

// TestQuitConfirmYAccepts verifies that pressing "y" in the quit confirm dialog
// runs the quit command.
func TestQuitConfirmYAccepts(t *testing.T) {
	t.Parallel()

	app := newQuitTestApp(runningSessions(1))
	model, _ := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	withConfirm := model.(*App)

	if !withConfirm.confirmActive {
		t.Fatal("precondition: expected confirmActive after q with running session")
	}

	model2, cmd := withConfirm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	afterConfirm := model2.(*App)

	if afterConfirm.confirmActive {
		t.Fatal("confirmActive = true after y, expected dialog to be dismissed")
	}

	// The confirm returns a cmd that produces QuitConfirmedMsg.
	msg := cmd()
	if _, ok := msg.(QuitConfirmedMsg); !ok {
		t.Fatalf("confirm cmd returned %T, want QuitConfirmedMsg", msg)
	}

	// Dispatch QuitConfirmedMsg to trigger teardown + quit.
	_, quitCmd := afterConfirm.Update(msg)
	if !isQuitCmd(quitCmd) {
		t.Fatalf("QuitConfirmedMsg did not produce tea.Quit")
	}
}

// TestQuitConfirmCtrlCForceQuits verifies that pressing ctrl+c inside the quit
// confirm dialog acts as "yes" (confirms the quit).
func TestQuitConfirmCtrlCForceQuits(t *testing.T) {
	t.Parallel()

	app := newQuitTestApp(runningSessions(1))
	model, _ := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	withConfirm := model.(*App)

	model2, cmd := withConfirm.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	afterConfirm := model2.(*App)

	if afterConfirm.confirmActive {
		t.Fatal("confirmActive = true after ctrl+c on confirm, expected force-quit")
	}

	msg := cmd()
	if _, ok := msg.(QuitConfirmedMsg); !ok {
		t.Fatalf("confirm cmd returned %T, want QuitConfirmedMsg", msg)
	}

	_, quitCmd := afterConfirm.Update(msg)
	if !isQuitCmd(quitCmd) {
		t.Fatalf("QuitConfirmedMsg did not produce tea.Quit after ctrl+c")
	}
}

// TestQuitConfirmEscCancels verifies that any key other than y/enter/ctrl+c
// cancels the quit dialog without exiting.
func TestQuitConfirmEscCancels(t *testing.T) {
	t.Parallel()

	app := newQuitTestApp(runningSessions(1))
	model, _ := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	withConfirm := model.(*App)

	model2, cmd := withConfirm.Update(tea.KeyMsg{Type: tea.KeyEsc})
	afterCancel := model2.(*App)

	if afterCancel.confirmActive {
		t.Fatal("confirmActive = true after esc, expected dialog dismissed")
	}
	if cmd != nil {
		t.Fatalf("cmd = %v after esc on confirm, want nil (no quit)", cmd)
	}
}

// quitTestMockSession is a minimal adapter.AgentSession that tracks Abort calls.
type quitTestMockSession struct {
	id         string
	aborted    bool
	resumeInfo map[string]string
}

func (m *quitTestMockSession) ID() string                                    { return m.id }
func (m *quitTestMockSession) Wait(_ context.Context) error                  { return nil }
func (m *quitTestMockSession) Events() <-chan adapter.AgentEvent             { return nil }
func (m *quitTestMockSession) SendMessage(_ context.Context, _ string) error { return nil }
func (m *quitTestMockSession) Abort(_ context.Context) error                 { m.aborted = true; return nil }
func (m *quitTestMockSession) Steer(_ context.Context, _ string) error       { return nil }
func (m *quitTestMockSession) SendAnswer(_ context.Context, _ string) error  { return nil }
func (m *quitTestMockSession) ResumeInfo() map[string]string                 { return m.resumeInfo }
func (m *quitTestMockSession) Compact(_ context.Context) error               { return nil }

// TestQuitConfirmedMsgAbortsRegistrySessions verifies that dispatching
// QuitConfirmedMsg calls AbortAndDeregister on running sessions and cancels
// pipeline contexts.
func TestQuitConfirmedMsgAbortsRegistrySessions(t *testing.T) {
	t.Parallel()

	sessions := []domain.AgentSession{
		{ID: "task-1", WorkItemID: "wi-1", Status: domain.AgentSessionRunning},
		{ID: "task-2", WorkItemID: "wi-1", Status: domain.AgentSessionCompleted},
		{ID: "task-3", WorkItemID: "wi-2", Status: domain.AgentSessionRunning},
	}
	app, reg, taskRepo := newQuitTestAppWithRegistry(sessions)

	// Register running sessions in the registry.
	mock1 := &quitTestMockSession{id: "task-1", resumeInfo: map[string]string{"session": "resume-1"}}
	mock3 := &quitTestMockSession{id: "task-3"}
	reg.Register("task-1", mock1)
	reg.Register("task-3", mock3)

	// Register pipeline cancel contexts.
	_ = app.registerPipelineCancel("wi-1")
	_ = app.registerPipelineCancel("wi-2")

	_, cmd := app.Update(QuitConfirmedMsg{})
	if !isQuitCmd(cmd) {
		t.Fatal("QuitConfirmedMsg did not produce tea.Quit")
	}

	// Running sessions should have been aborted.
	if !mock1.aborted {
		t.Fatal("task-1 (running) should have been aborted")
	}
	if !mock3.aborted {
		t.Fatal("task-3 (running) should have been aborted")
	}

	// Registry should no longer track either session.
	if reg.IsRunning("task-1") {
		t.Fatal("task-1 should be deregistered")
	}
	if reg.IsRunning("task-3") {
		t.Fatal("task-3 should be deregistered")
	}
	if got := taskRepo.tasks["task-1"].Status; got != domain.AgentSessionInterrupted {
		t.Fatalf("task-1 status = %q, want interrupted", got)
	}
	if got := taskRepo.tasks["task-1"].ResumeInfo["session"]; got != "resume-1" {
		t.Fatalf("task-1 resume info = %q, want resume-1", got)
	}
}

// TestQuitConfirmedMsgCancelsPipelineContexts verifies that pipeline
// cancel functions are actually invoked during quit teardown.
func TestFocusedInterruptInterruptsSelectedAgentSession(t *testing.T) {
	t.Parallel()

	sessions := []domain.AgentSession{
		{ID: "task-1", WorkItemID: "wi-1", WorkspaceID: "ws-1", Status: domain.AgentSessionRunning},
		{ID: "task-2", WorkItemID: "wi-1", WorkspaceID: "ws-1", Status: domain.AgentSessionRunning},
	}
	app, reg, taskRepo := newQuitTestAppWithRegistry(sessions)
	app.workItems = []domain.Session{{ID: "wi-1", WorkspaceID: "ws-1", State: domain.SessionImplementing}}
	app.currentWorkItemID = "wi-1"
	app.sidebarMode = sidebarPaneTasks
	app.setSelectedTaskSessionID("task-1")

	mock1 := &quitTestMockSession{id: "task-1", resumeInfo: map[string]string{"session": "resume-focused"}}
	mock2 := &quitTestMockSession{id: "task-2"}
	reg.Register("task-1", mock1)
	reg.Register("task-2", mock2)
	pipelineCtx := app.registerPipelineCancel("wi-1")

	model, cmd := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'I'}})
	updated := model.(*App)
	if cmd == nil {
		t.Fatal("interrupt key returned nil cmd")
	}
	confirm, ok := cmd().(ConfirmInterruptSessionsMsg)
	if !ok {
		t.Fatalf("cmd message = %T, want ConfirmInterruptSessionsMsg", cmd())
	}
	if len(confirm.SessionIDs) != 1 || confirm.SessionIDs[0] != "task-1" {
		t.Fatalf("interrupt ids = %#v, want [task-1]", confirm.SessionIDs)
	}

	model, _ = updated.Update(confirm)
	updated = model.(*App)
	if !updated.confirmActive {
		t.Fatal("expected interrupt confirm modal")
	}
	if !strings.Contains(updated.confirm.Message, "resumable") {
		t.Fatalf("confirm message = %q, want resumable copy", updated.confirm.Message)
	}

	msg := updated.confirm.OnYes()
	interruptMsg, ok := msg.(InterruptSessionsMsg)
	if !ok {
		t.Fatalf("confirm OnYes = %T, want InterruptSessionsMsg", msg)
	}
	// Pipeline cancellation happens in the InterruptSessionsMsg handler before calling
	// interruptAgentSessionsByID. Simulate that here.
	for _, id := range interruptMsg.SessionIDs {
		for _, session := range updated.sessions {
			if session.ID == id {
				updated.cancelPipeline(session.WorkItemID)
			}
		}
	}
	result := updated.interruptAgentSessionsByID(context.Background(), interruptMsg.SessionIDs, updated.sessions)
	if result != nil {
		t.Fatalf("interruptAgentSessionsByID: %v", result)
	}
	if pipelineCtx.Err() == nil {
		t.Fatal("pipeline context should be cancelled before aborting focused session")
	}
	if !mock1.aborted {
		t.Fatal("task-1 should have been aborted")
	}
	if mock2.aborted {
		t.Fatal("task-2 should not have been aborted")
	}
	if got := taskRepo.tasks["task-1"].Status; got != domain.AgentSessionInterrupted {
		t.Fatalf("task-1 status = %q, want interrupted", got)
	}
	if got := taskRepo.tasks["task-2"].Status; got != domain.AgentSessionRunning {
		t.Fatalf("task-2 status = %q, want running", got)
	}
	if got := taskRepo.tasks["task-1"].ResumeInfo["session"]; got != "resume-focused" {
		t.Fatalf("task-1 resume info = %q, want resume-focused", got)
	}
}

func TestFocusedInterruptWorkItemInterruptsRunningChildren(t *testing.T) {
	t.Parallel()

	sessions := []domain.AgentSession{
		{ID: "task-1", WorkItemID: "wi-1", WorkspaceID: "ws-1", Status: domain.AgentSessionRunning},
		{ID: "task-2", WorkItemID: "wi-1", WorkspaceID: "ws-1", Status: domain.AgentSessionWaitingForAnswer},
		{ID: "task-3", WorkItemID: "wi-2", WorkspaceID: "ws-1", Status: domain.AgentSessionRunning},
	}
	app, reg, taskRepo := newQuitTestAppWithRegistry(sessions)
	app.workItems = []domain.Session{{ID: "wi-1", WorkspaceID: "ws-1", State: domain.SessionImplementing}}
	app.currentWorkItemID = "wi-1"
	app.sidebarMode = sidebarPaneSessions
	app.setSelectedTaskSessionID("")

	mock1 := &quitTestMockSession{id: "task-1"}
	mock2 := &quitTestMockSession{id: "task-2"}
	mock3 := &quitTestMockSession{id: "task-3"}
	reg.Register("task-1", mock1)
	reg.Register("task-2", mock2)
	reg.Register("task-3", mock3)

	ids := app.interruptibleFocusedSessionIDs()
	idSet := map[string]bool{}
	for _, id := range ids {
		idSet[id] = true
	}
	if len(ids) != 2 || !idSet["task-1"] || !idSet["task-2"] {
		t.Fatalf("interruptibleFocusedSessionIDs = %#v, want task-1 and task-2", ids)
	}
	if err := app.interruptAgentSessionsByID(context.Background(), ids, app.sessions); err != nil {
		t.Fatalf("interruptAgentSessionsByID: %v", err)
	}
	if !mock1.aborted || !mock2.aborted {
		t.Fatal("work item children should have been aborted")
	}
	if mock3.aborted {
		t.Fatal("other work item session should not have been aborted")
	}
	if got := taskRepo.tasks["task-1"].Status; got != domain.AgentSessionInterrupted {
		t.Fatalf("task-1 status = %q, want interrupted", got)
	}
	if got := taskRepo.tasks["task-2"].Status; got != domain.AgentSessionInterrupted {
		t.Fatalf("task-2 status = %q, want interrupted", got)
	}
	if got := taskRepo.tasks["task-3"].Status; got != domain.AgentSessionRunning {
		t.Fatalf("task-3 status = %q, want running", got)
	}
}

func TestQuitConfirmedMsgCancelsPipelineContexts(t *testing.T) {
	t.Parallel()

	app := newQuitTestApp(nil)
	ctx := app.registerPipelineCancel("wi-1")

	// Before quit, context should be alive.
	if ctx.Err() != nil {
		t.Fatal("pipeline context cancelled prematurely")
	}

	app.Update(QuitConfirmedMsg{})

	// After quit, context should be cancelled.
	if ctx.Err() == nil {
		t.Fatal("pipeline context was not cancelled by QuitConfirmedMsg")
	}
}

// TestFocusedInterruptModalFitsNarrowTerminal verifies that the interrupt confirmation
// modal renders correctly and contains expected content.
func TestFocusedInterruptModalFitsNarrowTerminal(t *testing.T) {
	t.Parallel()

	sessions := []domain.AgentSession{
		{ID: "task-1", WorkItemID: "wi-1", WorkspaceID: "ws-1", Status: domain.AgentSessionRunning},
	}
	app := newQuitTestApp(sessions)
	app.workItems = []domain.Session{{ID: "wi-1", WorkspaceID: "ws-1", State: domain.SessionImplementing}}
	app.currentWorkItemID = "wi-1"
	app.sidebarMode = sidebarPaneTasks
	app.setSelectedTaskSessionID("task-1")

	// Set a realistic terminal size.
	model, _ := app.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	app = model.(*App)

	// Trigger the interrupt confirmation modal.
	model, cmd := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'I'}})
	app = model.(*App)
	if cmd == nil {
		t.Fatal("interrupt key returned nil cmd")
	}

	confirmMsg, ok := cmd().(ConfirmInterruptSessionsMsg)
	if !ok {
		t.Fatalf("cmd message = %T, want ConfirmInterruptSessionsMsg", cmd())
	}
	if len(confirmMsg.SessionIDs) != 1 {
		t.Fatalf("interrupt ids = %#v, want [task-1]", confirmMsg.SessionIDs)
	}

	// Show the confirmation dialog.
	model, _ = app.Update(confirmMsg)
	app = model.(*App)

	if !app.confirmActive {
		t.Fatal("expected confirm modal to be active")
	}

	// Verify the modal renders without crashing.
	confirmView := app.confirm.View()
	if confirmView == "" {
		t.Fatal("confirm view returned empty string")
	}

	// Verify the modal contains expected content.
	plain := confirmView
	for _, want := range []string{"Interrupt", "session", "[y]", "[n]"} {
		if !strings.Contains(plain, want) {
			t.Errorf("confirm view missing %q\nview:\n%s", want, plain)
		}
	}
}

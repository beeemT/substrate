package views

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/beeemT/substrate/internal/domain"
)

// newQuitTestApp creates a minimal App with the given sessions list.
func newQuitTestApp(sessions []domain.Task) App {
	app := NewApp(Services{WorkspaceID: "ws-1", WorkspaceName: "test", Settings: &SettingsService{}})
	app.sessions = sessions
	return app
}

func runningSessions(n int) []domain.Task {
	tasks := make([]domain.Task, n)
	for i := range tasks {
		tasks[i] = domain.Task{ID: "s", Status: domain.AgentSessionRunning}
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
	updated := model.(App)

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
	updated := model.(App)

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
	updated := model.(App)

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
	withConfirm := model.(App)

	if !withConfirm.confirmActive {
		t.Fatal("precondition: expected confirmActive after q with running session")
	}

	model2, cmd := withConfirm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	afterConfirm := model2.(App)

	if afterConfirm.confirmActive {
		t.Fatal("confirmActive = true after y, expected dialog to be dismissed")
	}
	if !isQuitCmd(cmd) {
		t.Fatalf("cmd = %v after confirming quit, want tea.Quit", cmd)
	}
}

// TestQuitConfirmCtrlCForceQuits verifies that pressing ctrl+c inside the quit
// confirm dialog acts as "yes" (confirms the quit).
func TestQuitConfirmCtrlCForceQuits(t *testing.T) {
	t.Parallel()

	app := newQuitTestApp(runningSessions(1))
	model, _ := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	withConfirm := model.(App)

	model2, cmd := withConfirm.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	afterConfirm := model2.(App)

	if afterConfirm.confirmActive {
		t.Fatal("confirmActive = true after ctrl+c on confirm, expected force-quit")
	}
	if !isQuitCmd(cmd) {
		t.Fatalf("cmd = %v after ctrl+c on confirm, want tea.Quit", cmd)
	}
}

// TestQuitConfirmEscCancels verifies that any key other than y/enter/ctrl+c
// cancels the quit dialog without exiting.
func TestQuitConfirmEscCancels(t *testing.T) {
	t.Parallel()

	app := newQuitTestApp(runningSessions(1))
	model, _ := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	withConfirm := model.(App)

	model2, cmd := withConfirm.Update(tea.KeyMsg{Type: tea.KeyEsc})
	afterCancel := model2.(App)

	if afterCancel.confirmActive {
		t.Fatal("confirmActive = true after esc, expected dialog dismissed")
	}
	if cmd != nil {
		t.Fatalf("cmd = %v after esc on confirm, want nil (no quit)", cmd)
	}
}

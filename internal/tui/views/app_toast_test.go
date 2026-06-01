package views

import (
	"context"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/tui/components"
)

var toastANSIPattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripToastANSI(s string) string {
	return toastANSIPattern.ReplaceAllString(s, "")
}

func visibleColumn(line, needle string) int {
	before, _, ok := strings.Cut(line, needle)
	if !ok {
		return -1
	}

	return ansi.StringWidth(before)
}

func findLineContaining(lines []string, needle string) int {
	for i, line := range lines {
		if strings.Contains(line, needle) {
			return i
		}
	}

	return -1
}

func newToastTestApp(t *testing.T) *App {
	t.Helper()

	app := newTestApp(Services{
		WorkspaceID:   "ws-1",
		WorkspaceName: "workspace",
		Settings:      newTestSettingsService(),
	})
	model, _ := app.Update(tea.WindowSizeMsg{Width: 80, Height: 16})
	updated, ok := model.(*App)
	if !ok {
		t.Fatalf("model = %T, want *App", model)
	}

	return updated
}

func updateToastTestApp(t *testing.T, app *App, msg tea.Msg) *App {
	t.Helper()

	model, _ := app.Update(msg)
	updated, ok := model.(*App)
	if !ok {
		t.Fatalf("model = %T, want *App", model)
	}

	return updated
}

func toastCount(app *App) int {
	return reflect.ValueOf(app.toasts).FieldByName("toasts").Len()
}

func toastMessageAndLevel(app *App, index int) (string, components.ToastLevel) {
	toast := reflect.ValueOf(app.toasts).FieldByName("toasts").Index(index)
	return toast.FieldByName("Message").String(), components.ToastLevel(toast.FieldByName("Level").Int())
}

func TestQuestionRaisedMsgShowsInfoToastAcrossSessions(t *testing.T) {
	t.Parallel()

	app := newToastTestApp(t)
	app.currentWorkItemID = "wi-current"
	app.workItems = []domain.Session{{ID: "wi-current", WorkspaceID: "ws-1", State: domain.SessionPlanning}}
	app.sessions = []domain.AgentSession{{ID: "plan-session", WorkItemID: "wi-question", WorkspaceID: "ws-1", Kind: domain.AgentSessionKindPlanning, Status: domain.AgentSessionWaitingForAnswer}}

	longQuestion := "  Should we cut over all services now?\nPlease include the legacy path and deployment order before answering.  "
	updated := updateToastTestApp(t, app, QuestionRaisedMsg{
		WorkItemID: "wi-question",
		SessionID:  "plan-session",
		Question: domain.Question{
			ID:      "q-1",
			Content: longQuestion,
			Stage:   domain.AgentSessionKindPlanning,
		},
	})

	if got := toastCount(updated); got != 1 {
		t.Fatalf("toast count = %d, want 1", got)
	}
	message, level := toastMessageAndLevel(updated, 0)
	if level != components.ToastInfo {
		t.Fatalf("toast level = %v, want ToastInfo", level)
	}
	if !strings.HasPrefix(message, "Planning question: ") {
		t.Fatalf("toast message = %q, want planning question prefix", message)
	}
	preview := strings.TrimPrefix(message, "Planning question: ")
	if len([]rune(preview)) > 60 {
		t.Fatalf("preview rune length = %d, want <= 60 (%q)", len([]rune(preview)), preview)
	}
	if strings.Contains(preview, "\n") || strings.Contains(preview, "  ") {
		t.Fatalf("preview = %q, want collapsed whitespace", preview)
	}
}

func TestRenderTopRightOverlay_RespectsBottomInset(t *testing.T) {
	t.Parallel()

	got := renderTopRightOverlay("aaaaaaaa\nbbbbbbbb\ncccccccc\ndddddddd", "XX\nYY", 8, 1, 1)
	want := "aaaaaaaa\nbbbbXXbb\nccccYYcc\ndddddddd"
	if got != want {
		t.Fatalf("overlay result = %q, want %q", got, want)
	}
}

func TestAppView_RendersStartupIntegrationToastUntilReady(t *testing.T) {
	t.Parallel()

	app := newToastTestApp(t)
	app.startupIntegrationsInProgress = true
	view := stripToastANSI(app.toasts.StackView(app.pinnedToasts()...))
	if !strings.Contains(view, components.SpinnerFrame(0)) || !strings.Contains(view, "Starting") || !strings.Contains(view, "integrations") {
		t.Fatalf("toast stack missing startup integrations spinner/message: %q", view)
	}
	if strings.Contains(view, "| Starting integrations") {
		t.Fatalf("startup integrations toast rendered legacy ASCII spinner: %q", view)
	}

	spinnerMsg, ok := StartupIntegrationsSpinnerTickCmd(app.startupIntegrationSpinner)().(StartupIntegrationsSpinnerTickMsg)
	if !ok {
		t.Fatalf("startup spinner tick command returned %T, want StartupIntegrationsSpinnerTickMsg", spinnerMsg)
	}
	*app.cachedBase = strings.Join([]string{
		"cached startup base",
		strings.Repeat(".", 80),
		strings.Repeat(".", 80),
		strings.Repeat(".", 80),
	}, "\n")
	model, nextSpinnerCmd := app.Update(spinnerMsg)
	updated, ok := model.(*App)
	if !ok {
		t.Fatalf("model = %T, want *App", model)
	}
	if nextSpinnerCmd == nil {
		t.Fatal("startup spinner tick did not schedule the next frame")
	}
	if rendered := stripToastANSI(updated.View()); !strings.Contains(rendered, "cached startup base") {
		t.Fatalf("startup spinner frame rebuilt the full base instead of reusing cached base: %q", rendered)
	}
	view = stripToastANSI(updated.toasts.StackView(updated.pinnedToasts()...))
	if !strings.Contains(view, components.SpinnerFrame(1)) || !strings.Contains(view, "Starting") || !strings.Contains(view, "integrations") {
		t.Fatalf("toast stack did not advance shared spinner frame: %q", view)
	}

	updated = updateToastTestApp(t, updated, StartupIntegrationsReadyMsg{Reload: viewsServicesReload{
		Services: Services{
			WorkspaceID:   "ws-1",
			WorkspaceName: "workspace",
			Settings:      newTestSettingsService(),
		},
		Cfg: &config.Config{},
	}})
	if updated.startupIntegrationsInProgress {
		t.Fatal("startup integrations toast still marked in progress after ready message")
	}
	if view := stripToastANSI(updated.toasts.StackView(updated.pinnedToasts()...)); strings.Contains(view, "Starting") || strings.Contains(view, "integrations") {
		t.Fatalf("startup integrations toast still visible after ready message: %q", view)
	}
}

func TestAppView_RendersToastInUpperRightWithoutGrowingLayout(t *testing.T) {
	t.Parallel()

	app := newToastTestApp(t)
	withoutToast := strings.Split(stripToastANSI(app.View()), "\n")

	app.toasts.AddToast("Workspace initialized", components.ToastSuccess)
	withToast := strings.Split(stripToastANSI(app.View()), "\n")

	if len(withToast) != len(withoutToast) {
		t.Fatalf("line count with toast = %d, want %d", len(withToast), len(withoutToast))
	}

	toastLine := -1
	for i, line := range withToast {
		if strings.Contains(line, "Workspace") {
			toastLine = i

			break
		}
	}
	if toastLine == -1 {
		t.Fatalf("view missing toast: %q", strings.Join(withToast, "\n"))
	}
	if toastLine > 2 {
		t.Fatalf("toast line = %d, want toast near the top of the view", toastLine)
	}
	for i := len(withToast) - 2; i < len(withToast); i++ {
		if i >= 0 && strings.Contains(withToast[i], "Workspace") {
			t.Fatalf("toast rendered in status bar line %d: %q", i, withToast[i])
		}
	}
}

func TestAppView_ReadOnlyToastStacksTransientToastsBelow(t *testing.T) {
	t.Parallel()

	app := newToastTestApp(t)
	_ = app.loadHistoryEntry(SidebarEntry{
		Kind:          SidebarEntrySessionHistory,
		SessionID:     "sess-remote",
		WorkspaceID:   "ws-remote",
		WorkspaceName: "remote",
		ExternalID:    "SUB-2",
		Title:         "Remote item",
	})
	app.toasts.AddToast("First toast", components.ToastInfo)
	app.toasts.AddToast("Second toast", components.ToastSuccess)

	rendered := app.View()
	assertAppViewFitsWindow(t, rendered, 80, 16)
	lines := strings.Split(stripToastANSI(rendered), "\n")

	readOnlyLine := findLineContaining(lines, "Read only")
	secondLine := findLineContaining(lines, "Second toast")
	firstLine := findLineContaining(lines, "First toast")
	if readOnlyLine == -1 || secondLine == -1 || firstLine == -1 {
		t.Fatalf("view missing stacked toasts: %q", strings.Join(lines, "\n"))
	}
	if readOnlyLine >= secondLine || secondLine >= firstLine {
		t.Fatalf("toast order = read-only:%d second:%d first:%d, want read only above transient stack", readOnlyLine, secondLine, firstLine)
	}
}

func TestAppView_ReadOnlyToastStackRightAlignsNarrowerToasts(t *testing.T) {
	t.Parallel()

	app := newToastTestApp(t)
	_ = app.loadHistoryEntry(SidebarEntry{
		Kind:          SidebarEntrySessionHistory,
		SessionID:     "sess-remote",
		WorkspaceID:   "ws-remote",
		WorkspaceName: "remote",
		ExternalID:    "SUB-2",
		Title:         "Remote item",
	})
	app.toasts.AddToast("tiny", components.ToastInfo)
	app.toasts.AddToast("This transient toast is intentionally much longer", components.ToastSuccess)

	rendered := app.View()
	assertAppViewFitsWindow(t, rendered, 80, 16)
	lines := strings.Split(stripToastANSI(rendered), "\n")

	readOnlyLine := findLineContaining(lines, "Read only")
	tinyLine := findLineContaining(lines, "tiny")
	longLine := findLineContaining(lines, "This transient")
	if readOnlyLine == -1 || tinyLine == -1 || longLine == -1 {
		t.Fatalf("view missing stacked toasts: %q", strings.Join(lines, "\n"))
	}

	// All toasts in a stack share the same left edge (the widest toast's left edge).
	readOnlyCol := visibleColumn(lines[readOnlyLine], "│")
	tinyCol := visibleColumn(lines[tinyLine], "│")
	longCol := visibleColumn(lines[longLine], "│")
	if readOnlyCol == -1 || tinyCol == -1 || longCol == -1 {
		t.Fatalf("toast columns not found in lines: read-only=%q tiny=%q long=%q", lines[readOnlyLine], lines[tinyLine], lines[longLine])
	}
	if readOnlyCol != longCol || tinyCol != longCol {
		t.Fatalf("toast left edges = read-only:%d tiny:%d long:%d, want equal shared left edge", readOnlyCol, tinyCol, longCol)
	}
}

func TestAppView_ReadOnlyToastStackFitsNarrowWindow(t *testing.T) {
	t.Parallel()

	app := newToastTestApp(t)
	model, _ := app.Update(tea.WindowSizeMsg{Width: 36, Height: 12})
	updated, ok := model.(*App)
	if !ok {
		t.Fatalf("model = %T, want *App", model)
	}
	updated.loadHistoryEntry(SidebarEntry{
		Kind:          SidebarEntrySessionHistory,
		SessionID:     "sess-remote",
		WorkspaceID:   "ws-remote",
		WorkspaceName: "remote",
		ExternalID:    "SUB-2",
		Title:         "Remote item",
	})
	updated.toasts.AddToast("Sync complete", components.ToastSuccess)

	lines := assertAppViewFitsWindow(t, updated.View(), 36, 12)
	assertBodyEndsAboveFooter(t, lines)
	plain := stripToastANSI(strings.Join(lines, "\n"))
	for _, want := range []string{"Read", "Sync"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("view = %q, want %q in narrow toast stack", plain, want)
		}
	}
}

func TestAppView_MultipleTransientToastsStack(t *testing.T) {
	t.Parallel()

	app := newToastTestApp(t)
	app.toasts.AddToast("First toast", components.ToastInfo)
	app.toasts.AddToast("Second toast", components.ToastSuccess)
	app.toasts.AddToast("Third toast", components.ToastWarning)

	rendered := app.View()
	assertAppViewFitsWindow(t, rendered, 80, 16)
	lines := strings.Split(stripToastANSI(rendered), "\n")

	firstLine := findLineContaining(lines, "First toast")
	secondLine := findLineContaining(lines, "Second toast")
	thirdLine := findLineContaining(lines, "Third toast")
	if firstLine == -1 || secondLine == -1 || thirdLine == -1 {
		t.Fatalf("view missing stacked toasts (first=%d second=%d third=%d): %q",
			firstLine, secondLine, thirdLine, strings.Join(lines, "\n"))
	}
	// StackView appends newest first, so order top-to-bottom is: Third, Second, First
	if thirdLine >= secondLine || secondLine >= firstLine {
		t.Fatalf("toast order = third:%d second:%d first:%d, want newest on top", thirdLine, secondLine, firstLine)
	}
}

func TestAppView_ToastRendersOnLogsOverlay(t *testing.T) {
	t.Parallel()

	app := newToastTestApp(t)
	app.logsOverlay.SetSize(80, 16)
	app.logsOverlay.Open()
	app.activeOverlay = overlayLogs
	app.toasts.AddToast("Saved", components.ToastSuccess)

	rendered := app.View()
	lines := strings.Split(stripToastANSI(rendered), "\n")

	toastLine := findLineContaining(lines, "Saved")
	if toastLine == -1 {
		t.Fatalf("toast not visible on logs overlay: %q", strings.Join(lines, "\n"))
	}
}

func TestAppView_ToastRendersOnSettingsOverlay(t *testing.T) {
	t.Parallel()

	app := newToastTestApp(t)
	app.activeOverlay = overlaySettings
	app.toasts.AddToast("Saved", components.ToastSuccess)

	rendered := app.View()
	lines := strings.Split(stripToastANSI(rendered), "\n")

	toastLine := findLineContaining(lines, "Saved")
	if toastLine == -1 {
		t.Fatalf("toast not visible on settings overlay: %q", strings.Join(lines, "\n"))
	}
}

func TestAppView_PinsHarnessWarningAboveTransientToasts(t *testing.T) {
	t.Parallel()

	svc := newTestSettingsService()
	cfg := &config.Config{
		Harness: config.HarnessConfig{
			Default: config.HarnessClaudeCode,
		},
		Adapters: config.AdaptersConfig{
			ClaudeCode: config.ClaudeCodeConfig{
				BridgePath: filepath.Join(t.TempDir(), "missing-bridge"),
			},
		},
	}
	if err := svc.RefreshWithDiagnostics(context.Background(), cfg); err != nil {
		t.Fatalf("RefreshWithDiagnostics: %v", err)
	}

	app := newTestApp(Services{
		WorkspaceID:   "ws-1",
		WorkspaceName: "workspace",
		Settings:      svc,
	})
	model, _ := app.Update(tea.WindowSizeMsg{Width: 80, Height: 16})
	updated, ok := model.(*App)
	if !ok {
		t.Fatalf("model = %T, want *App", model)
	}
	updated.toasts.AddToast("Sync complete", components.ToastSuccess)

	rendered := updated.View()
	assertAppViewFitsWindow(t, rendered, 80, 16)
	lines := strings.Split(stripToastANSI(rendered), "\n")
	warningLine := findLineContaining(lines, "unavailable. Check")
	syncLine := findLineContaining(lines, "Sync complete")
	if warningLine == -1 || syncLine == -1 {
		t.Fatalf("view missing warning stack: %q", strings.Join(lines, "\n"))
	}
	if warningLine >= syncLine {
		t.Fatalf("toast order = warning:%d sync:%d, want pinned warning above transient toast", warningLine, syncLine)
	}
}

func TestAppUpdate_NewSessionAutonomousStartToastDeduplicatesStatusInfo(t *testing.T) {
	t.Parallel()

	app := newToastTestApp(t)
	runtime := &NewSessionAutonomousRuntime{}
	events := make(chan tea.Msg)

	app = updateToastTestApp(t, app, NewSessionAutonomousStartedMsg{
		Runtime: runtime,
		Events:  events,
		Message: autonomousLifecycleStartedToast,
	})
	app = updateToastTestApp(t, app, NewSessionAutonomousStatusMsg{
		Level:   "info",
		Message: autonomousLifecycleStartedToast,
	})

	if got := toastCount(app); got != 1 {
		t.Fatalf("start toast count = %d, want 1", got)
	}
}

func TestAppUpdate_NewSessionAutonomousStopToastDeduplicatesRepeatedStopMessages(t *testing.T) {
	t.Parallel()

	app := newToastTestApp(t)
	runtime := &NewSessionAutonomousRuntime{}
	events := make(chan tea.Msg)

	app = updateToastTestApp(t, app, NewSessionAutonomousStartedMsg{
		Runtime: runtime,
		Events:  events,
	})
	app = updateToastTestApp(t, app, NewSessionAutonomousStoppedMsg{Message: autonomousLifecycleStoppedToast})
	app = updateToastTestApp(t, app, NewSessionAutonomousStoppedMsg{Message: autonomousLifecycleStoppedToast})

	if got := toastCount(app); got != 1 {
		t.Fatalf("stop toast count = %d, want 1", got)
	}
}

func TestAppUpdate_NewSessionAutonomousStatusKeepsWarningErrorAndNonLifecycleInfo(t *testing.T) {
	t.Parallel()

	app := newToastTestApp(t)
	app = updateToastTestApp(t, app, NewSessionAutonomousStatusMsg{Level: "warning", Message: "Watch degraded"})
	app = updateToastTestApp(t, app, NewSessionAutonomousStatusMsg{Level: "error", Message: "Watch failed"})
	app = updateToastTestApp(t, app, NewSessionAutonomousStatusMsg{Level: "info", Message: "Heartbeat"})

	if got := toastCount(app); got != 3 {
		t.Fatalf("status toast count = %d, want 3", got)
	}
}

func TestAppUpdate_QuitConfirmedStopsActiveNewSessionAutonomousRuntime(t *testing.T) {
	t.Parallel()

	app := newToastTestApp(t)
	done := make(chan struct{})
	stopCalled := false
	runtime := &NewSessionAutonomousRuntime{
		started: true,
		done:    done,
	}
	runtime.cancel = func() {
		stopCalled = true
		close(done)
	}

	app.newSessionAutonomous = runtime
	app.newSessionAutonomousChan = make(chan tea.Msg)
	app.syncNewSessionFilterOverlays()

	model, _ := app.Update(QuitConfirmedMsg{})
	updated, ok := model.(*App)
	if !ok {
		t.Fatalf("model = %T, want *App", model)
	}

	if !stopCalled {
		t.Fatal("expected teardown to stop active autonomous runtime")
	}
	if updated.newSessionAutonomous != nil {
		t.Fatal("expected teardown to clear autonomous runtime")
	}
	if updated.newSessionAutonomousChan != nil {
		t.Fatal("expected teardown to clear autonomous runtime channel")
	}
	if updated.newSessionAutonomousOverlay.running {
		t.Fatal("expected teardown to sync overlay runtime state to stopped")
	}
}

func TestAppUpdate_FollowUpPlanResultSuccessToastSaysReadyForReview(t *testing.T) {
	t.Parallel()

	app := newToastTestApp(t)
	app = updateToastTestApp(t, app, FollowUpPlanResultMsg{WorkItemID: "wi-1"})

	view := strings.ReplaceAll(stripToastANSI(app.toasts.StackView()), "\n", "")
	if !strings.Contains(view, "Follow-up plan") || !strings.Contains(view, "ready for review") {
		t.Fatalf("toast view = %q, want ready-for-review copy", view)
	}
	if strings.Contains(view, "Follow-up planning started") {
		t.Fatalf("toast view = %q, must not claim planning merely started after blocking command completed", view)
	}
}

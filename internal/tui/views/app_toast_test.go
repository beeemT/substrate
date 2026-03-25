package views

import (
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/tui/components"
)

var toastANSIPattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripToastANSI(s string) string {
	return toastANSIPattern.ReplaceAllString(s, "")
}

func lastNonSpaceColumn(line string) int {
	trimmed := strings.TrimRight(line, " ")
	if trimmed == "" {
		return -1
	}

	return ansi.StringWidth(trimmed) - 1
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

func newToastTestApp(t *testing.T) App {
	t.Helper()

	cfg := &config.Config{}
	app := NewApp(Services{
		WorkspaceID:   "ws-1",
		WorkspaceName: "workspace",
		Settings:      &SettingsService{},
		SettingsData: SettingsSnapshot{
			Sections:  buildSettingsSections(cfg),
			Providers: buildProviderStatuses(cfg),
		},
	})
	model, _ := app.Update(tea.WindowSizeMsg{Width: 80, Height: 16})
	updated, ok := model.(App)
	if !ok {
		t.Fatalf("model = %T, want App", model)
	}

	return updated
}

func TestRenderTopRightOverlay_RespectsBottomInset(t *testing.T) {
	t.Parallel()

	got := renderTopRightOverlay("aaaaaaaa\nbbbbbbbb\ncccccccc\ndddddddd", "XX\nYY", 8, 1, 1)
	want := "aaaaaaaa\nbbbbXXbb\nccccYYcc\ndddddddd"
	if got != want {
		t.Fatalf("overlay result = %q, want %q", got, want)
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
	for i := len(withToast) - statusBarHeight; i < len(withToast); i++ {
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
	updated, ok := model.(App)
	if !ok {
		t.Fatalf("model = %T, want App", model)
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

	cfg := &config.Config{}
	app := NewApp(Services{
		WorkspaceID:   "ws-1",
		WorkspaceName: "workspace",
		Settings:      &SettingsService{},
		SettingsData: SettingsSnapshot{
			Sections:       buildSettingsSections(cfg),
			Providers:      buildProviderStatuses(cfg),
			HarnessWarning: "Planning unavailable. Check Harness Routing.",
		},
	})
	model, _ := app.Update(tea.WindowSizeMsg{Width: 80, Height: 16})
	updated, ok := model.(App)
	if !ok {
		t.Fatalf("model = %T, want App", model)
	}
	updated.toasts.AddToast("Sync complete", components.ToastSuccess)

	rendered := updated.View()
	assertAppViewFitsWindow(t, rendered, 80, 16)
	lines := strings.Split(stripToastANSI(rendered), "\n")
	warningLine := findLineContaining(lines, "Planning")
	syncLine := findLineContaining(lines, "Sync complete")
	if warningLine == -1 || syncLine == -1 {
		t.Fatalf("view missing warning stack: %q", strings.Join(lines, "\n"))
	}
	if warningLine >= syncLine {
		t.Fatalf("toast order = warning:%d sync:%d, want pinned warning above transient toast", warningLine, syncLine)
	}
}

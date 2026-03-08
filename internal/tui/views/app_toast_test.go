package views

import (
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/tui/components"
)

var toastANSIPattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripToastANSI(s string) string {
	return toastANSIPattern.ReplaceAllString(s, "")
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
	want := "aaaaaaaa\nbbbbbbXX\nccccccYY\ndddddddd"
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
		if strings.Contains(line, "Workspace initialized") {
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
		if i >= 0 && strings.Contains(withToast[i], "Workspace initialized") {
			t.Fatalf("toast rendered in status bar line %d: %q", i, withToast[i])
		}
	}
}

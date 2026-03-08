package views

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/beeemT/substrate/internal/config"
)

func TestApp_EscClosesSettingsOverlay(t *testing.T) {
	t.Parallel()

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
	app.activeOverlay = overlaySettings
	app.settingsPage.Open()

	model, cmd := app.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("expected Esc to emit a close-overlay command while settings is open")
	}

	msg := cmd()
	if _, ok := msg.(CloseOverlayMsg); !ok {
		t.Fatalf("msg = %T, want CloseOverlayMsg", msg)
	}

	updated, ok := model.(App)
	if !ok {
		t.Fatalf("model = %T, want App", model)
	}

	model, _ = updated.Update(msg)
	closed, ok := model.(App)
	if !ok {
		t.Fatalf("closed model = %T, want App", model)
	}
	if closed.activeOverlay != overlayNone {
		t.Fatalf("activeOverlay = %v, want %v", closed.activeOverlay, overlayNone)
	}
	if closed.settingsPage.Active() {
		t.Fatal("expected settings page to be inactive after closing overlay")
	}
}

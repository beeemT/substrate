package views

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestAppLKeyOpensLogsOverlay asserts that pressing 'L' opens the logs overlay.
func TestAppLKeyOpensLogsOverlay(t *testing.T) {
	t.Parallel()

	app := NewApp(Services{
		WorkspaceID:   "ws-local",
		WorkspaceName: "local",
		Settings:      &SettingsService{},
	})
	model, _ := app.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	updated, ok := model.(*App)
	if !ok {
		t.Fatalf("model = %T, want *App", model)
	}

	// Verify no overlay is active initially.
	if updated.activeOverlay != overlayNone {
		t.Fatalf("activeOverlay = %v, want overlayNone initially", updated.activeOverlay)
	}

	// Press 'L' — should open the logs overlay.
	model, cmd := updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'L'}})
	updated, ok = model.(*App)
	if !ok {
		t.Fatalf("model = %T, want *App after L key", model)
	}

	// Command should be nil (the L handler returns nil, not a cmd).
	if cmd != nil {
		t.Fatalf("expected nil cmd from 'L' key, got %T", cmd)
	}

	if updated.activeOverlay != overlayLogs {
		t.Fatalf("activeOverlay = %v, want overlayLogs after pressing L", updated.activeOverlay)
	}

	// Verify the logs overlay is ready (Open() was called).
	if !updated.logsOverlay.ready {
		t.Fatal("logsOverlay should be ready after L key opens it")
	}
}

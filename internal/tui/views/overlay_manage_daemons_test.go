package views

import (
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/beeemT/substrate/internal/config"
)

func TestAppDKeyOpensManageDaemonsOverlay(t *testing.T) {
	t.Parallel()

	app := newTestApp(Services{WorkspaceID: "ws-1", WorkspaceName: "ws", Settings: newTestSettingsService()})
	model, _ := app.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	updated := model.(*App)

	model, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'D'}})
	updated = model.(*App)

	if updated.activeOverlay != overlayManageDaemons {
		t.Fatalf("activeOverlay = %v, want overlayManageDaemons", updated.activeOverlay)
	}
	if !updated.manageDaemons.Active() {
		t.Fatal("manage daemons overlay is not active")
	}
}

func TestManageDaemonsOverlaySwitchAndRemove(t *testing.T) {
	t.Setenv("SUBSTRATE_HOME", t.TempDir())
	cfg := &config.Config{}
	cfg.TUI.ActiveDaemon = "local"
	cfg.TUI.Daemons = map[string]config.DaemonRegistryEntry{
		"local":  {Kind: "local", Label: "Local", AutoManaged: true},
		"remote": {Kind: "remote", Label: "Remote", Address: "127.0.0.1:1", TokenRef: "env:SUBSTRATE_TEST_TOKEN"},
	}
	path, err := config.ConfigPath()
	if err != nil {
		t.Fatalf("ConfigPath() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := config.Save(path, cfg); err != nil {
		t.Fatalf("config.Save() error = %v", err)
	}

	overlay := NewManageDaemonsOverlay(cfg, testStyles())
	overlay.Open()
	model, cmd := overlay.Update(tea.KeyMsg{Type: tea.KeyDown})
	overlay = model
	if cmd != nil {
		t.Fatal("down key returned a command")
	}
	model, cmd = overlay.Update(tea.KeyMsg{Type: tea.KeyEnter})
	overlay = model
	if cmd == nil {
		t.Fatal("switch key returned nil command")
	}
	msg, ok := cmd().(DaemonRegistryResultMsg)
	if !ok {
		t.Fatalf("cmd() = %T, want DaemonRegistryResultMsg", cmd())
	}
	if msg.Err != nil {
		t.Fatalf("switch daemon error = %v", msg.Err)
	}
	if cfg.TUI.ActiveDaemon != "remote" {
		t.Fatalf("active daemon = %q, want remote", cfg.TUI.ActiveDaemon)
	}

	model, cmd = overlay.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if cmd == nil {
		t.Fatal("remove key returned nil command")
	}
	msg, ok = cmd().(DaemonRegistryResultMsg)
	if !ok {
		t.Fatalf("remove cmd() = %T, want DaemonRegistryResultMsg", cmd())
	}
	if msg.Err != nil {
		t.Fatalf("remove daemon error = %v", msg.Err)
	}
	if _, ok := cfg.TUI.Daemons["remote"]; ok {
		t.Fatal("remote daemon still exists after remove")
	}
}

func TestManageDaemonsOverlayRejectsLocalRemove(t *testing.T) {
	t.Setenv("SUBSTRATE_HOME", t.TempDir())
	cfg := &config.Config{TUI: config.TUIConfig{Daemons: map[string]config.DaemonRegistryEntry{"local": {Kind: "local", AutoManaged: true}}}}
	path, err := config.ConfigPath()
	if err != nil {
		t.Fatalf("ConfigPath() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := config.Save(path, cfg); err != nil {
		t.Fatalf("config.Save() error = %v", err)
	}
	overlay := NewManageDaemonsOverlay(cfg, testStyles())
	overlay.Open()
	_, cmd := overlay.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if cmd == nil {
		t.Fatal("remove key returned nil command")
	}
	msg := cmd().(DaemonRegistryResultMsg)
	if msg.Err == nil {
		t.Fatal("remove local daemon error = nil")
	}
}


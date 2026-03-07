package views

import (
	"strings"
	"testing"

	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/tui/styles"
	tea "github.com/charmbracelet/bubbletea"
)

func TestSettingsPage_TogglesBoolField(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{}
	cfg.Foreman.Enabled = true
	page := NewSettingsPage(&SettingsService{}, SettingsSnapshot{Sections: buildSettingsSections(cfg), Providers: buildProviderStatuses(cfg)}, styles.NewStyles(styles.DefaultTheme))
	page.sectionCursor = 3
	page.fieldCursor = 0

	updated, _ := page.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" ")}, Services{})
	if got := updated.sections[3].Fields[0].Value; got != "false" {
		t.Fatalf("bool field = %q, want false", got)
	}
	if !updated.dirty {
		t.Fatal("expected page to become dirty")
	}
}

func TestSettingsPage_RevealSecretsMasksByDefault(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{}
	cfg.Adapters.GitHub.Token = "secret-token"
	page := NewSettingsPage(&SettingsService{}, SettingsSnapshot{Sections: buildSettingsSections(cfg), Providers: buildProviderStatuses(cfg)}, styles.NewStyles(styles.DefaultTheme))
	page.sectionCursor = 10
	page.fieldCursor = 0
	page.SetSize(120, 40)

	view := page.View()
	if strings.Contains(view, "secret-token") {
		t.Fatal("expected token to be masked by default")
	}

	updated, _ := page.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")}, Services{})
	view = updated.View()
	if !strings.Contains(view, "secret-token") {
		t.Fatal("expected token to be revealed after toggle")
	}
}

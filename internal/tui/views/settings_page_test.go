package views

import (
	"strings"
	"testing"

	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/tui/styles"
	tea "github.com/charmbracelet/bubbletea"
)

func newTestSettingsPage(cfg *config.Config) SettingsPage {
	page := NewSettingsPage(&SettingsService{}, SettingsSnapshot{Sections: buildSettingsSections(cfg), Providers: buildProviderStatuses(cfg)}, styles.NewStyles(styles.DefaultTheme))
	page.SetSize(120, 40)
	return page
}

func findSectionIndex(t *testing.T, page SettingsPage, sectionID string) int {
	t.Helper()
	for i, section := range page.sections {
		if section.ID == sectionID {
			return i
		}
	}
	t.Fatalf("section %q not found", sectionID)
	return -1
}

func findFieldIndex(t *testing.T, page SettingsPage, sectionID, key string) int {
	t.Helper()
	sectionIndex := findSectionIndex(t, page, sectionID)
	for i, field := range page.sections[sectionIndex].Fields {
		if field.Key == key {
			return i
		}
	}
	t.Fatalf("field %q not found in section %q", key, sectionID)
	return -1
}

func TestSettingsPage_TogglesBoolField(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{}
	cfg.Adapters.Codex.FullAuto = true
	page := newTestSettingsPage(cfg)

	sectionIndex := findSectionIndex(t, page, "harness.codex")
	fieldIndex := findFieldIndex(t, page, "harness.codex", "full_auto")
	page.sectionCursor = sectionIndex
	page.fieldCursor = fieldIndex
	page.focus = settingsFocusFields

	updated, _ := page.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" ")}, Services{})
	if got := updated.sections[sectionIndex].Fields[fieldIndex].Value; got != "false" {
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
	page := newTestSettingsPage(cfg)
	page.sectionCursor = findSectionIndex(t, page, "provider.github")
	page.fieldCursor = 0

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

func TestSettingsPage_GroupFocusMovesSectionsOnly(t *testing.T) {
	t.Parallel()
	page := newTestSettingsPage(&config.Config{})
	if len(page.sections) < 2 {
		t.Fatal("expected at least two settings sections")
	}
	page.sectionCursor = 0
	page.fieldCursor = 3

	updated, _ := page.Update(tea.KeyMsg{Type: tea.KeyDown}, Services{})
	if updated.sectionCursor != 1 {
		t.Fatalf("sectionCursor = %d, want 1", updated.sectionCursor)
	}
	if updated.fieldCursor != 0 {
		t.Fatalf("fieldCursor = %d, want 0 after changing groups", updated.fieldCursor)
	}
	if updated.focus != settingsFocusSections {
		t.Fatalf("focus = %v, want groups focus", updated.focus)
	}
}

func TestSettingsPage_EnterFocusesFieldsAndEscReturnsToGroups(t *testing.T) {
	t.Parallel()
	page := newTestSettingsPage(&config.Config{})
	page.sectionCursor = findSectionIndex(t, page, "provider.github")
	if len(page.sections[page.sectionCursor].Fields) < 2 {
		t.Fatal("expected provider.github to have at least two fields")
	}

	updated, cmd := page.Update(tea.KeyMsg{Type: tea.KeyRight}, Services{})
	if cmd != nil {
		t.Fatal("expected Right on a group to change focus without emitting a command")
	}
	if updated.focus != settingsFocusFields {
		t.Fatalf("focus = %v, want field focus after Right", updated.focus)
	}

	updated, cmd = updated.Update(tea.KeyMsg{Type: tea.KeyLeft}, Services{})
	if cmd != nil {
		t.Fatal("expected Left in field focus to return to groups without emitting a command")
	}
	if updated.focus != settingsFocusSections {
		t.Fatalf("focus = %v, want groups focus after Left", updated.focus)
	}

	updated, cmd = updated.Update(tea.KeyMsg{Type: tea.KeyEnter}, Services{})
	if cmd != nil {
		t.Fatal("expected Enter on a group to change focus without emitting a command")
	}
	if updated.focus != settingsFocusFields {
		t.Fatalf("focus = %v, want field focus after Enter", updated.focus)
	}
	if updated.fieldCursor != 0 {
		t.Fatalf("fieldCursor = %d, want 0 on entering fields", updated.fieldCursor)
	}

	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown}, Services{})
	if updated.sectionCursor != page.sectionCursor {
		t.Fatalf("sectionCursor = %d, want %d while moving through fields", updated.sectionCursor, page.sectionCursor)
	}
	if updated.fieldCursor != 1 {
		t.Fatalf("fieldCursor = %d, want 1 after moving down within fields", updated.fieldCursor)
	}

	updated, cmd = updated.Update(tea.KeyMsg{Type: tea.KeyEsc}, Services{})
	if cmd != nil {
		t.Fatal("expected Esc in field focus to return to groups without closing overlay")
	}
	if updated.focus != settingsFocusSections {
		t.Fatalf("focus = %v, want groups focus after Esc", updated.focus)
	}

	_, cmd = updated.Update(tea.KeyMsg{Type: tea.KeyEsc}, Services{})
	if cmd == nil {
		t.Fatal("expected Esc in group focus to emit close-overlay command")
	}
	if _, ok := cmd().(CloseOverlayMsg); !ok {
		t.Fatalf("msg = %T, want CloseOverlayMsg", cmd())
	}
}

func TestSettingsPage_FieldFocusCrossesGroupBoundaries(t *testing.T) {
	t.Parallel()
	page := newTestSettingsPage(&config.Config{})

	fieldSections := make([]int, 0, len(page.sections))
	for i, section := range page.sections {
		if len(section.Fields) > 0 {
			fieldSections = append(fieldSections, i)
		}
	}
	if len(fieldSections) < 3 {
		t.Fatal("expected at least three field-bearing settings sections")
	}

	prevIndex := fieldSections[0]
	middleIndex := fieldSections[1]
	nextIndex := fieldSections[2]

	page.sectionCursor = middleIndex
	page.fieldCursor = 0
	page.focus = settingsFocusFields

	updated, _ := page.Update(tea.KeyMsg{Type: tea.KeyUp}, Services{})
	if updated.sectionCursor != prevIndex {
		t.Fatalf("sectionCursor = %d, want previous field-bearing section %d", updated.sectionCursor, prevIndex)
	}
	if updated.fieldCursor != len(updated.sections[prevIndex].Fields)-1 {
		t.Fatalf("fieldCursor = %d, want last field of previous section", updated.fieldCursor)
	}
	if updated.focus != settingsFocusFields {
		t.Fatalf("focus = %v, want field focus after crossing to previous group", updated.focus)
	}

	page.sectionCursor = middleIndex
	page.fieldCursor = len(page.sections[middleIndex].Fields) - 1
	page.focus = settingsFocusFields

	updated, _ = page.Update(tea.KeyMsg{Type: tea.KeyDown}, Services{})
	if updated.sectionCursor != nextIndex {
		t.Fatalf("sectionCursor = %d, want next field-bearing section %d", updated.sectionCursor, nextIndex)
	}
	if updated.fieldCursor != 0 {
		t.Fatalf("fieldCursor = %d, want first field of next section", updated.fieldCursor)
	}
	if updated.focus != settingsFocusFields {
		t.Fatalf("focus = %v, want field focus after crossing to next group", updated.focus)
	}
}

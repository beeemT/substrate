package views

import (
	"fmt"
	"strings"
	"testing"

	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/tui/styles"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
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

func TestSettingsPage_TreeCollapseAndExpandHarnessSections(t *testing.T) {
	t.Parallel()
	page := newTestSettingsPage(&config.Config{})

	harnessIndex := findSectionIndex(t, page, "harness")
	ohmypiIndex := findSectionIndex(t, page, "harness.ohmypi")
	linearIndex := findSectionIndex(t, page, "provider.linear")
	page.sectionCursor = harnessIndex
	page.focus = settingsFocusSections

	updated, _ := page.Update(tea.KeyMsg{Type: tea.KeyLeft}, Services{})
	if updated.expandedSections["harness"] {
		t.Fatal("expected Left on harness root to collapse its tree branch")
	}
	if updated.sectionCursor != harnessIndex {
		t.Fatalf("sectionCursor = %d, want harness root %d after collapse", updated.sectionCursor, harnessIndex)
	}

	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown}, Services{})
	if updated.navCursor != "group.providers" {
		t.Fatalf("navCursor = %q, want group.providers after moving to synthetic providers group", updated.navCursor)
	}
	if updated.sectionCursor != linearIndex {
		t.Fatalf("sectionCursor = %d, want provider.linear %d when collapsed branch is skipped", updated.sectionCursor, linearIndex)
	}

	updated.sectionCursor = harnessIndex
	updated.navCursor = page.sections[harnessIndex].ID

	updated.sectionCursor = harnessIndex
	updated.navCursor = page.sections[harnessIndex].ID
	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRight}, Services{})
	if !updated.expandedSections["harness"] {
		t.Fatal("expected Right on collapsed harness root to expand its tree branch")
	}
	if updated.sectionCursor != harnessIndex {
		t.Fatalf("sectionCursor = %d, want harness root %d after expanding", updated.sectionCursor, harnessIndex)
	}

	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRight}, Services{})
	if updated.sectionCursor != ohmypiIndex {
		t.Fatalf("sectionCursor = %d, want first harness child %d after moving into expanded branch", updated.sectionCursor, ohmypiIndex)
	}
}

func TestSettingsPage_ViewShowsTreeAndFieldHelp(t *testing.T) {
	t.Parallel()
	page := newTestSettingsPage(&config.Config{})
	page.sectionCursor = findSectionIndex(t, page, "provider.github")
	page.fieldCursor = findFieldIndex(t, page, "provider.github", "token_ref")

	view := page.View()
	if !strings.Contains(view, "▾ Harness Routing") {
		t.Fatal("expected tree sidebar to show expandable harness root")
	}
	if !strings.Contains(view, "▾ Providers") {
		t.Fatal("expected tree sidebar to show synthetic Providers group")
	}
	if !strings.Contains(view, "▾ Repo lifecycle") {
		t.Fatal("expected tree sidebar to show synthetic Repo lifecycle group")
	}
	if !strings.Contains(view, "Oh My Pi") {
		t.Fatal("expected tree sidebar to show harness child label without the full dotted title")
	}
	if strings.Contains(view, "Harness Routing · configured") {
		t.Fatal("expected configured status to be removed from the sidebar tree")
	}
	if !strings.Contains(view, "Section status: configured") {
		t.Fatal("expected section status to be rendered in the main pane")
	}
	if strings.Contains(view, "Context:") {
		t.Fatal("expected inline context labels to be replaced by the sticky section header")
	}
	if strings.Contains(view, "Focus:") {
		t.Fatal("expected explicit focus text to be removed in favor of border highlighting")
	}
	if !strings.Contains(view, "GitHub token stored in config or the OS keychain") {
		t.Fatal("expected field explanation to be rendered in the sticky detail pane")
	}
	if !strings.Contains(view, "Current: <empty>") {
		t.Fatal("expected sticky detail pane to show the current field value")
	}
	if !strings.Contains(view, "Default: empty") {
		t.Fatal("expected field default to be rendered in the sticky detail pane")
	}
	if !strings.Contains(view, "▐") {
		t.Fatal("expected the main viewport to render the custom narrow scrollbar thumb")
	}
}

func TestSettingsPage_SyntheticProvidersGroupCollapsesAndExpands(t *testing.T) {
	t.Parallel()
	page := newTestSettingsPage(&config.Config{})

	providerChildren := page.syntheticGroupChildren("group.providers")
	if len(providerChildren) < 2 {
		t.Fatal("expected synthetic providers group to contain at least two providers")
	}
	linearIndex := providerChildren[0]
	nextProviderIndex := providerChildren[1]
	page.sectionCursor = linearIndex
	page.navCursor = "group.providers"
	page.focus = settingsFocusSections

	updated, _ := page.Update(tea.KeyMsg{Type: tea.KeyLeft}, Services{})
	if updated.expandedSections["group.providers"] {
		t.Fatal("expected Left on synthetic providers node to collapse the branch")
	}
	if updated.navCursor != "group.providers" {
		t.Fatalf("navCursor = %q, want group.providers after collapse", updated.navCursor)
	}

	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown}, Services{})
	if updated.navCursor != "group.repo-lifecycle" {
		t.Fatalf("navCursor = %q, want group.repo-lifecycle when moving past a collapsed providers group", updated.navCursor)
	}

	updated.navCursor = "group.providers"
	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRight}, Services{})
	if !updated.expandedSections["group.providers"] {
		t.Fatal("expected Right on synthetic providers node to expand the branch")
	}
	if updated.navCursor != "group.providers" {
		t.Fatalf("navCursor = %q, want group.providers after expansion", updated.navCursor)
	}

	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRight}, Services{})
	if updated.sectionCursor != linearIndex {
		t.Fatalf("sectionCursor = %d, want provider.linear %d after entering synthetic group", updated.sectionCursor, linearIndex)
	}
	if updated.navCursor != page.sections[linearIndex].ID {
		t.Fatalf("navCursor = %q, want %q after entering first provider child", updated.navCursor, page.sections[linearIndex].ID)
	}

	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown}, Services{})
	if updated.sectionCursor != nextProviderIndex {
		t.Fatalf("sectionCursor = %d, want next provider child %d when moving within providers branch", updated.sectionCursor, nextProviderIndex)
	}
}

func TestSettingsPage_RenderStickySectionHeaderShowsSelectedSection(t *testing.T) {
	t.Parallel()

	page := newTestSettingsPage(&config.Config{})
	page.sectionCursor = findSectionIndex(t, page, "provider.github")

	rendered := page.renderStickySectionHeader(32)
	if !strings.Contains(rendered, "GitHub") {
		t.Fatal("expected sticky header to show the selected section title")
	}
	if !strings.Contains(rendered, "Providers") {
		t.Fatal("expected sticky header to show the selected section breadcrumb")
	}
	if strings.Contains(rendered, "Context:") {
		t.Fatal("expected sticky header to avoid the old context label prefix")
	}
}

func TestSettingsPage_BuildMainDocumentOmitsContextLabels(t *testing.T) {
	t.Parallel()

	page := newTestSettingsPage(&config.Config{})
	doc, _, _ := page.buildMainDocument(40)
	if strings.Contains(doc, "Context:") {
		t.Fatal("expected inline context labels to be removed from the main document")
	}
}

func TestSettingsPage_MainPaneFitsWithinWidthAtNarrowSizes(t *testing.T) {
	t.Parallel()

	for _, width := range []int{20, 24, 30, 40} {
		t.Run(fmt.Sprintf("width=%d", width), func(t *testing.T) {
			page := newTestSettingsPage(&config.Config{})
			page.sectionCursor = findSectionIndex(t, page, "provider.github")
			page.fieldCursor = findFieldIndex(t, page, "provider.github", "token_ref")
			page.focus = settingsFocusFields
			page.SetSize(width, 18)

			_, mainWidth, bodyHeight, _ := page.layoutMetrics()
			rendered := page.renderMainPane(mainWidth, bodyHeight)
			for i, line := range strings.Split(rendered, "\n") {
				if got := ansi.StringWidth(line); got > mainWidth {
					t.Fatalf("line %d width = %d, want <= %d", i+1, got, mainWidth)
				}
			}
		})
	}
}

func TestSettingsPage_MainScrollbarVisibleWithoutOverflow(t *testing.T) {
	t.Parallel()
	page := newTestSettingsPage(&config.Config{})
	vp := viewport.New(20, 4)
	vp.SetContent("one\ntwo")

	rendered := page.renderMainScrollbar(vp, 4)
	if !strings.Contains(rendered, "▐") {
		t.Fatal("expected scrollbar thumb to remain visible even when content fits")
	}
}

func TestSettingsPage_LayoutMetricsFitAvailableWidth(t *testing.T) {
	t.Parallel()

	for _, width := range []int{3, 5, 12, 20, 30, 40} {
		t.Run(fmt.Sprintf("width=%d", width), func(t *testing.T) {
			page := newTestSettingsPage(&config.Config{})
			page.SetSize(width, 18)

			leftWidth, mainWidth, _, _ := page.layoutMetrics()
			if got := leftWidth + mainWidth + page.layoutSpacerWidth(); got > width {
				t.Fatalf("layout width = %d, want <= %d", got, width)
			}
		})
	}
}

func TestSettingsPage_ViewFitsAvailableWidthAtNarrowSizes(t *testing.T) {
	t.Parallel()

	for _, width := range []int{20, 24, 30} {
		t.Run(fmt.Sprintf("width=%d", width), func(t *testing.T) {
			page := newTestSettingsPage(&config.Config{})
			page.SetSize(width, 18)

			view := page.View()
			for i, line := range strings.Split(view, "\n") {
				if got := ansi.StringWidth(line); got > width {
					t.Fatalf("line %d width = %d, want <= %d", i+1, got, width)
				}
			}
		})
	}
}

func TestSettingsPage_StickyDetailsOrderDescriptionBeforeDefaultBeforeCurrent(t *testing.T) {
	t.Parallel()

	page := newTestSettingsPage(&config.Config{})
	page.sectionCursor = findSectionIndex(t, page, "provider.github")
	page.fieldCursor = findFieldIndex(t, page, "provider.github", "token_ref")

	rendered := ansi.Strip(page.renderStickyFieldDetails(60, 10))
	descriptionIndex := strings.Index(rendered, "GitHub token stored in config or the OS keychain")
	defaultIndex := strings.Index(rendered, "Default: empty")
	currentIndex := strings.Index(rendered, "Current: <empty>")
	if descriptionIndex == -1 || defaultIndex == -1 || currentIndex == -1 {
		t.Fatalf("expected description, default, and current lines in sticky details, got %q", rendered)
	}
	if !(descriptionIndex < defaultIndex && defaultIndex < currentIndex) {
		t.Fatalf("expected description before default before current, got %q", rendered)
	}
}

func TestSettingsPage_StickyDetailsUseBoxBorder(t *testing.T) {
	t.Parallel()
	page := newTestSettingsPage(&config.Config{})
	page.sectionCursor = findSectionIndex(t, page, "provider.github")
	page.fieldCursor = findFieldIndex(t, page, "provider.github", "token_ref")

	rendered := page.renderStickyFieldDetails(50, 8)
	if !strings.Contains(rendered, "╭") || !strings.Contains(rendered, "╯") {
		t.Fatal("expected sticky details to render with a visible rounded box border")
	}
}

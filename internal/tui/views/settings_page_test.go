package views

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/tui/styles"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func newTestSettingsPageWithSnapshot(snapshot SettingsSnapshot) SettingsPage {
	// Build a minimal fake SettingsService that returns the given snapshot.
	testSvc := &testSettingsService{snapshot: snapshot}
	page := NewSettingsPage(testSvc, styles.NewStyles(styles.DefaultTheme))
	page.SetSize(120, 40)

	return page
}

// testSettingsService is a fake SettingsService for settings page tests.
// It tracks provider status set via TestProvider so that RefreshFromService
// returns the current status rather than the construction-time snapshot.
type testSettingsService struct {
	snapshot       SettingsSnapshot
	providerStatus map[string]ProviderStatus
}

func (t *testSettingsService) Snapshot() SettingsSnapshot {
	// Defensive copy mirrors the production settingsService behavior.
	// Callers must not be able to mutate the service's cached state.
	sections := make([]SettingsSection, len(t.snapshot.Sections))
	for i := range t.snapshot.Sections {
		sections[i] = t.snapshot.Sections[i]
		fields := make([]SettingsField, len(t.snapshot.Sections[i].Fields))
		copy(fields, t.snapshot.Sections[i].Fields)
		sections[i].Fields = fields
	}
	providers := make(map[string]ProviderStatus, len(t.snapshot.Providers))
	for k, v := range t.snapshot.Providers {
		providers[k] = v
	}
	return SettingsSnapshot{
		Sections:         sections,
		Providers:        providers,
		RawYAML:          t.snapshot.RawYAML,
		HarnessWarning:   t.snapshot.HarnessWarning,
		DiagnosticsState: t.snapshot.DiagnosticsState,
	}
}

func (t *testSettingsService) RefreshConfigOnly(_ context.Context, _ *config.Config) error {
	return nil
}

func (t *testSettingsService) RefreshWithDiagnostics(_ context.Context, _ *config.Config) error {
	return nil
}

func (t *testSettingsService) Save(_ context.Context, _ []SettingsSection, _ Services) (SettingsApplyResult, error) {
	return SettingsApplyResult{}, errors.New("Save not implemented in test")
}

func (t *testSettingsService) TestProvider(_ context.Context, provider string, _ []SettingsSection) (ProviderStatus, error) {
	if t.providerStatus == nil {
		t.providerStatus = make(map[string]ProviderStatus)
	}
	status := buildProviderStatuses(&config.Config{})[provider]
	t.providerStatus[provider] = status
	return status, nil
}

func (t *testSettingsService) LoginProvider(_ context.Context, _, _ string, _ []SettingsSection, _ Services) (SettingsLoginResult, error) {
	return SettingsLoginResult{}, errors.New("LoginProvider not implemented in test")
}

func (t *testSettingsService) RefreshLoginSnapshot(_ context.Context, _ []SettingsSection) error {
	return nil
}

func (t *testSettingsService) RefreshLoginSnapshotFromConfig(_ context.Context, _ *config.Config) error {
	return nil
}

func (t *testSettingsService) SetDiagnosticsState(state SettingsDiagnosticsState) {
	t.snapshot.DiagnosticsState = state
}

func newTestSettingsPage(cfg *config.Config) SettingsPage {
	return newTestSettingsPageWithSnapshot(SettingsSnapshot{Sections: buildSettingsSections(cfg), Providers: buildProviderStatuses(cfg)})
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

func findFirstSectionWithFields(t *testing.T, page SettingsPage) int {
	t.Helper()
	for i, section := range page.sections {
		if len(section.Fields) > 0 {
			return i
		}
	}
	t.Fatal("no section with fields found")

	return -1
}

func findLastSectionWithFields(t *testing.T, page SettingsPage) int {
	t.Helper()
	for i := len(page.sections) - 1; i >= 0; i-- {
		if len(page.sections[i].Fields) > 0 {
			return i
		}
	}
	t.Fatal("no section with fields found")

	return -1
}

func findFirstVisibleSidebarSection(t *testing.T, page SettingsPage) int {
	t.Helper()
	for _, node := range page.visibleNavNodes() {
		if node.sectionIndex >= 0 {
			return node.sectionIndex
		}
	}
	t.Fatal("no visible sidebar section found")

	return -1
}

func findLastVisibleSidebarSection(t *testing.T, page SettingsPage) int {
	t.Helper()
	nodes := page.visibleNavNodes()
	for i := len(nodes) - 1; i >= 0; i-- {
		if nodes[i].sectionIndex >= 0 {
			return nodes[i].sectionIndex
		}
	}
	t.Fatal("no visible sidebar section found")

	return -1
}

func assertSettingsPageFitsWindow(t *testing.T, rendered string, width, height int) []string {
	t.Helper()
	lines := strings.Split(rendered, "\n")
	if len(lines) > height {
		t.Fatalf("line count = %d, want <= %d\nview:\n%s", len(lines), height, rendered)
	}
	for i, line := range lines {
		if got := ansi.StringWidth(line); got > width {
			t.Fatalf("line %d width = %d, want <= %d\nview:\n%s", i+1, got, width, rendered)
		}
	}

	return lines
}

func assertSelectedFieldVisibleInViewport(t *testing.T, page SettingsPage) {
	t.Helper()
	viewportWidth, viewportHeight, _ := page.mainViewportSize()
	_, _, fieldAnchors := page.buildMainDocument(viewportWidth)
	anchor, ok := fieldAnchors[settingsFieldAnchorKey(page.sectionCursor, page.fieldCursor)]
	if !ok {
		t.Fatalf("missing field anchor for section=%d field=%d", page.sectionCursor, page.fieldCursor)
	}
	vp := page.configuredMainViewport(viewportWidth, viewportHeight)
	top := vp.YOffset
	bottom := top + vp.Height - 1
	if anchor < top || anchor > bottom {
		t.Fatalf("selected field anchor = %d, want between %d and %d", anchor, top, bottom)
	}
}

func assertSelectedSectionVisibleInViewport(t *testing.T, page SettingsPage) {
	t.Helper()
	viewportWidth, viewportHeight, _ := page.mainViewportSize()
	_, sectionAnchors, _ := page.buildMainDocument(viewportWidth)
	anchor, ok := sectionAnchors[page.sectionCursor]
	if !ok {
		t.Fatalf("missing section anchor for section=%d", page.sectionCursor)
	}
	vp := page.configuredMainViewport(viewportWidth, viewportHeight)
	top := vp.YOffset
	bottom := top + vp.Height - 1
	if anchor < top || anchor > bottom {
		t.Fatalf("selected section anchor = %d, want between %d and %d", anchor, top, bottom)
	}
}

func scrollbarThumbTop(rendered string) int {
	for i, line := range strings.Split(rendered, "\n") {
		if strings.Contains(line, "▐") {
			return i
		}
	}

	return -1
}

func TestSettingsPage_TextEditModalShowsTypedInput(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{}
	cfg.Adapters.Codex.Model = "gpt"
	page := newTestSettingsPage(cfg)
	sectionIndex := findSectionIndex(t, page, "harness.codex")
	fieldIndex := findFieldIndex(t, page, "harness.codex", "model")
	page.sectionCursor = sectionIndex
	page.fieldCursor = fieldIndex
	page.focus = settingsFocusFields

	updated, cmd := page.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}}, Services{})
	if cmd != nil {
		t.Fatalf("unexpected command opening text editor with e: %v", cmd)
	}
	if !updated.editing || updated.editMode != settingsEditModeText {
		t.Fatalf("editing state = (%v, %v), want text modal", updated.editing, updated.editMode)
	}
	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}}, Services{})
	rendered := ansi.Strip(updated.View())
	if !strings.Contains(rendered, "Settings") {
		t.Fatalf("view = %q, want underlying settings page to remain visible", rendered)
	}
	if !strings.Contains(rendered, "gptx") {
		t.Fatalf("view = %q, want visible typed input", rendered)
	}
	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEnter}, Services{})
	if got := updated.sections[sectionIndex].Fields[fieldIndex].Value; got != "gptx" {
		t.Fatalf("field value = %q, want gptx", got)
	}
}

func TestSettingsPage_EnumFieldUsesSelectionModal(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{}
	cfg.Harness.Default = config.HarnessOhMyPi
	page := newTestSettingsPage(cfg)
	sectionIndex := findSectionIndex(t, page, "harness")
	fieldIndex := findFieldIndex(t, page, "harness", "default")
	page.sectionCursor = sectionIndex
	page.fieldCursor = fieldIndex
	page.focus = settingsFocusFields

	updated, cmd := page.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}}, Services{})
	if cmd != nil {
		t.Fatalf("unexpected command opening selection modal with e: %v", cmd)
	}
	if !updated.editing || updated.editMode != settingsEditModeSelect {
		t.Fatalf("editing state = (%v, %v), want selection modal", updated.editing, updated.editMode)
	}
	rendered := ansi.Strip(updated.View())
	if !strings.Contains(rendered, "Settings") {
		t.Fatalf("view = %q, want underlying settings page to remain visible", rendered)
	}
	for _, want := range []string{"Oh My Pi", "Claude Code", "Codex", "OpenCode"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("view = %q, want %q in selection modal", rendered, want)
		}
	}
	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown}, Services{})
	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEnter}, Services{})
	if got := updated.sections[sectionIndex].Fields[fieldIndex].Value; got != string(config.HarnessClaudeCode) {
		t.Fatalf("field value = %q, want %q", got, config.HarnessClaudeCode)
	}
}

func TestSettingsPage_SelectOpenCodeHarness(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{}
	cfg.Harness.Default = config.HarnessOhMyPi
	page := newTestSettingsPage(cfg)
	sectionIndex := findSectionIndex(t, page, "harness")
	fieldIndex := findFieldIndex(t, page, "harness", "default")
	page.sectionCursor = sectionIndex
	page.fieldCursor = fieldIndex
	page.focus = settingsFocusFields

	// Open selection modal.
	updated, _ := page.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}}, Services{})
	if !updated.editing || updated.editMode != settingsEditModeSelect {
		t.Fatalf("editing state = (%v, %v), want selection modal", updated.editing, updated.editMode)
	}

	// Navigate to OpenCode: Oh My Pi -> Claude Code -> Codex -> OpenCode (3 downs).
	for range 3 {
		updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown}, Services{})
	}
	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEnter}, Services{})

	if got := updated.sections[sectionIndex].Fields[fieldIndex].Value; got != string(config.HarnessOpenCode) {
		t.Fatalf("field value = %q, want %q", got, config.HarnessOpenCode)
	}
}

func TestSettingsPage_EditModalFitsNarrowWindow(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{}
	cfg.Adapters.Codex.Model = "gpt-5"
	page := newTestSettingsPage(cfg)
	page.SetSize(36, 12)
	page.sectionCursor = findSectionIndex(t, page, "harness.codex")
	page.fieldCursor = findFieldIndex(t, page, "harness.codex", "model")
	page.focus = settingsFocusFields

	updated, _ := page.Update(tea.KeyMsg{Type: tea.KeyEnter}, Services{})
	assertSettingsPageFitsWindow(t, updated.View(), 36, 12)
}

func TestSettingsPage_SelectModalFitsNarrowWindow(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{}
	cfg.Harness.Default = config.HarnessOhMyPi
	page := newTestSettingsPage(cfg)
	page.SetSize(36, 12)
	page.sectionCursor = findSectionIndex(t, page, "harness")
	page.fieldCursor = findFieldIndex(t, page, "harness", "default")
	page.focus = settingsFocusFields

	updated, _ := page.Update(tea.KeyMsg{Type: tea.KeyEnter}, Services{})
	assertSettingsPageFitsWindow(t, updated.View(), 36, 12)
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

func TestSettingsPage_ViewShowsHarnessWarningAndSectionError(t *testing.T) {
	t.Parallel()

	snapshot := SettingsSnapshot{
		Sections:       buildSettingsSections(&config.Config{}),
		Providers:      buildProviderStatuses(&config.Config{}),
		HarnessWarning: "Planning unavailable. Check Harness Routing.",
	}
	for i := range snapshot.Sections {
		if snapshot.Sections[i].ID == "harness" {
			snapshot.Sections[i].Status = "warning"
			snapshot.Sections[i].Error = `Planning: Codex not found.`

			break
		}
	}

	page := newTestSettingsPageWithSnapshot(snapshot)
	page.sectionCursor = findSectionIndex(t, page, "harness")
	page.navCursor = page.sections[page.sectionCursor].ID
	page.syncMainViewport()
	rendered := ansi.Strip(page.View())
	if !strings.Contains(rendered, "warning: Planning unavailable. Check Harness Routing.") {
		t.Fatalf("view = %q, want footer warning", rendered)
	}
	doc, _, _ := page.buildMainDocument(80)
	if !strings.Contains(ansi.Strip(doc), `Planning: Codex not found.`) {
		t.Fatalf("document = %q, want section error detail", ansi.Strip(doc))
	}
}

func TestSettingsPage_ViewWithHarnessWarningFitsNarrowWidth(t *testing.T) {
	t.Parallel()

	snapshot := SettingsSnapshot{
		Sections:       buildSettingsSections(&config.Config{}),
		Providers:      buildProviderStatuses(&config.Config{}),
		HarnessWarning: "Harnesses unavailable. Check Harness Routing.",
	}
	for i := range snapshot.Sections {
		if snapshot.Sections[i].ID == "harness" {
			snapshot.Sections[i].Status = "warning"
			snapshot.Sections[i].Error = `Planning: Codex not found.`

			break
		}
	}

	page := newTestSettingsPageWithSnapshot(snapshot)
	page.SetSize(36, 18)
	for i, line := range strings.Split(page.View(), "\n") {
		if got := ansi.StringWidth(line); got > 36 {
			t.Fatalf("line %d width = %d, want <= %d", i+1, got, 36)
		}
	}
}

func TestSettingsPage_RenderProviderStatusLineColorsConnectedGreen(t *testing.T) {
	t.Parallel()

	page := newTestSettingsPage(&config.Config{})
	status := page.providerStatus["github"]
	status.AuthSource = "gh"
	status.Configured = true
	status.Connected = true

	rendered := page.renderProviderStatusLine("  ", status, 80)
	if !strings.Contains(rendered, "Provider auth: gh") {
		t.Fatalf("expected provider auth label in rendered status line, got %q", rendered)
	}
	if !strings.Contains(rendered, page.styles.Success.Render("connected")) {
		t.Fatalf("expected connected provider status to be rendered in green, got %q", rendered)
	}
}

func TestSettingsPage_SentrySectionRendersProviderStatusAndDetails(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}
	cfg.Adapters.Sentry.Token = "sentry-secret"
	cfg.Adapters.Sentry.Organization = "acme"
	cfg.Adapters.Sentry.Projects = []string{"web", "api"}

	page := newTestSettingsPage(cfg)
	page.sectionCursor = findSectionIndex(t, page, "provider.sentry")
	page.fieldCursor = findFieldIndex(t, page, "provider.sentry", "token_ref")

	header := page.renderStickySectionHeader(32)
	if !strings.Contains(header, "Sentry") {
		t.Fatalf("header = %q, want selected Sentry title", header)
	}
	if !strings.Contains(header, "Providers") {
		t.Fatalf("header = %q, want Providers breadcrumb", header)
	}

	details := ansi.Strip(page.renderStickyFieldDetails(70, 10))
	if !strings.Contains(details, "Sentry token stored in config or the OS keychain") || !strings.Contains(details, "Status: config token") {
		t.Fatalf("details = %q, want Sentry token description", details)
	}
	if !strings.Contains(details, "Default: empty") {
		t.Fatalf("details = %q, want default value line", details)
	}

	doc, _, _ := page.buildMainDocument(80)
	rendered := ansi.Strip(doc)
	if !strings.Contains(rendered, "Provider auth: config token") {
		t.Fatalf("document = %q, want Sentry provider status", rendered)
	}
	for _, want := range []string{"Organization", "Projects"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("document = %q, want %q in Sentry section", rendered, want)
		}
	}
}

func TestSettingsPage_SentryShowsLoginAction(t *testing.T) {
	t.Parallel()

	page := newTestSettingsPage(&config.Config{})
	page.sectionCursor = findSectionIndex(t, page, "provider.sentry")
	if !strings.Contains(page.footerText(), "[g] login") {
		t.Fatalf("footer = %q, want Sentry login hint", page.footerText())
	}
	page.focus = settingsFocusFields
	if !strings.Contains(page.footerText(), "[g] login") {
		t.Fatalf("field footer = %q, want Sentry login hint", page.footerText())
	}
	if cmd := page.loginProviderCmd(Services{}); cmd == nil {
		t.Fatal("expected Sentry login command to remain available")
	}

	githubPage := newTestSettingsPage(&config.Config{})
	githubPage.sectionCursor = findSectionIndex(t, githubPage, "provider.github")
	if !strings.Contains(githubPage.footerText(), "[g] login") {
		t.Fatalf("footer = %q, want GitHub login hint", githubPage.footerText())
	}
	if cmd := githubPage.loginProviderCmd(Services{}); cmd == nil {
		t.Fatal("expected GitHub login command to remain available")
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

func TestSettingsPage_MouseWheelScrollMovesWithinBounds(t *testing.T) {
	t.Parallel()

	page := newTestSettingsPage(&config.Config{})
	page.sectionCursor = findFirstSectionWithFields(t, page)
	page.fieldCursor = 0
	page.focus = settingsFocusFields
	page.navCursor = page.sections[page.sectionCursor].ID
	page.SetSize(80, 12)
	viewportWidth, viewportHeight, _ := page.mainViewportSize()
	page.mainViewport = page.preparedMainViewport(viewportWidth, viewportHeight, false)
	if page.mainViewport.TotalLineCount() <= page.mainViewport.Height {
		t.Fatal("expected settings content to overflow the viewport")
	}

	updated, _ := page.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown}, Services{})
	maxOffset := max(0, updated.mainViewport.TotalLineCount()-updated.mainViewport.Height)
	if updated.mainViewport.YOffset > maxOffset {
		t.Fatalf("y offset = %d, want <= %d", updated.mainViewport.YOffset, maxOffset)
	}
	assertSelectedFieldVisibleInViewport(t, updated)
}

func TestSettingsPage_MouseWheelUpRecoversImmediatelyFromOverscroll(t *testing.T) {
	t.Parallel()

	page := newTestSettingsPage(&config.Config{})
	page.sectionCursor = findFirstSectionWithFields(t, page)
	page.fieldCursor = 0
	page.focus = settingsFocusFields
	page.navCursor = page.sections[page.sectionCursor].ID
	page.SetSize(80, 12)
	viewportWidth, viewportHeight, _ := page.mainViewportSize()
	page.mainViewport = page.preparedMainViewport(viewportWidth, viewportHeight, false)
	maxOffset := max(0, page.mainViewport.TotalLineCount()-page.mainViewport.Height)
	if maxOffset == 0 {
		t.Fatal("expected settings content to overflow the viewport")
	}
	page.mainViewport.YOffset = maxOffset + 20

	updated, _ := page.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelUp}, Services{})
	if updated.mainViewport.YOffset > maxOffset {
		t.Fatalf("y offset = %d, want <= %d after recovering from overscroll", updated.mainViewport.YOffset, maxOffset)
	}
	assertSelectedFieldVisibleInViewport(t, updated)
}

func TestSettingsPage_MouseWheelAdvancesFocusedFieldSelection(t *testing.T) {
	t.Parallel()

	page := newTestSettingsPage(&config.Config{})
	page.sectionCursor = findSectionIndex(t, page, "harness")
	page.fieldCursor = findFieldIndex(t, page, "harness", "default")
	page.focus = settingsFocusFields
	page.SetSize(80, 12)

	viewportWidth, viewportHeight, _ := page.mainViewportSize()
	page.mainViewport = page.preparedMainViewport(viewportWidth, viewportHeight, false)
	maxOffset := max(0, page.mainViewport.TotalLineCount()-page.mainViewport.Height)
	if maxOffset == 0 {
		t.Fatal("expected settings content to overflow the viewport")
	}
	page.mainViewport.YOffset = maxOffset
	originalSection := page.sectionCursor
	originalField := page.fieldCursor

	updated, _ := page.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelUp}, Services{})
	if updated.sectionCursor == originalSection && updated.fieldCursor == originalField {
		t.Fatalf("selection stayed at section=%d field=%d, want the focused field to advance with wheel scrolling", updated.sectionCursor, updated.fieldCursor)
	}
	assertSelectedFieldVisibleInViewport(t, updated)
}

func TestSettingsPage_MouseWheelSectionFocusAdvancesSectionAndChangesMainPane(t *testing.T) {
	t.Parallel()

	page := newTestSettingsPage(&config.Config{})
	page.sectionCursor = findSectionIndex(t, page, "provider.github")
	page.navCursor = page.sections[page.sectionCursor].ID
	page.focus = settingsFocusSections
	page.SetSize(80, 12)
	page.syncMainViewport()
	_, beforeMainWidth, beforeBodyHeight, _ := page.layoutMetrics()
	beforeMain := ansi.Strip(page.renderMainPane(beforeMainWidth, beforeBodyHeight))
	originalSection := page.sectionCursor

	updated, _ := page.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown}, Services{})
	if updated.sectionCursor == originalSection {
		t.Fatalf("section cursor stayed at %d, want the focused section to advance with wheel scrolling", updated.sectionCursor)
	}
	_, afterMainWidth, afterBodyHeight, _ := updated.layoutMetrics()
	afterMain := ansi.Strip(updated.renderMainPane(afterMainWidth, afterBodyHeight))
	if beforeMain == afterMain {
		t.Fatal("main pane render did not change after section-focused wheel navigation")
	}
	assertSelectedSectionVisibleInViewport(t, updated)
}

func TestSettingsPage_MouseWheelSectionFocusKeepsFieldCursorAtSectionStart(t *testing.T) {
	t.Parallel()

	page := newTestSettingsPage(&config.Config{})
	page.sectionCursor = findSectionIndex(t, page, "commit")
	page.fieldCursor = findFieldIndex(t, page, "commit", "message_template")
	page.navCursor = page.sections[page.sectionCursor].ID
	page.focus = settingsFocusSections
	page.SetSize(80, 12)
	page.syncMainViewport()
	_, beforeMainWidth, beforeBodyHeight, _ := page.layoutMetrics()
	beforeMain := ansi.Strip(page.renderMainPane(beforeMainWidth, beforeBodyHeight))

	updated, _ := page.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown}, Services{})
	if updated.focus != settingsFocusSections {
		t.Fatalf("focus = %v, want %v after sidebar wheel scroll", updated.focus, settingsFocusSections)
	}
	if updated.fieldCursor != 0 {
		t.Fatalf("field cursor = %d, want 0 after scrolling while sections are focused", updated.fieldCursor)
	}
	_, afterMainWidth, afterBodyHeight, _ := updated.layoutMetrics()
	afterMain := ansi.Strip(updated.renderMainPane(afterMainWidth, afterBodyHeight))
	if beforeMain == afterMain {
		t.Fatal("main pane render did not change after section-focused wheel navigation")
	}
	assertSelectedSectionVisibleInViewport(t, updated)
}

func TestSettingsPage_KeySectionNavigationRefreshesPersistedMainViewport(t *testing.T) {
	t.Parallel()

	page := newTestSettingsPage(&config.Config{})
	page.sectionCursor = findSectionIndex(t, page, "provider.github")
	page.navCursor = page.sections[page.sectionCursor].ID
	page.focus = settingsFocusSections
	page.SetSize(80, 12)
	page.syncMainViewport()

	beforeViewport := page.mainViewport.View()
	originalSection := page.sectionCursor

	updated, _ := page.Update(tea.KeyMsg{Type: tea.KeyDown}, Services{})
	if updated.sectionCursor == originalSection {
		t.Fatalf("section cursor stayed at %d, want the focused section to advance with key navigation", updated.sectionCursor)
	}
	afterViewport := updated.mainViewport.View()
	if beforeViewport == afterViewport {
		t.Fatal("persisted main viewport did not change after section-focused key navigation")
	}
	assertSelectedSectionVisibleInViewport(t, updated)
}

func TestSettingsPage_KeyFieldNavigationRefreshesPersistedMainViewport(t *testing.T) {
	t.Parallel()

	page := newTestSettingsPage(&config.Config{})
	page.sectionCursor = findSectionIndex(t, page, "commit")
	page.fieldCursor = 0
	page.focus = settingsFocusFields
	page.navCursor = page.sections[page.sectionCursor].ID
	page.SetSize(80, 12)
	page.syncMainViewport()

	beforeViewport := page.mainViewport.View()
	originalSection := page.sectionCursor
	originalField := page.fieldCursor

	updated, _ := page.Update(tea.KeyMsg{Type: tea.KeyDown}, Services{})
	if updated.sectionCursor == originalSection && updated.fieldCursor == originalField {
		t.Fatalf("selection stayed at section=%d field=%d, want key navigation to advance the focused field", updated.sectionCursor, updated.fieldCursor)
	}
	afterViewport := updated.mainViewport.View()
	if beforeViewport == afterViewport {
		t.Fatal("persisted main viewport did not change after field-focused key navigation")
	}
	assertSelectedFieldVisibleInViewport(t, updated)
}

func TestSettingsPage_MouseWheelDownMovesViewportImmediatelyFromTopBoundaryForFields(t *testing.T) {
	t.Parallel()

	page := newTestSettingsPage(&config.Config{})
	page.sectionCursor = findFirstSectionWithFields(t, page)
	page.fieldCursor = 0
	page.focus = settingsFocusFields
	page.navCursor = page.sections[page.sectionCursor].ID
	page.SetSize(80, 12)
	viewportWidth, viewportHeight, _ := page.mainViewportSize()
	page.mainViewport = page.preparedMainViewport(viewportWidth, viewportHeight, false)
	maxOffset := max(0, page.mainViewport.TotalLineCount()-page.mainViewport.Height)
	if maxOffset == 0 {
		t.Fatal("expected settings content to overflow the viewport")
	}
	page.mainViewport.YOffset = 0
	overshot := page
	for range 5 {
		next, _ := overshot.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelUp}, Services{})
		overshot = next
	}
	if overshot.mainViewport.YOffset != 0 {
		t.Fatalf("y offset after top overshoot = %d, want 0", overshot.mainViewport.YOffset)
	}
	if overshot.sectionCursor != page.sectionCursor || overshot.fieldCursor != page.fieldCursor {
		t.Fatalf("selection changed during top overshoot: got section=%d field=%d, want section=%d field=%d", overshot.sectionCursor, overshot.fieldCursor, page.sectionCursor, page.fieldCursor)
	}

	updated, _ := overshot.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown}, Services{})
	if updated.mainViewport.YOffset <= overshot.mainViewport.YOffset {
		t.Fatalf("y offset = %d, want > %d after reversing from top overshoot", updated.mainViewport.YOffset, overshot.mainViewport.YOffset)
	}
	assertSelectedFieldVisibleInViewport(t, updated)
}

func TestSettingsPage_MouseWheelUpMovesViewportImmediatelyFromBottomBoundaryForFields(t *testing.T) {
	t.Parallel()

	page := newTestSettingsPage(&config.Config{})
	page.sectionCursor = findLastSectionWithFields(t, page)
	page.fieldCursor = len(page.sections[page.sectionCursor].Fields) - 1
	page.focus = settingsFocusFields
	page.navCursor = page.sections[page.sectionCursor].ID
	page.SetSize(80, 12)
	page.syncMainViewport()
	maxOffset := max(0, page.mainViewport.TotalLineCount()-page.mainViewport.Height)
	if maxOffset == 0 {
		t.Fatal("expected settings content to overflow the viewport")
	}
	page.mainViewport.YOffset = maxOffset
	overshot := page
	for range 5 {
		next, _ := overshot.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown}, Services{})
		overshot = next
	}
	if overshot.mainViewport.YOffset != maxOffset {
		t.Fatalf("y offset after bottom overshoot = %d, want %d", overshot.mainViewport.YOffset, maxOffset)
	}
	if overshot.sectionCursor != page.sectionCursor || overshot.fieldCursor != page.fieldCursor {
		t.Fatalf("selection changed during bottom overshoot: got section=%d field=%d, want section=%d field=%d", overshot.sectionCursor, overshot.fieldCursor, page.sectionCursor, page.fieldCursor)
	}

	updated, _ := overshot.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelUp}, Services{})
	if updated.mainViewport.YOffset >= maxOffset {
		t.Fatalf("y offset = %d, want < %d after reversing from bottom overshoot", updated.mainViewport.YOffset, maxOffset)
	}
	assertSelectedFieldVisibleInViewport(t, updated)
}

func TestSettingsPage_MouseWheelScrollPastEndDoesNotJumpToTop(t *testing.T) {
	t.Parallel()

	page := newTestSettingsPage(&config.Config{})
	page.focus = settingsFocusFields
	page.sectionCursor = 0
	page.fieldCursor = 0
	page.navCursor = page.sections[0].ID
	page.SetSize(80, 12)
	page.syncMainViewport()

	// Scroll all the way down in many steps.
	current := page
	for range 200 {
		next, _ := current.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown}, Services{})
		current = next
	}

	// Record the bottom state.
	bottomOffset := current.mainViewport.YOffset
	bottomSection := current.sectionCursor
	bottomField := current.fieldCursor
	maxOffset := max(0, current.mainViewport.TotalLineCount()-current.mainViewport.Height)
	if bottomOffset < maxOffset-3 {
		t.Fatalf("expected to reach near bottom: offset=%d, max=%d", bottomOffset, maxOffset)
	}

	// One more scroll down should not jump to top.
	after, _ := current.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown}, Services{})
	if after.mainViewport.YOffset < bottomOffset {
		t.Fatalf("scroll past end jumped: offset went from %d to %d", bottomOffset, after.mainViewport.YOffset)
	}
	if after.sectionCursor != bottomSection || after.fieldCursor != bottomField {
		t.Fatalf("selection changed on scroll past end: section %d→%d, field %d→%d",
			bottomSection, after.sectionCursor, bottomField, after.fieldCursor)
	}
}

func TestSettingsPage_KeyboardScrollPastEndDoesNotJumpToTop(t *testing.T) {
	t.Parallel()

	page := newTestSettingsPage(&config.Config{})
	page.focus = settingsFocusFields
	page.sectionCursor = 0
	page.fieldCursor = 0
	page.navCursor = page.sections[0].ID
	page.SetSize(80, 12)
	page.syncMainViewport()

	// Move down field by field to the bottom.
	current := page
	for range 200 {
		next, _ := current.Update(tea.KeyMsg{Type: tea.KeyDown}, Services{})
		if next.sectionCursor == current.sectionCursor && next.fieldCursor == current.fieldCursor {
			current = next

			break // Clamped at end.
		}
		current = next
	}

	// Record the bottom state.
	bottomOffset := current.mainViewport.YOffset
	bottomSection := current.sectionCursor
	bottomField := current.fieldCursor

	// One more down should not jump to top.
	after, _ := current.Update(tea.KeyMsg{Type: tea.KeyDown}, Services{})
	if after.mainViewport.YOffset == 0 && bottomOffset > 0 {
		t.Fatalf("keyboard scroll past end jumped to top: offset went from %d to 0", bottomOffset)
	}
	if after.sectionCursor < bottomSection {
		t.Fatalf("section went backwards on scroll past end: %d\u2192%d", bottomSection, after.sectionCursor)
	}
	_ = bottomField
}

func TestSettingsPage_MouseWheelUpAtFirstSectionKeepsSectionFocusStable(t *testing.T) {
	t.Parallel()

	page := newTestSettingsPage(&config.Config{})
	page.sectionCursor = findFirstVisibleSidebarSection(t, page)
	page.fieldCursor = 0
	page.focus = settingsFocusSections
	page.navCursor = page.sections[page.sectionCursor].ID
	page.SetSize(80, 12)
	page.syncMainViewport()
	beforeOffset := page.mainViewport.YOffset
	beforeSection := page.sectionCursor
	beforeNav := page.navCursor

	updated, _ := page.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelUp}, Services{})
	if updated.mainViewport.YOffset != beforeOffset {
		t.Fatalf("y offset = %d, want %d at the first section boundary", updated.mainViewport.YOffset, beforeOffset)
	}
	if updated.sectionCursor != beforeSection {
		t.Fatalf("section cursor = %d, want %d at the first section boundary", updated.sectionCursor, beforeSection)
	}
	if updated.navCursor != beforeNav {
		t.Fatalf("nav cursor = %q, want %q at the first section boundary", updated.navCursor, beforeNav)
	}
	if updated.fieldCursor != 0 {
		t.Fatalf("field cursor = %d, want 0 while sections stay focused", updated.fieldCursor)
	}
}

func TestSettingsPage_MouseWheelDownAtLastSectionKeepsSectionFocusStable(t *testing.T) {
	t.Parallel()

	page := newTestSettingsPage(&config.Config{})
	page.sectionCursor = findLastVisibleSidebarSection(t, page)
	page.fieldCursor = 0
	page.focus = settingsFocusSections
	page.navCursor = page.sections[page.sectionCursor].ID
	page.SetSize(80, 12)
	page.syncMainViewport()
	beforeOffset := page.mainViewport.YOffset
	beforeSection := page.sectionCursor
	beforeNav := page.navCursor

	updated, _ := page.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown}, Services{})
	if updated.mainViewport.YOffset != beforeOffset {
		t.Fatalf("y offset = %d, want %d at the last section boundary", updated.mainViewport.YOffset, beforeOffset)
	}
	if updated.sectionCursor != beforeSection {
		t.Fatalf("section cursor = %d, want %d at the last section boundary", updated.sectionCursor, beforeSection)
	}
	if updated.navCursor != beforeNav {
		t.Fatalf("nav cursor = %q, want %q at the last section boundary", updated.navCursor, beforeNav)
	}
	if updated.fieldCursor != 0 {
		t.Fatalf("field cursor = %d, want 0 while sections stay focused", updated.fieldCursor)
	}
}

func TestSettingsPage_ScrollbarTracksSelectedFieldMovement(t *testing.T) {
	t.Parallel()

	page := newTestSettingsPage(&config.Config{})
	page.sectionCursor = findSectionIndex(t, page, "commit")
	page.fieldCursor = 0
	page.focus = settingsFocusFields
	page.SetSize(80, 12)
	page.syncMainViewport()
	viewportWidth, viewportHeight, _ := page.mainViewportSize()
	initialViewport := page.configuredMainViewport(viewportWidth, viewportHeight)
	initialScrollbar := page.renderMainScrollbar(initialViewport, initialViewport.Height)
	initialThumbTop := scrollbarThumbTop(initialScrollbar)
	if initialThumbTop == -1 {
		t.Fatal("expected initial scrollbar thumb")
	}

	updated := page
	for range 40 {
		next, _ := updated.Update(tea.KeyMsg{Type: tea.KeyDown}, Services{})
		updated = next
	}
	updatedViewport := updated.configuredMainViewport(viewportWidth, viewportHeight)
	if updatedViewport.YOffset <= 0 {
		t.Fatalf("y offset = %d, want > 0 after moving selection", updatedViewport.YOffset)
	}
	updatedScrollbar := updated.renderMainScrollbar(updatedViewport, updatedViewport.Height)
	updatedThumbTop := scrollbarThumbTop(updatedScrollbar)
	if updatedThumbTop == -1 {
		t.Fatal("expected updated scrollbar thumb")
	}
	if updatedScrollbar == initialScrollbar {
		t.Fatalf("scrollbar did not change after moving selection\ninitial:\n%s\nupdated:\n%s", initialScrollbar, updatedScrollbar)
	}
	assertSelectedFieldVisibleInViewport(t, updated)
}

func TestSettingsPage_KeyNavigationKeepsSelectedSectionVisible(t *testing.T) {
	t.Parallel()

	page := newTestSettingsPage(&config.Config{})
	page.sectionCursor = findSectionIndex(t, page, "commit")
	page.navCursor = page.sections[page.sectionCursor].ID
	page.focus = settingsFocusSections
	page.SetSize(80, 12)
	page.syncMainViewport()

	updated := page
	for range 10 {
		next, _ := updated.Update(tea.KeyMsg{Type: tea.KeyDown}, Services{})
		updated = next
		assertSelectedSectionVisibleInViewport(t, updated)
	}
}

func TestSettingsPage_KeyNavigationKeepsSelectedFieldVisible(t *testing.T) {
	t.Parallel()

	page := newTestSettingsPage(&config.Config{})
	page.sectionCursor = findSectionIndex(t, page, "commit")
	page.fieldCursor = 0
	page.focus = settingsFocusFields
	page.SetSize(80, 12)
	page.syncMainViewport()

	updated := page
	for range 10 {
		next, _ := updated.Update(tea.KeyMsg{Type: tea.KeyDown}, Services{})
		updated = next
		assertSelectedFieldVisibleInViewport(t, updated)
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

func TestSettingsPage_ViewFitsAvailableHeightWithSelectedField(t *testing.T) {
	t.Parallel()

	for _, height := range []int{12, 18, 24} {
		t.Run(fmt.Sprintf("height=%d", height), func(t *testing.T) {
			page := newTestSettingsPage(&config.Config{})
			page.SetSize(80, height)
			page.sectionCursor = findSectionIndex(t, page, "provider.github")
			page.fieldCursor = findFieldIndex(t, page, "provider.github", "token_ref")
			page.focus = settingsFocusFields

			lines := assertSettingsPageFitsWindow(t, page.View(), 80, height)
			if len(lines) == 0 || !strings.Contains(lines[0], "╭") {
				t.Fatalf("top line = %q, want visible settings chrome", strings.Join(lines, "\n"))
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
	if descriptionIndex >= defaultIndex || defaultIndex >= currentIndex {
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

func TestSettingsPage_StickyDetailsWrapsLongDescription(t *testing.T) {
	// The repo_docs.paths field has a long description that previously got hard-truncated
	// at the box inner width. It must now wrap and be fully visible.
	t.Parallel()
	page := newTestSettingsPage(&config.Config{})
	page.sectionCursor = findSectionIndex(t, page, "repo_docs")
	page.fieldCursor = findFieldIndex(t, page, "repo_docs", "paths")

	// Use a realistic narrow width where the description spans multiple lines.
	// Strip ANSI only; check individual tokens that prove the tail of the
	// description is visible and has been word-wrapped (not hard-truncated).
	stripped := ansi.Strip(page.renderStickyFieldDetails(60, 14))
	// Full description ends with "...before planning." - both must be present.
	for _, want := range []string{"implementation repos", "before", "planning."} {
		if !strings.Contains(stripped, want) {
			t.Fatalf("sticky details = %q\nwant %q visible (description must not be truncated)", stripped, want)
		}
	}
	// Description must be wrapped: the first line must NOT contain text from later wrapped lines,
	// which proves it is not emitted as one long untruncated string.
	if strings.Contains(strings.SplitN(stripped, "\n", 3)[1], "documentation repositories") {
		t.Fatal("description is not wrapping: first description line contains text from second wrapped line")
	}
}

func TestSettingsPage_ShowsCheckingHarnessAvailabilityWhilePending(t *testing.T) {
	t.Parallel()

	// Create a fake service that returns a pending diagnostics snapshot.
	fake := &testSettingsService{
		snapshot: SettingsSnapshot{
			DiagnosticsState: SettingsDiagnosticsPending,
			Sections:         buildSettingsSections(&config.Config{}),
			Providers:        buildProviderStatuses(&config.Config{}),
		},
	}
	page := NewSettingsPage(fake, styles.NewStyles(styles.DefaultTheme))
	page.SetSize(120, 40)
	page.Open()

	rendered := ansi.Strip(page.View())
	if !strings.Contains(rendered, "Checking harness availability…") {
		t.Fatalf("view = %q, want footer to contain %q", rendered, "Checking harness availability…")
	}
}

func TestSettingsPage_DoesNotClobberDirtyEditsOnAsyncDiagnostics(t *testing.T) {
	t.Parallel()

	// Create a fake that starts with a clean snapshot.
	fake := &testSettingsService{
		snapshot: SettingsSnapshot{
			DiagnosticsState: SettingsDiagnosticsReady,
			Sections:         buildSettingsSections(&config.Config{}),
			Providers:        buildProviderStatuses(&config.Config{}),
		},
	}
	page := NewSettingsPage(fake, styles.NewStyles(styles.DefaultTheme))
	page.SetSize(120, 40)
	page.Open()

	// Simulate a user editing a field: mark dirty and change a field value.
	if len(page.sections) == 0 {
		t.Fatal("expected at least one section")
	}
	for i := range page.sections {
		if len(page.sections[i].Fields) > 0 {
			page.sections[i].Fields[0].Dirty = true
			page.sections[i].Fields[0].Value = "user-edited-value"
			page.dirty = true
			break
		}
	}

	// Simulate async diagnostics completing: update the service snapshot.
	fake.snapshot.DiagnosticsState = SettingsDiagnosticsReady
	fake.snapshot.HarnessWarning = "Harness unavailable."
	// Also change the sections in the service snapshot so we can verify
	// the page does NOT adopt the service's sections.
	fake.snapshot.Sections[0].Fields[0].Value = "service-value"

	// Refresh from service while dirty.
	page.RefreshFromService()

	// Verify the user's edit is preserved.
	found := false
	for _, sec := range page.sections {
		for _, f := range sec.Fields {
			if f.Value == "user-edited-value" && f.Dirty {
				found = true
				break
			}
		}
		if found {
			break
		}
	}
	if !found {
		t.Fatal("dirty field edit was clobbered by RefreshFromService")
	}
}

// fakeSettingsServiceWithSaveSuccess is a test fake that returns a controlled snapshot
// and succeeds on Save for testing the confirm modal save flow.
type fakeSettingsServiceWithSaveSuccess struct {
	snapshot SettingsSnapshot
}

func (f *fakeSettingsServiceWithSaveSuccess) Snapshot() SettingsSnapshot {
	sections := make([]SettingsSection, len(f.snapshot.Sections))
	for i := range f.snapshot.Sections {
		sections[i] = f.snapshot.Sections[i]
		fields := make([]SettingsField, len(f.snapshot.Sections[i].Fields))
		copy(fields, f.snapshot.Sections[i].Fields)
		sections[i].Fields = fields
	}
	providers := make(map[string]ProviderStatus, len(f.snapshot.Providers))
	for k, v := range f.snapshot.Providers {
		providers[k] = v
	}
	return SettingsSnapshot{
		Sections:         sections,
		Providers:        providers,
		RawYAML:          f.snapshot.RawYAML,
		HarnessWarning:   f.snapshot.HarnessWarning,
		DiagnosticsState: f.snapshot.DiagnosticsState,
	}
}

func (f *fakeSettingsServiceWithSaveSuccess) RefreshConfigOnly(_ context.Context, _ *config.Config) error {
	return nil
}

func (f *fakeSettingsServiceWithSaveSuccess) RefreshWithDiagnostics(_ context.Context, _ *config.Config) error {
	return nil
}

func (f *fakeSettingsServiceWithSaveSuccess) Save(_ context.Context, _ []SettingsSection, _ Services) (SettingsApplyResult, error) {
	return SettingsApplyResult{Message: "Settings saved successfully"}, nil
}

func (f *fakeSettingsServiceWithSaveSuccess) TestProvider(_ context.Context, _ string, _ []SettingsSection) (ProviderStatus, error) {
	return ProviderStatus{}, nil
}

func (f *fakeSettingsServiceWithSaveSuccess) LoginProvider(_ context.Context, _, _ string, _ []SettingsSection, _ Services) (SettingsLoginResult, error) {
	return SettingsLoginResult{}, nil
}

func (f *fakeSettingsServiceWithSaveSuccess) RefreshLoginSnapshot(_ context.Context, _ []SettingsSection) error {
	return nil
}

func (f *fakeSettingsServiceWithSaveSuccess) RefreshLoginSnapshotFromConfig(_ context.Context, _ *config.Config) error {
	return nil
}

func (f *fakeSettingsServiceWithSaveSuccess) SetDiagnosticsState(state SettingsDiagnosticsState) {
	f.snapshot.DiagnosticsState = state
}

// fakeSettingsServiceWithSaveFailure is a test fake that fails on Save
// for testing error handling from the confirm modal.
type fakeSettingsServiceWithSaveFailure struct {
	snapshot SettingsSnapshot
}

func (f *fakeSettingsServiceWithSaveFailure) Snapshot() SettingsSnapshot {
	sections := make([]SettingsSection, len(f.snapshot.Sections))
	for i := range f.snapshot.Sections {
		sections[i] = f.snapshot.Sections[i]
		fields := make([]SettingsField, len(f.snapshot.Sections[i].Fields))
		copy(fields, f.snapshot.Sections[i].Fields)
		sections[i].Fields = fields
	}
	providers := make(map[string]ProviderStatus, len(f.snapshot.Providers))
	for k, v := range f.snapshot.Providers {
		providers[k] = v
	}
	return SettingsSnapshot{
		Sections:         sections,
		Providers:        providers,
		RawYAML:          f.snapshot.RawYAML,
		HarnessWarning:   f.snapshot.HarnessWarning,
		DiagnosticsState: f.snapshot.DiagnosticsState,
	}
}

func (f *fakeSettingsServiceWithSaveFailure) RefreshConfigOnly(_ context.Context, _ *config.Config) error {
	return nil
}

func (f *fakeSettingsServiceWithSaveFailure) RefreshWithDiagnostics(_ context.Context, _ *config.Config) error {
	return nil
}

func (f *fakeSettingsServiceWithSaveFailure) Save(_ context.Context, _ []SettingsSection, _ Services) (SettingsApplyResult, error) {
	return SettingsApplyResult{}, errors.New("save failed")
}

func (f *fakeSettingsServiceWithSaveFailure) TestProvider(_ context.Context, _ string, _ []SettingsSection) (ProviderStatus, error) {
	return ProviderStatus{}, nil
}

func (f *fakeSettingsServiceWithSaveFailure) LoginProvider(_ context.Context, _, _ string, _ []SettingsSection, _ Services) (SettingsLoginResult, error) {
	return SettingsLoginResult{}, nil
}

func (f *fakeSettingsServiceWithSaveFailure) RefreshLoginSnapshot(_ context.Context, _ []SettingsSection) error {
	return nil
}

func (f *fakeSettingsServiceWithSaveFailure) RefreshLoginSnapshotFromConfig(_ context.Context, _ *config.Config) error {
	return nil
}

func (f *fakeSettingsServiceWithSaveFailure) SetDiagnosticsState(state SettingsDiagnosticsState) {
	f.snapshot.DiagnosticsState = state
}

func TestSettingsPage_EscWithDirtyOpensConfirmModal(t *testing.T) {
	t.Parallel()
	page := newTestSettingsPage(&config.Config{})
	page.Open()
	page.SetDirty(true)

	if page.confirmModalOpen {
		t.Fatal("confirmModalOpen should be false initially")
	}

	updated, _ := page.Update(tea.KeyMsg{Type: tea.KeyEsc}, Services{})

	if !updated.confirmModalOpen {
		t.Fatal("expected confirmModalOpen to be true after Esc with dirty state")
	}
}

func TestSettingsPage_EscWithoutDirtyClosesOverlay(t *testing.T) {
	t.Parallel()
	page := newTestSettingsPage(&config.Config{})
	page.Open()

	if page.dirty {
		t.Fatal("dirty should be false initially")
	}

	_, cmd := page.Update(tea.KeyMsg{Type: tea.KeyEsc}, Services{})
	if cmd == nil {
		t.Fatal("expected Esc without dirty state to emit close-overlay command")
	}
	msg := cmd()
	if _, ok := msg.(CloseOverlayMsg); !ok {
		t.Fatalf("msg = %T, want CloseOverlayMsg", msg)
	}
}

func TestSettingsPage_EnterWithConfirmModalSavesAndCloses(t *testing.T) {
	t.Parallel()
	// Use the service that returns success on Save
	svc := &fakeSettingsServiceWithSaveSuccess{
		snapshot: SettingsSnapshot{
			Sections:         buildSettingsSections(&config.Config{}),
			Providers:        buildProviderStatuses(&config.Config{}),
			DiagnosticsState: SettingsDiagnosticsReady,
		},
	}
	page := NewSettingsPage(svc, styles.NewStyles(styles.DefaultTheme))
	page.SetSize(120, 40)
	page.Open()
	page.SetDirty(true)

	// Open the confirm modal first
	updated, _ := page.Update(tea.KeyMsg{Type: tea.KeyEsc}, Services{})
	if !updated.confirmModalOpen {
		t.Fatal("expected confirmModalOpen to be true after Esc with dirty state")
	}

	// Press Enter to confirm save
	updated, cmd := updated.Update(tea.KeyMsg{Type: tea.KeyEnter}, Services{})
	if cmd == nil {
		t.Fatal("expected Enter with modal open to trigger save command")
	}

	// Execute the save command
	msg := cmd()
	if _, ok := msg.(SettingsAppliedMsg); !ok {
		t.Fatalf("msg = %T, want SettingsAppliedMsg", msg)
	}

	// Apply the message to the page - this should close the overlay
	updated, cmd = updated.Update(msg, Services{})
	if cmd == nil {
		t.Fatal("expected CloseOverlayMsg command after successful save with closeAfterSave")
	}

	closeMsg := cmd()
	if _, ok := closeMsg.(CloseOverlayMsg); !ok {
		t.Fatalf("msg = %T, want CloseOverlayMsg after successful save", closeMsg)
	}
}

func TestSettingsPage_YKeyWithConfirmModalSavesAndCloses(t *testing.T) {
	t.Parallel()
	svc := &fakeSettingsServiceWithSaveSuccess{
		snapshot: SettingsSnapshot{
			Sections:         buildSettingsSections(&config.Config{}),
			Providers:        buildProviderStatuses(&config.Config{}),
			DiagnosticsState: SettingsDiagnosticsReady,
		},
	}
	page := NewSettingsPage(svc, styles.NewStyles(styles.DefaultTheme))
	page.SetSize(120, 40)
	page.Open()
	page.SetDirty(true)

	// Open the confirm modal first
	updated, _ := page.Update(tea.KeyMsg{Type: tea.KeyEsc}, Services{})
	if !updated.confirmModalOpen {
		t.Fatal("expected confirmModalOpen to be true after Esc with dirty state")
	}

	// Press y to confirm save
	updated, cmd := updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}}, Services{})
	if cmd == nil {
		t.Fatal("expected y key with modal open to trigger save command")
	}

	// Execute the save command
	msg := cmd()
	if _, ok := msg.(SettingsAppliedMsg); !ok {
		t.Fatalf("msg = %T, want SettingsAppliedMsg", msg)
	}

	// Apply the message to the page - this should close the overlay
	updated, cmd = updated.Update(msg, Services{})
	if cmd == nil {
		t.Fatal("expected CloseOverlayMsg command after successful save with closeAfterSave")
	}

	closeMsg := cmd()
	if _, ok := closeMsg.(CloseOverlayMsg); !ok {
		t.Fatalf("msg = %T, want CloseOverlayMsg after successful save", closeMsg)
	}
}

func TestSettingsPage_NKeyWithConfirmModalDiscardsAndCloses(t *testing.T) {
	t.Parallel()
	page := newTestSettingsPage(&config.Config{})
	page.Open()
	page.SetDirty(true)

	// Open the confirm modal first
	updated, _ := page.Update(tea.KeyMsg{Type: tea.KeyEsc}, Services{})
	if !updated.confirmModalOpen {
		t.Fatal("expected confirmModalOpen to be true after Esc with dirty state")
	}

	// Press n to discard
	_, cmd := updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}}, Services{})
	if cmd == nil {
		t.Fatal("expected n key with modal open to emit close-overlay command")
	}

	msg := cmd()
	if _, ok := msg.(CloseOverlayMsg); !ok {
		t.Fatalf("msg = %T, want CloseOverlayMsg after discarding", msg)
	}
}

func TestSettingsPage_EscWithConfirmModalDiscardsAndCloses(t *testing.T) {
	t.Parallel()
	page := newTestSettingsPage(&config.Config{})
	page.Open()
	page.SetDirty(true)

	// Open the confirm modal first
	updated, _ := page.Update(tea.KeyMsg{Type: tea.KeyEsc}, Services{})
	if !updated.confirmModalOpen {
		t.Fatal("expected confirmModalOpen to be true after Esc with dirty state")
	}

	// Press Esc again to discard
	_, cmd := updated.Update(tea.KeyMsg{Type: tea.KeyEsc}, Services{})
	if cmd == nil {
		t.Fatal("expected Esc with modal open to emit close-overlay command")
	}

	msg := cmd()
	if _, ok := msg.(CloseOverlayMsg); !ok {
		t.Fatalf("msg = %T, want CloseOverlayMsg after discarding with Esc", msg)
	}
}

func TestSettingsPage_FooterTextDoesNotShowSaveHint(t *testing.T) {
	t.Parallel()
	page := newTestSettingsPage(&config.Config{})
	page.Open()
	page.SetSize(120, 40)

	output := page.View()
	if strings.Contains(output, "[s] save") {
		t.Fatal("footer should not contain '[s] save' hint")
	}
	if strings.Contains(output, "[s]Save") {
		t.Fatal("footer should not contain '[s]Save' hint")
	}
}

func TestSettingsPage_SaveFailureFromConfirmModalClosesModal(t *testing.T) {
	t.Parallel()
	svc := &fakeSettingsServiceWithSaveFailure{
		snapshot: SettingsSnapshot{
			Sections:         buildSettingsSections(&config.Config{}),
			Providers:        buildProviderStatuses(&config.Config{}),
			DiagnosticsState: SettingsDiagnosticsReady,
		},
	}
	page := NewSettingsPage(svc, styles.NewStyles(styles.DefaultTheme))
	page.SetSize(120, 40)
	page.Open()
	page.SetDirty(true)

	// Open the confirm modal first
	updated, _ := page.Update(tea.KeyMsg{Type: tea.KeyEsc}, Services{})
	if !updated.confirmModalOpen {
		t.Fatal("expected confirmModalOpen to be true after Esc with dirty state")
	}

	// Press Enter to confirm save
	updated, cmd := updated.Update(tea.KeyMsg{Type: tea.KeyEnter}, Services{})
	if cmd == nil {
		t.Fatal("expected Enter with modal open to trigger save command")
	}

	// Execute the save command - it will fail
	msg := cmd()
	if _, ok := msg.(ErrMsg); !ok {
		t.Fatalf("msg = %T, want ErrMsg from failed save", msg)
	}

	// Apply the error message - confirmModalOpen should be false so user isn't trapped
	updated, _ = updated.Update(msg, Services{})
	if updated.confirmModalOpen {
		t.Fatal("expected confirmModalOpen to be false after save failure")
	}
}

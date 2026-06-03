package views

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/gitwork"
	"github.com/beeemT/substrate/internal/terminal"
	"github.com/beeemT/substrate/internal/tui/components"
	"github.com/beeemT/substrate/internal/tui/styles"
)

func TestActionMenuViewRendersActionRowsWithSharedChrome(t *testing.T) {
	t.Parallel()

	st := styles.NewStyles(styles.DefaultTheme)
	model := NewActionMenuModel(st)
	model.SetSize(48, 14)
	model.actions = []Action{
		{ID: "open", Label: "Open selected session with a very long label", Shortcut: "o"},
		{ID: "close", Label: "Close", Shortcut: "c"},
	}
	model.matches = []int{0, 1}
	model.query = " " // non-empty to trigger label truncation, not search placeholder ellipsis

	view := model.View()
	lines := strings.Split(view, "\n")
	if len(lines) > 14 {
		t.Fatalf("view has %d lines, want <= 14", len(lines))
	}
	for i, line := range lines {
		if got := ansi.StringWidth(line); got > 48 {
			t.Fatalf("line %d width = %d, want <= 48: %q", i+1, got, line)
		}
	}

	plain := ansi.Strip(view)
	for _, want := range []string{"╭", "Actions", "Search:", "›", "[o]", "[c]"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("view missing %q\n%s", want, plain)
		}
	}
	if !strings.Contains(plain, "…") {
		t.Fatalf("view should truncate the long action label with an ellipsis\n%s", plain)
	}
}

func TestActionMenuViewUsesAtLeastHalfScreen(t *testing.T) {
	t.Parallel()

	st := styles.NewStyles(styles.DefaultTheme)
	model := NewActionMenuModel(st)
	model.SetSize(200, 60)
	model.actions = []Action{{ID: "open", Label: "Open", Shortcut: "o"}}
	model.matches = []int{0}

	view := model.View()
	lines := strings.Split(view, "\n")
	if len(lines) < 30 {
		t.Fatalf("view height = %d lines, want at least 30", len(lines))
	}
	for i, line := range lines {
		if got := ansi.StringWidth(line); got > 200 {
			t.Fatalf("line %d width = %d, want <= 200: %q", i+1, got, line)
		}
	}
	if got := ansi.StringWidth(lines[0]); got < 100 {
		t.Fatalf("view width = %d, want at least 100", got)
	}
}

func TestActionRegistryIncludesOverviewOpenTerminalPicker(t *testing.T) {
	st := styles.NewStyles(styles.DefaultTheme)
	app := &App{
		content: NewContentModel(st),
		plans:   make(map[string]*domain.Plan),
	}
	app.content.SetMode(ContentModeOverview)

	actions := app.BuildActionRegistry(ContextOverview)
	action := findAction(actions, "open_worktree_picker")
	if action == nil {
		t.Fatalf("overview actions missing open_worktree_picker: %#v", actionIDs(actions))
	}
	if action.Shortcut != "t" {
		t.Fatalf("shortcut = %q, want t", action.Shortcut)
	}
	msg := action.Handler(app)()
	if _, ok := msg.(OpenWorktreePickerMsg); !ok {
		t.Fatalf("handler msg = %#v, want OpenWorktreePickerMsg", msg)
	}
}

func TestActionRegistryIncludesOverviewOpenTerminalPickerInOverviewSubcontexts(t *testing.T) {
	st := styles.NewStyles(styles.DefaultTheme)
	app := &App{
		content: NewContentModel(st),
		plans:   make(map[string]*domain.Plan),
	}
	app.content.SetMode(ContentModeOverview)

	actions := app.BuildActionRegistry(ContextInterrupted)
	action := findAction(actions, "open_worktree_picker")
	if action == nil {
		t.Fatalf("interrupted overview actions missing open_worktree_picker: %#v", actionIDs(actions))
	}
}

func TestActionMenuSearchAcceptsSpaceKey(t *testing.T) {
	st := styles.NewStyles(styles.DefaultTheme)
	app := &App{content: NewContentModel(st)}
	model := NewActionMenuModel(st)
	model.SetSize(80, 24)
	model.Open(app, ContextGlobal)

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeySpace})
	if updated.query != " " {
		t.Fatalf("query = %q, want single space", updated.query)
	}
}

func TestActionMenuOpensAboveActiveOverlay(t *testing.T) {
	app := newSidebarDrilldownTestApp()
	app.activeOverlay = overlayRepoManager

	model, _ := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	updated := model.(*App)
	if updated.activeOverlay != overlayActionMenu {
		t.Fatalf("active overlay = %v, want action menu", updated.activeOverlay)
	}
	if updated.actionMenuReturnOverlay != overlayRepoManager {
		t.Fatalf("return overlay = %v, want repo manager", updated.actionMenuReturnOverlay)
	}
	if updated.actionMenu.context != ContextRepoManager {
		t.Fatalf("action menu context = %v, want repo manager", updated.actionMenu.context)
	}
}

func TestActionRegistryIncludesPreviouslyMissingActions(t *testing.T) {
	st := styles.NewStyles(styles.DefaultTheme)
	app := &App{
		content:      NewContentModel(st),
		plans:        make(map[string]*domain.Plan),
		logsOverlay:  NewLogsOverlay(nil, st),
		settingsPage: NewSettingsPage(&testSettingsService{}, st),
	}
	app.content.SetMode(ContentModeAgentSession)

	assertActionIDs(t, app.BuildActionRegistry(ContextAgentSessionLog), "goto_top")
	assertActionIDs(t, app.BuildActionRegistry(ContextLogs), "copy_log")
	assertActionIDs(t, app.BuildActionRegistry(ContextNewSession),
		"cycle_new_session_scope",
		"cycle_new_session_view",
		"cycle_new_session_state",
		"reset_new_session_filters",
		"save_new_session_filter",
		"load_new_session_filter",
		"manual_new_session",
	)
	assertActionIDs(t, app.BuildActionRegistry(ContextAddRepo),
		"manual_clone_url",
		"reload_repos",
		"toggle_owned_repos",
	)
	assertActionIDs(t, app.BuildActionRegistry(ContextSettings),
		"test_provider",
		"reveal_secrets",
	)
}

func assertActionIDs(t *testing.T, actions []Action, ids ...string) {
	t.Helper()
	for _, id := range ids {
		if findAction(actions, id) == nil {
			t.Fatalf("actions missing %s: %#v", id, actionIDs(actions))
		}
	}
}

func TestActionRegistryIncludesFocusedSidebarOpenTerminal(t *testing.T) {
	app := appWithFocusedTerminalSession(t, mainFocusSidebar)

	actions := app.BuildActionRegistry(ContextGlobal)
	action := findAction(actions, "open_terminal")
	if action == nil {
		t.Fatalf("actions missing open_terminal: %#v", actionIDs(actions))
	}
	if action.Shortcut != "t" {
		t.Fatalf("shortcut = %q, want t", action.Shortcut)
	}
	if cmd := action.Handler(app); cmd == nil {
		t.Fatal("handler returned nil command")
	}
}

func TestActionRegistryIncludesAgentSessionLogOpenTerminal(t *testing.T) {
	app := appWithFocusedTerminalSession(t, mainFocusContent)

	actions := app.BuildActionRegistry(ContextAgentSessionLog)
	action := findAction(actions, "open_terminal")
	if action == nil {
		t.Fatalf("actions missing open_terminal: %#v", actionIDs(actions))
	}
	if cmd := action.Handler(app); cmd == nil {
		t.Fatal("handler returned nil command")
	}
}

func appWithFocusedTerminalSession(t *testing.T, focus mainFocusArea) *App {
	t.Helper()
	app := newSidebarDrilldownTestApp()
	model, _ := app.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	updated := model.(*App)
	for i := range updated.sessions {
		if updated.sessions[i].ID == "sess-1" {
			updated.sessions[i].WorktreePath = "/workspace/repo-a"
			break
		}
	}
	updated.mainFocus = focus
	updated.sidebarMode = sidebarPaneTasks
	updated.taskSessionSelectionByWorkItem[updated.currentWorkItemID] = "sess-1"
	if focus == mainFocusContent {
		updated.content.SetMode(ContentModeAgentSession)
		updated.content.sessionLog.SetLogPath("sess-1", "/tmp/session.log")
	}
	return updated
}

func TestActionRegistryIncludesWorktreePickerActions(t *testing.T) {
	st := styles.NewStyles(styles.DefaultTheme)
	app := &App{
		content:        NewContentModel(st),
		worktreePicker: NewWorktreePickerOverlay("/workspace", nil, st),
	}
	app.worktreePicker.active = true
	app.worktreePicker.worktrees = []gitwork.Worktree{{Path: "/workspace/repo/main", Branch: "main", IsMain: true}}
	app.worktreePicker.worktreeList.SetItems([]list.Item{worktreePickerItem{worktree: app.worktreePicker.worktrees[0]}})

	actions := app.BuildActionRegistry(ContextWorktreePicker)
	openAction := findAction(actions, "open_selected_worktree_terminal")
	if openAction == nil {
		t.Fatalf("worktree picker actions missing terminal action: %#v", actionIDs(actions))
	}
	if openAction.Shortcut != "t" {
		t.Fatalf("shortcut = %q, want t", openAction.Shortcut)
	}
	msg := openAction.Handler(app)()
	if got, ok := msg.(OpenTerminalInWorktreeMsg); !ok || got.WorktreePath != "/workspace/repo/main" {
		t.Fatalf("handler msg = %#v, want OpenTerminalInWorktreeMsg for selected path", msg)
	}

	switchAction := findAction(actions, "switch_worktree_picker_focus")
	if switchAction == nil {
		t.Fatalf("worktree picker actions missing focus action: %#v", actionIDs(actions))
	}
	if app.worktreePicker.picker.Focus() != components.SplitPaneFocusLeft {
		t.Fatalf("initial focus = %v, want left", app.worktreePicker.picker.Focus())
	}
	switchAction.Handler(app)
	if app.worktreePicker.picker.Focus() != components.SplitPaneFocusRight {
		t.Fatalf("focus = %v, want right", app.worktreePicker.picker.Focus())
	}
}

func findAction(actions []Action, id string) *Action {
	for i := range actions {
		if actions[i].ID == id {
			return &actions[i]
		}
	}
	return nil
}

func actionIDs(actions []Action) []string {
	ids := make([]string, len(actions))
	for i := range actions {
		ids[i] = actions[i].ID
	}
	return ids
}

func TestTerminalOpenedMessageDescribesLimitedTerminals(t *testing.T) {
	if got := terminalOpenedMessage(terminal.TerminalWarp); got != "Opened terminal in Warp tab" {
		t.Fatalf("Warp message = %q", got)
	}
	if got := terminalOpenedMessage(terminal.TerminalAlacritty); !strings.Contains(got, "tmux or zellij") {
		t.Fatalf("Alacritty message = %q", got)
	}
	if got := terminalOpenedMessage(terminal.TerminalTerminal); got != "Opened terminal" {
		t.Fatalf("Terminal.app message = %q", got)
	}
}

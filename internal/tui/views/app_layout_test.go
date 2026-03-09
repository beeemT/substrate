package views

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/beeemT/substrate/internal/domain"
)

func TestAppStatusBarTextIncludesWorkspace(t *testing.T) {
	app := NewApp(Services{
		WorkspaceID:   "ws-1",
		WorkspaceName: "workspace",
		Settings:      &SettingsService{},
	})

	if got := app.statusBarText(); got != "workspace · 0 active sessions" {
		t.Fatalf("status bar text = %q, want %q", got, "workspace · 0 active sessions")
	}
}

func TestAppStatusBarTextCountsOnlyActiveSessions(t *testing.T) {
	t.Parallel()

	app := NewApp(Services{
		WorkspaceID:   "ws-1",
		WorkspaceName: "workspace",
		Settings:      &SettingsService{},
	})
	app.sessions = []domain.AgentSession{
		{ID: "pending", Status: domain.AgentSessionPending},
		{ID: "running", Status: domain.AgentSessionRunning},
		{ID: "waiting", Status: domain.AgentSessionWaitingForAnswer},
		{ID: "interrupted", Status: domain.AgentSessionInterrupted},
		{ID: "completed", Status: domain.AgentSessionCompleted},
		{ID: "failed", Status: domain.AgentSessionFailed},
	}

	if got := app.statusBarText(); got != "workspace · 3 active sessions" {
		t.Fatalf("status bar text = %q, want %q", got, "workspace · 3 active sessions")
	}
}

func TestAppViewUsesFooterForWorkspaceInfo(t *testing.T) {
	app := NewApp(Services{
		WorkspaceID:   "ws-1",
		WorkspaceName: "workspace",
		Settings:      &SettingsService{},
	})

	model, _ := app.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	updated, ok := model.(App)
	if !ok {
		t.Fatalf("model = %T, want App", model)
	}

	view := updated.View()
	lines := strings.Split(view, "\n")
	if !strings.Contains(view, "workspace · 0 active sessions") {
		t.Fatalf("view = %q, want workspace info in footer", view)
	}
	if strings.Contains(view, "Substrate ─ workspace") {
		t.Fatalf("view = %q, want header line removed", view)
	}
	if len(lines) != 20 {
		t.Fatalf("line count = %d, want 20", len(lines))
	}
	if !strings.Contains(lines[0], "╭") || !strings.Contains(lines[0], "╮") {
		t.Fatalf("top body line = %q, want rounded top borders", lines[0])
	}
	if !strings.Contains(lines[len(lines)-2], "╰") || !strings.Contains(lines[len(lines)-2], "╯") {
		t.Fatalf("bottom body line = %q, want rounded bottom borders above the footer", lines[len(lines)-2])
	}
	if strings.Contains(lines[len(lines)-1], "─") {
		t.Fatalf("footer line = %q, want borderless status bar", lines[len(lines)-1])
	}
}

func sizedLayoutTestApp(t *testing.T, width, height int) App {
	t.Helper()

	app := NewApp(Services{
		WorkspaceID:   "ws-1",
		WorkspaceName: "workspace",
		Settings:      &SettingsService{},
	})

	model, _ := app.Update(tea.WindowSizeMsg{Width: width, Height: height})
	updated, ok := model.(App)
	if !ok {
		t.Fatalf("model = %T, want App", model)
	}
	return updated
}

func assertAppViewFitsWindow(t *testing.T, view string, width, height int) []string {
	t.Helper()

	lines := strings.Split(view, "\n")
	if got := len(lines); got != height {
		t.Fatalf("line count = %d, want %d\nview:\n%s", got, height, view)
	}
	for i, line := range lines {
		if got := ansi.StringWidth(line); got > width {
			t.Fatalf("line %d width = %d, want <= %d\nline: %q", i+1, got, width, line)
		}
	}
	return lines
}

func assertBodyEndsAboveFooter(t *testing.T, lines []string) {
	t.Helper()

	if !strings.Contains(lines[len(lines)-2], "╰") || !strings.Contains(lines[len(lines)-2], "╯") {
		t.Fatalf("bottom body line = %q, want rounded bottom borders above the footer", lines[len(lines)-2])
	}
	if strings.Contains(lines[len(lines)-1], "─") {
		t.Fatalf("footer line = %q, want borderless status bar", lines[len(lines)-1])
	}
}

func TestAppViewWithSessionInteractionFitsWindow(t *testing.T) {
	t.Parallel()

	app := sizedLayoutTestApp(t, 72, 16)
	app.sidebar.SetEntries([]SidebarEntry{{
		Kind:           SidebarEntryWorkItem,
		WorkItemID:     "wi-1",
		SessionID:      "sess-1",
		ExternalID:     "SUB-1",
		Title:          "Investigate overflow",
		WorkspaceName:  "workspace",
		RepositoryName: "repo-1",
		State:          domain.WorkItemImplementing,
		SessionStatus:  domain.AgentSessionRunning,
	}})
	app.content.SetSessionInteraction(
		"SUB-1 · Investigate overflow",
		"SUB-1 · workspace · repo-1 · sess-1",
		[]string{"line 1", "line 2", "line 3", "line 4"},
	)

	lines := assertAppViewFitsWindow(t, app.View(), 72, 16)
	assertBodyEndsAboveFooter(t, lines)
}

func TestAppViewWithImplementingSessionFitsWindow(t *testing.T) {
	t.Parallel()

	app := sizedLayoutTestApp(t, 72, 16)
	app.sidebar.SetEntries([]SidebarEntry{{
		Kind:          SidebarEntryWorkItem,
		WorkItemID:    "wi-1",
		ExternalID:    "SUB-1",
		Title:         "Implement overflow fix",
		State:         domain.WorkItemImplementing,
		SessionStatus: domain.AgentSessionRunning,
	}})
	app.content.SetWorkItem(&domain.WorkItem{
		ID:         "wi-1",
		ExternalID: "SUB-1",
		Title:      "Implement overflow fix",
		State:      domain.WorkItemImplementing,
	})
	app.content.SetMode(ContentModeImplementing)
	app.content.implementing.SetRepos([]RepoProgress{{
		Name:      "repo-1",
		SubPlanID: "sp-1",
		SessionID: "sess-1",
		Status:    domain.SubPlanInProgress,
	}})

	lines := assertAppViewFitsWindow(t, app.View(), 72, 16)
	assertBodyEndsAboveFooter(t, lines)
}

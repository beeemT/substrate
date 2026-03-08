package views

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

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

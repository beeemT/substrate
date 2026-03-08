package views

import (
	"testing"

	"github.com/beeemT/substrate/internal/domain"
)

func TestLoadHistoryEntry_LocalWorkspaceUsesWorkItemContent(t *testing.T) {
	app := NewApp(Services{
		WorkspaceID:   "ws-local",
		WorkspaceName: "local",
		Settings:      &SettingsService{},
	})
	app.workItems = []domain.WorkItem{{
		ID:         "wi-1",
		ExternalID: "SUB-1",
		Title:      "Local item",
		State:      domain.WorkItemIngested,
	}}

	cmd := app.loadHistoryEntry(SidebarEntry{
		Kind:        SidebarEntrySessionHistory,
		WorkItemID:  "wi-1",
		SessionID:   "sess-local",
		WorkspaceID: "ws-local",
		ExternalID:  "SUB-1",
		Title:       "Local item",
	})

	if cmd != nil {
		t.Fatalf("loadHistoryEntry() cmd = %v, want nil for local workspace entry", cmd)
	}
	if app.currentWorkItemID != "wi-1" {
		t.Fatalf("currentWorkItemID = %q, want wi-1", app.currentWorkItemID)
	}
	if app.currentHistorySessionID != "" {
		t.Fatalf("currentHistorySessionID = %q, want empty", app.currentHistorySessionID)
	}
	if app.content.Mode() != ContentModeReadyToPlan {
		t.Fatalf("content mode = %v, want %v", app.content.Mode(), ContentModeReadyToPlan)
	}
}

func TestLoadHistoryEntry_RemoteWorkspaceUsesSessionInteraction(t *testing.T) {
	app := NewApp(Services{
		WorkspaceID:   "ws-local",
		WorkspaceName: "local",
		Settings:      &SettingsService{},
	})

	cmd := app.loadHistoryEntry(SidebarEntry{
		Kind:          SidebarEntrySessionHistory,
		SessionID:     "sess-remote",
		WorkspaceID:   "ws-remote",
		WorkspaceName: "remote",
		ExternalID:    "SUB-2",
		Title:         "Remote item",
	})
	if cmd == nil {
		t.Fatal("loadHistoryEntry() cmd = nil, want interaction load command")
	}
	if app.currentWorkItemID != "" {
		t.Fatalf("currentWorkItemID = %q, want empty", app.currentWorkItemID)
	}
	if app.currentHistorySessionID != "sess-remote" {
		t.Fatalf("currentHistorySessionID = %q, want sess-remote", app.currentHistorySessionID)
	}
	if app.content.Mode() != ContentModeSessionInteraction {
		t.Fatalf("content mode = %v, want %v", app.content.Mode(), ContentModeSessionInteraction)
	}

	msg := cmd()
	loaded, ok := msg.(SessionInteractionLoadedMsg)
	if !ok {
		t.Fatalf("cmd() message = %T, want SessionInteractionLoadedMsg", msg)
	}
	if loaded.SessionID != "sess-remote" {
		t.Fatalf("loaded session id = %q, want sess-remote", loaded.SessionID)
	}
}

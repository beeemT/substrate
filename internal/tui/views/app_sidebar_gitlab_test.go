package views

import (
	"testing"

	"github.com/beeemT/substrate/internal/domain"
)

func TestSidebarEntryFromWorkItem_GitlabUsesProjectPath(t *testing.T) {
	app := NewApp(Services{
		WorkspaceID:   "ws-1",
		WorkspaceName: "workspace",
		Settings:      &SettingsService{},
	})

	wi := domain.Session{
		ID:         "wi-1",
		ExternalID: "gl:issue:1234#42",
		Source:     providerGitlab,
		Title:      "Fix auth timeouts",
		State:      domain.SessionIngested,
		Metadata: map[string]any{
			"tracker_refs": []domain.TrackerReference{
				{
					Provider: "gitlab",
					Kind:     "issue",
					Repo:     "acme/rocket",
					Number:   42,
					URL:      "https://gitlab.example.com/acme/rocket/-/issues/42",
				},
			},
		},
	}

	entry := app.sidebarEntryFromWorkItem(wi)

	// ExternalID must be untouched — only the display label changes.
	if entry.ExternalID != wi.ExternalID {
		t.Fatalf("ExternalID = %q, want %q (must not be modified)", entry.ExternalID, wi.ExternalID)
	}

	wantLabel := "acme/rocket#42"
	if entry.ExternalLabel != wantLabel {
		t.Fatalf("ExternalLabel = %q, want %q", entry.ExternalLabel, wantLabel)
	}

	if got := entry.displayExternalID(); got != wantLabel {
		t.Fatalf("displayExternalID() = %q, want %q", got, wantLabel)
	}
}

func TestSidebarEntryFromWorkItem_GitlabNoTrackerRefs_FallsBackToNumeric(t *testing.T) {
	app := NewApp(Services{
		WorkspaceID:   "ws-1",
		WorkspaceName: "workspace",
		Settings:      &SettingsService{},
	})

	wi := domain.Session{
		ID:         "wi-2",
		ExternalID: "gl:issue:1234#42",
		Source:     providerGitlab,
		Title:      "No refs",
		State:      domain.SessionIngested,
	}

	entry := app.sidebarEntryFromWorkItem(wi)

	if entry.ExternalLabel != "" {
		t.Fatalf("ExternalLabel = %q, want empty (no tracker refs)", entry.ExternalLabel)
	}
	// Falls back to stripping the protocol prefix.
	if got := entry.displayExternalID(); got != "1234#42" {
		t.Fatalf("displayExternalID() = %q, want %q", got, "1234#42")
	}
}

func TestSidebarEntryFromWorkItem_NonGitlabUnaffected(t *testing.T) {
	app := NewApp(Services{
		WorkspaceID:   "ws-1",
		WorkspaceName: "workspace",
		Settings:      &SettingsService{},
	})

	wi := domain.Session{
		ID:         "wi-3",
		ExternalID: "gh:issue:owner/repo#99",
		Source:     providerGithub,
		Title:      "GitHub issue",
		State:      domain.SessionIngested,
		Metadata: map[string]any{
			"tracker_refs": []domain.TrackerReference{
				{Provider: "github", Kind: "issue", Repo: "owner/repo", Number: 99},
			},
		},
	}

	entry := app.sidebarEntryFromWorkItem(wi)

	if entry.ExternalLabel != "" {
		t.Fatalf("ExternalLabel = %q, want empty (non-gitlab must be unchanged)", entry.ExternalLabel)
	}
	if entry.ExternalID != wi.ExternalID {
		t.Fatalf("ExternalID = %q, want %q", entry.ExternalID, wi.ExternalID)
	}
}

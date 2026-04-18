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

	want := "gl:issue:acme/rocket#42"
	if entry.ExternalID != want {
		t.Fatalf("ExternalID = %q, want %q", entry.ExternalID, want)
	}

	wantDisplay := "acme/rocket#42"
	if got := entry.displayExternalID(); got != wantDisplay {
		t.Fatalf("displayExternalID() = %q, want %q", got, wantDisplay)
	}
}

func TestSidebarEntryFromWorkItem_GitlabNoTrackerRefs_KeepsExternalID(t *testing.T) {
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

	// Without tracker refs, the ExternalID must remain unchanged so the
	// numeric form is shown rather than nothing.
	if entry.ExternalID != wi.ExternalID {
		t.Fatalf("ExternalID = %q, want %q", entry.ExternalID, wi.ExternalID)
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

	if entry.ExternalID != wi.ExternalID {
		t.Fatalf("ExternalID = %q, want %q (non-gitlab must be unchanged)", entry.ExternalID, wi.ExternalID)
	}
}

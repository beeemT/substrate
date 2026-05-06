package views

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/repository"
	"github.com/beeemT/substrate/internal/service"
)

func TestSidebarEntryFromWorkItem_GitlabUsesProjectPath(t *testing.T) {
	app := newTestApp(Services{
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
	app := newTestApp(Services{
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
	app := newTestApp(Services{
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

type emptySessionArtifactRepo struct{}

func (r emptySessionArtifactRepo) Upsert(_ context.Context, _ domain.SessionReviewArtifact) error {
	return nil
}

func (r emptySessionArtifactRepo) ListByWorkItemID(_ context.Context, _ string) ([]domain.SessionReviewArtifact, error) {
	return nil, nil
}

func (r emptySessionArtifactRepo) ListByWorkspaceID(_ context.Context, _ string) ([]domain.SessionReviewArtifact, error) {
	return nil, nil
}

func TestTaskSidebarEntries_GitlabIncludesRecordedArtifactWithoutLink(t *testing.T) {
	t.Parallel()

	now := time.Now()
	payload, err := json.Marshal(domain.ReviewArtifactEventPayload{
		WorkItemID: "wi-1",
		Artifact: domain.ReviewArtifact{
			Provider:  "gitlab",
			Kind:      "MR",
			RepoName:  "group/project",
			Ref:       "!5",
			URL:       "https://gitlab.com/group/project/-/merge_requests/5",
			State:     "draft",
			Branch:    "sub-GL-137-1015-fix-bug",
			UpdatedAt: now,
		},
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	eventRepo := &overviewEventRepo{events: []domain.SystemEvent{{
		ID:          domain.NewID(),
		EventType:   string(domain.EventReviewArtifactRecorded),
		WorkspaceID: "ws-1",
		Payload:     string(payload),
		CreatedAt:   now,
	}}}
	app := newTestApp(Services{
		WorkspaceID:   "ws-1",
		WorkspaceName: "workspace",
		Settings:      &SettingsService{},
		Events:        service.NewEventService(repository.NoopTransacter{Res: repository.Resources{Events: eventRepo}}),
		SessionArtifacts: service.NewSessionReviewArtifactService(repository.NoopTransacter{Res: repository.Resources{
			SessionReviewArtifacts: emptySessionArtifactRepo{},
		}}),
	})
	app.workItems = []domain.Session{{
		ID:          "wi-1",
		WorkspaceID: "ws-1",
		ExternalID:  "gl:issue:137#1015",
		Source:      providerGitlab,
		Title:       "GitLab issue",
		State:       domain.SessionImplementing,
		UpdatedAt:   now,
	}}

	entries := app.taskSidebarEntries("wi-1")
	for _, entry := range entries {
		if entry.Kind == SidebarEntryTaskArtifacts {
			if entry.SessionID != taskSidebarArtifactsID || entry.SubtitleText != "1 artifact" {
				t.Fatalf("artifact entry = %+v", entry)
			}
			return
		}
	}
	t.Fatalf("task sidebar entries missing artifact node: %+v", entries)
}

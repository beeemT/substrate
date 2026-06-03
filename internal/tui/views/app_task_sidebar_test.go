package views

import (
	"context"
	"testing"
	"time"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/repository"
	"github.com/beeemT/substrate/internal/service"
)

type noopEventRepo struct{}

func (r *noopEventRepo) Create(_ context.Context, _ domain.SystemEvent) error { return nil }
func (r *noopEventRepo) ListByType(_ context.Context, _ string, _ int) ([]domain.SystemEvent, error) {
	return nil, nil
}
func (r *noopEventRepo) ListByWorkspaceID(_ context.Context, _ string, _ int) ([]domain.SystemEvent, error) {
	return nil, nil
}
func (r *noopEventRepo) DeleteByID(_ context.Context, _ string) error         { return nil }
func (r *noopEventRepo) DeleteByWorkItemID(_ context.Context, _ string) error { return nil }

type noopSessionArtifactRepo struct{}

func (r noopSessionArtifactRepo) Upsert(_ context.Context, _ domain.SessionReviewArtifact) error {
	return nil
}
func (r noopSessionArtifactRepo) ListByWorkItemID(_ context.Context, _ string) ([]domain.SessionReviewArtifact, error) {
	return nil, nil
}
func (r noopSessionArtifactRepo) ListByWorkspaceID(_ context.Context, _ string) ([]domain.SessionReviewArtifact, error) {
	return nil, nil
}
func (r noopSessionArtifactRepo) TransferArtifactLinks(_ context.Context, _, _ string) error {
	return nil
}
func (r noopSessionArtifactRepo) DeleteByWorkItemID(_ context.Context, _ string) error { return nil }

func TestTaskSidebarEntries_ForemanSession(t *testing.T) {
	now := time.Now()
	app := newTestApp(Services{
		WorkspaceID:   "ws-1",
		WorkspaceName: "workspace",
		Settings:      newTestSettingsService(),
		Events:        service.NewEventService(repository.NoopTransacter{Res: repository.Resources{Events: &noopEventRepo{}}}),
		SessionArtifacts: service.NewSessionReviewArtifactService(repository.NoopTransacter{Res: repository.Resources{
			SessionReviewArtifacts: &noopSessionArtifactRepo{},
		}}),
	})
	app.workItems = []domain.Session{{
		ID:          "wi-1",
		WorkspaceID: "ws-1",
		ExternalID:  "gh:issue:42",
		Source:      providerGithub,
		Title:       "Test issue",
		State:       domain.SessionImplementing,
		UpdatedAt:   now,
	}}
	app.sessions = []domain.AgentSession{{
		ID:          "foreman-session-1",
		WorkItemID:  "wi-1",
		WorkspaceID: "ws-1",
		Kind:        domain.AgentSessionKindForeman,
		HarnessName: "omp",
		Status:      domain.AgentSessionRunning,
		CreatedAt:   now.Add(-10 * time.Minute),
		UpdatedAt:   now,
	}}

	entries := app.taskSidebarEntries("wi-1")

	// Find the Foreman group header.
	var hasForemanHeader bool
	var foremanSessionEntry *SidebarEntry
	for _, entry := range entries {
		if entry.Kind == SidebarEntryGroupHeader && entry.GroupTitle == "Foreman" {
			hasForemanHeader = true
		}
		if entry.Kind == SidebarEntryTaskSession && entry.RepositoryName == "Foreman" {
			foremanSessionEntry = &entry
		}
	}

	if !hasForemanHeader {
		t.Fatalf("expected Foreman group header in entries, got: %+v", entries)
	}

	if foremanSessionEntry == nil {
		t.Fatalf("expected Foreman task session entry in entries, got: %+v", entries)
	}

	if foremanSessionEntry.SessionID != "foreman-session-1" {
		t.Fatalf("Foreman session SessionID = %q, want %q", foremanSessionEntry.SessionID, "foreman-session-1")
	}

	if foremanSessionEntry.RepositoryName != "Foreman" {
		t.Fatalf("Foreman session RepositoryName = %q, want %q", foremanSessionEntry.RepositoryName, "Foreman")
	}
}

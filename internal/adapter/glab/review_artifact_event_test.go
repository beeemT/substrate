package glab

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	coreadapter "github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/repository"
	"github.com/beeemT/substrate/internal/service"
)

type glabArtifactEventRepo struct {
	events []domain.SystemEvent
}

func (r *glabArtifactEventRepo) Create(_ context.Context, e domain.SystemEvent) error {
	r.events = append(r.events, e)

	return nil
}

func (r *glabArtifactEventRepo) ListByType(_ context.Context, eventType string, limit int) ([]domain.SystemEvent, error) {
	filtered := make([]domain.SystemEvent, 0, len(r.events))
	for _, event := range r.events {
		if event.EventType == eventType {
			filtered = append(filtered, event)
		}
	}
	if limit > 0 && len(filtered) > limit {
		filtered = filtered[:limit]
	}

	return filtered, nil
}

func (r *glabArtifactEventRepo) ListByWorkspaceID(_ context.Context, workspaceID string, limit int) ([]domain.SystemEvent, error) {
	filtered := make([]domain.SystemEvent, 0, len(r.events))
	for _, event := range r.events {
		if event.WorkspaceID == workspaceID {
			filtered = append(filtered, event)
		}
	}
	if limit > 0 && len(filtered) > limit {
		filtered = filtered[:limit]
	}

	return filtered, nil
}

func TestWorktreeCreatedPersistsReviewArtifactEvent(t *testing.T) {
	t.Parallel()

	repo := &glabArtifactEventRepo{}
	stub := &stubRunner{output: []byte("https://gitlab.com/org/repo/-/merge_requests/5\n")}
	a := newWithRunner(config.GlabConfig{}, coreadapter.ReviewArtifactRepos{Events: service.NewEventService(repository.NoopTransacter{Res: repository.Resources{Events: repo}})}, "", stub.run)
	payload := mustJSON(worktreePayload{
		WorkspaceID:  "ws-1",
		WorkItemID:   "wi-1",
		Repository:   "group/repo",
		Branch:       "sub-GL-1234-40-fix-bug",
		WorktreePath: "/tmp/wt",
		SubPlan:      "Repo specific implementation plan",
	})
	if err := a.OnEvent(context.Background(), domain.SystemEvent{EventType: string(domain.EventWorktreeCreated), Payload: payload}); err != nil {
		t.Fatalf("OnEvent: %v", err)
	}
	if len(repo.events) != 1 {
		t.Fatalf("persisted events = %d, want 1", len(repo.events))
	}
	var recorded domain.ReviewArtifactEventPayload
	if err := json.Unmarshal([]byte(repo.events[0].Payload), &recorded); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if recorded.WorkItemID != "wi-1" {
		t.Fatalf("work item id = %q", recorded.WorkItemID)
	}
	if recorded.Artifact.Ref != "!5" || recorded.Artifact.URL != "https://gitlab.com/org/repo/-/merge_requests/5" || recorded.Artifact.State != "draft" {
		t.Fatalf("artifact = %+v", recorded.Artifact)
	}
	if recorded.Artifact.UpdatedAt.IsZero() || time.Since(recorded.Artifact.UpdatedAt) > time.Minute {
		t.Fatalf("artifact updated_at = %v, want recent timestamp", recorded.Artifact.UpdatedAt)
	}
}

func TestWorktreeCreatedPersistsGitlabMRLinkFromCreatedURL(t *testing.T) {
	t.Parallel()

	mrRepo := &inMemGitlabMRRepo{}
	artifactRepo := &inMemArtifactLinkRepo{}
	stub := &stubRunner{output: []byte("https://gitlab.com/org/repo/-/merge_requests/5\n")}
	repos := coreadapter.ReviewArtifactRepos{
		GitlabMRs:        service.NewGitlabMRService(repository.NoopTransacter{Res: repository.Resources{GitlabMRs: mrRepo}}),
		SessionArtifacts: service.NewSessionReviewArtifactService(repository.NoopTransacter{Res: repository.Resources{SessionReviewArtifacts: artifactRepo}}),
	}
	a := newWithRunner(config.GlabConfig{}, repos, "", stub.run)
	payload := mustJSON(worktreePayload{
		WorkspaceID:  "ws-1",
		WorkItemID:   "wi-1",
		Repository:   "group/repo",
		Branch:       "sub-GL-137-1015-fix-bug",
		WorktreePath: "/tmp/wt",
	})

	if err := a.OnEvent(context.Background(), domain.SystemEvent{EventType: string(domain.EventWorktreeCreated), Payload: payload}); err != nil {
		t.Fatalf("OnEvent: %v", err)
	}

	mr, err := mrRepo.GetByIID(context.Background(), "group/repo", 5)
	if err != nil {
		t.Fatalf("GetByIID persisted MR: %v", err)
	}
	if mr.WebURL != "https://gitlab.com/org/repo/-/merge_requests/5" || mr.SourceBranch != "sub-GL-137-1015-fix-bug" || mr.WorktreePath != "/tmp/wt" {
		t.Fatalf("persisted MR = %+v", mr)
	}
	if len(artifactRepo.links) != 1 {
		t.Fatalf("artifact links = %d, want 1", len(artifactRepo.links))
	}
	link := artifactRepo.links[0]
	if link.WorkItemID != "wi-1" || link.Provider != "gitlab" || link.ProviderArtifactID != mr.ID {
		t.Fatalf("artifact link = %+v, MR ID %q", link, mr.ID)
	}
}

func TestWorktreeCreatedPersistsGitlabMRUsingReviewProjectPath(t *testing.T) {
	t.Parallel()

	mrRepo := &inMemGitlabMRRepo{}
	artifactRepo := &inMemArtifactLinkRepo{}
	eventRepo := &glabArtifactEventRepo{}
	stub := &stubRunner{output: []byte("https://gitlab.com/backend/postback-service/-/merge_requests/421\n")}
	repos := coreadapter.ReviewArtifactRepos{
		Events:           service.NewEventService(repository.NoopTransacter{Res: repository.Resources{Events: eventRepo}}),
		GitlabMRs:        service.NewGitlabMRService(repository.NoopTransacter{Res: repository.Resources{GitlabMRs: mrRepo}}),
		SessionArtifacts: service.NewSessionReviewArtifactService(repository.NoopTransacter{Res: repository.Resources{SessionReviewArtifacts: artifactRepo}}),
	}
	a := newWithRunner(config.GlabConfig{}, repos, "", stub.run)
	payload := mustJSON(worktreePayload{
		WorkspaceID:  "ws-1",
		WorkItemID:   "wi-1",
		Repository:   "postback-service",
		Branch:       "sub-GL-421-fix-comments",
		WorktreePath: "/tmp/wt",
		Review: domain.ReviewRef{
			BaseRepo: domain.RepoRef{Provider: "gitlab", Owner: "backend", Repo: "postback-service"},
		},
	})

	if err := a.OnEvent(context.Background(), domain.SystemEvent{EventType: string(domain.EventWorktreeCreated), Payload: payload}); err != nil {
		t.Fatalf("OnEvent: %v", err)
	}

	mr, err := mrRepo.GetByIID(context.Background(), "backend/postback-service", 421)
	if err != nil {
		t.Fatalf("GetByIID persisted MR under review project path: %v", err)
	}
	if mr.ProjectPath != "backend/postback-service" || mr.SourceBranch != "sub-GL-421-fix-comments" {
		t.Fatalf("persisted MR = %+v", mr)
	}
	if len(eventRepo.events) != 1 {
		t.Fatalf("persisted events = %d, want 1", len(eventRepo.events))
	}
	var recorded domain.ReviewArtifactEventPayload
	if err := json.Unmarshal([]byte(eventRepo.events[0].Payload), &recorded); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if recorded.Artifact.RepoName != "backend/postback-service" {
		t.Fatalf("artifact repo = %q, want backend/postback-service", recorded.Artifact.RepoName)
	}
}

func TestExistingDraftMergeRequestRemainsDraft(t *testing.T) {
	t.Parallel()

	repo := &glabArtifactEventRepo{}
	stub := &stubRunner{output: []byte(`{"iid":5,"state":"opened","web_url":"https://gitlab.com/org/repo/-/merge_requests/5","draft":true}`)}
	a := newWithRunner(config.GlabConfig{}, coreadapter.ReviewArtifactRepos{Events: service.NewEventService(repository.NoopTransacter{Res: repository.Resources{Events: repo}})}, "", stub.run)
	payload := mustJSON(worktreePayload{WorkspaceID: "ws-1", WorkItemID: "wi-1", Repository: "group/repo", Branch: "sub-GL-1234-40-fix-bug", WorktreePath: "/tmp/wt"})
	if err := a.OnEvent(context.Background(), domain.SystemEvent{EventType: string(domain.EventWorktreeCreated), Payload: payload}); err != nil {
		t.Fatalf("OnEvent: %v", err)
	}
	if len(repo.events) != 1 {
		t.Fatalf("persisted events = %d, want 1", len(repo.events))
	}
	var recorded domain.ReviewArtifactEventPayload
	if err := json.Unmarshal([]byte(repo.events[0].Payload), &recorded); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if recorded.Artifact.State != "draft" {
		t.Fatalf("artifact = %+v, want draft state preserved", recorded.Artifact)
	}
}

func TestSubPlanPRReady_UsesPersistedArtifactAfterRestart(t *testing.T) {
	t.Parallel()

	// Simulate adapter restart: event repo returns no artifacts for this work item.
	// onSubPlanPRReady should still work - it uses the payload's WorktreePath and Review.
	repo := &glabArtifactEventRepo{events: []domain.SystemEvent{}} // empty - no persisted artifacts
	stub := &stubRunner{output: []byte(`{"iid":5,"state":"opened","web_url":"https://gitlab.com/group/project/-/merge_requests/5"}`)}
	a := newWithRunner(config.GlabConfig{}, coreadapter.ReviewArtifactRepos{Events: service.NewEventService(repository.NoopTransacter{Res: repository.Resources{Events: repo}})}, "", stub.run)

	payload := mustJSON(subPlanPRReadyPayload{
		WorkItemID:   "wi-1",
		WorkspaceID:  "ws-1",
		Branch:       "sub-branch",
		WorktreePath: "/tmp/wt",
		Repository:   "group/project",
		Review: domain.ReviewRef{
			BaseRepo: domain.RepoRef{Provider: "gitlab", Owner: "group", Repo: "project"},
		},
	})
	if err := a.OnEvent(context.Background(), domain.SystemEvent{EventType: string(domain.EventSubPlanPRReady), Payload: payload}); err != nil {
		t.Fatalf("OnEvent: %v", err)
	}

	// Expect: mr view (to find existing MR) + mr update --ready + mr view (refresh state after undrafting)
	if len(stub.calls) != 3 {
		t.Fatalf("glab calls = %d, want 3", len(stub.calls))
	}

	// First call: mr view to find the MR
	if !strings.Contains(strings.Join(stub.calls[0].args, " "), "mr view") {
		t.Fatalf("call[0] = %#v, want mr view", stub.calls[0])
	}
	if stub.calls[0].dir != "/tmp/wt" {
		t.Fatalf("call[0] dir = %q, want /tmp/wt", stub.calls[0].dir)
	}

	// Second call: mr update --ready
	if !strings.Contains(strings.Join(stub.calls[1].args, " "), "mr update") {
		t.Fatalf("call[1] = %#v, want mr update", stub.calls[1])
	}
	if !strings.Contains(strings.Join(stub.calls[1].args, " "), "--ready") {
		t.Fatalf("call[1] missing --ready")
	}

	// Event should be recorded
	if got := len(repo.events); got != 1 {
		t.Fatalf("persisted events = %d, want 1", got)
	}
	var recorded domain.ReviewArtifactEventPayload
	if err := json.Unmarshal([]byte(repo.events[0].Payload), &recorded); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if recorded.Artifact.State != "ready" || recorded.Artifact.WorktreePath != "/tmp/wt" || recorded.Artifact.RepoName != "group/project" {
		t.Fatalf("artifact = %+v", recorded.Artifact)
	}
	if recorded.WorkItemID != "wi-1" {
		t.Fatalf("workItemID = %q, want wi-1", recorded.WorkItemID)
	}
}

func TestClosedDraftMergeRequestRemainsClosed(t *testing.T) {
	t.Parallel()

	repo := &glabArtifactEventRepo{}
	stub := &stubRunner{output: []byte(`{"iid":5,"state":"closed","web_url":"https://gitlab.com/org/repo/-/merge_requests/5","draft":true}`)}
	a := newWithRunner(config.GlabConfig{}, coreadapter.ReviewArtifactRepos{Events: service.NewEventService(repository.NoopTransacter{Res: repository.Resources{Events: repo}})}, "", stub.run)
	payload := mustJSON(worktreePayload{WorkspaceID: "ws-1", WorkItemID: "wi-1", Repository: "group/repo", Branch: "sub-GL-1234-40-fix-bug", WorktreePath: "/tmp/wt"})
	if err := a.OnEvent(context.Background(), domain.SystemEvent{EventType: string(domain.EventWorktreeCreated), Payload: payload}); err != nil {
		t.Fatalf("OnEvent: %v", err)
	}
	if len(repo.events) != 1 {
		t.Fatalf("persisted events = %d, want 1", len(repo.events))
	}
	var recorded domain.ReviewArtifactEventPayload
	if err := json.Unmarshal([]byte(repo.events[0].Payload), &recorded); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if recorded.Artifact.State != "closed" {
		t.Fatalf("artifact = %+v, want closed state preserved", recorded.Artifact)
	}
}

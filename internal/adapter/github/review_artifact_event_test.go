package github

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/domain"
)

type githubArtifactEventRepo struct {
	events []domain.SystemEvent
}

func (r *githubArtifactEventRepo) Create(_ context.Context, e domain.SystemEvent) error {
	r.events = append(r.events, e)
	return nil
}

func (r *githubArtifactEventRepo) ListByType(_ context.Context, eventType string, limit int) ([]domain.SystemEvent, error) {
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

func (r *githubArtifactEventRepo) ListByWorkspaceID(_ context.Context, workspaceID string, limit int) ([]domain.SystemEvent, error) {
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

func mustReviewArtifactPayload(t *testing.T, workItemID string, artifact domain.ReviewArtifact) string {
	t.Helper()
	payload, err := json.Marshal(domain.ReviewArtifactEventPayload{WorkItemID: workItemID, Artifact: artifact})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return string(payload)
}

func TestWorktreeCreatedPersistsReviewArtifactEvent(t *testing.T) {
	t.Parallel()

	repo := &githubArtifactEventRepo{}
	a, err := newWithDeps(context.Background(), config.GithubConfig{PollInterval: "10ms"}, repo, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/user":
			return jsonResp(t, http.StatusOK, map[string]any{"login": "alice"}), nil
		case "/repos/acme/rocket/pulls":
			if req.Method == http.MethodGet {
				return jsonResp(t, http.StatusOK, []any{}), nil
			}
			return jsonResp(t, http.StatusCreated, map[string]any{"number": 7, "draft": true, "html_url": "https://github.com/acme/rocket/pull/7"}), nil
		default:
			return jsonResp(t, http.StatusOK, map[string]any{}), nil
		}
	}), func(context.Context) (string, error) { return "token", nil })
	if err != nil {
		t.Fatalf("newWithDeps: %v", err)
	}
	payload := `{"workspace_id":"ws-1","work_item_id":"wi-1","branch":"sub-branch","work_item_title":"Feature title","sub_plan":"Repo specific implementation plan","review":{"base_repo":{"provider":"github","owner":"acme","repo":"rocket"},"head_repo":{"provider":"github","owner":"acme","repo":"rocket"},"base_branch":"main","head_branch":"sub-branch"}}`
	if err := a.OnEvent(context.Background(), domain.SystemEvent{EventType: string(domain.EventWorktreeCreated), Payload: payload}); err != nil {
		t.Fatalf("OnEvent: %v", err)
	}
	if len(repo.events) != 1 {
		t.Fatalf("persisted events = %d, want 1", len(repo.events))
	}
	if repo.events[0].EventType != string(domain.EventReviewArtifactRecorded) {
		t.Fatalf("event type = %q", repo.events[0].EventType)
	}
	var recorded domain.ReviewArtifactEventPayload
	if err := json.Unmarshal([]byte(repo.events[0].Payload), &recorded); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if recorded.WorkItemID != "wi-1" {
		t.Fatalf("work item id = %q", recorded.WorkItemID)
	}
	if recorded.Artifact.Ref != "#7" || recorded.Artifact.URL != "https://github.com/acme/rocket/pull/7" || recorded.Artifact.State != "draft" {
		t.Fatalf("artifact = %+v", recorded.Artifact)
	}
	if recorded.Artifact.UpdatedAt.IsZero() || time.Since(recorded.Artifact.UpdatedAt) > time.Minute {
		t.Fatalf("artifact updated_at = %v, want recent timestamp", recorded.Artifact.UpdatedAt)
	}
}

func TestWorkItemCompletedUpdatesAllPersistedArtifacts(t *testing.T) {
	t.Parallel()

	now := time.Now()
	repo := &githubArtifactEventRepo{events: []domain.SystemEvent{
		{ID: domain.NewID(), EventType: string(domain.EventReviewArtifactRecorded), WorkspaceID: "ws-1", Payload: mustReviewArtifactPayload(t, "wi-1", domain.ReviewArtifact{Provider: "github", Kind: "PR", RepoName: "acme/rocket", Ref: "#7", URL: "https://github.com/acme/rocket/pull/7", State: "draft", Branch: "sub-branch", UpdatedAt: now})},
		{ID: domain.NewID(), EventType: string(domain.EventReviewArtifactRecorded), WorkspaceID: "ws-1", Payload: mustReviewArtifactPayload(t, "wi-1", domain.ReviewArtifact{Provider: "github", Kind: "PR", RepoName: "acme/engine", Ref: "#9", URL: "https://github.com/acme/engine/pull/9", State: "draft", Branch: "sub-branch", UpdatedAt: now})},
	}}
	var requests []string
	a, err := newWithDeps(context.Background(), config.GithubConfig{PollInterval: "10ms"}, repo, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests = append(requests, req.Method+" "+req.URL.Path)
		switch req.URL.Path {
		case "/user":
			return jsonResp(t, http.StatusOK, map[string]any{"login": "alice"}), nil
		case "/repos/acme/rocket/pulls/7":
			return jsonResp(t, http.StatusOK, map[string]any{"number": 7, "draft": false, "html_url": "https://github.com/acme/rocket/pull/7"}), nil
		case "/repos/acme/engine/pulls/9":
			return jsonResp(t, http.StatusOK, map[string]any{"number": 9, "draft": false, "html_url": "https://github.com/acme/engine/pull/9"}), nil
		case "/repos/acme/rocket/issues/42":
			return jsonResp(t, http.StatusOK, map[string]any{"number": 42}), nil
		default:
			return jsonResp(t, http.StatusOK, map[string]any{}), nil
		}
	}), func(context.Context) (string, error) { return "token", nil })
	if err != nil {
		t.Fatalf("newWithDeps: %v", err)
	}
	payload := `{"workspace_id":"ws-1","work_item_id":"wi-1","branch":"sub-branch","external_id":"gh:issue:acme/rocket#42"}`
	if err := a.OnEvent(context.Background(), domain.SystemEvent{EventType: string(domain.EventWorkItemCompleted), Payload: payload}); err != nil {
		t.Fatalf("OnEvent: %v", err)
	}
	seenRocket, seenEngine := false, false
	for _, req := range requests {
		if req == "PATCH /repos/acme/rocket/pulls/7" {
			seenRocket = true
		}
		if req == "PATCH /repos/acme/engine/pulls/9" {
			seenEngine = true
		}
	}
	if !seenRocket || !seenEngine {
		t.Fatalf("requests = %v, want both repo pull updates", requests)
	}
}

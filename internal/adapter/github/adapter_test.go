package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/repository"
	"github.com/beeemT/substrate/internal/service"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) Do(req *http.Request) (*http.Response, error) { return f(req) }

func jsonResp(t *testing.T, status int, v any) *http.Response {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}

	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(string(b)))}
}

func newTestAdapter(t *testing.T, rt roundTripFunc) *GithubAdapter {
	t.Helper()
	a, err := newWithDeps(context.Background(), config.GithubConfig{PollInterval: "10ms", StateMappings: map[string]string{"in_progress": "open", "in_review": "open", "done": "closed"}}, adapter.ReviewArtifactRepos{}, rt, func(context.Context) (string, error) { return "token-from-gh", nil })
	if err != nil {
		t.Fatalf("newWithDeps: %v", err)
	}

	return a
}

func TestNewWithDeps_UsesConfiguredBaseURL(t *testing.T) {
	var seenHost, seenPath string
	a, err := newWithDeps(context.Background(), config.GithubConfig{BaseURL: "https://github.internal/api/v3"}, adapter.ReviewArtifactRepos{}, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		seenHost = req.URL.Host
		seenPath = req.URL.Path

		return jsonResp(t, http.StatusOK, map[string]any{"login": "alice"}), nil
	}), func(context.Context) (string, error) { return "token-from-gh", nil })
	if err != nil {
		t.Fatalf("newWithDeps: %v", err)
	}
	if a.baseURL != "https://github.internal/api/v3" {
		t.Fatalf("baseURL = %q, want configured enterprise URL", a.baseURL)
	}
	if seenHost != "github.internal" || seenPath != "/api/v3/user" {
		t.Fatalf("viewer request = %s%s, want https://github.internal/api/v3/user", seenHost, seenPath)
	}
}

func TestTokenFallbackAndDefaultBranchFallback(t *testing.T) {
	resolved := false
	a, err := newWithDeps(context.Background(), config.GithubConfig{}, adapter.ReviewArtifactRepos{}, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/repos/acme/rocket":
			return jsonResp(t, http.StatusUnauthorized, map[string]any{"message": "nope"}), nil
		case "/user":
			return jsonResp(t, http.StatusOK, map[string]any{"login": "alice"}), nil
		default:
			return jsonResp(t, http.StatusOK, map[string]any{}), nil
		}
	}), func(context.Context) (string, error) {
		resolved = true

		return "resolved-token", nil
	})
	if err != nil {
		t.Fatalf("newWithDeps: %v", err)
	}
	if !resolved {
		t.Fatal("expected token resolver to be called")
	}
	if a.token != "resolved-token" {
		t.Fatalf("token = %q", a.token)
	}
	if a.defaultBranch != "main" {
		t.Fatalf("defaultBranch = %q, want main", a.defaultBranch)
	}
}

func TestCreatedByMeUsesViewerLoginWhenAssigneeConfigured(t *testing.T) {
	userCalls := 0
	var issueQuery string
	a, err := newWithDeps(context.Background(), config.GithubConfig{Assignee: "bob"}, adapter.ReviewArtifactRepos{}, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/user":
			userCalls++

			return jsonResp(t, http.StatusOK, map[string]any{"login": "alice"}), nil
		case "/search/issues":
			issueQuery = req.URL.RawQuery

			return jsonResp(t, http.StatusOK, map[string]any{"items": []any{}}), nil
		default:
			t.Fatalf("unexpected request: %s", req.URL.Path)

			return nil, nil
		}
	}), func(context.Context) (string, error) { return "token-from-gh", nil })
	if err != nil {
		t.Fatalf("newWithDeps: %v", err)
	}
	if a.assignee != "bob" {
		t.Fatalf("assignee = %q, want bob", a.assignee)
	}
	if a.viewer != "alice" {
		t.Fatalf("viewer = %q, want alice", a.viewer)
	}
	_, err = a.ListSelectable(context.Background(), adapter.ListOpts{Scope: domain.ScopeIssues, View: "created_by_me"})
	if err != nil {
		t.Fatalf("ListSelectable: %v", err)
	}
	if userCalls != 1 {
		t.Fatalf("/user calls = %d, want 1 cached lookup", userCalls)
	}
	values, err := url.ParseQuery(issueQuery)
	if err != nil {
		t.Fatalf("ParseQuery: %v", err)
	}
	q := values.Get("q")
	if !strings.Contains(q, "author:alice") || strings.Contains(q, "author:bob") {
		t.Fatalf("search query = %q, want viewer author only", q)
	}
}

func TestParsePollInterval(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want time.Duration
	}{
		{name: "empty falls back to default", raw: "", want: 5 * time.Minute},
		{name: "invalid falls back to default", raw: "not-a-duration", want: 5 * time.Minute},
		{name: "below floor clamps", raw: "5s", want: 60 * time.Second},
		{name: "above floor unchanged", raw: "90s", want: 90 * time.Second},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := parsePollInterval(tc.raw); got != tc.want {
				t.Fatalf("parsePollInterval(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}

func TestParseExternalID(t *testing.T) {
	t.Run("plain owner and repo", func(t *testing.T) {
		owner, repo, n, err := parseExternalID("gh:issue:acme/rocket#42")
		if err != nil {
			t.Fatalf("parseExternalID: %v", err)
		}
		if owner != "acme" || repo != "rocket" || n != 42 {
			t.Fatalf("parsed = %s/%s#%d, want acme/rocket#42", owner, repo, n)
		}
	})

	t.Run("hyphenated owner and repo", func(t *testing.T) {
		owner, repo, n, err := parseExternalID("gh:issue:acme-inc/rocket-app#42")
		if err != nil {
			t.Fatalf("parseExternalID: %v", err)
		}
		if owner != "acme-inc" || repo != "rocket-app" || n != 42 {
			t.Fatalf("parsed = %s/%s#%d, want acme-inc/rocket-app#42", owner, repo, n)
		}
	})

	t.Run("invalid", func(t *testing.T) {
		_, _, _, err := parseExternalID("gh:issue:other")
		if err == nil {
			t.Fatal("expected invalid external id error")
		}
	})
}

func TestScopeInitiativesUnsupported(t *testing.T) {
	a := newTestAdapter(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/repos/acme/rocket":
			return jsonResp(t, http.StatusOK, map[string]any{"default_branch": "main"}), nil
		case "/user":
			return jsonResp(t, http.StatusOK, map[string]any{"login": "alice"}), nil
		default:
			return jsonResp(t, http.StatusOK, map[string]any{}), nil
		}
	}))
	_, err := a.ListSelectable(context.Background(), adapter.ListOpts{Scope: domain.ScopeInitiatives})
	if err != adapter.ErrBrowseNotSupported {
		t.Fatalf("err = %v, want ErrBrowseNotSupported", err)
	}
}

func TestLifecycleCreateAndReady(t *testing.T) {
	requests := make([]string, 0, 6)
	pullLookups := 0
	a := newTestAdapter(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests = append(requests, req.Method+" "+req.URL.Path+"?"+req.URL.RawQuery)
		switch {
		case req.URL.Path == "/user":
			return jsonResp(t, http.StatusOK, map[string]any{"login": "alice"}), nil
		case req.URL.Path == "/repos/acme/rocket/pulls" && req.Method == http.MethodGet && strings.Contains(req.URL.RawQuery, "head=acme%3Asub-branch") && strings.Contains(req.URL.RawQuery, "base=develop"):
			pullLookups++
			if pullLookups == 1 {
				return jsonResp(t, http.StatusOK, []any{}), nil
			}

			return jsonResp(t, http.StatusOK, []any{map[string]any{"number": 7, "draft": true, "html_url": "https://github.com/acme/rocket/pull/7"}}), nil
		case req.URL.Path == "/repos/acme/rocket/pulls" && req.Method == http.MethodPost:
			return jsonResp(t, http.StatusCreated, map[string]any{"number": 7, "draft": true, "html_url": "https://github.com/acme/rocket/pull/7"}), nil
		case req.URL.Path == "/repos/acme/rocket/pulls/7" && req.Method == http.MethodPatch:
			return jsonResp(t, http.StatusOK, map[string]any{"number": 7, "draft": false}), nil
		case req.URL.Path == "/repos/acme/rocket/issues/42" && req.Method == http.MethodPatch:
			return jsonResp(t, http.StatusOK, map[string]any{"number": 42}), nil
		default:
			return jsonResp(t, http.StatusOK, map[string]any{}), nil
		}
	}))
	createPayload := `{"branch":"sub-branch","work_item_title":"Feature title","sub_plan":"Repo specific implementation plan","review":{"base_repo":{"provider":"github","owner":"acme","repo":"rocket"},"head_repo":{"provider":"github","owner":"acme","repo":"rocket"},"base_branch":"develop","head_branch":"sub-branch"}}`
	if err := a.OnEvent(context.Background(), domain.SystemEvent{EventType: string(domain.EventWorktreeCreated), Payload: createPayload}); err != nil {
		t.Fatalf("worktree created: %v", err)
	}
	completePayload := `{"branch":"sub-branch","external_id":"gh:issue:acme/rocket#42","review":{"base_repo":{"provider":"github","owner":"acme","repo":"rocket"},"head_repo":{"provider":"github","owner":"acme","repo":"rocket"},"base_branch":"develop","head_branch":"sub-branch"}}`
	if err := a.OnEvent(context.Background(), domain.SystemEvent{EventType: string(domain.EventWorkItemCompleted), Payload: completePayload}); err != nil {
		t.Fatalf("work item completed: %v", err)
	}
	seenCreate, seenReady := false, false
	for _, req := range requests {
		if strings.HasPrefix(req, "POST /repos/acme/rocket/pulls?") {
			seenCreate = true
		}
		if strings.HasPrefix(req, "PATCH /repos/acme/rocket/pulls/7?") {
			seenReady = true
		}
	}
	if !seenCreate || !seenReady {
		t.Fatalf("requests missing create/ready flow: %v", requests)
	}
}

func TestListIssuesUsesIssueSearchForCreatedByMeAndPreservesRepositoryMetadata(t *testing.T) {
	var issueQuery string
	var issuePath string
	a := newTestAdapter(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/user":
			return jsonResp(t, http.StatusOK, map[string]any{"login": "alice"}), nil
		case "/search/issues":
			issuePath = req.URL.Path
			issueQuery = req.URL.RawQuery

			return jsonResp(t, http.StatusOK, map[string]any{"items": []any{
				map[string]any{"number": 7, "title": "Shared bug", "state": "closed", "labels": []any{map[string]any{"name": "bug"}}, "body": "body", "html_url": "https://github.com/other/engine/issues/7", "repository_url": "https://api.github.com/repos/other/engine"},
				map[string]any{"number": 8, "title": "PR", "state": "open", "labels": []any{}, "pull_request": map[string]any{}, "html_url": "https://github.com/acme/rocket/pull/8", "repository_url": "https://api.github.com/repos/acme/rocket"},
			}}), nil
		default:
			t.Fatalf("unexpected request: %s", req.URL.Path)

			return nil, nil
		}
	}))
	res, err := a.ListSelectable(context.Background(), adapter.ListOpts{Scope: domain.ScopeIssues, Search: "memory leak", Labels: []string{"bug"}, Limit: 25, View: "created_by_me", State: "closed", Owner: "other", Repo: "engine"})
	if err != nil {
		t.Fatalf("ListSelectable: %v", err)
	}
	if issuePath != "/search/issues" {
		t.Fatalf("issue path = %q, want /search/issues", issuePath)
	}
	values, err := url.ParseQuery(issueQuery)
	if err != nil {
		t.Fatalf("ParseQuery: %v", err)
	}
	q := values.Get("q")
	for _, want := range []string{"is:issue", "author:alice", "state:closed", "repo:other/engine", `label:"bug"`, "memory leak"} {
		if !strings.Contains(q, want) {
			t.Fatalf("search query = %q, want %q", q, want)
		}
	}
	if values.Get("per_page") != "25" {
		t.Fatalf("per_page = %q, want 25", values.Get("per_page"))
	}
	if len(res.Items) != 1 {
		t.Fatalf("items = %+v, want 1 issue", res.Items)
	}
	item := res.Items[0]
	if item.ID != "other/engine#7" {
		t.Fatalf("item ID = %q, want other/engine#7", item.ID)
	}
	if item.Title != "[other/engine] #7: Shared bug" {
		t.Fatalf("item title = %q", item.Title)
	}
	if item.URL != "https://github.com/other/engine/issues/7" {
		t.Fatalf("item URL = %q, want issue HTML URL", item.URL)
	}
	if item.ParentRef == nil || item.ParentRef.ID != "other/engine" || item.ParentRef.Type != "repository" {
		t.Fatalf("parent ref = %+v, want repository other/engine", item.ParentRef)
	}
}

func TestListMilestonesRemainRepoScoped(t *testing.T) {
	a := newTestAdapter(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/user":
			return jsonResp(t, http.StatusOK, map[string]any{"login": "alice"}), nil
		case "/repos/acme/rocket/milestones":
			return jsonResp(t, http.StatusOK, []any{map[string]any{"number": 3, "title": "v1", "description": "repo milestone", "state": "open"}}), nil
		default:
			return jsonResp(t, http.StatusOK, map[string]any{}), nil
		}
	}))
	res, err := a.ListSelectable(context.Background(), adapter.ListOpts{Scope: domain.ScopeProjects, Owner: "acme", Repo: "rocket"})
	if err != nil {
		t.Fatalf("ListSelectable milestones: %v", err)
	}
	if len(res.Items) != 1 || res.Items[0].Title != "v1 (repo milestone)" {
		t.Fatalf("milestone items = %+v, want explicit repo-scoped title", res.Items)
	}
}

func TestResolveIssuePreservesRepositoryTrackerRefs(t *testing.T) {
	a := newTestAdapter(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/repos/acme/rocket":
			return jsonResp(t, http.StatusOK, map[string]any{"default_branch": "main"}), nil
		case "/user":
			return jsonResp(t, http.StatusOK, map[string]any{"login": "alice"}), nil
		case "/repos/other/engine/issues/42":
			return jsonResp(t, http.StatusOK, map[string]any{"number": 42, "title": "Cross repo issue", "state": "open", "labels": []any{}, "body": "body", "html_url": "https://github.com/other/engine/issues/42", "repository": map[string]any{"full_name": "other/engine", "owner": map[string]any{"login": "other"}, "name": "engine"}}), nil
		default:
			return jsonResp(t, http.StatusOK, map[string]any{}), nil
		}
	}))
	item, err := a.Resolve(context.Background(), adapter.Selection{Scope: domain.ScopeIssues, ItemIDs: []string{"other/engine#42"}})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if item.ExternalID != "gh:issue:other/engine#42" {
		t.Fatalf("external ID = %q, want gh:issue:other/engine#42", item.ExternalID)
	}
	if len(item.SourceItemIDs) != 1 || item.SourceItemIDs[0] != "other/engine#42" {
		t.Fatalf("source item ids = %#v, want repo-qualified id", item.SourceItemIDs)
	}
	refs, ok := item.Metadata["tracker_refs"].([]domain.TrackerReference)
	if !ok || len(refs) != 1 {
		t.Fatalf("tracker_refs = %#v, want 1 typed ref", item.Metadata["tracker_refs"])
	}
	if refs[0].Owner != "other" || refs[0].Repo != "engine" || refs[0].Number != 42 {
		t.Fatalf("tracker ref = %+v, want other/engine#42", refs[0])
	}
}

func TestPlanApprovedAddsComments(t *testing.T) {
	var commentPaths []string
	a := newTestAdapter(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/repos/acme/rocket":
			return jsonResp(t, http.StatusOK, map[string]any{"default_branch": "main"}), nil
		case "/user":
			return jsonResp(t, http.StatusOK, map[string]any{"login": "alice"}), nil
		case "/repos/acme/rocket/issues/42/comments", "/repos/acme/rocket/issues/43/comments":
			commentPaths = append(commentPaths, req.URL.Path)

			return jsonResp(t, http.StatusCreated, map[string]any{"id": 1}), nil
		case "/repos/acme/rocket/issues/42":
			return jsonResp(t, http.StatusOK, map[string]any{"number": 42, "title": "Issue", "state": "open", "labels": []any{}, "body": "body"}), nil
		default:
			return jsonResp(t, http.StatusOK, map[string]any{}), nil
		}
	}))
	if err := a.OnEvent(context.Background(), domain.SystemEvent{EventType: string(domain.EventPlanApproved), Payload: `{"external_id":"gh:issue:acme/rocket#42","comment_body":"Overall plan text","external_ids":["gh:issue:acme/rocket#42","gh:issue:acme/rocket#43"]}`}); err != nil {
		t.Fatalf("plan approved: %v", err)
	}
	if len(commentPaths) != 2 {
		t.Fatalf("comment paths = %v, want 2 comments", commentPaths)
	}
}

func TestLifecycleCreateAddsGitHubResolvesFooter(t *testing.T) {
	var createBody string
	a := newTestAdapter(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.URL.Path == "/user":
			return jsonResp(t, http.StatusOK, map[string]any{"login": "alice"}), nil
		case req.URL.Path == "/repos/acme/rocket/pulls" && req.Method == http.MethodGet:
			return jsonResp(t, http.StatusOK, []any{}), nil
		case req.URL.Path == "/repos/acme/rocket/pulls" && req.Method == http.MethodPost:
			payload, _ := io.ReadAll(req.Body)
			createBody = string(payload)

			return jsonResp(t, http.StatusCreated, map[string]any{"number": 7, "draft": true}), nil
		default:
			return jsonResp(t, http.StatusOK, map[string]any{}), nil
		}
	}))
	payload := `{"branch":"sub-branch","work_item_title":"Feature title","sub_plan":"Repo specific implementation plan","tracker_refs":[{"provider":"github","kind":"issue","id":"40","owner":"acme","repo":"rocket","number":40}],"review":{"base_repo":{"provider":"github","owner":"acme","repo":"rocket"},"head_repo":{"provider":"github","owner":"acme","repo":"rocket"},"base_branch":"main","head_branch":"sub-branch"}}`
	if err := a.OnEvent(context.Background(), domain.SystemEvent{EventType: string(domain.EventWorktreeCreated), Payload: payload}); err != nil {
		t.Fatalf("worktree created: %v", err)
	}
	if !strings.Contains(createBody, `"body":"Repo specific implementation plan\n\nResolves #40"`) {
		t.Fatalf("create body = %s, want resolves footer", createBody)
	}
}

func TestLifecycleCreateAddsLinearResolvesFooter(t *testing.T) {
	var createBody string
	a := newTestAdapter(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.URL.Path == "/user":
			return jsonResp(t, http.StatusOK, map[string]any{"login": "alice"}), nil
		case req.URL.Path == "/repos/acme/rocket/pulls" && req.Method == http.MethodGet:
			return jsonResp(t, http.StatusOK, []any{}), nil
		case req.URL.Path == "/repos/acme/rocket/pulls" && req.Method == http.MethodPost:
			payload, _ := io.ReadAll(req.Body)
			createBody = string(payload)

			return jsonResp(t, http.StatusCreated, map[string]any{"number": 7, "draft": true}), nil
		default:
			return jsonResp(t, http.StatusOK, map[string]any{}), nil
		}
	}))
	payload := `{"branch":"sub-branch","work_item_title":"Feature title","sub_plan":"Repo specific implementation plan","tracker_refs":[{"provider":"linear","kind":"issue","id":"FOO-123","url":"https://linear.app/acme/issue/FOO-123"}],"review":{"base_repo":{"provider":"github","owner":"acme","repo":"rocket"},"head_repo":{"provider":"github","owner":"acme","repo":"rocket"},"base_branch":"main","head_branch":"sub-branch"}}`
	if err := a.OnEvent(context.Background(), domain.SystemEvent{EventType: string(domain.EventWorktreeCreated), Payload: payload}); err != nil {
		t.Fatalf("worktree created: %v", err)
	}
	if !strings.Contains(createBody, `Resolves [FOO-123](https://linear.app/acme/issue/FOO-123)`) {
		t.Fatalf("create body = %s, want linear resolves footer", createBody)
	}
}

func TestListIssuesRejectsUnsupportedNormalizedView(t *testing.T) {
	a := newTestAdapter(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/repos/acme/rocket":
			return jsonResp(t, http.StatusOK, map[string]any{"default_branch": "main"}), nil
		case "/user":
			return jsonResp(t, http.StatusOK, map[string]any{"login": "alice"}), nil
		default:
			t.Fatalf("unexpected request: %s", req.URL.Path)

			return nil, nil
		}
	}))
	_, err := a.ListSelectable(context.Background(), adapter.ListOpts{Scope: domain.ScopeIssues, View: "bogus"})
	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("err = %v, want unsupported view error", err)
	}
}

func TestListIssuesFiltersByLabels(t *testing.T) {
	a := newTestAdapter(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/repos/acme/rocket":
			return jsonResp(t, http.StatusOK, map[string]any{"default_branch": "main"}), nil
		case "/user":
			return jsonResp(t, http.StatusOK, map[string]any{"login": "alice"}), nil
		case "/issues":
			return jsonResp(t, http.StatusOK, []any{
				map[string]any{"number": 7, "title": "Bug A", "state": "open", "labels": []any{map[string]any{"name": "bug"}, map[string]any{"name": "backend"}}, "body": "body", "repository": map[string]any{"full_name": "other/engine", "owner": map[string]any{"login": "other"}, "name": "engine"}},
				map[string]any{"number": 8, "title": "Bug B", "state": "open", "labels": []any{map[string]any{"name": "bug"}}, "body": "body", "repository": map[string]any{"full_name": "other/engine", "owner": map[string]any{"login": "other"}, "name": "engine"}},
			}), nil
		default:
			return jsonResp(t, http.StatusOK, map[string]any{}), nil
		}
	}))
	res, err := a.ListSelectable(context.Background(), adapter.ListOpts{Scope: domain.ScopeIssues, Labels: []string{"bug", "backend"}})
	if err != nil {
		t.Fatalf("ListSelectable: %v", err)
	}
	if len(res.Items) != 1 || res.Items[0].ID != "other/engine#7" {
		t.Fatalf("items = %+v, want only bug+backend issue", res.Items)
	}
}

func TestWorkItemCompletedTransitionsToInReview(t *testing.T) {
	var patchBody []byte
	patchFired := false
	a := newTestAdapter(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.URL.Path == "/user":
			return jsonResp(t, http.StatusOK, map[string]any{"login": "alice"}), nil
		case req.URL.Path == "/repos/acme/rocket":
			return jsonResp(t, http.StatusOK, map[string]any{"default_branch": "main"}), nil
		case req.URL.Path == "/repos/acme/rocket/issues/42" && req.Method == http.MethodPatch:
			patchFired = true
			var err error
			patchBody, err = io.ReadAll(req.Body)
			if err != nil {
				t.Fatalf("reading patch body: %v", err)
			}
			return jsonResp(t, http.StatusOK, map[string]any{"number": 42}), nil
		default:
			return jsonResp(t, http.StatusOK, map[string]any{}), nil
		}
	}))
	completePayload := `{"external_id":"gh:issue:acme/rocket#42"}`
	if err := a.OnEvent(context.Background(), domain.SystemEvent{EventType: string(domain.EventWorkItemCompleted), Payload: completePayload}); err != nil {
		t.Fatalf("OnEvent: %v", err)
	}
	if !patchFired {
		t.Fatal("expected PATCH /repos/acme/rocket/issues/42 to fire, but it did not")
	}
	body := string(patchBody)
	if !strings.Contains(body, "open") {
		t.Fatalf("patch body = %q, want to contain \"open\" (in_review mapping)", body)
	}
	if strings.Contains(body, "closed") {
		t.Fatalf("patch body = %q, must not contain \"closed\" (done mapping)", body)
	}
}

func TestWorkItemCompleted_GitLabProvider_IsIgnored(t *testing.T) {
	// The GitHub adapter must not attempt any GitHub API calls when the
	// EventWorkItemCompleted payload names a GitLab-hosted repo.
	apiCalled := false
	a := newTestAdapter(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/user":
			return jsonResp(t, http.StatusOK, map[string]any{"login": "alice"}), nil
		default:
			apiCalled = true
			return jsonResp(t, http.StatusNotFound, map[string]any{"message": "Not Found", "status": "404"}), nil
		}
	}))
	payload, _ := json.Marshal(map[string]any{
		"external_id":  "gl:issue:org/project#7",
		"branch":       "sub-gl-7-feature",
		"work_item_id": "wi-123",
		"workspace_id": "ws-local",
		"review": map[string]any{
			"base_repo": map[string]any{
				"provider": "gitlab",
				"owner":    "org",
				"repo":     "project",
			},
		},
	})
	if err := a.OnEvent(context.Background(), domain.SystemEvent{
		EventType: string(domain.EventWorkItemCompleted),
		Payload:   string(payload),
	}); err != nil {
		t.Fatalf("OnEvent: %v", err)
	}
	if apiCalled {
		t.Fatal("GitHub API was called for a GitLab-hosted work item; adapter must skip cross-platform events")
	}
}

func TestLifecycleAppliesReviewersAndLabels(t *testing.T) {
	var requestedReviewers []string
	var appliedLabels []string
	pullCreated := false

	rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.URL.Path == "/user":
			return jsonResp(t, http.StatusOK, map[string]any{"login": "alice"}), nil
		case req.URL.Path == "/repos/acme/rocket/pulls" && req.Method == http.MethodGet:
			return jsonResp(t, http.StatusOK, []any{}), nil
		case req.URL.Path == "/repos/acme/rocket/pulls" && req.Method == http.MethodPost:
			pullCreated = true
			return jsonResp(t, http.StatusCreated, map[string]any{"number": 9, "draft": true, "html_url": "https://github.com/acme/rocket/pull/9"}), nil
		case req.URL.Path == "/repos/acme/rocket/pulls/9/requested_reviewers" && req.Method == http.MethodPost:
			var body map[string]any
			data, _ := io.ReadAll(req.Body)
			_ = json.Unmarshal(data, &body)
			if raw, ok := body["reviewers"]; ok {
				if arr, ok := raw.([]any); ok {
					for _, r := range arr {
						requestedReviewers = append(requestedReviewers, r.(string))
					}
				}
			}
			return jsonResp(t, http.StatusCreated, map[string]any{}), nil
		case req.URL.Path == "/repos/acme/rocket/issues/9/labels" && req.Method == http.MethodPost:
			var body map[string]any
			data, _ := io.ReadAll(req.Body)
			_ = json.Unmarshal(data, &body)
			if raw, ok := body["labels"]; ok {
				if arr, ok := raw.([]any); ok {
					for _, l := range arr {
						appliedLabels = append(appliedLabels, l.(string))
					}
				}
			}
			return jsonResp(t, http.StatusOK, []any{}), nil
		default:
			return jsonResp(t, http.StatusOK, map[string]any{}), nil
		}
	})

	a, err := newWithDeps(
		context.Background(),
		config.GithubConfig{
			Reviewers:     []string{"bob", "carol"},
			Labels:        []string{"needs-review"},
			StateMappings: map[string]string{"in_progress": "open", "done": "closed"},
		},
		adapter.ReviewArtifactRepos{},
		rt,
		func(context.Context) (string, error) { return "tok", nil },
	)
	if err != nil {
		t.Fatalf("newWithDeps: %v", err)
	}

	payload := `{"branch":"feat/x","work_item_title":"Feature","review":{"base_repo":{"provider":"github","owner":"acme","repo":"rocket"},"head_repo":{"provider":"github","owner":"acme","repo":"rocket"},"base_branch":"main","head_branch":"feat/x"}}`
	if err := a.OnEvent(context.Background(), domain.SystemEvent{EventType: string(domain.EventWorktreeCreated), Payload: payload}); err != nil {
		t.Fatalf("OnEvent worktree created: %v", err)
	}

	if !pullCreated {
		t.Fatal("expected draft PR to be created")
	}
	if len(requestedReviewers) != 2 || requestedReviewers[0] != "bob" || requestedReviewers[1] != "carol" {
		t.Fatalf("requestedReviewers = %v, want [bob carol]", requestedReviewers)
	}
	if len(appliedLabels) != 1 || appliedLabels[0] != "needs-review" {
		t.Fatalf("appliedLabels = %v, want [needs-review]", appliedLabels)
	}
}

func TestOnEvent_IgnoresForeignExternalID_PlanApproved(t *testing.T) {
	// The adapter must NOT make any HTTP calls and must return nil
	// when plan.approved carries a non-gh: external_id.
	a := newTestAdapter(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/user" {
			return jsonResp(t, http.StatusOK, map[string]any{"login": "alice"}), nil
		}
		t.Fatalf("unexpected HTTP request to %s", req.URL.Path)
		return nil, nil
	}))
	for _, foreignID := range []string{"gl:issue:83#4796", "LIN-TEAM-42", "SEN-acme-12345"} {
		payload := `{"external_id":"` + foreignID + `"}`
		if err := a.OnEvent(context.Background(), domain.SystemEvent{
			EventType: string(domain.EventPlanApproved),
			Payload:   payload,
		}); err != nil {
			t.Errorf("OnEvent(plan.approved, %q): got error %v, want nil", foreignID, err)
		}
	}
}

func TestOnEvent_IgnoresForeignExternalID_WorkItemCompleted(t *testing.T) {
	a := newTestAdapter(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/user" {
			return jsonResp(t, http.StatusOK, map[string]any{"login": "alice"}), nil
		}
		t.Fatalf("unexpected HTTP request to %s", req.URL.Path)
		return nil, nil
	}))
	for _, foreignID := range []string{"gl:issue:83#4796", "LIN-TEAM-42"} {
		payload := `{"external_id":"` + foreignID + `"}`
		if err := a.OnEvent(context.Background(), domain.SystemEvent{
			EventType: string(domain.EventWorkItemCompleted),
			Payload:   payload,
		}); err != nil {
			t.Errorf("OnEvent(work_item.completed, %q): got error %v, want nil", foreignID, err)
		}
	}
}

// TestListIssuesFiltersInboxByRepoPathWithoutOwner verifies that the repo filter
// works when opts.Repo is supplied as "owner/repo" without a separate opts.Owner.
// Previously the client-side filterGitHubIssuesByContainer call was gated on
// opts.Owner != "", silently ignoring the repo filter.
func TestListIssuesFiltersInboxByRepoPathWithoutOwner(t *testing.T) {
	a := newTestAdapter(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/user":
			return jsonResp(t, http.StatusOK, map[string]any{"login": "alice"}), nil
		case "/issues":
			return jsonResp(t, http.StatusOK, []any{
				// Issue from the target repo — must be kept.
				map[string]any{"number": 1, "title": "Keep me", "state": "open", "labels": []any{}, "body": "", "html_url": "https://github.com/acme/rocket/issues/1", "repository": map[string]any{"full_name": "acme/rocket", "owner": map[string]any{"login": "acme"}, "name": "rocket"}},
				// Issue from a different repo — must be filtered out.
				map[string]any{"number": 2, "title": "Drop me", "state": "open", "labels": []any{}, "body": "", "html_url": "https://github.com/other/engine/issues/2", "repository": map[string]any{"full_name": "other/engine", "owner": map[string]any{"login": "other"}, "name": "engine"}},
			}), nil
		default:
			t.Fatalf("unexpected request: %s", req.URL.Path)
			return nil, nil
		}
	}))
	res, err := a.ListSelectable(context.Background(), adapter.ListOpts{
		Scope: domain.ScopeIssues,
		Repo:  "acme/rocket", // no Owner field — the overlay sets Repo as "owner/repo"
	})
	if err != nil {
		t.Fatalf("ListSelectable: %v", err)
	}
	if len(res.Items) != 1 {
		t.Fatalf("items = %+v, want 1 item (acme/rocket only)", res.Items)
	}
	if res.Items[0].ID != "acme/rocket#1" {
		t.Fatalf("item ID = %q, want acme/rocket#1", res.Items[0].ID)
	}
}

// TestListCreatedIssuesFiltersSearchByRepoPathWithoutOwner verifies that the
// created_by_me view appends a repo: search term when opts.Repo is supplied as
// "owner/repo" without a separate opts.Owner.
func TestListCreatedIssuesFiltersSearchByRepoPathWithoutOwner(t *testing.T) {
	var searchQuery string
	a := newTestAdapter(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/user":
			return jsonResp(t, http.StatusOK, map[string]any{"login": "alice"}), nil
		case "/search/issues":
			searchQuery = req.URL.Query().Get("q")
			// Return the target-repo issue and a foreign-repo issue; client-side
			// filtering must discard the latter even though the search term already
			// constrains the API result set.
			return jsonResp(t, http.StatusOK, map[string]any{"items": []any{
				map[string]any{"number": 3, "title": "My fix", "state": "open", "labels": []any{}, "body": "", "html_url": "https://github.com/acme/rocket/issues/3", "repository_url": "https://api.github.com/repos/acme/rocket"},
			}}), nil
		default:
			t.Fatalf("unexpected request: %s", req.URL.Path)
			return nil, nil
		}
	}))
	res, err := a.ListSelectable(context.Background(), adapter.ListOpts{
		Scope: domain.ScopeIssues,
		View:  "created_by_me",
		Repo:  "acme/rocket", // no Owner field
	})
	if err != nil {
		t.Fatalf("ListSelectable: %v", err)
	}
	if !strings.Contains(searchQuery, "repo:acme/rocket") {
		t.Fatalf("search query = %q, want repo:acme/rocket qualifier", searchQuery)
	}
	if len(res.Items) != 1 || res.Items[0].ID != "acme/rocket#3" {
		t.Fatalf("items = %+v, want acme/rocket#3 only", res.Items)
	}
}


// --- In-memory repos for PR description sync tests ---

type inMemGithubPRRepo struct {
	prs map[string]domain.GithubPullRequest
}

func (r *inMemGithubPRRepo) Upsert(_ context.Context, pr domain.GithubPullRequest) error {
	r.prs[pr.ID] = pr
	return nil
}

func (r *inMemGithubPRRepo) Get(_ context.Context, id string) (domain.GithubPullRequest, error) {
	pr, ok := r.prs[id]
	if !ok {
		return domain.GithubPullRequest{}, fmt.Errorf("pr %q not found", id)
	}
	return pr, nil
}

func (r *inMemGithubPRRepo) GetByNumber(_ context.Context, owner, repo string, number int) (domain.GithubPullRequest, error) {
	for _, pr := range r.prs {
		if pr.Owner == owner && pr.Repo == repo && pr.Number == number {
			return pr, nil
		}
	}
	return domain.GithubPullRequest{}, fmt.Errorf("pr %s/%s#%d not found", owner, repo, number)
}

func (r *inMemGithubPRRepo) ListByWorkspaceID(_ context.Context, _ string) ([]domain.GithubPullRequest, error) {
	out := make([]domain.GithubPullRequest, 0, len(r.prs))
	for _, pr := range r.prs {
		out = append(out, pr)
	}
	return out, nil
}

func (r *inMemGithubPRRepo) ListNonTerminal(_ context.Context, _ string) ([]domain.GithubPullRequest, error) {
	var out []domain.GithubPullRequest
	for _, pr := range r.prs {
		if pr.State != "merged" && pr.State != "closed" {
			out = append(out, pr)
		}
	}
	return out, nil
}

type inMemArtifactLinkRepo struct {
	links []domain.SessionReviewArtifact
}

func (r *inMemArtifactLinkRepo) Upsert(_ context.Context, link domain.SessionReviewArtifact) error {
	r.links = append(r.links, link)
	return nil
}

func (r *inMemArtifactLinkRepo) ListByWorkItemID(_ context.Context, workItemID string) ([]domain.SessionReviewArtifact, error) {
	var out []domain.SessionReviewArtifact
	for _, l := range r.links {
		if l.WorkItemID == workItemID {
			out = append(out, l)
		}
	}
	return out, nil
}

func (r *inMemArtifactLinkRepo) ListByWorkspaceID(_ context.Context, workspaceID string) ([]domain.SessionReviewArtifact, error) {
	var out []domain.SessionReviewArtifact
	for _, l := range r.links {
		if l.WorkspaceID == workspaceID {
			out = append(out, l)
		}
	}
	return out, nil
}

// --- Test helpers for PR description sync ---

func newDescSyncAdapter(t *testing.T, repos adapter.ReviewArtifactRepos, rt roundTripFunc) *GithubAdapter {
	t.Helper()
	a, err := newWithDeps(context.Background(), config.GithubConfig{
		PollInterval:  "10ms",
		StateMappings: map[string]string{"in_progress": "open", "in_review": "open", "done": "closed"},
	}, repos, rt, func(context.Context) (string, error) { return "token-from-gh", nil })
	if err != nil {
		t.Fatalf("newWithDeps: %v", err)
	}
	return a
}

func TestSyncPRDescriptionsOnApproval_UpdatesOpenPRs(t *testing.T) {
	t.Parallel()

	prRepo := &inMemGithubPRRepo{prs: map[string]domain.GithubPullRequest{
		"pr-1": {ID: "pr-1", Owner: "acme", Repo: "rocket", Number: 42, State: "open"},
		"pr-2": {ID: "pr-2", Owner: "acme", Repo: "engine", Number: 43, State: "merged"},
	}}
	artifactRepo := &inMemArtifactLinkRepo{links: []domain.SessionReviewArtifact{
		{ID: "a1", WorkspaceID: "ws-1", WorkItemID: "wi-1", Provider: "github", ProviderArtifactID: "pr-1"},
		{ID: "a2", WorkspaceID: "ws-1", WorkItemID: "wi-1", Provider: "github", ProviderArtifactID: "pr-2"},
	}}

	type httpReq struct {
		method string
		path   string
		body   string
	}
	var captured []httpReq

	a := newDescSyncAdapter(t, adapter.ReviewArtifactRepos{
		GithubPRs:        service.NewGithubPRService(repository.NoopTransacter{Res: repository.Resources{GithubPRs: prRepo}}),
		SessionArtifacts: service.NewSessionReviewArtifactService(repository.NoopTransacter{Res: repository.Resources{SessionReviewArtifacts: artifactRepo}}),
	}, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		var bodyBytes []byte
		if req.Body != nil {
			bodyBytes, _ = io.ReadAll(req.Body)
		}
		captured = append(captured, httpReq{method: req.Method, path: req.URL.Path, body: string(bodyBytes)})
		switch req.URL.Path {
		case "/user":
			return jsonResp(t, http.StatusOK, map[string]any{"login": "alice"}), nil
		default:
			return jsonResp(t, http.StatusOK, map[string]any{}), nil
		}
	}))

	payload, _ := json.Marshal(map[string]any{
		"work_item_id": "wi-1",
		"comment_body": "Updated plan text",
		"external_id":  "gh:issue:acme/rocket#10",
		"external_ids": []string{"gh:issue:acme/rocket#10"},
	})

	err := a.OnEvent(context.Background(), domain.SystemEvent{
		EventType:   string(domain.EventPlanApproved),
		WorkspaceID: "ws-1",
		Payload:     string(payload),
	})
	if err != nil {
		t.Fatalf("OnEvent: %v", err)
	}

	var patchedOpen, patchedMerged bool
	var patchBody string
	for _, r := range captured {
		if r.method == http.MethodPatch && r.path == "/repos/acme/rocket/pulls/42" {
			patchedOpen = true
			patchBody = r.body
		}
		if r.method == http.MethodPatch && r.path == "/repos/acme/engine/pulls/43" {
			patchedMerged = true
		}
	}
	if !patchedOpen {
		t.Fatalf("expected PATCH /repos/acme/rocket/pulls/42, got requests: %+v", captured)
	}
	if patchedMerged {
		t.Fatalf("should not PATCH merged PR /repos/acme/engine/pulls/43, got requests: %+v", captured)
	}
	if !strings.Contains(patchBody, `"body":"Updated plan text"`) {
		t.Fatalf("PATCH body = %q, want comment body in body field", patchBody)
	}
}

func TestSyncPRDescriptionsOnApproval_SkipsEmptyCommentBody(t *testing.T) {
	t.Parallel()

	prRepo := &inMemGithubPRRepo{prs: map[string]domain.GithubPullRequest{
		"pr-1": {ID: "pr-1", Owner: "acme", Repo: "rocket", Number: 42, State: "open"},
	}}
	artifactRepo := &inMemArtifactLinkRepo{links: []domain.SessionReviewArtifact{
		{ID: "a1", WorkspaceID: "ws-1", WorkItemID: "wi-1", Provider: "github", ProviderArtifactID: "pr-1"},
	}}

	var requests []string

	a := newDescSyncAdapter(t, adapter.ReviewArtifactRepos{
		GithubPRs:        service.NewGithubPRService(repository.NoopTransacter{Res: repository.Resources{GithubPRs: prRepo}}),
		SessionArtifacts: service.NewSessionReviewArtifactService(repository.NoopTransacter{Res: repository.Resources{SessionReviewArtifacts: artifactRepo}}),
	}, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests = append(requests, req.Method+" "+req.URL.Path)
		switch req.URL.Path {
		case "/user":
			return jsonResp(t, http.StatusOK, map[string]any{"login": "alice"}), nil
		default:
			return jsonResp(t, http.StatusOK, map[string]any{}), nil
		}
	}))

	payload, _ := json.Marshal(map[string]any{
		"work_item_id": "wi-1",
		"comment_body": "",
		"external_id":  "gh:issue:acme/rocket#10",
		"external_ids": []string{"gh:issue:acme/rocket#10"},
	})

	err := a.OnEvent(context.Background(), domain.SystemEvent{
		EventType:   string(domain.EventPlanApproved),
		WorkspaceID: "ws-1",
		Payload:     string(payload),
	})
	if err != nil {
		t.Fatalf("OnEvent: %v", err)
	}

	for _, r := range requests {
		if strings.Contains(r, "pulls") {
			t.Fatalf("expected no PATCH to pulls, got: %v", requests)
		}
	}
}

func TestSyncPRDescriptionsOnApproval_WorkItemInstancePostsComments(t *testing.T) {
	t.Parallel()

	var requests []string

	a := newDescSyncAdapter(t, adapter.ReviewArtifactRepos{}, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests = append(requests, req.Method+" "+req.URL.Path)
		switch req.URL.Path {
		case "/user":
			return jsonResp(t, http.StatusOK, map[string]any{"login": "alice"}), nil
		default:
			return jsonResp(t, http.StatusOK, map[string]any{}), nil
		}
	}))

	payload, _ := json.Marshal(map[string]any{
		"comment_body": "Plan text",
		"external_id":  "gh:issue:acme/rocket#10",
		"external_ids": []string{"gh:issue:acme/rocket#10"},
	})

	err := a.OnEvent(context.Background(), domain.SystemEvent{
		EventType:   string(domain.EventPlanApproved),
		WorkspaceID: "ws-1",
		Payload:     string(payload),
	})
	if err != nil {
		t.Fatalf("OnEvent: %v", err)
	}

	var postedComment, patchedPull bool
	for _, r := range requests {
		if r == "POST /repos/acme/rocket/issues/10/comments" {
			postedComment = true
		}
		if strings.Contains(r, "PATCH") && strings.Contains(r, "pulls") {
			patchedPull = true
		}
	}
	if !postedComment {
		t.Fatalf("expected POST /repos/acme/rocket/issues/10/comments, got: %v", requests)
	}
	if patchedPull {
		t.Fatalf("work-item instance should not PATCH pulls, got: %v", requests)
	}
}
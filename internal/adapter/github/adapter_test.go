package github

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/domain"
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
	a, err := newWithDeps(context.Background(), config.GithubConfig{Owner: "acme", Repo: "rocket", PollInterval: "10ms", StateMappings: map[string]string{"in_progress": "open", "done": "closed"}}, rt, func(context.Context) (string, error) { return "token-from-gh", nil })
	if err != nil {
		t.Fatalf("newWithDeps: %v", err)
	}
	return a
}

func TestTokenFallbackAndDefaultBranchFallback(t *testing.T) {
	resolved := false
	a, err := newWithDeps(context.Background(), config.GithubConfig{Owner: "acme", Repo: "rocket"}, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/repos/acme/rocket":
			return jsonResp(t, http.StatusUnauthorized, map[string]any{"message": "nope"}), nil
		case "/user":
			return jsonResp(t, http.StatusOK, map[string]any{"login": "alice"}), nil
		default:
			return jsonResp(t, http.StatusOK, map[string]any{}), nil
		}
	}), func(context.Context) (string, error) { resolved = true; return "resolved-token", nil })
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

func TestParseExternalID(t *testing.T) {
	t.Run("plain owner and repo", func(t *testing.T) {
		n, err := parseExternalID("acme", "rocket", "GH-acme-rocket-42")
		if err != nil {
			t.Fatalf("parseExternalID: %v", err)
		}
		if n != 42 {
			t.Fatalf("number = %d, want 42", n)
		}
	})

	t.Run("hyphenated owner and repo", func(t *testing.T) {
		n, err := parseExternalID("acme-inc", "rocket-app", "GH-acme-inc-rocket-app-42")
		if err != nil {
			t.Fatalf("parseExternalID: %v", err)
		}
		if n != 42 {
			t.Fatalf("number = %d, want 42", n)
		}
	})

	t.Run("repo mismatch", func(t *testing.T) {
		_, err := parseExternalID("acme", "rocket", "GH-other-rocket-42")
		if err == nil {
			t.Fatal("expected mismatch error")
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
	var requests []string
	a := newTestAdapter(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests = append(requests, req.Method+" "+req.URL.Path+"?"+req.URL.RawQuery)
		switch {
		case req.URL.Path == "/repos/acme/rocket" && req.Method == http.MethodGet:
			return jsonResp(t, http.StatusOK, map[string]any{"default_branch": "develop"}), nil
		case req.URL.Path == "/user":
			return jsonResp(t, http.StatusOK, map[string]any{"login": "alice"}), nil
		case req.URL.Path == "/repos/acme/rocket/pulls" && req.Method == http.MethodGet && strings.Contains(req.URL.RawQuery, "head=acme%3Asub-branch"):
			return jsonResp(t, http.StatusOK, []any{}), nil
		case req.URL.Path == "/repos/acme/rocket/pulls" && req.Method == http.MethodPost:
			return jsonResp(t, http.StatusCreated, map[string]any{"number": 7, "draft": true}), nil
		case req.URL.Path == "/repos/acme/rocket/pulls/7" && req.Method == http.MethodPatch:
			return jsonResp(t, http.StatusOK, map[string]any{"number": 7, "draft": false}), nil
		default:
			return jsonResp(t, http.StatusOK, map[string]any{}), nil
		}
	}))
	if err := a.OnEvent(context.Background(), domain.SystemEvent{EventType: string(domain.EventWorktreeCreated), Payload: `{"branch":"sub-branch","work_item_title":"Feature title","sub_plan":"Repo specific implementation plan"}`}); err != nil {
		t.Fatalf("worktree created: %v", err)
	}
	if err := a.OnEvent(context.Background(), domain.SystemEvent{EventType: string(domain.EventWorkItemCompleted), Payload: `{"branch":"sub-branch","external_id":"GH-acme-rocket-42"}`}); err != nil {
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

func TestListIssuesFiltersPullRequests(t *testing.T) {
	a := newTestAdapter(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/repos/acme/rocket":
			return jsonResp(t, http.StatusOK, map[string]any{"default_branch": "main"}), nil
		case "/user":
			return jsonResp(t, http.StatusOK, map[string]any{"login": "alice"}), nil
		case "/repos/acme/rocket/issues":
			return jsonResp(t, http.StatusOK, []any{map[string]any{"number": 1, "title": "Issue", "state": "open", "labels": []any{}, "body": "body"}, map[string]any{"number": 2, "title": "PR", "state": "open", "labels": []any{}, "pull_request": map[string]any{}}}), nil
		default:
			return jsonResp(t, http.StatusOK, map[string]any{}), nil
		}
	}))
	res, err := a.ListSelectable(context.Background(), adapter.ListOpts{Scope: domain.ScopeIssues})
	if err != nil {
		t.Fatalf("ListSelectable: %v", err)
	}
	if len(res.Items) != 1 || res.Items[0].ID != "1" {
		t.Fatalf("items = %+v, want only issue #1", res.Items)
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
	if err := a.OnEvent(context.Background(), domain.SystemEvent{EventType: string(domain.EventPlanApproved), Payload: `{"external_id":"GH-acme-rocket-42","comment_body":"Overall plan text","external_ids":["GH-acme-rocket-42","GH-acme-rocket-43"]}`}); err != nil {
		t.Fatalf("plan approved: %v", err)
	}
	if len(commentPaths) != 2 {
		t.Fatalf("comment paths = %v, want 2 comments", commentPaths)
	}
}

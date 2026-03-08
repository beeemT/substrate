package gitlab

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/domain"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) Do(req *http.Request) (*http.Response, error) { return f(req) }

func jsonResponse(t *testing.T, status int, v any) *http.Response {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(string(b)))}
}

func makeAdapter(t *testing.T, fn roundTripFunc) *GitlabAdapter {
	t.Helper()
	a, err := newWithClient(context.Background(), config.GitlabConfig{Token: "token", BaseURL: "https://gitlab.example.com", ProjectID: 1234, Assignee: "alice", PollInterval: "5s", StateMappings: map[string]string{"in_progress": "reopen", "done": "close"}}, fn)
	if err != nil {
		t.Fatalf("newWithClient: %v", err)
	}
	return a
}

func makeAdapterWithConfig(t *testing.T, cfg config.GitlabConfig, fn roundTripFunc) *GitlabAdapter {
	t.Helper()
	a, err := newWithClient(context.Background(), cfg, fn)
	if err != nil {
		t.Fatalf("newWithClient: %v", err)
	}
	return a
}

func TestParseExternalID(t *testing.T) {
	iid, err := parseExternalID(1234, "GL-1234-42")
	if err != nil {
		t.Fatalf("parseExternalID: %v", err)
	}
	if iid != 42 {
		t.Fatalf("iid = %d, want 42", iid)
	}
	if _, err := parseExternalID(1234, "GL-999-42"); err == nil {
		t.Fatal("expected project mismatch error")
	}
}

func TestParsePollIntervalFloor(t *testing.T) {
	if got := parsePollInterval("5s"); got != 30*time.Second {
		t.Fatalf("parsePollInterval floor = %v, want 30s", got)
	}
}

func TestNewAllowsIssueBrowsingWithoutProjectID(t *testing.T) {
	var calls []string
	a := makeAdapterWithConfig(t, config.GitlabConfig{Token: "token", BaseURL: "https://gitlab.example.com"}, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls = append(calls, req.Method+" "+req.URL.Path)
		t.Fatalf("unexpected request during constructor: %s %s", req.Method, req.URL.String())
		return nil, nil
	}))
	if a.cfg.ProjectID != 0 {
		t.Fatalf("ProjectID = %d, want 0", a.cfg.ProjectID)
	}
	if len(calls) != 0 {
		t.Fatalf("calls = %v, want no metadata requests", calls)
	}
	_, err := a.ListSelectable(context.Background(), adapter.ListOpts{Scope: domain.ScopeProjects})
	if err == nil {
		t.Fatal("expected projects browse to require project context")
	}
	_, err = a.ListSelectable(context.Background(), adapter.ListOpts{Scope: domain.ScopeInitiatives})
	if !errors.Is(err, adapter.ErrBrowseNotSupported) {
		t.Fatalf("initiatives err = %v, want ErrBrowseNotSupported", err)
	}
	if len(calls) != 0 {
		t.Fatalf("calls after scoped browse = %v, want no requests without project context", calls)
	}
}

func TestListSelectableIssuesUsesGlobalInbox(t *testing.T) {
	var gotPath string
	var gotQuery string
	a := makeAdapterWithConfig(t, config.GitlabConfig{Token: "token", BaseURL: "https://gitlab.example.com"}, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		gotPath = req.URL.Path
		gotQuery = req.URL.RawQuery
		if req.URL.Path != "/api/v4/issues" {
			t.Fatalf("path = %s, want /api/v4/issues", req.URL.Path)
		}
		if req.URL.Query().Get("scope") != "created_by_me" {
			t.Fatalf("scope = %q, want created_by_me", req.URL.Query().Get("scope"))
		}
		if req.URL.Query().Get("state") != "closed" {
			t.Fatalf("state = %q, want closed", req.URL.Query().Get("state"))
		}
		if req.URL.Query().Get("search") != "bug" {
			t.Fatalf("search = %q, want bug", req.URL.Query().Get("search"))
		}
		if req.URL.Query().Get("per_page") != "5" {
			t.Fatalf("per_page = %q, want 5", req.URL.Query().Get("per_page"))
		}
		return jsonResponse(t, http.StatusOK, []map[string]any{{
			"iid":         42,
			"project_id":  5678,
			"title":       "Cross-project issue",
			"description": "body",
			"state":       "opened",
			"labels":      []string{"bug"},
			"web_url":     "https://gitlab.example.com/other-group/other-project/-/issues/42",
			"references":  map[string]any{"full": "other-group/other-project#42"},
		}}), nil
	}))

	result, err := a.ListSelectable(context.Background(), adapter.ListOpts{Scope: domain.ScopeIssues, Search: "bug", Limit: 5, View: "created_by_me", State: "closed", Labels: []string{"bug"}, Repo: "other-group/other-project", Group: "my-group"})
	if err != nil {
		t.Fatalf("ListSelectable: %v", err)
	}
	if gotPath != "/api/v4/issues" {
		t.Fatalf("path = %s, want /api/v4/issues", gotPath)
	}
	for _, want := range []string{"scope=created_by_me", "state=closed", "search=bug", "per_page=5", "labels=bug", "project_path=other-group%2Fother-project", "group_id=my-group"} {
		if !strings.Contains(gotQuery, want) {
			t.Fatalf("query = %q, want %q", gotQuery, want)
		}
	}
	if result.TotalCount != 1 || len(result.Items) != 1 {
		t.Fatalf("result count = %d items=%d, want 1", result.TotalCount, len(result.Items))
	}
	item := result.Items[0]
	if item.ID != "5678#42" {
		t.Fatalf("ID = %q, want 5678#42", item.ID)
	}
	if item.Identifier != "#42" {
		t.Fatalf("Identifier = %q, want #42", item.Identifier)
	}
	if item.Title != "Cross-project issue" {
		t.Fatalf("Title = %q, want issue title", item.Title)
	}
	if item.ContainerRef != "other-group/other-project" {
		t.Fatalf("ContainerRef = %q, want other-group/other-project", item.ContainerRef)
	}
	if item.URL != "https://gitlab.example.com/other-group/other-project/-/issues/42" {
		t.Fatalf("URL = %q, want issue url", item.URL)
	}
	if item.Description != "body" {
		t.Fatalf("Description = %q, want body", item.Description)
	}
	if len(item.Labels) != 1 || item.Labels[0] != "bug" {
		t.Fatalf("Labels = %#v, want [bug]", item.Labels)
	}
}

func TestListSelectableInitiativesUnsupportedForPersonalNamespace(t *testing.T) {
	a, err := newWithClient(context.Background(), config.GitlabConfig{Token: "token", BaseURL: "https://gitlab.example.com", ProjectID: 1234}, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return jsonResponse(t, http.StatusOK, map[string]any{"namespace": map[string]any{"id": 9, "kind": "user"}}), nil
	}))
	if err != nil {
		t.Fatalf("newWithClient: %v", err)
	}
	_, err = a.ListSelectable(context.Background(), adapter.ListOpts{Scope: domain.ScopeInitiatives})
	if !errors.Is(err, adapter.ErrBrowseNotSupported) {
		t.Fatalf("err = %v, want ErrBrowseNotSupported", err)
	}
}

func TestOnEventUpdatesStates(t *testing.T) {
	var calls []string
	a := makeAdapter(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls = append(calls, req.Method+" "+req.URL.Path)
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/api/v4/projects/1234":
			return jsonResponse(t, http.StatusOK, map[string]any{"namespace": map[string]any{"id": 55, "kind": "group"}}), nil
		case req.Method == http.MethodPut && req.URL.Path == "/api/v4/projects/1234/issues/42":
			return jsonResponse(t, http.StatusOK, map[string]any{"ok": true}), nil
		default:
			return jsonResponse(t, http.StatusOK, map[string]any{}), nil
		}
	}))
	if err := a.OnEvent(context.Background(), domain.SystemEvent{EventType: string(domain.EventPlanApproved), Payload: `{"external_id":"GL-1234-42"}`}); err != nil {
		t.Fatalf("plan approved: %v", err)
	}
	if err := a.OnEvent(context.Background(), domain.SystemEvent{EventType: string(domain.EventWorkItemCompleted), Payload: `{"external_id":"GL-1234-42"}`}); err != nil {
		t.Fatalf("work item completed: %v", err)
	}
	puts := 0
	for _, call := range calls {
		if strings.HasPrefix(call, "PUT /api/v4/projects/1234/issues/42") {
			puts++
		}
	}
	if puts != 2 {
		t.Fatalf("put calls = %d, want 2; calls=%v", puts, calls)
	}
}

func TestUpdateStateNoMappingNoop(t *testing.T) {
	a, err := newWithClient(context.Background(), config.GitlabConfig{Token: "token", BaseURL: "https://gitlab.example.com", ProjectID: 1234}, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/api/v4/projects/1234" {
			return jsonResponse(t, http.StatusOK, map[string]any{"namespace": map[string]any{"id": 1, "kind": "group"}}), nil
		}
		t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
		return nil, nil
	}))
	if err != nil {
		t.Fatalf("newWithClient: %v", err)
	}
	if err := a.UpdateState(context.Background(), "GL-1234-42", domain.TrackerStateDone); err != nil {
		t.Fatalf("UpdateState noop: %v", err)
	}
}

func TestPlanApprovedAddsComments(t *testing.T) {
	var commentPaths []string
	a := makeAdapter(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/api/v4/projects/1234":
			return jsonResponse(t, http.StatusOK, map[string]any{"namespace": map[string]any{"id": 55, "kind": "group"}}), nil
		case "/api/v4/projects/1234/issues/42/notes", "/api/v4/projects/1234/issues/43/notes":
			commentPaths = append(commentPaths, req.URL.Path)
			return jsonResponse(t, http.StatusCreated, map[string]any{"id": 1}), nil
		case "/api/v4/projects/1234/issues/42":
			return jsonResponse(t, http.StatusOK, map[string]any{"iid": 42, "title": "Issue 42", "state": "opened"}), nil
		default:
			return jsonResponse(t, http.StatusOK, map[string]any{}), nil
		}
	}))
	if err := a.OnEvent(context.Background(), domain.SystemEvent{EventType: string(domain.EventPlanApproved), Payload: `{"external_id":"GL-1234-42","comment_body":"Overall plan text","external_ids":["GL-1234-42","GL-1234-43"]}`}); err != nil {
		t.Fatalf("plan approved: %v", err)
	}
	if len(commentPaths) != 2 {
		t.Fatalf("comment paths = %v, want 2 comments", commentPaths)
	}
}

func TestResolveProjectMilestoneUsesDirectEndpoint(t *testing.T) {
	var calls []string
	a := makeAdapter(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls = append(calls, req.Method+" "+req.URL.Path)
		switch req.URL.Path {
		case "/api/v4/projects/1234":
			return jsonResponse(t, http.StatusOK, map[string]any{"namespace": map[string]any{"id": 55, "kind": "group"}}), nil
		case "/api/v4/projects/1234/milestones/77":
			return jsonResponse(t, http.StatusOK, map[string]any{"id": 77, "title": "Platform", "description": "Milestone desc", "web_url": "https://gitlab.example.com/groups/acme/-/milestones/77"}), nil
		case "/api/v4/projects/1234/milestones":
			t.Fatal("unexpected paginated milestone list fetch")
			return nil, nil
		default:
			return jsonResponse(t, http.StatusOK, map[string]any{}), nil
		}
	}))

	item, err := a.Resolve(context.Background(), adapter.Selection{Scope: domain.ScopeProjects, ItemIDs: []string{"77"}})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if item.ExternalID != "GL-1234-MILESTONE" {
		t.Fatalf("ExternalID = %q, want GL-1234-MILESTONE", item.ExternalID)
	}
	if item.Title != "Platform" {
		t.Fatalf("Title = %q, want Platform", item.Title)
	}
	if !strings.Contains(item.Description, "Milestone desc") {
		t.Fatalf("Description = %q, want milestone description", item.Description)
	}
	if len(calls) < 2 || calls[1] != "GET /api/v4/projects/1234/milestones/77" {
		t.Fatalf("calls = %v, want direct milestone fetch", calls)
	}
}

func TestResolveInitiativeEpicUsesDirectEndpoint(t *testing.T) {
	var calls []string
	a := makeAdapter(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls = append(calls, req.Method+" "+req.URL.Path)
		switch req.URL.Path {
		case "/api/v4/projects/1234":
			return jsonResponse(t, http.StatusOK, map[string]any{"namespace": map[string]any{"id": 55, "kind": "group"}}), nil
		case "/api/v4/groups/55/epics/12":
			return jsonResponse(t, http.StatusOK, map[string]any{"iid": 12, "title": "Epic title", "description": "Epic desc", "web_url": "https://gitlab.example.com/groups/acme/-/epics/12"}), nil
		case "/api/v4/groups/55/epics":
			t.Fatal("unexpected paginated epic list fetch")
			return nil, nil
		default:
			return jsonResponse(t, http.StatusOK, map[string]any{}), nil
		}
	}))

	item, err := a.Resolve(context.Background(), adapter.Selection{Scope: domain.ScopeInitiatives, ItemIDs: []string{"12"}})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if item.ExternalID != "GL-1234-EPIC-12" {
		t.Fatalf("ExternalID = %q, want GL-1234-EPIC-12", item.ExternalID)
	}
	if item.Title != "Epic title" {
		t.Fatalf("Title = %q, want Epic title", item.Title)
	}
	if item.Description != "Epic desc" {
		t.Fatalf("Description = %q, want Epic desc", item.Description)
	}
	if len(calls) < 2 || calls[1] != "GET /api/v4/groups/55/epics/12" {
		t.Fatalf("calls = %v, want direct epic fetch", calls)
	}
}

func TestResolveIssueTrackerRefs(t *testing.T) {
	a := makeAdapter(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/api/v4/projects/1234":
			return jsonResponse(t, http.StatusOK, map[string]any{"namespace": map[string]any{"id": 55, "kind": "group"}}), nil
		case "/api/v4/projects/1234/issues/42":
			return jsonResponse(t, http.StatusOK, map[string]any{"iid": 42, "title": "Issue 42", "description": "body", "labels": []any{}, "web_url": "https://gitlab.example.com/acme/rocket/-/issues/42"}), nil
		default:
			return jsonResponse(t, http.StatusOK, map[string]any{}), nil
		}
	}))
	item, err := a.Resolve(context.Background(), adapter.Selection{Scope: domain.ScopeIssues, ItemIDs: []string{"42"}})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	refs, ok := item.Metadata["tracker_refs"].([]domain.TrackerReference)
	if !ok || len(refs) != 1 {
		t.Fatalf("tracker_refs = %#v, want 1 typed ref", item.Metadata["tracker_refs"])
	}
	if refs[0].Provider != "gitlab" || refs[0].Number != 42 {
		t.Fatalf("tracker ref = %+v, want gitlab issue 42", refs[0])
	}
}

func TestResolveIssueTrackerRefsUsesProjectPath(t *testing.T) {
	a := makeAdapter(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/api/v4/projects/1234":
			return jsonResponse(t, http.StatusOK, map[string]any{"namespace": map[string]any{"id": 55, "kind": "group"}}), nil
		case "/api/v4/projects/1234/issues/42":
			return jsonResponse(t, http.StatusOK, map[string]any{"iid": 42, "title": "Issue 42", "description": "body", "labels": []any{}, "web_url": "https://gitlab.example.com/other-group/other-project/-/issues/42", "references": map[string]any{"full": "other-group/other-project#42"}}), nil
		default:
			return jsonResponse(t, http.StatusOK, map[string]any{}), nil
		}
	}))
	item, err := a.Resolve(context.Background(), adapter.Selection{Scope: domain.ScopeIssues, ItemIDs: []string{"42"}})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	refs, ok := item.Metadata["tracker_refs"].([]domain.TrackerReference)
	if !ok || len(refs) != 1 {
		t.Fatalf("tracker_refs = %#v, want 1 typed ref", item.Metadata["tracker_refs"])
	}
	if refs[0].Repo != "other-group/other-project" {
		t.Fatalf("tracker ref repo = %q, want other-group/other-project", refs[0].Repo)
	}
}

func TestResolveIssueUsesProjectQualifiedSelectionID(t *testing.T) {
	var calls []string
	a := makeAdapterWithConfig(t, config.GitlabConfig{Token: "token", BaseURL: "https://gitlab.example.com"}, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls = append(calls, req.Method+" "+req.URL.Path)
		switch req.URL.Path {
		case "/api/v4/projects/5678/issues/42":
			return jsonResponse(t, http.StatusOK, map[string]any{
				"iid":         42,
				"project_id":  5678,
				"title":       "Cross-project issue",
				"description": "body",
				"labels":      []any{"bug"},
				"web_url":     "https://gitlab.example.com/other-group/other-project/-/issues/42",
				"references":  map[string]any{"full": "other-group/other-project#42"},
			}), nil
		default:
			return jsonResponse(t, http.StatusOK, map[string]any{}), nil
		}
	}))
	item, err := a.Resolve(context.Background(), adapter.Selection{Scope: domain.ScopeIssues, ItemIDs: []string{"5678#42"}})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if item.ExternalID != "GL-5678-42" {
		t.Fatalf("ExternalID = %q, want GL-5678-42", item.ExternalID)
	}
	if len(item.SourceItemIDs) != 1 || item.SourceItemIDs[0] != "5678#42" {
		t.Fatalf("SourceItemIDs = %#v, want [5678#42]", item.SourceItemIDs)
	}
	if len(calls) != 1 || calls[0] != "GET /api/v4/projects/5678/issues/42" {
		t.Fatalf("calls = %v, want project-qualified fetch", calls)
	}
}

func TestResolveIssueSelectionWithoutProjectIDFails(t *testing.T) {
	a := makeAdapterWithConfig(t, config.GitlabConfig{Token: "token", BaseURL: "https://gitlab.example.com"}, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
		return nil, nil
	}))
	_, err := a.Resolve(context.Background(), adapter.Selection{Scope: domain.ScopeIssues, ItemIDs: []string{"42"}})
	if err == nil || !strings.Contains(err.Error(), "missing project id") {
		t.Fatalf("err = %v, want missing project id", err)
	}
}

func TestListSelectableIssuesRejectsUnsupportedView(t *testing.T) {
	a := makeAdapterWithConfig(t, config.GitlabConfig{Token: "token", BaseURL: "https://gitlab.example.com"}, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
		return nil, nil
	}))
	_, err := a.ListSelectable(context.Background(), adapter.ListOpts{Scope: domain.ScopeIssues, View: "mentioned"})
	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("err = %v, want unsupported view error", err)
	}
}

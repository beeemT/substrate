package linear

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/domain"
)

type testGQLBody struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
}

type capturedRequest struct {
	Query     string
	Variables map[string]any
}

func respondJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// testLinearAdapter constructs a LinearAdapter pointed at the given mock server URL.
func testLinearAdapter(t *testing.T, serverURL string) *LinearAdapter {
	t.Helper()
	cfg := config.LinearConfig{
		APIKey:       "test-key",
		TeamID:       "team1",
		PollInterval: "50ms",
	}
	return &LinearAdapter{
		cfg:    cfg,
		client: newGQLClient(cfg.APIKey, serverURL),
	}
}

// testIssueNode returns a minimal linearIssue-compatible map.
func testIssueNode(id, identifier, title string, labelNames []string, teamKey string) map[string]any {
	labels := make([]map[string]any, 0, len(labelNames))
	for _, n := range labelNames {
		labels = append(labels, map[string]any{"name": n})
	}
	return map[string]any{
		"id":          id,
		"identifier":  identifier,
		"title":       title,
		"description": "Details for " + title,
		"priority":    1,
		"url":         "https://linear.app/issue/" + identifier,
		"state": map[string]any{
			"id":   "state1",
			"name": "In Progress",
			"type": "started",
		},
		"labels":    map[string]any{"nodes": labels},
		"assignee":  map[string]any{"id": "user1", "name": "Alice"},
		"team":      map[string]any{"id": "team1", "key": teamKey},
		"createdAt": "2024-01-01T00:00:00.000Z",
		"updatedAt": "2024-01-01T00:00:00.000Z",
	}
}

func TestListSelectable(t *testing.T) {
	t.Run("issues", func(t *testing.T) {
		issue := testIssueNode("abc123", "FOO-123", "Fix bug", []string{"backend"}, "FOO")
		var issueReq capturedRequest
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var req testGQLBody
			_ = json.NewDecoder(r.Body).Decode(&req)
			issueReq = capturedRequest(req)
			respondJSON(w, map[string]any{
				"data": map[string]any{
					"issues": map[string]any{
						"nodes": []any{issue},
					},
				},
			})
		}))
		defer srv.Close()

		a := testLinearAdapter(t, srv.URL)
		result, err := a.ListSelectable(context.Background(), adapter.ListOpts{
			Scope:  domain.ScopeIssues,
			TeamID: "team1",
		})
		if err != nil {
			t.Fatalf("ListSelectable(ScopeIssues): %v", err)
		}
		if len(result.Items) != 1 {
			t.Fatalf("expected 1 item, got %d", len(result.Items))
		}
		item := result.Items[0]
		if item.ID != "abc123" {
			t.Errorf("ID: want %q, got %q", "abc123", item.ID)
		}
		if item.Title != "FOO-123: Fix bug" {
			t.Errorf("Title: want %q, got %q", "FOO-123: Fix bug", item.Title)
		}
		if item.Description != "Details for Fix bug" {
			t.Errorf("Description: want %q, got %q", "Details for Fix bug", item.Description)
		}
		if item.State != "In Progress" {
			t.Errorf("State: want %q, got %q", "In Progress", item.State)
		}
		if len(item.Labels) != 1 || item.Labels[0] != "backend" {
			t.Errorf("Labels: want [backend], got %v", item.Labels)
		}
		if !strings.Contains(issueReq.Query, "TeamIssues") {
			t.Fatalf("query = %q, want TeamIssues (base query without user filters)", issueReq.Query)
		}
		for _, forbidden := range []string{"assigneeId", "creatorId", "subscriberId"} {
			if _, exists := issueReq.Variables[forbidden]; exists {
				t.Fatalf("TeamIssues query should not send %s variable, got %v", forbidden, issueReq.Variables[forbidden])
			}
		}
		// Null-valued optional filters must not appear in the request.
		for _, nilVar := range []string{"search", "labelNames", "stateTypes", "stateNames"} {
			if v, exists := issueReq.Variables[nilVar]; exists {
				t.Fatalf("TeamIssues query with no filters should not send %s, got %v", nilVar, v)
			}
		}
		// The generated query must not contain filter clauses for omitted variables.
		for _, clause := range []string{"containsIgnoreCase", "labelNames", "stateTypes", "stateNames"} {
			if strings.Contains(issueReq.Query, clause) {
				t.Fatalf("TeamIssues query should not contain %q when filter is empty", clause)
			}
		}
	})

	t.Run("projects", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			respondJSON(w, map[string]any{
				"data": map[string]any{
					"projects": map[string]any{
						"nodes": []any{
							map[string]any{
								"id":          "proj1",
								"name":        "Project Alpha",
								"description": "Desc",
								"state":       "in_progress",
								"icon":        "",
								"color":       "",
								"createdAt":   "2025-01-01T00:00:00Z",
								"updatedAt":   "2025-06-01T00:00:00Z",
							},
						},
					},
				},
			})
		}))
		defer srv.Close()

		a := testLinearAdapter(t, srv.URL)
		result, err := a.ListSelectable(context.Background(), adapter.ListOpts{
			Scope:  domain.ScopeProjects,
			TeamID: "team1",
		})
		if err != nil {
			t.Fatalf("ListSelectable(ScopeProjects): %v", err)
		}
		if len(result.Items) != 1 {
			t.Fatalf("expected 1 item, got %d", len(result.Items))
		}
		item := result.Items[0]
		if item.ID != "proj1" {
			t.Errorf("ID: want %q, got %q", "proj1", item.ID)
		}
		if item.Title != "Project Alpha" {
			t.Errorf("Title: want %q, got %q", "Project Alpha", item.Title)
		}
		if item.Description != "Desc" {
			t.Errorf("Description: want %q, got %q", "Desc", item.Description)
		}
		if item.State != "in_progress" {
			t.Errorf("State: want %q, got %q", "in_progress", item.State)
		}
		if len(item.Labels) != 0 {
			t.Errorf("Labels: want [], got %v", item.Labels)
		}
	})

	t.Run("initiatives", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			respondJSON(w, map[string]any{
				"data": map[string]any{
					"initiatives": map[string]any{
						"nodes": []any{
							map[string]any{
								"id":          "init1",
								"name":        "Initiative Beta",
								"description": "Desc",
								"status":      "planned",
								"projects": map[string]any{"nodes": []any{
									map[string]any{
										"id":          "proj1",
										"name":        "Project Alpha",
										"description": "",
										"issues":      map[string]any{"nodes": []any{}},
									},
								}},
							},
						},
					},
				},
			})
		}))
		defer srv.Close()

		a := testLinearAdapter(t, srv.URL)
		result, err := a.ListSelectable(context.Background(), adapter.ListOpts{
			Scope: domain.ScopeInitiatives,
		})
		if err != nil {
			t.Fatalf("ListSelectable(ScopeInitiatives): %v", err)
		}
		if len(result.Items) != 1 {
			t.Fatalf("expected 1 item, got %d", len(result.Items))
		}
		item := result.Items[0]
		if item.ID != "init1" {
			t.Errorf("ID: want %q, got %q", "init1", item.ID)
		}
		if item.Title != "Initiative Beta" {
			t.Errorf("Title: want %q, got %q", "Initiative Beta", item.Title)
		}
		if item.Description != "Desc" {
			t.Errorf("Description: want %q, got %q", "Desc", item.Description)
		}
		if item.State != "planned" {
			t.Errorf("State: want %q, got %q", "planned", item.State)
		}
		if len(item.Labels) != 1 || item.Labels[0] != "Project Alpha" {
			t.Errorf("Labels: want [Project Alpha], got %v", item.Labels)
		}
	})
}

func TestLinearCapabilitiesExposeExpandedBrowseSupport(t *testing.T) {
	t.Parallel()

	a := &LinearAdapter{}
	caps := a.Capabilities()
	issues := caps.BrowseFilters[domain.ScopeIssues]
	if !issues.SupportsLabels || !issues.SupportsSearch || !issues.SupportsCursor || !issues.SupportsTeam {
		t.Fatalf("issue capabilities = %#v, want labels/search/cursor/team support", issues)
	}
	for _, want := range []string{"assigned_to_me", "created_by_me", "subscribed", "all"} {
		if !containsString(issues.Views, want) {
			t.Fatalf("issue views = %#v, want %q", issues.Views, want)
		}
	}
	for _, want := range []string{"open", "closed", "triage", "started", "completed"} {
		if !containsString(issues.States, want) {
			t.Fatalf("issue states = %#v, want %q", issues.States, want)
		}
	}
	projects := caps.BrowseFilters[domain.ScopeProjects]
	if !projects.SupportsSearch || !projects.SupportsCursor || !projects.SupportsTeam {
		t.Fatalf("project capabilities = %#v, want search/cursor/team support", projects)
	}
	initiatives := caps.BrowseFilters[domain.ScopeInitiatives]
	if !initiatives.SupportsSearch || !initiatives.SupportsCursor {
		t.Fatalf("initiative capabilities = %#v, want search/cursor support", initiatives)
	}
}

func TestListSelectableIssuesSupportsViewStateLabelsAndCursor(t *testing.T) {
	t.Parallel()

	issue := testIssueNode("abc123", "FOO-123", "Fix bug", []string{"backend", "urgent"}, "FOO")
	var requests []capturedRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req testGQLBody
		_ = json.NewDecoder(r.Body).Decode(&req)
		requests = append(requests, capturedRequest(req))
		switch {
		case strings.Contains(req.Query, "Viewer"):
			respondJSON(w, map[string]any{"data": map[string]any{"viewer": map[string]any{"id": "user1"}}})
		default:
			respondJSON(w, map[string]any{"data": map[string]any{"issues": map[string]any{"nodes": []any{issue}, "pageInfo": map[string]any{"hasNextPage": true, "endCursor": "cursor-2"}}}})
		}
	}))
	defer srv.Close()

	a := testLinearAdapter(t, srv.URL)
	result, err := a.ListSelectable(context.Background(), adapter.ListOpts{
		Scope:  domain.ScopeIssues,
		TeamID: "team1",
		View:   "assigned_to_me",
		State:  "open",
		Labels: []string{"backend", "urgent"},
		Search: "bug",
		Limit:  25,
		Cursor: "cursor-1",
	})
	if err != nil {
		t.Fatalf("ListSelectable(ScopeIssues): %v", err)
	}
	if len(requests) < 2 {
		t.Fatalf("requests = %d, want viewer + issues", len(requests))
	}
	issueReq := requests[len(requests)-1]
	if !strings.Contains(issueReq.Query, "AssignedIssues") {
		t.Fatalf("query = %q, want AssignedIssues", issueReq.Query)
	}
	if got := issueReq.Variables["teamId"]; got != "team1" {
		t.Fatalf("teamId = %v, want team1", got)
	}
	if got := issueReq.Variables["assigneeId"]; got != "user1" {
		t.Fatalf("assigneeId = %v, want user1", got)
	}
	if got := issueReq.Variables["search"]; got != "bug" {
		t.Fatalf("search = %v, want bug", got)
	}
	if got := issueReq.Variables["after"]; got != "cursor-1" {
		t.Fatalf("after = %v, want cursor-1", got)
	}
	if got := intFromAny(issueReq.Variables["first"]); got != 25 {
		t.Fatalf("first = %v, want 25", issueReq.Variables["first"])
	}
	if got := stringSliceFromAny(issueReq.Variables["labelNames"]); !equalStrings(got, []string{"backend", "urgent"}) {
		t.Fatalf("labelNames = %#v, want backend+urgent", got)
	}
	if got := stringSliceFromAny(issueReq.Variables["stateTypes"]); !equalStrings(got, []string{"triage", "backlog", "unstarted", "started"}) {
		t.Fatalf("stateTypes = %#v", got)
	}
	if result.NextCursor != "cursor-2" || !result.HasMore {
		t.Fatalf("pagination = %#v, want hasMore cursor-2", result)
	}
	if len(result.Items) != 1 || result.Items[0].Identifier != "FOO-123" || result.Items[0].ContainerRef != "FOO" {
		t.Fatalf("items = %#v, want identifier/container metadata", result.Items)
	}
	// Verify the dynamic query includes all filter clauses when values are provided.
	for _, clause := range []string{"containsIgnoreCase", "$labelNames", "$stateTypes", "$assigneeId", "$teamId"} {
		if !strings.Contains(issueReq.Query, clause) {
			t.Fatalf("query should contain %q when filter values are provided", clause)
		}
	}
}

func TestListSelectableIssuesSupportsCreatedByMe(t *testing.T) {
	t.Parallel()

	issue := testIssueNode("abc123", "FOO-123", "Fix bug", []string{"backend"}, "FOO")
	var issueReq capturedRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req testGQLBody
		_ = json.NewDecoder(r.Body).Decode(&req)
		if strings.Contains(req.Query, "Viewer") {
			respondJSON(w, map[string]any{"data": map[string]any{"viewer": map[string]any{"id": "user1"}}})
			return
		}
		issueReq = capturedRequest(req)
		respondJSON(w, map[string]any{"data": map[string]any{"issues": map[string]any{"nodes": []any{issue}, "pageInfo": map[string]any{"hasNextPage": false, "endCursor": ""}}}})
	}))
	defer srv.Close()

	a := testLinearAdapter(t, srv.URL)
	_, err := a.ListSelectable(context.Background(), adapter.ListOpts{Scope: domain.ScopeIssues, View: "created_by_me", State: "closed"})
	if err != nil {
		t.Fatalf("ListSelectable(ScopeIssues): %v", err)
	}
	if got := issueReq.Variables["creatorId"]; got != "user1" {
		t.Fatalf("creatorId = %v, want user1", got)
	}
	if !strings.Contains(issueReq.Query, "CreatorIssues") {
		t.Fatalf("query = %q, want CreatorIssues", issueReq.Query)
	}
	if got := stringSliceFromAny(issueReq.Variables["stateTypes"]); !equalStrings(got, []string{"completed", "canceled"}) {
		t.Fatalf("stateTypes = %#v, want completed/canceled", got)
	}
}

func TestListSelectableIssuesSupportsSubscribed(t *testing.T) {
	t.Parallel()

	issue := testIssueNode("abc123", "FOO-123", "Fix bug", []string{"backend"}, "FOO")
	var issueReq capturedRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req testGQLBody
		_ = json.NewDecoder(r.Body).Decode(&req)
		if strings.Contains(req.Query, "Viewer") {
			respondJSON(w, map[string]any{"data": map[string]any{"viewer": map[string]any{"id": "user1"}}})
			return
		}
		issueReq = capturedRequest(req)
		respondJSON(w, map[string]any{"data": map[string]any{"issues": map[string]any{"nodes": []any{issue}, "pageInfo": map[string]any{"hasNextPage": false, "endCursor": ""}}}})
	}))
	defer srv.Close()

	a := testLinearAdapter(t, srv.URL)
	_, err := a.ListSelectable(context.Background(), adapter.ListOpts{Scope: domain.ScopeIssues, View: "subscribed", State: "open"})
	if err != nil {
		t.Fatalf("ListSelectable(ScopeIssues): %v", err)
	}
	if got := issueReq.Variables["subscriberId"]; got != "user1" {
		t.Fatalf("subscriberId = %v, want user1", got)
	}
	if !strings.Contains(issueReq.Query, "subscribers") {
		t.Fatalf("query = %q, want subscribers filter", issueReq.Query)
	}
	if !strings.Contains(issueReq.Query, "SubscribedIssues") {
		t.Fatalf("query = %q, want SubscribedIssues", issueReq.Query)
	}
}

func TestBuildIssueQueryOmitsNilFilters(t *testing.T) {
	t.Parallel()

	// When all optional vars are nil, the query should have an empty filter block.
	vars := map[string]any{
		"teamId":     nil,
		"search":     nil,
		"labelNames": nil,
		"stateTypes": nil,
		"stateNames": nil,
		"first":      50,
		"after":      nil,
	}
	q := buildIssueQuery("TeamIssues", vars)
	for _, clause := range []string{"containsIgnoreCase", "labelNames", "stateTypes", "stateNames", "eq: $teamId"} {
		if strings.Contains(q, clause) {
			t.Errorf("nil-filter query should not contain %q, got:\n%s", clause, q)
		}
	}
	// Must still be a valid query with pagination variables.
	if !strings.Contains(q, "$first: Int") || !strings.Contains(q, "TeamIssues") {
		t.Errorf("query missing basic structure:\n%s", q)
	}

	// When values are provided, clauses must appear.
	vars["teamId"] = "team1"
	vars["search"] = "bug"
	vars["labelNames"] = []string{"backend"}
	vars["stateTypes"] = []string{"started"}
	q = buildIssueQuery("TeamIssues", vars)
	for _, clause := range []string{"eq: $teamId", "containsIgnoreCase: $search", "in: $labelNames", "in: $stateTypes"} {
		if !strings.Contains(q, clause) {
			t.Errorf("filtered query should contain %q, got:\n%s", clause, q)
		}
	}
}

func TestBuildProjectQueryOmitsNilFilters(t *testing.T) {
	t.Parallel()

	vars := map[string]any{
		"teamId": nil,
		"search": nil,
		"states": nil,
		"first":  50,
		"after":  nil,
	}
	q := buildProjectQuery(vars)
	for _, clause := range []string{"containsIgnoreCase", "eq: $teamId", "in: $states"} {
		if strings.Contains(q, clause) {
			t.Errorf("nil-filter project query should not contain %q, got:\n%s", clause, q)
		}
	}

	vars["search"] = "alpha"
	vars["states"] = []string{"started"}
	q = buildProjectQuery(vars)
	for _, clause := range []string{"containsIgnoreCase: $search", "in: $states"} {
		if !strings.Contains(q, clause) {
			t.Errorf("filtered project query should contain %q, got:\n%s", clause, q)
		}
	}
}

func TestBuildInitiativeQueryOmitsNilFilters(t *testing.T) {
	t.Parallel()

	vars := map[string]any{
		"search":   nil,
		"statuses": nil,
		"first":    50,
		"after":    nil,
	}
	q := buildInitiativeQuery(vars)
	for _, clause := range []string{"containsIgnoreCase", "in: $statuses"} {
		if strings.Contains(q, clause) {
			t.Errorf("nil-filter initiative query should not contain %q, got:\n%s", clause, q)
		}
	}

	vars["search"] = "beta"
	vars["statuses"] = []string{"started"}
	q = buildInitiativeQuery(vars)
	for _, clause := range []string{"containsIgnoreCase: $search", "in: $statuses"} {
		if !strings.Contains(q, clause) {
			t.Errorf("filtered initiative query should contain %q, got:\n%s", clause, q)
		}
	}
}

func TestListSelectableProjectsSupportSearchStateAndCursor(t *testing.T) {
	t.Parallel()

	var projectReq capturedRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req testGQLBody
		_ = json.NewDecoder(r.Body).Decode(&req)
		projectReq = capturedRequest(req)
		respondJSON(w, map[string]any{"data": map[string]any{"projects": map[string]any{"nodes": []any{map[string]any{"id": "proj1", "name": "Project Alpha", "description": "Desc", "state": "started", "icon": "", "color": "", "issues": map[string]any{"nodes": []any{testIssueNode("abc123", "FOO-123", "Fix bug", nil, "FOO")}}}}, "pageInfo": map[string]any{"hasNextPage": true, "endCursor": "proj-cursor-2"}}}})
	}))
	defer srv.Close()

	a := testLinearAdapter(t, srv.URL)
	result, err := a.ListSelectable(context.Background(), adapter.ListOpts{Scope: domain.ScopeProjects, TeamID: "team1", Search: "alpha", State: "open", Limit: 10, Cursor: "proj-cursor-1"})
	if err != nil {
		t.Fatalf("ListSelectable(ScopeProjects): %v", err)
	}
	if got := projectReq.Variables["search"]; got != "alpha" {
		t.Fatalf("search = %v, want alpha", got)
	}
	if got := stringSliceFromAny(projectReq.Variables["states"]); !equalStrings(got, []string{"planned", "backlog", "started", "paused"}) {
		t.Fatalf("states = %#v", got)
	}
	if got := projectReq.Variables["after"]; got != "proj-cursor-1" {
		t.Fatalf("after = %v, want proj-cursor-1", got)
	}
	if !result.HasMore || result.NextCursor != "proj-cursor-2" {
		t.Fatalf("pagination = %#v", result)
	}
}

func TestListSelectableInitiativesSupportSearchStateAndCursor(t *testing.T) {
	t.Parallel()

	var initiativeReq capturedRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req testGQLBody
		_ = json.NewDecoder(r.Body).Decode(&req)
		initiativeReq = capturedRequest(req)
		respondJSON(w, map[string]any{
			"data": map[string]any{
				"initiatives": map[string]any{
					"nodes": []any{
						map[string]any{
							"id":          "init1",
							"name":        "Initiative Beta",
							"description": "Desc",
							"status":      "started",
							"projects": map[string]any{"nodes": []any{
								map[string]any{
									"id":          "proj1",
									"name":        "Project Alpha",
									"description": "",
									"state":       "started",
									"issues":      map[string]any{"nodes": []any{}},
								},
							}},
						},
					},
					"pageInfo": map[string]any{"hasNextPage": true, "endCursor": "init-cursor-2"},
				},
			},
		})
	}))
	defer srv.Close()

	a := testLinearAdapter(t, srv.URL)
	result, err := a.ListSelectable(context.Background(), adapter.ListOpts{Scope: domain.ScopeInitiatives, Search: "beta", State: "started", Limit: 5, Cursor: "init-cursor-1"})
	if err != nil {
		t.Fatalf("ListSelectable(ScopeInitiatives): %v", err)
	}
	if got := initiativeReq.Variables["search"]; got != "beta" {
		t.Fatalf("search = %v, want beta", got)
	}
	if got := stringSliceFromAny(initiativeReq.Variables["statuses"]); !equalStrings(got, []string{"started"}) {
		t.Fatalf("statuses = %#v", got)
	}
	if got := initiativeReq.Variables["after"]; got != "init-cursor-1" {
		t.Fatalf("after = %v, want init-cursor-1", got)
	}
	if !result.HasMore || result.NextCursor != "init-cursor-2" {
		t.Fatalf("pagination = %#v", result)
	}
}

func containsString(values []string, want string) bool {
	return slices.Contains(values, want)
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func stringSliceFromAny(value any) []string {
	if value == nil {
		return nil
	}
	raw, ok := value.([]any)
	if ok {
		out := make([]string, 0, len(raw))
		for _, item := range raw {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	stringsValue, ok := value.([]string)
	if ok {
		return stringsValue
	}
	return nil
}

func intFromAny(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case float64:
		return int(v)
	default:
		return 0
	}
}

func TestResolve(t *testing.T) {
	t.Run("single_issue", func(t *testing.T) {
		issue := testIssueNode("abc123", "FOO-123", "Fix bug", []string{"backend"}, "FOO")
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var req testGQLBody
			_ = json.NewDecoder(r.Body).Decode(&req)
			if strings.Contains(req.Query, "IssueByID") {
				respondJSON(w, map[string]any{
					"data": map[string]any{"issue": issue},
				})
			} else {
				// IssuesByIDs or any issues query
				respondJSON(w, map[string]any{
					"data": map[string]any{
						"issues": map[string]any{"nodes": []any{issue}},
					},
				})
			}
		}))
		defer srv.Close()

		a := testLinearAdapter(t, srv.URL)
		wi, err := a.Resolve(context.Background(), adapter.Selection{
			Scope:   domain.ScopeIssues,
			ItemIDs: []string{"abc123"},
		})
		if err != nil {
			t.Fatalf("Resolve single issue: %v", err)
		}
		if wi.ExternalID != "LIN-FOO-123" {
			t.Errorf("ExternalID: want %q, got %q", "LIN-FOO-123", wi.ExternalID)
		}
		if wi.Title != "Fix bug" {
			t.Errorf("Title: want %q, got %q", "Fix bug", wi.Title)
		}
		if wi.Source != "linear" {
			t.Errorf("Source: want %q, got %q", "linear", wi.Source)
		}
	})

	t.Run("multi_issue", func(t *testing.T) {
		issue1 := testIssueNode("abc123", "FOO-123", "Fix bug", []string{"backend"}, "FOO")
		issue2 := testIssueNode("def456", "FOO-456", "Add feature", []string{"frontend"}, "FOO")
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			respondJSON(w, map[string]any{
				"data": map[string]any{
					"issues": map[string]any{"nodes": []any{issue1, issue2}},
				},
			})
		}))
		defer srv.Close()

		a := testLinearAdapter(t, srv.URL)
		wi, err := a.Resolve(context.Background(), adapter.Selection{
			Scope:   domain.ScopeIssues,
			ItemIDs: []string{"abc123", "def456"},
		})
		if err != nil {
			t.Fatalf("Resolve multi issue: %v", err)
		}
		if !strings.Contains(wi.Title, "+1 more") {
			t.Errorf("Title: expected '+1 more', got %q", wi.Title)
		}
		// Labels should be merged from both issues.
		labelSet := make(map[string]bool, len(wi.Labels))
		for _, l := range wi.Labels {
			labelSet[l] = true
		}
		if !labelSet["backend"] {
			t.Errorf("Labels: missing 'backend' in %v", wi.Labels)
		}
		if !labelSet["frontend"] {
			t.Errorf("Labels: missing 'frontend' in %v", wi.Labels)
		}
	})

	t.Run("project", func(t *testing.T) {
		issue := testIssueNode("abc123", "FOO-123", "Fix bug", []string{}, "FOO")
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			respondJSON(w, map[string]any{
				"data": map[string]any{
					"project": map[string]any{
						"id":          "proj1",
						"name":        "Project Alpha",
						"description": "Desc",
						"state":       "in_progress",
						"icon":        "",
						"color":       "",
						"issues": map[string]any{
							"nodes": []any{issue},
						},
					},
				},
			})
		}))
		defer srv.Close()

		a := testLinearAdapter(t, srv.URL)
		wi, err := a.Resolve(context.Background(), adapter.Selection{
			Scope:   domain.ScopeProjects,
			ItemIDs: []string{"proj1"},
		})
		if err != nil {
			t.Fatalf("Resolve project: %v", err)
		}
		if wi.Title != "Project Alpha" {
			t.Errorf("Title: want %q, got %q", "Project Alpha", wi.Title)
		}
		if wi.Source != "linear" {
			t.Errorf("Source: want %q, got %q", "linear", wi.Source)
		}
	})

	t.Run("initiative_single", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			respondJSON(w, map[string]any{
				"data": map[string]any{
					"initiative": map[string]any{
						"id":          "init1",
						"name":        "Initiative Beta",
						"description": "Desc",
						"status":      "planned",
						"projects": map[string]any{
							"nodes": []any{
								map[string]any{
									"id":          "proj1",
									"name":        "Project Alpha",
									"description": "",
									"issues":      map[string]any{"nodes": []any{}},
								},
							},
						},
					},
				},
			})
		}))
		defer srv.Close()

		a := testLinearAdapter(t, srv.URL)
		wi, err := a.Resolve(context.Background(), adapter.Selection{
			Scope:   domain.ScopeInitiatives,
			ItemIDs: []string{"init1"},
		})
		if err != nil {
			t.Fatalf("Resolve initiative: %v", err)
		}
		if wi.Title != "Initiative Beta" {
			t.Errorf("Title: want %q, got %q", "Initiative Beta", wi.Title)
		}
		if wi.Source != "linear" {
			t.Errorf("Source: want %q, got %q", "linear", wi.Source)
		}
	})

	t.Run("initiative_multiple_ids_error", func(t *testing.T) {
		// Initiatives scope with >1 ID must return an error; server must not be hit.
		var hit int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			atomic.AddInt32(&hit, 1)
			respondJSON(w, map[string]any{"data": map[string]any{}})
		}))
		defer srv.Close()

		a := testLinearAdapter(t, srv.URL)
		_, err := a.Resolve(context.Background(), adapter.Selection{
			Scope:   domain.ScopeInitiatives,
			ItemIDs: []string{"init1", "init2"},
		})
		if err == nil {
			t.Error("expected error for multiple initiative IDs, got nil")
		}
		// The error should be returned without an API call.
		if atomic.LoadInt32(&hit) != 0 {
			t.Errorf("expected no API calls for multi-initiative Resolve, got %d", hit)
		}
	})
}

func TestResolveWatchPollInterval(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want time.Duration
	}{
		{name: "invalid falls back to default", raw: "nope", want: defaultWatchPollInterval},
		{name: "empty falls back to default", raw: "", want: defaultWatchPollInterval},
		{name: "below floor clamps", raw: "30s", want: minWatchPollInterval},
		{name: "zero clamps", raw: "0s", want: minWatchPollInterval},
		{name: "negative clamps", raw: "-10s", want: minWatchPollInterval},
		{name: "equal to floor", raw: "60s", want: minWatchPollInterval},
		{name: "above floor stays", raw: "90s", want: 90 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveWatchPollInterval(tt.raw)
			if got != tt.want {
				t.Fatalf("resolveWatchPollInterval(%q) = %s, want %s", tt.raw, got, tt.want)
			}
		})
	}
}

func TestNextWatchBackoff(t *testing.T) {
	t.Run("doubles until cap", func(t *testing.T) {
		base := minWatchPollInterval
		got := nextWatchBackoff(base, base)
		want := 2 * base
		if got != want {
			t.Fatalf("nextWatchBackoff(%s, %s) = %s, want %s", base, base, got, want)
		}
	})

	t.Run("caps at 10x resolved base", func(t *testing.T) {
		base := defaultWatchPollInterval
		got := nextWatchBackoff(8*base, base)
		want := 10 * base
		if got != want {
			t.Fatalf("nextWatchBackoff(%s, %s) = %s, want %s", 8*base, base, got, want)
		}
	})
}

func TestLinearExternalID(t *testing.T) {
	tests := []struct {
		identifier string
		teamKey    string
		want       string
	}{
		{"FOO-123", "FOO", "LIN-FOO-123"},
		{"BAR-456", "BAR", "LIN-BAR-456"},
		{"TEAM-1", "TEAM", "LIN-TEAM-1"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			issue := linearIssue{
				Identifier: tt.identifier,
				Team:       linearTeamRef{Key: tt.teamKey},
			}
			got := linearExternalID(issue)
			if got != tt.want {
				t.Errorf("linearExternalID = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveMetadataIncludesProviderAwareFields(t *testing.T) {
	t.Run("issue", func(t *testing.T) {
		issue := testIssueNode("abc123", "FOO-123", "Fix bug", []string{"backend"}, "FOO")
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			respondJSON(w, map[string]any{
				"data": map[string]any{
					"issues": map[string]any{"nodes": []any{issue}},
				},
			})
		}))
		defer srv.Close()

		a := testLinearAdapter(t, srv.URL)
		wi, err := a.Resolve(context.Background(), adapter.Selection{Scope: domain.ScopeIssues, ItemIDs: []string{"abc123"}})
		if err != nil {
			t.Fatalf("Resolve single issue: %v", err)
		}
		if got := wi.Metadata["linear_team_key"]; got != "FOO" {
			t.Fatalf("linear_team_key = %#v, want %q", got, "FOO")
		}
		if got := wi.Metadata["linear_state_name"]; got != "In Progress" {
			t.Fatalf("linear_state_name = %#v, want %q", got, "In Progress")
		}
		if got := wi.Metadata["linear_assignee_name"]; got != "Alice" {
			t.Fatalf("linear_assignee_name = %#v, want %q", got, "Alice")
		}
	})

	t.Run("project", func(t *testing.T) {
		issue := testIssueNode("abc123", "FOO-123", "Fix bug", []string{}, "FOO")
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			respondJSON(w, map[string]any{
				"data": map[string]any{
					"project": map[string]any{
						"id":          "proj1",
						"name":        "Project Alpha",
						"description": "Desc",
						"state":       "in_progress",
						"icon":        "",
						"color":       "",
						"issues":      map[string]any{"nodes": []any{issue}},
					},
				},
			})
		}))
		defer srv.Close()

		a := testLinearAdapter(t, srv.URL)
		wi, err := a.Resolve(context.Background(), adapter.Selection{Scope: domain.ScopeProjects, ItemIDs: []string{"proj1"}})
		if err != nil {
			t.Fatalf("Resolve project: %v", err)
		}
		refs, ok := wi.Metadata["tracker_refs"].([]domain.TrackerReference)
		if !ok || len(refs) != 1 || refs[0].Kind != "project" || refs[0].ID != "proj1" {
			t.Fatalf("tracker_refs = %#v, want single project ref", wi.Metadata["tracker_refs"])
		}
		if got := wi.Metadata["linear_project_name"]; got != "Project Alpha" {
			t.Fatalf("linear_project_name = %#v, want %q", got, "Project Alpha")
		}
		ids, ok := wi.Metadata["linear_project_ids"].([]string)
		if !ok || len(ids) != 1 || ids[0] != "proj1" {
			t.Fatalf("linear_project_ids = %#v, want [proj1]", wi.Metadata["linear_project_ids"])
		}
	})

	t.Run("initiative", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			respondJSON(w, map[string]any{
				"data": map[string]any{
					"initiative": map[string]any{
						"id":          "init1",
						"name":        "Initiative Beta",
						"description": "Desc",
						"status":      "planned",
						"projects": map[string]any{"nodes": []any{
							map[string]any{
								"id":          "proj1",
								"name":        "Project Alpha",
								"description": "",
								"issues":      map[string]any{"nodes": []any{}},
							},
						}},
					},
				},
			})
		}))
		defer srv.Close()

		a := testLinearAdapter(t, srv.URL)
		wi, err := a.Resolve(context.Background(), adapter.Selection{Scope: domain.ScopeInitiatives, ItemIDs: []string{"init1"}})
		if err != nil {
			t.Fatalf("Resolve initiative: %v", err)
		}
		refs, ok := wi.Metadata["tracker_refs"].([]domain.TrackerReference)
		if !ok || len(refs) != 1 || refs[0].Kind != "initiative" || refs[0].ID != "init1" {
			t.Fatalf("tracker_refs = %#v, want single initiative ref", wi.Metadata["tracker_refs"])
		}
		if got := wi.Metadata["linear_initiative_status"]; got != "planned" {
			t.Fatalf("linear_initiative_status = %#v, want %q", got, "planned")
		}
		names, ok := wi.Metadata["linear_project_names"].([]string)
		if !ok || len(names) != 1 || names[0] != "Project Alpha" {
			t.Fatalf("linear_project_names = %#v, want [Project Alpha]", wi.Metadata["linear_project_names"])
		}
	})
}

func TestResolveIssueTrackerRefs(t *testing.T) {
	issue := testIssueNode("abc123", "FOO-123", "Fix bug", []string{"backend"}, "FOO")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		respondJSON(w, map[string]any{
			"data": map[string]any{
				"issues": map[string]any{"nodes": []any{issue}},
			},
		})
	}))
	defer srv.Close()

	a := testLinearAdapter(t, srv.URL)
	wi, err := a.Resolve(context.Background(), adapter.Selection{Scope: domain.ScopeIssues, ItemIDs: []string{"abc123"}})
	if err != nil {
		t.Fatalf("Resolve single issue: %v", err)
	}
	refs, ok := wi.Metadata["tracker_refs"].([]domain.TrackerReference)
	if !ok || len(refs) != 1 {
		t.Fatalf("tracker_refs = %#v, want 1 typed ref", wi.Metadata["tracker_refs"])
	}
	if refs[0].Provider != "linear" || refs[0].ID != "FOO-123" || refs[0].URL == "" {
		t.Fatalf("tracker ref = %+v, want linear FOO-123 with url", refs[0])
	}
}

func TestResolveMultiIssueTrackerRefs(t *testing.T) {
	issue1 := testIssueNode("abc123", "FOO-123", "Fix bug", []string{"backend"}, "FOO")
	issue2 := testIssueNode("def456", "FOO-456", "Add feature", []string{"frontend"}, "FOO")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		respondJSON(w, map[string]any{
			"data": map[string]any{
				"issues": map[string]any{"nodes": []any{issue1, issue2}},
			},
		})
	}))
	defer srv.Close()

	a := testLinearAdapter(t, srv.URL)
	wi, err := a.Resolve(context.Background(), adapter.Selection{Scope: domain.ScopeIssues, ItemIDs: []string{"abc123", "def456"}})
	if err != nil {
		t.Fatalf("Resolve multi issue: %v", err)
	}
	refs, ok := wi.Metadata["tracker_refs"].([]domain.TrackerReference)
	if !ok || len(refs) != 2 {
		t.Fatalf("tracker_refs = %#v, want 2 typed refs", wi.Metadata["tracker_refs"])
	}
	if refs[0].ID != "FOO-123" || refs[1].ID != "FOO-456" {
		t.Fatalf("tracker refs = %+v, want ordered issue identifiers", refs)
	}
}

func TestSubstrateToLinearIdentifier(t *testing.T) {
	tests := []struct {
		externalID string
		want       string
	}{
		{"LIN-FOO-123", "FOO-123"},
		{"LIN-BAR-456", "BAR-456"},
		{"LIN-TEAM-1", "TEAM-1"},
	}
	for _, tt := range tests {
		t.Run(tt.externalID, func(t *testing.T) {
			got, err := substrateToLinearIdentifier(tt.externalID)
			if err != nil {
				t.Fatalf("substrateToLinearIdentifier(%q) error: %v", tt.externalID, err)
			}
			if got != tt.want {
				t.Errorf("substrateToLinearIdentifier(%q) = %q, want %q", tt.externalID, got, tt.want)
			}
		})
	}
}


func TestOnEvent_IgnoresForeignExternalID(t *testing.T) {
	// No GraphQL mutation must fire when external_id doesn't start with LIN-.
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/graphql" {
			called = true
		}
		respondJSON(w, map[string]any{"data": map[string]any{}})
	}))
	defer srv.Close()
	a := testLinearAdapter(t, srv.URL)

	foreignIDs := []string{"gh:issue:acme/rocket#42", "gl:issue:83#4796", "SEN-acme-12345"}
	for _, fid := range foreignIDs {
		called = false
		payload := `{"external_id":"` + fid + `"}`
		if err := a.OnEvent(context.Background(), domain.SystemEvent{
			EventType: string(domain.EventPlanApproved),
			Payload:   payload,
		}); err != nil {
			t.Errorf("OnEvent(plan.approved, %q): got error %v, want nil", fid, err)
		}
		if called {
			t.Errorf("OnEvent(plan.approved, %q): made unexpected HTTP call", fid)
		}

		called = false
		if err := a.OnEvent(context.Background(), domain.SystemEvent{
			EventType: string(domain.EventWorkItemCompleted),
			Payload:   payload,
		}); err != nil {
			t.Errorf("OnEvent(work_item.completed, %q): got error %v, want nil", fid, err)
		}
		if called {
			t.Errorf("OnEvent(work_item.completed, %q): made unexpected HTTP call", fid)
		}
	}
}
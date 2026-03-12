package sentry

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/domain"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) Do(req *http.Request) (*http.Response, error) { return f(req) }

func jsonResponse(t *testing.T, status int, header http.Header, body any) *http.Response {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	if header == nil {
		header = http.Header{}
	}
	if header.Get("Content-Type") == "" {
		header.Set("Content-Type", "application/json")
	}
	return &http.Response{
		StatusCode: status,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(string(payload))),
	}
}

func testAdapter(t *testing.T, cfg config.SentryConfig, rt roundTripFunc) *SentryAdapter {
	t.Helper()
	a, err := newWithClient(cfg, rt)
	if err != nil {
		t.Fatalf("newWithClient: %v", err)
	}
	return a
}

func testIssue(id, shortID, title, project string) sentryIssue {
	firstSeen := &sentryTime{Time: time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)}
	lastSeen := &sentryTime{Time: time.Date(2024, 1, 3, 4, 5, 6, 0, time.UTC)}
	return sentryIssue{
		ID:        id,
		ShortID:   shortID,
		Title:     title,
		Culprit:   "svc.handler",
		Permalink: "https://sentry.example/issues/" + id,
		Status:    "unresolved",
		Level:     "error",
		Count:     "12",
		UserCount: "3",
		FirstSeen: firstSeen,
		LastSeen:  lastSeen,
		Project: sentryProject{
			ID:   "1",
			Slug: project,
			Name: strings.ToUpper(project),
		},
	}
}

func issuePayload(issue sentryIssue, userCount any) map[string]any {
	payload := map[string]any{
		"id":        issue.ID,
		"shortId":   issue.ShortID,
		"title":     issue.Title,
		"culprit":   issue.Culprit,
		"permalink": issue.Permalink,
		"status":    issue.Status,
		"level":     issue.Level,
		"count":     issue.Count.String(),
		"userCount": userCount,
		"project": map[string]any{
			"id":   issue.Project.ID,
			"slug": issue.Project.Slug,
			"name": issue.Project.Name,
		},
	}
	if issue.FirstSeen != nil {
		payload["firstSeen"] = issue.FirstSeen.Time.Format(time.RFC3339Nano)
	}
	if issue.LastSeen != nil {
		payload["lastSeen"] = issue.LastSeen.Time.Format(time.RFC3339Nano)
	}
	return payload
}

func TestNewRequiresTokenAndOrganization(t *testing.T) {
	t.Parallel()

	if _, err := New(config.SentryConfig{Organization: "acme"}); err == nil {
		t.Fatal("New() error = nil, want missing token error")
	}
	if _, err := New(config.SentryConfig{Token: "token"}); err == nil {
		t.Fatal("New() error = nil, want missing organization error")
	}

	a, err := newWithClient(config.SentryConfig{Token: " token ", Organization: " acme "}, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected request: %s", req.URL.String())
		return nil, nil
	}))
	if err != nil {
		t.Fatalf("newWithClient: %v", err)
	}
	if a.baseURL != defaultBaseURL {
		t.Fatalf("baseURL = %q, want %q", a.baseURL, defaultBaseURL)
	}
	if a.token != "token" || a.organization != "acme" {
		t.Fatalf("trimmed cfg = token:%q org:%q", a.token, a.organization)
	}
}

func TestCapabilitiesExposeSourceOnlyIssueBrowse(t *testing.T) {
	t.Parallel()

	caps := (&SentryAdapter{}).Capabilities()
	if caps.CanWatch || !caps.CanBrowse || caps.CanMutate {
		t.Fatalf("capabilities = %#v", caps)
	}
	issues := caps.BrowseFilters[domain.ScopeIssues]
	if !issues.SupportsSearch || !issues.SupportsCursor || !issues.SupportsRepo {
		t.Fatalf("issue capabilities = %#v", issues)
	}
	for _, want := range []string{"assigned_to_me", "all"} {
		if !contains(issues.Views, want) {
			t.Fatalf("views = %#v, want %q", issues.Views, want)
		}
	}
	for _, want := range []string{"unresolved", "for_review", "regressed", "escalating", "resolved", "archived"} {
		if !contains(issues.States, want) {
			t.Fatalf("states = %#v, want %q", issues.States, want)
		}
	}
}

func TestBuildIssueListQueryAppliesFiltersAndAllowlist(t *testing.T) {
	t.Parallel()

	a := &SentryAdapter{projects: []string{"web", "api"}}
	values, empty, err := a.buildIssueListQuery(adapter.ListOpts{
		View:   "assigned_to_me",
		State:  "resolved",
		Search: "memory leak",
		Repo:   "web",
		Cursor: "0:100:0",
		Limit:  25,
	})
	if err != nil {
		t.Fatalf("buildIssueListQuery: %v", err)
	}
	if empty {
		t.Fatal("empty result = true, want query values")
	}
	if got := values.Get("query"); got != "assigned:me is:resolved project:web memory leak" {
		t.Fatalf("query = %q", got)
	}
	if got := values["project"]; len(got) != 0 {
		t.Fatalf("project params = %#v, want none", got)
	}
	if got := values.Get("cursor"); got != "0:100:0" {
		t.Fatalf("cursor = %q, want 0:100:0", got)
	}
	if got := values.Get("limit"); got != "25" {
		t.Fatalf("limit = %q, want 25", got)
	}

	values, empty, err = a.buildIssueListQuery(adapter.ListOpts{})
	if err != nil {
		t.Fatalf("buildIssueListQuery default: %v", err)
	}
	if empty {
		t.Fatal("default query unexpectedly empty")
	}
	if got := values.Get("query"); got != "project:[web,api]" {
		t.Fatalf("default query = %q", got)
	}
	if got := values["project"]; len(got) != 0 {
		t.Fatalf("default project params = %#v, want none", got)
	}

	_, empty, err = a.buildIssueListQuery(adapter.ListOpts{Repo: "payments"})
	if err != nil {
		t.Fatalf("buildIssueListQuery outside allowlist: %v", err)
	}
	if !empty {
		t.Fatal("empty result = false, want outside-allowlist short-circuit")
	}
}

func TestParseNextCursorReadsLinkHeader(t *testing.T) {
	t.Parallel()

	link := `<https://sentry.io/api/0/organizations/acme/issues/?cursor=0:0:1>; rel="previous"; results="false"; cursor="0:0:1", <https://sentry.io/api/0/organizations/acme/issues/?cursor=0:100:0>; rel="next"; results="true"; cursor="0:100:0"`
	cursor, hasMore := parseNextCursor(link)
	if !hasMore || cursor != "0:100:0" {
		t.Fatalf("parseNextCursor() = (%q, %v), want (0:100:0, true)", cursor, hasMore)
	}

	cursor, hasMore = parseNextCursor(`<https://sentry.io/api/0/organizations/acme/issues/?cursor=0:200:0>; rel="next"; results="false"; cursor="0:200:0"`)
	if hasMore || cursor != "" {
		t.Fatalf("final page cursor = (%q, %v), want empty false", cursor, hasMore)
	}
}

func TestListSelectableMapsIssuesAndPagination(t *testing.T) {
	t.Parallel()

	issue := testIssue("101", "SEN-101", "Crash in checkout", "web")
	var seenPath string
	var seenQuery url.Values
	a := testAdapter(t, config.SentryConfig{Token: "token", Organization: "acme", BaseURL: "https://sentry.example/api/0", Projects: []string{"web", "api"}}, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		seenPath = req.URL.Path
		seenQuery = req.URL.Query()
		if got := req.Header.Get("Authorization"); got != "Bearer token" {
			t.Fatalf("authorization = %q, want bearer token", got)
		}
		header := http.Header{}
		header.Set("Link", `<https://sentry.example/api/0/organizations/acme/issues/?cursor=0:100:0>; rel="next"; results="true"; cursor="0:100:0"`)
		return jsonResponse(t, http.StatusOK, header, []map[string]any{issuePayload(issue, 3)}), nil
	}))

	res, err := a.ListSelectable(context.Background(), adapter.ListOpts{
		Scope:  domain.ScopeIssues,
		View:   "assigned_to_me",
		State:  "resolved",
		Search: "checkout panic",
		Repo:   "web",
		Cursor: "0:0:0",
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("ListSelectable: %v", err)
	}
	if seenPath != "/api/0/organizations/acme/issues/" {
		t.Fatalf("path = %q, want org issues endpoint", seenPath)
	}
	if got := seenQuery.Get("query"); got != "assigned:me is:resolved project:web checkout panic" {
		t.Fatalf("query = %q", got)
	}
	if got := seenQuery["project"]; len(got) != 0 {
		t.Fatalf("project params = %#v, want none", got)
	}
	if got := seenQuery.Get("cursor"); got != "0:0:0" {
		t.Fatalf("cursor = %q", got)
	}
	if got := seenQuery.Get("limit"); got != "10" {
		t.Fatalf("limit = %q", got)
	}
	if !res.HasMore || res.NextCursor != "0:100:0" {
		t.Fatalf("pagination = %#v", res)
	}
	if len(res.Items) != 1 {
		t.Fatalf("items = %#v, want 1 item", res.Items)
	}
	item := res.Items[0]
	if item.ID != "101" || item.Identifier != "SEN-101" {
		t.Fatalf("item identifiers = %#v", item)
	}
	if item.Title != "Crash in checkout" {
		t.Fatalf("item title = %q", item.Title)
	}
	if item.ContainerRef != "web" {
		t.Fatalf("container = %q, want web", item.ContainerRef)
	}
	if item.Description != "culprit: svc.handler | level: error | status: unresolved | last_seen: 2024-01-03T04:05:06Z | url: https://sentry.example/issues/101" {
		t.Fatalf("description = %q", item.Description)
	}
	if item.URL != "https://sentry.example/issues/101" {
		t.Fatalf("url = %q", item.URL)
	}
	if got := item.Metadata["sentry_organization"]; got != "acme" {
		t.Fatalf("metadata organization = %v", got)
	}
}

func TestListSelectableOutsideAllowlistReturnsEmpty(t *testing.T) {
	t.Parallel()

	a := testAdapter(t, config.SentryConfig{Token: "token", Organization: "acme", Projects: []string{"web"}}, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected request: %s", req.URL.String())
		return nil, nil
	}))
	res, err := a.ListSelectable(context.Background(), adapter.ListOpts{Scope: domain.ScopeIssues, Repo: "api"})
	if err != nil {
		t.Fatalf("ListSelectable: %v", err)
	}
	if res == nil || len(res.Items) != 0 || res.TotalCount != 0 || res.HasMore || res.NextCursor != "" {
		t.Fatalf("result = %#v, want empty page", res)
	}
}

func TestResolveSingleIssue(t *testing.T) {
	t.Parallel()

	issue := testIssue("101", "SEN-101", "Crash in checkout", "web")
	a := testAdapter(t, config.SentryConfig{Token: "token", Organization: "acme", BaseURL: "https://sentry.example/api/0"}, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/api/0/organizations/acme/issues/101/" {
			t.Fatalf("path = %q, want detail endpoint", req.URL.Path)
		}
		return jsonResponse(t, http.StatusOK, nil, issuePayload(issue, 3)), nil
	}))

	item, err := a.Resolve(context.Background(), adapter.Selection{Scope: domain.ScopeIssues, ItemIDs: []string{"101"}})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if item.ExternalID != "SEN-acme-101" {
		t.Fatalf("ExternalID = %q", item.ExternalID)
	}
	if item.Source != "sentry" || item.SourceScope != domain.ScopeIssues {
		t.Fatalf("source = %q scope = %q", item.Source, item.SourceScope)
	}
	if !equalStrings(item.SourceItemIDs, []string{"101"}) {
		t.Fatalf("SourceItemIDs = %#v", item.SourceItemIDs)
	}
	if item.Title != "Crash in checkout" {
		t.Fatalf("Title = %q", item.Title)
	}
	for _, want := range []string{"SEN-101 - Crash in checkout", "project: web", "status: unresolved", "culprit: svc.handler", "events: 12", "users: 3", "url: https://sentry.example/issues/101"} {
		if !strings.Contains(item.Description, want) {
			t.Fatalf("description = %q, want %q", item.Description, want)
		}
	}
	refs, ok := item.Metadata["tracker_refs"].([]domain.TrackerReference)
	if !ok || len(refs) != 1 || refs[0].Provider != "sentry" || refs[0].ID != "SEN-101" {
		t.Fatalf("tracker refs = %#v", item.Metadata["tracker_refs"])
	}
}

func TestResolveMultipleIssuesAggregates(t *testing.T) {
	t.Parallel()

	issues := map[string]sentryIssue{
		"101": testIssue("101", "SEN-101", "Crash in checkout", "web"),
		"202": testIssue("202", "SEN-202", "Panic in billing", "api"),
	}
	a := testAdapter(t, config.SentryConfig{Token: "token", Organization: "acme", BaseURL: "https://sentry.example/api/0"}, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		issueID := strings.TrimSuffix(strings.TrimPrefix(req.URL.Path, "/api/0/organizations/acme/issues/"), "/")
		issue, ok := issues[issueID]
		if !ok {
			t.Fatalf("unexpected request path: %s", req.URL.Path)
		}
		return jsonResponse(t, http.StatusOK, nil, issue), nil
	}))

	item, err := a.Resolve(context.Background(), adapter.Selection{Scope: domain.ScopeIssues, ItemIDs: []string{"101", "202"}})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if item.ExternalID != "SEN-acme-101" {
		t.Fatalf("ExternalID = %q, want first issue external id", item.ExternalID)
	}
	if item.Title != "Crash in checkout (+1 more)" {
		t.Fatalf("Title = %q", item.Title)
	}
	if !equalStrings(item.SourceItemIDs, []string{"101", "202"}) {
		t.Fatalf("SourceItemIDs = %#v", item.SourceItemIDs)
	}
	for _, want := range []string{"SEN-101 - Crash in checkout", "SEN-202 - Panic in billing", "project: web", "project: api", "events: 12", "users: 3"} {
		if !strings.Contains(item.Description, want) {
			t.Fatalf("description = %q, want %q", item.Description, want)
		}
	}
	if got := item.Metadata["sentry_projects"]; !equalStrings(anyToStrings(got), []string{"api", "web"}) {
		t.Fatalf("sentry_projects = %#v", got)
	}
}

func TestFetchParsesExternalIDAndFallsBackToNumericIdentifier(t *testing.T) {
	t.Parallel()

	issue := testIssue("303", "", "Worker timeout", "ops")
	a := testAdapter(t, config.SentryConfig{Token: "token", Organization: "unused", BaseURL: "https://sentry.example/api/0"}, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/api/0/organizations/acme-prod/issues/303/" {
			t.Fatalf("path = %q, want org from external id", req.URL.Path)
		}
		return jsonResponse(t, http.StatusOK, nil, issuePayload(issue, 3)), nil
	}))

	item, err := a.Fetch(context.Background(), "SEN-acme-prod-303")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if item.ExternalID != "SEN-acme-prod-303" {
		t.Fatalf("ExternalID = %q", item.ExternalID)
	}
	if !strings.Contains(item.Description, "users: 3") {
		t.Fatalf("description = %q, want users line", item.Description)
	}
	if got := item.Metadata["sentry_identifier"]; got != "303" {
		t.Fatalf("sentry_identifier = %v, want fallback numeric ID", got)
	}
	refs, ok := item.Metadata["tracker_refs"].([]domain.TrackerReference)
	if !ok || len(refs) != 1 || refs[0].ID != "303" {
		t.Fatalf("tracker refs = %#v", item.Metadata["tracker_refs"])
	}
}

func TestSourceOnlyMethodsAreNoOps(t *testing.T) {
	t.Parallel()

	a := testAdapter(t, config.SentryConfig{Token: "token", Organization: "acme"}, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected request: %s", req.URL.String())
		return nil, nil
	}))

	ch, err := a.Watch(context.Background(), adapter.WorkItemFilter{})
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("watch channel open, want closed")
		}
	default:
		t.Fatal("watch channel was not closed")
	}
	if err := a.UpdateState(context.Background(), "SEN-acme-101", domain.TrackerStateDone); err != nil {
		t.Fatalf("UpdateState: %v", err)
	}
	if err := a.AddComment(context.Background(), "SEN-acme-101", "note"); err != nil {
		t.Fatalf("AddComment: %v", err)
	}
	if err := a.OnEvent(context.Background(), domain.SystemEvent{EventType: string(domain.EventPlanApproved), Payload: `{}`}); err != nil {
		t.Fatalf("OnEvent: %v", err)
	}
}

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
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

func anyToStrings(v any) []string {
	switch value := v.(type) {
	case []string:
		return append([]string(nil), value...)
	case []any:
		out := make([]string, 0, len(value))
		for _, item := range value {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

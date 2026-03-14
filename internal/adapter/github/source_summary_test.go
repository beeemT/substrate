package github

import (
	"context"
	"net/http"
	"testing"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/domain"
)

func TestResolveMultipleIssuesPersistsSourceSummaries(t *testing.T) {
	t.Parallel()

	a := newTestAdapter(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/user":
			return jsonResp(t, http.StatusOK, map[string]any{"login": "alice"}), nil
		case "/repos/acme/rocket/issues/42":
			return jsonResp(t, http.StatusOK, map[string]any{"number": 42, "title": "Fix auth", "state": "open", "labels": []any{}, "body": "Investigate auth timeouts", "html_url": "https://github.com/acme/rocket/issues/42", "repository": map[string]any{"full_name": "acme/rocket", "owner": map[string]any{"login": "acme"}, "name": "rocket"}}), nil
		case "/repos/acme/rocket/issues/43":
			return jsonResp(t, http.StatusOK, map[string]any{"number": 43, "title": "Repair billing", "state": "open", "labels": []any{}, "body": "Stabilize billing retries", "html_url": "https://github.com/acme/rocket/issues/43", "repository": map[string]any{"full_name": "acme/rocket", "owner": map[string]any{"login": "acme"}, "name": "rocket"}}), nil
		default:
			return jsonResp(t, http.StatusOK, map[string]any{}), nil
		}
	}))

	item, err := a.Resolve(context.Background(), adapter.Selection{Scope: domain.ScopeIssues, ItemIDs: []string{"acme/rocket#42", "acme/rocket#43"}})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	summaries, ok := item.Metadata["source_summaries"].([]domain.SourceSummary)
	if !ok || len(summaries) != 2 {
		t.Fatalf("source_summaries = %#v, want 2 typed summaries", item.Metadata["source_summaries"])
	}
	if summaries[0].Ref != "acme/rocket#42" || summaries[0].Title != "Fix auth" || summaries[0].Description != "Investigate auth timeouts" || summaries[0].State != "open" || summaries[0].Container != "acme/rocket" || summaries[0].URL == "" {
		t.Fatalf("summary[0] = %+v", summaries[0])
	}
	if summaries[1].Ref != "acme/rocket#43" || summaries[1].Title != "Repair billing" || summaries[1].Description != "Stabilize billing retries" || summaries[1].State != "open" || summaries[1].Container != "acme/rocket" || summaries[1].URL == "" {
		t.Fatalf("summary[1] = %+v", summaries[1])
	}
}

func TestResolveMilestonesPersistSourceSummaryURLs(t *testing.T) {
	t.Parallel()

	a := newTestAdapter(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/user":
			return jsonResp(t, http.StatusOK, map[string]any{"login": "alice"}), nil
		case "/repos/acme/rocket/milestones/1":
			return jsonResp(t, http.StatusOK, map[string]any{"number": 1, "title": "Sprint 1", "description": "Ship the login fixes", "state": "open", "html_url": "https://github.com/acme/rocket/milestone/1"}), nil
		default:
			return jsonResp(t, http.StatusOK, map[string]any{}), nil
		}
	}))

	item, err := a.Resolve(context.Background(), adapter.Selection{
		Scope:   domain.ScopeProjects,
		ItemIDs: []string{"1"},
		Metadata: map[string]any{
			"owner": "acme",
			"repo":  "rocket",
		},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	summaries, ok := item.Metadata["source_summaries"].([]domain.SourceSummary)
	if !ok || len(summaries) != 1 {
		t.Fatalf("source_summaries = %#v, want 1 typed summary", item.Metadata["source_summaries"])
	}
	if summaries[0].Ref != "acme/rocket milestone #1" || summaries[0].Title != "Sprint 1" || summaries[0].URL != "https://github.com/acme/rocket/milestone/1" {
		t.Fatalf("summary[0] = %+v", summaries[0])
	}
}

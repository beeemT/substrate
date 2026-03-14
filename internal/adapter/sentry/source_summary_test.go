package sentry

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/domain"
)

func TestResolveMultipleIssuesPersistsSourceSummaries(t *testing.T) {
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
	summaries, ok := item.Metadata["source_summaries"].([]domain.SourceSummary)
	if !ok || len(summaries) != 2 {
		t.Fatalf("source_summaries = %#v, want 2 typed summaries", item.Metadata["source_summaries"])
	}
	if summaries[0].Ref != "SEN-101" || summaries[0].Title != "Crash in checkout" || summaries[0].URL == "" {
		t.Fatalf("summary[0] = %+v", summaries[0])
	}
	if summaries[1].Ref != "SEN-202" || summaries[1].Title != "Panic in billing" || summaries[1].URL == "" {
		t.Fatalf("summary[1] = %+v", summaries[1])
	}
}

package gitlab

import (
	"context"
	"net/http"
	"testing"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/domain"
)

func TestResolveMultipleIssuesPersistsSourceSummaries(t *testing.T) {
	t.Parallel()

	a := makeAdapter(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/api/v4/projects/1234/issues/42":
			return jsonResponse(t, http.StatusOK, map[string]any{"iid": 42, "project_id": 1234, "title": "Fix auth", "description": "Investigate auth timeouts", "labels": []any{}, "web_url": "https://gitlab.example.com/acme/rocket/-/issues/42", "references": map[string]any{"full": "acme/rocket#42"}}), nil
		case "/api/v4/projects/1234/issues/43":
			return jsonResponse(t, http.StatusOK, map[string]any{"iid": 43, "project_id": 1234, "title": "Repair billing", "description": "Stabilize billing retries", "labels": []any{}, "web_url": "https://gitlab.example.com/acme/rocket/-/issues/43", "references": map[string]any{"full": "acme/rocket#43"}}), nil
		default:
			return jsonResponse(t, http.StatusOK, map[string]any{}), nil
		}
	}))

	item, err := a.Resolve(context.Background(), adapter.Selection{Scope: domain.ScopeIssues, ItemIDs: []string{"1234#42", "1234#43"}})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	summaries, ok := item.Metadata["source_summaries"].([]domain.SourceSummary)
	if !ok || len(summaries) != 2 {
		t.Fatalf("source_summaries = %#v, want 2 typed summaries", item.Metadata["source_summaries"])
	}
	if summaries[0].Ref != "acme/rocket#42" || summaries[0].Title != "Fix auth" || summaries[0].URL == "" {
		t.Fatalf("summary[0] = %+v", summaries[0])
	}
	if summaries[1].Ref != "acme/rocket#43" || summaries[1].Title != "Repair billing" || summaries[1].URL == "" {
		t.Fatalf("summary[1] = %+v", summaries[1])
	}
}

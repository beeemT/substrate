package linear

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/domain"
)

func TestResolveMultipleIssuesPersistsSourceSummaries(t *testing.T) {
	t.Parallel()

	issue1 := testIssueNode("abc123", "FOO-123", "Fix bug", []string{"backend"}, "FOO")
	issue2 := testIssueNode("def456", "FOO-456", "Add feature", []string{"frontend"}, "FOO")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		respondJSON(w, map[string]any{"data": map[string]any{"issues": map[string]any{"nodes": []any{issue1, issue2}}}})
	}))
	defer srv.Close()

	a := testLinearAdapter(t, srv.URL)
	wi, err := a.Resolve(context.Background(), adapter.Selection{Scope: domain.ScopeIssues, ItemIDs: []string{"abc123", "def456"}})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	summaries, ok := wi.Metadata["source_summaries"].([]domain.SourceSummary)
	if !ok || len(summaries) != 2 {
		t.Fatalf("source_summaries = %#v, want 2 typed summaries", wi.Metadata["source_summaries"])
	}
	if summaries[0].Ref != "FOO-123" || summaries[0].Title != "Fix bug" || summaries[0].Description != "Details for Fix bug" || summaries[0].State != "In Progress" || summaries[0].Container != "FOO" || len(summaries[0].Metadata) < 3 || summaries[0].URL == "" {
		t.Fatalf("summary[0] = %+v", summaries[0])
	}
	if summaries[1].Ref != "FOO-456" || summaries[1].Title != "Add feature" || summaries[1].Description != "Details for Add feature" || summaries[1].State != "In Progress" || summaries[1].Container != "FOO" || len(summaries[1].Metadata) < 3 || summaries[1].URL == "" {
		t.Fatalf("summary[1] = %+v", summaries[1])
	}
}

func TestResolveProjectPersistsSourceSummary(t *testing.T) {
	t.Parallel()

	issue := testIssueNode("abc123", "FOO-123", "Fix bug", []string{}, "FOO")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		respondJSON(w, map[string]any{"data": map[string]any{"project": map[string]any{"id": "proj1", "name": "Project Alpha", "description": "Desc", "state": "in_progress", "icon": "", "color": "", "issues": map[string]any{"nodes": []any{issue}}}}})
	}))
	defer srv.Close()

	a := testLinearAdapter(t, srv.URL)
	wi, err := a.Resolve(context.Background(), adapter.Selection{Scope: domain.ScopeProjects, ItemIDs: []string{"proj1"}})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	summaries, ok := wi.Metadata["source_summaries"].([]domain.SourceSummary)
	if !ok || len(summaries) != 1 {
		t.Fatalf("source_summaries = %#v, want 1 typed summary", wi.Metadata["source_summaries"])
	}
	if summaries[0].Ref != "proj1" || summaries[0].Title != "Project Alpha" || summaries[0].Description != "Desc" || summaries[0].State != "in_progress" {
		t.Fatalf("summary[0] = %+v", summaries[0])
	}
}

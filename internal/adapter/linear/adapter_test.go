package linear

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/domain"
)

// testGQLBody is the shape of a GraphQL request body sent to the mock server.
type testGQLBody struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
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
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		if item.State != "In Progress" {
			t.Errorf("State: want %q, got %q", "In Progress", item.State)
		}
		if len(item.Labels) != 1 || item.Labels[0] != "backend" {
			t.Errorf("Labels: want [backend], got %v", item.Labels)
		}
	})

	t.Run("projects", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
								"issues":      map[string]any{"nodes": []any{}},
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
	})

	t.Run("initiatives", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			respondJSON(w, map[string]any{
				"data": map[string]any{
					"initiatives": map[string]any{
						"nodes": []any{
							map[string]any{
								"id":          "init1",
								"name":        "Initiative Beta",
								"description": "Desc",
								"status":      "planned",
								"projects":    map[string]any{"nodes": []any{}},
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
	})
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
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

func TestWatchBackoff(t *testing.T) {
	var reqCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&reqCount, 1)
		if n <= 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		// After the first two 429s, return a valid response.
		// The Watch goroutine may call viewer first, then issues.
		var req testGQLBody
		_ = json.NewDecoder(r.Body).Decode(&req)
		switch {
		case strings.Contains(req.Query, "Viewer"):
			respondJSON(w, map[string]any{
				"data": map[string]any{
					"viewer": map[string]any{"id": "user1"},
				},
			})
		default:
			respondJSON(w, map[string]any{
				"data": map[string]any{
					"issues": map[string]any{"nodes": []any{}},
				},
			})
		}
	}))
	defer srv.Close()

	cfg := config.LinearConfig{
		APIKey:       "test-key",
		TeamID:       "team1",
		PollInterval: "50ms",
	}
	a := &LinearAdapter{
		cfg:    cfg,
		client: newGQLClient(cfg.APIKey, srv.URL),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	ch, err := a.Watch(ctx, adapter.WorkItemFilter{TeamID: "team1"})
	if err != nil {
		t.Fatalf("Watch returned error: %v", err)
	}

	timeout := time.After(500 * time.Millisecond)
	for {
		select {
		case event, ok := <-ch:
			if !ok {
				t.Fatal("Watch channel closed before any error event was received")
			}
			if event.Type == "error" {
				// Confirmed: error event received on 429 responses.
				return
			}
		case <-timeout:
			t.Fatal("timed out waiting for error event from Watch on 429 responses")
		}
	}
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

func TestResolveIssueTrackerRefs(t *testing.T) {
	issue := testIssueNode("abc123", "FOO-123", "Fix bug", []string{"backend"}, "FOO")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

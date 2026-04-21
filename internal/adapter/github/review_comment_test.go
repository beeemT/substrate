package github

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// graphqlRespRoundTripper builds a roundTripFunc that serves /user with an
// empty viewer payload and serves /graphql with the given response body.
func graphqlRespRoundTripper(t *testing.T, body any) roundTripFunc {
	t.Helper()
	return roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/user":
			return jsonResp(t, http.StatusOK, map[string]any{"login": "alice"}), nil
		case "/graphql":
			if req.Method != http.MethodPost {
				t.Fatalf("graphql method = %s, want POST", req.Method)
			}
			if got := req.Header.Get("Authorization"); !strings.HasPrefix(got, "Bearer ") {
				t.Fatalf("missing Bearer auth header: %q", got)
			}
			return jsonResp(t, http.StatusOK, body), nil
		default:
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
			return nil, nil
		}
	})
}

func graphqlReviewThreadsResp(threads []map[string]any) map[string]any {
	return map[string]any{
		"data": map[string]any{
			"repository": map[string]any{
				"pullRequest": map[string]any{
					"reviewThreads": map[string]any{
						"nodes": threads,
					},
				},
			},
		},
	}
}

func thread(resolved bool, comments ...map[string]any) map[string]any {
	return map[string]any{
		"isResolved": resolved,
		"comments": map[string]any{
			"nodes": comments,
		},
	}
}

func comment(id, body, path string, line int, login string) map[string]any {
	c := map[string]any{
		"id":        id,
		"body":      body,
		"url":       "https://github.com/acme/rocket/pull/1#discussion_r" + id,
		"createdAt": "2024-01-02T03:04:05Z",
		"author":    map[string]any{"login": login},
	}
	if path != "" {
		c["path"] = path
	}
	if line != 0 {
		c["line"] = line
	}
	return c
}

func TestFetchReviewComments_FiltersResolved(t *testing.T) {
	resp := graphqlReviewThreadsResp([]map[string]any{
		thread(false, comment("c1", "first body", "a.go", 10, "bob")),
		thread(true, comment("c2", "resolved body", "b.go", 20, "carol")),
		thread(false, comment("c3", "third body", "c.go", 30, "dave"), comment("c3b", "second of thread", "c.go", 31, "dave")),
	})
	a := newTestAdapter(t, graphqlRespRoundTripper(t, resp))
	got, err := a.FetchReviewComments(context.Background(), "acme/rocket", 1)
	if err != nil {
		t.Fatalf("FetchReviewComments: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("comments = %d, want 2: %+v", len(got), got)
	}
	if got[0].ID != "c1" || got[1].ID != "c3" {
		t.Fatalf("ids = %s,%s want c1,c3", got[0].ID, got[1].ID)
	}
	if got[0].ReviewerLogin != "bob" || got[0].Path != "a.go" || got[0].Line != 10 {
		t.Fatalf("first comment = %+v", got[0])
	}
}

func TestFetchReviewComments_TopLevelAndInline(t *testing.T) {
	resp := graphqlReviewThreadsResp([]map[string]any{
		thread(false, comment("top", "top-level", "", 0, "bob")),
		thread(false, comment("inl", "inline", "src/x.go", 42, "carol")),
	})
	a := newTestAdapter(t, graphqlRespRoundTripper(t, resp))
	got, err := a.FetchReviewComments(context.Background(), "acme/rocket", 1)
	if err != nil {
		t.Fatalf("FetchReviewComments: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("comments = %d, want 2", len(got))
	}
	if got[0].Path != "" || got[0].Line != 0 {
		t.Fatalf("top-level comment has path/line: %+v", got[0])
	}
	if got[1].Path != "src/x.go" || got[1].Line != 42 {
		t.Fatalf("inline comment = %+v", got[1])
	}
}

func TestFetchReviewComments_InvalidIdentifier(t *testing.T) {
	a := newTestAdapter(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/user" {
			return jsonResp(t, http.StatusOK, map[string]any{"login": "alice"}), nil
		}
		t.Fatalf("unexpected request: %s", req.URL.Path)
		return nil, nil
	}))
	_, err := a.FetchReviewComments(context.Background(), "no-slash", 1)
	if err == nil {
		t.Fatal("expected error for invalid identifier")
	}
	if !strings.Contains(err.Error(), "invalid github repo identifier") {
		t.Fatalf("error = %v, want contains 'invalid github repo identifier'", err)
	}
}

func TestFetchReviewComments_GraphQLErrorSurfaces(t *testing.T) {
	resp := map[string]any{
		"data":   nil,
		"errors": []any{map[string]any{"message": "bad"}},
	}
	a := newTestAdapter(t, graphqlRespRoundTripper(t, resp))
	_, err := a.FetchReviewComments(context.Background(), "acme/rocket", 1)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "graphql error") || !strings.Contains(err.Error(), "bad") {
		t.Fatalf("error = %v, want contains 'graphql error' and 'bad'", err)
	}
}

func TestFetchReviewComments_NilPR(t *testing.T) {
	resp := map[string]any{
		"data": map[string]any{
			"repository": map[string]any{
				"pullRequest": nil,
			},
		},
	}
	a := newTestAdapter(t, graphqlRespRoundTripper(t, resp))
	got, err := a.FetchReviewComments(context.Background(), "acme/rocket", 1)
	if err != nil {
		t.Fatalf("FetchReviewComments: %v", err)
	}
	if got != nil {
		t.Fatalf("comments = %+v, want nil", got)
	}
}

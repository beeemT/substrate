package glab

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"

	coreadapter "github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/config"
)

// glabReviewStub serves the per-endpoint payloads FetchReviewComments needs:
// the MR detail endpoint for web_url and the paginated discussions endpoint.
// emptyAfter caps how many discussions pages return data; subsequent pages
// return an empty array.
type glabReviewStub struct {
	web            string
	discussionsRaw [][]byte // page index → JSON payload
	discussionsErr error
	mrErr          error
	calls          []stubCall
}

func (s *glabReviewStub) run(_ context.Context, dir, name string, args ...string) ([]byte, error) {
	s.calls = append(s.calls, stubCall{dir: dir, name: name, args: args})

	if name != "glab" || len(args) < 2 || args[0] != "api" {
		return nil, fmt.Errorf("unexpected glab call: %v", args)
	}
	endpoint := args[1]
	switch {
	case strings.Contains(endpoint, "/discussions"):
		if s.discussionsErr != nil {
			return []byte("not authenticated"), s.discussionsErr
		}
		page := pageFromEndpoint(endpoint)
		if page-1 < len(s.discussionsRaw) {
			return s.discussionsRaw[page-1], nil
		}
		return []byte("[]"), nil
	case strings.Contains(endpoint, "/merge_requests/"):
		if s.mrErr != nil {
			return nil, s.mrErr
		}
		web := s.web
		if web == "" {
			web = "https://gitlab.example.com/group/repo/-/merge_requests/0"
		}
		return []byte(`{"web_url":"` + web + `"}`), nil
	}
	return nil, fmt.Errorf("unhandled endpoint: %s", endpoint)
}

// pageFromEndpoint extracts &page=N (1 when missing). Searches for the literal
// query parameter rather than substring "page=" to avoid matching per_page=N.
func pageFromEndpoint(endpoint string) int {
	for _, sep := range []string{"&page=", "?page="} {
		idx := strings.Index(endpoint, sep)
		if idx < 0 {
			continue
		}
		rest := endpoint[idx+len(sep):]
		if amp := strings.IndexByte(rest, '&'); amp >= 0 {
			rest = rest[:amp]
		}
		n, err := strconv.Atoi(rest)
		if err != nil || n <= 0 {
			return 1
		}
		return n
	}
	return 1
}

func newReviewAdapter(stub *glabReviewStub) *GlabAdapter {
	return newWithRunner(config.GlabConfig{}, coreadapter.ReviewArtifactRepos{}, "/tmp/ws", stub.run)
}

func TestFetchReviewComments_FiltersResolved(t *testing.T) {
	payload := []byte(`[
		{"id":"d1","notes":[{"id":1,"body":"resolved one","author":{"username":"alice"},"created_at":"2026-01-01T00:00:00Z","resolvable":true,"resolved":true}]},
		{"id":"d2","notes":[{"id":2,"body":"please fix","author":{"username":"bob"},"created_at":"2026-01-02T00:00:00Z","resolvable":true,"resolved":false,"position":{"new_path":"src/x.go","new_line":10}}]},
		{"id":"d3","notes":[{"id":3,"body":"general note","author":{"username":"carol"},"created_at":"2026-01-03T00:00:00Z","resolvable":false,"resolved":false}]}
	]`)
	stub := &glabReviewStub{web: "https://gitlab.example.com/group/repo/-/merge_requests/7", discussionsRaw: [][]byte{payload}}
	a := newReviewAdapter(stub)

	got, err := a.FetchReviewComments(context.Background(), "group/repo", 7)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d comments, want 2: %+v", len(got), got)
	}
	if got[0].ID != "2" || got[0].ReviewerLogin != "bob" || got[0].Path != "src/x.go" || got[0].Line != 10 {
		t.Errorf("unexpected first comment: %+v", got[0])
	}
	if got[0].URL != "https://gitlab.example.com/group/repo/-/merge_requests/7#note_2" {
		t.Errorf("first comment URL = %q, want note_2 link", got[0].URL)
	}
	if got[1].ID != "3" || got[1].ReviewerLogin != "carol" || got[1].Path != "" || got[1].Line != 0 {
		t.Errorf("unexpected second comment: %+v", got[1])
	}

	// Verify the runner was called with the expected glab api endpoints:
	// MR detail (for web_url) + discussions page 1.
	if len(stub.calls) < 2 {
		t.Fatalf("expected at least 2 runner calls, got %d", len(stub.calls))
	}
	discussionArgs := strings.Join(stub.calls[1].args, " ")
	if !strings.Contains(discussionArgs, "/projects/group%2Frepo/merge_requests/7/discussions") {
		t.Errorf("unexpected discussions call args: %q", discussionArgs)
	}
}

func TestFetchReviewComments_SkipsSystemNotes(t *testing.T) {
	payload := []byte(`[
		{"id":"d1","notes":[{"id":11,"body":"changed milestone","author":{"username":"alice"},"created_at":"2026-01-01T00:00:00Z","system":true}]}
	]`)
	stub := &glabReviewStub{discussionsRaw: [][]byte{payload}}
	a := newReviewAdapter(stub)

	got, err := a.FetchReviewComments(context.Background(), "g/r", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 comments, got %d: %+v", len(got), got)
	}
}

func TestFetchReviewComments_PositionMapping(t *testing.T) {
	payload := []byte(`[
		{"id":"d1","notes":[{"id":42,"body":"nit","author":{"username":"dave"},"created_at":"2026-02-01T12:34:56.789Z","resolvable":true,"resolved":false,"position":{"new_path":"src/main.go","new_line":42}}]}
	]`)
	stub := &glabReviewStub{discussionsRaw: [][]byte{payload}}
	a := newReviewAdapter(stub)

	got, err := a.FetchReviewComments(context.Background(), "g/r", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(got))
	}
	c := got[0]
	if c.Path != "src/main.go" || c.Line != 42 {
		t.Errorf("position mapping wrong: path=%q line=%d", c.Path, c.Line)
	}
	if c.CreatedAt.IsZero() {
		t.Errorf("expected parsed CreatedAt, got zero")
	}
}

// TestFetchReviewComments_OldLineFallback covers reviewer notes anchored to a
// removed line, where new_line is null and only old_path/old_line populated.
func TestFetchReviewComments_OldLineFallback(t *testing.T) {
	payload := []byte(`[
		{"id":"d1","notes":[{"id":7,"body":"why was this removed?","author":{"username":"eve"},"created_at":"2026-03-01T00:00:00Z","resolvable":true,"resolved":false,"position":{"old_path":"src/legacy.go","old_line":99}}]}
	]`)
	stub := &glabReviewStub{discussionsRaw: [][]byte{payload}}
	a := newReviewAdapter(stub)

	got, err := a.FetchReviewComments(context.Background(), "g/r", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(got))
	}
	c := got[0]
	if c.Path != "src/legacy.go" || c.Line != 99 {
		t.Errorf("old-line fallback wrong: path=%q line=%d, want src/legacy.go:99", c.Path, c.Line)
	}
}

// TestFetchReviewComments_Paginates verifies that multiple pages are fully
// consumed; PRs/MRs with >100 discussions used to silently truncate.
func TestFetchReviewComments_Paginates(t *testing.T) {
	// page 1: full page (100 discussions, all unresolved with single notes)
	var page1 strings.Builder
	page1.WriteByte('[')
	for i := 1; i <= 100; i++ {
		if i > 1 {
			page1.WriteByte(',')
		}
		fmt.Fprintf(&page1, `{"id":"d%d","notes":[{"id":%d,"body":"c%d","author":{"username":"u"},"created_at":"2026-01-01T00:00:00Z"}]}`, i, i, i)
	}
	page1.WriteByte(']')
	// page 2: short (1 discussion); no page 3 should be fetched.
	page2 := []byte(`[{"id":"d101","notes":[{"id":101,"body":"last","author":{"username":"u"},"created_at":"2026-01-02T00:00:00Z"}]}]`)
	stub := &glabReviewStub{discussionsRaw: [][]byte{[]byte(page1.String()), page2}}
	a := newReviewAdapter(stub)

	got, err := a.FetchReviewComments(context.Background(), "g/r", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 101 {
		t.Fatalf("expected 101 comments (pagination merged), got %d", len(got))
	}
	// MR detail call + 2 discussions pages = 3 calls. No page 3 because page 2
	// returned <100 results.
	discussionCalls := 0
	for _, c := range stub.calls {
		args := strings.Join(c.args, " ")
		if strings.Contains(args, "/discussions") {
			discussionCalls++
		}
	}
	if discussionCalls != 2 {
		t.Fatalf("expected 2 discussions calls, got %d", discussionCalls)
	}
}

func TestFetchReviewComments_RunnerError(t *testing.T) {
	stub := &glabReviewStub{discussionsErr: errors.New("boom")}
	a := newReviewAdapter(stub)

	_, err := a.FetchReviewComments(context.Background(), "g/r", 99)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "glab discussions for !99") {
		t.Errorf("error %q missing prefix %q", err.Error(), "glab discussions for !99")
	}
}

func TestFetchReviewComments_BadJSON(t *testing.T) {
	stub := &glabReviewStub{discussionsRaw: [][]byte{[]byte("[")}}
	a := newReviewAdapter(stub)

	_, err := a.FetchReviewComments(context.Background(), "g/r", 1)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "parse glab discussions") {
		t.Errorf("error %q missing %q", err.Error(), "parse glab discussions")
	}
}

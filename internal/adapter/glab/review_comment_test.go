package glab

import (
	"context"
	"errors"
	"strings"
	"testing"

	coreadapter "github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/config"
)

func newFetcherAdapter(stub *stubRunner) *GlabAdapter {
	return newWithRunner(config.GlabConfig{}, coreadapter.ReviewArtifactRepos{}, "/tmp/ws", stub.run)
}

func TestFetchReviewComments_FiltersResolved(t *testing.T) {
	payload := `[
		{"id":"d1","notes":[{"id":1,"body":"resolved one","author":{"username":"alice"},"created_at":"2026-01-01T00:00:00Z","resolvable":true,"resolved":true}]},
		{"id":"d2","notes":[{"id":2,"body":"please fix","author":{"username":"bob"},"created_at":"2026-01-02T00:00:00Z","resolvable":true,"resolved":false,"position":{"new_path":"src/x.go","new_line":10}}]},
		{"id":"d3","notes":[{"id":3,"body":"general note","author":{"username":"carol"},"created_at":"2026-01-03T00:00:00Z","resolvable":false,"resolved":false}]}
	]`
	stub := &stubRunner{output: []byte(payload)}
	a := newFetcherAdapter(stub)

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
	if got[1].ID != "3" || got[1].ReviewerLogin != "carol" || got[1].Path != "" || got[1].Line != 0 {
		t.Errorf("unexpected second comment: %+v", got[1])
	}

	// Verify the runner was called with the expected glab api endpoint.
	if len(stub.calls) != 1 {
		t.Fatalf("expected 1 runner call, got %d", len(stub.calls))
	}
	args := strings.Join(stub.calls[0].args, " ")
	if !strings.Contains(args, "api") || !strings.Contains(args, "/projects/group%2Frepo/merge_requests/7/discussions") {
		t.Errorf("unexpected args: %q", args)
	}
}

func TestFetchReviewComments_SkipsSystemNotes(t *testing.T) {
	payload := `[
		{"id":"d1","notes":[{"id":11,"body":"changed milestone","author":{"username":"alice"},"created_at":"2026-01-01T00:00:00Z","system":true}]}
	]`
	stub := &stubRunner{output: []byte(payload)}
	a := newFetcherAdapter(stub)

	got, err := a.FetchReviewComments(context.Background(), "g/r", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 comments, got %d: %+v", len(got), got)
	}
}

func TestFetchReviewComments_PositionMapping(t *testing.T) {
	payload := `[
		{"id":"d1","notes":[{"id":42,"body":"nit","author":{"username":"dave"},"created_at":"2026-02-01T12:34:56.789Z","resolvable":true,"resolved":false,"position":{"new_path":"src/main.go","new_line":42}}]}
	]`
	stub := &stubRunner{output: []byte(payload)}
	a := newFetcherAdapter(stub)

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

func TestFetchReviewComments_RunnerError(t *testing.T) {
	stub := &stubRunner{err: errors.New("boom"), output: []byte("not authenticated")}
	a := newFetcherAdapter(stub)

	_, err := a.FetchReviewComments(context.Background(), "g/r", 99)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "glab discussions for !99") {
		t.Errorf("error %q missing prefix %q", err.Error(), "glab discussions for !99")
	}
}

func TestFetchReviewComments_BadJSON(t *testing.T) {
	stub := &stubRunner{output: []byte("[")}
	a := newFetcherAdapter(stub)

	_, err := a.FetchReviewComments(context.Background(), "g/r", 1)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "parse glab discussions") {
		t.Errorf("error %q missing %q", err.Error(), "parse glab discussions")
	}
}

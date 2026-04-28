package adapter

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type fakeFetcher struct {
	provider string
	comments []ReviewComment
	err      error

	gotTarget ReviewCommentTarget
	calls     int
}

func (f *fakeFetcher) Provider() string { return f.provider }

func (f *fakeFetcher) FetchReviewComments(_ context.Context, target ReviewCommentTarget) ([]ReviewComment, error) {
	f.calls++
	f.gotTarget = target
	if f.err != nil {
		return nil, f.err
	}
	return f.comments, nil
}

func TestReviewCommentDispatcher_RoutesByProvider(t *testing.T) {
	gh := &fakeFetcher{
		provider: "github",
		comments: []ReviewComment{{ID: "gh-1", ReviewerLogin: "octocat", Body: "fix this", CreatedAt: time.Unix(1, 0)}},
	}
	gl := &fakeFetcher{
		provider: "gitlab",
		comments: []ReviewComment{{ID: "gl-1", ReviewerLogin: "tanuki", Body: "nit", CreatedAt: time.Unix(2, 0)}},
	}
	d := NewReviewCommentDispatcher(map[string]ReviewCommentFetcher{
		"github": gh,
		"gitlab": gl,
	})

	got, err := d.FetchReviewComments(context.Background(), "github", "owner/repo", 42)
	if err != nil {
		t.Fatalf("github fetch: unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].ID != "gh-1" {
		t.Fatalf("github fetch: unexpected comments: %+v", got)
	}
	if gh.calls != 1 || gh.gotTarget.Provider != "github" || gh.gotTarget.RepoIdentifier != "owner/repo" || gh.gotTarget.Number != 42 {
		t.Fatalf("github fetcher not invoked correctly: calls=%d target=%+v", gh.calls, gh.gotTarget)
	}
	if gl.calls != 0 {
		t.Fatalf("gitlab fetcher should not have been called, got calls=%d", gl.calls)
	}

	got, err = d.FetchReviewComments(context.Background(), "gitlab", "group/proj", 7)
	if err != nil {
		t.Fatalf("gitlab fetch: unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].ID != "gl-1" {
		t.Fatalf("gitlab fetch: unexpected comments: %+v", got)
	}
	if gl.calls != 1 || gl.gotTarget.Provider != "gitlab" || gl.gotTarget.RepoIdentifier != "group/proj" || gl.gotTarget.Number != 7 {
		t.Fatalf("gitlab fetcher not invoked correctly: calls=%d target=%+v", gl.calls, gl.gotTarget)
	}
}

func TestReviewCommentDispatcher_RoutesFullTargetContext(t *testing.T) {
	gl := &fakeFetcher{provider: "gitlab"}
	d := NewReviewCommentDispatcher(map[string]ReviewCommentFetcher{"gitlab": gl})

	_, err := d.FetchReviewCommentsForTarget(context.Background(), ReviewCommentTarget{
		Provider:       "gitlab",
		RepoIdentifier: "group/proj",
		Number:         7,
		WorktreePath:   "/workspace/proj",
	})
	if err != nil {
		t.Fatalf("gitlab fetch: unexpected error: %v", err)
	}
	if gl.gotTarget.WorktreePath != "/workspace/proj" {
		t.Fatalf("worktree path = %q, want /workspace/proj", gl.gotTarget.WorktreePath)
	}
}

func TestReviewCommentDispatcher_PropagatesFetcherError(t *testing.T) {
	wantErr := errors.New("boom")
	gh := &fakeFetcher{provider: "github", err: wantErr}
	d := NewReviewCommentDispatcher(map[string]ReviewCommentFetcher{"github": gh})

	_, err := d.FetchReviewComments(context.Background(), "github", "owner/repo", 1)
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected wrapped fetcher error, got %v", err)
	}
}

func TestReviewCommentDispatcher_UnknownProvider(t *testing.T) {
	d := NewReviewCommentDispatcher(map[string]ReviewCommentFetcher{
		"github": &fakeFetcher{provider: "github"},
	})

	_, err := d.FetchReviewComments(context.Background(), "bitbucket", "owner/repo", 1)
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
	if !strings.Contains(err.Error(), `"bitbucket"`) {
		t.Fatalf("error should name the missing provider, got: %v", err)
	}
}

func TestReviewCommentDispatcher_EmptyProvider(t *testing.T) {
	d := NewReviewCommentDispatcher(map[string]ReviewCommentFetcher{
		"github": &fakeFetcher{provider: "github"},
	})

	_, err := d.FetchReviewComments(context.Background(), "", "owner/repo", 1)
	if err == nil {
		t.Fatal("expected error for empty provider")
	}
	if !strings.Contains(err.Error(), "no review comment fetcher registered") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestReviewCommentDispatcher_NilMap(t *testing.T) {
	d := NewReviewCommentDispatcher(nil)

	_, err := d.FetchReviewComments(context.Background(), "github", "owner/repo", 1)
	if err == nil {
		t.Fatal("expected error from dispatcher with nil map")
	}
}

func TestReviewCommentDispatcher_NilReceiverDoesNotPanic(t *testing.T) {
	var d *ReviewCommentDispatcher

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("nil dispatcher panicked: %v", r)
		}
	}()

	_, err := d.FetchReviewComments(context.Background(), "github", "owner/repo", 1)
	if err == nil {
		t.Fatal("expected error from nil dispatcher")
	}
}

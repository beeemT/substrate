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

	gotRepo   string
	gotNumber int
	calls     int
}

func (f *fakeFetcher) Provider() string { return f.provider }

func (f *fakeFetcher) FetchReviewComments(_ context.Context, repoIdentifier string, number int) ([]ReviewComment, error) {
	f.calls++
	f.gotRepo = repoIdentifier
	f.gotNumber = number
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
	if gh.calls != 1 || gh.gotRepo != "owner/repo" || gh.gotNumber != 42 {
		t.Fatalf("github fetcher not invoked correctly: calls=%d repo=%q number=%d", gh.calls, gh.gotRepo, gh.gotNumber)
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
	if gl.calls != 1 || gl.gotRepo != "group/proj" || gl.gotNumber != 7 {
		t.Fatalf("gitlab fetcher not invoked correctly: calls=%d repo=%q number=%d", gl.calls, gl.gotRepo, gl.gotNumber)
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

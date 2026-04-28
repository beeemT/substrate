package views

import (
	"context"
	"testing"

	coreadapter "github.com/beeemT/substrate/internal/adapter"
)

func TestParseArtifactFetchArgsGitLabUsesProjectPathFromMRURL(t *testing.T) {
	t.Parallel()

	identifier, number, ok := parseArtifactFetchArgs(ArtifactItem{
		Provider: "gitlab",
		RepoName: "postback-service",
		Ref:      "!421",
		URL:      "https://gitlab.example.com/backend/postback-service/-/merge_requests/421?diff_id=123#note_456",
	})
	if !ok {
		t.Fatal("parseArtifactFetchArgs returned ok=false")
	}
	if identifier != "backend/postback-service" {
		t.Fatalf("identifier = %q, want %q", identifier, "backend/postback-service")
	}
	if number != 421 {
		t.Fatalf("number = %d, want 421", number)
	}
}

func TestParseArtifactFetchArgsGitHubUsesRepoFromPRURL(t *testing.T) {
	t.Parallel()

	identifier, number, ok := parseArtifactFetchArgs(ArtifactItem{
		Provider: "github",
		RepoName: "rocket",
		Ref:      "#42",
		URL:      "https://github.example.com/acme/rocket/pull/42/files#discussion_r123",
	})
	if !ok {
		t.Fatal("parseArtifactFetchArgs returned ok=false")
	}
	if identifier != "acme/rocket" {
		t.Fatalf("identifier = %q, want %q", identifier, "acme/rocket")
	}
	if number != 42 {
		t.Fatalf("number = %d, want 42", number)
	}
}

type captureReviewCommentFetcher struct {
	target coreadapter.ReviewCommentTarget
}

func (f *captureReviewCommentFetcher) Provider() string { return "gitlab" }

func (f *captureReviewCommentFetcher) FetchReviewComments(_ context.Context, target coreadapter.ReviewCommentTarget) ([]coreadapter.ReviewComment, error) {
	f.target = target
	return []coreadapter.ReviewComment{{ID: "note-1"}}, nil
}

func TestFetchReviewCommentsCmdPassesWorktreePathToFetcher(t *testing.T) {
	fetcher := &captureReviewCommentFetcher{}
	dispatcher := coreadapter.NewReviewCommentDispatcher(map[string]coreadapter.ReviewCommentFetcher{
		"gitlab": fetcher,
	})

	msg := FetchReviewCommentsCmd(dispatcher, "wi-1", []ArtifactItem{{
		ID:           "gitlab:backend.postback-service:!421",
		Provider:     "gitlab",
		RepoName:     "backend.postback-service",
		Ref:          "!421",
		URL:          "https://gitlab.example.com/justtrack/backend/postback-service/-/merge_requests/421",
		WorktreePath: "/workspace/backend.postback-service",
	}}, "", 1)()

	fetched, ok := msg.(ReviewCommentsFetchedMsg)
	if !ok {
		t.Fatalf("msg = %T, want ReviewCommentsFetchedMsg", msg)
	}
	if fetched.Err != nil {
		t.Fatalf("FetchReviewCommentsCmd err = %v", fetched.Err)
	}
	if fetcher.target.Provider != "gitlab" {
		t.Fatalf("provider = %q, want gitlab", fetcher.target.Provider)
	}
	if fetcher.target.RepoIdentifier != "justtrack/backend/postback-service" {
		t.Fatalf("repo identifier = %q, want justtrack/backend/postback-service", fetcher.target.RepoIdentifier)
	}
	if fetcher.target.Number != 421 {
		t.Fatalf("number = %d, want 421", fetcher.target.Number)
	}
	if fetcher.target.WorktreePath != "/workspace/backend.postback-service" {
		t.Fatalf("worktree path = %q, want /workspace/backend.postback-service", fetcher.target.WorktreePath)
	}
}

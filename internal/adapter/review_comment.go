package adapter

import (
	"context"
	"fmt"
	"time"
)

// ReviewComment is a normalized PR/MR review comment.
type ReviewComment struct {
	ID            string // provider-specific stable identifier (string form)
	ReviewerLogin string
	Body          string
	Path          string // empty for top-level comments
	Line          int    // 0 for top-level
	URL           string // direct link to the comment
	CreatedAt     time.Time
}

// ReviewCommentTarget identifies a PR/MR and optional local repository context.
// WorktreePath is used by CLI-backed providers whose host/auth resolution depends
// on the current repository directory.
type ReviewCommentTarget struct {
	Provider       string
	RepoIdentifier string
	Number         int
	WorktreePath   string
}

// ReviewCommentFetcher fetches unresolved review comments for one PR/MR.
// target.RepoIdentifier is provider-specific:
//   - GitHub: "owner/repo"
//   - GitLab: project path (URL-escaped by adapter)
//
// target.Number is the PR number / MR IID. Implementations MUST filter out
// resolved comments before returning.
type ReviewCommentFetcher interface {
	Provider() string
	FetchReviewComments(ctx context.Context, target ReviewCommentTarget) ([]ReviewComment, error)
}

// ReviewCommentDispatcher routes review-comment fetches to a per-provider
// fetcher implementation.
type ReviewCommentDispatcher struct {
	fetchers map[string]ReviewCommentFetcher
}

// NewReviewCommentDispatcher constructs a dispatcher from the given fetcher
// map. A nil map yields a dispatcher that always reports 'no fetcher
// registered'.
func NewReviewCommentDispatcher(fetchers map[string]ReviewCommentFetcher) *ReviewCommentDispatcher {
	return &ReviewCommentDispatcher{fetchers: fetchers}
}

// FetchReviewComments looks up the fetcher for the given provider and
// delegates. Returns an error when no fetcher is registered, including when
// the dispatcher or its fetcher map is nil.
func (d *ReviewCommentDispatcher) FetchReviewComments(ctx context.Context, provider, repoIdentifier string, number int) ([]ReviewComment, error) {
	return d.FetchReviewCommentsForTarget(ctx, ReviewCommentTarget{
		Provider:       provider,
		RepoIdentifier: repoIdentifier,
		Number:         number,
	})
}

// FetchReviewCommentsForTarget looks up the fetcher for target.Provider and
// delegates with the full target context.
func (d *ReviewCommentDispatcher) FetchReviewCommentsForTarget(ctx context.Context, target ReviewCommentTarget) ([]ReviewComment, error) {
	if d == nil || d.fetchers == nil {
		return nil, fmt.Errorf("no review comment fetcher registered for provider %q", target.Provider)
	}
	fetcher, ok := d.fetchers[target.Provider]
	if !ok {
		return nil, fmt.Errorf("no review comment fetcher registered for provider %q", target.Provider)
	}
	comments, err := fetcher.FetchReviewComments(ctx, target)
	if err != nil {
		return nil, fmt.Errorf("fetch review comments for %s %s#%d: %w", target.Provider, target.RepoIdentifier, target.Number, err)
	}
	return comments, nil
}

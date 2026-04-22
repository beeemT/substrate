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

// ReviewCommentFetcher fetches unresolved review comments for one PR/MR.
// repoIdentifier is provider-specific:
//   - GitHub: "owner/repo"
//   - GitLab: project path (URL-escaped by adapter)
//
// number is the PR number / MR IID. Implementations MUST filter out resolved
// comments before returning.
type ReviewCommentFetcher interface {
	Provider() string
	FetchReviewComments(ctx context.Context, repoIdentifier string, number int) ([]ReviewComment, error)
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
	if d == nil || d.fetchers == nil {
		return nil, fmt.Errorf("no review comment fetcher registered for provider %q", provider)
	}
	fetcher, ok := d.fetchers[provider]
	if !ok {
		return nil, fmt.Errorf("no review comment fetcher registered for provider %q", provider)
	}
	comments, err := fetcher.FetchReviewComments(ctx, repoIdentifier, number)
	if err != nil {
		return nil, fmt.Errorf("fetch review comments for %s %s#%d: %w", provider, repoIdentifier, number, err)
	}
	return comments, nil
}

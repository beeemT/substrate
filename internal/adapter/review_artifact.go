package adapter

import (
	"context"
	"time"

	"github.com/beeemT/substrate/internal/domain"
)

// ReviewArtifact describes a PR/MR discovered from durable review context.
type ReviewArtifact struct {
	Kind      string
	RepoName  string
	Ref       string
	URL       string
	State     string
	Draft     bool
	UpdatedAt time.Time
}

// ReviewArtifactResolver is an optional adapter capability for resolving
// repository review artifacts from persisted review context.
type ReviewArtifactResolver interface {
	ResolveReviewArtifact(ctx context.Context, review domain.ReviewRef, branch string) (*ReviewArtifact, error)
}

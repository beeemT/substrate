package github

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/domain"
)

type githubReviewPull struct {
	Number    int        `json:"number"`
	Title     string     `json:"title"`
	State     string     `json:"state"`
	Draft     bool       `json:"draft"`
	HTMLURL   string     `json:"html_url"`
	MergedAt  *time.Time `json:"merged_at"`
	UpdatedAt *time.Time `json:"updated_at"`
}

func (a *GithubAdapter) ResolveReviewArtifact(ctx context.Context, review domain.ReviewRef, branch string) (*adapter.ReviewArtifact, error) {
	baseOwner := strings.TrimSpace(review.BaseRepo.Owner)
	baseRepo := strings.TrimSpace(review.BaseRepo.Repo)
	headOwner := strings.TrimSpace(review.HeadRepo.Owner)
	branch = strings.TrimSpace(branch)
	if baseOwner == "" || baseRepo == "" || headOwner == "" || branch == "" {
		return nil, nil
	}
	pull, err := a.findPullByBranchState(ctx, baseOwner, baseRepo, headOwner, branch, "all")
	if err != nil || pull == nil {
		return nil, err
	}
	state := strings.TrimSpace(pull.State)
	if pull.MergedAt != nil {
		state = "merged"
	} else if pull.Draft && state == "open" {
		state = "draft"
	} else if state == "open" {
		state = "ready"
	}
	updatedAt := time.Time{}
	if pull.UpdatedAt != nil {
		updatedAt = *pull.UpdatedAt
	}
	return &adapter.ReviewArtifact{
		Kind:      "PR",
		RepoName:  firstNonEmptyReviewRepo(baseOwner, baseRepo),
		Ref:       fmt.Sprintf("#%d", pull.Number),
		URL:       strings.TrimSpace(pull.HTMLURL),
		State:     state,
		Draft:     pull.Draft,
		UpdatedAt: updatedAt,
	}, nil
}

func (a *GithubAdapter) findPullByBranchState(ctx context.Context, baseOwner, baseRepo, headOwner, branch, state string) (*githubReviewPull, error) {
	query := url.Values{"head": []string{headOwner + ":" + branch}}
	if strings.TrimSpace(state) != "" {
		query.Set("state", state)
	}
	var pulls []githubReviewPull
	if err := a.getJSON(ctx, fmt.Sprintf("/repos/%s/%s/pulls", baseOwner, baseRepo), query, &pulls); err != nil {
		return nil, err
	}
	if len(pulls) == 0 {
		return nil, nil
	}
	return &pulls[0], nil
}

func firstNonEmptyReviewRepo(owner, repo string) string {
	if owner != "" && repo != "" {
		return owner + "/" + repo
	}
	return strings.TrimSpace(repo)
}

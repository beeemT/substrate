package gitlab

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/domain"
)

type reviewMergeRequest struct {
	IID            int64      `json:"iid"`
	Title          string     `json:"title"`
	State          string     `json:"state"`
	WebURL         string     `json:"web_url"`
	Draft          bool       `json:"draft"`
	UpdatedAt      *time.Time `json:"updated_at"`
	MergedAt       *time.Time `json:"merged_at"`
	WorkInProgress bool       `json:"work_in_progress"`
}

func (a *GitlabAdapter) ResolveReviewArtifact(ctx context.Context, review domain.ReviewRef, branch string) (*adapter.ReviewArtifact, error) {
	branch = strings.TrimSpace(branch)
	projectPath := strings.TrimSpace(review.BaseRepo.Owner)
	if repo := strings.TrimSpace(review.BaseRepo.Repo); repo != "" {
		if projectPath != "" {
			projectPath += "/" + repo
		} else {
			projectPath = repo
		}
	}
	if projectPath == "" || branch == "" {
		return nil, nil
	}
	mrs, err := a.findMergeRequestsByBranch(ctx, projectPath, branch)
	if err != nil || len(mrs) == 0 {
		return nil, err
	}
	mr := mrs[0]
	state := strings.TrimSpace(mr.State)
	if mr.MergedAt != nil {
		state = "merged"
	} else if mr.Draft || mr.WorkInProgress {
		state = "draft"
	} else if state == "opened" {
		state = "ready"
	}
	updatedAt := time.Time{}
	if mr.UpdatedAt != nil {
		updatedAt = *mr.UpdatedAt
	}
	return &adapter.ReviewArtifact{
		Kind:      "MR",
		RepoName:  projectPath,
		Ref:       fmt.Sprintf("!%d", mr.IID),
		URL:       strings.TrimSpace(mr.WebURL),
		State:     state,
		Draft:     mr.Draft || mr.WorkInProgress,
		UpdatedAt: updatedAt,
	}, nil
}

func (a *GitlabAdapter) findMergeRequestsByBranch(ctx context.Context, projectPath, branch string) ([]reviewMergeRequest, error) {
	query := url.Values{}
	query.Set("state", "all")
	query.Set("source_branch", branch)
	var mrs []reviewMergeRequest
	endpoint := "/api/v4/projects/" + url.PathEscape(projectPath) + "/merge_requests"
	if err := a.getJSON(ctx, endpoint, query, &mrs); err != nil {
		return nil, err
	}
	return mrs, nil
}

package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/repository"
)

// ReviewArtifactRepos bundles the repositories needed for dual-write of review artifacts.
type ReviewArtifactRepos struct {
	Events           repository.EventRepository
	GithubPRs        repository.GithubPullRequestRepository
	GitlabMRs        repository.GitlabMergeRequestRepository
	SessionArtifacts repository.SessionReviewArtifactRepository
}

func PersistReviewArtifact(ctx context.Context, eventRepo repository.EventRepository, workspaceID, workItemID string, artifact domain.ReviewArtifact) error {
	if eventRepo == nil || strings.TrimSpace(workspaceID) == "" || strings.TrimSpace(workItemID) == "" {
		return nil
	}
	payload, err := json.Marshal(domain.ReviewArtifactEventPayload{WorkItemID: workItemID, Artifact: artifact})
	if err != nil {
		return fmt.Errorf("marshal review artifact payload: %w", err)
	}
	createdAt := artifact.UpdatedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}

	return eventRepo.Create(ctx, domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   string(domain.EventReviewArtifactRecorded),
		WorkspaceID: workspaceID,
		Payload:     string(payload),
		CreatedAt:   createdAt,
	})
}

// PersistGithubPR dual-writes a GitHub PR: event (audit trail) + provider table + link table.
func PersistGithubPR(ctx context.Context, repos ReviewArtifactRepos, workspaceID, workItemID string, artifact domain.ReviewArtifact, owner, repo string, number int) error {
	if err := PersistReviewArtifact(ctx, repos.Events, workspaceID, workItemID, artifact); err != nil {
		return err
	}
	if repos.GithubPRs == nil || repos.SessionArtifacts == nil {
		return nil
	}
	now := time.Now()
	pr := domain.GithubPullRequest{
		ID:         domain.NewID(),
		Owner:      owner,
		Repo:       repo,
		Number:     number,
		State:      artifact.State,
		Draft:      artifact.Draft,
		HeadBranch: artifact.Branch,
		HTMLURL:    artifact.URL,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := repos.GithubPRs.Upsert(ctx, pr); err != nil {
		return fmt.Errorf("upsert github pr: %w", err)
	}
	existing, err := repos.GithubPRs.GetByNumber(ctx, owner, repo, number)
	if err != nil {
		return fmt.Errorf("fetch github pr after upsert: %w", err)
	}
	link := domain.SessionReviewArtifact{
		ID:                 domain.NewID(),
		WorkspaceID:        workspaceID,
		WorkItemID:         workItemID,
		Provider:           "github",
		ProviderArtifactID: existing.ID,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if err := repos.SessionArtifacts.Upsert(ctx, link); err != nil {
		return fmt.Errorf("upsert github review artifact link: %w", err)
	}
	return nil
}

// PersistGitlabMR dual-writes a GitLab MR: event (audit trail) + provider table + link table.
func PersistGitlabMR(ctx context.Context, repos ReviewArtifactRepos, workspaceID, workItemID string, artifact domain.ReviewArtifact, projectPath string, iid int) error {
	if err := PersistReviewArtifact(ctx, repos.Events, workspaceID, workItemID, artifact); err != nil {
		return err
	}
	if repos.GitlabMRs == nil || repos.SessionArtifacts == nil || iid == 0 {
		return nil
	}
	now := time.Now()
	mr := domain.GitlabMergeRequest{
		ID:           domain.NewID(),
		ProjectPath:  projectPath,
		IID:          iid,
		State:        artifact.State,
		Draft:        artifact.Draft,
		SourceBranch: artifact.Branch,
		WebURL:       artifact.URL,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := repos.GitlabMRs.Upsert(ctx, mr); err != nil {
		return fmt.Errorf("upsert gitlab mr: %w", err)
	}
	existing, err := repos.GitlabMRs.GetByIID(ctx, projectPath, iid)
	if err != nil {
		return fmt.Errorf("fetch gitlab mr after upsert: %w", err)
	}
	link := domain.SessionReviewArtifact{
		ID:                 domain.NewID(),
		WorkspaceID:        workspaceID,
		WorkItemID:         workItemID,
		Provider:           "gitlab",
		ProviderArtifactID: existing.ID,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if err := repos.SessionArtifacts.Upsert(ctx, link); err != nil {
		return fmt.Errorf("upsert gitlab review artifact link: %w", err)
	}
	return nil
}

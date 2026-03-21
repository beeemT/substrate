package sqlite

import (
	"context"
	"fmt"

	"github.com/beeemT/go-atomic/generic"
	"github.com/beeemT/substrate/internal/domain"
)

type sessionReviewArtifactRow struct {
	ID                 string `db:"id"`
	WorkspaceID        string `db:"workspace_id"`
	WorkItemID         string `db:"work_item_id"`
	Provider           string `db:"provider"`
	ProviderArtifactID string `db:"provider_artifact_id"`
	CreatedAt          string `db:"created_at"`
	UpdatedAt          string `db:"updated_at"`
}

func (r *sessionReviewArtifactRow) toDomain() (domain.SessionReviewArtifact, error) {
	createdAt, err := parseTime(r.CreatedAt)
	if err != nil {
		return domain.SessionReviewArtifact{}, fmt.Errorf("created_at: %w", err)
	}
	updatedAt, err := parseTime(r.UpdatedAt)
	if err != nil {
		return domain.SessionReviewArtifact{}, fmt.Errorf("updated_at: %w", err)
	}

	return domain.SessionReviewArtifact{
		ID:                 r.ID,
		WorkspaceID:        r.WorkspaceID,
		WorkItemID:         r.WorkItemID,
		Provider:           r.Provider,
		ProviderArtifactID: r.ProviderArtifactID,
		CreatedAt:          createdAt,
		UpdatedAt:          updatedAt,
	}, nil
}

func rowFromSessionReviewArtifact(link domain.SessionReviewArtifact) sessionReviewArtifactRow {
	return sessionReviewArtifactRow{
		ID:                 link.ID,
		WorkspaceID:        link.WorkspaceID,
		WorkItemID:         link.WorkItemID,
		Provider:           link.Provider,
		ProviderArtifactID: link.ProviderArtifactID,
		CreatedAt:          formatTime(link.CreatedAt),
		UpdatedAt:          formatTime(link.UpdatedAt),
	}
}

// SessionReviewArtifactRepo implements repository.SessionReviewArtifactRepository using SQLite.
type SessionReviewArtifactRepo struct{ remote generic.SQLXRemote }

func NewSessionReviewArtifactRepo(remote generic.SQLXRemote) SessionReviewArtifactRepo {
	return SessionReviewArtifactRepo{remote: remote}
}

func (r SessionReviewArtifactRepo) Upsert(ctx context.Context, link domain.SessionReviewArtifact) error {
	row := rowFromSessionReviewArtifact(link)
	_, err := r.remote.NamedExecContext(ctx,
		`INSERT INTO session_review_artifacts (id, workspace_id, work_item_id, provider, provider_artifact_id, created_at, updated_at)
		 VALUES (:id, :workspace_id, :work_item_id, :provider, :provider_artifact_id, :created_at, :updated_at)
		 ON CONFLICT(workspace_id, work_item_id, provider, provider_artifact_id) DO UPDATE SET
		   updated_at = excluded.updated_at`, row)
	if err != nil {
		return fmt.Errorf("upsert session review artifact %s: %w", link.ID, err)
	}

	return nil
}

func (r SessionReviewArtifactRepo) ListByWorkItemID(ctx context.Context, workItemID string) ([]domain.SessionReviewArtifact, error) {
	var rows []sessionReviewArtifactRow
	if err := r.remote.SelectContext(ctx, &rows,
		`SELECT * FROM session_review_artifacts WHERE work_item_id = ? ORDER BY created_at`, workItemID); err != nil {
		return nil, fmt.Errorf("list session review artifacts for work item %s: %w", workItemID, err)
	}

	return r.toDomainSlice(rows)
}

func (r SessionReviewArtifactRepo) ListByWorkspaceID(ctx context.Context, workspaceID string) ([]domain.SessionReviewArtifact, error) {
	var rows []sessionReviewArtifactRow
	if err := r.remote.SelectContext(ctx, &rows,
		`SELECT * FROM session_review_artifacts WHERE workspace_id = ? ORDER BY created_at`, workspaceID); err != nil {
		return nil, fmt.Errorf("list session review artifacts for workspace %s: %w", workspaceID, err)
	}

	return r.toDomainSlice(rows)
}

func (r SessionReviewArtifactRepo) toDomainSlice(rows []sessionReviewArtifactRow) ([]domain.SessionReviewArtifact, error) {
	links := make([]domain.SessionReviewArtifact, len(rows))
	for i := range rows {
		link, err := rows[i].toDomain()
		if err != nil {
			return nil, fmt.Errorf("convert session review artifact: %w", err)
		}
		links[i] = link
	}

	return links, nil
}

package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/beeemT/go-atomic/generic"
	"github.com/beeemT/substrate/internal/domain"
)

type documentationRow struct {
	ID             string  `db:"id"`
	WorkspaceID    string  `db:"workspace_id"`
	SourceType     string  `db:"source_type"`
	Path           *string `db:"path"`
	RepoURL        *string `db:"repo_url"`
	RepositoryName *string `db:"repository_name"`
	Branch         *string `db:"branch"`
	Description    *string `db:"description"`
	LastSynced     *string `db:"last_synced"`
	CreatedAt      string  `db:"created_at"`
}

func (r *documentationRow) toDomain() (domain.DocumentationSource, error) {
	lastSyncedAt, err := parseTimePtr(r.LastSynced)
	if err != nil {
		return domain.DocumentationSource{}, fmt.Errorf("last_synced: %w", err)
	}
	createdAt, err := parseTime(r.CreatedAt)
	if err != nil {
		return domain.DocumentationSource{}, fmt.Errorf("created_at: %w", err)
	}
	return domain.DocumentationSource{
		ID:             r.ID,
		WorkspaceID:    r.WorkspaceID,
		RepositoryName: derefStr(r.RepositoryName),
		Type:           domain.DocumentationSourceType(r.SourceType),
		Path:           derefStr(r.Path),
		RepoURL:        derefStr(r.RepoURL),
		Branch:         derefStr(r.Branch),
		Description:    derefStr(r.Description),
		LastSyncedAt:   lastSyncedAt,
		CreatedAt:      createdAt,
	}, nil
}

func rowFromDocumentation(ds domain.DocumentationSource) documentationRow {
	return documentationRow{
		ID:             ds.ID,
		WorkspaceID:    ds.WorkspaceID,
		SourceType:     string(ds.Type),
		Path:           strPtr(ds.Path),
		RepoURL:        strPtr(ds.RepoURL),
		RepositoryName: strPtr(ds.RepositoryName),
		Branch:         strPtr(ds.Branch),
		Description:    strPtr(ds.Description),
		LastSynced:     formatTimePtr(ds.LastSyncedAt),
		CreatedAt:      formatTime(ds.CreatedAt),
	}
}

// DocumentationRepo implements repository.DocumentationRepository using SQLite.
type DocumentationRepo struct{ remote generic.SQLXRemote }

func NewDocumentationRepo(remote generic.SQLXRemote) DocumentationRepo {
	return DocumentationRepo{remote: remote}
}

func (r DocumentationRepo) Get(ctx context.Context, id string) (domain.DocumentationSource, error) {
	var row documentationRow
	if err := r.remote.GetContext(ctx, &row, `SELECT * FROM documentation_sources WHERE id = ?`, id); err != nil {
		return domain.DocumentationSource{}, fmt.Errorf("get documentation source %s: %w", id, err)
	}
	return row.toDomain()
}

func (r DocumentationRepo) ListByWorkspaceID(ctx context.Context, workspaceID string) ([]domain.DocumentationSource, error) {
	var rows []documentationRow
	if err := r.remote.SelectContext(ctx, &rows, `SELECT * FROM documentation_sources WHERE workspace_id = ? ORDER BY created_at`, workspaceID); err != nil {
		return nil, fmt.Errorf("list documentation sources for workspace %s: %w", workspaceID, err)
	}
	sources := make([]domain.DocumentationSource, len(rows))
	for i := range rows {
		src, err := rows[i].toDomain()
		if err != nil {
			return nil, fmt.Errorf("convert documentation source: %w", err)
		}
		sources[i] = src
	}
	return sources, nil
}

func (r DocumentationRepo) Create(ctx context.Context, ds domain.DocumentationSource) error {
	row := rowFromDocumentation(ds)
	_, err := r.remote.NamedExecContext(ctx,
		`INSERT INTO documentation_sources (id, workspace_id, source_type, path, repo_url, repository_name, branch, description, last_synced, created_at)
		 VALUES (:id, :workspace_id, :source_type, :path, :repo_url, :repository_name, :branch, :description, :last_synced, :created_at)`, row)
	if err != nil {
		return fmt.Errorf("create documentation source %s: %w", ds.ID, err)
	}
	return nil
}

func (r DocumentationRepo) Update(ctx context.Context, ds domain.DocumentationSource) error {
	row := rowFromDocumentation(ds)
	res, err := r.remote.NamedExecContext(ctx,
		`UPDATE documentation_sources SET workspace_id = :workspace_id, source_type = :source_type,
		 path = :path, repo_url = :repo_url, repository_name = :repository_name, branch = :branch,
		 description = :description, last_synced = :last_synced WHERE id = :id`, row)
	if err != nil {
		return fmt.Errorf("update documentation source %s: %w", ds.ID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update documentation source %s: get rows affected: %w", ds.ID, err)
	}
	if n == 0 {
		return fmt.Errorf("update documentation source %s: %w", ds.ID, sql.ErrNoRows)
	}
	return nil
}

func (r DocumentationRepo) Delete(ctx context.Context, id string) error {
	_, err := r.remote.NamedExecContext(ctx, `DELETE FROM documentation_sources WHERE id = :id`, map[string]any{"id": id})
	if err != nil {
		return fmt.Errorf("delete documentation source %s: %w", id, err)
	}
	return nil
}

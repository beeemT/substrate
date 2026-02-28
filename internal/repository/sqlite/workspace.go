package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/beeemT/go-atomic/generic"
	"github.com/beeemT/substrate/internal/domain"
)

type workspaceRow struct {
	ID        string `db:"id"`
	Name      string `db:"name"`
	RootPath  string `db:"root_path"`
	Status    string `db:"status"`
	CreatedAt string `db:"created_at"`
	UpdatedAt string `db:"updated_at"`
}

func (r *workspaceRow) toDomain() (domain.Workspace, error) {
	createdAt, err := parseTime(r.CreatedAt)
	if err != nil {
		return domain.Workspace{}, fmt.Errorf("created_at: %w", err)
	}
	updatedAt, err := parseTime(r.UpdatedAt)
	if err != nil {
		return domain.Workspace{}, fmt.Errorf("updated_at: %w", err)
	}
	return domain.Workspace{
		ID:        r.ID,
		Name:      r.Name,
		RootPath:  r.RootPath,
		Status:    domain.WorkspaceStatus(r.Status),
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
	}, nil
}

func rowFromWorkspace(ws domain.Workspace) workspaceRow {
	return workspaceRow{
		ID:        ws.ID,
		Name:      ws.Name,
		RootPath:  ws.RootPath,
		Status:    string(ws.Status),
		CreatedAt: formatTime(ws.CreatedAt),
		UpdatedAt: formatTime(ws.UpdatedAt),
	}
}

// WorkspaceRepo implements repository.WorkspaceRepository using SQLite.
type WorkspaceRepo struct{ remote generic.SQLXRemote }

func NewWorkspaceRepo(remote generic.SQLXRemote) WorkspaceRepo {
	return WorkspaceRepo{remote: remote}
}

func (r WorkspaceRepo) Get(ctx context.Context, id string) (domain.Workspace, error) {
	var row workspaceRow
	if err := r.remote.GetContext(ctx, &row, `SELECT * FROM workspaces WHERE id = ?`, id); err != nil {
		return domain.Workspace{}, fmt.Errorf("get workspace %s: %w", id, err)
	}
	return row.toDomain()
}

func (r WorkspaceRepo) Create(ctx context.Context, ws domain.Workspace) error {
	row := rowFromWorkspace(ws)
	_, err := r.remote.NamedExecContext(ctx,
		`INSERT INTO workspaces (id, name, root_path, status, created_at, updated_at)
		 VALUES (:id, :name, :root_path, :status, :created_at, :updated_at)`, row)
	if err != nil {
		return fmt.Errorf("create workspace %s: %w", ws.ID, err)
	}
	return nil
}

func (r WorkspaceRepo) Update(ctx context.Context, ws domain.Workspace) error {
	row := rowFromWorkspace(ws)
	res, err := r.remote.NamedExecContext(ctx,
		`UPDATE workspaces SET name = :name, root_path = :root_path, status = :status,
		 updated_at = :updated_at WHERE id = :id`, row)
	if err != nil {
		return fmt.Errorf("update workspace %s: %w", ws.ID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update workspace %s: get rows affected: %w", ws.ID, err)
	}
	if n == 0 {
		return fmt.Errorf("update workspace %s: %w", ws.ID, sql.ErrNoRows)
	}
	return nil
}

func (r WorkspaceRepo) Delete(ctx context.Context, id string) error {
	_, err := r.remote.NamedExecContext(ctx, `DELETE FROM workspaces WHERE id = :id`, map[string]any{"id": id})
	if err != nil {
		return fmt.Errorf("delete workspace %s: %w", id, err)
	}
	return nil
}

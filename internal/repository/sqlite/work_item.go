package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/beeemT/go-atomic/generic"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/repository"
)

type workItemRow struct {
	ID            string  `db:"id"`
	WorkspaceID   string  `db:"workspace_id"`
	ExternalID    *string `db:"external_id"`
	Source        string  `db:"source"`
	SourceScope   *string `db:"source_scope"`
	Title         string  `db:"title"`
	Description   *string `db:"description"`
	AssigneeID    *string `db:"assignee_id"`
	State         string  `db:"state"`
	Labels        *string `db:"labels"`
	SourceItemIDs *string `db:"source_item_ids"`
	Metadata      *string `db:"metadata"`
	CreatedAt     string  `db:"created_at"`
	UpdatedAt     string  `db:"updated_at"`
}

func (r *workItemRow) toDomain() (domain.Session, error) {
	labels, err := unmarshalStringSlice(r.Labels)
	if err != nil {
		return domain.Session{}, fmt.Errorf("unmarshal labels: %w", err)
	}
	sourceItemIDs, err := unmarshalStringSlice(r.SourceItemIDs)
	if err != nil {
		return domain.Session{}, fmt.Errorf("unmarshal source_item_ids: %w", err)
	}
	metadata, err := unmarshalMap(r.Metadata)
	if err != nil {
		return domain.Session{}, fmt.Errorf("unmarshal metadata: %w", err)
	}
	createdAt, err := parseTime(r.CreatedAt)
	if err != nil {
		return domain.Session{}, fmt.Errorf("created_at: %w", err)
	}
	updatedAt, err := parseTime(r.UpdatedAt)
	if err != nil {
		return domain.Session{}, fmt.Errorf("updated_at: %w", err)
	}

	return domain.Session{
		ID:            r.ID,
		WorkspaceID:   r.WorkspaceID,
		ExternalID:    derefStr(r.ExternalID),
		Source:        r.Source,
		SourceScope:   domain.SelectionScope(derefStr(r.SourceScope)),
		Title:         r.Title,
		Description:   derefStr(r.Description),
		AssigneeID:    derefStr(r.AssigneeID),
		State:         domain.SessionState(r.State),
		Labels:        labels,
		SourceItemIDs: sourceItemIDs,
		Metadata:      metadata,
		CreatedAt:     createdAt,
		UpdatedAt:     updatedAt,
	}, nil
}

func rowFromWorkItem(item domain.Session) (workItemRow, error) {
	labels, err := marshalStringSlice(item.Labels)
	if err != nil {
		return workItemRow{}, fmt.Errorf("marshal labels: %w", err)
	}
	sourceItemIDs, err := marshalStringSlice(item.SourceItemIDs)
	if err != nil {
		return workItemRow{}, fmt.Errorf("marshal source_item_ids: %w", err)
	}
	metadata, err := marshalMap(item.Metadata)
	if err != nil {
		return workItemRow{}, fmt.Errorf("marshal metadata: %w", err)
	}

	return workItemRow{
		ID:            item.ID,
		WorkspaceID:   item.WorkspaceID,
		ExternalID:    strPtr(item.ExternalID),
		Source:        item.Source,
		SourceScope:   strPtr(string(item.SourceScope)),
		Title:         item.Title,
		Description:   strPtr(item.Description),
		AssigneeID:    strPtr(item.AssigneeID),
		State:         string(item.State),
		Labels:        labels,
		SourceItemIDs: sourceItemIDs,
		Metadata:      metadata,
		CreatedAt:     formatTime(item.CreatedAt),
		UpdatedAt:     formatTime(item.UpdatedAt),
	}, nil
}

// SessionRepo implements repository.WorkItemRepository using SQLite.
type SessionRepo struct{ remote generic.SQLXRemote }

// NewSessionRepo creates a new WorkItemRepo.
func NewSessionRepo(remote generic.SQLXRemote) SessionRepo {
	return SessionRepo{remote: remote}
}

func (r SessionRepo) Get(ctx context.Context, id string) (domain.Session, error) {
	var row workItemRow
	if err := r.remote.GetContext(ctx, &row, `SELECT * FROM work_items WHERE id = ?`, id); err != nil {
		return domain.Session{}, fmt.Errorf("get work item %s: %w", id, err)
	}

	return row.toDomain()
}

func (r SessionRepo) List(ctx context.Context, filter repository.SessionFilter) ([]domain.Session, error) {
	query := `SELECT * FROM work_items WHERE 1=1`
	var args []any
	if filter.WorkspaceID != nil {
		query += ` AND workspace_id = ?`
		args = append(args, *filter.WorkspaceID)
	}
	if filter.ExternalID != nil {
		query += ` AND external_id = ?`
		args = append(args, *filter.ExternalID)
	}
	if filter.State != nil {
		query += ` AND state = ?`
		args = append(args, string(*filter.State))
	}
	if filter.Source != nil {
		query += ` AND source = ?`
		args = append(args, *filter.Source)
	}
	query += ` ORDER BY created_at DESC`

	limit := filter.Limit
	if limit == 0 {
		limit = 100 // default cap to prevent unbounded scans
	}
	query += ` LIMIT ? OFFSET ?`
	args = append(args, limit, filter.Offset)

	var rows []workItemRow
	if err := r.remote.SelectContext(ctx, &rows, query, args...); err != nil {
		return nil, fmt.Errorf("list work items: %w", err)
	}
	items := make([]domain.Session, 0, len(rows))
	for i := range rows {
		item, err := rows[i].toDomain()
		if err != nil {
			return nil, fmt.Errorf("convert work item: %w", err)
		}
		items = append(items, item)
	}

	return items, nil
}

func (r SessionRepo) Create(ctx context.Context, item domain.Session) error {
	row, err := rowFromWorkItem(item)
	if err != nil {
		return fmt.Errorf("create work item %s: %w", item.ID, err)
	}
	_, err = r.remote.NamedExecContext(ctx,
		`INSERT INTO work_items
		 (id, workspace_id, external_id, source, source_scope, title, description, assignee_id,
		  state, labels, source_item_ids, metadata, created_at, updated_at)
		 VALUES
		 (:id, :workspace_id, :external_id, :source, :source_scope, :title, :description, :assignee_id,
		  :state, :labels, :source_item_ids, :metadata, :created_at, :updated_at)`, row)
	if err != nil {
		return fmt.Errorf("create work item %s: %w", item.ID, err)
	}

	return nil
}

func (r SessionRepo) Update(ctx context.Context, item domain.Session) error {
	row, err := rowFromWorkItem(item)
	if err != nil {
		return fmt.Errorf("update work item %s: %w", item.ID, err)
	}
	res, err := r.remote.NamedExecContext(ctx,
		`UPDATE work_items SET
		 workspace_id = :workspace_id, external_id = :external_id, source = :source,
		 source_scope = :source_scope, title = :title, description = :description,
		 assignee_id = :assignee_id, state = :state, labels = :labels,
		 source_item_ids = :source_item_ids, metadata = :metadata, updated_at = :updated_at
		 WHERE id = :id`, row)
	if err != nil {
		return fmt.Errorf("update work item %s: %w", item.ID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update work item %s: get rows affected: %w", item.ID, err)
	}
	if n == 0 {
		return fmt.Errorf("update work item %s: %w", item.ID, sql.ErrNoRows)
	}

	return nil
}

func (r SessionRepo) Delete(ctx context.Context, id string) error {
	_, err := r.remote.NamedExecContext(ctx, `DELETE FROM work_items WHERE id = :id`, map[string]any{"id": id})
	if err != nil {
		return fmt.Errorf("delete work item %s: %w", id, err)
	}

	return nil
}

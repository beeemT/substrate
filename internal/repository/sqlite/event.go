package sqlite

import (
	"context"
	"fmt"

	"github.com/beeemT/go-atomic/generic"
	"github.com/beeemT/substrate/internal/domain"
)

type eventRow struct {
	ID          string  `db:"id"`
	EventType   string  `db:"event_type"`
	WorkspaceID *string `db:"workspace_id"`
	Payload     string  `db:"payload"`
	CreatedAt   string  `db:"created_at"`
}

func (r *eventRow) toDomain() (domain.SystemEvent, error) {
	createdAt, err := parseTime(r.CreatedAt)
	if err != nil {
		return domain.SystemEvent{}, fmt.Errorf("created_at: %w", err)
	}

	return domain.SystemEvent{
		ID:          r.ID,
		EventType:   r.EventType,
		WorkspaceID: derefStr(r.WorkspaceID),
		Payload:     r.Payload,
		CreatedAt:   createdAt,
	}, nil
}

func rowFromEvent(e domain.SystemEvent) eventRow {
	return eventRow{
		ID:          e.ID,
		EventType:   e.EventType,
		WorkspaceID: strPtr(e.WorkspaceID),
		Payload:     e.Payload,
		CreatedAt:   formatTime(e.CreatedAt),
	}
}

// EventRepo implements repository.EventRepository using SQLite.
type EventRepo struct{ remote generic.SQLXRemote }

func NewEventRepo(remote generic.SQLXRemote) EventRepo {
	return EventRepo{remote: remote}
}

func (r EventRepo) Create(ctx context.Context, e domain.SystemEvent) error {
	row := rowFromEvent(e)
	_, err := r.remote.NamedExecContext(ctx,
		`INSERT INTO system_events (id, event_type, workspace_id, payload, created_at)
		 VALUES (:id, :event_type, :workspace_id, :payload, :created_at)`, row)
	if err != nil {
		return fmt.Errorf("create event %s: %w", e.ID, err)
	}

	return nil
}

func (r EventRepo) ListByType(ctx context.Context, eventType string, limit int) ([]domain.SystemEvent, error) {
	query := `SELECT * FROM system_events WHERE event_type = ? ORDER BY created_at DESC`
	var args []any
	args = append(args, eventType)
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	var rows []eventRow
	if err := r.remote.SelectContext(ctx, &rows, query, args...); err != nil {
		return nil, fmt.Errorf("list events by type %s: %w", eventType, err)
	}
	events := make([]domain.SystemEvent, len(rows))
	for i := range rows {
		ev, err := rows[i].toDomain()
		if err != nil {
			return nil, fmt.Errorf("convert event: %w", err)
		}
		events[i] = ev
	}

	return events, nil
}

func (r EventRepo) ListByWorkspaceID(ctx context.Context, workspaceID string, limit int) ([]domain.SystemEvent, error) {
	query := `SELECT * FROM system_events WHERE workspace_id = ? ORDER BY created_at DESC`
	var args []any
	args = append(args, workspaceID)
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	var rows []eventRow
	if err := r.remote.SelectContext(ctx, &rows, query, args...); err != nil {
		return nil, fmt.Errorf("list events for workspace %s: %w", workspaceID, err)
	}
	events := make([]domain.SystemEvent, len(rows))
	for i := range rows {
		ev, err := rows[i].toDomain()
		if err != nil {
			return nil, fmt.Errorf("convert event: %w", err)
		}
		events[i] = ev
	}

	return events, nil
}

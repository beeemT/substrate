package sqlite

import (
	"context"
	"fmt"
	"time"

	"github.com/beeemT/go-atomic/generic"
	"github.com/beeemT/substrate/internal/domain"
)

type eventRow struct {
	ID          string  `db:"id"`
	Sequence    uint64  `db:"sequence"`
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
		Sequence:    r.Sequence,
		EventType:   r.EventType,
		WorkspaceID: derefStr(r.WorkspaceID),
		Payload:     r.Payload,
		CreatedAt:   createdAt,
	}, nil
}

func rowFromEvent(e domain.SystemEvent) eventRow {
	return eventRow{
		ID:          e.ID,
		Sequence:    e.Sequence,
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

// Create persists a system event with automatic retry on SQLITE_BUSY.
// This handles concurrent database access without requiring transaction coordination.
//
// Idempotency: if an event with the same ID already exists, the existing
// persisted row (including its assigned sequence) is returned and no new row
// is created. This makes Create safe to retry on transient failures and lets
// the bus treat a duplicate publish as the same logical event.
func (r EventRepo) Create(ctx context.Context, e domain.SystemEvent) (domain.SystemEvent, error) {
	row := rowFromEvent(e)
	var lastErr error
	for i, backoff := range eventRetryBackoffs {
		if i > 0 {
			time.Sleep(backoff)
		}
		var created eventRow
		err := r.remote.GetContext(ctx, &created,
			`INSERT INTO system_events (id, sequence, event_type, workspace_id, payload, created_at)
			 VALUES (?, (SELECT COALESCE(MAX(sequence), 0) + 1 FROM system_events), ?, ?, ?, ?)
			 ON CONFLICT(id) DO UPDATE SET id = system_events.id
			 RETURNING id, sequence, event_type, workspace_id, payload, created_at`,
			row.ID, row.EventType, row.WorkspaceID, row.Payload, row.CreatedAt)
		if err == nil {
			event, convertErr := created.toDomain()
			if convertErr != nil {
				return domain.SystemEvent{}, fmt.Errorf("convert created event: %w", convertErr)
			}
			return event, nil
		}
		if !isSQLiteBusyOrLocked(err) {
			return domain.SystemEvent{}, fmt.Errorf("create event %s: %w", e.ID, err)
		}
		lastErr = err
	}
	return domain.SystemEvent{}, fmt.Errorf("create event %s: %w (after retries)", e.ID, lastErr)
}

func (r EventRepo) ListByType(ctx context.Context, eventType string, limit int) ([]domain.SystemEvent, error) {
	query := `SELECT * FROM system_events WHERE event_type = ? ORDER BY sequence DESC`
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
	query := `SELECT * FROM system_events WHERE workspace_id = ? ORDER BY sequence DESC`
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

func (r EventRepo) ListByWorkspaceIDAfterSequence(ctx context.Context, workspaceID string, afterSequence uint64, limit int) ([]domain.SystemEvent, error) {
	query := `SELECT * FROM system_events WHERE workspace_id = ? AND sequence > ? ORDER BY sequence ASC`
	args := []any{workspaceID, afterSequence}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	var rows []eventRow
	if err := r.remote.SelectContext(ctx, &rows, query, args...); err != nil {
		return nil, fmt.Errorf("list events for workspace %s after sequence %d: %w", workspaceID, afterSequence, err)
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

func (r EventRepo) LatestSequence(ctx context.Context, workspaceID string) (uint64, error) {
	var sequence uint64
	if err := r.remote.GetContext(ctx, &sequence, `SELECT COALESCE(MAX(sequence), 0) FROM system_events WHERE workspace_id = ?`, workspaceID); err != nil {
		return 0, fmt.Errorf("latest event sequence for workspace %s: %w", workspaceID, err)
	}
	return sequence, nil
}

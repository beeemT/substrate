package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/beeemT/go-atomic/generic"
	"github.com/beeemT/substrate/internal/domain"
)

type sessionContinuationRow struct {
	ID             string  `db:"id"`
	AgentSessionID string  `db:"agent_session_id"`
	WorkItemID     string  `db:"work_item_id"`
	SubPlanID      *string `db:"sub_plan_id"`
	Kind           string  `db:"kind"`
	Status         string  `db:"status"`
	Attempt        int     `db:"attempt"`
	LastError      string  `db:"last_error"`
	StartedAt      *string `db:"started_at"`
	CompletedAt    *string `db:"completed_at"`
	CreatedAt      string  `db:"created_at"`
	UpdatedAt      string  `db:"updated_at"`
}

func (r *sessionContinuationRow) toDomain() (domain.AgentSessionContinuation, error) {
	startedAt, err := parseTimePtr(r.StartedAt)
	if err != nil {
		return domain.AgentSessionContinuation{}, fmt.Errorf("started_at: %w", err)
	}
	completedAt, err := parseTimePtr(r.CompletedAt)
	if err != nil {
		return domain.AgentSessionContinuation{}, fmt.Errorf("completed_at: %w", err)
	}
	createdAt, err := parseTime(r.CreatedAt)
	if err != nil {
		return domain.AgentSessionContinuation{}, fmt.Errorf("created_at: %w", err)
	}
	updatedAt, err := parseTime(r.UpdatedAt)
	if err != nil {
		return domain.AgentSessionContinuation{}, fmt.Errorf("updated_at: %w", err)
	}

	return domain.AgentSessionContinuation{
		ID:             r.ID,
		AgentSessionID: r.AgentSessionID,
		WorkItemID:     r.WorkItemID,
		SubPlanID:      derefStr(r.SubPlanID),
		Kind:           r.Kind,
		Status:         domain.AgentSessionContinuationStatus(r.Status),
		Attempt:        r.Attempt,
		LastError:      r.LastError,
		StartedAt:      startedAt,
		CompletedAt:    completedAt,
		CreatedAt:      createdAt,
		UpdatedAt:      updatedAt,
	}, nil
}

func rowFromSessionContinuation(c domain.AgentSessionContinuation) sessionContinuationRow {
	return sessionContinuationRow{
		ID:             c.ID,
		AgentSessionID: c.AgentSessionID,
		WorkItemID:     c.WorkItemID,
		SubPlanID:      strPtr(c.SubPlanID),
		Kind:           c.Kind,
		Status:         string(c.Status),
		Attempt:        c.Attempt,
		LastError:      c.LastError,
		StartedAt:      formatTimePtr(c.StartedAt),
		CompletedAt:    formatTimePtr(c.CompletedAt),
		CreatedAt:      formatTime(c.CreatedAt),
		UpdatedAt:      formatTime(c.UpdatedAt),
	}
}

// SessionContinuationRepo implements repository.AgentSessionContinuationRepository using SQLite.
type SessionContinuationRepo struct{ remote generic.SQLXRemote }

func NewSessionContinuationRepo(remote generic.SQLXRemote) SessionContinuationRepo {
	return SessionContinuationRepo{remote: remote}
}

func (r SessionContinuationRepo) Get(ctx context.Context, id string) (domain.AgentSessionContinuation, error) {
	var row sessionContinuationRow
	if err := r.remote.GetContext(ctx, &row, `SELECT * FROM agent_session_continuations WHERE id = ?`, id); err != nil {
		return domain.AgentSessionContinuation{}, fmt.Errorf("get agent session continuation %s: %w", id, err)
	}
	return row.toDomain()
}

func (r SessionContinuationRepo) GetActive(ctx context.Context, agentSessionID, kind string) (domain.AgentSessionContinuation, error) {
	var row sessionContinuationRow
	if err := r.remote.GetContext(ctx, &row, `SELECT * FROM agent_session_continuations WHERE agent_session_id = ? AND kind = ? AND status IN ('pending', 'running', 'failed') ORDER BY attempt DESC LIMIT 1`, agentSessionID, kind); err != nil {
		return domain.AgentSessionContinuation{}, fmt.Errorf("get active continuation for session %s kind %s: %w", agentSessionID, kind, err)
	}
	return row.toDomain()
}

func (r SessionContinuationRepo) ListRecoverable(ctx context.Context, workspaceID string) ([]domain.AgentSessionContinuation, error) {
	var rows []sessionContinuationRow
	if err := r.remote.SelectContext(ctx, &rows, `SELECT c.* FROM agent_session_continuations AS c JOIN agent_sessions AS s ON s.id = c.agent_session_id WHERE s.workspace_id = ? AND c.status IN ('pending', 'running', 'failed') ORDER BY c.updated_at, c.created_at`, workspaceID); err != nil {
		return nil, fmt.Errorf("list recoverable continuations for workspace %s: %w", workspaceID, err)
	}
	items := make([]domain.AgentSessionContinuation, len(rows))
	for i := range rows {
		c, err := rows[i].toDomain()
		if err != nil {
			return nil, fmt.Errorf("convert continuation: %w", err)
		}
		items[i] = c
	}
	return items, nil
}

func (r SessionContinuationRepo) Create(ctx context.Context, c domain.AgentSessionContinuation) error {
	row := rowFromSessionContinuation(c)
	_, err := r.remote.NamedExecContext(ctx, `INSERT INTO agent_session_continuations (id, agent_session_id, work_item_id, sub_plan_id, kind, status, attempt, last_error, started_at, completed_at, created_at, updated_at)
		VALUES (:id, :agent_session_id, :work_item_id, :sub_plan_id, :kind, :status, :attempt, :last_error, :started_at, :completed_at, :created_at, :updated_at)`, row)
	if err != nil {
		return fmt.Errorf("create agent session continuation %s: %w", c.ID, err)
	}
	return nil
}

func (r SessionContinuationRepo) Update(ctx context.Context, c domain.AgentSessionContinuation) error {
	row := rowFromSessionContinuation(c)
	res, err := r.remote.NamedExecContext(ctx, `UPDATE agent_session_continuations SET agent_session_id = :agent_session_id, work_item_id = :work_item_id, sub_plan_id = :sub_plan_id, kind = :kind, status = :status, attempt = :attempt, last_error = :last_error, started_at = :started_at, completed_at = :completed_at, updated_at = :updated_at WHERE id = :id`, row)
	if err != nil {
		return fmt.Errorf("update agent session continuation %s: %w", c.ID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update agent session continuation %s: get rows affected: %w", c.ID, err)
	}
	if n == 0 {
		return fmt.Errorf("update agent session continuation %s: %w", c.ID, sql.ErrNoRows)
	}
	return nil
}

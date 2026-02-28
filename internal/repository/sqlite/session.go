package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/beeemT/go-atomic/generic"
	"github.com/beeemT/substrate/internal/domain"
)

type sessionRow struct {
	ID              string  `db:"id"`
	SubPlanID       string  `db:"sub_plan_id"`
	WorkspaceID     string  `db:"workspace_id"`
	RepositoryName  string  `db:"repository_name"`
	HarnessName     string  `db:"harness_name"`
	WorktreeDir     string  `db:"worktree_dir"`
	PID             *int    `db:"pid"`
	Status          string  `db:"status"`
	ExitCode        *int    `db:"exit_code"`
	StartedAt       *string `db:"started_at"`
	ShutdownAt      *string `db:"shutdown_at"`
	CompletedAt     *string `db:"completed_at"`
	CreatedAt       string  `db:"created_at"`
	OwnerInstanceID *string `db:"owner_instance_id"`
	UpdatedAt       string  `db:"updated_at"`
}

func (r *sessionRow) toDomain() (domain.AgentSession, error) {
	startedAt, err := parseTimePtr(r.StartedAt)
	if err != nil {
		return domain.AgentSession{}, fmt.Errorf("started_at: %w", err)
	}
	shutdownAt, err := parseTimePtr(r.ShutdownAt)
	if err != nil {
		return domain.AgentSession{}, fmt.Errorf("shutdown_at: %w", err)
	}
	completedAt, err := parseTimePtr(r.CompletedAt)
	if err != nil {
		return domain.AgentSession{}, fmt.Errorf("completed_at: %w", err)
	}
	createdAt, err := parseTime(r.CreatedAt)
	if err != nil {
		return domain.AgentSession{}, fmt.Errorf("created_at: %w", err)
	}
	updatedAt, err := parseTime(r.UpdatedAt)
	if err != nil {
		return domain.AgentSession{}, fmt.Errorf("updated_at: %w", err)
	}
	return domain.AgentSession{
		ID:              r.ID,
		SubPlanID:       r.SubPlanID,
		WorkspaceID:     r.WorkspaceID,
		RepositoryName:  r.RepositoryName,
		HarnessName:     r.HarnessName,
		WorktreePath:    r.WorktreeDir,
		PID:             r.PID,
		Status:          domain.AgentSessionStatus(r.Status),
		ExitCode:        r.ExitCode,
		StartedAt:       startedAt,
		ShutdownAt:      shutdownAt,
		CompletedAt:     completedAt,
		OwnerInstanceID: r.OwnerInstanceID,
		CreatedAt:       createdAt,
		UpdatedAt:       updatedAt,
	}, nil
}

func rowFromSession(s domain.AgentSession) sessionRow {
	return sessionRow{
		ID:              s.ID,
		SubPlanID:       s.SubPlanID,
		WorkspaceID:     s.WorkspaceID,
		RepositoryName:  s.RepositoryName,
		HarnessName:     s.HarnessName,
		WorktreeDir:     s.WorktreePath,
		PID:             s.PID,
		Status:          string(s.Status),
		ExitCode:        s.ExitCode,
		StartedAt:       formatTimePtr(s.StartedAt),
		ShutdownAt:      formatTimePtr(s.ShutdownAt),
		CompletedAt:     formatTimePtr(s.CompletedAt),
		OwnerInstanceID: s.OwnerInstanceID,
		CreatedAt:       formatTime(s.CreatedAt),
		UpdatedAt:       formatTime(s.UpdatedAt),
	}
}

// SessionRepo implements repository.SessionRepository using SQLite.
type SessionRepo struct{ remote generic.SQLXRemote }

func NewSessionRepo(remote generic.SQLXRemote) SessionRepo {
	return SessionRepo{remote: remote}
}

func (r SessionRepo) Get(ctx context.Context, id string) (domain.AgentSession, error) {
	var row sessionRow
	if err := r.remote.GetContext(ctx, &row, `SELECT * FROM agent_sessions WHERE id = ?`, id); err != nil {
		return domain.AgentSession{}, fmt.Errorf("get session %s: %w", id, err)
	}
	return row.toDomain()
}

func (r SessionRepo) ListBySubPlanID(ctx context.Context, subPlanID string) ([]domain.AgentSession, error) {
	var rows []sessionRow
	if err := r.remote.SelectContext(ctx, &rows, `SELECT * FROM agent_sessions WHERE sub_plan_id = ? ORDER BY created_at`, subPlanID); err != nil {
		return nil, fmt.Errorf("list sessions for sub-plan %s: %w", subPlanID, err)
	}
	sessions := make([]domain.AgentSession, len(rows))
	for i := range rows {
		s, err := rows[i].toDomain()
		if err != nil {
			return nil, fmt.Errorf("convert session: %w", err)
		}
		sessions[i] = s
	}
	return sessions, nil
}

func (r SessionRepo) ListByWorkspaceID(ctx context.Context, workspaceID string) ([]domain.AgentSession, error) {
	var rows []sessionRow
	if err := r.remote.SelectContext(ctx, &rows, `SELECT * FROM agent_sessions WHERE workspace_id = ? ORDER BY created_at`, workspaceID); err != nil {
		return nil, fmt.Errorf("list sessions for workspace %s: %w", workspaceID, err)
	}
	sessions := make([]domain.AgentSession, len(rows))
	for i := range rows {
		s, err := rows[i].toDomain()
		if err != nil {
			return nil, fmt.Errorf("convert session: %w", err)
		}
		sessions[i] = s
	}
	return sessions, nil
}

func (r SessionRepo) Create(ctx context.Context, s domain.AgentSession) error {
	row := rowFromSession(s)
	_, err := r.remote.NamedExecContext(ctx,
		`INSERT INTO agent_sessions
		 (id, sub_plan_id, workspace_id, repository_name, harness_name, worktree_dir,
		  pid, status, exit_code, started_at, shutdown_at, completed_at, created_at,
		  owner_instance_id, updated_at)
		 VALUES
		 (:id, :sub_plan_id, :workspace_id, :repository_name, :harness_name, :worktree_dir,
		  :pid, :status, :exit_code, :started_at, :shutdown_at, :completed_at, :created_at,
		  :owner_instance_id, :updated_at)`, row)
	if err != nil {
		return fmt.Errorf("create session %s: %w", s.ID, err)
	}
	return nil
}

func (r SessionRepo) Update(ctx context.Context, s domain.AgentSession) error {
	row := rowFromSession(s)
	res, err := r.remote.NamedExecContext(ctx,
		`UPDATE agent_sessions SET sub_plan_id = :sub_plan_id, workspace_id = :workspace_id,
		 repository_name = :repository_name, harness_name = :harness_name, worktree_dir = :worktree_dir,
		 pid = :pid, status = :status, exit_code = :exit_code, started_at = :started_at,
		 shutdown_at = :shutdown_at, completed_at = :completed_at, owner_instance_id = :owner_instance_id,
		 updated_at = :updated_at WHERE id = :id`, row)
	if err != nil {
		return fmt.Errorf("update session %s: %w", s.ID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update session %s: get rows affected: %w", s.ID, err)
	}
	if n == 0 {
		return fmt.Errorf("update session %s: %w", s.ID, sql.ErrNoRows)
	}
	return nil
}

func (r SessionRepo) Delete(ctx context.Context, id string) error {
	_, err := r.remote.NamedExecContext(ctx, `DELETE FROM agent_sessions WHERE id = :id`, map[string]any{"id": id})
	if err != nil {
		return fmt.Errorf("delete session %s: %w", id, err)
	}
	return nil
}

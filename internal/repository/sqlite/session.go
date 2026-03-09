package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

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

type sessionHistoryRow struct {
	SessionID          string  `db:"session_id"`
	WorkspaceID        string  `db:"workspace_id"`
	WorkspaceName      string  `db:"workspace_name"`
	WorkItemID         string  `db:"work_item_id"`
	WorkItemExternalID *string `db:"work_item_external_id"`
	WorkItemTitle      string  `db:"work_item_title"`
	WorkItemState      string  `db:"work_item_state"`
	RepositoryName     string  `db:"repository_name"`
	HarnessName        string  `db:"harness_name"`
	Status             string  `db:"status"`
	AgentSessionCount  int     `db:"agent_session_count"`
	HasOpenQuestion    int     `db:"has_open_question"`
	HasInterrupted     int     `db:"has_interrupted"`
	CreatedAt          string  `db:"created_at"`
	UpdatedAt          string  `db:"updated_at"`
	CompletedAt        *string `db:"completed_at"`
}

func (r *sessionHistoryRow) toDomain() (domain.SessionHistoryEntry, error) {
	createdAt, err := parseTime(r.CreatedAt)
	if err != nil {
		return domain.SessionHistoryEntry{}, fmt.Errorf("created_at: %w", err)
	}
	updatedAt, err := parseTime(r.UpdatedAt)
	if err != nil {
		return domain.SessionHistoryEntry{}, fmt.Errorf("updated_at: %w", err)
	}
	completedAt, err := parseTimePtr(r.CompletedAt)
	if err != nil {
		return domain.SessionHistoryEntry{}, fmt.Errorf("completed_at: %w", err)
	}
	return domain.SessionHistoryEntry{
		SessionID:          r.SessionID,
		WorkspaceID:        r.WorkspaceID,
		WorkspaceName:      r.WorkspaceName,
		WorkItemID:         r.WorkItemID,
		WorkItemExternalID: derefStr(r.WorkItemExternalID),
		WorkItemTitle:      r.WorkItemTitle,
		WorkItemState:      domain.WorkItemState(r.WorkItemState),
		RepositoryName:     r.RepositoryName,
		HarnessName:        r.HarnessName,
		Status:             domain.AgentSessionStatus(r.Status),
		AgentSessionCount:  r.AgentSessionCount,
		HasOpenQuestion:    r.HasOpenQuestion != 0,
		HasInterrupted:     r.HasInterrupted != 0,
		CreatedAt:          createdAt,
		UpdatedAt:          updatedAt,
		CompletedAt:        completedAt,
	}, nil
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

func (r SessionRepo) ListByOwnerInstanceID(ctx context.Context, instanceID string) ([]domain.AgentSession, error) {
	var rows []sessionRow
	if err := r.remote.SelectContext(ctx, &rows, `SELECT * FROM agent_sessions WHERE owner_instance_id = ? ORDER BY created_at`, instanceID); err != nil {
		return nil, fmt.Errorf("list sessions for instance %s: %w", instanceID, err)
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

func (r SessionRepo) SearchHistory(ctx context.Context, filter domain.SessionHistoryFilter) ([]domain.SessionHistoryEntry, error) {
	query := `WITH latest_session AS (
		SELECT
			p.work_item_id AS work_item_id,
			s.id AS session_id,
			s.repository_name AS repository_name,
			s.harness_name AS harness_name,
			s.status AS status,
			s.completed_at AS completed_at,
			ROW_NUMBER() OVER (
				PARTITION BY p.work_item_id
				ORDER BY s.updated_at DESC, s.created_at DESC, s.id DESC
			) AS rn
		FROM agent_sessions s
		JOIN sub_plans sp ON sp.id = s.sub_plan_id
		JOIN plans p ON p.id = sp.plan_id
	), session_stats AS (
		SELECT
			p.work_item_id AS work_item_id,
			COUNT(*) AS agent_session_count,
			MAX(s.updated_at) AS latest_session_updated_at,
			MAX(CASE WHEN s.status = 'waiting_for_answer' THEN 1 ELSE 0 END) AS has_open_question,
			MAX(CASE WHEN s.status = 'interrupted' THEN 1 ELSE 0 END) AS has_interrupted
		FROM agent_sessions s
		JOIN sub_plans sp ON sp.id = s.sub_plan_id
		JOIN plans p ON p.id = sp.plan_id
		GROUP BY p.work_item_id
	)
	SELECT
		COALESCE(ls.session_id, '') AS session_id,
		wi.workspace_id AS workspace_id,
		w.name AS workspace_name,
		wi.id AS work_item_id,
		wi.external_id AS work_item_external_id,
		wi.title AS work_item_title,
		wi.state AS work_item_state,
		COALESCE(ls.repository_name, '') AS repository_name,
		COALESCE(ls.harness_name, '') AS harness_name,
		COALESCE(ls.status, '') AS status,
		COALESCE(ss.agent_session_count, 0) AS agent_session_count,
		COALESCE(ss.has_open_question, 0) AS has_open_question,
		COALESCE(ss.has_interrupted, 0) AS has_interrupted,
		wi.created_at AS created_at,
		CASE
			WHEN ss.latest_session_updated_at IS NOT NULL AND ss.latest_session_updated_at > wi.updated_at THEN ss.latest_session_updated_at
			ELSE wi.updated_at
		END AS updated_at,
		ls.completed_at AS completed_at
	FROM work_items wi
	JOIN workspaces w ON w.id = wi.workspace_id
	LEFT JOIN latest_session ls ON ls.work_item_id = wi.id AND ls.rn = 1
	LEFT JOIN session_stats ss ON ss.work_item_id = wi.id
	WHERE 1=1`
	var args []any
	if filter.WorkspaceID != nil {
		query += ` AND wi.workspace_id = ?`
		args = append(args, *filter.WorkspaceID)
	}
	search := strings.ToLower(strings.TrimSpace(filter.Search))
	if search != "" {
		like := "%" + search + "%"
		query += ` AND (
			lower(wi.id) LIKE ? OR
			lower(COALESCE(wi.external_id, '')) LIKE ? OR
			lower(wi.title) LIKE ? OR
			lower(wi.state) LIKE ? OR
			lower(w.name) LIKE ? OR
			lower(COALESCE(ls.session_id, '')) LIKE ? OR
			lower(COALESCE(ls.repository_name, '')) LIKE ? OR
			lower(COALESCE(ls.harness_name, '')) LIKE ? OR
			lower(COALESCE(ls.status, '')) LIKE ?
		)`
		args = append(args, like, like, like, like, like, like, like, like, like)
	}
	query += ` ORDER BY updated_at DESC, wi.created_at DESC, wi.id`
	limit := filter.Limit
	if limit == 0 {
		limit = 100
	}
	query += ` LIMIT ? OFFSET ?`
	args = append(args, limit, filter.Offset)

	var rows []sessionHistoryRow
	if err := r.remote.SelectContext(ctx, &rows, query, args...); err != nil {
		return nil, fmt.Errorf("search session history: %w", err)
	}
	entries := make([]domain.SessionHistoryEntry, 0, len(rows))
	for i := range rows {
		entry, err := rows[i].toDomain()
		if err != nil {
			return nil, fmt.Errorf("convert session history entry: %w", err)
		}
		entries = append(entries, entry)
	}
	return entries, nil
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

package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/beeemT/go-atomic/generic"
	"github.com/beeemT/substrate/internal/domain"
)

type sessionRow struct {
	ID              string  `db:"id"`
	WorkItemID      string  `db:"work_item_id"`
	SubPlanID       *string `db:"sub_plan_id"`
	WorkspaceID     string  `db:"workspace_id"`
	Phase           string  `db:"phase"`
	RepositoryName  *string `db:"repository_name"`
	HarnessName     string  `db:"harness_name"`
	WorktreePath    *string `db:"worktree_path"`
	PID             *int    `db:"pid"`
	Status          string  `db:"status"`
	ExitCode        *int    `db:"exit_code"`
	StartedAt       *string `db:"started_at"`
	ShutdownAt      *string `db:"shutdown_at"`
	CompletedAt     *string `db:"completed_at"`
	CreatedAt       string  `db:"created_at"`
	OwnerInstanceID *string `db:"owner_instance_id"`
	UpdatedAt       string  `db:"updated_at"`
	ResumeInfo      *string `db:"resume_info"` // JSON-encoded map[string]string
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
		WorkItemState:      domain.SessionState(r.WorkItemState),
		RepositoryName:     r.RepositoryName,
		HarnessName:        r.HarnessName,
		Status:             domain.TaskStatus(r.Status),
		AgentSessionCount:  r.AgentSessionCount,
		HasOpenQuestion:    r.HasOpenQuestion != 0,
		HasInterrupted:     r.HasInterrupted != 0,
		CreatedAt:          createdAt,
		UpdatedAt:          updatedAt,
		CompletedAt:        completedAt,
	}, nil
}

func (r *sessionRow) toDomain() (domain.Task, error) {
	startedAt, err := parseTimePtr(r.StartedAt)
	if err != nil {
		return domain.Task{}, fmt.Errorf("started_at: %w", err)
	}
	shutdownAt, err := parseTimePtr(r.ShutdownAt)
	if err != nil {
		return domain.Task{}, fmt.Errorf("shutdown_at: %w", err)
	}
	completedAt, err := parseTimePtr(r.CompletedAt)
	if err != nil {
		return domain.Task{}, fmt.Errorf("completed_at: %w", err)
	}
	createdAt, err := parseTime(r.CreatedAt)
	if err != nil {
		return domain.Task{}, fmt.Errorf("created_at: %w", err)
	}
	updatedAt, err := parseTime(r.UpdatedAt)
	if err != nil {
		return domain.Task{}, fmt.Errorf("updated_at: %w", err)
	}

	return domain.Task{
		ID:              r.ID,
		WorkItemID:      r.WorkItemID,
		SubPlanID:       derefStr(r.SubPlanID),
		WorkspaceID:     r.WorkspaceID,
		Phase:           domain.TaskPhase(r.Phase),
		RepositoryName:  derefStr(r.RepositoryName),
		HarnessName:     r.HarnessName,
		WorktreePath:    derefStr(r.WorktreePath),
		PID:             r.PID,
		Status:          domain.TaskStatus(r.Status),
		ExitCode:        r.ExitCode,
		StartedAt:       startedAt,
		ShutdownAt:      shutdownAt,
		CompletedAt:     completedAt,
		OwnerInstanceID: r.OwnerInstanceID,
		CreatedAt:       createdAt,
		UpdatedAt:       updatedAt,
		ResumeInfo:      parseResumeInfo(r.ResumeInfo),
	}, nil
}

func rowFromSession(s domain.Task) sessionRow {
	return sessionRow{
		ID:              s.ID,
		WorkItemID:      s.WorkItemID,
		SubPlanID:       strPtr(s.SubPlanID),
		WorkspaceID:     s.WorkspaceID,
		Phase:           string(s.Phase),
		RepositoryName:  strPtr(s.RepositoryName),
		HarnessName:     s.HarnessName,
		WorktreePath:    strPtr(s.WorktreePath),
		PID:             s.PID,
		Status:          string(s.Status),
		ExitCode:        s.ExitCode,
		StartedAt:       formatTimePtr(s.StartedAt),
		ShutdownAt:      formatTimePtr(s.ShutdownAt),
		CompletedAt:     formatTimePtr(s.CompletedAt),
		OwnerInstanceID: s.OwnerInstanceID,
		CreatedAt:       formatTime(s.CreatedAt),
		UpdatedAt:       formatTime(s.UpdatedAt),
		ResumeInfo:      marshalResumeInfo(s.ResumeInfo),
	}
}

// TaskRepo implements repository.TaskRepository using SQLite.
type TaskRepo struct{ remote generic.SQLXRemote }

func NewTaskRepo(remote generic.SQLXRemote) TaskRepo {
	return TaskRepo{remote: remote}
}

func (r TaskRepo) Get(ctx context.Context, id string) (domain.Task, error) {
	var row sessionRow
	if err := r.remote.GetContext(ctx, &row, `SELECT * FROM agent_sessions WHERE id = ?`, id); err != nil {
		return domain.Task{}, fmt.Errorf("get session %s: %w", id, err)
	}

	return row.toDomain()
}

func (r TaskRepo) ListByWorkItemID(ctx context.Context, workItemID string) ([]domain.Task, error) {
	return r.list(ctx, `SELECT * FROM agent_sessions WHERE work_item_id = ? ORDER BY created_at`, workItemID)
}

func (r TaskRepo) ListBySubPlanID(ctx context.Context, subPlanID string) ([]domain.Task, error) {
	return r.list(ctx, `SELECT * FROM agent_sessions WHERE sub_plan_id = ? ORDER BY created_at`, subPlanID)
}

func (r TaskRepo) ListByWorkspaceID(ctx context.Context, workspaceID string) ([]domain.Task, error) {
	return r.list(ctx, `SELECT * FROM agent_sessions WHERE workspace_id = ? ORDER BY created_at`, workspaceID)
}

func (r TaskRepo) ListByOwnerInstanceID(ctx context.Context, instanceID string) ([]domain.Task, error) {
	return r.list(ctx, `SELECT * FROM agent_sessions WHERE owner_instance_id = ? ORDER BY created_at`, instanceID)
}

func (r TaskRepo) list(ctx context.Context, query string, args ...any) ([]domain.Task, error) {
	var rows []sessionRow
	if err := r.remote.SelectContext(ctx, &rows, query, args...); err != nil {
		return nil, err
	}
	sessions := make([]domain.Task, len(rows))
	for i := range rows {
		s, err := rows[i].toDomain()
		if err != nil {
			return nil, fmt.Errorf("convert session: %w", err)
		}
		sessions[i] = s
	}

	return sessions, nil
}

func (r TaskRepo) SearchHistory(ctx context.Context, filter domain.SessionHistoryFilter) ([]domain.SessionHistoryEntry, error) {
	query := `WITH latest_session AS (
		SELECT
			s.work_item_id AS work_item_id,
			s.id AS session_id,
			COALESCE(s.repository_name, '') AS repository_name,
			s.harness_name AS harness_name,
			s.status AS status,
			s.completed_at AS completed_at,
			ROW_NUMBER() OVER (
				PARTITION BY s.work_item_id
				ORDER BY s.updated_at DESC, s.created_at DESC, s.id DESC
			) AS rn
		FROM agent_sessions s
	), session_stats AS (
		SELECT
			s.work_item_id AS work_item_id,
			COUNT(*) AS agent_session_count,
			MAX(s.updated_at) AS latest_session_updated_at,
			MAX(CASE WHEN s.status = 'waiting_for_answer' THEN 1 ELSE 0 END) AS has_open_question,
			MAX(CASE WHEN s.status = 'interrupted' THEN 1 ELSE 0 END) AS has_interrupted
		FROM agent_sessions s
		GROUP BY s.work_item_id
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
	query += ` AND (wi.state <> 'ingested' OR COALESCE(ss.agent_session_count, 0) > 0)`
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
			lower(COALESCE(ls.status, '')) LIKE ? OR
			EXISTS (
				SELECT 1
				FROM agent_sessions s2
				WHERE s2.work_item_id = wi.id AND (
					lower(s2.id) LIKE ? OR
					lower(COALESCE(s2.repository_name, '')) LIKE ? OR
					lower(COALESCE(s2.harness_name, '')) LIKE ? OR
					lower(s2.status) LIKE ?
				)
			)
		)`
		args = append(args, like, like, like, like, like, like, like, like, like, like, like, like, like)
	}
	query += ` ORDER BY updated_at DESC, COALESCE(ss.latest_session_updated_at, '') DESC, wi.updated_at DESC, wi.created_at DESC, wi.id`
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

func (r TaskRepo) Create(ctx context.Context, s domain.Task) error {
	row := rowFromSession(s)
	_, err := r.remote.NamedExecContext(ctx,
		`INSERT INTO agent_sessions
		 (id, work_item_id, sub_plan_id, workspace_id, phase, repository_name, harness_name, worktree_path,
		  pid, status, exit_code, started_at, shutdown_at, completed_at, created_at,
		  owner_instance_id, updated_at, resume_info)
		 VALUES
		 (:id, :work_item_id, :sub_plan_id, :workspace_id, :phase, :repository_name, :harness_name, :worktree_path,
		  :pid, :status, :exit_code, :started_at, :shutdown_at, :completed_at, :created_at,
		  :owner_instance_id, :updated_at, :resume_info)`, row)
	if err != nil {
		return fmt.Errorf("create session %s: %w", s.ID, err)
	}

	return nil
}

func (r TaskRepo) Update(ctx context.Context, s domain.Task) error {
	row := rowFromSession(s)
	res, err := r.remote.NamedExecContext(ctx,
		`UPDATE agent_sessions SET work_item_id = :work_item_id, sub_plan_id = :sub_plan_id, workspace_id = :workspace_id,
		 phase = :phase, repository_name = :repository_name, harness_name = :harness_name, worktree_path = :worktree_path,
		 pid = :pid, status = :status, exit_code = :exit_code, started_at = :started_at,
		 shutdown_at = :shutdown_at, completed_at = :completed_at, owner_instance_id = :owner_instance_id,
		 updated_at = :updated_at, resume_info = :resume_info WHERE id = :id`, row)
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

func (r TaskRepo) Delete(ctx context.Context, id string) error {
	params := map[string]any{"id": id}

	if _, err := r.remote.NamedExecContext(ctx, `DELETE FROM questions WHERE agent_session_id = :id`, params); err != nil {
		return fmt.Errorf("delete session %s questions: %w", id, err)
	}
	if _, err := r.remote.NamedExecContext(ctx, `DELETE FROM review_cycles WHERE agent_session_id = :id`, params); err != nil {
		return fmt.Errorf("delete session %s review cycles: %w", id, err)
	}
	if _, err := r.remote.NamedExecContext(ctx, `DELETE FROM agent_sessions WHERE id = :id`, params); err != nil {
		return fmt.Errorf("delete session %s: %w", id, err)
	}

	return nil
}

// parseResumeInfo decodes a nullable JSON string into a map.
// Returns nil if the column is NULL or the JSON is invalid.
func parseResumeInfo(s *string) map[string]string {
	if s == nil || *s == "" {
		return nil
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(*s), &m); err != nil {
		return nil
	}
	return m
}

// marshalResumeInfo encodes a map to a nullable JSON string.
// Returns nil when the map is empty or nil.
func marshalResumeInfo(m map[string]string) *string {
	if len(m) == 0 {
		return nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil
	}
	s := string(b)
	return &s
}

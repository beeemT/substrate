package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/beeemT/go-atomic/generic"
	"github.com/beeemT/substrate/internal/domain"
)

type newSessionFilterRow struct {
	ID           string `db:"id"`
	WorkspaceID  string `db:"workspace_id"`
	Name         string `db:"name"`
	Provider     string `db:"provider"`
	CriteriaJSON string `db:"criteria_json"`
	CreatedAt    string `db:"created_at"`
	UpdatedAt    string `db:"updated_at"`
}

func (r newSessionFilterRow) toDomain() (domain.NewSessionFilter, error) {
	createdAt, err := parseTime(r.CreatedAt)
	if err != nil {
		return domain.NewSessionFilter{}, fmt.Errorf("created_at: %w", err)
	}
	updatedAt, err := parseTime(r.UpdatedAt)
	if err != nil {
		return domain.NewSessionFilter{}, fmt.Errorf("updated_at: %w", err)
	}
	criteria := domain.NewSessionFilterCriteria{}
	if err := json.Unmarshal([]byte(r.CriteriaJSON), &criteria); err != nil {
		return domain.NewSessionFilter{}, fmt.Errorf("criteria_json: %w", err)
	}

	return domain.NewSessionFilter{
		ID:          r.ID,
		WorkspaceID: r.WorkspaceID,
		Name:        r.Name,
		Provider:    r.Provider,
		Criteria:    criteria,
		CreatedAt:   createdAt,
		UpdatedAt:   updatedAt,
	}, nil
}

func rowFromNewSessionFilter(filter domain.NewSessionFilter) (newSessionFilterRow, error) {
	criteriaBytes, err := json.Marshal(filter.Criteria)
	if err != nil {
		return newSessionFilterRow{}, fmt.Errorf("marshal criteria_json: %w", err)
	}

	return newSessionFilterRow{
		ID:           filter.ID,
		WorkspaceID:  filter.WorkspaceID,
		Name:         filter.Name,
		Provider:     filter.Provider,
		CriteriaJSON: string(criteriaBytes),
		CreatedAt:    formatTime(filter.CreatedAt),
		UpdatedAt:    formatTime(filter.UpdatedAt),
	}, nil
}

// SessionFilterRepo implements repository.NewSessionFilterRepository using SQLite.
type SessionFilterRepo struct{ remote generic.SQLXRemote }

// NewSessionFilterRepo creates a repository for saved New Session filters.
func NewSessionFilterRepo(remote generic.SQLXRemote) SessionFilterRepo {
	return SessionFilterRepo{remote: remote}
}

func (r SessionFilterRepo) Get(ctx context.Context, id string) (domain.NewSessionFilter, error) {
	var row newSessionFilterRow
	if err := r.remote.GetContext(ctx, &row, `SELECT * FROM new_session_filters WHERE id = ?`, id); err != nil {
		return domain.NewSessionFilter{}, fmt.Errorf("get new session filter %s: %w", id, err)
	}
	return row.toDomain()
}

func (r SessionFilterRepo) GetByWorkspaceProviderName(ctx context.Context, workspaceID, provider, name string) (domain.NewSessionFilter, error) {
	var row newSessionFilterRow
	if err := r.remote.GetContext(ctx, &row,
		`SELECT * FROM new_session_filters WHERE workspace_id = ? AND provider = ? AND name = ?`,
		workspaceID, provider, name,
	); err != nil {
		return domain.NewSessionFilter{}, fmt.Errorf("get new session filter %s/%s/%s: %w", workspaceID, provider, name, err)
	}
	return row.toDomain()
}

func (r SessionFilterRepo) ListByWorkspaceID(ctx context.Context, workspaceID string) ([]domain.NewSessionFilter, error) {
	var rows []newSessionFilterRow
	if err := r.remote.SelectContext(ctx, &rows,
		`SELECT * FROM new_session_filters WHERE workspace_id = ? ORDER BY provider, name`,
		workspaceID,
	); err != nil {
		return nil, fmt.Errorf("list new session filters for workspace %s: %w", workspaceID, err)
	}
	return newSessionFiltersFromRows(rows)
}

func (r SessionFilterRepo) ListByWorkspaceProvider(ctx context.Context, workspaceID, provider string) ([]domain.NewSessionFilter, error) {
	var rows []newSessionFilterRow
	if err := r.remote.SelectContext(ctx, &rows,
		`SELECT * FROM new_session_filters WHERE workspace_id = ? AND provider = ? ORDER BY name`,
		workspaceID, provider,
	); err != nil {
		return nil, fmt.Errorf("list new session filters for workspace/provider %s/%s: %w", workspaceID, provider, err)
	}
	return newSessionFiltersFromRows(rows)
}

func (r SessionFilterRepo) Create(ctx context.Context, filter domain.NewSessionFilter) error {
	row, err := rowFromNewSessionFilter(filter)
	if err != nil {
		return fmt.Errorf("create new session filter %s: %w", filter.ID, err)
	}
	_, err = r.remote.NamedExecContext(ctx,
		`INSERT INTO new_session_filters (id, workspace_id, name, provider, criteria_json, created_at, updated_at)
		 VALUES (:id, :workspace_id, :name, :provider, :criteria_json, :created_at, :updated_at)`,
		row,
	)
	if err != nil {
		return fmt.Errorf("create new session filter %s: %w", filter.ID, err)
	}
	return nil
}

func (r SessionFilterRepo) Update(ctx context.Context, filter domain.NewSessionFilter) error {
	row, err := rowFromNewSessionFilter(filter)
	if err != nil {
		return fmt.Errorf("update new session filter %s: %w", filter.ID, err)
	}
	res, err := r.remote.NamedExecContext(ctx,
		`UPDATE new_session_filters
		 SET workspace_id = :workspace_id, name = :name, provider = :provider, criteria_json = :criteria_json, updated_at = :updated_at
		 WHERE id = :id`,
		row,
	)
	if err != nil {
		return fmt.Errorf("update new session filter %s: %w", filter.ID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update new session filter %s: get rows affected: %w", filter.ID, err)
	}
	if n == 0 {
		return fmt.Errorf("update new session filter %s: %w", filter.ID, sql.ErrNoRows)
	}
	return nil
}

func (r SessionFilterRepo) Delete(ctx context.Context, id string) error {
	_, err := r.remote.NamedExecContext(ctx, `DELETE FROM new_session_filters WHERE id = :id`, map[string]any{"id": id})
	if err != nil {
		return fmt.Errorf("delete new session filter %s: %w", id, err)
	}
	return nil
}

func newSessionFiltersFromRows(rows []newSessionFilterRow) ([]domain.NewSessionFilter, error) {
	filters := make([]domain.NewSessionFilter, len(rows))
	for i := range rows {
		filter, err := rows[i].toDomain()
		if err != nil {
			return nil, fmt.Errorf("convert new session filter: %w", err)
		}
		filters[i] = filter
	}
	return filters, nil
}

type newSessionFilterLockRow struct {
	FilterID       string `db:"filter_id"`
	InstanceID     string `db:"instance_id"`
	LeaseExpiresAt string `db:"lease_expires_at"`
	AcquiredAt     string `db:"acquired_at"`
	UpdatedAt      string `db:"updated_at"`
}

func (r newSessionFilterLockRow) toDomain() (domain.NewSessionFilterLock, error) {
	leaseExpiresAt, err := parseTime(r.LeaseExpiresAt)
	if err != nil {
		return domain.NewSessionFilterLock{}, fmt.Errorf("lease_expires_at: %w", err)
	}
	acquiredAt, err := parseTime(r.AcquiredAt)
	if err != nil {
		return domain.NewSessionFilterLock{}, fmt.Errorf("acquired_at: %w", err)
	}
	updatedAt, err := parseTime(r.UpdatedAt)
	if err != nil {
		return domain.NewSessionFilterLock{}, fmt.Errorf("updated_at: %w", err)
	}
	return domain.NewSessionFilterLock{
		FilterID:       r.FilterID,
		InstanceID:     r.InstanceID,
		LeaseExpiresAt: leaseExpiresAt,
		AcquiredAt:     acquiredAt,
		UpdatedAt:      updatedAt,
	}, nil
}

func rowFromNewSessionFilterLock(lock domain.NewSessionFilterLock) newSessionFilterLockRow {
	return newSessionFilterLockRow{
		FilterID:       lock.FilterID,
		InstanceID:     lock.InstanceID,
		LeaseExpiresAt: formatTime(lock.LeaseExpiresAt),
		AcquiredAt:     formatTime(lock.AcquiredAt),
		UpdatedAt:      formatTime(lock.UpdatedAt),
	}
}

// SessionFilterLockRepo implements repository.NewSessionFilterLockRepository using SQLite.
type SessionFilterLockRepo struct{ remote generic.SQLXRemote }

// NewSessionFilterLockRepo creates a lock repository for New Session filters.
func NewSessionFilterLockRepo(remote generic.SQLXRemote) SessionFilterLockRepo {
	return SessionFilterLockRepo{remote: remote}
}

func (r SessionFilterLockRepo) Get(ctx context.Context, filterID string) (domain.NewSessionFilterLock, error) {
	var row newSessionFilterLockRow
	if err := r.remote.GetContext(ctx, &row, `SELECT * FROM new_session_filter_locks WHERE filter_id = ?`, filterID); err != nil {
		return domain.NewSessionFilterLock{}, fmt.Errorf("get new session filter lock %s: %w", filterID, err)
	}
	return row.toDomain()
}

func (r SessionFilterLockRepo) Acquire(ctx context.Context, lock domain.NewSessionFilterLock) (domain.NewSessionFilterLock, bool, error) {
	row := rowFromNewSessionFilterLock(lock)
	res, err := r.remote.NamedExecContext(ctx,
		`INSERT INTO new_session_filter_locks (filter_id, instance_id, lease_expires_at, acquired_at, updated_at)
		 VALUES (:filter_id, :instance_id, :lease_expires_at, :acquired_at, :updated_at)
		 ON CONFLICT(filter_id) DO UPDATE SET
		   instance_id = excluded.instance_id,
		   lease_expires_at = excluded.lease_expires_at,
		   acquired_at = CASE
		     WHEN new_session_filter_locks.instance_id = excluded.instance_id THEN new_session_filter_locks.acquired_at
		     ELSE excluded.acquired_at
		   END,
		   updated_at = excluded.updated_at
		 WHERE new_session_filter_locks.instance_id = excluded.instance_id
		    OR new_session_filter_locks.lease_expires_at <= excluded.updated_at`,
		row,
	)
	if err != nil {
		return domain.NewSessionFilterLock{}, false, fmt.Errorf("acquire new session filter lock %s: %w", lock.FilterID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return domain.NewSessionFilterLock{}, false, fmt.Errorf("acquire new session filter lock %s: get rows affected: %w", lock.FilterID, err)
	}
	current, getErr := r.Get(ctx, lock.FilterID)
	if getErr != nil {
		if n == 0 && errors.Is(getErr, sql.ErrNoRows) {
			return domain.NewSessionFilterLock{}, false, nil
		}
		return domain.NewSessionFilterLock{}, false, getErr
	}
	return current, n > 0, nil
}

func (r SessionFilterLockRepo) Renew(ctx context.Context, lock domain.NewSessionFilterLock) (domain.NewSessionFilterLock, bool, error) {
	row := rowFromNewSessionFilterLock(lock)
	res, err := r.remote.NamedExecContext(ctx,
		`UPDATE new_session_filter_locks
		 SET lease_expires_at = :lease_expires_at,
		     updated_at = :updated_at
		 WHERE filter_id = :filter_id
		   AND instance_id = :instance_id`,
		row,
	)
	if err != nil {
		return domain.NewSessionFilterLock{}, false, fmt.Errorf("renew new session filter lock %s: %w", lock.FilterID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return domain.NewSessionFilterLock{}, false, fmt.Errorf("renew new session filter lock %s: get rows affected: %w", lock.FilterID, err)
	}
	if n == 0 {
		current, getErr := r.Get(ctx, lock.FilterID)
		if getErr != nil {
			if errors.Is(getErr, sql.ErrNoRows) {
				return domain.NewSessionFilterLock{}, false, nil
			}
			return domain.NewSessionFilterLock{}, false, getErr
		}
		return current, false, nil
	}
	current, getErr := r.Get(ctx, lock.FilterID)
	if getErr != nil {
		return domain.NewSessionFilterLock{}, false, getErr
	}
	return current, true, nil
}

func (r SessionFilterLockRepo) Release(ctx context.Context, filterID, instanceID string) error {
	_, err := r.remote.NamedExecContext(ctx,
		`DELETE FROM new_session_filter_locks WHERE filter_id = :filter_id AND instance_id = :instance_id`,
		map[string]any{"filter_id": filterID, "instance_id": instanceID},
	)
	if err != nil {
		return fmt.Errorf("release new session filter lock %s: %w", filterID, err)
	}
	return nil
}

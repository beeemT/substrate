package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/beeemT/go-atomic/generic"
	"github.com/beeemT/substrate/internal/domain"
)

type instanceRow struct {
	ID            string `db:"id"`
	WorkspaceID   string `db:"workspace_id"`
	PID           int    `db:"pid"`
	Hostname      string `db:"hostname"`
	LastHeartbeat string `db:"last_heartbeat"`
	StartedAt     string `db:"started_at"`
}

func (r *instanceRow) toDomain() (domain.SubstrateInstance, error) {
	lastHeartbeat, err := parseTime(r.LastHeartbeat)
	if err != nil {
		return domain.SubstrateInstance{}, fmt.Errorf("last_heartbeat: %w", err)
	}
	startedAt, err := parseTime(r.StartedAt)
	if err != nil {
		return domain.SubstrateInstance{}, fmt.Errorf("started_at: %w", err)
	}
	return domain.SubstrateInstance{
		ID:            r.ID,
		WorkspaceID:   r.WorkspaceID,
		PID:           r.PID,
		Hostname:      r.Hostname,
		LastHeartbeat: lastHeartbeat,
		StartedAt:     startedAt,
	}, nil
}

func rowFromInstance(inst domain.SubstrateInstance) instanceRow {
	return instanceRow{
		ID:            inst.ID,
		WorkspaceID:   inst.WorkspaceID,
		PID:           inst.PID,
		Hostname:      inst.Hostname,
		LastHeartbeat: formatTime(inst.LastHeartbeat),
		StartedAt:     formatTime(inst.StartedAt),
	}
}

// InstanceRepo implements repository.InstanceRepository using SQLite.
type InstanceRepo struct{ remote generic.SQLXRemote }

func NewInstanceRepo(remote generic.SQLXRemote) InstanceRepo {
	return InstanceRepo{remote: remote}
}

func (r InstanceRepo) Get(ctx context.Context, id string) (domain.SubstrateInstance, error) {
	var row instanceRow
	if err := r.remote.GetContext(ctx, &row, `SELECT * FROM substrate_instances WHERE id = ?`, id); err != nil {
		return domain.SubstrateInstance{}, fmt.Errorf("get instance %s: %w", id, err)
	}
	return row.toDomain()
}

func (r InstanceRepo) ListByWorkspaceID(ctx context.Context, workspaceID string) ([]domain.SubstrateInstance, error) {
	var rows []instanceRow
	if err := r.remote.SelectContext(ctx, &rows, `SELECT * FROM substrate_instances WHERE workspace_id = ? ORDER BY started_at`, workspaceID); err != nil {
		return nil, fmt.Errorf("list instances for workspace %s: %w", workspaceID, err)
	}
	instances := make([]domain.SubstrateInstance, len(rows))
	for i := range rows {
		inst, err := rows[i].toDomain()
		if err != nil {
			return nil, fmt.Errorf("convert instance: %w", err)
		}
		instances[i] = inst
	}
	return instances, nil
}

func (r InstanceRepo) Create(ctx context.Context, inst domain.SubstrateInstance) error {
	row := rowFromInstance(inst)
	_, err := r.remote.NamedExecContext(ctx,
		`INSERT INTO substrate_instances (id, workspace_id, pid, hostname, last_heartbeat, started_at)
		 VALUES (:id, :workspace_id, :pid, :hostname, :last_heartbeat, :started_at)`, row)
	if err != nil {
		return fmt.Errorf("create instance %s: %w", inst.ID, err)
	}
	return nil
}

func (r InstanceRepo) Update(ctx context.Context, inst domain.SubstrateInstance) error {
	row := rowFromInstance(inst)
	res, err := r.remote.NamedExecContext(ctx,
		`UPDATE substrate_instances SET workspace_id = :workspace_id, pid = :pid,
		 hostname = :hostname, last_heartbeat = :last_heartbeat WHERE id = :id`, row)
	if err != nil {
		return fmt.Errorf("update instance %s: %w", inst.ID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update instance %s: get rows affected: %w", inst.ID, err)
	}
	if n == 0 {
		return fmt.Errorf("update instance %s: %w", inst.ID, sql.ErrNoRows)
	}
	return nil
}

func (r InstanceRepo) Delete(ctx context.Context, id string) error {
	_, err := r.remote.NamedExecContext(ctx, `DELETE FROM substrate_instances WHERE id = :id`, map[string]any{"id": id})
	if err != nil {
		return fmt.Errorf("delete instance %s: %w", id, err)
	}
	return nil
}

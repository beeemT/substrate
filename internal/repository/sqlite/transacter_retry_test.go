package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/repository"
	reposqlite "github.com/beeemT/substrate/internal/repository/sqlite"
	"github.com/beeemT/substrate/migrations"
)

func TestTransacterRetriesSQLiteBusySnapshot(t *testing.T) {
	db := setupRetryDB(t)
	tx := beginTx(t, db)
	ws := makeWorkspace(t, tx)
	wi := makeWorkItem(t, tx, ws.ID)
	task := domain.Task{
		ID:          domain.NewID(),
		WorkItemID:  wi.ID,
		WorkspaceID: ws.ID,
		Phase:       domain.TaskPhasePlanning,
		HarnessName: "claude",
		Status:      domain.AgentSessionInterrupted,
		CreatedAt:   now(),
		UpdatedAt:   now(),
	}
	if err := reposqlite.NewTaskRepo(tx).Create(context.Background(), task); err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit seed tx: %v", err)
	}

	transacter := reposqlite.NewTransacter(db)
	attempts := 0
	err := transacter.Transact(context.Background(), func(ctx context.Context, res repository.Resources) error {
		attempts++
		current, err := res.Tasks.Get(ctx, task.ID)
		if err != nil {
			return err
		}

		if attempts == 1 {
			if _, err := db.ExecContext(ctx, `UPDATE agent_sessions SET updated_at = ? WHERE id = ?`, formatRetryTestTime(time.Now()), task.ID); err != nil {
				t.Fatalf("concurrent update: %v", err)
			}
		}

		current.Status = domain.AgentSessionFailed
		current.UpdatedAt = now()
		return res.Tasks.Update(ctx, current)
	})
	if err != nil {
		t.Fatalf("transact returned error: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}

	var got domain.Task
	err = transacter.Transact(context.Background(), func(ctx context.Context, res repository.Resources) error {
		var err error
		got, err = res.Tasks.Get(ctx, task.ID)
		return err
	})
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if got.Status != domain.AgentSessionFailed {
		t.Fatalf("status = %s, want %s", got.Status, domain.AgentSessionFailed)
	}
}

func formatRetryTestTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func setupRetryDB(t *testing.T) *sqlx.DB {
	t.Helper()
	db, err := sqlx.Open("sqlite", filepath.Join(t.TempDir(), "retry.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA foreign_keys=ON",
		"PRAGMA busy_timeout=5000",
	} {
		if _, err := db.ExecContext(context.Background(), pragma); err != nil {
			t.Fatalf("pragma %s: %v", pragma, err)
		}
	}
	if err := repository.Migrate(context.Background(), db, migrations.FS); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

package repository

import (
	"context"
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

func openTestDB(t *testing.T) *sqlx.DB {
	t.Helper()
	db, err := sqlx.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.MustExec("PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;")
	t.Cleanup(func() { db.Close() })

	return db
}

func TestMigrateCreatesSchemaTable(t *testing.T) {
	db := openTestDB(t)
	emptyFS := fstest.MapFS{}

	if err := Migrate(context.Background(), db, emptyFS); err != nil {
		t.Fatalf("Migrate() error: %v", err)
	}

	var count int
	if err := db.Get(&count, "SELECT COUNT(*) FROM schema_migrations"); err != nil {
		t.Fatalf("schema_migrations table should exist: %v", err)
	}
}

func TestMigrateAppliesMigrations(t *testing.T) {
	db := openTestDB(t)

	testFS := fstest.MapFS{
		"001_create_test.sql": &fstest.MapFile{
			Data: []byte(`CREATE TABLE test_table (id TEXT PRIMARY KEY, name TEXT NOT NULL);`),
		},
	}

	if err := Migrate(context.Background(), db, testFS); err != nil {
		t.Fatalf("Migrate() error: %v", err)
	}

	// Verify the migration was applied
	var version int
	if err := db.Get(&version, "SELECT version FROM schema_migrations WHERE version = 1"); err != nil {
		t.Fatalf("migration version 1 should be recorded: %v", err)
	}

	// Verify the table was created
	var count int
	if err := db.Get(&count, "SELECT COUNT(*) FROM test_table"); err != nil {
		t.Fatalf("test_table should exist: %v", err)
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	db := openTestDB(t)

	testFS := fstest.MapFS{
		"001_create_test.sql": &fstest.MapFile{
			Data: []byte(`CREATE TABLE test_table (id TEXT PRIMARY KEY);`),
		},
	}

	if err := Migrate(context.Background(), db, testFS); err != nil {
		t.Fatalf("first Migrate() error: %v", err)
	}
	if err := Migrate(context.Background(), db, testFS); err != nil {
		t.Fatalf("second Migrate() error: %v", err)
	}

	var count int
	if err := db.Get(&count, "SELECT COUNT(*) FROM schema_migrations"); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("expected 1 migration record, got %d", count)
	}
}

func TestMigrateMultipleVersions(t *testing.T) {
	db := openTestDB(t)

	testFS := fstest.MapFS{
		"001_first.sql": &fstest.MapFile{
			Data: []byte(`CREATE TABLE first (id TEXT PRIMARY KEY);`),
		},
		"002_second.sql": &fstest.MapFile{
			Data: []byte(`CREATE TABLE second (id TEXT PRIMARY KEY);`),
		},
	}

	if err := Migrate(context.Background(), db, testFS); err != nil {
		t.Fatalf("Migrate() error: %v", err)
	}

	var count int
	if err := db.Get(&count, "SELECT COUNT(*) FROM schema_migrations"); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Errorf("expected 2 migration records, got %d", count)
	}

	// Both tables should exist
	if err := db.Get(&count, "SELECT COUNT(*) FROM first"); err != nil {
		t.Fatal("first table should exist")
	}
	if err := db.Get(&count, "SELECT COUNT(*) FROM second"); err != nil {
		t.Fatal("second table should exist")
	}
}

func TestMigrateOrdersCorrectly(t *testing.T) {
	db := openTestDB(t)

	// 002 depends on 001 (references its table)
	testFS := fstest.MapFS{
		"002_child.sql": &fstest.MapFile{
			Data: []byte(`CREATE TABLE child (id TEXT PRIMARY KEY, parent_id TEXT NOT NULL REFERENCES parent(id));`),
		},
		"001_parent.sql": &fstest.MapFile{
			Data: []byte(`CREATE TABLE parent (id TEXT PRIMARY KEY);`),
		},
	}

	if err := Migrate(context.Background(), db, testFS); err != nil {
		t.Fatalf("Migrate() error: %v", err)
	}
}

func TestMigrateWithRealMigrations(t *testing.T) {
	db := openTestDB(t)

	// Use the actual embedded migrations
	realFS, err := fs.Sub(realMigrationsFS, ".")
	if err != nil {
		t.Fatalf("fs.Sub error: %v", err)
	}

	if err := Migrate(context.Background(), db, realFS); err != nil {
		t.Fatalf("Migrate() with real migrations error: %v", err)
	}

	// Verify key tables exist
	tables := []string{
		"workspaces", "work_items", "plans", "sub_plans", "agent_sessions",
		"review_cycles", "critiques", "questions",
		"system_events", "substrate_instances",
		"new_session_filters", "new_session_filter_locks",
	}

	for _, table := range tables {
		var count int
		if err := db.Get(&count, "SELECT COUNT(*) FROM "+table); err != nil {
			t.Errorf("table %s should exist: %v", table, err)
		}
	}
}

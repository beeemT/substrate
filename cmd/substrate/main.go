// Package main is the entry point for the Substrate CLI.
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"

	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/repository"
	"github.com/beeemT/substrate/migrations"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	cfgPath := "~/.substrate/config.toml"
	if p := os.Getenv("SUBSTRATE_CONFIG"); p != "" {
		cfgPath = p
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	globalDir, err := cfg.GlobalDir()
	if err != nil {
		return fmt.Errorf("getting global directory: %w", err)
	}
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		return fmt.Errorf("creating global directory: %w", err)
	}

	dbPath, err := cfg.GlobalDBPath()
	if err != nil {
		return fmt.Errorf("getting database path: %w", err)
	}
	db, err := sqlx.Open("sqlite", dbPath)
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer db.Close()

	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA foreign_keys=ON",
		"PRAGMA busy_timeout=5000",
	} {
		if _, err := db.Exec(pragma); err != nil {
			return fmt.Errorf("set pragma: %w", err)
		}
	}

	ctx := context.Background()
	if err := repository.Migrate(ctx, db, migrations.FS); err != nil {
		return fmt.Errorf("running migrations: %w", err)
	}

	fmt.Println("substrate: initialized successfully")
	return nil
}

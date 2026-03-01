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
	// Get paths from config package (respects SUBSTRATE_HOME)
	globalDir, err := config.GlobalDir()
	if err != nil {
		return fmt.Errorf("getting global directory: %w", err)
	}

	// Ensure global directory exists
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		return fmt.Errorf("creating global directory: %w", err)
	}

	cfgPath, err := config.ConfigPath()
	if err != nil {
		return fmt.Errorf("getting config path: %w", err)
	}

	// Global self-initialization: create default config if not exists
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		if err := initializeGlobalConfig(cfgPath); err != nil {
			return fmt.Errorf("initializing global config: %w", err)
		}
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	_ = cfg // Config loaded and validated, will be used in future phases

	// Ensure sessions directory exists
	sessionsDir, err := config.SessionsDir()
	if err != nil {
		return fmt.Errorf("getting sessions directory: %w", err)
	}
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		return fmt.Errorf("creating sessions directory: %w", err)
	}

	dbPath, err := config.GlobalDBPath()
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

// initializeGlobalConfig creates the default config file.
func initializeGlobalConfig(cfgPath string) error {
	defaultConfig := `# Substrate Configuration
# This file was auto-generated with default values.
# All settings have sensible defaults - customize as needed.

# Commit behavior for agent sessions
[commit]
# strategy: granular (every change), semi-regular (logical chunks), single (one commit)
# strategy = "semi-regular"

# message_format: ai-generated, conventional, custom
# message_format = "ai-generated"

# message_template: required when message_format = "custom"
# message_template = "feat({{.Scope}}): {{.Description}}"

# Planning pipeline settings
[plan]
# max_parse_retries: number of correction attempts when plan parsing fails
# max_parse_retries = 2

# Review pipeline settings
[review]
# pass_threshold: nit_only, minor_ok, no_critiques
# pass_threshold = "minor_ok"

# max_cycles: maximum review->re-implement iterations before escalation
# max_cycles = 3

# Foreman (question-answering) settings
[foreman]
# enabled: whether to use foreman for agent questions
# enabled = true

# question_timeout: duration string (e.g., "5m") or "0" for unlimited
# question_timeout = "0"

# Oh-my-pi adapter settings
[adapters.ohmypi]
# bun_path: path to bun executable (defaults to "bun" in PATH)
# bridge_path: path to omp-bridge.ts (defaults to bundled bridge)
# thinking_level: thinking level for all sessions
`

	if err := os.WriteFile(cfgPath, []byte(defaultConfig), 0o644); err != nil {
		return fmt.Errorf("writing default config: %w", err)
	}

	fmt.Printf("substrate: created default config at %s\n", cfgPath)
	return nil
}

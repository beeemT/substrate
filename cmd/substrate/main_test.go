package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/beeemT/substrate/internal/config"
)

func TestInitializeGlobalConfig_WritesParsableYAML(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := initializeGlobalConfig(cfgPath); err != nil {
		t.Fatalf("initializeGlobalConfig() error = %v", err)
	}

	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", cfgPath, err)
	}
	if strings.HasPrefix(string(raw), "\t") || strings.Contains(string(raw), "\n\t#") {
		t.Fatalf("generated config contains tab-indented comment lines:\n%s", string(raw))
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load(%q) error = %v\n%s", cfgPath, err, string(raw))
	}
	if cfg.Commit.Strategy != config.CommitStrategySemiRegular {
		t.Fatalf("commit.strategy = %q, want %q", cfg.Commit.Strategy, config.CommitStrategySemiRegular)
	}
}

func TestParseDaemonSelectionArg(t *testing.T) {
	name, remaining, err := parseDaemonSelectionArg([]string{"--daemon", "staging", "--version"})
	if err != nil {
		t.Fatalf("parseDaemonSelectionArg() error = %v", err)
	}
	if name != "staging" || len(remaining) != 1 || remaining[0] != "--version" {
		t.Fatalf("parseDaemonSelectionArg() = %q, %#v", name, remaining)
	}

	name, remaining, err = parseDaemonSelectionArg([]string{"--daemon=prod", "--help"})
	if err != nil {
		t.Fatalf("parseDaemonSelectionArg(--daemon=prod) error = %v", err)
	}
	if name != "prod" || len(remaining) != 1 || remaining[0] != "--help" {
		t.Fatalf("parseDaemonSelectionArg(--daemon=prod) = %q, %#v", name, remaining)
	}
}

func TestParseDaemonSelectionArgRejectsMissingName(t *testing.T) {
	if _, _, err := parseDaemonSelectionArg([]string{"--daemon"}); err == nil {
		t.Fatal("parseDaemonSelectionArg(--daemon) error = nil")
	}
	if _, _, err := parseDaemonSelectionArg([]string{"--daemon="}); err == nil {
		t.Fatal("parseDaemonSelectionArg(--daemon=) error = nil")
	}
}

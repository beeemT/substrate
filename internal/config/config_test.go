package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTestConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "substrate.toml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadDefaults(t *testing.T) {
	path := writeTestConfig(t, `# empty config - all defaults
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Commit.Strategy != CommitStrategySemiRegular {
		t.Errorf("commit.strategy = %q, want %q", cfg.Commit.Strategy, CommitStrategySemiRegular)
	}
	if cfg.Commit.MessageFormat != CommitMessageAIGenerated {
		t.Errorf("commit.message_format = %q, want %q", cfg.Commit.MessageFormat, CommitMessageAIGenerated)
	}
	if *cfg.Plan.MaxParseRetries != 2 {
		t.Errorf("plan.max_parse_retries = %d, want 2", *cfg.Plan.MaxParseRetries)
	}
	if cfg.Review.PassThreshold != PassThresholdMinorOK {
		t.Errorf("review.pass_threshold = %q, want %q", cfg.Review.PassThreshold, PassThresholdMinorOK)
	}
	if *cfg.Review.MaxCycles != 3 {
		t.Errorf("review.max_cycles = %d, want 3", *cfg.Review.MaxCycles)
	}
}

func TestLoadExplicitValues(t *testing.T) {
	path := writeTestConfig(t, `
[commit]
strategy = "granular"
message_format = "conventional"

[plan]
max_parse_retries = 5

[review]
pass_threshold = "no_critiques"
max_cycles = 7
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Commit.Strategy != CommitStrategyGranular {
		t.Errorf("commit.strategy = %q, want %q", cfg.Commit.Strategy, CommitStrategyGranular)
	}
	if cfg.Commit.MessageFormat != CommitMessageConventional {
		t.Errorf("commit.message_format = %q, want %q", cfg.Commit.MessageFormat, CommitMessageConventional)
	}
	if *cfg.Plan.MaxParseRetries != 5 {
		t.Errorf("plan.max_parse_retries = %d, want 5", *cfg.Plan.MaxParseRetries)
	}
	if cfg.Review.PassThreshold != PassThresholdNoCritiques {
		t.Errorf("review.pass_threshold = %q, want %q", cfg.Review.PassThreshold, PassThresholdNoCritiques)
	}
	if *cfg.Review.MaxCycles != 7 {
		t.Errorf("review.max_cycles = %d, want 7", *cfg.Review.MaxCycles)
	}
}

func TestLoadCustomMessageFormatRequiresTemplate(t *testing.T) {
	path := writeTestConfig(t, `
[commit]
message_format = "custom"
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() should error when message_format=custom without message_template")
	}
}

func TestLoadCustomMessageFormatWithTemplate(t *testing.T) {
	path := writeTestConfig(t, `
[commit]
message_format = "custom"
message_template = "feat({{.Scope}}): {{.Description}}"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Commit.MessageTemplate == "" {
		t.Error("commit.message_template should not be empty")
	}
}

func TestLoadInvalidStrategy(t *testing.T) {
	path := writeTestConfig(t, `
[commit]
strategy = "invalid"
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() should error on invalid commit.strategy")
	}
}

func TestLoadInvalidMessageFormat(t *testing.T) {
	path := writeTestConfig(t, `
[commit]
message_format = "invalid"
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() should error on invalid commit.message_format")
	}
}

func TestLoadInvalidPassThreshold(t *testing.T) {
	path := writeTestConfig(t, `
[review]
pass_threshold = "invalid"
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() should error on invalid review.pass_threshold")
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load("/nonexistent/path/substrate.toml")
	if err == nil {
		t.Fatal("Load() should error on missing file")
	}
}

func TestLoadInvalidTOML(t *testing.T) {
	path := writeTestConfig(t, `
[commit
strategy = "granular"
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() should error on invalid TOML")
	}
}

func TestLoadWithRepos(t *testing.T) {
	path := writeTestConfig(t, `
[repos.myrepo]
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if _, ok := cfg.Repos["myrepo"]; !ok {
		t.Error("repos.myrepo should exist in config")
	}
}

func TestGlobalDBPath(t *testing.T) {
	path, err := GlobalDBPath()
	if err != nil {
		t.Fatalf("GlobalDBPath() error = %v", err)
	}
	if filepath.Base(path) != "state.db" {
		t.Errorf("GlobalDBPath() = %q, want to end with state.db", path)
	}
}

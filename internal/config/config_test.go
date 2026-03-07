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
	if cfg.Harness.Default != HarnessOhMyPi {
		t.Errorf("harness.default = %q, want %q", cfg.Harness.Default, HarnessOhMyPi)
	}
	if len(cfg.Harness.Fallback) != 2 || cfg.Harness.Fallback[0] != HarnessClaudeCode || cfg.Harness.Fallback[1] != HarnessCodex {
		t.Errorf("harness.fallback = %v, want [%s %s]", cfg.Harness.Fallback, HarnessClaudeCode, HarnessCodex)
	}
	if cfg.Harness.Phase.Planning != HarnessOhMyPi || cfg.Harness.Phase.Implementation != HarnessOhMyPi || cfg.Harness.Phase.Review != HarnessOhMyPi || cfg.Harness.Phase.Foreman != HarnessOhMyPi {
		t.Errorf("harness phase defaults = %+v, want all ohmypi", cfg.Harness.Phase)
	}
	if cfg.Adapters.Linear.PollInterval != "30s" {
		t.Errorf("adapters.linear.poll_interval = %q, want %q", cfg.Adapters.Linear.PollInterval, "30s")
	}
	if cfg.Adapters.GitLab.BaseURL != "https://gitlab.com" {
		t.Errorf("adapters.gitlab.base_url = %q, want %q", cfg.Adapters.GitLab.BaseURL, "https://gitlab.com")
	}
	if cfg.Adapters.GitLab.PollInterval != "60s" {
		t.Errorf("adapters.gitlab.poll_interval = %q, want %q", cfg.Adapters.GitLab.PollInterval, "60s")
	}
	if cfg.Adapters.GitHub.PollInterval != "60s" {
		t.Errorf("adapters.github.poll_interval = %q, want %q", cfg.Adapters.GitHub.PollInterval, "60s")
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

func TestLoadHarnessConfig(t *testing.T) {
	path := writeTestConfig(t, `
	[harness]
	default = "codex"
	fallback = ["claude-code", "ohmypi"]

	[harness.phase]
	planning = "claude-code"
	implementation = "codex"
	review = "claude-code"
	foreman = "ohmypi"
	`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Harness.Default != HarnessCodex {
		t.Fatalf("harness.default = %q, want %q", cfg.Harness.Default, HarnessCodex)
	}
	if len(cfg.Harness.Fallback) != 2 || cfg.Harness.Fallback[0] != HarnessClaudeCode || cfg.Harness.Fallback[1] != HarnessOhMyPi {
		t.Fatalf("unexpected harness.fallback: %v", cfg.Harness.Fallback)
	}
	if cfg.Harness.Phase.Foreman != HarnessOhMyPi {
		t.Fatalf("harness.phase.foreman = %q, want %q", cfg.Harness.Phase.Foreman, HarnessOhMyPi)
	}
}

func TestLoadInvalidHarnessDefault(t *testing.T) {
	path := writeTestConfig(t, `
	[harness]
	default = "invalid"
	`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() should error on invalid harness.default")
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

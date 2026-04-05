package config

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeTestConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
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
	if cfg.Harness.Phase.Planning != HarnessOhMyPi || cfg.Harness.Phase.Implementation != HarnessOhMyPi || cfg.Harness.Phase.Review != HarnessOhMyPi || cfg.Harness.Phase.Foreman != HarnessOhMyPi {
		t.Errorf("harness phase defaults = %+v, want all ohmypi", cfg.Harness.Phase)
	}
	if cfg.Foreman.QuestionTimeout != "0" {
		t.Errorf("foreman.question_timeout = %q, want %q", cfg.Foreman.QuestionTimeout, "0")
	}
	if cfg.Adapters.Linear.PollInterval != "30s" {
		t.Errorf("adapters.linear.poll_interval = %q, want %q", cfg.Adapters.Linear.PollInterval, "30s")
	}
	if cfg.Adapters.GitLab.BaseURL != "https://gitlab.com" {
		t.Errorf("adapters.gitlab.base_url = %q, want %q", cfg.Adapters.GitLab.BaseURL, "https://gitlab.com")
	}
	if cfg.Adapters.GitHub.BaseURL != "https://api.github.com" {
		t.Errorf("adapters.github.base_url = %q, want %q", cfg.Adapters.GitHub.BaseURL, "https://api.github.com")
	}
	if cfg.Adapters.Sentry.BaseURL != "https://sentry.io/api/0" {
		t.Errorf("adapters.sentry.base_url = %q, want %q", cfg.Adapters.Sentry.BaseURL, "https://sentry.io/api/0")
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
commit:
  strategy: granular
  message_format: conventional

plan:
  max_parse_retries: 5

review:
  pass_threshold: no_critiques
  max_cycles: 7

foreman:
  question_timeout: 45s
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
		t.Errorf("review.max_cycles = %d, want %d", *cfg.Review.MaxCycles, 7)
	}
	if cfg.Foreman.QuestionTimeout != "45s" {
		t.Errorf("foreman.question_timeout = %q, want %q", cfg.Foreman.QuestionTimeout, "45s")
	}
}

func TestLoadSentryConfig(t *testing.T) {
	path := writeTestConfig(t, `
adapters:
  sentry:
    token_ref: keychain:sentry.token
    organization: acme
    projects:
      - web
      - api
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Adapters.Sentry.TokenRef != "keychain:sentry.token" {
		t.Fatalf("adapters.sentry.token_ref = %q, want %q", cfg.Adapters.Sentry.TokenRef, "keychain:sentry.token")
	}
	if cfg.Adapters.Sentry.BaseURL != "https://sentry.io/api/0" {
		t.Fatalf("adapters.sentry.base_url = %q, want %q", cfg.Adapters.Sentry.BaseURL, "https://sentry.io/api/0")
	}
	if cfg.Adapters.Sentry.Organization != "acme" {
		t.Fatalf("adapters.sentry.organization = %q, want %q", cfg.Adapters.Sentry.Organization, "acme")
	}
	if len(cfg.Adapters.Sentry.Projects) != 2 || cfg.Adapters.Sentry.Projects[0] != "web" || cfg.Adapters.Sentry.Projects[1] != "api" {
		t.Fatalf("adapters.sentry.projects = %#v, want %#v", cfg.Adapters.Sentry.Projects, []string{"web", "api"})
	}
}

func TestLoadInvalidSentryBaseURL(t *testing.T) {
	for _, raw := range []string{"://bad-url", "/api/0", "ftp://sentry.io/api/0"} {
		t.Run(raw, func(t *testing.T) {
			path := writeTestConfig(t, fmt.Sprintf("adapters:\n  sentry:\n    base_url: %s\n", raw))
			_, err := Load(path)
			if err == nil {
				t.Fatal("Load() should error on invalid adapters.sentry.base_url")
			}
		})
	}
}

func TestLoadCustomMessageFormatRequiresTemplate(t *testing.T) {
	path := writeTestConfig(t, `
commit:
  message_format: custom
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() should error when message_format=custom without message_template")
	}
}

func TestLoadCustomMessageFormatWithTemplate(t *testing.T) {
	path := writeTestConfig(t, `
commit:
  message_format: custom
  message_template: 'feat({{.Scope}}): {{.Description}}'
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
commit:
  strategy: invalid
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() should error on invalid commit.strategy")
	}
}

func TestLoadInvalidMessageFormat(t *testing.T) {
	path := writeTestConfig(t, `
commit:
  message_format: invalid
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() should error on invalid commit.message_format")
	}
}

func TestLoadInvalidPassThreshold(t *testing.T) {
	path := writeTestConfig(t, `
review:
  pass_threshold: invalid
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() should error on invalid review.pass_threshold")
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load("/nonexistent/path/config.yaml")
	if err == nil {
		t.Fatal("Load() should error on missing file")
	}
}

func TestLoadInvalidYAML(t *testing.T) {
	path := writeTestConfig(t, `
commit:
  strategy: granular
  broken: [1, 2
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() should error on invalid YAML")
	}
}

func TestLoadIgnoresUnknownFields(t *testing.T) {
	path := writeTestConfig(t, `
commit:
  strategy: granular
unknown_top_level: true
harness:
  default: codex
  fallback:
    - claude-code
  unknown_nested: value
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Commit.Strategy != CommitStrategyGranular {
		t.Fatalf("commit.strategy = %q, want %q", cfg.Commit.Strategy, CommitStrategyGranular)
	}
	if cfg.Harness.Default != HarnessCodex {
		t.Fatalf("harness.default = %q, want %q", cfg.Harness.Default, HarnessCodex)
	}
}

func TestLoadWithRepos(t *testing.T) {
	path := writeTestConfig(t, `
repos:
  myrepo: {}
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
harness:
  default: codex
  phase:
    planning: claude-code
    implementation: codex
    review: claude-code
    foreman: ohmypi
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Harness.Default != HarnessCodex {
		t.Fatalf("harness.default = %q, want %q", cfg.Harness.Default, HarnessCodex)
	}
	if cfg.Harness.Phase.Foreman != HarnessOhMyPi {
		t.Fatalf("harness.phase.foreman = %q, want %q", cfg.Harness.Phase.Foreman, HarnessOhMyPi)
	}
}

func TestLoadInvalidHarnessDefault(t *testing.T) {
	path := writeTestConfig(t, `
harness:
  default: invalid
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

func TestReviewTimeout(t *testing.T) {
	tests := []struct {
		name    string
		timeout *string
		want    time.Duration
	}{
		{name: "nil defaults to 1h", timeout: nil, want: time.Hour},
		{name: "custom 30m", timeout: ptr("30m"), want: 30 * time.Minute},
		{name: "invalid falls back to 1h", timeout: ptr("not-a-duration"), want: time.Hour},
		{name: "short 5s", timeout: ptr("5s"), want: 5 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rc := ReviewConfig{Timeout: tt.timeout}
			if got := rc.ReviewTimeout(); got != tt.want {
				t.Errorf("ReviewTimeout() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAutoFeedbackLoopDefault(t *testing.T) {
	path := writeTestConfig(t, `# empty
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Review.AutoFeedbackLoop == nil || !*cfg.Review.AutoFeedbackLoop {
		t.Errorf("AutoFeedbackLoop = %v, want ptr(true)", cfg.Review.AutoFeedbackLoop)
	}

	// Explicit false is preserved.
	path = writeTestConfig(t, `
review:
  auto_feedback_loop: false
`)
	cfg, err = Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Review.AutoFeedbackLoop == nil || *cfg.Review.AutoFeedbackLoop {
		t.Errorf("AutoFeedbackLoop = %v, want ptr(false)", cfg.Review.AutoFeedbackLoop)
	}
}

func TestIssueCommentContentDefault(t *testing.T) {
	path := writeTestConfig(t, "# empty\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Adapters.GitHub.IssueCommentContent != IssueCommentSubPlan {
		t.Errorf("github issue_comment_content = %q, want %q", cfg.Adapters.GitHub.IssueCommentContent, IssueCommentSubPlan)
	}
	if cfg.Adapters.GitLab.IssueCommentContent != IssueCommentSubPlan {
		t.Errorf("gitlab issue_comment_content = %q, want %q", cfg.Adapters.GitLab.IssueCommentContent, IssueCommentSubPlan)
	}
}

func TestIssueCommentContentInvalidRejected(t *testing.T) {
	path := writeTestConfig(t, `
adapters:
  github:
    issue_comment_content: invalid_value
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() should error on invalid adapters.github.issue_comment_content")
	}
}

func TestValidHarnessName_OpenCode(t *testing.T) {
	path := writeTestConfig(t, `
harness:
  default: opencode
  phase:
    planning: opencode
    implementation: opencode
    review: opencode
    foreman: opencode
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Harness.Default != HarnessOpenCode {
		t.Fatalf("harness.default = %q, want %q", cfg.Harness.Default, HarnessOpenCode)
	}
	if cfg.Harness.Phase.Planning != HarnessOpenCode {
		t.Fatalf("harness.phase.planning = %q, want %q", cfg.Harness.Phase.Planning, HarnessOpenCode)
	}
}

func TestLoadOpenCodeAdapterConfig(t *testing.T) {
	path := writeTestConfig(t, `
harness:
  default: opencode
adapters:
  opencode:
    binary_path: /usr/local/bin/opencode
    port: 8080
    hostname: 0.0.0.0
    model: opencode-custom
    agent: plan
    variant: high
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	oc := cfg.Adapters.OpenCode
	if oc.BinaryPath != "/usr/local/bin/opencode" {
		t.Fatalf("opencode.binary_path = %q, want %q", oc.BinaryPath, "/usr/local/bin/opencode")
	}
	if oc.Port != 8080 {
		t.Fatalf("opencode.port = %d, want 8080", oc.Port)
	}
	if oc.Hostname != "0.0.0.0" {
		t.Fatalf("opencode.hostname = %q, want %q", oc.Hostname, "0.0.0.0")
	}
	if oc.Model != "opencode-custom" {
		t.Fatalf("opencode.model = %q, want %q", oc.Model, "opencode-custom")
	}
	if oc.Agent != "plan" {
		t.Fatalf("opencode.agent = %q, want %q", oc.Agent, "plan")
	}
	if oc.Variant != "high" {
		t.Fatalf("opencode.variant = %q, want %q", oc.Variant, "high")
	}
}

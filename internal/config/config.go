// Package config handles loading and validating the config.yaml configuration.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

func ptr[T any](v T) *T { return &v }

// CommitStrategy controls how often agents commit during a session.
type CommitStrategy string

const (
	CommitStrategyGranular    CommitStrategy = "granular"
	CommitStrategySemiRegular CommitStrategy = "semi-regular"
	CommitStrategySingle      CommitStrategy = "single"
)

// CommitMessageFormat controls the format of commit messages.
type CommitMessageFormat string

const (
	CommitMessageAIGenerated  CommitMessageFormat = "ai-generated"
	CommitMessageConventional CommitMessageFormat = "conventional"
	CommitMessageCustom       CommitMessageFormat = "custom"
)

// PassThreshold controls how strict the review pipeline is.
type PassThreshold string

const (
	PassThresholdNitOnly     PassThreshold = "nit_only"
	PassThresholdMinorOK     PassThreshold = "minor_ok"
	PassThresholdNoCritiques PassThreshold = "no_critiques"
)

// IssueCommentContent controls what plan content is posted as a comment on linked issues at plan approval.
type IssueCommentContent string

const (
	// IssueCommentNone disables issue comments on plan approval.
	IssueCommentNone IssueCommentContent = "none"
	// IssueCommentOrchestratorPlan posts only the top-level orchestration narrative.
	IssueCommentOrchestratorPlan IssueCommentContent = "orchestrator_plan"
	// IssueCommentSubPlan posts only the per-repository sub-plan content (default).
	IssueCommentSubPlan IssueCommentContent = "sub_plan"
	// IssueCommentOrchestratorAndSubPlan posts the orchestration narrative followed by all sub-plans.
	IssueCommentOrchestratorAndSubPlan IssueCommentContent = "orchestrator_and_sub_plan"
	// IssueCommentFullPlan posts the full plan document including the execution-groups fence block.
	IssueCommentFullPlan IssueCommentContent = "full_plan"
)

// Config is the top-level configuration loaded from config.yaml.
type Config struct {
	Commit   CommitConfig          `yaml:"commit"`
	Plan     PlanConfig            `yaml:"plan"`
	Review   ReviewConfig          `yaml:"review"`
	Harness  HarnessConfig         `yaml:"harness"`
	Adapters AdaptersConfig        `yaml:"adapters"`
	Foreman  ForemanConfig         `yaml:"foreman"`
	Repos    map[string]RepoConfig `yaml:"repos"`
}

// CommitConfig controls agent commit behavior.
type CommitConfig struct {
	Strategy        CommitStrategy      `yaml:"strategy"`
	MessageFormat   CommitMessageFormat `yaml:"message_format"`
	MessageTemplate string              `yaml:"message_template"`
}

// PlanConfig controls the planning pipeline.
type PlanConfig struct {
	MaxParseRetries *int `yaml:"max_parse_retries"`
}

// ReviewConfig controls the review pipeline.
type ReviewConfig struct {
	PassThreshold    PassThreshold `yaml:"pass_threshold"`
	MaxCycles        *int          `yaml:"max_cycles"`
	Timeout          *string       `yaml:"timeout"`
	AutoFeedbackLoop *bool         `yaml:"auto_feedback_loop"`
}

// ReviewTimeout returns the parsed review session timeout duration.
// Falls back to 1 hour if parsing fails.
func (r ReviewConfig) ReviewTimeout() time.Duration {
	if r.Timeout != nil {
		if d, err := time.ParseDuration(*r.Timeout); err == nil {
			return d
		}
	}
	return time.Hour
}

type HarnessName string

const (
	HarnessOhMyPi     HarnessName = "ohmypi"
	HarnessClaudeCode HarnessName = "claude-code"
	HarnessCodex      HarnessName = "codex"
	HarnessOpenCode   HarnessName = "opencode"
)
const defaultPollInterval = "60s"

type HarnessConfig struct {
	Default HarnessName        `yaml:"default"`
	Phase   HarnessPhaseConfig `yaml:"phase"`
}

type HarnessPhaseConfig struct {
	Planning       HarnessName `yaml:"planning"`
	Implementation HarnessName `yaml:"implementation"`
	Review         HarnessName `yaml:"review"`
	Foreman        HarnessName `yaml:"foreman"`
}

// AdaptersConfig contains per-adapter configuration.
type AdaptersConfig struct {
	OhMyPi     OhMyPiConfig     `yaml:"ohmypi"`
	ClaudeCode ClaudeCodeConfig `yaml:"claude_code"`
	Codex      CodexConfig      `yaml:"codex"`
	OpenCode   OpenCodeConfig   `yaml:"opencode"`
	Linear     LinearConfig     `yaml:"linear"`
	Glab       GlabConfig       `yaml:"glab"`
	GitLab     GitlabConfig     `yaml:"gitlab"`
	GitHub     GithubConfig     `yaml:"github"`
	Sentry     SentryConfig     `yaml:"sentry"`
}

// LinearConfig configures the Linear GraphQL adapter.
type LinearConfig struct {
	APIKeyRef      string            `yaml:"api_key_ref"`
	APIKey         string            `yaml:"-"` //nolint:gosec // credential field name, not a hardcoded value
	TeamID         string            `yaml:"team_id"`
	AssigneeFilter string            `yaml:"assignee_filter"` // "me" or explicit user ID
	PollInterval   string            `yaml:"poll_interval"`   // e.g. "30s"; default "30s"
	StateMappings  map[string]string `yaml:"state_mappings"`  // TrackerState -> Linear workflow state UUID
}

type GitlabConfig struct {
	TokenRef            string              `yaml:"token_ref"` // keychain reference for GitLab REST API
	Token               string              `yaml:"-"`
	BaseURL             string              `yaml:"base_url"`      // default: https://gitlab.com
	Assignee            string              `yaml:"assignee"`      // username filter for Watch
	PollInterval        string              `yaml:"poll_interval"` // default: 60s
	StateMappings       map[string]string   `yaml:"state_mappings"`
	IssueCommentContent IssueCommentContent `yaml:"issue_comment_content"`
}

type GithubConfig struct {
	TokenRef            string              `yaml:"token_ref"` // optional keychain reference; gh auth token remains fallback
	Token               string              `yaml:"-"`
	BaseURL             string              `yaml:"base_url"`      // default: https://api.github.com
	Assignee            string              `yaml:"assignee"`      // username filter for Watch; "me" resolves via /user
	PollInterval        string              `yaml:"poll_interval"` // default: 60s
	Reviewers           []string            `yaml:"reviewers"`
	Labels              []string            `yaml:"labels"`
	StateMappings       map[string]string   `yaml:"state_mappings"`
	IssueCommentContent IssueCommentContent `yaml:"issue_comment_content"`
}

type SentryConfig struct {
	TokenRef        string   `yaml:"token_ref"`
	Token           string   `yaml:"-"`
	BaseURL         string   `yaml:"base_url"`
	BaseURLExplicit bool     `yaml:"-"`
	Organization    string   `yaml:"organization"`
	Projects        []string `yaml:"projects"`
}

// GlabConfig configures the glab CLI adapter.
// All fields are optional; the adapter is always registered regardless.
type GlabConfig struct {
	// Reviewers is a list of GitLab usernames added as reviewers to created MRs.
	Reviewers []string `yaml:"reviewers"`
	// Labels is a list of GitLab label names added to created MRs.
	Labels []string `yaml:"labels"`
}

// OhMyPiConfig configures the oh-my-pi agent harness.
type OhMyPiConfig struct {
	BunPath       string `yaml:"bun_path"`
	BridgePath    string `yaml:"bridge_path"`
	ThinkingLevel string `yaml:"thinking_level"`
}

// ClaudeCodeConfig configures the claude-agent bridge harness.
type ClaudeCodeConfig struct {
	// BunPath is the path to the bun executable.
	// Defaults to "bun" resolved via PATH.
	BunPath string `yaml:"bun_path"`

	// BridgePath overrides the default bridge script location.
	// Defaults to bridge/claude-agent-bridge.ts next to the substrate binary.
	BridgePath string `yaml:"bridge_path"`

	// Model is the Claude model name. Empty means use Claude's own default.
	Model string `yaml:"model"`

	// PermissionMode controls tool permissions. Defaults to "acceptEdits".
	// Valid: "default", "acceptEdits", "bypassPermissions", "dontAsk".
	PermissionMode string `yaml:"permission_mode"`

	// MaxTurns caps the agentic turn count (0 = SDK default).
	MaxTurns int `yaml:"max_turns"`

	// MaxBudgetUSD caps spending per session (0 = unlimited).
	MaxBudgetUSD float64 `yaml:"max_budget_usd"`
}

// OpenCodeConfig configures the opencode server harness.
type OpenCodeConfig struct {
	// ServerPath is the path to the opencode binary.
	// Defaults to "opencode" resolved via PATH.
	ServerPath string `yaml:"server_path"`

	// Port is the HTTP port for opencode serve.
	// 0 means let the server pick an available port.
	Port int `yaml:"port"`

	// Hostname is the bind address for opencode serve.
	// Defaults to "127.0.0.1".
	Hostname string `yaml:"hostname"`

	// Model is the provider/model identifier (e.g. "anthropic/claude-sonnet-4-20250514").
	// Empty means use opencode's own default.
	Model string `yaml:"model"`

	// Agent selects the opencode agent type: "build" (full-access) or "plan" (read-only).
	// Defaults to "build".
	Agent string `yaml:"agent"`

	// Variant selects the model variant (reasoning effort) for prompts.
	// Empty defers to the model's own default.
	// Valid values depend on the provider and model:
	//   Anthropic: high, max (adaptive models also: low, medium)
	//   OpenAI: none, minimal, low, medium, high, xhigh
	//   Google: low, high (2.5 models: high, max)
	// Unsupported values are silently ignored by opencode.
	Variant string `yaml:"variant"`
}

type CodexConfig struct {
	BinaryPath   string `yaml:"binary_path"`
	Model        string `yaml:"model"`
	ApprovalMode string `yaml:"approval_mode"`
	FullAuto     bool   `yaml:"full_auto"`
	Quiet        bool   `yaml:"quiet"`
}

// ForemanConfig controls the foreman question-answering system.
type ForemanConfig struct {
	QuestionTimeout string `yaml:"question_timeout"`
}

// RepoConfig contains per-repo overrides.
type RepoConfig struct {
	// DocPaths are documentation paths to include in planning context.
	DocPaths []string `yaml:"doc_paths"`
}

// GlobalDir returns the path to the global Substrate directory.
// It respects the SUBSTRATE_HOME environment variable if set.
// Tilde (~) is expanded and relative paths are resolved to absolute.
func GlobalDir() (string, error) {
	home := os.Getenv("SUBSTRATE_HOME")
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("get home directory: %w", err)
		}
		return filepath.Join(userHome, ".substrate"), nil
	}

	// Expand tilde if present
	if strings.HasPrefix(home, "~") {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("get home directory: %w", err)
		}
		if home == "~" {
			return userHome, nil
		}
		// Handle ~/path or ~user (latter not supported, treat as ~/path)
		return filepath.Join(userHome, home[2:]), nil
	}

	// Resolve to absolute path
	abs, err := filepath.Abs(home)
	if err != nil {
		return "", fmt.Errorf("resolve absolute path: %w", err)
	}
	return abs, nil
}

// GlobalDBPath returns the path to the global SQLite database.
func GlobalDBPath() (string, error) {
	dir, err := GlobalDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "state.db"), nil
}

// ConfigPath returns the path to the configuration file.
func ConfigPath() (string, error) {
	dir, err := GlobalDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.yaml"), nil
}

// SessionsDir returns the path to the sessions directory.
func SessionsDir() (string, error) {
	dir, err := GlobalDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "sessions"), nil
}

// Load reads and validates a config.yaml configuration file.
// Missing fields are filled with defaults before validation.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}

	applyDefaults(cfg)

	if err := validate(cfg); err != nil {
		return nil, fmt.Errorf("validating config: %w", err)
	}

	return cfg, nil
}

func applyDefaults(cfg *Config) {
	if cfg.Commit.Strategy == "" {
		cfg.Commit.Strategy = CommitStrategySemiRegular
	}
	if cfg.Commit.MessageFormat == "" {
		cfg.Commit.MessageFormat = CommitMessageAIGenerated
	}
	if cfg.Plan.MaxParseRetries == nil {
		cfg.Plan.MaxParseRetries = ptr(2)
	}
	if cfg.Review.PassThreshold == "" {
		cfg.Review.PassThreshold = PassThresholdMinorOK
	}
	if cfg.Review.MaxCycles == nil {
		cfg.Review.MaxCycles = ptr(3)
	}
	if cfg.Review.Timeout == nil {
		cfg.Review.Timeout = ptr("1h")
	}
	if cfg.Review.AutoFeedbackLoop == nil {
		cfg.Review.AutoFeedbackLoop = ptr(true)
	}
	if cfg.Harness.Default == "" {
		cfg.Harness.Default = HarnessOhMyPi
	}
	if cfg.Harness.Phase.Planning == "" {
		cfg.Harness.Phase.Planning = cfg.Harness.Default
	}
	if cfg.Harness.Phase.Implementation == "" {
		cfg.Harness.Phase.Implementation = cfg.Harness.Default
	}
	if cfg.Harness.Phase.Review == "" {
		cfg.Harness.Phase.Review = cfg.Harness.Default
	}
	if cfg.Harness.Phase.Foreman == "" {
		cfg.Harness.Phase.Foreman = cfg.Harness.Default
	}
	if cfg.Foreman.QuestionTimeout == "" {
		cfg.Foreman.QuestionTimeout = "0"
	}
	if cfg.Adapters.Linear.PollInterval == "" {
		cfg.Adapters.Linear.PollInterval = "30s"
	}
	if cfg.Adapters.GitLab.BaseURL == "" {
		cfg.Adapters.GitLab.BaseURL = "https://gitlab.com"
	}
	if cfg.Adapters.GitLab.PollInterval == "" {
		cfg.Adapters.GitLab.PollInterval = defaultPollInterval
	}
	if cfg.Adapters.GitHub.BaseURL == "" {
		cfg.Adapters.GitHub.BaseURL = "https://api.github.com"
	}
	if cfg.Adapters.GitHub.PollInterval == "" {
		cfg.Adapters.GitHub.PollInterval = defaultPollInterval
	}
	if cfg.Adapters.GitHub.IssueCommentContent == "" {
		cfg.Adapters.GitHub.IssueCommentContent = IssueCommentSubPlan
	}
	if cfg.Adapters.GitLab.IssueCommentContent == "" {
		cfg.Adapters.GitLab.IssueCommentContent = IssueCommentSubPlan
	}
	if cfg.Adapters.Sentry.BaseURL == "" {
		cfg.Adapters.Sentry.BaseURL = DefaultSentryBaseURL
	}
}

func validate(cfg *Config) error {
	switch cfg.Commit.Strategy {
	case CommitStrategyGranular, CommitStrategySemiRegular, CommitStrategySingle:
	default:
		return fmt.Errorf("invalid commit.strategy: %q (must be granular, semi-regular, or single)", cfg.Commit.Strategy)
	}

	switch cfg.Commit.MessageFormat {
	case CommitMessageAIGenerated, CommitMessageConventional, CommitMessageCustom:
	default:
		return fmt.Errorf("invalid commit.message_format: %q (must be ai-generated, conventional, or custom)", cfg.Commit.MessageFormat)
	}

	if cfg.Commit.MessageFormat == CommitMessageCustom && cfg.Commit.MessageTemplate == "" {
		return fmt.Errorf("commit.message_template is required when commit.message_format is %q", CommitMessageCustom)
	}

	if *cfg.Plan.MaxParseRetries < 0 {
		return fmt.Errorf("plan.max_parse_retries must be non-negative, got %d", *cfg.Plan.MaxParseRetries)
	}

	switch cfg.Review.PassThreshold {
	case PassThresholdNitOnly, PassThresholdMinorOK, PassThresholdNoCritiques:
	default:
		return fmt.Errorf("invalid review.pass_threshold: %q (must be nit_only, minor_ok, or no_critiques)", cfg.Review.PassThreshold)
	}

	if *cfg.Review.MaxCycles < 1 {
		return fmt.Errorf("review.max_cycles must be at least 1, got %d", *cfg.Review.MaxCycles)
	}

	if cfg.Foreman.QuestionTimeout != "" {
		if _, err := time.ParseDuration(cfg.Foreman.QuestionTimeout); err != nil {
			return fmt.Errorf("invalid foreman.question_timeout: %w", err)
		}
	}

	if cfg.Adapters.GitHub.BaseURL != "" {
		if err := validateHTTPSURL(cfg.Adapters.GitHub.BaseURL); err != nil {
			return fmt.Errorf("invalid github base_url: %w", err)
		}
	}

	if cfg.Adapters.GitLab.BaseURL != "" {
		if err := validateHTTPSURL(cfg.Adapters.GitLab.BaseURL); err != nil {
			return fmt.Errorf("invalid gitlab base_url: %w", err)
		}
	}

	if cfg.Adapters.Sentry.BaseURL != "" {
		if err := validateAbsoluteHTTPURL(cfg.Adapters.Sentry.BaseURL); err != nil {
			return fmt.Errorf("invalid sentry base_url: %w", err)
		}
	}

	validIssueCommentContent := map[IssueCommentContent]bool{
		IssueCommentNone:                   true,
		IssueCommentOrchestratorPlan:       true,
		IssueCommentSubPlan:                true,
		IssueCommentOrchestratorAndSubPlan: true,
		IssueCommentFullPlan:               true,
	}
	if !validIssueCommentContent[cfg.Adapters.GitHub.IssueCommentContent] {
		return fmt.Errorf("invalid adapters.github.issue_comment_content: %q (must be none, orchestrator_plan, sub_plan, orchestrator_and_sub_plan, or full_plan)", cfg.Adapters.GitHub.IssueCommentContent)
	}
	if !validIssueCommentContent[cfg.Adapters.GitLab.IssueCommentContent] {
		return fmt.Errorf("invalid adapters.gitlab.issue_comment_content: %q (must be none, orchestrator_plan, sub_plan, orchestrator_and_sub_plan, or full_plan)", cfg.Adapters.GitLab.IssueCommentContent)
	}

	validHarnesses := map[HarnessName]bool{
		HarnessOhMyPi:     true,
		HarnessClaudeCode: true,
		HarnessCodex:      true,
		HarnessOpenCode:   true,
	}
	if !validHarnesses[cfg.Harness.Default] {
		return fmt.Errorf("invalid harness.default: %q", cfg.Harness.Default)
	}
	for field, value := range map[string]HarnessName{
		"planning":       cfg.Harness.Phase.Planning,
		"implementation": cfg.Harness.Phase.Implementation,
		"review":         cfg.Harness.Phase.Review,
		"foreman":        cfg.Harness.Phase.Foreman,
	} {
		if !validHarnesses[value] {
			return fmt.Errorf("invalid harness.phase.%s: %q", field, value)
		}
	}

	return nil
}

// IssueCommentContentForSource returns the IssueCommentContent configured for the given
// work-item source adapter ("github" or "gitlab"). Falls back to IssueCommentSubPlan
// for unknown sources.
func (c *Config) IssueCommentContentForSource(source string) IssueCommentContent {
	switch source {
	case "github":
		return c.Adapters.GitHub.IssueCommentContent
	case "gitlab":
		return c.Adapters.GitLab.IssueCommentContent
	default:
		return IssueCommentSubPlan
	}
}

func validateAbsoluteHTTPURL(raw string) error {
	parsed, err := url.ParseRequestURI(raw)
	if err != nil {
		return err
	}
	if !parsed.IsAbs() || parsed.Host == "" {
		return errors.New("must be an absolute URL")
	}
	switch parsed.Scheme {
	case "http", "https":
		return nil
	default:
		return errors.New("must use http or https")
	}
}

func validateHTTPSURL(raw string) error {
	parsed, err := url.ParseRequestURI(raw)
	if err != nil {
		return err
	}
	if !parsed.IsAbs() || parsed.Host == "" {
		return errors.New("must be an absolute URL")
	}
	if parsed.Scheme != "https" {
		return errors.New("must use https (bearer tokens must not be sent over plaintext)")
	}
	return nil
}

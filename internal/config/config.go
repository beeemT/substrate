// Package config handles loading and validating the config.yaml configuration.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/beeemT/substrate/internal/domain"
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

// IssueActionScope controls which linked issues receive plan-approval actions
// (comments, assignment, and status transitions) on plan approval.
type IssueActionScope string

const (
	// IssueActionScopeAll applies actions to all linked issues (default).
	IssueActionScopeAll IssueActionScope = "all"
	// IssueActionScopeMine applies actions only to issues in the user's own namespace.
	IssueActionScopeMine IssueActionScope = "mine"
	// IssueActionScopeNone skips all plan-approval actions on linked issues.
	IssueActionScopeNone IssueActionScope = "none"
)

// UIConfig controls TUI presentation defaults.
type UIConfig struct {
	// DefaultFilter sets the initial filter mode for the sessions list.
	// Valid values: "all", "active", "attention", "completed".
	DefaultFilter string `yaml:"default_filter"`
	// DefaultGroup sets the initial grouping dimension for the sessions list.
	// Valid values: "none", "state", "source", "created", "activity".
	DefaultGroup string `yaml:"default_group"`
	// LogLevel sets the minimum log level captured to the in-memory log buffer.
	// Valid values: "debug", "info", "warn", "error".
	LogLevel string `yaml:"log_level"`
}

// DaemonRegistryEntry describes a daemon endpoint known to the local TUI.
type DaemonRegistryEntry struct {
	Label               string `yaml:"label"`
	Kind                string `yaml:"kind"`
	Address             string `yaml:"address"`
	TokenRef            string `yaml:"token_ref"`
	AutoManaged         bool   `yaml:"auto_managed"`
	LastSeenVersion     string `yaml:"last_seen_version"`
	LastSeenWorkspaceID string `yaml:"last_seen_workspace_id"`
}

// TUIConfig owns local visualization settings and daemon selection state.
type TUIConfig struct {
	ActiveDaemon string                         `yaml:"active_daemon"`
	UI           UIConfig                       `yaml:"ui"`
	Daemons      map[string]DaemonRegistryEntry `yaml:"daemons"`
}

// DaemonRuntimeConfig owns daemon process runtime settings.
type DaemonRuntimeConfig struct {
	Bind              DaemonBindConfig `yaml:"bind"`
	DatabasePath      string           `yaml:"database_path"`
	LogLevel          string           `yaml:"log_level"`
	ReflectionEnabled bool             `yaml:"reflection_enabled"`
}

// DaemonBindConfig describes where the daemon listens.
type DaemonBindConfig struct {
	Kind       string `yaml:"kind"`
	SocketPath string `yaml:"socket_path"`
}

// DaemonConfig owns daemon runtime and product orchestration settings.
type DaemonConfig struct {
	Runtime  DaemonRuntimeConfig   `yaml:"runtime"`
	Commit   CommitConfig          `yaml:"commit"`
	Plan     PlanConfig            `yaml:"plan"`
	Review   ReviewConfig          `yaml:"review"`
	Harness  HarnessConfig         `yaml:"harness"`
	Adapters AdaptersConfig        `yaml:"adapters"`
	Foreman  ForemanConfig         `yaml:"foreman"`
	RepoDocs RepoDocsConfig        `yaml:"repo_docs"`
	Repos    map[string]RepoConfig `yaml:"repos"`
}

// Config is the top-level configuration loaded from config.yaml.
type Config struct {
	Daemon DaemonConfig `yaml:"daemon"`
	TUI    TUIConfig    `yaml:"tui"`

	Commit   CommitConfig          `yaml:"commit,omitempty"`
	Plan     PlanConfig            `yaml:"plan,omitempty"`
	Review   ReviewConfig          `yaml:"review,omitempty"`
	Harness  HarnessConfig         `yaml:"harness,omitempty"`
	Adapters AdaptersConfig        `yaml:"adapters,omitempty"`
	Foreman  ForemanConfig         `yaml:"foreman,omitempty"`
	RepoDocs RepoDocsConfig        `yaml:"repo_docs,omitempty"`
	Repos    map[string]RepoConfig `yaml:"repos,omitempty"`
	UI       UIConfig              `yaml:"ui,omitempty"`
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
	HarnessACP        HarnessName = "acp"
)
const defaultPollInterval = "5m"

type HarnessConfig struct {
	// The harness used for all agent phases.
	Default HarnessName `yaml:"default"`
}

// AdaptersConfig contains per-adapter configuration.
type AdaptersConfig struct {
	OhMyPi     OhMyPiConfig     `yaml:"ohmypi"`
	ClaudeCode ClaudeCodeConfig `yaml:"claude_code"`
	Codex      CodexConfig      `yaml:"codex"`
	OpenCode   OpenCodeConfig   `yaml:"opencode"`
	ACP        ACPConfig        `yaml:"acp"`
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
	PollInterval   string            `yaml:"poll_interval"`   // e.g. "5m"; default "5m"
	StateMappings  map[string]string `yaml:"state_mappings"`  // TrackerState -> Linear workflow state UUID
}

const DefaultGitLabInProgressStatus = "In progress"

type GitlabConfig struct {
	TokenRef              string              `yaml:"token_ref"` // keychain reference for GitLab REST API
	Token                 string              `yaml:"-"`
	BaseURL               string              `yaml:"base_url"`                // default: https://gitlab.com
	Assignee              string              `yaml:"assignee"`                // username filter for Watch; "me" resolves via /user
	PollInterval          string              `yaml:"poll_interval"`           // default: 5m
	StatusRefreshInterval string              `yaml:"status_refresh_interval"` // default: 5m
	StateMappings         map[string]string   `yaml:"state_mappings"`
	IssueCommentContent   IssueCommentContent `yaml:"issue_comment_content"`
	IssueActionScope      IssueActionScope    `yaml:"issue_comment_scope"` // all, mine, none
	// InProgressStatus is the Work Item status name to set on linked issues
	// at plan approval via GraphQL. Defaults to GitLab's built-in "In progress" status.
	InProgressStatus string `yaml:"in_progress_status"`
}

type GithubConfig struct {
	TokenRef            string              `yaml:"token_ref"` // optional keychain reference; gh auth token remains fallback
	Token               string              `yaml:"-"`
	BaseURL             string              `yaml:"base_url"`      // default: https://api.github.com
	Assignee            string              `yaml:"assignee"`      // username filter for Watch; "me" resolves via /user
	PollInterval        string              `yaml:"poll_interval"` // default: 5m
	Reviewers           []string            `yaml:"reviewers"`
	Labels              []string            `yaml:"labels"`
	StateMappings       map[string]string   `yaml:"state_mappings"`
	IssueCommentContent IssueCommentContent `yaml:"issue_comment_content"`
	IssueActionScope    IssueActionScope    `yaml:"issue_comment_scope"` // all, mine, none
	PostMergeCloseIssue bool                `yaml:"post_merge_close_issue"`
	// InProgressStatus is the issue label to apply to linked issues at plan
	// approval. Leave empty to skip.
	InProgressStatus string `yaml:"in_progress_status"`
}

type SentryConfig struct {
	TokenRef        string   `yaml:"token_ref"`
	Token           string   `yaml:"-"`
	BaseURL         string   `yaml:"base_url"`
	BaseURLExplicit bool     `yaml:"-"`
	Organization    string   `yaml:"organization"`
	Projects        []string `yaml:"projects"`
	PollInterval    string   `yaml:"poll_interval"` // default: 5m
}

// GlabConfig configures the glab CLI adapter.
// All fields are optional; the adapter is always registered regardless.
type GlabConfig struct {
	// BaseURL is the GitLab instance URL (e.g., https://gitlab.justtrack.io).
	// Populated automatically from glab auth status if not set.
	BaseURL string `yaml:"base_url"`
	// Reviewers is a list of GitLab usernames added as reviewers to created MRs.
	Reviewers []string `yaml:"reviewers"`
	// Labels is a list of GitLab label names added to created MRs.
	Labels []string `yaml:"labels"`
	// PostMergeCloseIssue closes the linked GitLab issue when all MRs for a work item are merged.
	PostMergeCloseIssue bool `yaml:"post_merge_close_issue"`
}

// ACPConfig configures a generic Agent Client Protocol stdio harness.
type ACPConfig struct {
	Agent              string            `yaml:"agent"`
	Command            string            `yaml:"command"`
	Args               []string          `yaml:"args"`
	Env                map[string]string `yaml:"env"`
	RegistryID         string            `yaml:"registry_id"`
	Model              string            `yaml:"model"`
	Mode               string            `yaml:"mode"`
	ThoughtLevel       string            `yaml:"thought_level"`
	QuestionBridgePath string            `yaml:"question_bridge_path"`
	ClientFS           *bool             `yaml:"client_fs"`
	ClientTerminal     *bool             `yaml:"client_terminal"`
	AuthTerminal       *bool             `yaml:"auth_terminal"`
}

// ValidThinkingLevels lists the accepted values for OhMyPiConfig.ThinkingLevel.
// An empty string means "defer to the oh-my-pi agent's own default."
var ValidThinkingLevels = []string{"", "off", "minimal", "low", "medium", "high", "xhigh"}

// OhMyPiConfig configures the oh-my-pi agent harness.
type OhMyPiConfig struct {
	BunPath    string `yaml:"bun_path"`
	BridgePath string `yaml:"bridge_path"`
	// ThinkingLevel controls reasoning depth for the oh-my-pi agent.
	// Empty defers to the agent's own configured default.
	ThinkingLevel string `yaml:"thinking_level"`
	// Model is the model pattern for new sessions (e.g. "anthropic/claude-sonnet-4-20250514").
	// Empty means use oh-my-pi's own default.
	Model string `yaml:"model"`
}

// ValidateThinkingLevel returns an error if level is not a recognised value.
func ValidateThinkingLevel(level string) error {
	if level == "" {
		return nil
	}
	if !slices.Contains(ValidThinkingLevels, level) {
		return fmt.Errorf("invalid thinking_level %q: must be one of %v", level, ValidThinkingLevels)
	}
	return nil
}

// ValidClaudeThinkingLevels lists the accepted values for ClaudeCodeConfig.Thinking.
// An empty string means "defer to Claude's own default."
var ValidClaudeThinkingLevels = []string{"", "adaptive", "enabled", "disabled"}

// ValidClaudeEffortLevels lists the accepted values for ClaudeCodeConfig.Effort.
// An empty string means "defer to Claude's own default."
var ValidClaudeEffortLevels = []string{"", "low", "medium", "high", "max"}

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

	// Thinking controls extended thinking mode. Empty uses Claude's default.
	Thinking string `yaml:"thinking"`

	// Effort controls reasoning depth and token spend. Empty uses Claude's default.
	Effort string `yaml:"effort"`
}

// ValidateClaudeThinking returns an error if level is not a recognised value.
func ValidateClaudeThinking(level string) error {
	if level == "" {
		return nil
	}
	if !slices.Contains(ValidClaudeThinkingLevels, level) {
		return fmt.Errorf("invalid thinking %q: must be one of %v", level, ValidClaudeThinkingLevels)
	}
	return nil
}

// ValidateClaudeEffort returns an error if level is not a recognised value.
func ValidateClaudeEffort(level string) error {
	if level == "" {
		return nil
	}
	if !slices.Contains(ValidClaudeEffortLevels, level) {
		return fmt.Errorf("invalid effort %q: must be one of %v", level, ValidClaudeEffortLevels)
	}
	return nil
}

// ValidateCodexReasoningEffort returns an error if level is not a recognised value.
func ValidateCodexReasoningEffort(level string) error {
	if level == "" {
		return nil
	}
	if !slices.Contains(ValidCodexReasoningEfforts, level) {
		return fmt.Errorf("invalid reasoning_effort %q: must be one of %v", level, ValidCodexReasoningEfforts)
	}
	return nil
}

// OpenCodeConfig configures the opencode server harness.
type OpenCodeConfig struct {
	// BinaryPath is the path to the opencode binary.
	// Defaults to "opencode" resolved via PATH.
	BinaryPath string `yaml:"binary_path"`

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

// ValidCodexReasoningEfforts lists the accepted values for CodexConfig.ReasoningEffort.
// An empty string means "defer to the Codex model's own default."
var ValidCodexReasoningEfforts = []string{"", "none", "minimal", "low", "medium", "high", "xhigh"}

// CodexConfig configures the OpenAI Codex CLI harness.
type CodexConfig struct {
	BinaryPath string `yaml:"binary_path"`
	// Model is the OpenAI model name passed via -m flag (e.g. "o4", "o4-mini", "o3").
	// Empty uses Codex's own default.
	Model string `yaml:"model"`
	// ReasoningEffort controls model reasoning depth via -c model_reasoning_effort.
	// Empty defers to the model's own default.
	ReasoningEffort string `yaml:"reasoning_effort"`
}

// ForemanConfig controls the foreman question-answering system.
type ForemanConfig struct {
	QuestionTimeout string `yaml:"question_timeout"`
}

// RepoDocsConfig contains workspace-level documentation paths used during planning.
type RepoDocsConfig struct {
	// Paths are documentation repositories or folders outside implementation repos.
	Paths []string `yaml:"paths"`
}

// RepoConfig contains per-repo overrides.
type RepoConfig struct {
	// IssueActionScope controls whether plan-approval actions are applied.
	// Valid values: "all" (default), "mine", "none".
	IssueActionScope IssueActionScope `yaml:"issue_comment_scope"`
}

// IssueActionScopeOrDefault returns the scope, defaulting to "all" if empty.
func (r RepoConfig) IssueActionScopeOrDefault() IssueActionScope {
	switch r.IssueActionScope {
	case IssueActionScopeNone:
		return IssueActionScopeNone
	case IssueActionScopeMine:
		return IssueActionScopeMine
	default:
		return IssueActionScopeAll
	}
}

// IssueActionScopeForRepo returns the action scope for a specific repository.
func (c Config) IssueActionScopeForRepo(repo string) IssueActionScope {
	if repoConfig, ok := c.Repos[repo]; ok && repoConfig.IssueActionScope != "" {
		return repoConfig.IssueActionScopeOrDefault()
	}
	return ""
}

// IssueActionScopesForWorkItem returns a map of repository → action scope for all
// repositories linked to a work item's source and source_item_ids.
// Repositories without an explicit scope entry default to "all".
func (c Config) IssueActionScopesForWorkItem(workItem domain.Session) map[string]string {
	repoScopes := make(map[string]string)
	seen := make(map[string]struct{})
	addRepo := func(id string) {
		if id == "" {
			return
		}
		// SourceItemIDs may be "owner/repo#number" or "owner/repo" format.
		repo := id
		if idx := strings.IndexByte(id, '#'); idx >= 0 {
			repo = id[:idx]
		}
		if _, ok := seen[repo]; ok {
			return
		}
		seen[repo] = struct{}{}
		repoScopes[repo] = string(c.IssueActionScopeForRepo(repo))
	}
	// Add primary source.
	addRepo(workItem.ExternalID)
	// Add all source item IDs.
	for _, id := range workItem.SourceItemIDs {
		addRepo(id)
	}
	return repoScopes
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

	normalizeConfigRoots(cfg)
	applyDefaults(cfg)
	syncConfigRoots(cfg)

	if err := validate(cfg); err != nil {
		return nil, fmt.Errorf("validating config: %w", err)
	}

	return cfg, nil
}

// Save writes cfg to path using the canonical nested daemon/TUI config shape.
func Save(path string, cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("config is required")
	}
	normalizeConfigRoots(cfg)
	syncConfigRoots(cfg)
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write config file: %w", err)
	}
	return nil
}

func (c Config) MarshalYAML() (any, error) {
	syncConfigRoots(&c)
	type nestedConfig struct {
		Daemon DaemonConfig `yaml:"daemon"`
		TUI    TUIConfig    `yaml:"tui"`
	}
	return nestedConfig{Daemon: c.Daemon, TUI: c.TUI}, nil
}

// normalizeConfigRoots copies nested daemon/UI subsections into their legacy
// top-level mirrors ONLY for subsections that were explicitly set in the
// nested form. Legacy top-level subsections that have no corresponding
// nested entry are preserved untouched, so a config that mixes
// `daemon.commit:` with a top-level `harness:` round-trips without losing
// either side.
// normalizeConfigRoots merges nested daemon/UI subsections into their legacy
// top-level mirrors on a per-field basis. Each non-zero nested field
// overrides its legacy top-level sibling, and legacy top-level fields that
// have no nested counterpart are preserved untouched. This lets a config mix
// `tui.ui.log_level: debug` with a top-level `ui.default_filter: active`
// without either side clobbering the other.
func normalizeConfigRoots(cfg *Config) {
	if cfg == nil {
		return
	}
	if daemonHasCommitConfig(cfg.Daemon) {
		cfg.Commit = cfg.Daemon.Commit
	}
	if daemonHasPlanConfig(cfg.Daemon) {
		cfg.Plan = cfg.Daemon.Plan
	}
	if daemonHasReviewConfig(cfg.Daemon) {
		cfg.Review = cfg.Daemon.Review
	}
	if daemonHasHarnessConfig(cfg.Daemon) {
		cfg.Harness = cfg.Daemon.Harness
	}
	if daemonHasAdaptersConfig(cfg.Daemon) {
		cfg.Adapters = cfg.Daemon.Adapters
	}
	if daemonHasForemanConfig(cfg.Daemon) {
		cfg.Foreman = cfg.Daemon.Foreman
	}
	if daemonHasRepoDocsConfig(cfg.Daemon) {
		cfg.RepoDocs = cfg.Daemon.RepoDocs
	}
	if cfg.Daemon.Repos != nil {
		cfg.Repos = cfg.Daemon.Repos
	}
	mergeUIConfig(&cfg.UI, cfg.TUI.UI)
}

func mergeUIConfig(dst *UIConfig, src UIConfig) {
	if src.DefaultFilter != "" {
		dst.DefaultFilter = src.DefaultFilter
	}
	if src.DefaultGroup != "" {
		dst.DefaultGroup = src.DefaultGroup
	}
	if src.LogLevel != "" {
		dst.LogLevel = src.LogLevel
	}
}

func daemonHasCommitConfig(cfg DaemonConfig) bool {
	return !reflect.DeepEqual(cfg.Commit, CommitConfig{})
}

func daemonHasPlanConfig(cfg DaemonConfig) bool {
	return !reflect.DeepEqual(cfg.Plan, PlanConfig{})
}

func daemonHasReviewConfig(cfg DaemonConfig) bool {
	return !reflect.DeepEqual(cfg.Review, ReviewConfig{})
}

func daemonHasHarnessConfig(cfg DaemonConfig) bool {
	return !reflect.DeepEqual(cfg.Harness, HarnessConfig{})
}

func daemonHasAdaptersConfig(cfg DaemonConfig) bool {
	return !reflect.DeepEqual(cfg.Adapters, AdaptersConfig{})
}

func daemonHasForemanConfig(cfg DaemonConfig) bool {
	return !reflect.DeepEqual(cfg.Foreman, ForemanConfig{})
}

func daemonHasRepoDocsConfig(cfg DaemonConfig) bool {
	return !reflect.DeepEqual(cfg.RepoDocs, RepoDocsConfig{})
}

func syncConfigRoots(cfg *Config) {
	if cfg == nil {
		return
	}
	cfg.Daemon.Commit = cfg.Commit
	cfg.Daemon.Plan = cfg.Plan
	cfg.Daemon.Review = cfg.Review
	cfg.Daemon.Harness = cfg.Harness
	cfg.Daemon.Adapters = cfg.Adapters
	cfg.Daemon.Foreman = cfg.Foreman
	cfg.Daemon.RepoDocs = cfg.RepoDocs
	cfg.Daemon.Repos = cfg.Repos
	cfg.TUI.UI = cfg.UI
	if cfg.TUI.ActiveDaemon == "" {
		cfg.TUI.ActiveDaemon = "local"
	}
	if cfg.TUI.Daemons == nil {
		cfg.TUI.Daemons = map[string]DaemonRegistryEntry{}
	}
	if _, ok := cfg.TUI.Daemons["local"]; !ok {
		cfg.TUI.Daemons["local"] = DaemonRegistryEntry{
			Label:       "Local",
			Kind:        "local",
			TokenRef:    "keychain:daemon.local.access_token",
			AutoManaged: true,
		}
	}
	if cfg.Daemon.Runtime.Bind.Kind == "" {
		cfg.Daemon.Runtime.Bind.Kind = "unix"
	}
	if cfg.Daemon.Runtime.LogLevel == "" {
		cfg.Daemon.Runtime.LogLevel = "info"
	}
}

// hasDaemonProductConfig reports whether any nested daemon product subsection
// is configured. It exists for callers that want a single boolean (e.g. the
// marshaler deciding whether to emit a `daemon:` block) without choosing
// which subsections to merge.
func hasDaemonProductConfig(cfg DaemonConfig) bool {
	return daemonHasCommitConfig(cfg) ||
		daemonHasPlanConfig(cfg) ||
		daemonHasReviewConfig(cfg) ||
		daemonHasHarnessConfig(cfg) ||
		daemonHasAdaptersConfig(cfg) ||
		daemonHasForemanConfig(cfg) ||
		daemonHasRepoDocsConfig(cfg) ||
		cfg.Repos != nil
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
	// ACP client-side filesystem and terminal methods default on for OMP parity.
	if cfg.Adapters.ACP.ClientFS == nil {
		cfg.Adapters.ACP.ClientFS = ptr(true)
	}
	if cfg.Adapters.ACP.ClientTerminal == nil {
		cfg.Adapters.ACP.ClientTerminal = ptr(true)
	}
	if cfg.Adapters.ACP.AuthTerminal == nil {
		cfg.Adapters.ACP.AuthTerminal = ptr(true)
	}
	if cfg.Foreman.QuestionTimeout == "" {
		cfg.Foreman.QuestionTimeout = "0"
	}
	if cfg.Adapters.Linear.PollInterval == "" {
		cfg.Adapters.Linear.PollInterval = defaultPollInterval
	}
	if cfg.Adapters.GitLab.BaseURL == "" {
		cfg.Adapters.GitLab.BaseURL = InferGlabBaseURL()
	}
	// Glab adapter also needs the base URL for remote API calls.
	if cfg.Adapters.Glab.BaseURL == "" {
		cfg.Adapters.Glab.BaseURL = InferGlabBaseURL()
	}
	if cfg.Adapters.GitLab.PollInterval == "" {
		cfg.Adapters.GitLab.PollInterval = defaultPollInterval
	}
	if cfg.Adapters.GitLab.StatusRefreshInterval == "" {
		cfg.Adapters.GitLab.StatusRefreshInterval = defaultPollInterval
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
	if cfg.Adapters.GitHub.IssueActionScope == "" {
		cfg.Adapters.GitHub.IssueActionScope = IssueActionScopeAll
	}
	if cfg.Adapters.GitLab.IssueCommentContent == "" {
		cfg.Adapters.GitLab.IssueCommentContent = IssueCommentSubPlan
	}
	if cfg.Adapters.GitLab.IssueActionScope == "" {
		cfg.Adapters.GitLab.IssueActionScope = IssueActionScopeAll
	}
	if cfg.Adapters.GitLab.InProgressStatus == "" {
		cfg.Adapters.GitLab.InProgressStatus = DefaultGitLabInProgressStatus
	}
	if cfg.Adapters.Sentry.BaseURL == "" {
		cfg.Adapters.Sentry.BaseURL = DefaultSentryBaseURL
	}
	if cfg.Adapters.Sentry.PollInterval == "" {
		cfg.Adapters.Sentry.PollInterval = defaultPollInterval
	}
	if cfg.UI.DefaultFilter == "" {
		cfg.UI.DefaultFilter = "all"
	}
	if cfg.UI.DefaultGroup == "" {
		cfg.UI.DefaultGroup = "state"
	}
	if cfg.UI.LogLevel == "" {
		cfg.UI.LogLevel = "info"
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
		if err := validateHTTPSURL(cfg.Adapters.Sentry.BaseURL); err != nil {
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

	validIssueActionScope := map[IssueActionScope]bool{
		IssueActionScopeAll:  true,
		IssueActionScopeMine: true,
		IssueActionScopeNone: true,
	}
	if !validIssueActionScope[cfg.Adapters.GitHub.IssueActionScope] {
		return fmt.Errorf("invalid adapters.github.issue_comment_scope: %q (must be all, mine, or none)", cfg.Adapters.GitHub.IssueActionScope)
	}
	if !validIssueActionScope[cfg.Adapters.GitLab.IssueActionScope] {
		return fmt.Errorf("invalid adapters.gitlab.issue_comment_scope: %q (must be all, mine, or none)", cfg.Adapters.GitLab.IssueActionScope)
	}

	validHarnesses := map[HarnessName]bool{
		HarnessOhMyPi:     true,
		HarnessClaudeCode: true,
		HarnessCodex:      true,
		HarnessOpenCode:   true,
		HarnessACP:        true,
	}
	if !validHarnesses[cfg.Harness.Default] {
		return fmt.Errorf("invalid harness.default: %q", cfg.Harness.Default)
	}

	if err := ValidateClaudeThinking(cfg.Adapters.ClaudeCode.Thinking); err != nil {
		return fmt.Errorf("invalid adapters.claude_code.thinking: %w", err)
	}
	if err := ValidateClaudeEffort(cfg.Adapters.ClaudeCode.Effort); err != nil {
		return fmt.Errorf("invalid adapters.claude_code.effort: %w", err)
	}
	if err := ValidateCodexReasoningEffort(cfg.Adapters.Codex.ReasoningEffort); err != nil {
		return fmt.Errorf("invalid adapters.codex.reasoning_effort: %w", err)
	}
	if err := ValidateThinkingLevel(cfg.Adapters.OhMyPi.ThinkingLevel); err != nil {
		return fmt.Errorf("invalid adapters.ohmypi.thinking_level: %w", err)
	}

	validUIFilters := map[string]bool{"all": true, "active": true, "attention": true, "completed": true}
	if !validUIFilters[cfg.UI.DefaultFilter] {
		return fmt.Errorf("invalid ui.default_filter: %q (must be all, active, attention, or completed)", cfg.UI.DefaultFilter)
	}
	validUIGroups := map[string]bool{"none": true, "state": true, "source": true, "created": true, "activity": true}
	if !validUIGroups[cfg.UI.DefaultGroup] {
		return fmt.Errorf("invalid ui.default_group: %q (must be none, state, source, created, or activity)", cfg.UI.DefaultGroup)
	}
	if cfg.Daemon.Runtime.Bind.Kind != "unix" {
		return fmt.Errorf("invalid daemon.runtime.bind.kind: %q (only unix is supported)", cfg.Daemon.Runtime.Bind.Kind)
	}
	if cfg.Daemon.Runtime.ReflectionEnabled {
		return fmt.Errorf("invalid daemon.runtime.reflection_enabled: true (gRPC reflection is not supported)")
	}
	if err := validateDaemonRegistryAddresses(cfg); err != nil {
		return err
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

// validateDaemonAddress ensures the daemon endpoint cannot silently transport
// bearer tokens over plaintext to a non-local peer. The current gRPC client
// uses insecure credentials, so every transport that leaves the host
// (non-loopback HTTP and any non-loopback scheme the client does not yet
// speak over TLS) is rejected. Only unix-domain sockets and loopback HTTP
// (traffic that never leaves the kernel) are accepted. HTTPS for remote
// hosts is intentionally rejected until the client is wired with TLS
// credentials; relying on the URL scheme while still using
// insecure.NewCredentials() on the client would silently downgrade to
// plaintext.
func validateDaemonAddress(raw string) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return errors.New("daemon address is required")
	}
	// Unix-domain sockets are always acceptable: traffic is confined to the
	// kernel and cannot be intercepted on the wire.
	if strings.HasPrefix(trimmed, "unix:") || strings.HasPrefix(trimmed, "unix://") {
		return nil
	}
	parsed, err := url.ParseRequestURI(trimmed)
	if err != nil {
		return fmt.Errorf("invalid daemon address: %w", err)
	}
	switch parsed.Scheme {
	case "http", "https":
		switch parsed.Hostname() {
		case "localhost", "127.0.0.1", "::1":
			return nil
		}
		return errors.New("remote daemons require TLS-wired gRPC credentials; only unix sockets and loopback http/https are accepted until then")
	}
	return errors.New("daemon address must use unix socket or loopback http/https")
}

// validateDaemonRegistryAddresses walks the daemon registry and rejects
// any entry whose address would expose its bearer token in transit or whose
// token_ref embeds a plaintext secret.
func validateDaemonRegistryAddresses(cfg *Config) error {
	active := strings.TrimSpace(cfg.TUI.ActiveDaemon)
	if active != "" {
		if _, ok := cfg.TUI.Daemons[active]; !ok {
			return fmt.Errorf("tui.active_daemon %q is not configured in tui.daemons", active)
		}
	}
	for name, entry := range cfg.TUI.Daemons {
		if strings.TrimSpace(name) == "" {
			return errors.New("tui.daemons contains an empty name")
		}
		if err := validateDaemonTokenRef(entry.TokenRef); err != nil {
			return fmt.Errorf("tui.daemons.%s.token_ref: %w", name, err)
		}
		if entry.Kind != "local" && strings.TrimSpace(entry.Address) == "" {
			return fmt.Errorf("tui.daemons.%s.address is required", name)
		}
		if strings.TrimSpace(entry.Address) == "" {
			continue
		}
		if err := validateDaemonAddress(entry.Address); err != nil {
			return fmt.Errorf("tui.daemons.%s.address: %w", name, err)
		}
	}
	return nil
}

func validateHTTPSURL(raw string) error {
	parsed, err := url.ParseRequestURI(raw)
	if err != nil {
		return err
	}
	if !parsed.IsAbs() || parsed.Host == "" {
		return errors.New("must be an absolute URL")
	}
	if parsed.Scheme == "https" {
		return nil
	}
	// Allow plain HTTP only for loopback addresses (local dev and test servers).
	// Loopback traffic never leaves the machine, so plaintext is acceptable.
	if parsed.Scheme == "http" {
		switch parsed.Hostname() {
		case "localhost", "127.0.0.1", "::1":
			return nil
		}
	}
	return errors.New("must use https (bearer tokens must not be sent over plaintext)")
}

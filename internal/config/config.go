// Package config handles loading and validating the substrate.toml configuration.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
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

// Config is the top-level configuration loaded from substrate.toml.
type Config struct {
	Commit   CommitConfig          `toml:"commit"`
	Plan     PlanConfig            `toml:"plan"`
	Review   ReviewConfig          `toml:"review"`
	Adapters AdaptersConfig        `toml:"adapters"`
	Foreman  ForemanConfig         `toml:"foreman"`
	Repos    map[string]RepoConfig `toml:"repos"`
}

// CommitConfig controls agent commit behavior.
type CommitConfig struct {
	Strategy        CommitStrategy      `toml:"strategy"`
	MessageFormat   CommitMessageFormat `toml:"message_format"`
	MessageTemplate string              `toml:"message_template"`
}

// PlanConfig controls the planning pipeline.
type PlanConfig struct {
	MaxParseRetries *int `toml:"max_parse_retries"`
}

// ReviewConfig controls the review pipeline.
type ReviewConfig struct {
	PassThreshold PassThreshold `toml:"pass_threshold"`
	MaxCycles     *int          `toml:"max_cycles"`
}

// AdaptersConfig contains per-adapter configuration.
type AdaptersConfig struct {
	OhMyPi OhMyPiConfig `toml:"ohmypi"`
	Linear LinearConfig `toml:"linear"`
	Glab   GlabConfig   `toml:"glab"`
}

// LinearConfig configures the Linear GraphQL adapter.
type LinearConfig struct {
	APIKey         string            `toml:"api_key"`
	TeamID         string            `toml:"team_id"`
	AssigneeFilter string            `toml:"assignee_filter"` // "me" or explicit user ID
	PollInterval   string            `toml:"poll_interval"`   // e.g. "30s"; default "30s"
	StateMappings  map[string]string `toml:"state_mappings"`  // TrackerState -> Linear workflow state UUID
}

// GlabConfig configures the glab CLI adapter.
// All fields are optional; the adapter is always registered regardless.
type GlabConfig struct {
	// Reviewers is a list of GitLab usernames added as reviewers to created MRs.
	Reviewers []string `toml:"reviewers"`
	// Labels is a list of GitLab label names added to created MRs.
	Labels []string `toml:"labels"`
}

// OhMyPiConfig configures the oh-my-pi agent harness.
type OhMyPiConfig struct {
	BunPath       string `toml:"bun_path"`
	BridgePath    string `toml:"bridge_path"`
	ThinkingLevel string `toml:"thinking_level"`
}

// ForemanConfig controls the foreman question-answering system.
type ForemanConfig struct {
	Enabled         bool   `toml:"enabled"`
	QuestionTimeout string `toml:"question_timeout"`
}

// RepoConfig contains per-repo overrides.
type RepoConfig struct {
	// DocPaths are documentation paths to include in planning context.
	DocPaths []string `toml:"doc_paths"`
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
	return filepath.Join(dir, "config.toml"), nil
}

// SessionsDir returns the path to the sessions directory.
func SessionsDir() (string, error) {
	dir, err := GlobalDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "sessions"), nil
}

// Load reads and validates a substrate.toml configuration file.
// Missing fields are filled with defaults before validation.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	cfg := &Config{}
	if err := toml.Unmarshal(data, cfg); err != nil {
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
	// Foreman defaults
	if cfg.Foreman.QuestionTimeout == "" {
		cfg.Foreman.QuestionTimeout = "0"
	}
	// Linear defaults
	if cfg.Adapters.Linear.PollInterval == "" {
		cfg.Adapters.Linear.PollInterval = "30s"
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

	return nil
}

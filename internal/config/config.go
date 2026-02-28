// Package config handles loading and validating the substrate.toml configuration.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
)

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
	Commit   CommitConfig            `toml:"commit"`
	Plan     PlanConfig              `toml:"plan"`
	Review   ReviewConfig            `toml:"review"`
	Adapters AdaptersConfig          `toml:"adapters"`
	Foreman  ForemanConfig           `toml:"foreman"`
	Repos    map[string]RepoConfig   `toml:"repos"`
}

// CommitConfig controls agent commit behavior.
type CommitConfig struct {
	Strategy        CommitStrategy      `toml:"strategy"`
	MessageFormat   CommitMessageFormat `toml:"message_format"`
	MessageTemplate string              `toml:"message_template"`
}

// PlanConfig controls the planning pipeline.
type PlanConfig struct {
	MaxParseRetries int `toml:"max_parse_retries"`
}

// ReviewConfig controls the review pipeline.
type ReviewConfig struct {
	PassThreshold PassThreshold `toml:"pass_threshold"`
	MaxCycles     int           `toml:"max_cycles"`
}

// AdaptersConfig contains per-adapter configuration.
type AdaptersConfig struct {
	OhMyPi OhMyPiConfig `toml:"ohmypi"`
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
type RepoConfig struct{}

// GlobalDBPath returns the path to the global SQLite database.
func (c *Config) GlobalDBPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".substrate", "state.db")
}

// GlobalDir returns the path to the global Substrate directory.
func (c *Config) GlobalDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".substrate")
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
	if cfg.Plan.MaxParseRetries == 0 {
		cfg.Plan.MaxParseRetries = 2
	}
	if cfg.Review.PassThreshold == "" {
		cfg.Review.PassThreshold = PassThresholdMinorOK
	}
	if cfg.Review.MaxCycles == 0 {
		cfg.Review.MaxCycles = 3
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

	if cfg.Plan.MaxParseRetries < 0 {
		return fmt.Errorf("plan.max_parse_retries must be non-negative, got %d", cfg.Plan.MaxParseRetries)
	}

	switch cfg.Review.PassThreshold {
	case PassThresholdNitOnly, PassThresholdMinorOK, PassThresholdNoCritiques:
	default:
		return fmt.Errorf("invalid review.pass_threshold: %q (must be nit_only, minor_ok, or no_critiques)", cfg.Review.PassThreshold)
	}

	if cfg.Review.MaxCycles < 1 {
		return fmt.Errorf("review.max_cycles must be at least 1, got %d", cfg.Review.MaxCycles)
	}

	return nil
}

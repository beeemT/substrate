package views

import (
	"strings"
	"testing"

	"github.com/beeemT/substrate/internal/config"
)

func TestSettingsSerialize_RoundTripsCriticalFields(t *testing.T) {
	t.Parallel()
	svc := &SettingsService{}
	cfg := &config.Config{}
	cfg.Commit.Strategy = config.CommitStrategyGranular
	cfg.Commit.MessageFormat = config.CommitMessageConventional
	cfg.Harness.Default = config.HarnessCodex
	cfg.Harness.Phase.Planning = config.HarnessClaudeCode
	cfg.Harness.Phase.Implementation = config.HarnessCodex
	cfg.Harness.Phase.Review = config.HarnessClaudeCode
	cfg.Harness.Phase.Foreman = config.HarnessOhMyPi
	cfg.Adapters.Linear.APIKey = "lin-secret"
	cfg.Adapters.Linear.TeamID = "team-1"
	cfg.Adapters.GitHub.Token = "gh-secret"
	cfg.Adapters.GitLab.Token = "gl-secret"

	raw, rebuilt, err := svc.Serialize(buildSettingsSections(cfg))
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	for _, want := range []string{"api_key_ref = 'keychain:linear.api_key'", "token_ref = 'keychain:github.token'"} {
		if !strings.Contains(raw, want) {
			t.Fatalf("serialized config missing %q\n%s", want, raw)
		}
	}
	if rebuilt.Adapters.Linear.APIKeyRef != "keychain:linear.api_key" || rebuilt.Adapters.GitHub.TokenRef != "keychain:github.token" {
		t.Fatalf("rebuilt config mismatch: %+v", rebuilt.Adapters)
	}
}

func TestBuildProviderStatuses_UsesGithubFallback(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{}
	statuses := buildProviderStatuses(cfg)
	got := statuses["github"]
	if got.AuthSource == "" {
		t.Fatal("expected github auth source to be populated")
	}
}

func TestProviderForSection(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"provider.linear": "linear",
		"provider.gitlab": "gitlab",
		"provider.github": "github",
		"commit":          "",
	}
	for id, want := range cases {
		sec := &SettingsSection{ID: id}
		if got := providerForSection(sec); got != want {
			t.Fatalf("providerForSection(%q) = %q, want %q", id, got, want)
		}
	}
}

package views

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/beeemT/substrate/internal/config"
)

func TestSettingsSerialize_RoundTripsCriticalFields(t *testing.T) {
	t.Setenv("SUBSTRATE_HOME", t.TempDir())

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

func TestSettingsSerialize_RejectsInvalidScalarInput(t *testing.T) {
	t.Setenv("SUBSTRATE_HOME", t.TempDir())

	svc := &SettingsService{}
	cases := []struct {
		name    string
		section string
		key     string
		value   string
		want    string
	}{
		{name: "int", section: "adapters.claude_code", key: "max_turns", value: "abc", want: `adapters.claude_code.max_turns: invalid integer "abc"`},
		{name: "float", section: "adapters.claude_code", key: "max_budget_usd", value: "12.3.4", want: `adapters.claude_code.max_budget_usd: invalid number "12.3.4"`},
		{name: "bool", section: "adapters.codex", key: "full_auto", value: "sometimes", want: `adapters.codex.full_auto: invalid boolean "sometimes"`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sections := buildSettingsSections(newSettingsApplyHarnessConfig())
			setSettingsFieldValue(t, sections, tc.section, tc.key, tc.value)

			_, _, err := svc.Serialize(sections)
			if err == nil {
				t.Fatalf("Serialize(%s.%s) error = nil, want invalid scalar", tc.section, tc.key)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Serialize(%s.%s) error = %q, want substring %q", tc.section, tc.key, err, tc.want)
			}
		})
	}
}

func TestSettingsApply_PersistsConfigAndReportsHarnessWarnings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SUBSTRATE_HOME", home)

	svc := &SettingsService{}
	currentRaw := mustSerializeSettingsConfig(t, svc, newSettingsApplyHarnessConfig())
	if err := svc.SaveRaw(currentRaw); err != nil {
		t.Fatalf("SaveRaw(current): %v", err)
	}

	brokenCfg := newSettingsApplyHarnessConfig()
	brokenCfg.Adapters.ClaudeCode.BinaryPath = "/definitely/missing/claude"
	brokenRaw := mustSerializeSettingsConfig(t, svc, brokenCfg)

	result, err := svc.Apply(context.Background(), brokenRaw, Services{})
	if err != nil {
		t.Fatalf("Apply(): %v", err)
	}
	if result.Services.Services.Planning != nil {
		t.Fatal("Apply() planning service = non-nil, want nil when harness is unavailable")
	}
	if result.Services.Services.Implementation != nil {
		t.Fatal("Apply() implementation service = non-nil, want nil when harness is unavailable")
	}
	if result.Services.Services.Foreman != nil {
		t.Fatal("Apply() foreman service = non-nil, want nil when harness is unavailable")
	}
	if !strings.Contains(result.Services.SettingsData.HarnessWarning, "Some harnesses are unavailable") {
		t.Fatalf("Apply() harness warning = %q, want aggregated warning", result.Services.SettingsData.HarnessWarning)
	}

	var routing *SettingsSection
	for i := range result.Services.SettingsData.Sections {
		if result.Services.SettingsData.Sections[i].ID == "harness" {
			routing = &result.Services.SettingsData.Sections[i]
			break
		}
	}
	if routing == nil {
		t.Fatal("expected harness routing section in settings snapshot")
	}
	if routing.Status != "warning" {
		t.Fatalf("routing status = %q, want warning", routing.Status)
	}
	if !strings.Contains(routing.Error, "Claude Code CLI") {
		t.Fatalf("routing error = %q, want concise Claude Code detail", routing.Error)
	}

	cfgPath, err := config.ConfigPath()
	if err != nil {
		t.Fatalf("ConfigPath: %v", err)
	}
	persisted, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", cfgPath, err)
	}
	if string(persisted) != brokenRaw {
		t.Fatalf("persisted config mismatch\nwant:\n%s\n\ngot:\n%s", brokenRaw, string(persisted))
	}
}

func TestSettingsApply_ReturnsRebuiltServicesOnSuccess(t *testing.T) {
	t.Setenv("SUBSTRATE_HOME", t.TempDir())

	svc := &SettingsService{}
	cfg := newSettingsApplyHarnessConfig()
	cfg.Adapters.ClaudeCode.Model = "claude-3-7-sonnet"
	raw := mustSerializeSettingsConfig(t, svc, cfg)

	result, err := svc.Apply(context.Background(), raw, Services{})
	if err != nil {
		t.Fatalf("Apply(): %v", err)
	}
	if result.Message != "Settings applied" {
		t.Fatalf("Apply() message = %q, want %q", result.Message, "Settings applied")
	}
	if result.Services.Services.Cfg == nil {
		t.Fatal("Apply() returned nil config")
	}
	if got := result.Services.Services.Cfg.Adapters.ClaudeCode.Model; got != cfg.Adapters.ClaudeCode.Model {
		t.Fatalf("Apply() rebuilt model = %q, want %q", got, cfg.Adapters.ClaudeCode.Model)
	}
	if result.Services.Services.Foreman == nil {
		t.Fatal("Apply() returned nil foreman service")
	}
	if result.Services.SettingsData.RawTOML != raw {
		t.Fatalf("Apply() snapshot raw = %q, want %q", result.Services.SettingsData.RawTOML, raw)
	}
	if result.Services.SettingsData.HarnessWarning != "" {
		t.Fatalf("Apply() harness warning = %q, want empty", result.Services.SettingsData.HarnessWarning)
	}
}

func TestBuildProviderStatuses_UsesGithubFallback(t *testing.T) {
	cfg := &config.Config{}
	statuses := buildProviderStatuses(cfg)
	got := statuses["github"]
	if got.AuthSource == "" {
		t.Fatal("expected github auth source to be populated")
	}
}

func TestProviderForSection(t *testing.T) {
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

func newSettingsApplyHarnessConfig() *config.Config {
	cfg := &config.Config{}
	cfg.Harness.Default = config.HarnessClaudeCode
	cfg.Harness.Phase.Planning = config.HarnessClaudeCode
	cfg.Harness.Phase.Implementation = config.HarnessClaudeCode
	cfg.Harness.Phase.Review = config.HarnessClaudeCode
	cfg.Harness.Phase.Foreman = config.HarnessClaudeCode
	cfg.Adapters.ClaudeCode.BinaryPath = "/bin/sh"
	return cfg
}

func mustSerializeSettingsConfig(t *testing.T, svc *SettingsService, cfg *config.Config) string {
	t.Helper()
	raw, _, err := svc.Serialize(buildSettingsSections(cfg))
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	return raw
}

func setSettingsFieldValue(t *testing.T, sections []SettingsSection, section, key, value string) {
	t.Helper()
	for i := range sections {
		for j := range sections[i].Fields {
			field := &sections[i].Fields[j]
			if field.Section == section && field.Key == key {
				field.Value = value
				return
			}
		}
	}
	t.Fatalf("field %s.%s not found", section, key)
}

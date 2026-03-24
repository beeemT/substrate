package views

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	cfg.Adapters.Sentry.Token = "sentry-secret"
	cfg.Adapters.Sentry.Organization = "acme"
	cfg.Adapters.Sentry.Projects = []string{"web", "api"}

	raw, rebuilt, err := svc.Serialize(buildSettingsSections(cfg))
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	for _, want := range []string{"api_key_ref: keychain:linear.api_key", "token_ref: keychain:github.token", "token_ref: keychain:sentry.token", "organization: acme", "- web", "- api"} {
		if !strings.Contains(raw, want) {
			t.Fatalf("serialized YAML missing %q\n%s", want, raw)
		}
	}
	if rebuilt.Adapters.Linear.APIKeyRef != "keychain:linear.api_key" || rebuilt.Adapters.GitHub.TokenRef != "keychain:github.token" || rebuilt.Adapters.Sentry.TokenRef != "keychain:sentry.token" {
		t.Fatalf("rebuilt config mismatch: %+v", rebuilt.Adapters)
	}
	if rebuilt.Adapters.Sentry.Organization != "acme" {
		t.Fatalf("rebuilt sentry organization = %q, want %q", rebuilt.Adapters.Sentry.Organization, "acme")
	}
	if got := rebuilt.Adapters.Sentry.Projects; len(got) != 2 || got[0] != "web" || got[1] != "api" {
		t.Fatalf("rebuilt sentry projects = %#v, want %#v", got, []string{"web", "api"})
	}
}

func TestSettingsSerialize_ClearsSentryTokenRefWhenSecretFieldBlank(t *testing.T) {
	t.Setenv("SUBSTRATE_HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	t.Setenv("SENTRY_AUTH_TOKEN", "")

	svc := &SettingsService{}
	cfg := &config.Config{}
	cfg.Adapters.Sentry.TokenRef = "keychain:sentry.token"
	cfg.Adapters.Sentry.Organization = "acme"

	sections := buildSettingsSections(cfg)
	setSettingsFieldValue(t, sections, "adapters.sentry", "token_ref", "")

	raw, rebuilt, err := svc.Serialize(sections)
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	if strings.Contains(raw, "token_ref: keychain:sentry.token") {
		t.Fatalf("serialized YAML retained cleared Sentry token ref\n%s", raw)
	}
	if rebuilt.Adapters.Sentry.Token != "" || rebuilt.Adapters.Sentry.TokenRef != "" {
		t.Fatalf("rebuilt sentry auth = (%q, %q), want both cleared", rebuilt.Adapters.Sentry.Token, rebuilt.Adapters.Sentry.TokenRef)
	}
	status := buildProviderStatuses(rebuilt)["sentry"]
	if status.Configured {
		t.Fatalf("sentry status = %+v, want Configured=false after clearing token", status)
	}
	if status.AuthSource != "unset" {
		t.Fatalf("sentry auth source = %q, want %q", status.AuthSource, "unset")
	}
}

func TestSettingsSerialize_PreservesSentryKeychainRefWithoutPendingSave(t *testing.T) {
	t.Setenv("SUBSTRATE_HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	t.Setenv("SENTRY_AUTH_TOKEN", "")

	svc := &SettingsService{}
	cfg := &config.Config{}
	cfg.Adapters.Sentry.TokenRef = "keychain:sentry.token"
	cfg.Adapters.Sentry.Organization = "acme"

	raw, rebuilt, err := svc.Serialize(buildSettingsSections(cfg))
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	if !strings.Contains(raw, "token_ref: keychain:sentry.token") {
		t.Fatalf("serialized YAML missing Sentry token ref\n%s", raw)
	}
	if rebuilt.Adapters.Sentry.Token != "" {
		t.Fatalf("rebuilt sentry token = %q, want empty when preserving keychain ref", rebuilt.Adapters.Sentry.Token)
	}
	if rebuilt.Adapters.Sentry.TokenRef != "keychain:sentry.token" {
		t.Fatalf("rebuilt sentry token ref = %q, want %q", rebuilt.Adapters.Sentry.TokenRef, "keychain:sentry.token")
	}
	status := buildProviderStatuses(rebuilt)["sentry"]
	if !status.Configured {
		t.Fatalf("sentry status = %+v, want Configured=true with keychain ref", status)
	}
	if status.AuthSource != "keychain" {
		t.Fatalf("sentry auth source = %q, want %q", status.AuthSource, "keychain")
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
	if result.Services.SettingsData.HarnessWarning != "Harnesses unavailable. Check Harness Routing." {
		t.Fatalf("Apply() harness warning = %q, want short aggregated warning", result.Services.SettingsData.HarnessWarning)
	}

	var routing *SettingsSection
	var claude *SettingsSection
	for i := range result.Services.SettingsData.Sections {
		switch result.Services.SettingsData.Sections[i].ID {
		case "harness":
			routing = &result.Services.SettingsData.Sections[i]
		case "harness.claude":
			claude = &result.Services.SettingsData.Sections[i]
		}
	}
	if routing == nil {
		t.Fatal("expected harness routing section in settings snapshot")
	}
	if routing.Status != "warning" {
		t.Fatalf("routing status = %q, want warning", routing.Status)
	}
	if routing.Error != "Planning, Implementation, Review, Foreman: Claude Code binary not found." {
		t.Fatalf("routing error = %q, want grouped Claude Code detail", routing.Error)
	}
	if strings.Contains(routing.Error, "Binary Path") {
		t.Fatalf("routing error = %q, want short warning without settings copy", routing.Error)
	}
	if claude == nil {
		t.Fatal("expected Claude Code harness section in settings snapshot")
	}
	if claude.Error != "Planning, Implementation, Review, Foreman: Claude Code binary not found." {
		t.Fatalf("claude section error = %q, want grouped Claude Code detail", claude.Error)
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
	if result.Services.SettingsData.RawYAML != raw {
		t.Fatalf("Apply() snapshot raw = %q, want %q", result.Services.SettingsData.RawYAML, raw)
	}
	if result.Services.SettingsData.HarnessWarning != "" {
		t.Fatalf("Apply() harness warning = %q, want empty", result.Services.SettingsData.HarnessWarning)
	}
}

func TestBuildSettingsSections_LeavesUnusedHarnessSectionsQuiet(t *testing.T) {
	cfg := &config.Config{}
	cfg.Harness.Default = config.HarnessOhMyPi
	cfg.Harness.Phase.Planning = config.HarnessOhMyPi
	cfg.Harness.Phase.Implementation = config.HarnessOhMyPi
	cfg.Harness.Phase.Review = config.HarnessOhMyPi
	cfg.Harness.Phase.Foreman = config.HarnessOhMyPi
	cfg.Adapters.OhMyPi.BridgePath = filepath.Join(t.TempDir(), "missing-bridge")

	sections := buildSettingsSections(cfg)
	var ohmypi, claude, codex *SettingsSection
	for i := range sections {
		switch sections[i].ID {
		case "harness.ohmypi":
			ohmypi = &sections[i]
		case "harness.claude":
			claude = &sections[i]
		case "harness.codex":
			codex = &sections[i]
		}
	}
	if ohmypi == nil || claude == nil || codex == nil {
		t.Fatal("expected harness sections in settings snapshot")
	}
	if ohmypi.Status != "warning" || ohmypi.Error != "Planning, Implementation, Review, Foreman: Oh My Pi bridge not found." {
		t.Fatalf("ohmypi section = %+v, want grouped concise warning", *ohmypi)
	}
	if strings.Contains(ohmypi.Error, "\n") {
		t.Fatalf("ohmypi section error = %q, want one grouped line", ohmypi.Error)
	}
	if claude.Status == "warning" || claude.Error != "" {
		t.Fatalf("claude section = %+v, want unused harness to stay quiet", *claude)
	}
	if codex.Status == "warning" || codex.Error != "" {
		t.Fatalf("codex section = %+v, want unused harness to stay quiet", *codex)
	}
}

func TestBuildSettingsSections_OmitsHarnessFallbackField(t *testing.T) {
	sections := buildSettingsSections(&config.Config{})
	for _, section := range sections {
		for _, field := range section.Fields {
			if field.Section == "harness" && field.Key == "fallback" {
				t.Fatalf("unexpected fallback field in settings sections: %+v", field)
			}
		}
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

func TestBuildSettingsSections_IncludesSentryProviderSection(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}
	cfg.Adapters.Sentry.Token = "sentry-secret"
	cfg.Adapters.Sentry.Organization = "acme"
	cfg.Adapters.Sentry.Projects = []string{"web", "api"}

	section := findSection(buildSettingsSections(cfg), "provider.sentry")
	if section.ID == "" {
		t.Fatal("expected provider.sentry section in settings snapshot")
	}
	if section.Title != "Provider · Sentry" {
		t.Fatalf("section title = %q, want %q", section.Title, "Provider · Sentry")
	}
	if len(section.Fields) != 4 {
		t.Fatalf("field count = %d, want 4", len(section.Fields))
	}
	if section.Fields[0].Key != "token_ref" || section.Fields[1].Key != "base_url" || section.Fields[2].Key != "organization" || section.Fields[3].Key != "projects" {
		t.Fatalf("unexpected field keys = %#v", []string{section.Fields[0].Key, section.Fields[1].Key, section.Fields[2].Key, section.Fields[3].Key})
	}
	if !strings.Contains(section.Fields[0].Description, "Sentry token stored in config or the OS keychain; runtime may also use SENTRY_AUTH_TOKEN or authenticated sentry CLI.") {
		t.Fatalf("token description = %q, want Sentry credential copy", section.Fields[0].Description)
	}
	if section.Fields[1].DefaultValue != config.DefaultSentryBaseURL {
		t.Fatalf("base_url default = %q, want %q", section.Fields[1].DefaultValue, config.DefaultSentryBaseURL)
	}
}

func TestBuildProviderStatuses_TracksSentryTokenState(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("SENTRY_AUTH_TOKEN", "")

	cfg := &config.Config{}
	statuses := buildProviderStatuses(cfg)
	if got := statuses["sentry"]; got.Configured {
		t.Fatalf("unconfigured sentry status = %+v, want Configured=false", got)
	}

	cfg.Adapters.Sentry.Token = "sentry-secret"
	got := buildProviderStatuses(cfg)["sentry"]
	if !got.Configured {
		t.Fatalf("configured sentry status = %+v, want Configured=true", got)
	}
	if got.AuthSource != "config token" {
		t.Fatalf("sentry auth source = %q, want %q", got.AuthSource, "config token")
	}
}

func TestSettingsService_TestProviderSentryReportsConstructorError(t *testing.T) {
	t.Parallel()

	svc := &SettingsService{}
	cfg := &config.Config{}
	cfg.Adapters.Sentry.Token = "sentry-secret"

	status, err := svc.TestProvider(context.Background(), "sentry", buildSettingsSections(cfg))
	if err == nil {
		t.Fatal("TestProvider(sentry) error = nil, want constructor error")
	}
	if !strings.Contains(err.Error(), "sentry organization is required") {
		t.Fatalf("TestProvider(sentry) error = %q, want organization requirement", err)
	}
	if status.Connected {
		t.Fatalf("status = %+v, want Connected=false", status)
	}
	if !status.Configured {
		t.Fatalf("status = %+v, want Configured=true when token is present", status)
	}
	if status.LastError != "sentry organization is required" {
		t.Fatalf("status.LastError = %q, want %q", status.LastError, "sentry organization is required")
	}
}

func TestSettingsService_TestProviderSentryMarksConnectedOnSuccess(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/0/organizations/acme/issues/" {
			t.Fatalf("path = %q, want org issues endpoint", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sentry-secret" {
			t.Fatalf("authorization = %q, want bearer token", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()

	svc := &SettingsService{}
	cfg := &config.Config{}
	cfg.Adapters.Sentry.Token = "sentry-secret"
	cfg.Adapters.Sentry.BaseURL = server.URL + "/api/0"
	cfg.Adapters.Sentry.Organization = "acme"

	status, err := svc.TestProvider(context.Background(), "sentry", buildSettingsSections(cfg))
	if err != nil {
		t.Fatalf("TestProvider(sentry): %v", err)
	}
	if !status.Configured || !status.Connected {
		t.Fatalf("status = %+v, want configured connected provider", status)
	}
	if status.LastError != "" {
		t.Fatalf("status.LastError = %q, want empty", status.LastError)
	}
}

func TestSettingsService_TestProviderSentrySurfacesAPIError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("upstream unavailable"))
	}))
	defer server.Close()

	svc := &SettingsService{}
	cfg := &config.Config{}
	cfg.Adapters.Sentry.Token = "sentry-secret"
	cfg.Adapters.Sentry.BaseURL = server.URL + "/api/0"
	cfg.Adapters.Sentry.Organization = "acme"

	status, err := svc.TestProvider(context.Background(), "sentry", buildSettingsSections(cfg))
	if err == nil {
		t.Fatal("TestProvider(sentry) error = nil, want API failure")
	}
	if status.Connected {
		t.Fatalf("status = %+v, want Connected=false", status)
	}
	if !strings.Contains(status.LastError, "upstream unavailable") {
		t.Fatalf("status.LastError = %q, want API error body", status.LastError)
	}
}

func TestProviderForSection(t *testing.T) {
	cases := map[string]string{
		"provider.linear": "linear",
		"provider.gitlab": "gitlab",
		"provider.sentry": "sentry",
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

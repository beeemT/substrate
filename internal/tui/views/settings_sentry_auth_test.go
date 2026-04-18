package views

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/app"
	"github.com/beeemT/substrate/internal/config"
)

type stubHarnessRunner struct {
	run func(context.Context, adapter.HarnessActionRequest) (adapter.HarnessActionResult, error)
}

func (s stubHarnessRunner) Name() string { return "stub" }

func (s stubHarnessRunner) SupportsCompact() bool { return false }
func (s stubHarnessRunner) StartSession(context.Context, adapter.SessionOpts) (adapter.AgentSession, error) {
	return nil, nil
}

func (s stubHarnessRunner) RunAction(ctx context.Context, req adapter.HarnessActionRequest) (adapter.HarnessActionResult, error) {
	return s.run(ctx, req)
}

func writeSettingsExecutable(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}

	return path
}

func clearSentryViewEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"SENTRY_AUTH_TOKEN", "SENTRY_URL", "SENTRY_ORG", "SENTRY_PROJECT"} {
		t.Setenv(key, "")
	}
}

func TestBuildProviderStatuses_UsesSentryCLIAuthSource(t *testing.T) {
	clearSentryViewEnv(t)
	binDir := t.TempDir()
	writeSettingsExecutable(t, binDir, "sentry", "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then\n  echo \"sentry-cli 0.27.0\"\n  exit 0\nfi\nif [ \"$1\" = \"auth\" ] && [ \"$2\" = \"status\" ]; then\n  exit 0\nfi\nexit 1\n")
	t.Setenv("PATH", binDir)

	status := buildProviderStatuses(&config.Config{})["sentry"]
	if !status.Configured {
		t.Fatalf("status = %+v, want Configured=true with authenticated sentry CLI", status)
	}
	if status.AuthSource != "sentry cli" {
		t.Fatalf("status.AuthSource = %q, want %q", status.AuthSource, "sentry cli")
	}
}

func TestSettingsService_TestProviderSentryUsesCLITransport(t *testing.T) {
	clearSentryViewEnv(t)
	binDir := t.TempDir()
	writeSettingsExecutable(t, binDir, "sentry", "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then\n  echo \"sentry-cli 0.27.0\"\n  exit 0\nfi\nif [ \"$1\" = \"auth\" ] && [ \"$2\" = \"status\" ]; then\n  exit 0\nfi\nif [ \"$1\" = \"api\" ]; then\n  printf 'HTTP/2 200\nContent-Type: application/json\n\n[]\n'\n  exit 0\nfi\nexit 1\n")
	t.Setenv("PATH", binDir)

	svc := &SettingsService{}
	cfg := &config.Config{}
	cfg.Adapters.Sentry.Organization = "acme"

	status, err := svc.TestProvider(context.Background(), "sentry", buildSettingsSections(cfg))
	if err != nil {
		t.Fatalf("TestProvider(sentry): %v", err)
	}
	if !status.Configured || !status.Connected {
		t.Fatalf("status = %+v, want configured connected provider", status)
	}
	if status.AuthSource != "sentry cli" {
		t.Fatalf("status.AuthSource = %q, want %q", status.AuthSource, "sentry cli")
	}
}

func TestSettingsService_LoginProviderSentryRefreshesCLIStatus(t *testing.T) {
	clearSentryViewEnv(t)
	binDir := t.TempDir()
	writeSettingsExecutable(t, binDir, "sentry", "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then\n  echo \"sentry-cli 0.27.0\"\n  exit 0\nfi\nif [ \"$1\" = \"auth\" ] && [ \"$2\" = \"status\" ]; then\n  exit 0\nfi\nexit 1\n")
	t.Setenv("PATH", binDir)

	cfg := &config.Config{}
	cfg.Adapters.Sentry.BaseURL = "https://sentry.example.com/self-hosted/api/0"
	cfg.Adapters.Sentry.Organization = "acme"
	sections := buildSettingsSections(cfg)

	var seenReq adapter.HarnessActionRequest
	svcs := Services{
		Harnesses: app.AgentHarnesses{
			Foreman: stubHarnessRunner{run: func(_ context.Context, req adapter.HarnessActionRequest) (adapter.HarnessActionResult, error) {
				seenReq = req

				return adapter.HarnessActionResult{Success: true, Message: "sentry login succeeded"}, nil
			}},
		},
	}

	result, err := (&SettingsService{}).LoginProvider(context.Background(), "sentry", "sentry", sections, svcs)
	if err != nil {
		t.Fatalf("LoginProvider(sentry): %v", err)
	}
	if seenReq.Action != "login_provider" || seenReq.Provider != "sentry" {
		t.Fatalf("request = %#v, want sentry login_provider action", seenReq)
	}
	if seenReq.Inputs["base_url"] != "https://sentry.example.com/self-hosted" {
		t.Fatalf("base_url input = %q, want self-hosted root URL", seenReq.Inputs["base_url"])
	}
	if result.Dirty {
		t.Fatal("Dirty = true, want false for CLI-backed Sentry login")
	}
	if result.Message != "sentry login succeeded" {
		t.Fatalf("Message = %q, want %q", result.Message, "sentry login succeeded")
	}
	provider := result.Snapshot.Providers["sentry"]
	if provider.AuthSource != "sentry cli" || !provider.Configured {
		t.Fatalf("provider = %+v, want sentry cli configured snapshot", provider)
	}
	section := findSection(result.Snapshot.Sections, "provider.sentry")
	if len(section.Fields) == 0 || section.Fields[0].Status != "sentry cli" {
		t.Fatalf("section = %+v, want token field status refreshed to sentry cli", section)
	}
}

func TestSettingsService_TestProviderSentryDirectHTTPStillUsesToken(t *testing.T) {
	clearSentryViewEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	if status.AuthSource != "config token" {
		t.Fatalf("status.AuthSource = %q, want %q", status.AuthSource, "config token")
	}
}

package views

import (
	"testing"

	"github.com/beeemT/substrate/internal/config"
)

func clearSentryPageTestEnv(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
	for _, key := range []string{"SENTRY_AUTH_TOKEN", "SENTRY_URL", "SENTRY_ORG", "SENTRY_PROJECT"} {
		t.Setenv(key, "")
	}
}

func TestSettingsPage_LoginRefreshPreservesTestedProviderState(t *testing.T) {
	clearSentryPageTestEnv(t)

	page := newTestSettingsPage(&config.Config{})
	page.providerStatus["sentry"] = ProviderStatus{Title: "Sentry", AuthSource: "unset", Configured: false, Connected: true}

	cfg := &config.Config{}
	cfg.Adapters.Sentry.Token = "token"
	cfg.Adapters.Sentry.Organization = "acme"
	snapshot := SettingsSnapshot{Sections: buildSettingsSections(cfg), Providers: buildProviderStatuses(cfg)}

	updatedModel, _ := page.Update(SettingsLoginCompletedMsg{Snapshot: snapshot, Message: "sentry login succeeded", Dirty: false}, Services{})
	updated := updatedModel
	status := updated.providerStatus["sentry"]
	if !status.Connected {
		t.Fatalf("status = %+v, want Connected=true preserved from prior test result", status)
	}
	if status.AuthSource != "config token" {
		t.Fatalf("status.AuthSource = %q, want %q from refreshed snapshot", status.AuthSource, "config token")
	}
}

func TestSettingsPage_LoginRefreshPreservesSidebarExpansionState(t *testing.T) {
	clearSentryPageTestEnv(t)

	page := newTestSettingsPage(&config.Config{})
	page.expandedSections["group.providers"] = false

	cfg := &config.Config{}
	cfg.Adapters.Sentry.Token = "token"
	cfg.Adapters.Sentry.Organization = "acme"
	snapshot := SettingsSnapshot{Sections: buildSettingsSections(cfg), Providers: buildProviderStatuses(cfg)}

	updatedModel, _ := page.Update(SettingsLoginCompletedMsg{Snapshot: snapshot, Message: "sentry login succeeded", Dirty: false}, Services{})
	updated := updatedModel
	if updated.expandedSections["group.providers"] {
		t.Fatalf("expandedSections[%q] = true, want collapsed state preserved", "group.providers")
	}
}

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

	updatedModel, _ := page.Update(SettingsLoginCompletedMsg{Message: "sentry login succeeded", Dirty: false}, Services{})
	updated := updatedModel
	status := updated.providerStatus["sentry"]
	if !status.Connected {
		t.Fatalf("status = %+v, want Connected=true preserved from prior test result", status)
	}
}

func TestSettingsPage_LoginRefreshPreservesSidebarExpansionState(t *testing.T) {
	clearSentryPageTestEnv(t)

	page := newTestSettingsPage(&config.Config{})
	page.expandedSections["group.providers"] = false

	updatedModel, _ := page.Update(SettingsLoginCompletedMsg{Message: "sentry login succeeded", Dirty: false}, Services{})
	updated := updatedModel
	if updated.expandedSections["group.providers"] {
		t.Fatalf("expandedSections[%q] = true, want collapsed state preserved", "group.providers")
	}
}

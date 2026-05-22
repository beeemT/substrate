package views

import (
	"testing"

	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/tui/styles"
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

	// Build the service snapshot with the provider already tested (Connected=true).
	snapshot := SettingsSnapshot{
		Sections:  buildSettingsSections(&config.Config{}),
		Providers: buildProviderStatuses(&config.Config{}),
	}
	snapshot.Providers["sentry"] = ProviderStatus{Title: "Sentry", AuthSource: "unset", Configured: false, Connected: true}

	// Inject the pre-tested provider status into the service snapshot so RefreshFromService
	// copies it to the page.
	fake := &testSettingsService{snapshot: snapshot}
	page := NewSettingsPage(fake, styles.NewStyles(styles.DefaultTheme))
	page.SetSize(120, 40)

	updatedModel, _ := page.Update(SettingsLoginCompletedMsg{Message: "sentry login succeeded", Dirty: false}, Services{})
	updated := updatedModel
	status := updated.providerStatus["sentry"]
	if !status.Connected {
		t.Fatalf("status = %+v, want Connected=true preserved from service snapshot", status)
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

package views

import (
	"context"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/repository"
)

func TestApp_EscClosesSettingsOverlay(t *testing.T) {
	t.Parallel()

	app := newTestApp(Services{
		WorkspaceID:   "ws-1",
		WorkspaceName: "workspace",
		Settings:      newTestSettingsService(),
	})
	app.activeOverlay = overlaySettings
	app.settingsPage.Open()

	model, cmd := app.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("expected Esc to emit a close-overlay command while settings is open")
	}

	msg := cmd()
	if _, ok := msg.(CloseOverlayMsg); !ok {
		t.Fatalf("msg = %T, want CloseOverlayMsg", msg)
	}

	updated, ok := model.(*App)
	if !ok {
		t.Fatalf("model = %T, want *App", model)
	}

	model, _ = updated.Update(msg)
	closed, ok := model.(*App)
	if !ok {
		t.Fatalf("closed model = %T, want *App", model)
	}
	if closed.activeOverlay != overlayNone {
		t.Fatalf("activeOverlay = %v, want %v", closed.activeOverlay, overlayNone)
	}
	if closed.settingsPage.Active() {
		t.Fatal("expected settings page to be inactive after closing overlay")
	}
}

func TestApp_EscWithDirtyOpensConfirmModal(t *testing.T) {
	t.Parallel()

	app := newTestApp(Services{
		WorkspaceID:   "ws-1",
		WorkspaceName: "workspace",
		Settings:      newTestSettingsService(),
	})
	app.activeOverlay = overlaySettings
	app.settingsPage.Open()
	app.settingsPage.SetDirty(true)

	model, cmd := app.Update(tea.KeyMsg{Type: tea.KeyEsc})

	// With dirty state, Esc should NOT emit CloseOverlayMsg
	// Instead, it should open the confirm modal
	if cmd != nil {
		msg := cmd()
		if _, ok := msg.(CloseOverlayMsg); ok {
			t.Fatal("expected Esc with dirty state NOT to emit CloseOverlayMsg; should open confirm modal instead")
		}
	}

	updated, ok := model.(*App)
	if !ok {
		t.Fatalf("model = %T, want *App", model)
	}

	// The confirm modal should be open
	if !updated.settingsPage.confirmModalOpen {
		t.Fatal("expected confirmModalOpen to be true after Esc with dirty state")
	}
}

func TestApp_SOpensSettingsOverlay(t *testing.T) {
	t.Parallel()

	app := newTestApp(Services{
		WorkspaceID:   "ws-1",
		WorkspaceName: "workspace",
		Settings:      newTestSettingsService(),
	})

	model, cmd := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if cmd != nil {
		t.Fatalf("cmd = %v, want nil when opening settings", cmd)
	}

	updated, ok := model.(*App)
	if !ok {
		t.Fatalf("model = %T, want *App", model)
	}
	if updated.activeOverlay != overlaySettings {
		t.Fatalf("activeOverlay = %v, want %v", updated.activeOverlay, overlaySettings)
	}
	if !updated.settingsPage.Active() {
		t.Fatal("expected settings page to be active after pressing s")
	}
}

// fakeSettingsService is a test fake that returns a controlled snapshot.
type fakeSettingsService struct {
	snapshot SettingsSnapshot
}

func (f *fakeSettingsService) Snapshot() SettingsSnapshot {
	return f.snapshot
}

func (f *fakeSettingsService) RefreshConfigOnly(_ context.Context, _ *config.Config) error {
	return nil
}

func (f *fakeSettingsService) RefreshWithDiagnostics(_ context.Context, _ *config.Config) error {
	return nil
}

func (f *fakeSettingsService) Save(_ context.Context, _ []SettingsSection, _ Services) (SettingsApplyResult, error) {
	return SettingsApplyResult{}, nil
}

func (f *fakeSettingsService) TestProvider(_ context.Context, _ string, _ []SettingsSection) (ProviderStatus, error) {
	return ProviderStatus{}, nil
}

func (f *fakeSettingsService) LoginProvider(_ context.Context, _, _ string, _ []SettingsSection, _ Services) (SettingsLoginResult, error) {
	return SettingsLoginResult{}, nil
}

func (f *fakeSettingsService) RefreshLoginSnapshot(_ context.Context, _ []SettingsSection) error {
	return nil
}

func (f *fakeSettingsService) RefreshLoginSnapshotFromConfig(_ context.Context, _ *config.Config) error {
	return nil
}

func (f *fakeSettingsService) SetDiagnosticsState(_ SettingsDiagnosticsState) {}

func TestAppHarnessWarningToast_ReadsSettingsService(t *testing.T) {
	t.Parallel()

	fake := &fakeSettingsService{
		snapshot: SettingsSnapshot{
			DiagnosticsState: SettingsDiagnosticsReady,
			HarnessWarning:   "Planning unavailable. Check Harness Routing.",
			Sections:         buildSettingsSections(&config.Config{}),
			Providers:        buildProviderStatuses(&config.Config{}),
		},
	}
	app := newTestApp(Services{
		WorkspaceID:   "ws-1",
		WorkspaceName: "workspace",
		Settings:      fake,
	})

	toast, ok := app.harnessWarningToast()
	if !ok {
		t.Fatal("harnessWarningToast returned false, want true with ready diagnostics and warning")
	}
	if toast.Message != "Planning unavailable. Check Harness Routing." {
		t.Fatalf("toast.Message = %q, want %q", toast.Message, "Planning unavailable. Check Harness Routing.")
	}

	// Update the service snapshot after app construction — toast must reflect the new warning.
	fake.snapshot.HarnessWarning = "Custom warning"
	toast, ok = app.harnessWarningToast()
	if !ok {
		t.Fatal("harnessWarningToast returned false after snapshot update, want true")
	}
	if toast.Message != "Custom warning" {
		t.Fatalf("toast.Message = %q after snapshot update, want %q", toast.Message, "Custom warning")
	}
}

func TestAppHarnessWarningToast_HiddenWhileDiagnosticsPending(t *testing.T) {
	t.Parallel()

	fake := &fakeSettingsService{
		snapshot: SettingsSnapshot{
			DiagnosticsState: SettingsDiagnosticsPending,
			HarnessWarning:   "Some warning", // non-empty but should not show
			Sections:         buildSettingsSections(&config.Config{}),
			Providers:        buildProviderStatuses(&config.Config{}),
		},
	}
	app := newTestApp(Services{
		WorkspaceID:   "ws-1",
		WorkspaceName: "workspace",
		Settings:      fake,
	})

	toast, ok := app.harnessWarningToast()
	if ok {
		t.Fatalf("harnessWarningToast returned ok=true while diagnostics pending, want false; got message=%q", toast.Message)
	}
}

func TestStartupIntegrationsCmd_RefreshesSettingsDiagnostics(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SUBSTRATE_HOME", home)

	cfg := &config.Config{}
	cfg.Harness.Default = config.HarnessCodex
	cfg.Commit.Strategy = config.CommitStrategyGranular
	cfg.Commit.MessageFormat = config.CommitMessageConventional

	serviceMgr := NewServiceManager(repository.NoopTransacter{}, nil)
	svc := NewSettingsService(repository.NoopTransacter{}, config.NoopKeychainStore{}, serviceMgr)
	raw := mustSerializeSettingsConfig(t, svc, cfg)
	if err := svc.SaveRaw(raw); err != nil {
		t.Fatalf("SaveRaw: %v", err)
	}
	if err := svc.RefreshConfigOnly(context.Background(), cfg); err != nil {
		t.Fatalf("RefreshConfigOnly: %v", err)
	}

	// Verify initial snapshot is pending.
	if snap := svc.Snapshot(); snap.DiagnosticsState != SettingsDiagnosticsPending {
		t.Fatalf("initial DiagnosticsState = %q, want pending", snap.DiagnosticsState)
	}

	// Initialize the service manager so StartupIntegrationsCmd has services to work with.
	if err := serviceMgr.InitWithServices(context.Background(), cfg, Services{Settings: svc}); err != nil {
		t.Fatalf("InitWithServices: %v", err)
	}

	// Run the integration command which should refresh diagnostics.
	cmd := StartupIntegrationsCmd(serviceMgr, RuntimeContext{
		Cfg: cfg,
	})
	result := cmd()

	readyMsg, ok := result.(StartupIntegrationsReadyMsg)
	if !ok {
		t.Fatalf("result = %T, want StartupIntegrationsReadyMsg", result)
	}
	if readyMsg.Err != nil {
		t.Fatalf("StartupIntegrationsReadyMsg.Err = %v, want nil", readyMsg.Err)
	}

	// Diagnostics must now be ready.
	if snap := svc.Snapshot(); snap.DiagnosticsState != SettingsDiagnosticsReady {
		t.Fatalf("after StartupIntegrationsCmd, DiagnosticsState = %q, want ready", snap.DiagnosticsState)
	}
}

func TestSettingsDiagnosticsCmd_DoesNotBlockUpdateLoop(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SUBSTRATE_HOME", home)

	cfg := &config.Config{}
	cfg.Harness.Default = config.HarnessCodex
	cfg.Commit.Strategy = config.CommitStrategyGranular
	cfg.Commit.MessageFormat = config.CommitMessageConventional

	serviceMgr := NewServiceManager(repository.NoopTransacter{}, nil)
	svc := NewSettingsService(repository.NoopTransacter{}, config.NoopKeychainStore{}, serviceMgr)
	raw := mustSerializeSettingsConfig(t, svc, cfg)
	if err := svc.SaveRaw(raw); err != nil {
		t.Fatalf("SaveRaw: %v", err)
	}
	if err := svc.RefreshConfigOnly(context.Background(), cfg); err != nil {
		t.Fatalf("RefreshConfigOnly: %v", err)
	}

	// SettingsDiagnosticsStartCmd must return promptly.
	startCmd := SettingsDiagnosticsStartCmd()
	startResult := startCmd()
	if _, ok := startResult.(SettingsDiagnosticsStartMsg); !ok {
		t.Fatalf("SettingsDiagnosticsStartCmd returned %T, want SettingsDiagnosticsStartMsg", startResult)
	}

	// SettingsDiagnosticsCmd must not block — it launches a goroutine and returns.
	ready := make(chan SettingsDiagnosticsReadyMsg, 1)
	diagCmd := SettingsDiagnosticsCmd(svc, cfg, func(msg tea.Msg) {
		if readyMsg, ok := msg.(SettingsDiagnosticsReadyMsg); ok {
			ready <- readyMsg
		}
	})
	startTime := time.Now()
	diagResult := diagCmd()
	elapsed := time.Since(startTime)
	if elapsed > time.Second {
		t.Fatalf("SettingsDiagnosticsCmd took %v (> 1s), want non-blocking execution", elapsed)
	}
	if diagResult != nil {
		t.Fatalf("diagCmd result = %T, want nil", diagResult)
	}

	var msg SettingsDiagnosticsReadyMsg
	select {
	case msg = <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for SettingsDiagnosticsReadyMsg")
	}
	if msg.Err != nil {
		t.Fatalf("SettingsDiagnosticsReadyMsg.Err = %v, want nil", msg.Err)
	}
}

func TestSettingsDiagnosticsReadyMsg_RefreshesSettingsPageFromService(t *testing.T) {
	t.Parallel()

	// Fake that starts with a pending snapshot.
	fake := &fakeSettingsService{
		snapshot: SettingsSnapshot{
			DiagnosticsState: SettingsDiagnosticsPending,
			Sections:         buildSettingsSections(&config.Config{}),
			Providers:        buildProviderStatuses(&config.Config{}),
		},
	}
	app := newTestApp(Services{
		WorkspaceID:   "ws-1",
		WorkspaceName: "workspace",
		Settings:      fake,
	})

	// Advance the settings page to have some state, then mark dirty.
	app.settingsPage.Open()
	app.settingsPage.SetDirty(true)

	// Simulate diagnostics completing by updating the fake service snapshot.
	fake.snapshot.DiagnosticsState = SettingsDiagnosticsReady
	fake.snapshot.HarnessWarning = "Planning unavailable."
	// Mutate a section to verify it is preserved when dirty.
	for i := range fake.snapshot.Sections {
		for j := range fake.snapshot.Sections[i].Fields {
			fake.snapshot.Sections[i].Fields[j].Dirty = true
			fake.snapshot.Sections[i].Fields[j].Value = "user-edited"
			break
		}
		break
	}

	// Send the ready message.
	model, _ := app.Update(SettingsDiagnosticsReadyMsg{})
	updated := model.(*App)

	// When dirty, RefreshFromService must preserve edits.
	updated.settingsPage.Open()
	found := false
	for _, sec := range updated.settingsPage.sections {
		for _, f := range sec.Fields {
			if f.Value == "user-edited" {
				found = true
				break
			}
		}
		if found {
			break
		}
	}
	if !found {
		t.Fatal("dirty edits were clobbered by RefreshFromService")
	}
}

package views

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/repository"
)

type stubWorkspaceInitAdapter struct{ name string }

func (a stubWorkspaceInitAdapter) Name() string { return a.name }
func (a stubWorkspaceInitAdapter) Capabilities() adapter.AdapterCapabilities {
	return adapter.AdapterCapabilities{}
}

func (a stubWorkspaceInitAdapter) ListSelectable(context.Context, adapter.ListOpts) (*adapter.ListResult, error) {
	return nil, adapter.ErrBrowseNotSupported
}

func (a stubWorkspaceInitAdapter) Resolve(context.Context, adapter.Selection) (domain.Session, error) {
	return domain.Session{}, errors.New("not implemented")
}

func (a stubWorkspaceInitAdapter) Watch(context.Context, adapter.WorkItemFilter) (<-chan adapter.WorkItemEvent, error) {
	return nil, adapter.ErrWatchNotSupported
}

func (a stubWorkspaceInitAdapter) Fetch(context.Context, string) (domain.Session, error) {
	return domain.Session{}, errors.New("not implemented")
}

func (a stubWorkspaceInitAdapter) UpdateState(context.Context, string, domain.TrackerState) error {
	return adapter.ErrMutateNotSupported
}

func (a stubWorkspaceInitAdapter) AddComment(context.Context, string, string) error {
	return adapter.ErrMutateNotSupported
}
func (a stubWorkspaceInitAdapter) OnEvent(context.Context, domain.SystemEvent) error { return nil }

type stubInstanceRepo struct {
	created []domain.SubstrateInstance
	byID    map[string]domain.SubstrateInstance
}

func (r *stubInstanceRepo) Get(_ context.Context, id string) (domain.SubstrateInstance, error) {
	inst, ok := r.byID[id]
	if !ok {
		return domain.SubstrateInstance{}, fmt.Errorf("instance %s not found", id)
	}

	return inst, nil
}

func (r *stubInstanceRepo) ListByWorkspaceID(_ context.Context, workspaceID string) ([]domain.SubstrateInstance, error) {
	instances := make([]domain.SubstrateInstance, 0, len(r.byID))
	for _, inst := range r.byID {
		if inst.WorkspaceID == workspaceID {
			instances = append(instances, inst)
		}
	}

	return instances, nil
}

func (r *stubInstanceRepo) Create(_ context.Context, inst domain.SubstrateInstance) error {
	if r.byID == nil {
		r.byID = make(map[string]domain.SubstrateInstance)
	}
	r.created = append(r.created, inst)
	r.byID[inst.ID] = inst

	return nil
}

func (r *stubInstanceRepo) Update(_ context.Context, inst domain.SubstrateInstance) error {
	if r.byID == nil {
		r.byID = make(map[string]domain.SubstrateInstance)
	}
	r.byID[inst.ID] = inst

	return nil
}

func (r *stubInstanceRepo) Delete(_ context.Context, id string) error {
	delete(r.byID, id)

	return nil
}

func newWorkspaceInitHarnessConfig() *config.Config {
	cfg := &config.Config{}
	cfg.Harness.Default = config.HarnessClaudeCode
	cfg.Adapters.ClaudeCode.BridgePath = "/bin/sh"

	return cfg
}

func TestInitializeWorkspaceServicesCmd_RebuildsServicesAndRegistersInstance(t *testing.T) {
	t.Setenv("SUBSTRATE_HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	workspaceDir := t.TempDir()
	instanceRepo := &stubInstanceRepo{}
	settings := &SettingsService{transacter: repository.NoopTransacter{Res: repository.Resources{Instances: instanceRepo}}}
	current := Services{
		Cfg:      newWorkspaceInitHarnessConfig(),
		Settings: settings,
	}

	msg := initializeWorkspaceServicesCmd(settings, current, "ws-1", "workspace", workspaceDir)()
	got, ok := msg.(WorkspaceServicesReloadedMsg)
	if !ok {
		t.Fatalf("msg = %T, want WorkspaceServicesReloadedMsg", msg)
	}
	if got.Reload.Services.WorkspaceID != "ws-1" {
		t.Fatalf("workspace id = %q, want ws-1", got.Reload.Services.WorkspaceID)
	}
	if got.Reload.Services.WorkspaceName != "workspace" {
		t.Fatalf("workspace name = %q, want workspace", got.Reload.Services.WorkspaceName)
	}
	if got.Reload.Services.WorkspaceDir != workspaceDir {
		t.Fatalf("workspace dir = %q, want %q", got.Reload.Services.WorkspaceDir, workspaceDir)
	}
	if got.Reload.Services.InstanceID == "" {
		t.Fatal("expected instance id to be registered")
	}
	if len(got.Reload.Services.Adapters) != 1 || got.Reload.Services.Adapters[0].Name() != "manual" {
		t.Fatalf("adapters = %v, want single manual adapter", got.Reload.Services.Adapters)
	}
	if got.Reload.Services.Harnesses.Planning == nil || got.Reload.Services.Harnesses.Implementation == nil || got.Reload.Services.Harnesses.Review == nil || got.Reload.Services.Harnesses.Foreman == nil {
		t.Fatal("expected all harnesses to be rebuilt")
	}
	if len(instanceRepo.created) != 1 {
		t.Fatalf("created instances = %d, want 1", len(instanceRepo.created))
	}
	created := instanceRepo.created[0]
	if created.WorkspaceID != "ws-1" {
		t.Fatalf("created workspace id = %q, want ws-1", created.WorkspaceID)
	}
	if created.PID != os.Getpid() {
		t.Fatalf("created pid = %d, want %d", created.PID, os.Getpid())
	}
}

func TestApp_WorkspaceInitDoneTriggersServiceReload(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}
	app := NewApp(Services{
		Settings: &SettingsService{},
		SettingsData: SettingsSnapshot{
			Sections:  buildSettingsSections(cfg),
			Providers: buildProviderStatuses(cfg),
		},
	})

	model, cmd := app.Update(WorkspaceInitDoneMsg{WorkspaceID: "ws-1", WorkspaceName: "workspace", WorkspaceDir: "/tmp/ws"})
	if cmd == nil {
		t.Fatal("expected WorkspaceInitDoneMsg to trigger service reload command")
	}
	updated, ok := model.(App)
	if !ok {
		t.Fatalf("model = %T, want App", model)
	}
	if updated.hasWorkspace {
		t.Fatal("workspace should not be marked ready until services finish reloading")
	}
	if updated.activeOverlay != overlayWorkspaceInit {
		t.Fatalf("activeOverlay = %v, want %v", updated.activeOverlay, overlayWorkspaceInit)
	}
}

func TestApp_WorkspaceServicesReloadedMsgAppliesReload(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}
	snapshot := SettingsSnapshot{Sections: buildSettingsSections(cfg), Providers: buildProviderStatuses(cfg)}
	app := NewApp(Services{Settings: &SettingsService{}, SettingsData: snapshot})
	app.activeOverlay = overlayWorkspaceInit

	reload := viewsServicesReload{
		SessionsDir:  "/tmp/sessions",
		SettingsData: snapshot,
		Services: Services{
			WorkspaceID:   "ws-1",
			WorkspaceName: "workspace",
			WorkspaceDir:  "/tmp/ws",
			Adapters:      []adapter.WorkItemAdapter{stubWorkspaceInitAdapter{name: "manual"}},
			Settings:      &SettingsService{},
			SettingsData:  snapshot,
		},
	}

	model, cmd := app.Update(WorkspaceServicesReloadedMsg{Reload: reload, Message: "Workspace initialized"})
	if cmd == nil {
		t.Fatal("expected WorkspaceServicesReloadedMsg to trigger follow-up loads")
	}
	updated, ok := model.(App)
	if !ok {
		t.Fatalf("model = %T, want App", model)
	}
	if !updated.hasWorkspace {
		t.Fatal("expected workspace to be marked ready")
	}
	if updated.activeOverlay != overlayNone {
		t.Fatalf("activeOverlay = %v, want %v", updated.activeOverlay, overlayNone)
	}
	if updated.svcs.WorkspaceID != "ws-1" {
		t.Fatalf("workspace id = %q, want ws-1", updated.svcs.WorkspaceID)
	}
	if got := updated.statusBarText(); got != "workspace · 0 active sessions" {
		t.Fatalf("status bar text = %q, want %q", got, "workspace · 0 active sessions")
	}
	if updated.newSession.workspaceID != "ws-1" {
		t.Fatalf("new session workspace id = %q, want ws-1", updated.newSession.workspaceID)
	}
	if len(updated.newSession.adapters) != 1 || updated.newSession.adapters[0].Name() != "manual" {
		t.Fatalf("new session adapters = %v, want single manual adapter", updated.newSession.adapters)
	}
	if updated.sessionsDir != "/tmp/sessions" {
		t.Fatalf("sessions dir = %q, want /tmp/sessions", updated.sessionsDir)
	}
}

func TestApp_WorkspaceServicesReloadedMsgRestoresOverlaySizes(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}
	snapshot := SettingsSnapshot{Sections: buildSettingsSections(cfg), Providers: buildProviderStatuses(cfg)}
	app := NewApp(Services{Settings: &SettingsService{}, SettingsData: snapshot})

	// Establish terminal size before the services reload.
	const wantWidth, wantHeight = 160, 48
	model, _ := app.Update(tea.WindowSizeMsg{Width: wantWidth, Height: wantHeight})
	app = model.(App)

	reload := viewsServicesReload{
		SessionsDir:  "/tmp/sessions",
		SettingsData: snapshot,
		Services: Services{
			WorkspaceID:   "ws-1",
			WorkspaceName: "workspace",
			WorkspaceDir:  "/tmp/ws",
			Adapters:      []adapter.WorkItemAdapter{stubWorkspaceInitAdapter{name: "manual"}},
			Settings:      &SettingsService{},
			SettingsData:  snapshot,
		},
	}

	model, _ = app.Update(WorkspaceServicesReloadedMsg{Reload: reload, Message: "Workspace initialized"})
	updated, ok := model.(App)
	if !ok {
		t.Fatalf("model = %T, want App", model)
	}

	// New overlay instances must inherit the terminal size set before the reload.
	// Without this, the overlay renders at 1px wide until the user manually resizes.
	if updated.newSession.width != wantWidth || updated.newSession.height != wantHeight {
		t.Fatalf("newSession size = (%d,%d), want (%d,%d) after services reload",
			updated.newSession.width, updated.newSession.height, wantWidth, wantHeight)
	}
	if updated.newSessionAutonomousOverlay.width != wantWidth || updated.newSessionAutonomousOverlay.height != wantHeight {
		t.Fatalf("newSessionAutonomousOverlay size = (%d,%d), want (%d,%d) after services reload",
			updated.newSessionAutonomousOverlay.width, updated.newSessionAutonomousOverlay.height, wantWidth, wantHeight)
	}
}

func TestApp_IgnoresStaleWorkspaceLoadMessages(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}
	snapshot := SettingsSnapshot{Sections: buildSettingsSections(cfg), Providers: buildProviderStatuses(cfg)}
	app := NewApp(Services{
		WorkspaceID:   "ws-new",
		WorkspaceName: "workspace",
		Settings:      &SettingsService{},
		SettingsData:  snapshot,
	})
	app.workItems = []domain.Session{{ID: "wi-current", WorkspaceID: "ws-new", Title: "current"}}
	app.sessions = []domain.Task{{ID: "sess-current", WorkspaceID: "ws-new"}}

	model, cmd := app.Update(SessionsLoadedMsg{
		WorkspaceID: "ws-old",
		Items:       []domain.Session{{ID: "wi-stale", WorkspaceID: "ws-old", Title: "stale"}},
	})
	if cmd != nil {
		t.Fatalf("expected no command for stale work item load, got %v", cmd)
	}
	updated, ok := model.(App)
	if !ok {
		t.Fatalf("model = %T, want App", model)
	}
	if len(updated.workItems) != 1 || updated.workItems[0].ID != "wi-current" {
		t.Fatalf("work items = %#v, want current workspace data preserved", updated.workItems)
	}

	model, cmd = updated.Update(TasksLoadedMsg{
		WorkspaceID: "ws-old",
		Sessions:    []domain.Task{{ID: "sess-stale", WorkspaceID: "ws-old"}},
	})
	if cmd != nil {
		t.Fatalf("expected no command for stale session load, got %v", cmd)
	}
	updated, ok = model.(App)
	if !ok {
		t.Fatalf("model = %T, want App", model)
	}
	if len(updated.sessions) != 1 || updated.sessions[0].ID != "sess-current" {
		t.Fatalf("sessions = %#v, want current workspace data preserved", updated.sessions)
	}
}

func TestApp_InitIncludesReconciliation(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}
	snapshot := SettingsSnapshot{Sections: buildSettingsSections(cfg), Providers: buildProviderStatuses(cfg)}
	app := NewApp(Services{
		WorkspaceID:   "ws-1",
		WorkspaceName: "workspace",
		InstanceID:    "inst-1",
		Settings:      &SettingsService{},
		SettingsData:  snapshot,
	})

	cmd := app.Init()
	if cmd == nil {
		t.Fatal("Init() must return commands when workspace is set (includes reconciliation)")
	}
}

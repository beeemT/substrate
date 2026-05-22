# Plan: Settings Service-Owned State and Async Diagnostics

## Problem Statement

Startup still performs settings work before the first TUI frame. The current flow builds a `SettingsSnapshot` in `cmd/substrate/main.go`, pushes that copied value through `RuntimeContext`, copies it again into `SettingsPage`, and then manually replaces the page snapshot after settings apply/login or startup integration reload.

This has three problems:

1. **Startup blocks on diagnostics**: `SettingsService.Snapshot(cfg)` runs `app.DiagnoseHarnesses(cfg, "")` before the TUI renders.
2. **Settings state is copied instead of owned**: `RuntimeContext.SettingsData`, `Services.SettingsData`, `viewsServicesReload.SettingsData`, and `SettingsPage` all carry copies of the same data.
3. **Staleness risk**: `App.harnessWarningToast()` reads `a.runtimeCtx.SettingsData.HarnessWarning`, so warnings can lag behind a service reload unless every path remembers to update the runtime copy.

Settings state should have one owner: the settings service owned by `ServiceManager`. UI surfaces should ask that service for the current snapshot when they need settings data.

## Current Flow

### Startup

`cmd/substrate/main.go` currently does:

```go
settingsData, err := serviceMgr.Settings().Snapshot(cfg)
return views.RunTUI(serviceMgr, views.RuntimeContext{
    Cfg:          cfg,
    SettingsData: settingsData,
    ...
})
```

`Snapshot(cfg)` currently:

1. resolves the config path;
2. reads raw YAML;
3. loads secrets;
4. runs `app.DiagnoseHarnesses(cfg, "")`;
5. builds sections, provider statuses, raw YAML, and `HarnessWarning`.

### App and page construction

`internal/tui/views/app.go` passes the copied snapshot into `SettingsPage`:

```go
settingsPage: NewSettingsPage(provider.Settings(), runtimeCtx.SettingsData, st),
```

`SettingsPage` copies snapshot fields into page-local state:

```go
sections       []SettingsSection
providerStatus map[string]ProviderStatus
rawContent     string
warningText    string
```

### Service reload

`viewsServicesReload` currently includes `SettingsData SettingsSnapshot`.

`App.applyServicesReload()` does:

```go
a.settingsPage.SetSnapshot(reload.SettingsData)
```

After removing `SettingsData`, this must become:

```go
a.settingsPage.RefreshFromService()
```

`SettingsAppliedMsg` handling in `SettingsPage.Update` must make the same cutover; it cannot read `msg.Reload.SettingsData`.

### Settings apply/login

`SettingsPage.applyCmd()` currently serializes sections itself and then calls `SettingsService.Apply()`:

```go
raw, _, err := m.service.Serialize(m.sections)
result, err := m.service.Apply(context.Background(), raw, svcs)
return SettingsAppliedMsg{Reload: result.Services, Message: result.Message}
```

`SettingsService.Apply()` rebuilds services, saves raw YAML, and returns a `SettingsApplyResult` containing a reload payload with another copied snapshot.

## Target Architecture

### Ownership rule

`SettingsService` owns settings state. The service manager owns the concrete settings service instance and preserves it across service graph rebuilds.

Consumers must not carry long-lived copies of settings data outside the settings page's local editable form state. The app and page obtain the current persisted/resolved settings state from `provider.Settings().Snapshot()`.

### Interface and implementation

Go cannot have both an exported interface and concrete struct named `SettingsService`. Introduce an interface and rename the concrete implementation.

```go
type SettingsService interface {
    Snapshot() SettingsSnapshot
    RefreshConfigOnly(ctx context.Context, cfg *config.Config) error
    RefreshWithDiagnostics(ctx context.Context, cfg *config.Config) error
    Save(ctx context.Context, sections []SettingsSection, current Services) (SettingsApplyResult, error)
    TestProvider(ctx context.Context, provider string, sections []SettingsSection) (ProviderStatus, error)
    LoginProvider(ctx context.Context, provider, harness string, sections []SettingsSection, svcs Services) (SettingsLoginResult, error)
    RefreshLoginSnapshot(ctx context.Context, sections []SettingsSection) error
}
```

Concrete implementation:

```go
type settingsService struct {
    transacter  atomic.Transacter[repository.Resources]
    secretStore config.SecretStore
    serviceMgr  *ServiceManager

    mu       sync.RWMutex
    snapshot SettingsSnapshot
}

var _ SettingsService = (*settingsService)(nil)
```

`NewSettingsService(...) SettingsService` should remain the constructor. If tests need access to concrete-only helpers, prefer moving those helpers behind package-private functions rather than exporting the struct.

`SettingsLoginResult` should no longer carry a snapshot copy:

```go
type SettingsLoginResult struct {
    Message string
    Dirty   bool
}
```

### Snapshot shape and builders

Add explicit diagnostic state so empty warning does not mean both "healthy" and "not checked yet".

```go
type SettingsDiagnosticsState string

const (
    SettingsDiagnosticsPending SettingsDiagnosticsState = "pending"
    SettingsDiagnosticsReady   SettingsDiagnosticsState = "ready"
)

type SettingsSnapshot struct {
    Sections         []SettingsSection
    Providers        map[string]ProviderStatus
    RawYAML          string
    HarnessWarning   string
    DiagnosticsState SettingsDiagnosticsState
}
```

`Snapshot()` returns a defensive copy:

- copy `Sections` and nested field slices as needed;
- copy the `Providers` map;
- return scalar fields directly.

No caller should be able to mutate the service's cached snapshot accidentally.

Split snapshot construction so diagnostics are always explicit input, never a hidden side effect:

```go
func buildSettingsSections(cfg *config.Config) []SettingsSection
func buildSettingsSectionsWithDiagnostics(cfg *config.Config, diagnostics app.HarnessDiagnostics) []SettingsSection
func buildSettingsSnapshot(cfg *config.Config, raw string, diagnosticsState SettingsDiagnosticsState, diagnostics app.HarnessDiagnostics) SettingsSnapshot
```

`buildSettingsSections` must not call `app.DiagnoseHarnesses`. Only the diagnostics-aware helper annotates harness warnings. This closes the current indirect startup blocker where `buildSettingsSections` calls `annotateHarnessWarnings(..., app.DiagnoseHarnesses(cfg, ""))`.

## Service Behavior

### `RefreshConfigOnly(ctx, cfg)`

Builds and stores a startup-safe snapshot:

1. read raw config YAML;
2. build settings sections from already-loaded config using the config-only builder;
3. build provider statuses from config;
4. set `DiagnosticsState = SettingsDiagnosticsPending`;
5. leave `HarnessWarning = ""`;
6. do not annotate section-level harness warnings.

It must not run `app.DiagnoseHarnesses`, directly or indirectly through section building.

It should not reload secrets during normal startup because `main.go` already calls `config.LoadSecrets(cfg, config.OSKeychainStore{})` before service initialization. If a future caller needs config-only refresh for a config that has not had secrets loaded, that caller must load secrets first or use a separate explicit helper.

### `RefreshWithDiagnostics(ctx, cfg)`

Builds and stores a fully resolved snapshot:

1. read the workspace root from current services (`ServiceManager.GetServices().WorkspaceDir`) if available;
2. build the same base data as `RefreshConfigOnly`;
3. run `app.DiagnoseHarnesses(cfg, workspaceRoot)`;
4. annotate sections through the diagnostics-aware section builder;
5. set `HarnessWarning = diagnostics.WarningSummary()`;
6. set `DiagnosticsState = SettingsDiagnosticsReady`.

Use the workspace root when available from the current services (`WorkspaceDir`) so bridge/harness readiness checks match runtime behavior. Do not hold the settings mutex while reading `ServiceManager`, running diagnostics, rebuilding sections, or doing filesystem work; lock only to publish the finished snapshot.

### `Save(ctx, sections, current)`

Replace the page-level `Serialize` + `Apply` choreography with one service method.

`Save` should:

1. serialize sections into raw YAML and config;
2. validate config;
3. save secrets;
4. save raw YAML before publishing a rebuilt service graph, so a disk write failure cannot leave runtime services using config that was not persisted;
5. rebuild the service graph through `ServiceManager.Rebuild(ctx, cfg, current)`;
6. stop old Foreman if required, preserving existing behavior;
7. refresh the cached settings snapshot with diagnostics;
8. return `SettingsApplyResult` containing the service reload and message.

If implementation cannot safely move raw config persistence before rebuild without changing validation or secret behavior, add a staged rebuild/swap helper instead. The invariant is that `SaveRaw` failure must not publish a new runtime service graph.

`Serialize`, `SaveRaw`, and `Apply` can become private helpers unless package tests need direct access. If tests do need them, keep package-private helpers and update tests to exercise `Save` for the public contract.

### Provider testing and login

`TestProvider` and `LoginProvider` remain service methods.

For login completion:

- update the cached snapshot through the service;
- return a `SettingsLoginResult` carrying only `Message` and `Dirty`;
- remove `SettingsLoginResult.Snapshot`;
- avoid direct calls from the page to `settingsSnapshotFromConfig`.

For harness-backed login, `LoginProvider` should update the cached snapshot before returning. For the Sentry `tea.ExecProcess("sentry", "auth", "login")` special path, add/use `RefreshLoginSnapshot(ctx, sections)` (or an equivalent service method) after the external process succeeds, then let the page refresh from the service. `settings_page.go` must not construct snapshots directly.

## ServiceManager and Provider Changes

### `Services`

Change:

```go
Settings     *SettingsService
SettingsData SettingsSnapshot
```

To:

```go
Settings SettingsService
```

Remove `SettingsData` from `Services` entirely.

### `ServiceProvider`

Change:

```go
Settings() *SettingsService
```

To:

```go
Settings() SettingsService
```

Update test providers accordingly.

### `ServiceManager`

`ServiceManager` already preserves the settings service across rebuilds:

```go
settingsSvc := current.Settings
if settingsSvc == nil {
    settingsSvc = NewSettingsService(...)
}
```

Keep that behavior. The cached settings snapshot lives inside this preserved service, so service graph rebuilds do not reset settings UI state.

## RuntimeContext and Reload Payload

Remove copied settings data from runtime and reload payloads.

### Remove from `RuntimeContext`

```go
SettingsData SettingsSnapshot
```

### Remove from `viewsServicesReload`

```go
SettingsData SettingsSnapshot
```

### App construction

Change:

```go
settingsPage: NewSettingsPage(provider.Settings(), runtimeCtx.SettingsData, st),
```

To:

```go
settingsPage: NewSettingsPage(provider.Settings(), st),
```

`NewSettingsPage` reads the initial snapshot from the service.

### App harness warning toast

Change from runtime snapshot:

```go
warning := strings.TrimSpace(a.runtimeCtx.SettingsData.HarnessWarning)
```

To service snapshot:

```go
snapshot := a.provider.Settings().Snapshot()
if snapshot.DiagnosticsState != SettingsDiagnosticsReady {
    return components.Toast{}, false
}
warning := strings.TrimSpace(snapshot.HarnessWarning)
```

No warning toast should show while diagnostics are pending.

## SettingsPage Changes

### Construction

Change:

```go
func NewSettingsPage(svc *SettingsService, snapshot SettingsSnapshot, st styles.Styles) SettingsPage
```

To:

```go
func NewSettingsPage(svc SettingsService, st styles.Styles) SettingsPage
```

The constructor calls `svc.Snapshot()` and initializes page-local editable fields from it.

### Page-local state

The page still needs local mutable fields while the user edits settings:

- `sections`
- `providerStatus`
- `rawContent`
- edit cursor/focus state
- dirty state

But these are editable working copies, not canonical settings state.

### Refresh from service

Add:

```go
func (m *SettingsPage) RefreshFromService()
```

Rules:

- If `m.dirty == false`, replace editable state from `m.service.Snapshot()`.
- If `m.dirty == true`, do not overwrite edited fields.
- While dirty, it is acceptable to update non-editing status surfaces only if that can be done without mutating user edits. Otherwise, defer until save/cancel.

This prevents async diagnostics from clobbering in-progress edits.

`SettingsAppliedMsg` handling should call `m.RefreshFromService()` after `m.service.Save(...)` succeeds; it must not read `msg.Reload.SettingsData`. `SettingsLoginCompletedMsg` handling should also call `m.RefreshFromService()` and preserve existing dirty/expanded-state behavior without reading `msg.Snapshot`.

### Pending diagnostics UI

When the current service snapshot has `DiagnosticsState == SettingsDiagnosticsPending`, the settings footer should show:

```text
Checking harness availability…
```

This should be short and local to settings. Do not show a global warning toast until diagnostics are ready.

Existing warning footer behavior remains after diagnostics complete:

```text
warning: Harness unavailable. Check Harness Routing.
```

## Startup Flow

### `cmd/substrate/main.go`

After service manager initialization, refresh settings cheaply:

```go
if err := serviceMgr.Settings().RefreshConfigOnly(ctx, cfg); err != nil {
    return fmt.Errorf("load settings: %w", err)
}

return views.RunTUI(serviceMgr, views.RuntimeContext{
    Cfg: cfg,
    ...
})
```

Do not call diagnostics before `RunTUI`.

### `StartupIntegrationsCmd`

After full integration rebuild succeeds, run settings diagnostics before emitting the ready message. Replace the current `reloaded.Settings.Snapshot(runtimeCtx.Cfg)` call in `StartupIntegrationsCmd` with `RefreshWithDiagnostics`; do not leave any `Snapshot(cfg)` compatibility path:

```go
reloaded, err := serviceMgr.Rebuild(context.Background(), runtimeCtx.Cfg, current)
if err != nil {
    return StartupIntegrationsReadyMsg{Err: err}
}

if err := reloaded.Settings.RefreshWithDiagnostics(context.Background(), runtimeCtx.Cfg); err != nil {
    return StartupIntegrationsReadyMsg{Err: err}
}

return StartupIntegrationsReadyMsg{Reload: viewsServicesReload{Services: *reloaded, ...}}
```

The startup toast remains visible until integrations and settings diagnostics are complete.

### Non-workspace startup

If there is no workspace and no deferred integration command runs, still avoid blocking the first frame on diagnostics. Either:

1. schedule a small `SettingsDiagnosticsStartCmd` from `App.Init()` when diagnostics are pending; or
2. run diagnostics lazily when Settings opens.

Preferred: schedule `SettingsDiagnosticsStartCmd` whenever `provider.Settings().Snapshot().DiagnosticsState == SettingsDiagnosticsPending` and startup integrations are not already running. This keeps behavior consistent across workspace and non-workspace launches without blocking the first frame.

## Message and Command Changes

### New/adjusted messages

Add a message for standalone settings diagnostics completion if needed outside startup integrations:

```go
type SettingsDiagnosticsStartMsg struct{}

type SettingsDiagnosticsReadyMsg struct {
    Err error
}
```

Startup integrations can continue using `StartupIntegrationsReadyMsg`; the diagnostics-ready message is only for the non-workspace or lazy path.

### New command

```go
func SettingsDiagnosticsStartCmd() tea.Cmd
func SettingsDiagnosticsCmd(settings SettingsService, cfg *config.Config) tea.Cmd
```

`SettingsDiagnosticsStartCmd` is a short tick that returns `SettingsDiagnosticsStartMsg` after the first frame.

`SettingsDiagnosticsCmd` must not run diagnostics synchronously on the Bubble Tea command path. Bubble Tea commands block the update loop. The command should only launch a goroutine that runs `settings.RefreshWithDiagnostics(context.Background(), cfg)` and delivers `SettingsDiagnosticsReadyMsg` through the app's existing asynchronous delivery mechanism (or an equivalent non-blocking program-send/event-bus path). If that delivery mechanism is not available to the command, add it before wiring diagnostics.

`App.Update` must handle `SettingsDiagnosticsReadyMsg` by logging/reporting `Err`, calling `settingsPage.RefreshFromService()`, and letting the pinned harness warning toast appear only when the refreshed snapshot is `SettingsDiagnosticsReady`.

## Cutover Steps

### 1. Introduce interface and cached implementation

Files:

- `internal/tui/views/settings_service.go`
- `internal/tui/views/service_provider.go`
- `internal/tui/views/services.go`
- `internal/tui/views/service_manager.go`
- `internal/tui/views/test_provider.go`

Changes:

- rename concrete `SettingsService` to `settingsService`;
- introduce `SettingsService` interface;
- add compile-time implementation check;
- add mutex-protected cached snapshot;
- add `Snapshot`, `RefreshConfigOnly`, `RefreshWithDiagnostics`, `RefreshLoginSnapshot`, and `Save`;
- split settings section builders so config-only section construction never runs diagnostics.

### 2. Remove snapshot passing through runtime/reload structs

Files:

- `internal/tui/views/runtime_context.go`
- `internal/tui/views/services.go`
- `internal/tui/views/settings_service.go`
- `internal/tui/views/cmds.go`
- `internal/tui/views/app.go`
- `cmd/substrate/main.go`

Changes:

- remove `RuntimeContext.SettingsData`;
- remove `Services.SettingsData`;
- remove `viewsServicesReload.SettingsData`;
- replace runtime snapshot reads with `provider.Settings().Snapshot()`.

### 3. Refactor SettingsPage to service source

Files:

- `internal/tui/views/settings_page.go`
- settings page tests

Changes:

- constructor takes `SettingsService` only;
- page initializes from `svc.Snapshot()`;
- add `RefreshFromService()`;
- update apply command to call `m.service.Save(...)`;
- update login paths to refresh snapshots through the service;
- remove `SettingsLoginResult.Snapshot` and replace `SettingsAppliedMsg`/`SettingsLoginCompletedMsg` snapshot reads with `RefreshFromService()`.

### 4. Wire async startup diagnostics

Files:

- `cmd/substrate/main.go`
- `internal/tui/views/cmds.go`
- `internal/tui/views/msgs.go`
- `internal/tui/views/app.go`

Changes:

- startup calls `RefreshConfigOnly` only;
- background startup calls `RefreshWithDiagnostics` after integration rebuild and removes the old `Snapshot(cfg)` call;
- non-workspace startup schedules settings diagnostics asynchronously if pending;
- diagnostics commands do not block the Bubble Tea update loop;
- `App.Update` handles diagnostics completion by refreshing the settings page from the service;
- startup warning toast reads service snapshot and hides while pending.

### 5. Update tests

Update affected tests to construct `SettingsService` through `NewSettingsService` or a small test implementation of the interface.

## Tests

### Settings service tests

1. `SettingsService_RefreshConfigOnly_SkipsHarnessDiagnostics`
   - uses config that would warn if diagnostics ran;
   - asserts `DiagnosticsState == SettingsDiagnosticsPending`;
   - asserts `HarnessWarning == ""`;
   - asserts section-level harness warning annotations are absent;
   - asserts sections/providers/raw YAML are populated.

2. `SettingsSections_ConfigOnlyBuilder_DoesNotDiagnoseHarnesses`
   - covers the indirect `buildSettingsSections -> DiagnoseHarnesses` regression path;
   - asserts config-only section building does not annotate harness warnings for an invalid harness config.

3. `SettingsService_RefreshWithDiagnostics_StoresHarnessWarning`
   - uses invalid harness config;
   - asserts `DiagnosticsState == SettingsDiagnosticsReady`;
   - asserts warning summary and section warning annotations are populated.

4. `SettingsService_Snapshot_ReturnsDefensiveCopy`
   - mutate returned provider map/sections;
   - assert subsequent `Snapshot()` is unchanged.

5. `SettingsService_Save_UpdatesCachedSnapshot`
   - edit a setting;
   - call `Save`;
   - assert cached snapshot reflects saved config and raw YAML.

6. `SettingsService_Save_DoesNotPublishRebuildWhenRawSaveFails`
   - force raw YAML persistence to fail;
   - assert the current service graph is not swapped to the new config.

### App tests

1. `AppHarnessWarningToast_ReadsSettingsService`
   - update service snapshot after app construction;
   - assert toast reflects latest warning.

2. `AppHarnessWarningToast_HiddenWhileDiagnosticsPending`
   - pending diagnostics with no warning;
   - assert no warning toast.

3. `StartupIntegrationsCmd_RefreshesSettingsDiagnostics`
   - initial snapshot pending;
   - command completes;
   - assert snapshot ready;
   - assert startup toast is dismissed by ready handler.

4. `SettingsDiagnosticsCmd_DoesNotBlockUpdateLoop`
   - command returns promptly after launching diagnostics;
   - diagnostics completion is delivered by `SettingsDiagnosticsReadyMsg`.

5. `SettingsDiagnosticsReadyMsg_RefreshesSettingsPageFromService`
   - update service snapshot;
   - send ready message;
   - assert settings page refreshes when clean and preserves dirty edits when dirty.

### Settings page tests

1. `SettingsPage_ShowsCheckingHarnessAvailabilityWhilePending`
   - snapshot pending;
   - render footer;
   - assert checking copy.

2. `SettingsPage_DoesNotClobberDirtyEditsOnAsyncDiagnostics`
   - edit a field;
   - update service snapshot;
   - call `RefreshFromService()`;
   - assert edit remains.

3. Existing apply/login tests
   - update expectations to use `SettingsService.Save` and service-owned snapshots;
   - assert login completion refreshes from the service and no longer depends on `SettingsLoginResult.Snapshot`.

## Acceptance Criteria

- No harness diagnostics run before the first TUI frame, including indirect calls from settings section construction.
- Settings data has one canonical owner: the settings service preserved by `ServiceManager`.
- `RuntimeContext`, `Services`, and `viewsServicesReload` no longer carry `SettingsSnapshot` copies.
- Settings page can open during startup and shows pending diagnostics accurately.
- Async diagnostics do not clobber unsaved settings edits.
- Harness warning toast reads the latest service snapshot and never shows while diagnostics are pending.
- Settings save updates persisted config, rebuilds services, refreshes cached settings, and updates the page.
- Existing startup integration toast remains visible until integrations and diagnostics are complete.
- Diagnostics commands do not block the Bubble Tea update loop after they are scheduled.
- A failed raw settings file write does not publish a rebuilt runtime service graph.

## Risks and Mitigations

### Risk: Async diagnostics overwrite user edits

Mitigation: `SettingsPage.RefreshFromService()` must not replace editable state while `dirty == true`.

### Risk: Snapshot callers mutate cached state

Mitigation: `SettingsService.Snapshot()` returns defensive copies.

### Risk: Interface rename churn touches many tests

Mitigation: keep constructor name `NewSettingsService`; update tests to depend on the interface or use a minimal fake where service behavior is not under test.

### Risk: Settings save and service rebuild recursion

Mitigation: `settingsService.Save` may call `ServiceManager.Rebuild`, but `ServiceManager` must continue preserving the same `SettingsService` instance from `current.Settings`. Do not construct a new settings service during rebuild when one already exists.

### Risk: Non-workspace startup never runs diagnostics

Mitigation: schedule a separate async diagnostics command whenever the settings snapshot is pending and startup integrations are not running.

### Risk: Diagnostics command freezes the TUI after first render

Mitigation: command functions must only launch diagnostics work and return immediately. Deliver completion through an explicit async message path.

### Risk: Settings save publishes unpersisted config

Mitigation: raw config persistence must succeed before the rebuilt service graph is made current, or the implementation must use a staged rebuild/swap helper that preserves that invariant.

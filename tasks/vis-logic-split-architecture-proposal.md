# Architecture Proposal: Logic / Visualization gRPC Split

<!-- docs:last-integrated-commit 10e50295fb75f72c67233e191ae34fb8fc091f1e -->

## 1. Goal

Separate Substrate's logic layer from its visualization layer so the logic layer can run as an independent local daemon and visualization clients communicate with it over local gRPC.

The target architecture has:

- a daemon-owned SQLite database, service graph, event bus, adapters, orchestrators, harness subprocesses, session registry, settings/secrets implementation, and log tailing
- one daemon-owned event stream channel for domain events and live status updates
- product-shaped gRPC APIs for actions, queries, settings, logs, and read models
- no direct database, service, orchestrator, adapter, harness, repository, filesystem-repo, keychain, or event-bus ownership inside the TUI process
- a migration path that preserves the current TUI while making the daemon boundary explicit

The durable architectural rule is:

> Expose product actions and product read models, not repository CRUD and not current Go service/orchestrator structs.

Good API shape:

- `ApprovePlan`
- `RunImplementation`
- `AnswerQuestion`
- `GetSessionOverview`
- `SubscribeEvents`

Avoid API shape:

- `PlanService.UpdateStatus`
- `AgentSessionService.ListByWorkItem`
- `Repository.GetSubPlans`
- `EventBus.SubscribeRawOnly`

The daemon should hide orchestration internals so the service graph can keep evolving without forcing every visualization client to know the current internal package layout.

## 2. Context

Substrate today is a single Go process (`cmd/substrate/main.go`) that in-process wires SQLite, a service graph, an event bus, adapters, child-process bridges, and a Bubble Tea TUI. The TUI is the largest consumer of the in-process event bus and the only direct caller of the service/orchestrator surface. It also embeds settings implementation, provider diagnostics, provider login flows, multi-step orchestrations, in-flight dedup maps, and state derivation that belongs to the product layer rather than the view layer.

The Electron-app plan (`docs/13-electron-app-plan.md`) sketches a future split using JSON-RPC over WebSocket. This proposal uses the same product separation with gRPC instead of JSON-RPC, a single dedicated event stream channel, and standard unary/server-streaming APIs for everything else.

This proposal is intentionally not a request to distribute the current Go package graph over RPC. The process split is an opportunity to define a stable product boundary.

## 3. Goals and Non-goals

### Goals

- `substrate daemon` / `substrate serve` runs as a long-lived local logic process.
- `substrate tui` runs as a visualization client and connects to the daemon over local gRPC.
- `substrate` with no args remains compatible: it starts or connects to a daemon, then launches the TUI.
- The daemon owns SQLite, migrations, config, secrets, service graph composition, event persistence, adapters, orchestrators, harness subprocesses, and session logs.
- Every daemon-published event can reach a client in publish order, with reconnect/catch-up semantics.
- Mutating RPCs are idempotent where duplicate submission would otherwise create duplicate work.
- Settings save continues to perform the existing hard service-graph rebuild, daemon-side, with a clear client resync signal.
- The TUI renders the same product state as today while progressively shedding non-visual responsibilities.
- The API surface remains compatible with future visualization clients such as Electron.

### Non-goals

- Electron renderer implementation.
- New IPC for existing TypeScript bridges. OMP, Claude, ACP, Codex, and foreman-mcp bridges remain daemon-owned subprocesses using their existing stdio/socket protocols.
- Remote multi-host deployment. The daemon listens on a local Unix domain socket by default.
- Replacing the in-process event bus for daemon-internal consumers. The bus remains the daemon's source of truth; gRPC streaming is a delivery mechanism.
- Full event payload schema rewrite in the first cut. Initial events carry raw JSON payloads plus metadata; typed payloads can be added event-by-event later.
- Multi-client editing semantics in the first revision. The API must not preclude multiple clients later, but the first supported mode may be one controlling TUI client per daemon/workspace.

## 4. Current Boundary Findings

### Natural logic boundary

The current natural server boundary is `internal/tui/views.ServiceManager`.

Key files:

- `internal/tui/views/service_manager.go`
- `internal/tui/views/services.go`
- `cmd/substrate/main.go`

`ServiceManager` currently owns the complete runtime graph:

- SQLite-backed repositories through the transacter
- `event.Bus`
- domain services for work items, plans, sub-plans, agent sessions, questions, reviews, events, instances, provider artifacts, filters, and locks
- adapters for work item sources, repo lifecycle, repo sources, and review comment dispatch
- orchestrators for planning, implementation, review, Foreman/question routing, answer routing, and manual sessions
- harnesses and child process bridges
- refresh goroutines and adapter event wiring
- service rebuilds on settings/workspace changes

This graph moves behind the daemon API. The visualization layer must not construct or hold any of it.

### Existing event boundary

The current event system already maps well to a single gRPC stream.

Key files:

- `internal/event/bus.go`
- `internal/domain/event.go`
- `internal/tui/views/event_consumer.go`
- `internal/tui/views/app.go:eventSubscriptionTopics`
- `docs/03-event-system.md`

Current model:

- producers publish `domain.SystemEvent`
- the bus persists events to SQLite before dispatch
- subscribers receive topic-filtered in-memory events
- the TUI subscribes to a fixed set of domain event topics
- the TUI decodes JSON-in-string event payloads into typed Bubble Tea messages

The daemon preserves the persisted event model and exposes gRPC streaming as a delivery mechanism. Persistence remains the source of truth for replay.

### Logic embedded in the TUI

The following responsibilities move daemon-side:

- service graph and orchestration wiring
- adapter event wiring
- refresh-loop startup/stop
- harness construction
- settings-triggered service rebuilds
- service/orchestrator side of command functions from `internal/tui/views/cmds.go`
- autonomous new-session runtime (`internal/tui/views/new_session_autonomous.go`)
- workspace health checks, managed repo scan, worktree list/load, clone/init/remove repo, workspace identity reconciliation
- settings implementation (`internal/tui/views/settings_service.go`): config load/save, secret/keychain writes, provider diagnostics, provider login flows, provider-specific adapter construction
- session log reading and tailing
- product-level read-model derivation after the command/event split is stable

The TUI keeps:

- Bubble Tea `Update` mechanics
- selected pane, focused item, cursor, scroll, expanded/collapsed state
- layout sizing and clipping
- styles and rendered strings
- modal and overlay presentation state
- local form draft state before submit
- view-level debounce/submitting flags
- operator-machine actions such as opening browser URLs or terminals

`OpenBrowserCmd`, `OpenTerminalCmd`, and `OpenTerminalWithCmd` stay client-side by default because they target the operator's visible machine, not daemon state.

## 5. Target Process Model

```text
substrate daemon / substrate serve
  owns:
    SQLite / migrations / WAL
    config + secrets + keychain writes
    ServiceManager / service graph
    event.Bus and persisted system_events
    adapters and repo/source integrations
    harness subprocesses and bridges
    orchestrators and session registry
    autonomous mode
    settings rebuild lifecycle
    session log tailing
    gRPC server over Unix socket

substrate tui
  owns:
    Bubble Tea model/update/view
    layout/rendering
    local navigation and presentation state
    gRPC client
    event/log stream consumers
```

CLI modes:

```text
substrate daemon   # run only the logic daemon
substrate serve    # accepted alias if preferred by CLI naming conventions
substrate tui      # connect to daemon and run the TUI client
substrate          # compatibility mode: start/connect daemon, then run TUI
```

`substrate` with no args:

1. Resolve daemon metadata for the selected workspace/user.
2. If a healthy daemon is already reachable, connect as a TUI client.
3. If no healthy daemon is reachable, spawn the daemon, wait for readiness, then connect.
4. If a stale PID/socket/metadata file exists, clean it up only after proving the process is gone or not the expected daemon.
5. If another controlling TUI is connected in single-client mode, show a clear error.

TUI exit sends `Disconnect` and stops the client process. It does not stop the daemon. Explicit daemon shutdown uses `substrate daemon stop`, `substrate serve stop`, or a documented shutdown flag/command that sends `SIGTERM` to the daemon and lets it run the existing graceful teardown path.

## 6. Socket, Auth, and Metadata

Use Unix domain sockets by default. Do not expose remote network listeners by default.

Preferred socket path:

```text
$XDG_RUNTIME_DIR/substrate/<workspace-or-user-id>.sock
```

macOS fallback:

```text
~/Library/Application Support/substrate/run/<workspace-or-user-id>.sock
```

The daemon writes an owner-only metadata file beside the socket:

```text
socket_path
pid
version
build_sha
schema_version
workspace_id
started_at
token
```

Auth rules:

- Filesystem permissions on the socket directory are the primary local boundary.
- The daemon also generates a random bearer token at startup, writes it with owner-only permissions, and requires it in gRPC metadata on every call.
- If loopback TCP is ever added, the bearer token is mandatory and remote listeners remain opt-in.
- gRPC reflection may be enabled by default for local debugging only when socket/token auth is enforced; it must be disableable.

Health and introspection live on the same gRPC server:

```proto
service SystemAPI {
  rpc Health(HealthRequest) returns (HealthResponse);
  rpc Info(InfoRequest) returns (InfoResponse);
  rpc Disconnect(DisconnectRequest) returns (DisconnectResponse);
  rpc Shutdown(ShutdownRequest) returns (ShutdownResponse);
}
```

`Health` returns ready/not-ready, daemon version, build SHA, schema version, uptime, workspace ID, and whether a rebuild is in progress.

## 7. Event Stream

The event stream is the load-bearing channel of the architecture. The gRPC client must see the same persisted event sequence the daemon publishes, in order, and must be able to reconnect without losing causality.

### Monotonic sequence

The current `system_events` table uses IDs that are not a reliable ordering primitive. Add a monotonic `sequence INTEGER` column populated in the same transaction as the event row.

Migration:

```text
migrations/024_system_events_sequence.sql
```

Implementation requirements:

- `sequence` is unique and increasing per daemon database.
- The sequence assignment occurs inside the event persistence transaction.
- SQLite write serialization must be relied on explicitly; do not implement sequence assignment in a way that races under concurrent publishers.
- `domain.SystemEvent` gains `Sequence` so in-process and gRPC subscribers use the same ordering primitive.

### Service shape

```proto
service EventStreamAPI {
  rpc Subscribe(SubscribeEventsRequest) returns (stream EventBatch);
}

message SubscribeEventsRequest {
  string workspace_id = 1;
  uint64 after_sequence = 2;       // 0 = no explicit replay cursor
  int32 replay_window = 3;         // default 500, bounded by server max
  repeated string event_types = 4; // empty = all events visible to client
  bool include_snapshot_marker = 5;
}

message EventBatch {
  repeated SystemEventEnvelope events = 1;
  uint64 latest_sequence = 2;
  bool caught_up = 3;
}

message SystemEventEnvelope {
  uint64 sequence = 1;
  string id = 2;
  string workspace_id = 3;
  string type = 4;
  google.protobuf.Timestamp created_at = 5;
  string payload_json = 6;
}
```

Semantics:

1. If `after_sequence > 0`, replay persisted events with `sequence > after_sequence`.
2. If `after_sequence == 0` and `replay_window > 0`, send the latest bounded replay window so a fresh TUI can rehydrate recent activity.
3. Subscribe to live `event.Bus` events after replay.
4. Stream monotonic event envelopes/batches.
5. The client stores the latest fully processed sequence.
6. On reconnect, the client resumes from that sequence.
7. If the replay cursor is older than retained events, the server returns a typed stale-cursor error and the client reloads a full snapshot.

Initial envelopes carry raw JSON payloads matching today's `domain.SystemEvent.Payload`. Do not duplicate every event payload as protobuf in the first phase. Typed proto payloads can be added later as optional `oneof` fields event-by-event.

### Back-pressure and slow clients

The server must define one coherent policy across the event bus subscriber buffer, gRPC send buffer, and kernel socket buffer.

Recommended first policy:

- Each connected client has a bounded outbound queue.
- If gRPC send blocks beyond a short deadline, close that client's stream with a retryable slow-consumer status.
- The client reconnects with its last processed sequence.
- The daemon does not block producers on a slow visualization client.
- The server logs drops/closures with structured `slog.Warn` entries.

An explicit ack RPC can be added if batching processed sequence updates through the event stream consumer is not enough. Do not make top-level acceptance depend on ack semantics until the core design chooses it.

## 8. Product-shaped gRPC API

Design APIs around product operations and stable read models. Internal Go services, repositories, and orchestrators are implementation details.

Suggested initial proto package:

```text
api/substrate/v1/
  system.proto
  events.proto
  workspace.proto
  sessions.proto
  agent_sessions.proto
  settings.proto
  logs.proto
  read_models.proto
```

This smaller boundary is deliberate. It avoids baking the current internal package layout into the public wire contract. Additional files can be split out later when a product capability proves stable.

### WorkspaceAPI

```proto
service WorkspaceAPI {
  rpc GetRuntimeContext(GetRuntimeContextRequest) returns (RuntimeContext);
  rpc InitializeWorkspace(InitializeWorkspaceRequest) returns (Workspace);
  rpc HealthCheckWorkspace(HealthCheckWorkspaceRequest) returns (WorkspaceHealth);
  rpc ListManagedRepos(ListManagedReposRequest) returns (ListManagedReposResponse);
  rpc ListWorktrees(ListWorktreesRequest) returns (ListWorktreesResponse);
  rpc CloneRepo(CloneRepoRequest) returns (CloneRepoResponse);
  rpc InitRepo(InitRepoRequest) returns (InitRepoResponse);
  rpc RemoveRepo(RemoveRepoRequest) returns (RemoveRepoResponse);
}
```

### SessionAPI

`Session` is the user-visible work item/session concept. The API should expose product actions, not raw repository writes.

```proto
service SessionAPI {
  rpc GetInitialSnapshot(GetInitialSnapshotRequest) returns (InitialSnapshot);
  rpc ListSessions(ListSessionsRequest) returns (ListSessionsResponse);
  rpc GetSession(GetSessionRequest) returns (Session);
  rpc ArchiveSession(ArchiveSessionRequest) returns (ActionResult);
  rpc UnarchiveSession(UnarchiveSessionRequest) returns (ActionResult);
  rpc DeleteSession(DeleteSessionRequest) returns (ActionResult);
  rpc ApprovePlan(ApprovePlanRequest) returns (ActionResult);
  rpc RequestPlanChanges(RequestPlanChangesRequest) returns (ActionResult);
  rpc SaveReviewedPlan(SaveReviewedPlanRequest) returns (ReviewedPlan);
  rpc StartPlanning(StartPlanningRequest) returns (Operation);
  rpc RestartPlanning(RestartPlanningRequest) returns (Operation);
  rpc RunImplementation(RunImplementationRequest) returns (Operation);
  rpc FinalizeSession(FinalizeSessionRequest) returns (Operation);
  rpc RetryFailedSession(RetryFailedSessionRequest) returns (Operation);
  rpc OverrideAccept(OverrideAcceptRequest) returns (ActionResult);
  rpc FailReview(FailReviewRequest) returns (ActionResult);
}
```

Long-running operations return quickly with an `Operation` token. Completion and progress are observed on the event stream. This matches today's TUI model: commands start work, events drive visible state.

### AgentSessionAPI

```proto
service AgentSessionAPI {
  rpc ListAgentSessions(ListAgentSessionsRequest) returns (ListAgentSessionsResponse);
  rpc SearchHistory(SearchHistoryRequest) returns (SearchHistoryResponse);
  rpc GetInteraction(GetInteractionRequest) returns (GetInteractionResponse);
  rpc AnswerQuestion(AnswerQuestionRequest) returns (ActionResult);
  rpc SkipQuestion(SkipQuestionRequest) returns (ActionResult);
  rpc SteerSession(SteerSessionRequest) returns (ActionResult);
  rpc FollowUpSession(FollowUpSessionRequest) returns (Operation);
  rpc RetrySession(RetrySessionRequest) returns (Operation);
  rpc ResumeAllForSession(ResumeAllForSessionRequest) returns (Operation);
  rpc CancelPipeline(CancelPipelineRequest) returns (ActionResult);
}
```

The daemon owns `orchestrator.SessionRegistry`. Visualization clients interact through events, questions, steering APIs, follow-up APIs, retry APIs, and log streams. Do not expose harness handles or registry internals over gRPC.

### SettingsAPI

```proto
service SettingsAPI {
  rpc GetSettings(GetSettingsRequest) returns (SettingsSnapshot);
  rpc SaveSettings(SaveSettingsRequest) returns (SaveSettingsResponse);
  rpc TestProvider(TestProviderRequest) returns (ProviderStatus);
  rpc LoginProvider(LoginProviderRequest) returns (LoginProviderResponse);
  rpc RefreshProviderDiagnostics(RefreshProviderDiagnosticsRequest) returns (SettingsSnapshot);
}
```

Settings save runs the existing hard service-graph rebuild daemon-side. Clients receive rebuild lifecycle events over `EventStreamAPI`, including a terminal `EventServiceGraphRebuilt`, then refresh runtime context/read models.

### AutonomousModeAPI

This may live in `sessions.proto` or its own file once stable.

```proto
service AutonomousModeAPI {
  rpc StartAutonomousMode(StartAutonomousModeRequest) returns (ActionResult);
  rpc StopAutonomousMode(StopAutonomousModeRequest) returns (ActionResult);
  rpc GetAutonomousModeStatus(GetAutonomousModeStatusRequest) returns (AutonomousModeStatus);
}
```

The daemon owns provider watch streams, filter locks, leases, deduplication, and provider capability checks.

### LogAPI

```proto
service LogAPI {
  rpc TailAgentSessionLog(TailAgentSessionLogRequest) returns (stream SessionLogBatch);
  rpc SnapshotAgentSessionLog(SnapshotAgentSessionLogRequest) returns (SessionLogSnapshot);
  rpc TailAppLog(TailAppLogRequest) returns (stream AppLogBatch);
  rpc SnapshotAppLog(SnapshotAppLogRequest) returns (AppLogSnapshot);
}
```

The daemon reads/tails session log files and owns the app log ring buffer. The TUI renders log batches into its existing views.

### ReadModelAPI

Introduce after the command/event split unless extracting specific read models first materially reduces risk.

```proto
service ReadModelAPI {
  rpc GetSessionOverview(GetSessionOverviewRequest) returns (SessionOverview);
  rpc GetSidebar(GetSidebarRequest) returns (SidebarModel);
  rpc GetPlan(GetPlanRequest) returns (PlanView);
  rpc GetArtifacts(GetArtifactsRequest) returns (ArtifactsView);
  rpc GetAvailableActions(GetAvailableActionsRequest) returns (AvailableActions);
}
```

Once there are multiple visualization clients, action availability and product state derivation must be consistent across clients. The daemon is the correct owner for these read models.

## 9. Initial Snapshot

A separate snapshot RPC is required. Event replay catches up changes, but clients also need a coherent starting view.

Today the TUI seeds state through a fan-out of commands such as `LoadSessionsCmd`, `LoadTasksCmd`, `LoadPlansCmd`, `LoadNewSessionFiltersCmd`, `LoadLiveInstancesCmd`, `ReconcileOrphanedTasksCmd`, and per-session cascades. Over gRPC that should become one bundled product snapshot.

```proto
service SnapshotAPI {
  rpc GetInitialSnapshot(GetInitialSnapshotRequest) returns (GetInitialSnapshotResponse);
}

message GetInitialSnapshotRequest {
  string workspace_id = 1;
}

message GetInitialSnapshotResponse {
  repeated Session sessions = 1;
  repeated AgentSession agent_sessions = 2;
  map<string, Plan> plans = 3;
  map<string, SubPlanList> sub_plans = 4;
  map<string, QuestionList> questions = 5;
  map<string, ReviewList> reviews = 6;
  repeated NewSessionFilter filters = 7;
  repeated Instance live_instances = 8;
  repeated string archived_session_ids = 9;
  uint64 latest_event_sequence = 10;
}
```

Startup flow:

1. Connect and call `Health`.
2. Subscribe to events, recording the stream sequence.
3. Call `GetInitialSnapshot`.
4. Apply snapshot.
5. Apply buffered events with `sequence > snapshot.latest_event_sequence`.
6. Continue live.

This avoids missed updates between snapshot and stream establishment.

## 10. Error, Idempotency, and Operation Model

### Errors

Use gRPC status codes plus structured error details.

```proto
enum ErrorCode {
  ERROR_CODE_UNSPECIFIED = 0;
  ERROR_CODE_NOT_FOUND = 1;
  ERROR_CODE_INVALID_TRANSITION = 2;
  ERROR_CODE_INVALID_INPUT = 3;
  ERROR_CODE_ALREADY_EXISTS = 4;
  ERROR_CODE_CONSTRAINT_VIOLATION = 5;
  ERROR_CODE_GRAPH_REBUILDING = 6;
  ERROR_CODE_DAEMON_SHUTTING_DOWN = 7;
  ERROR_CODE_UNAUTHORIZED = 8;
  ERROR_CODE_STALE_EVENT_CURSOR = 9;
  ERROR_CODE_SLOW_CONSUMER = 10;
  ERROR_CODE_INTERNAL = 11;
  ERROR_CODE_CANCELLED = 12;
  ERROR_CODE_DEADLINE_EXCEEDED = 13;
}

message ErrorInfo {
  ErrorCode code = 1;
  string message = 2;
  map<string, string> details = 3;
  uint64 latest_sequence = 4;
}
```

Map service/domain sentinels to stable product errors. Do not leak repository or orchestration implementation errors as API concepts.

### Idempotency

Every mutating RPC that can create duplicate work accepts an optional `idempotency_key`.

The daemon stores recent idempotency keys per workspace and RPC/action type and returns the cached response on replay. This protects against double-clicks, reconnects, and retry-after-deadline behavior.

Naturally idempotent state transitions can still reject invalid transitions; the idempotency key is for duplicate request delivery, not for hiding domain errors.

### Operations

Long-running actions return:

```proto
message Operation {
  string id = 1;
  string workspace_id = 2;
  string session_id = 3;
  OperationStatus status = 4;
  uint64 started_sequence = 5;
}
```

Completion is visible through domain events. The operation type is not a second event system.

## 11. Package Layout

Suggested layout:

```text
api/substrate/v1/
  system.proto
  events.proto
  workspace.proto
  sessions.proto
  agent_sessions.proto
  settings.proto
  logs.proto
  read_models.proto

internal/logic/
  manager.go              # moved ServiceManager ownership
  services.go             # moved Services aggregation
  settings/
  readmodel/
  autonomous/

internal/daemon/
  server/
  client/
  socket/
  auth/

internal/tui/
  views/                  # Bubble Tea only
```

The daemon packages may import service, orchestrator, adapter, repository, event, config, and bridge packages.

The TUI should eventually not import:

- `internal/service`
- `internal/orchestrator`
- `internal/adapter`
- `internal/gitwork`
- `internal/event`
- SQLite repository packages

Domain DTO imports can remain during transition, but generated proto DTOs should become the transport boundary where stability matters. Do not force a full DTO rewrite before the split works end-to-end.

## 12. What Moves From TUI to Logic Layer

### Service graph and orchestration wiring

Move from `internal/tui/views`:

- `service_manager.go`
- `Services`
- adapter event wiring
- refresh-loop startup/stop
- harness construction
- settings-triggered service rebuilds

### Domain mutation commands

Move the service/orchestrator side of command functions from `internal/tui/views/cmds.go` into daemon RPC handlers.

Representative commands:

- `ApprovePlanCmd`
- `StartPlanningCmd`
- `RestartPlanningCmd`
- `PlanWithFeedbackCmd`
- `RunImplementationCmd`
- `FinalizeWorkItemCmd`
- `RetryFailedCmd`
- `OverrideAcceptCmd`
- `FailReviewCmd`
- `FollowUpSessionCmd`
- `RetrySessionCmd`
- `SteerSessionCmd`
- `BeginForemanOrchestratedCmd`
- `EndForemanOrchestratedCmd`
- `FollowUpOrchestratedCmd`
- `AnswerQuestionCmd`
- `SkipQuestionCmd`
- `HeartbeatCmd`
- archive/unarchive/delete actions

The TUI command functions can remain as thin Bubble Tea adapters, but they call gRPC clients rather than service objects.

### Settings

`internal/tui/views/settings_service.go` should disappear.

Daemon/service side owns:

- config loading/saving
- validation and provider status derivation
- secret loading and keychain writes
- provider diagnostics
- provider login flows
- provider-specific adapter construction for diagnostics/login
- service rebuild after settings changes

TUI side keeps only:

- settings page UI state
- field focus/editing state
- rendering of settings sections/fields
- local form drafts before submit

### Autonomous new-session runtime

Move `internal/tui/views/new_session_autonomous.go` daemon-side.

The daemon owns provider watch streams, filter locks, lease renewal, deduplication, and provider capability checks. It exposes start/stop/status APIs and emits autonomous status changes over the event stream.

### Workspace and repo operations

Move filesystem/git operations daemon-side:

- workspace health checks
- managed repo scan
- worktree list/load
- clone repo
- initialize repo
- remove repo
- open/reconcile workspace identity

These operations touch local disk and repo state and belong with the logic process.

### Multi-step orchestration helpers

Move business workflows out of the TUI, including:

- delete-session/task/artifact cascades
- review follow-up address resolution
- interrupt/abort agent-session logic
- pipeline cancellation maps
- resume-in-flight maps
- durable duplicate-submission guards

The daemon owns durable in-flight state. The TUI may keep local UX-level flags such as a question overlay's `submitting` bool.

### Read-model derivation

Move product-level state derivation to daemon read models after the command/event split is stable.

Candidates:

- overview data/action cards/tasks/artifacts/activity builders
- sidebar/task grouping helpers, including virtual Source Details / Artifacts / Foreman nodes
- retry/follow-up/interrupt/action eligibility helpers
- artifact/review/CI aggregation
- live-instance actionability checks

Formatting helpers and view-only strings can stay in the TUI. Product decisions should not.

### Session log tailing

Move log reading/tailing daemon-side. The TUI should not read session log files or rotated gzip files directly after the split.

## 13. Migration Plan

Each phase keeps the system working end-to-end. No flag day.

### Phase 1 — Introduce product-shaped client interfaces in-process

Goal: make the TUI depend on a logic client interface rather than concrete services.

Steps:

1. Define interfaces matching the target product RPC shape.
2. Implement an in-process client backed by current services/orchestrators.
3. Convert command functions from direct service calls to client calls.
4. Keep behavior identical.
5. Add parity tests for critical commands where practical.

Acceptance:

- Converted TUI command code no longer requires direct service/orchestrator types.
- No process split yet.
- Existing rendered behavior remains unchanged for converted paths.

### Phase 2 — Move logic composition out of `internal/tui/views`

Goal: make the TUI package visually focused.

Steps:

1. Move `ServiceManager` and `Services` into `internal/logic` or `internal/daemon`.
2. Move settings implementation out of the TUI package.
3. Move autonomous runtime out of the TUI package.
4. Move durable in-flight/cancel orchestration state daemon-side.
5. Keep an in-process adapter for the current executable.

Acceptance:

- TUI no longer owns service graph composition.
- Daemon-like logic package can be started without Bubble Tea.
- `internal/tui/views/settings_service.go` is deleted or reduced to pure view conversion if transitional constraints require it.
- `internal/tui/views` does not import adapter packages for settings diagnostics/login.

### Phase 3 — Add event sequencing and snapshot contract

Goal: make reconnectable remote clients possible before relying on gRPC for all behavior.

Steps:

1. Add `system_events.sequence` migration.
2. Add `Sequence` to `domain.SystemEvent`.
3. Update event persistence and reads to preserve monotonic order.
4. Add snapshot APIs behind the in-process client boundary.
5. Verify startup ordering: subscribe, snapshot, replay buffered events after snapshot sequence.

Acceptance:

- Persisted events can be replayed by sequence.
- A client can miss live events, reconnect from the last processed sequence, and converge.
- Snapshot plus buffered event application has no lost-update window.

### Phase 4 — Add gRPC transport

Goal: expose the product client contract over local gRPC.

Steps:

1. Add proto definitions under `api/substrate/v1`.
2. Generate Go server/client code.
3. Implement daemon RPC handlers by calling the same logic interfaces from Phase 1.
4. Implement `EventStreamAPI.Subscribe` over persisted replay plus live `event.Bus` subscription.
5. Implement `LogAPI` streaming/snapshot APIs.
6. Add integration tests that exercise the same logic through gRPC.

Acceptance:

- Logic daemon can run without launching Bubble Tea.
- A gRPC client can get an initial snapshot, list sessions, perform a product action, subscribe to events, and tail a session log.
- Event reconnect works by sequence.
- Slow clients do not block daemon producers.

### Phase 5 — Split CLI modes

Goal: make process separation user-visible but compatible.

Steps:

1. Add `substrate daemon` / `substrate serve`.
2. Add `substrate tui`.
3. Keep `substrate` as compatibility mode: start/connect daemon, then launch TUI.
4. Add socket metadata and bearer-token management.
5. Add daemon health/info endpoints.
6. Ensure clean shutdown aborts/interrupts sessions using existing graceful shutdown semantics.

Acceptance:

- TUI process does not open SQLite.
- TUI process does not load secrets.
- TUI process does not construct adapters or harnesses.
- Quitting the TUI does not abort running agents.
- Explicit daemon shutdown runs the existing daemon-side teardown path.

### Phase 6 — Promote read models

Goal: remove product state derivation from visualization clients.

Steps:

1. Move overview derivation into `internal/logic/readmodel`.
2. Move sidebar/task tree derivation into read models.
3. Move action eligibility into read models.
4. Move artifact/review/CI aggregation into daemon read models.
5. Replace TUI-local derivation with `ReadModelAPI` calls.
6. Keep rendering in TUI.

Acceptance:

- Multiple visualization clients would show the same overview/sidebar/action availability from the same daemon read model.
- TUI rendering remains byte-identical for a fixed seeded state.

### Phase 7 — Future extensions

- Multi-workspace service graphs.
- Multi-client model with one controller and read-only watchers, if product requirements justify it.
- Electron main process uses the same daemon/socket/proto contracts.
- Typed event payloads added incrementally behind compatible fields.

## 14. Compatibility and Risk Notes

### Event payload typing

Current events use JSON strings in `domain.SystemEvent.Payload`. Do not attempt a full event schema rewrite in the first phase.

Initial gRPC event envelopes carry:

- sequence
- event ID
- workspace ID
- event type
- created at
- raw JSON payload
- optional typed payload fields only where already stable

Then migrate event by event to typed payloads if a client needs stronger typing.

### Event replay and reconnect

Use persisted events for replay and keep targeted reloads/snapshots as a safe fallback while typed event payloads mature.

The replay cursor is `sequence`, not UUID/event ID.

### Settings rebuild

Settings changes currently rebuild the service graph. In daemon mode:

- rebuild remains daemon-local
- mutating RPCs that cannot run during rebuild return `ERROR_CODE_GRAPH_REBUILDING`
- clients receive rebuild lifecycle events
- `EventServiceGraphRebuilt` is the terminal resync cue
- clients refresh runtime context/read models after that event

The server owns rebuild ordering and locking. The TUI does not coordinate rebuild internals.

### Long-running sessions

The daemon owns `orchestrator.SessionRegistry`, pipeline goroutines, and cancellation. Visualization clients should only interact through product APIs and streams.

Do not expose harness handles, goroutine handles, cancel funcs, registry internals, or adapter internals over gRPC.

### SQLite and multi-process safety

The daemon is the only SQLite writer and should be the only process opening the database in normal operation. The TUI must not open the DB after the split.

Killing the daemon must not corrupt the DB. Shutdown should stop refreshers/subprocesses, close services, close the event bus, close the gRPC server, close the database, remove socket metadata, and preserve SQLite's normal durability guarantees.

### Single-client versus multi-client

The first revision may enforce one controlling TUI client per daemon/workspace. The API should still include `workspace_id` and avoid global single-client assumptions in the wire contract so future read-only clients or multi-workspace daemons remain possible.

### Client-side terminal/browser actions

Opening terminals and browser URLs remains client-side by default because those actions should occur on the operator's visible machine. If a future remote UI exists, add explicit daemon APIs for path discovery rather than silently opening daemon-local terminals.

### Bridge package impact

`internal/adapter/bridge/*` remains daemon-owned. The TUI does not need to know about Bun subprocesses, stdio JSON harness protocols, the ACP foreman socket, or foreman-mcp. Packaging must ensure the daemon can find bridge assets from the installed binary layout.

### Schema versioning

Proto field numbers are forever. Use `reserved` ranges/names when removing fields. Keep the package under `substrate.v1` so a future incompatible contract can move to `v2`.

## 15. Top-level Acceptance Criteria

When implemented:

1. `substrate daemon` / `substrate serve` runs as a long-lived process that owns SQLite, the service graph, the event bus, adapters, orchestrators, and all agent subprocesses.
2. `substrate tui` connects to the daemon over a Unix socket, renders the full TUI, and contains no direct DB/service/orchestrator/adapter ownership.
3. `substrate` with no args starts or connects to the daemon and launches the TUI compatibly.
4. Quitting the TUI does not abort running agents.
5. Explicit daemon shutdown does not corrupt the DB and runs the existing graceful teardown semantics.
6. Settings save still hard-rebuilds the service graph and the TUI resyncs automatically after `EventServiceGraphRebuilt`.
7. Every event the daemon publishes is assigned a monotonic sequence and can be replayed by reconnecting clients.
8. The event stream has a defined slow-client policy that does not block daemon producers.
9. Mutating RPCs that can duplicate work support idempotency keys.
10. The TUI's rendered output is unchanged for fixed seeded state after each migration phase.
11. The gRPC layer has integration tests against a real daemon process or daemon server harness.
12. The API exposes product actions/read models, not repositories or current service/orchestrator structs.

## 16. References

- `docs/02-layered-architecture.md` — current layer diagram.
- `docs/03-event-system.md` — current event model.
- `docs/06-tui-design.md` — current TUI surface.
- `docs/13-electron-app-plan.md` — companion forward plan.
- `internal/event/bus.go` — current in-process bus.
- `internal/orchestrator/foreman.go` — current Foreman.
- `internal/orchestrator/session_registry.go` — current registry.
- `internal/tui/views/service_manager.go` — current graph lifecycle and rebuild ordering.
- `internal/tui/views/settings_service.go` — largest current settings/diagnostics boundary violation.

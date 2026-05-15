# Service Layer

## Transacter Pattern

The codebase uses `go-atomic` for transactional repository access. The canonical pattern is:

```go
type FooService struct {
    transacter atomic.Transacter[repository.Resources]
}

func (s *FooService) DoSomething(ctx context.Context) error {
    return s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
        // Access repos via res.Plans, res.SubPlans, res.Sessions, etc.
        return res.Plans.Create(ctx, plan)
    })
}
```

### Rules

- Services MUST hold `atomic.Transacter[repository.Resources]` directly from `go-atomic`. Do NOT create wrapper interfaces, function types, struct adapters, or shims around it.
- `repository.Resources` is the single struct (defined in `internal/repository/transacter.go`) that groups all transaction-bound repositories. Do NOT define parallel resource structs at other package levels (e.g. sqlite).
- Inside a `Transact` callback, access repos through the `res` parameter (`res.Plans`, `res.Sessions`, etc.). Do NOT close over repo fields from the service struct.
- For tests, use `repository.NoopTransacter{Res: repository.Resources{Plans: mock, SubPlans: mock}}`. Do NOT create test-specific transacter wrappers.

### Why

Previous iterations introduced wrapper types (`PlanRepoTransacter` interface, `TransactPlanReposFunc` function type, `NoopPlanTransacter` struct) that added indirection without value. Each wrapper required its own test helper and had to be threaded through constructors. The `atomic.Transacter[Resources]` interface already does the job -- use it directly.

## Service Design

- Each service owns business logic (state machines, validation, timestamps) for its domain entities.
- Services MUST NOT hold bare repository interfaces as struct fields. All repository access goes through the transacter. This ensures every operation is transaction-aware, even single-repo reads, and that the wiring surface stays uniform.
- When a service method only touches one repo, it still goes through `Transact`. The `NoopTransacter` in tests makes this zero-cost. In production the overhead is a single `BEGIN`/`COMMIT` pair, which SQLite handles in microseconds.
- Services MUST NOT import or depend on other services. Cross-service orchestration belongs in `internal/orchestrator`.

## Adding a New Repository

1. Add the interface to `internal/repository/` (e.g. `repository.FooRepository`).
2. Add a field to `repository.Resources`.
3. Add the sqlite implementation and wire it in `sqlite.ResourcesFactory`.
4. Access it in service methods via `res.Foos` inside the transact callback.

## Event Contract

### Every event payload MUST include `work_item_id` at the top level

The TUI reads `work_item_id` from the JSON `Payload` field (not from `SystemEvent.WorkspaceID`)
via `extractWorkItemID`. An empty payload causes `extractWorkItemID("")` to return `""`, which
breaks every TUI handler that relies on it. This is a hard requirement.

### Required top-level fields

| Field | Type | Description |
|---|---|---|
| `work_item_id` | `string` | The ID of the work item this event concerns. Required for all TUI-handled events. |
| `agent_session_id` | `string` | The agent session ID. Required for agent session lifecycle events. |
| `plan_id` | `string` | The plan ID. Omit if not applicable. |
| `sub_plan_id` | `string` | The sub-plan ID. Omit if not applicable. |

### Work-item state events: use `marshalWorkItemPayload`

For events that signal a work-item state transition (ingested, planning, approved, etc.), use
`marshalWorkItemPayload(item domain.Session) string`. It emits:

```json
{"work_item_id": "<item.ID>", "workspace_id": "<item.WorkspaceID>", "session": <full Session object>}
```

The TUI decoders read `work_item_id`, `workspace_id`, and `session` from this payload.
Always pass the full `Session` object — never emit only `work_item_id` alone.

### Agent session events: use `marshalAgentSessionPayload`

For events about individual agent sessions (started, completed, failed, etc.), use
`marshalAgentSessionPayload(agentSession domain.AgentSession) string` in `session.go`. It emits `work_item_id`,
`agent_session_id`, and the full nested `session` object.

### Plan/sub-plan events: use `marshalJSONOrEmpty` with named structs

Define a named payload struct with `json:"..."` tags. Do not emit raw JSON strings.

```go
// Good: named struct, omitempty for optional fields
type planEventPayload struct {
    WorkItemID string `json:"work_item_id"`
    PlanID     string `json:"plan_id,omitempty"`
    SubPlanID  string `json:"sub_plan_id,omitempty"`
}
Emit(s.eventBus, domain.SystemEvent{
    Payload: marshalJSONOrEmpty(string(domain.EventPlanApproved), planEventPayload{WorkItemID: plan.WorkItemID}),
    ...
})

// Bad: inline map with no type safety or schema
Payload: fmt.Sprintf(`{"work_item_id":"%s"}`, id),  // error-prone
```

### Payload helpers

- `marshalJSONOrEmpty(eventType string, v any)` in `plan.go` — marshals `v` to JSON; on error
  logs a warning and returns `{}`. Use this for all plan/service-layer events.
- `marshalWorkItemPayload(item domain.Session) string` in `work_item.go` — emits `work_item_id`,
  `workspace_id`, and `session`.
- `marshalAgentSessionPayload(agentSession domain.AgentSession) string` in `session.go` — emits `work_item_id`,
  `agent_session_id`, and the full nested `session` object.

### WorkspaceID vs Payload

`SystemEvent.WorkspaceID` is the workspace context for persistence/routing but is NOT read by the
TUI. Always put `work_item_id` in `Payload` so `extractWorkItemID` can extract it.

### Adding a new event

1. Choose the event type constant in `internal/domain/event.go`.
2. Pick the right helper: `marshalWorkItemPayload` for state transitions,
   `marshalAgentSessionPayload` for agent sessions, `marshalJSONOrEmpty` with a named struct for plans.
3. Marshal via the helper in the Emit call.
4. Verify the TUI has a typed message decoder and handler. If not, add them to
   `event_consumer.go` and `app.go` respectively.


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

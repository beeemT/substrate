# 07 - Implementation Plan
<!-- docs:last-integrated-commit f6b8e6e5f8374bd4c2f467852266f01cc2f323a2 -->

Phased build-out of Substrate. This file remains a roadmap, but implemented phases are rewritten to match repository HEAD instead of earlier pre-rename drafts.

## Directory Structure

```text
cmd/substrate/main.go
internal/
    domain/                # Session / Plan / Task / Review domain types
    repository/
        interfaces.go      # repository interfaces
        sqlite/            # SQLite implementations
    service/               # state machines and domain rules
    orchestrator/          # planning, implementation, review, foreman, resume, instance management
    adapter/
        linear/
        manual/
        gitlab/
        github/
        glab/
        sentry/
        ohmypi/
        claudecode/
        codex/
    app/                   # adapter + harness wiring, remote detection
    event/                 # persisted channel bus
    gitwork/               # git-work integration and workspace helpers
    config/                # YAML config + secret hydration
    tui/                   # Bubble Tea UI
bridge/omp-bridge.ts
migrations/001_initial.sql
~/.substrate/state.db
```

## Phase 0: Project Bootstrap (Week 1)

**Shipped.**

What exists today:

- typed config loading in `internal/config/`
- global path helpers (`GlobalDir`, `GlobalDBPath`, `ConfigPath`, `SessionsDir`)
- defaulting + validation for `commit`, `plan`, `review`, `harness`, `adapters`, `foreman`, and `repos`
- migration runner and `migrations/001_initial.sql`
- `cmd/substrate/main.go` startup that loads config, runs migrations, wires repos/services/bus/adapters/harnesses, and starts the TUI
- secret hydration via `config.LoadSecrets`

Current config model is richer than the original plan:

- `harness.default` plus per-phase overrides for `planning`, `implementation`, `review`, and `foreman`
- adapter config blocks for `ohmypi`, `claude_code`, `codex`, `linear`, `glab`, `gitlab`, `github`, and `sentry`
- `foreman.question_timeout`
- per-repo `repos.<name>.doc_paths`

## Phase 1: Core Domain + Persistence (Week 2)

**Shipped, with naming updated from older drafts.**

Current domain/storage split:

- root aggregate is `domain.Session`
- orchestration record is `domain.Plan`
- per-repo plan slice is `domain.TaskPlan`
- repo-scoped harness run is `domain.Task`
- review is `domain.ReviewCycle` + `domain.Critique`
- questions are `domain.Question`
- liveness is `domain.SubstrateInstance`

Storage still uses legacy table names:

- `work_items` stores `domain.Session`
- `agent_sessions` stores `domain.Task`

Current schema details worth preserving:

- `plans.faq` JSON column exists and backs the Foreman FAQ flow
- `questions.proposed_answer` exists for escalated-question UX
- `critiques.suggestion` exists
- `agent_sessions.owner_instance_id` points at `substrate_instances`
- `system_events` persists raw `domain.SystemEvent` rows

SQLite implementations live in `internal/repository/sqlite/` and accept `generic.SQLXRemote`. `resources.go` still groups transaction-bound repos into a `Resources` bundle for tests / transactional construction.

## Phase 2: Service Layer (Week 2-3)

**Shipped.**

Current services and names:

- `SessionService`
- `PlanService`
- `TaskService`
- `ReviewService`
- `QuestionService`
- `WorkspaceService`
- `InstanceService`

Important current behavior:

- `SessionService` enforces root-session uniqueness and lifecycle transitions
- `TaskService` owns task lifecycle plus `SearchHistory` / interrupted-owner queries
- `QuestionService` supports `EscalateWithProposal` and `UpdateProposal`
- `PlanService` owns sub-plan transitions and `AppendFAQ` through the repo boundary
- typed service errors (`ErrInvalidTransition`, `ErrNotFound`, `ErrInvalidInput`, `ErrConstraintViolation`) are in place

This layer is already using the renamed `Session` / `Task` model; older `WorkItemService` / `AgentSessionService` wording is obsolete.

## Phase 3: Event Bus + Adapter Interfaces (Week 3)

**Shipped, but in its current mixed form.**

Current reality:

- `domain.SystemEvent` is the persisted event shape
- `event.Bus` is a channel-based pub/sub bus with topic filtering
- `worktree.creating` is the default pre-hook event type
- pre-hooks and post-hooks are supported, with default 30s hook timeouts
- dispatch is non-blocking; full subscriber buffers yield `ErrRetryLater` unless a drop handler is installed
- work item adapters subscribe to all events and self-filter in `OnEvent`
- repo lifecycle adapters subscribe only to `worktree.created` and `work_item.completed`

Also important: not all events currently flow through the bus. Planning and part of implementation still persist some lifecycle events directly through `EventRepository.Create`.

Current adapter contracts are under `internal/adapter/interfaces.go` and use the renamed domain types (`domain.Session`, `domain.Task`, etc.).

## Phase 4: git-work Integration (Week 3-4)

**Shipped.**

Current behavior:

- workspace identity uses `.substrate-workspace`
- workspace discovery walks upward from cwd
- repo discovery looks for direct child directories containing `.bare/`
- planning preflight warns on plain git clones and failed pulls
- planning creates `<workspace>/.substrate/sessions/<planning-session-id>/plan-draft.md`
- implementation creates feature worktrees through `git-work checkout`

The workspace story is now tied to `internal/gitwork/` plus TUI workspace-init flows, not just the CLI bootstrap described in early drafts.

## Phase 6: Multi-Harness Agent Integration (Week 4-5)

**Partially shipped.**

Current production-quality path:

- `ohmypi` is the default harness and the only path with verified interactive continuation behavior across planning, implementation, review, and foreman flows.

Currently wired but still parity-limited:

- `claudecode`
- `codex`

Current router behavior in `internal/app/harness.go`:

- each phase resolves a single harness from config
- missing binaries cause that phase to be unavailable rather than silently pretending parity
- `Resume` currently reuses the implementation harness choice
- diagnostics are surfaced for settings/TUI consumption

The important naming update here is that the implementation talks about harness phases and `AgentHarness` instances, not the old single-harness assumption.

### 6a. oh-my-pi bridge (default, production path)

What is true today:

- runtime path is `internal/adapter/ohmypi/`
- package name is still `omp`
- readiness checks validate bridge availability and Bun requirements for source-bridge mode
- the bridge emits structured events consumed by the Go harness session wrapper

### 6b. Claude Code adapter (implemented, parity still limited)

- startup and selection are wired
- binary presence is checked
- do not treat it as equal to oh-my-pi for interactive continuation semantics unless verified by tests/runtime evidence

### 6c. Codex adapter (implemented, parity still limited)

- same current status as Claude Code: selectable and wired, not documented as full parity

### 6d. Harness routing, packaging, and validation

Current config shape is YAML, not TOML:

```yaml
harness:
  default: ohmypi
  phase:
    planning: ohmypi
    implementation: ohmypi
    review: ohmypi
    foreman: ohmypi
```

## Phase 7: Planning Pipeline (Week 5-6)

**Shipped.**

Current planning flow in `internal/orchestrator/planning.go`:

1. transition root `Session` from `ingested` to `planning`
2. load `Workspace`
3. run preflight (`Discoverer.PreflightCheck`)
4. pull `main/` worktrees best-effort
5. discover repos and metadata
6. read workspace-root `AGENTS.md`
7. create workspace-local planning session dir and draft path
8. render planning prompt
9. start harness planning session
10. wait for draft file or completion
11. parse/validate the draft
12. run correction loop up to `plan.max_parse_retries`
13. persist `Plan` and `TaskPlan`s
14. transition root `Session` to `plan_review`
15. persist planning events

Current naming details:

- planning is launched for a root `Session`, not a `WorkItem` type
- parsed repo slices are `TaskPlan`, not `SubPlan` in the domain model
- `PlanningContext` uses `WorkItemSnapshot` as a projection name, but it snapshots the `Session` aggregate

## Phase 8: Implementation Orchestrator (Week 6-7)

**Shipped.**

Current implementation flow in `ImplementationService`:

- requires `PlanApproved`
- loads the root `Session` and its `Workspace`
- discovers repository paths before mutating root-session state
- transitions root `Session` to `implementing`
- pre-creates unique worktrees sequentially to avoid same-wave races
- builds waves from `TaskPlan.Order`
- executes tasks in a wave concurrently via `errgroup`
- creates a durable `Task` row before launching each harness session
- forwards harness events to the bus while the task runs
- transitions the root `Session` to `reviewing` or `failed` at the end

Current event nuance:

- `worktree.creating` and `worktree.created` go through `event.Bus`
- `work_item.implementation_started` and task start/complete/fail events are still written directly through `EventRepository`

## Phase 9: Foreman + Review Pipeline (Week 7-8)

**Shipped in current form.**

### Foreman

What exists now:

- `Foreman` manages a persistent foreman-phase harness session per plan
- `StartForemanCmd` is triggered from the TUI after plan approval and during review-driven reimplementation loops
- questions are serialized through the foreman worker queue
- high-confidence answers are persisted immediately and appended to `Plan.FAQ`
- uncertain answers are escalated with `Question.ProposedAnswer`
- the TUI can keep iterating with the foreman before calling `ResolveEscalated`
- `question_timeout` is configurable through `foreman.question_timeout`

### Review

What exists now:

- review is modeled as `ReviewPipeline.ReviewSession(session domain.Task)`
- a review harness session is started in foreman mode
- output is parsed for `CRITIQUE` / `END_CRITIQUE` blocks or `NO_CRITIQUES`
- correction-loop retries reuse the same live review session
- major/critical critiques trigger reimplementation decisions via the review result path
- review outcome events are published through the bus

## Phase 9b: Resume & Recovery (Week 8)

**Shipped.**

Current behavior:

- `InstanceManager` registers the current process, maintains heartbeats, and reconciles stale owners
- stale or missing owners transition running/waiting tasks to `interrupted`
- `Resumption.ResumeSession` creates a new `Task` against the same `TaskPlan` and worktree
- the interrupted task remains interrupted for audit purposes
- resume context includes the last 50 log lines from `~/.substrate/sessions/<old-task-id>.log`
- `AgentSessionResumed` is published through the bus
- `AbandonSession` terminalizes an interrupted task as failed

One important correction versus older drafts: the runtime is based on instance-heartbeat ownership, not PID-only crash detection.

## Phase 10: Work Item Browsing and Selection (Week 8)

Still roadmap-oriented, but the terminology should be read as follows:

- browsing creates root `Session` records
- adapters resolve selections into `domain.Session`
- manual creation remains a separate explicit path

The capability-driven browse contract already exists in `internal/adapter/types.go` and `interfaces.go`; remaining work is mostly UI semantics and provider-scope parity.

## Phase 11: GitLab / GitHub Adapters and Unified Browse Semantics (Week 8-9)

Mixed state:

- GitLab, GitHub, Linear, Manual, Glab, and Sentry adapters exist
- work-item tracker mutation and repo-lifecycle automation are already split into different adapter contracts
- `internal/app/remotedetect` routes lifecycle adapters by detected provider
- browse/filter parity across all providers remains a roadmap item rather than a finished uniform contract

For Sentry specifically, the roadmap here should now be read alongside `04-adapters.md`: `07` owns the broader rollout picture, while `04` owns the shipped Sentry source-adapter contract, auth/config model, browse semantics, and settings integration details.

## Phase 12: TUI (Week 9-11)

Substantial portions are shipped.

Current TUI reality to keep in mind:

- the default sidebar is root-session / work-item centric
- history search uses `SessionHistoryEntry`
- work-item completion and plan approval publish bus events from TUI command helpers
- settings pages rebuild services and rewire adapters/harnesses dynamically
- workspace init is a TUI flow, not just a CLI-only concern

## Phase 13: End-to-End Integration (Week 11-12)

Still the umbrella outcome: provider work item -> planning -> approval -> implementation -> review -> completion, with lifecycle automation routed by repository host.

The current codebase already has e2e coverage scaffolding under `test/e2e/`, but this phase remains the overall integration target rather than a finished “nothing left to do” claim.

## Autonomous Validation Strategy

Keep the validation split, but interpret it against current repo structure:

```bash
# Unit
 go test ./...
 go test -race ./...
 go vet ./...

# Focused integration / e2e surfaces
 go test -tags=integration ./internal/gitwork/...
 go test -tags=integration ./internal/adapter/ohmypi/...
 go test -tags=integration ./internal/adapter/linear/...
 go test -tags=integration ./internal/adapter/gitlab/...
 go test -tags=integration ./internal/adapter/github/...
 go test -tags=integration ./internal/adapter/glab/...
 go test -tags=integration,e2e -timeout=30m ./test/e2e/...
```

## Risk Register

The main live risks that still match current architecture are:

- harness parity drift between oh-my-pi and the alternative harnesses
- provider browse/filter semantics diverging across adapters
- event-bus partial delivery when `ErrRetryLater` happens after some subscribers already received an event
- SQLite contention and retry behavior under concurrent writes
- bridge / CLI output format drift in external tools

## Known Gaps

Current gaps that remain accurate to call out:

- event-bus partial-delivery semantics are accepted and require idempotent consumers
- pre-hook timeouts cannot kill a misbehaving goroutine that ignores context cancellation
- Linux sandboxing for the oh-my-pi bridge remains less mature than the macOS path
- some adapter / harness parity claims are intentionally held back until real-binary verification exists

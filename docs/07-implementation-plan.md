# 07 - Implementation Plan

<!-- docs:last-integrated-commit 10e50295fb75f72c67233e191ae34fb8fc091f1e -->

Phased build-out of Substrate. Each phase builds on the last, delivering incrementally testable slices of the full system.

## Phase 0: Bootstrap

Establish the project foundation: typed configuration with validation, the migration runner, global path conventions, and the startup wiring that connects config → migrations → repositories → services → event bus → adapters and harnesses → TUI.

## Phase 1: Domain + Persistence

Define core domain types (Session, Plan, TaskPlan, Task, ReviewCycle, Critique, Question, Instance) and build the transactional service layer. All service operations run inside consistent transaction boundaries, ensuring read-after-write coherence across the persistence layer.

## Phase 2: Event Bus + Adapter Interfaces

Introduce the channel-based pub/sub event bus with topic filtering. Define adapter contracts so work-item trackers, repository lifecycle handlers, and external services can subscribe to events and react, with retry semantics and error escalation built into the bus contract.

## Phase 3: git-work Integration

Wire workspace discovery and worktree management. Substrate detects workspaces by convention, discovers repositories by layout, and runs preflight checks. Planning drafts and implementation worktrees are created under workspace-local paths.

## Phase 4: Agent Harnesses

Build the multi-harness architecture that resolves a harness per phase (planning, implementation, review, foreman) from configuration. Each harness is pluggable with binary-presence checks that make unavailable phases explicit rather than silently degraded.

## Phase 5: Planning Pipeline

Execute the full planning flow: ingest a session, run preflight, discover workspace and repos, render the planning prompt, launch the harness planning session, wait for the draft, parse and validate, run the correction loop, and persist the approved plan with its per-repo task slices.

## Phase 6: Implementation Orchestrator

Drive execution of an approved plan as ordered waves of tasks. Each wave runs concurrently; each task creates a durable record before the harness session starts, publishes lifecycle events to the bus, and transitions the root session to reviewing or failed when the wave completes.

## Phase 7: Foreman + Review

Implement persistent foreman sessions that resolve developer questions during planning and review. High-confidence answers are persisted immediately; uncertain answers are escalated to the developer. Review runs as a structured critique loop with correction retries, and major critiques trigger reimplementation decisions.

## Phase 8: Resume & Recovery

Handle interruption gracefully: detect stale or orphaned tasks on startup, let developers resume interrupted work in place, and support abandoning or superseded sessions. Graceful quit (q / ctrl+c / SIGTERM) triggers confirmation when agents are running.

## Phase 9: TUI

Build the Bubble Tea interface: sidebar with session list and filtering, plan approval, implementation and review views, session transcript rendering, log overlay with clipboard, source item browser, settings pages with live rewire, workspace init, repository add, and confirmation dialogs with quit guard.

## Phase 10: End-to-End Integration

The full provider work-item → planning → approval → implementation → review → completion pipeline. Lifecycle automation routes events by repository host, and all phases compose into a single cohesive developer experience.

## Autonomous Validation Strategy

Substrate is validated across three categories:

- **Unit tests** verify individual service logic, state machine transitions, and domain rules in isolation.
- **Integration tests** verify adapter behavior, event bus delivery, persistence transactions, and harness wiring against real dependencies.
- **End-to-end tests** verify the full pipeline from session ingestion through implementation and review, exercising all adapters and harnesses in a realistic environment.

## Risk Register

- Harness parity drift between the default bridge and alternative harnesses may cause silent degradation in non-default paths.
- Provider browse and filter semantics may diverge across adapters, creating inconsistent UX depending on which tracker is in use.
- Event-bus partial delivery occurs when some subscribers have already received an event before a retry-after condition; idempotent consumers mitigate this but it cannot be eliminated.
- SQLite under concurrent writes may exhibit contention; retry behavior must be tuned and monitored.
- External tool output format drift in bridges or CLIs can break harness session parsing without immediate failure.
- The foreman question timeout defaults to zero (documented as indefinite) but the runtime falls back to 60 seconds; this mismatch may surprise users.

## Known Gaps

- Event-bus partial-delivery semantics are accepted; consumers must be idempotent.
- There is no dead-letter store for adapter errors; best-effort TUI warnings can be dropped when the message channel is full.
- Pre-hook timeouts cannot kill a misbehaving goroutine that ignores context cancellation.
- Linux sandboxing for the oh-my-pi bridge is less mature than the macOS path.
- Some adapter and harness parity claims are intentionally held back pending real-binary runtime verification.

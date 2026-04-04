# AGENTS

## Workflow
- Always assume other agents are working on the codebase at the same time as you
- Commit your work using patches to only commit your work and not the work of other agents
- Commit semi regularly when there is a meaningful deliverable reached

## Naming
- In user-facing TUI copy and behavior, a `session` starts as soon as the user creates a work item through the New Session flow. A work item with no plan or agent run yet is still a session from the operator's perspective.
- Use `agent session` only for the child harness/planning/implementation runs underneath that user-visible session.
- Session sidebar, search, history, and related UX MUST include work items that do not yet have child agent sessions.
- In code and persistence layers, `work item` remains the canonical model name. When translating between domain terms and UX copy, preserve the user-visible meaning above so pre-planning sessions are not accidentally excluded.

## Terminology Cutovers
- When product terminology changes, search the entire subsystem for user-facing labels, internal symbol names, test names, and helper APIs using the old term, then cut them over together. A one-string rename is not done.
- Verify the current canonical product terms in the owning UI and tests before touching copy. Reject historical labels if the surface has already been renamed.

## User Corrections Override Prior Implementation
- When a user corrects runtime behavior, re-check the full action path against the user expectation. Convert any warning-only or report-only path into the required state-changing behavior if that is what the product promises.
- Trust the user's explicit correction over prior implementation, docs, or your own assumptions about intended behavior.

## Agent Orchestration
- When an agent is instructed to update a file progressively, finalize only on the agent turn's explicit completion signal. Treat intermediate file writes as provisional state unless the protocol says otherwise.
- Artifact existence is not a completion signal for interactive agent flows.

## TUI Rendering
- For any non-trivial TUI layout change, add tests that assert rendered line width stays within the requested terminal width and rendered line count stays within the requested terminal height, including narrow-size cases.

## Third-Party Integrations
- For third-party CLI integrations, verify the current install command, login/status commands, and documented auth interfaces before designing fallbacks. Prefer documented commands over private credential storage formats.
- Do not plan integrations around historical CLI naming or storage details without confirming the current binary and documented surfaces.

## Error Handling
- Errors **MUST** always be handled — never silently discard an `error` return value with `_` or an empty `if err != nil {}` body.
- Every handled error **MUST** be logged via `slog` (e.g. `slog.Error(...)`, `slog.Warn(...)`). `main.go` sets `slog.SetDefault` to a `tuilog.Handler`, which routes all `slog` entries to the TUI log screen automatically — no separate wiring is needed.
- Choose the level that matches the severity: `slog.Error` for unexpected or unrecoverable failures, `slog.Warn` for degraded-but-recoverable conditions, `slog.Debug` for transient or low-signal events. Always include the error as a structured attribute (`"error", err`).
- Preserve the error chain. Do not discard the original error when wrapping with `fmt.Errorf("%w", err)` or equivalent.

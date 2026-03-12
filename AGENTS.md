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

## TUI Rendering
- For Bubble Tea and Lip Gloss work under `internal/tui`, follow the detailed rendering rules in `internal/tui/AGENTS.md`.
- Keep the detailed TUI layout rules in that subtree-local file rather than duplicating them here.
- For any non-trivial TUI layout change, add tests that assert rendered line width stays within the requested terminal width and rendered line count stays within the requested terminal height, including narrow-size cases.

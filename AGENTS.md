# AGENTS

## Workflow
- Always assume other agents are working on the codebase at the same time as you
- Commit your work using patches to only commit your work and not the work of other agents
- Commit semi regularly when there is a meaningful deliverable reached

## TUI Rendering
- For Bubble Tea and Lip Gloss work under `internal/tui`, follow the detailed rendering rules in `internal/tui/AGENTS.md`.
- Keep the detailed TUI layout rules in that subtree-local file rather than duplicating them here.
- For any non-trivial TUI layout change, add tests that assert rendered line width stays within the requested terminal width and rendered line count stays within the requested terminal height, including narrow-size cases.

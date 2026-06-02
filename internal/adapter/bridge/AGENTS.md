# Bridge

This package provides shared infrastructure (`BridgeSession`, `BridgeRuntime`) embedded by the two concrete adapter bridges: `ohmypi` and `claudeagent`.

## Completion Signals

The bridge uses two cooperative completion signals to exit cleanly:

1. **`session.prompt()` resolving** — the happy path. The OMP SDK resolves the promise when the agent loop finishes.
2. **`agent_end` watchdog** — a defensive fallback. The OMP SDK emits `agent_end` with a `stopReason` field (`"stop" | "aborted" | ...`) as the canonical loop-ended signal. The bridge tracks `agent_end` state via `updateAgentEndWatchdog` in `session.subscribe(...)` and races it against the prompt promise. If the model emitted a terminal stop reason (`"stop"` or `"aborted"`), no retry/compaction is in flight, and `session.prompt()` has not resolved within a 2 s grace period, the watchdog fires and exits the process. This closes the post-turn SDK hang observed on resumed sessions where `session.prompt()` fails to resolve despite a clean model stop.

The watchdog is disabled in foreman mode — foreman sessions are reused across multiple prompts and must not exit after the first prompt. The `auto_retry_start` / `auto_compaction_start` events cancel the watchdog window, preventing false positives during legitimate post-turn SDK work.

See `bridge/agent-end-watchdog.ts` for the full state machine and `bridge/agent-end-watchdog.test.ts` for the test suite.

## Feature Parity

The two bridges must keep feature parity as far as the underlying backends allow.

- **Any bug fixed in one bridge MUST be investigated for the other bridge.** If the root cause applies there too, fix both in the same change.
- **Any capability added to one bridge MUST be evaluated for the other.** If the feature is applicable, implement it in both. If a genuine backend constraint prevents parity, document why in a code comment at the divergence point.
- When shared logic can be lifted into `BridgeSession` or `BridgeRuntime`, prefer that over duplicating the implementation across both adapters.

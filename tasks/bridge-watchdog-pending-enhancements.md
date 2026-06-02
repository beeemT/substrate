# Bridge Watchdog: Pending Enhancements

Two remaining edge cases from the code review of the `agent_end` watchdog (Tier 1b), plus a minor defensive fix already shipped.

---

## A — `stopReason: "length"` should compact + continue

### Problem

When the model hits `max_tokens`, the SDK emits `agent_end` with `stopReason: "length"` and exits the agent loop. The bridge currently treats this as non-terminal (watchdog doesn't fire) and `session.prompt()` resolves normally — so the bridge exits with `lifecycle.completed`.

The result: partial work committed, a review cycle discovers the incompleteness, and the orchestrator re-runs a full implementation. This wastes a review cycle and burns tokens restarting the agent from scratch, when the bridge already knows the model didn't finish.

### Proposed solution

Wrap the prompt-and-watchdog race in a bounded retry loop. When a turn ends with `stopReason: "length"`, compact (best-effort) and re-prompt to let the model finish. Cap at 3 iterations so a looping model doesn't spin forever — after max continuations, fall through to normal exit and let the orchestrator's review pipeline handle incomplete work.

```ts
const MAX_LENGTH_CONTINUATIONS = 3;
let lengthContinuations = 0;

while (true) {
  // … existing prompt + watchdog race …

  if (
    mode === "agent" &&
    outcome === "prompt-resolved" &&
    watchdogState.lastAgentStopReason === "length" &&
    lengthContinuations < MAX_LENGTH_CONTINUATIONS
  ) {
    lengthContinuations++;
    emit({ type: "lifecycle", stage: "length_continuation",
           message: `Model hit token limit; compacting and continuing (${lengthContinuations}/${MAX_LENGTH_CONTINUATIONS})` });
    try { await session.compact(); }
    catch (err) {
      if (!isBenignCompactSkip(err instanceof Error ? err.message : String(err))) {
        emit({ type: "lifecycle", stage: "length_continuation_compact_failed",
               message: String(err) });
      }
    }
    resetAgentEndWatchdog(watchdogState);
    promptText = "Your previous response hit the output token limit. " +
      "Please continue from exactly where you left off.";
    continue;
  }
  break;
}
```

### Design decisions

- **Foreman mode excluded** — foreman is single-turn Q&A; length there means the answer didn't fit, which the orchestrator handles by escalating.
- **Compact is best-effort** — `Already compacted` and similar benign skips are tolerated via the existing `isBenignCompactSkip` helper. A real compact failure logs but doesn't block the continuation attempt.
- **3 retries** — matches the SDK's default `retry.maxRetries`. After 3 length stops, the model is likely genuinely stuck and a fresh session (via orchestrator review → reimpl) is more productive than a 4th continuation with the same context.
- **Tests**: single length → continue → stop; three lengths in a row → falls through to exit; length with compact-failure → still continues.

---

## B — TTSR (Time-Travel Stream Rules) false-positive risk

### Problem

TTSR (`ttsr.enabled: true` by default) works by:

1. Detecting a TTSR rule match in streaming assistant content.
2. Calling `agent.abort()` — agent loop exits, emitting `agent_end { stopReason: "aborted" }`.
3. Async-emitting `ttsr_triggered { rules }` to subscribers.
4. Scheduling a post-prompt task (`delayMs: 50`) that calls `agent.continue()`.
5. The retry produces a new `agent_end` (typically `stopReason: "stop"`).

The current watchdog treats `"aborted"` as terminal (step 2 sets `agentEndTerminalAt`). If the TTSR retry (step 4–5) takes >2 s (model thinks, runs a tool, etc.), the watchdog fires during a legitimate pause and force-exits the bridge.

The ordering between steps 2 and 3 is non-deterministic — both are async. We cannot rely on `ttsr_triggered` arriving before `agent_end (aborted)`.

### Proposed solution

Add `ttsr_triggered` as a third "post-turn work in flight" trigger. Additionally, only clear `postTurnWorkInFlight` on non-aborted `agent_end` events (since an aborted `agent_end` during TTSR has a continuation coming):

```ts
// In updateAgentEndWatchdog:

if (e.type === "agent_end") {
  const last = /* … last assistant message … */;
  state.lastAgentStopReason = last?.stopReason;

  if (last?.stopReason === "stop" || last?.stopReason === "aborted") {
    state.agentEndTerminalAt = now;
  } else {
    state.agentEndTerminalAt = null;
  }

  // Clear post-turn work flag on any non-aborted agent_end. An aborted
  // agent_end may be a mid-TTSR-cycle interrupt with a continuation imminent
  // — keep the flag set so the watchdog stays gated until the retry's
  // agent_end arrives.
  if (last?.stopReason !== "aborted") {
    state.postTurnWorkInFlight = false;
  }
}

if (e.type === "auto_retry_start"
 || e.type === "auto_compaction_start"
 || e.type === "ttsr_triggered") {          // ← NEW
  state.postTurnWorkInFlight = true;
  state.agentEndTerminalAt = null;
}

if (e.type === "auto_retry_end" || e.type === "auto_compaction_end") {
  state.postTurnWorkInFlight = false;
}
```

### Trace verification

**Order A: `ttsr_triggered` arrives first**

| # | Event | `agentEndTerminalAt` | `postTurnWorkInFlight` | Watchdog fires? |
|---|---|---|---|---|
| 1 | `ttsr_triggered` | null | **true** | No |
| 2 | `agent_end (aborted)` | now | true (aborted doesn't clear) | No — `inFlight` gates it |
| 3 | TTSR retry runs (50ms+) | — | — | — |
| 4 | `agent_end (stop)` | now | **false** (non-aborted clears) | After 2s grace ✓ |

**Order B: `agent_end (aborted)` arrives first**

| # | Event | `agentEndTerminalAt` | `postTurnWorkInFlight` | Watchdog fires? |
|---|---|---|---|---|
| 1 | `agent_end (aborted)` | now | false (was already false) | Potentially — but within 50ms… |
| 2 | `ttsr_triggered` (within 50ms) | **null** | **true** | No — both fields cancel |
| 3 | TTSR retry runs | — | — | — |
| 4 | `agent_end (stop)` | now | **false** | After 2s grace ✓ |

In order B, there's a brief window (~50ms between steps 1 and 2) where `terminalAt` is set and `inFlight` is false. The 2 s grace period is much larger than 50ms, so the watchdog cannot fire in this window. Safe.

**User abort (no TTSR)**:

1. `agent_end (aborted)` → `terminalAt=now`, `inFlight` unchanged (stays false if it was false).
2. Bridge's `case "abort"` handler exits via `process.exit(0)` before the watchdog 2 s grace elapses.

**Standard retry**:

Unchanged — `auto_retry_start` sets `inFlight=true`, `auto_retry_end` clears it, next `agent_end (stop)` sets terminal and clears inFlight → watchdog fires after grace.

### Remaining question

TTSR has no explicit "end" event. We rely on the next non-aborted `agent_end` to clear `postTurnWorkInFlight`. If the TTSR retry itself is also aborted (cascading TTSR matches), `inFlight` stays true through multiple cycles until a non-aborted `agent_end` arrives. This is correct — the work genuinely isn't done yet. On the pathological case where the model infinite-loops on TTSR aborts, `session.prompt()` would still eventually reject or resolve (the SDK's own safeguards cap TTSR retries), and the catch/resolve branch handles that.

### Tests to add

- `ttsr_triggered` → `agent_end (aborted)` → `agent_end (stop)` after 50ms+ → fires after grace
- `agent_end (aborted)` → `ttsr_triggered` (within grace) → `agent_end (stop)` → fires correctly
- `ttsr_triggered` with two cascading aborted cycles → only fires on final `agent_end (stop)`
- Standard retry/compaction cycles still work (regression)
- Aborted `agent_end` without preceding `ttsr_triggered` → terminal timestamp set, `inFlight` unchanged

---

## C — Unhandled rejection on losing race branch (SHIPPED)

### Problem

If `session.prompt()` rejects AFTER the watchdog already won `Promise.race`, the detached `promptPromise.then(...)` becomes an unhandled rejected promise. Bun logs a warning before `process.exit(0)` runs.

### Fix

```ts
const promptPromise = session.prompt(text, { expandPromptTemplates: false });
promptPromise.catch(() => {}); // Mark as handled; race below uses separate .then() chain
```

Already applied. Tests pass. No behavioral change — purely suppresses a cosmetic warning.

---

## Implementation status

| Item | Status |
|---|---|
| C — unhandled rejection | **Shipped** |
| B — TTSR state machine | Proposed above; pending review |
| A — length → compact + continue | Proposed above; pending review |

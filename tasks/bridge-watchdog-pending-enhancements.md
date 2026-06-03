# Bridge Watchdog: Pending Enhancements

Two remaining edge cases from the code review of the `agent_end` watchdog (Tier 1b), plus a minor defensive fix already shipped.

---

## A — `stopReason: "length"` should compact + continue

### Review result

**Needs revision before implementation.** The retry-loop direction is right, but the proposed `session.compact()` call must reuse the existing redundant-compaction pre-check from the manual compact path. Current bridge code documents that `session.compact()` can preemptively disconnect/abort the agent before discovering a redundant compact; after that, a follow-up prompt may fail even when the error is a benign `Already compacted` skip. Treating the thrown message as benign is therefore too late.

Required implementation shape:

1. Before calling `session.compact()`, check `isCompactionRedundant(session.sessionManager.getBranch())`.
2. If redundant, emit the length-continuation lifecycle event with a skip message and continue without calling the SDK compact method.
3. If not redundant, call `session.compact()` best-effort and log non-benign failures, then continue anyway.
4. Add a test that proves the continuation path does **not** call `compact()` when the branch already ends in `compaction`.

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
    if (isCompactionRedundant(session.sessionManager.getBranch())) {
      emit({ type: "lifecycle", stage: "length_continuation_compact_skipped",
             message: "Already compacted — skipping" });
    } else {
      try { await session.compact(); }
      catch (err) {
        const message = err instanceof Error ? err.message : String(err);
        if (isBenignCompactSkip(message)) {
          emit({ type: "lifecycle", stage: "length_continuation_compact_skipped",
                 message });
        } else {
          emit({ type: "lifecycle", stage: "length_continuation_compact_failed",
                 message });
        }
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
- **Compact is best-effort, but must be pre-checked** — `Already compacted` and similar benign skips are tolerated via `isBenignCompactSkip`, but the implementation must first use `isCompactionRedundant(session.sessionManager.getBranch())` and skip `session.compact()` entirely when redundant. The existing manual compact path has this guard because `session.compact()` can disconnect/abort the agent before discovering the no-op.
- **3 retries** — matches the SDK's default `retry.maxRetries`. After 3 length stops, the model is likely genuinely stuck and a fresh session (via orchestrator review → reimpl) is more productive than a 4th continuation with the same context.
- **Tests**: single length → continue → stop; three lengths in a row → falls through to exit; length with non-benign compact failure → still continues; redundant branch ending in `compaction` → skips `session.compact()` and still continues.

---

## B — TTSR (Time-Travel Stream Rules) false-positive risk

### Feasibility review

**Feasible, but the bridge should not depend solely on seeing `ttsr_triggered`.** Source review confirms `ttsr_triggered` is a real `AgentSessionEvent` that reaches `session.subscribe(...)`, but its delivery is fire-and-forget after `agent.abort()`, so `agent_end (aborted)` may legitimately arrive before it.

Observed OMP path:

1. `createAgentSession` always constructs a `TtsrManager` from `settings.getGroup("ttsr")`; `ttsr.enabled` defaults to `true`, but a trigger only exists when discovered rules have a non-empty `condition`.
2. `AgentSession` subscribes to the core agent event stream and first forwards every core event to `#emitSessionEvent(displayEvent)`, which emits to extension handlers and then to `session.subscribe` listeners.
3. On `message_update` text/thinking/toolcall deltas, `AgentSession` checks `ttsrManager.checkDelta(...)` after forwarding that `message_update` event.
4. `ttsr_triggered` is emitted only for matches where `#shouldInterruptForTtsrMatch(...)` returns true. Non-interrupt/deferred TTSR injections are queued for a follow-up without emitting `ttsr_triggered`, and they do not create an aborted `agent_end` watchdog false positive.
5. On an interrupting match, it sets `#ttsrAbortPending = true`, creates `#ttsrResumePromise`, calls `agent.abort()`, then calls `#emitSessionEvent({ type: "ttsr_triggered", rules })` without awaiting it, and schedules a tracked post-prompt task with `delayMs: 50` that injects the TTSR reminder and calls `agent.continue()`.
6. The core `pi-agent-core` loop emits `agent_end` when the aborted assistant response exits the loop. Because the TTSR event is fire-and-forget after abort, core listeners do not await async session handling, and session `ttsr_triggered` fanout waits for extension handlers before bridge subscribers, subscription ordering is not a hard guarantee. Normal/no-slow-extension runs should observe `ttsr_triggered` before the aborted `agent_end`; slow extensions can let `agent_end` overtake it at the bridge.
7. `session.prompt()` waits for `#waitForPostPromptRecovery()`, which waits for `#ttsrResumePromise`, tracked post-prompt tasks, and idle streaming. So the SDK itself treats TTSR continuation as part of the same prompt lifecycle.

Conclusion: the watchdog fix is valid for the immediate-interrupt path. The robust signal is **not** “we saw `ttsr_triggered` before `agent_end`”. The robust state is “an aborted agent_end may be terminal unless a TTSR continuation signal arrives within the watchdog grace window.” Keeping the 2 s grace is essential, and tests must cover both event orders.

Implementation guidance:

- Add `ttsrContinuationInFlight` as a separate state flag, not a reuse of `postTurnWorkInFlight`.
- Set it on `ttsr_triggered` and clear `agentEndTerminalAt`.
- Do not clear it on `auto_retry_end` or `auto_compaction_end`.
- Clear it only on a non-aborted `agent_end`, because TTSR has no explicit end event and cascading TTSR aborts are still in-flight work.
- Keep plain user abort behavior unchanged: an `agent_end (aborted)` with no subsequent `ttsr_triggered` should still arm the watchdog and fire after grace if the bridge does not exit via its abort handler.

### Problem

TTSR (`ttsr.enabled: true` by default) works by:

1. Detecting a TTSR rule match in streaming assistant content.
2. Calling `agent.abort()` — agent loop exits, emitting `agent_end { stopReason: "aborted" }`.
3. Fire-and-forget emitting `ttsr_triggered { rules }` to extension handlers and then subscribers.
4. Scheduling a post-prompt task (`delayMs: 50`) that calls `agent.continue()`.
5. The retry produces a new `agent_end` (typically `stopReason: "stop"`).

The current watchdog treats `"aborted"` as terminal (step 2 sets `agentEndTerminalAt`). If the TTSR retry (step 4–5) takes >2 s (model thinks, runs a tool, etc.), the watchdog fires during a legitimate pause and force-exits the bridge.

The ordering between steps 2 and 3 is non-deterministic — both are async. We cannot rely on `ttsr_triggered` arriving before `agent_end (aborted)`.

### Proposed solution

Add `ttsr_triggered` as a separate continuation-in-flight trigger. Keep retry/compaction tracking independent so their end events cannot accidentally clear a TTSR continuation:

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

  // A non-aborted agent_end is the only explicit TTSR completion signal.
  if (last?.stopReason !== "aborted") {
    state.ttsrContinuationInFlight = false;
  }
}

if (e.type === "auto_retry_start" || e.type === "auto_compaction_start") {
  state.postTurnWorkInFlight = true;
  state.agentEndTerminalAt = null;
}

if (e.type === "auto_retry_end" || e.type === "auto_compaction_end") {
  state.postTurnWorkInFlight = false;
}

if (e.type === "ttsr_triggered") {
  state.ttsrContinuationInFlight = true;
  state.agentEndTerminalAt = null;
}

// shouldFireAgentEndWatchdog must return false while either
// postTurnWorkInFlight or ttsrContinuationInFlight is true.
```

### Trace verification

**Order A: `ttsr_triggered` arrives first**

| # | Event | `agentEndTerminalAt` | `ttsrContinuationInFlight` | Watchdog fires? |
|---|---|---|---|---|
| 1 | `ttsr_triggered` | null | **true** | No |
| 2 | `agent_end (aborted)` | now | true (aborted doesn't clear) | No — TTSR gate blocks it |
| 3 | TTSR retry runs (50ms+) | — | — | — |
| 4 | `agent_end (stop)` | now | **false** (non-aborted clears) | After 2s grace ✓ |

**Order B: `agent_end (aborted)` arrives first**

| # | Event | `agentEndTerminalAt` | `ttsrContinuationInFlight` | Watchdog fires? |
|---|---|---|---|---|
| 1 | `agent_end (aborted)` | now | false (was already false) | Potentially — but within grace… |
| 2 | `ttsr_triggered` (within grace) | **null** | **true** | No — both fields cancel |
| 3 | TTSR retry runs | — | — | — |
| 4 | `agent_end (stop)` | now | **false** | After 2s grace ✓ |

In order B, there's a brief window between steps 1 and 2 where `terminalAt` is set and the TTSR gate is false. The watchdog grace period is much larger than the SDK's scheduled `delayMs: 50`, so the watchdog cannot fire in this window. Safe.

**User abort (no TTSR)**:

1. `agent_end (aborted)` → `terminalAt=now`, `inFlight` unchanged (stays false if it was false).
2. Bridge's `case "abort"` handler exits via `process.exit(0)` before the watchdog 2 s grace elapses.

**Standard retry**:

Unchanged — `auto_retry_start` sets `inFlight=true`, `auto_retry_end` clears it, next `agent_end (stop)` sets terminal and clears inFlight → watchdog fires after grace.

### Remaining question

TTSR has no explicit "end" event. We rely on the next non-aborted `agent_end` to clear `ttsrContinuationInFlight`. If the TTSR retry itself is also aborted (cascading TTSR matches), the TTSR gate stays true through multiple cycles until a non-aborted `agent_end` arrives. This is correct — the work genuinely isn't done yet. On the pathological case where the model infinite-loops on TTSR aborts, `session.prompt()` should still eventually reject or resolve via the SDK's own safeguards, and the catch/resolve branch handles that.

### Tests to add

- `ttsr_triggered` → `agent_end (aborted)` → `agent_end (stop)` after 50ms+ → fires after grace
- `agent_end (aborted)` → `ttsr_triggered` (within grace) → `agent_end (stop)` → fires correctly
- `ttsr_triggered` with two cascading aborted cycles → only fires on final `agent_end (stop)`
- Standard retry/compaction cycles still work (regression)
- Aborted `agent_end` without preceding `ttsr_triggered` → terminal timestamp set, `inFlight` unchanged
- `ttsr_triggered` → `agent_end (aborted)` → `auto_retry_end` / `auto_compaction_end` must keep `ttsrContinuationInFlight=true`; only the final non-aborted `agent_end` clears it

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
| B — TTSR state machine | **Reviewed: revise with separate TTSR in-flight flag** |
| A — length → compact + continue | **Reviewed: revise compact pre-check before implementation** |

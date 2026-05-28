# Solution Proposal: Bulk-Retry Hang on Resumed Implementation Without Critique

## Context

This proposal addresses the runtime hang investigated in agent session
`01KSQEYNKTQ8YP91YBWCRB1ZD5` on 2026-05-28. The user clicked **Retry all**
on the action card of a failed work item (sub-plan: `frontend.paket`,
sub-plan ID `01KSN27TBBF8SNXVVQABTGKV6F`). A new implementation agent
session was created and transitioned to `running`, the omp bridge resumed
the prior conversation, but no prompt or message was ever delivered. The
session sat idle and would have failed via the 2 h `sessTimeout`.

A follow-up SendMessage from the user (manual TUI action) successfully
delivered a prompt, the agent ran one full turn (`stopReason: "stop"` at
14:28:18Z), and the session **hung again**. The bridge process did not
exit. This second hang is a different failure mode, at the bridge/SDK
layer, and is documented below as **Bug B**. Both bugs are reachable
independently and both must be addressed for the user-visible problem to
be fully fixed.

The companion fix `#3` (transition the review cycle to `failed` when the
review session crashes) is already implemented (see
`internal/orchestrator/review.go`) and ships independently; it removes the
"stuck `reviewing`" cycle that originally masked the issue but does **not**
prevent either of the hangs on its own.

## What the focused-retry plan says — and where it's incomplete

`tasks/focused-retry-with-review-loop-plan.md` claims:

> Bulk retry (`RetryFailedCmd`) works correctly because it goes through
> `ImplementationService.Implement(planID)`, which owns the full pipeline.
> The focused retry path bypasses all of that.

This investigation contradicts that claim. Bulk retry has its own
silent-no-op regression that the focused-retry plan does not address. It
manifests when `Implement()` enters `runImplementation` with a `prevImpl`
that has `ResumeInfo` and an empty `critiqueFeedback`.

The plan does describe the correct architectural rule, however, in **M5**:

> if a sub-plan is `InProgress` and the latest session for it is a completed
> implementation session with no successor review session, the next step is
> review.

M5 is currently labelled "not strictly required for the focused-retry
feature" and is deferred. I propose to **promote M5 to mandatory** and ship
a hotfix in the meantime.

## Root cause — Bug A: idle-bridge after resume without prompt

`internal/orchestrator/implementation.go::runImplementation`:

```go
hasResume   := prevSession != nil && len(prevSession.ResumeInfo) > 0
canCompact  := hasResume && s.harness.SupportsCompact()

if canCompact {
    opts.UserPrompt = ""                       // resume-only; no prompt
    opts.ResumeFromSessionID = prevSession.ID
    opts.ResumeInfo = prevSession.ResumeInfo
} else if critiqueFeedback != "" {
    opts.SystemPrompt += "\n\n" + critiqueFeedback
}

harness.StartSession(opts)                     // bridge resumes omp file
if canCompact {
    harness.Compact(ctx)                       // "already compacted — skip"
}
if canCompact && critiqueFeedback != "" {      // false in our case
    harness.SendMessage(ctx, critiqueFeedback) // ← skipped
}
harness.Wait(sessionCtx)                       // 2 h timeout
```

The resume branch is implicitly designed for the **auto-reimpl-after-review**
case, where critique feedback always exists. The branch silently no-ops in
any other path that supplies a `prevImpl` with resume info and no critique:

- Bulk retry of a failed work item whose latest completed impl had its
  review crash (the user's case).
- Bulk retry of a sub-plan where the previous review escalated and a human
  later approved the impl as-is, without recording new critiques.
- Future paths that route through `Implement()` with a completed prior
  session.

Even after fix `#3`, `loadCritiqueFeedback` still returns `""` for these
cases because the previous review's cycle is in `failed`/`passed`, neither
of which match the `critiques_found`/`reimplementing` filter. The hang
persists. Fix `#3` is necessary for cycle hygiene but not sufficient for
the bulk-retry hang.

## Root cause — Bug B: bridge does not exit after a successful turn on a resumed session

After the user manually sent a follow-up message at 14:27:23Z, the bridge
delivered the prompt to the OMP agent. The agent executed a full turn
(read files, ran tests, produced final summary) and the model stopped
generating with `stopReason: "stop"` at 14:28:18Z — confirmed in the OMP
session JSONL at
`~/.omp/agent/sessions/-git-workspaces-substrate-workspace-frontend.paket-…/2026-05-27T15-54-39-043Z_14f1d69c90f0cf42.jsonl`.

The bridge code path that should fire after a turn is:

```ts
async function runPrompt(text, inputKind) {
  ...
  await session.prompt(text, { expandPromptTemplates: false });
  // …branch on mode…
  emitLifecycle("completed", { summary: "Session complete" });
  await flushStdout();
  process.exit(0);  // ← single-use bridge contract
}
```

Observations:

- **No `lifecycle.completed` event** appears in the substrate session log
  after the assistant's stop message.
- **The bridge process is alive** (`ps -o stat`: `S+`). `sample 80471`
  shows the main thread parked in `kevent64` (idle JS event loop). All
  KQUEUE FDs report `count=0` — no pending work.
- The OMP framework log goes silent for this PID after init at 14:14:42Z
  — i.e., OMP's logger doesn't track prompt processing.

Conclusion: `await session.prompt(text)` did **not** resolve, even though
the model issued a clean stop. The bridge then sits in its event loop
forever, waiting for stdin or for the Promise to resolve.

This is a behaviour at the `@oh-my-pi/pi-coding-agent` 13.19.0 SDK layer,
likely specific to the resumed-session path
(`SessionManager.open(resumeSessionFile)`) combined with the
"compaction redundant — skipping" branch and a follow-up message. We
cannot fix the SDK from substrate, but the bridge has no observability
into the SDK's internal state machine and the orchestrator has no
inactivity-based safety net. The result is a session stuck in `running`
for the full 2 h `sessTimeout`.

The bridge contract — "process.exit after the first prompt completes" —
holds only when the SDK Promise actually resolves. There is no fallback.

## Implications for fix sequencing

The two bugs combine multiplicatively:

| Bug A patched? | Bug B patched? | What the user sees on bulk retry |
|---|---|---|
| no | no | Hang at start (no prompt). 2 h timeout. |
| **yes** | no | Bridge runs one turn, then hangs after stop. 2 h timeout. |
| no | yes | Bridge sits idle without prompt. New SDK quirks won't compound, but the silent no-op remains. |
| yes | yes | Turn runs, bridge exits, session transitions correctly. |

Therefore the originally-proposed Tier 1 (orientation message) closes only
half the gap. We need a defensive measure for Bug B in the same release.

## Proposed fix — three tiers

### Tier 1 — Hotfix for Bug A: send a continuation message when resuming without critique

**Scope:** `runImplementation` in `internal/orchestrator/implementation.go`.

Mirror the pattern already established in `resume.go::ResumeSessionWithPrompt`
(lines 132–155): when resuming a native session with no operator prompt and
no critique to feed, send a generic orientation message after `Compact` so
the bridge has something to drive a turn.

```go
if canCompact {
    if err := harnessSession.Compact(ctx); err != nil {
        slog.Warn("failed to compact resumed session, continuing without compact",
            "error", err, "agent_session_id", sessionID)
    }
}

switch {
case canCompact && critiqueFeedback != "":
    if err := harnessSession.SendMessage(ctx, critiqueFeedback); err != nil {
        slog.Warn("failed to send critique feedback to resumed session", ...)
    }
case canCompact:
    // Resumed but no critique to deliver (bulk retry, escalation reset, etc).
    // Without an explicit user turn the bridge sits idle until sessTimeout.
    msg := "You are continuing work on this sub-plan. The worktree may " +
        "contain partial changes from a previous session. Run `git status` " +
        "and `git diff` to understand current state, then continue " +
        "implementing remaining items."
    if err := harnessSession.SendMessage(ctx, msg); err != nil {
        slog.Warn("failed to send orientation to resumed session", ...)
    }
}
```

**Rationale.** Low-risk, single-function change. Closes Bug A for every
path that lands in `runImplementation`'s resume branch, not just the
specific bulk-retry shape we observed. Matches the orientation message
already in production for `Resumption.ResumeSessionWithPrompt`, so the
prompt copy is consistent.

**Tests.** Extend `TestRunImplementation_WithResumeInfo` to assert the new
message is sent when `critiqueFeedback == ""`. The existing assertion on
critique-as-message remains unchanged for the auto-reimpl path.

**Limitation.** Tier 1 alone is not enough. Once the message drives a turn,
Bug B can hang the bridge after the turn completes. Tier 1b is required in
the same release.

### Tier 1b — Hotfix for Bug B: bridge-side `agent_end` watchdog using stop reason

**Scope:** `bridge/omp-bridge.ts::runPrompt` and the existing
`session.subscribe(...)` callback. Lives entirely in the bridge; no
orchestrator changes.

The SDK already emits the canonical "agent loop is finished" signal we
need:

```ts
// from @oh-my-pi/pi-agent-core/src/types.ts
export type AgentEvent =
  | { type: "agent_start" }
  | { type: "agent_end"; messages: AgentMessage[] }
  | { type: "auto_retry_start"; ... }
  | { type: "auto_retry_end"; ... }
  | { type: "auto_compaction_start"; ... }
  | { type: "auto_compaction_end"; ... }
  | { type: "message_end"; message: AgentMessage }
  | ...
```

Each `AgentMessage` (when `role === "assistant"`) carries a
`stopReason: "stop" | "aborted" | "error" | "tool_use" | "length" | ...`
field, populated directly from the model's stop sequence. ACP mode in
`omp` already uses this exact pattern — `acp-agent.ts:350` resolves the
prompt promise on `agent_end` rather than on `session.prompt()`
resolution.

Why this is better than an inactivity timeout:

| Concern | Inactivity timeout | `agent_end` watchdog |
|---|---|---|
| Long `vitest run` / `bun build` mid-turn | False positive — kills legitimate work | Safe — `agent_end` doesn't fire mid-turn |
| Long thinking blocks | False positive — kills legitimate work | Safe — same |
| Rate-limit retry wait (model 429s) | False positive — looks like silence | Safe — `auto_retry_start` fires; we wait for next `agent_end` |
| Auto-compaction post-turn | False positive | Safe — `auto_compaction_start` cancels exit |
| Genuine post-turn SDK hang (Bug B) | Detected via long silence | **Detected via "model said stop, but `session.prompt()` didn't resolve"** |

The watchdog therefore uses the actual semantic signal: "the model
emitted a terminal stop reason and the SDK isn't doing follow-up work,
yet `session.prompt()` hasn't resolved → SDK is hung, exit anyway."

#### Implementation

1. **In the existing `session.subscribe(...)` callback**, track three
   pieces of state alongside `lastAssistantText`:
   - `agentEndTerminalAt: number | null` — timestamp when the most
     recent `agent_end` fired with a terminal stop reason
     (`"stop"` or `"aborted"`).
   - `postTurnWorkInFlight: boolean` — true between
     `auto_retry_start`/`auto_retry_end` and between
     `auto_compaction_start`/`auto_compaction_end`.
   - `lastAgentMessage: AssistantMessage | undefined` — the assistant
     message from the latest `agent_end`, used to derive the exit code.

```ts
let agentEndTerminalAt: number | null = null;
let postTurnWorkInFlight = false;
let lastAgentMessage: AssistantMessage | undefined;

session.subscribe((event: unknown) => {
  for (const mapped of mapEvent(event)) {
    emit(mapped);
  }
  const e = event as Record<string, unknown>;
  if (e.type === "message_update") {
    const assistantEvent = (e as Record<string, any>).assistantMessageEvent;
    if (assistantEvent?.type === "text_delta") {
      lastAssistantText += assistantEvent.delta;
    }
  }

  // ── new: stop-reason-aware watchdog state ──
  if (e.type === "agent_end") {
    const messages = (e as { messages: AgentMessage[] }).messages;
    const last = [...messages].reverse().find(m => m.role === "assistant") as
      AssistantMessage | undefined;
    lastAgentMessage = last;
    if (last?.stopReason === "stop" || last?.stopReason === "aborted") {
      agentEndTerminalAt = Date.now();
    } else {
      // stopReason === "error" / "tool_use" / etc. → SDK may retry or
      // continue. Reset the watchdog; we wait for the next agent_end.
      agentEndTerminalAt = null;
    }
  } else if (e.type === "auto_retry_start" || e.type === "auto_compaction_start") {
    postTurnWorkInFlight = true;
    agentEndTerminalAt = null; // SDK is doing legitimate work
  } else if (e.type === "auto_retry_end" || e.type === "auto_compaction_end") {
    postTurnWorkInFlight = false;
    // Don't set agentEndTerminalAt here — wait for the next agent_end
    // to confirm whether the work succeeded.
  }
});
```

2. **In `runPrompt`**, race `await session.prompt(text)` against a
   fallback that fires when `agent_end` was terminal and a grace period
   has elapsed without `auto_retry_start` / `auto_compaction_start`:

```ts
async function runPrompt(text: string, inputKind: "prompt" | "message"): Promise<void> {
  if (!session) {
    emitLifecycle("failed", { message: "Session not initialized" });
    return;
  }

  lastAssistantText = "";
  emitInput(inputKind, text);

  const promptPromise = session.prompt(text, { expandPromptTemplates: false });

  // Fallback: detect SDK hang via agent_end + grace period.
  const POST_TURN_GRACE_MS = 2_000;
  const fallbackPromise = (async () => {
    while (true) {
      await Bun.sleep(100);
      if (
        agentEndTerminalAt !== null &&
        !postTurnWorkInFlight &&
        Date.now() - agentEndTerminalAt >= POST_TURN_GRACE_MS
      ) {
        return "fallback" as const;
      }
    }
  })();

  let outcome: "prompt-resolved" | "fallback" = "prompt-resolved";
  try {
    const winner = await Promise.race([
      promptPromise.then(() => "prompt-resolved" as const),
      fallbackPromise,
    ]);
    outcome = winner;
  } catch (err) {
    const errorMessage = err instanceof Error ? err.message : String(err);
    emitLifecycle("failed", { message: errorMessage });
    if (mode !== "foreman") {
      await flushStdout();
      process.exit(1);
    }
    return;
  }

  if (mode === "foreman") {
    const { text: answer, uncertain } = extractConfidence(lastAssistantText);
    emit({ type: "foreman_proposed", text: answer, uncertain });
    return;
  }

  if (retryExhausted) {
    emitLifecycle("failed", { message: "Rate limit retries exhausted — session produced no work" });
    await flushStdout();
    process.exit(1);
    return;
  }

  if (outcome === "fallback") {
    // session.prompt() never resolved despite agent_end firing with a
    // terminal stop reason and no follow-up work scheduled. Treat as a
    // post-turn SDK hang and exit cleanly so substrate's Wait() returns.
    emitLifecycle("completed", {
      summary: "Session complete (forced exit after agent_end watchdog)",
      forced: true,
      stopReason: lastAgentMessage?.stopReason,
    });
  } else {
    emitLifecycle("completed", { summary: "Session complete" });
  }

  await flushStdout();
  process.exit(0);
}
```

#### Behaviour table

| Scenario | What the model emits | Watchdog state | Exit path |
|---|---|---|---|
| Happy path | `stop` → `agent_end` → `session.prompt()` resolves immediately | terminal at T, prompt resolves at T+ε | `prompt-resolved` branch, `process.exit(0)` |
| Bug B (resumed + compact-skip + SendMessage) | `stop` → `agent_end` → `session.prompt()` hangs | terminal at T, no retry/compact, hangs at T+∞ | `fallback` branch fires at T+2 s, `process.exit(0)` with `forced: true` |
| Rate-limit 429 mid-stream | `error` → `agent_end` → SDK schedules retry | `agentEndTerminalAt = null` (stopReason was `"error"`) → `auto_retry_start` → `postTurnWorkInFlight = true` → `auto_retry_end` (success) → another `agent_end` with `stop` | second `agent_end` resets terminal timestamp; happy path |
| Rate-limit retries exhausted | `error` → `agent_end` → SDK retries → max attempts | `auto_retry_end { success: false }` → SDK doesn't fire a new `agent_end`; `session.prompt()` rejects | catch branch, `process.exit(1)` |
| User abort via `harnessSession.Abort()` | `aborted` → `agent_end` | terminal at T; `Abort()` also sends `{type:"abort"}` over stdin → bridge calls `process.exit(0)` directly | bridge's `case "abort"` handler exits before watchdog matters |
| Long `bun test` (60s) mid-turn | tool_call → tool_result → still in turn, no `agent_end` yet | watchdog idle; `agentEndTerminalAt === null` | nothing fires; eventually `agent_end` arrives, normal happy path |
| Auto-compaction post-turn | `stop` → `agent_end` → `auto_compaction_start` within 2 s | terminal at T → `postTurnWorkInFlight = true` cancels watchdog → `auto_compaction_end` → next `agent_end` resets state | watchdog never fires; normal happy path |
| Long thinking turn (extended thinking, no streaming) | `thinking_delta` events but no `text_delta` for minutes | watchdog idle; no `agent_end` yet | no false positive |

#### Tests

Add a small test file `bridge/test/watchdog.test.ts` (or similar) that
exercises the watchdog logic via a mock event stream (no real SDK):

- `emits agent_end with stop, prompt resolves immediately → normal exit`
- `emits agent_end with stop, prompt never resolves → fallback exit after 2s`
- `emits agent_end with error → auto_retry_start → auto_retry_end success → agent_end with stop → fallback waits correctly`
- `emits agent_end with stop → auto_compaction_start within 1.5s → auto_compaction_end → next agent_end with stop → exit on second agent_end`
- `emits tool_execution_* events for 60s without agent_end → no fallback fires`

#### Rationale for not adding the orchestrator inactivity watchdog

Inactivity is a poor proxy. It conflates several legitimate scenarios
(long tool execution, slow thinking, rate-limit backoff) with the actual
failure mode (SDK promise stuck after a clean stop). The bridge has the
exact signal we need — `agent_end` with `stopReason: "stop"` — and the
SDK explicitly emits `auto_retry_start` / `auto_compaction_start` for the
legitimate post-turn work cases. Using the semantic signal means no false
positives and a much narrower fix.

If, in the future, we observe stalls **inside** an agent turn (no
`agent_end` ever fires despite the bridge process being alive), we can
revisit a turn-level activity timer at that point — but it would still
key on event-stream cadence, not wall-clock silence, so it can
distinguish "model thinking" from "process hung".

### Tier 2 — Cycle hygiene: review-cycle terminal-state filter (cherry-picked from M6)

**Scope:** `internal/orchestrator/review.go::ReviewSession` cycle counting.

Independently of Tiers 1/1b, the plan's M6 hardens cycle counting against
non-decision crashes (e.g. SIGKILL of the substrate process between
`CreateCycle` and `makeDecision`). Fix `#3` covers the in-process error
paths via the new defer; M6 covers the out-of-process case where no error
path runs.

This was already in scope of the focused-retry plan (M6); promote it to
ship alongside fix `#3` for completeness:

```go
terminal := 0
for _, c := range cycles {
    switch c.Status {
    case domain.ReviewCyclePassed,
         domain.ReviewCycleCritiquesFound,
         domain.ReviewCycleFailed:
        terminal++
    }
}
cycleNumber := terminal + 1
```

**Tests.** A test seeds two `Reviewing` cycles + one `CritiquesFound`,
verifies the next call uses cycle number 2 (not 4) and does not escalate.

### Tier 3 — Proper fix for Bug A: route bulk retry to review when the impl is complete

**Scope:** `internal/orchestrator/implementation.go::executeSubPlan` (and
the existing crash-recovery branch around lines 478–503).

Today there is exactly one crash-recovery rule: if the most recent session
for a sub-plan is a `review` session, retry the review on the latest
completed impl. The user's case escapes this rule because the most recent
session is an *impl* (interrupted retry from yesterday), not a review.

Generalize the rule. The right "next step" for a sub-plan with status
`InProgress` (or any non-terminal status during a retry) is determined by
its session graph:

| Session-graph state | Next step |
|---|---|
| No completed impl session | Run fresh implementation |
| Latest completed impl + no successor review (or last review failed/timed-out) | **Run review on the existing impl** |
| Latest completed impl + review with `critiques_found`/`reimplementing` | Run re-impl with critique feedback (current auto-reimpl) |
| Latest completed impl + `passed` review | Sub-plan done; should not have entered here |

The existing branch implements row 2 only when the latest session itself is
a review. Extend it to also fire when the latest session is a *failed* or
*interrupted* impl that came after a completed impl whose review never
reached a verdict. The condition becomes:

```go
prevImpl := s.latestCompletedImplSession(ctx, subPlan.ID)
if prevImpl != nil && !s.subPlanHasOutstandingCritique(ctx, subPlan.ID) {
    if !s.implHasPassedReview(ctx, prevImpl.ID) {
        // The completed impl has no successful review yet → review it.
        slog.Info("skipping implementation, reviewing existing completed impl",
            "sub_plan_id", subPlan.ID, "impl_session_id", prevImpl.ID)
        // run reviewLoop directly with prevImpl as the completed session
        ...
        return
    }
    // else: completed impl already passed review; sub-plan should not be
    // here — defensive log + fall through.
}
```

Where:

- `subPlanHasOutstandingCritique` is the existing logic from
  `loadCritiqueFeedback`: any review cycle in `critiques_found`/
  `reimplementing` for the latest completed impl.
- `implHasPassedReview` lists cycles for `prevImpl.ID` and checks for any
  `passed`.

The auto-reimpl path inside `reviewLoop` is unchanged; this only affects
the entry into `executeSubPlan`.

**Compatibility with the focused-retry plan.** This is a strict subset of
the plan's M5. M5 unifies the same rule into a new
`ContinueAfterImplSession` entry point that both bulk and focused retry
share. Tier 3 here can ship in two forms:

1. **As a localized branch in `executeSubPlan`** (smaller diff, doesn't
   require M1/M2/M5's wider refactor). This is what the user gets in the
   short term to close the hang at the architectural layer.
2. **Replaced by `ContinueAfterImplSession`** when the focused-retry plan
   lands. The localized branch is deleted in favour of the unified entry
   point. No behavioural difference; just code consolidation.

This means Tier 3 lands now, then *naturally evolves into* M5 when the
focused-retry plan is implemented — no rework, no double-implementation.

### Why not just rely on the focused-retry plan?

The focused-retry plan addresses a different surface (TUI `r` keypress on
one failed session). Its dependency chain (M1 sub-plan transitions, M2
`SubPlanEscalated` enum + migration, M3 kind preservation, M4 foreman
lifecycle, M6 cycle counting, then the new entry point) is a 7+ change
landing sequence. The bulk-retry hang exists today, in a path the plan
explicitly assumes is correct, and is reachable on every retry of a work
item whose review previously crashed. Waiting for the full plan to land
leaves users with sessions that hang for two hours and then get marked as
failed for no clear reason.

The right sequencing is:

1. **Now**: ship fix `#3` (already done) + Tier 1 hotfix + **Tier 1b
   inactivity watchdog** + Tier 2 cycle filter. ≈ 80 lines of code, six
   new tests, no schema changes. This combination closes both Bug A (no
   prompt) and Bug B (post-turn SDK hang); without Tier 1b the user still
   hits a 2 h hang after the orientation turn completes.
2. **Next**: ship Tier 3 (localized branch in `executeSubPlan`). Closes
   the architectural gap. ≈ 60 lines, two new tests.
3. **Later** (focused-retry plan): implement M1–M4 + the new
   `ContinueAfterImplSession` entry point. Replace Tier 3's localized
   branch with the unified entry. The user's request for focused retry is
   satisfied at the same time, and bulk retry trivially routes through
   the same code.

## Summary table

| # | Tier | What | When | Risk |
|---|------|------|------|------|
| 3 | (separate fix) | Fail review cycle on review-session error (defer in `ReviewSession`) | **Done** | Low |
| 1 | Tier 1 | Send orientation message in `runImplementation` resume branch when no critique | Now | Low |
| — | Tier 1b | Bridge-side `agent_end` watchdog using stop reason; force `process.exit(0)` when SDK hangs after terminal stop with no retry/compaction in flight | Now | Low (bridge-only, no orchestrator changes) |
| 2 | Tier 2 | Filter cycle counting to terminal statuses (M6 cherry-pick) | Now | Low |
|   | Tier 3 | `executeSubPlan` skips re-impl and reviews existing completed impl when no critique outstanding | Next | Medium |
|   | Long-term | Replace Tier 3 with `ContinueAfterImplSession` per focused-retry plan M5 | With focused-retry | — |

## Open questions

1. **Tier 1 message wording.** Should the orientation copy differ between
   the bulk-retry path and the focused-retry path (`Resumption`)? I'd
   argue no — the agent's situation is identical (resumed conversation,
   worktree may have partial state). Keep one canonical string in a
   shared helper.

2. **Tier 1b grace period.** 2 s is the proposed default. The SDK
   schedules retry/compaction via `setTimeout(..., 0)` so
   `auto_retry_start` / `auto_compaction_start` should fire within a
   tick or two of `agent_end`. 2 s is comfortably above that. Make it a
   bridge constant for now; if we observe legitimate post-turn work that
   takes longer to start, surface it as a config knob later.

3. **Tier 1b interaction with `auto_retry_end { success: false }`.**
   When retries are exhausted, the SDK emits `auto_retry_end` with
   `success: false` and rejects `session.prompt()`. The catch branch in
   `runPrompt` handles that and exits with code 1. The watchdog doesn't
   need special handling — `agentEndTerminalAt` stays null because the
   last `agent_end` was `error`, and the rejection wins the race.

4. **Tier 1b interaction with manual SendMessage.** If a human sends a
   follow-up message, that just produces another agent loop. Each
   `agent_end` resets the watchdog state, so a series of
   message → turn → message → turn → … works correctly. The watchdog
   only fires when `agent_end` lands and then nothing else happens.

4. **Tier 3 sub-plan status.** When `executeSubPlan` enters with sub-plan
   status `Failed` and decides to skip impl + review, should the sub-plan
   first be transitioned to `InProgress`? Today `Implement` does this for
   all `Failed`/`InProgress` sub-plans (lines 318–322 in `implementation.go`).
   Should be unchanged.

5. **Tier 3 + foreman.** The existing crash-recovery branch does not
   restart the foreman before invoking `reviewLoop`. The foreman is
   already alive at this point (started by `Implement` before the wave
   loop begins), so this is fine. Verify the same holds for the extended
   branch.

6. **Test fixture for Tier 3.** The `reviewPipelineFixture` in
   `phase9_test.go` does not currently exercise `executeSubPlan` directly.
   Tier 3 tests likely need a slimmer fixture or to extend the
   `implementation_test.go` setup.

7. **OMP SDK upstream.** The `session.prompt()` non-resolution is worth
   reporting upstream to `@oh-my-pi/pi-coding-agent` 13.19.0 maintainers
   with the reproducer (resume from existing jsonl + compact-skip + later
   SendMessage). Even if they fix it, Tier 1b stays as defense in depth.

## SDK upgrade research (13.19.0 → 15.5.10)

Investigated whether bumping `@oh-my-pi/pi-coding-agent` to current latest
fixes Bug B. Findings:

- **Latest published version**: 15.5.10 (2026-05-28T13:02Z). Distance from
  13.19.0 is ~423 versions across two major bumps.
- **No changelog entry addresses Bug B directly.** I scanned every `### Fixed`
  block from 13.19.x through 15.5.10 for terms like "session.prompt",
  "post-turn", "single-use", "resume + compact + hang". The closest hits
  (13.3.8 `waitForIdle()` / TTSR resume gate, 15.3.0 loop-mode async-job
  race) all add waiting in the opposite direction or operate on a
  different code path. The specific path "`SessionManager.open(jsonl)` +
  `compact()` returning `Already compacted — skipping` + later `SendMessage`
  → `session.prompt()` never resolves" is not called out in any release.
- **High-risk upgrade.** Two major bumps include several breaking changes
  on the bridge's exact contract surface:
  - `14.0.0` — moved to upstream `@oh-my-pi/pi-ai`; agent session and
    completion APIs changed (`submit_result` ↔ `complete` churn).
  - `14.5.0` — added `getCompactContext()` / auto-injected `complete` tool
    for subagents / prompt-template rendering changes.
  - `14.7.0` — `buildSystemPrompt` and `before_agent_start` hook now use
    `systemPrompt: string[]` blocks instead of a single string.
  - `15.0.0` — Agent Client Protocol (ACP) mode; reworked session
    lifecycle for multi-session managers.
  - `15.1.0` — extension schema typing migrated TypeBox → TSchema; `pi.zod`
    is canonical and `pi.typebox` is a Zod-backed compatibility shim.
  - `15.4.x`–`15.5.x` — multiple hashline grammar breaks and a Bun runtime
    bump that surfaces a `setTransports` import-path bug.
- The bridge uses `SessionManager.inMemory/open/create`, `createAgentSession`,
  `session.prompt`, `session.subscribe`, `session.compact()`,
  `sessionManager.getBranch()`, plus TypeBox schemas for `ask_foreman` /
  `ask` custom tools. All entry points still exist in 15.5.10, but the
  custom-tool execute signature was reordered around 14.0.x and the
  schema API path is on a deprecation track.

**Recommendation.** Don't upgrade the SDK as part of fixing the hang.

1. There's no documented fix for Bug B in any version between 13.19.0 and
   15.5.10. A fix this targeted would normally have a changelog line;
   absent one, there's no evidence the upgrade resolves it.
2. The blast radius (423 versions, two majors, breaking changes touching
   the bridge's exact API surface) is large enough that bundling it with
   the hang fix would introduce significant unrelated regression risk.
3. Tier 1b is needed regardless. The inactivity watchdog is independent of
   SDK behaviour, so even if a future SDK bump fixes Bug B, the watchdog
   is durable defence against any future regression.

**Suggested sequencing.**

1. Land Tier 1 + Tier 1b + Tier 2 now. Closes the user-visible hang
   regardless of SDK behaviour.
2. Build a minimal SDK-only reproducer (no bridge): `SessionManager.open`
   on an existing jsonl + `createAgentSession` (with compaction
   redundant) + `session.prompt("test")`. Run against 13.19.0 and 15.5.10.
   If 15.5.10 resolves cleanly, file an upstream issue documenting which
   release fixed it; if it also hangs, file the bug upstream with the
   reproducer.
3. Treat the SDK bump as a separate, scoped task. Pick a non-bleeding-edge
   minor (likely `15.4.x` or `15.5.x`), audit each breaking change against
   the bridge, and verify the custom-tool / schema / system-prompt
   interfaces still work. Don't bundle this with the hang fix.

## Referenced files

- `internal/orchestrator/implementation.go::runImplementation` (lines 675–800) — Bug A site
- `internal/orchestrator/implementation.go::executeSubPlan` (lines 478–545) — Tier 3 site
- `internal/orchestrator/review.go::ReviewSession` (lines 67–230) — fix `#3` + Tier 2
- `internal/orchestrator/resume.go::ResumeSessionWithPrompt` (lines 70–170) — orientation precedent
- `internal/adapter/bridge/session.go::Wait` (lines 119–155) — Bug B observed here as "bridge subprocess never exits"
- `bridge/omp-bridge.ts::runPrompt` (lines 260–300) — Bug B occurs here when `await session.prompt()` does not resolve
- `tasks/focused-retry-with-review-loop-plan.md` — long-term direction (M5/M6)

## Operational note for the currently-stuck session

The live session `01KSQEYNKTQ8YP91YBWCRB1ZD5` is a manifestation of Bug B
after the user's manual SendMessage drove a successful turn. The session
is in `running`, the bridge is alive but parked in `kevent64`, and no
`lifecycle.completed` will arrive. Recovery options:

1. **Abort via TUI** — fastest, leaves resume_info on the session for
   later retry. Recommended.
2. **Send another follow-up message** — drives another turn, but Bug B
   will recur after that turn too. Useful only if you want to make the
   agent commit/push remaining work first.
3. **Wait for `sessTimeout`** — 2 h after `started_at` (so ~16:13:59Z
   local +2 h ≈ 18:13:59 local, i.e. just before 18:14 CEST), the
   `sessionCtx` deadline fires, the session is marked failed via the
   context.Canceled path in `runImplementation`, and resume_info is
   stored.
4. **Kill the bridge process** (`kill 80471`) — the bridge process death
   is observed by `BridgeSession.Wait` via `startProcessReaper`. Wait
   returns `bridge subprocess exited` error, the session is failed, and
   resume_info is preserved. This is structurally equivalent to what
   Tier 1b would do automatically.

Either #1 or #4 is reasonable. If you want substrate to record the exit
reason cleanly, #1 (abort via TUI) is preferred — that path uses
`interruptSessionDurably` and persists resume info correctly.

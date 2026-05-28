/**
 * agent-end-watchdog.ts — defensive secondary completion signal for the OMP
 * bridge.
 *
 * Background. The OMP SDK's `await session.prompt(text)` is the canonical
 * "turn complete" signal, but on resumed sessions where the compaction
 * step reports "Already compacted — skipping", the SDK has been observed
 * to never resolve that promise even when the model emitted a clean
 * `stopReason: "stop"` and the agent loop ended. The bridge then sits idle
 * in its event loop until the orchestrator's sessTimeout fires (2 h),
 * leaving sub-plans stranded in `in_progress`.
 *
 * Solution. Use the SDK's `agent_end` event as a defensive secondary
 * signal. When `agent_end` fires with a terminal `stopReason`
 * (`"stop"` / `"aborted"`) and no post-turn work (auto-retry /
 * auto-compaction) is in flight, treat the turn as complete after a short
 * grace period. This is the same signal ACP mode uses to settle its own
 * prompt promise.
 *
 * Why a grace period. The SDK schedules retry/compaction asynchronously
 * (via `setTimeout(..., 0)`) AFTER emitting `agent_end` to subscribers.
 * Without the grace, we would race the SDK and force-exit during a
 * legitimate retry/compaction.
 */

/**
 * Default grace period (ms). The SDK schedules `auto_retry_start` /
 * `auto_compaction_start` via `setTimeout(..., 0)`, so they should fire
 * within a tick or two of `agent_end`. 2 s is comfortably above that.
 */
export const POST_TURN_GRACE_MS = 2_000;

/**
 * Per-prompt watchdog state. Held in a single object so callers can
 * reset/share/test it independently of any module-level globals.
 */
export interface AgentEndWatchdogState {
  /**
   * Timestamp (ms since epoch) when the most recent `agent_end` fired
   * with a terminal `stopReason` (`"stop"` / `"aborted"`). `null`
   * otherwise — including while post-turn work is in flight.
   */
  agentEndTerminalAt: number | null;

  /**
   * `true` between `auto_retry_start` / `auto_retry_end` or
   * `auto_compaction_start` / `auto_compaction_end`. The SDK is doing
   * legitimate work and the watchdog must not fire.
   */
  postTurnWorkInFlight: boolean;

  /**
   * `stopReason` from the assistant message of the most recent
   * `agent_end`. Surfaced in the forced-exit lifecycle event for
   * observability.
   */
  lastAgentStopReason: string | undefined;
}

export function newAgentEndWatchdogState(): AgentEndWatchdogState {
  return {
    agentEndTerminalAt: null,
    postTurnWorkInFlight: false,
    lastAgentStopReason: undefined,
  };
}

export function resetAgentEndWatchdog(state: AgentEndWatchdogState): void {
  state.agentEndTerminalAt = null;
  state.postTurnWorkInFlight = false;
  state.lastAgentStopReason = undefined;
}

/**
 * Update watchdog state from a session event. Pure mutation of `state`;
 * the event itself is not modified.
 *
 * Recognised event types:
 *   - `agent_end` — capture stopReason and stamp `agentEndTerminalAt` if
 *     terminal; otherwise clear it.
 *   - `auto_retry_start` / `auto_compaction_start` — set
 *     `postTurnWorkInFlight = true` and clear the terminal timestamp.
 *   - `auto_retry_end` / `auto_compaction_end` — clear
 *     `postTurnWorkInFlight`. Terminal timestamp stays cleared; we wait
 *     for the next `agent_end`.
 *
 * `now` defaults to `Date.now()` and is only used for `agent_end`. It is
 * a parameter so tests can deterministically control the clock.
 */
export function updateAgentEndWatchdog(
  state: AgentEndWatchdogState,
  event: unknown,
  now: number = Date.now(),
): void {
  if (event === null || typeof event !== "object") return;
  const e = event as Record<string, unknown>;

  if (e.type === "agent_end") {
    const messages = ((e as any).messages ?? []) as Array<{
      role?: string;
      stopReason?: string;
    }>;
    const last = [...messages].reverse().find((m) => m?.role === "assistant");
    state.lastAgentStopReason = last?.stopReason;
    if (last?.stopReason === "stop" || last?.stopReason === "aborted") {
      state.agentEndTerminalAt = now;
    } else {
      // stopReason === "error" / "toolUse" / "length" / undefined → SDK
      // may schedule a retry, continue with another tool turn, or
      // otherwise resume work. Reset the terminal timestamp; we wait for
      // the next `agent_end` to confirm whether the loop is actually
      // done.
      state.agentEndTerminalAt = null;
    }
    return;
  }

  if (e.type === "auto_retry_start" || e.type === "auto_compaction_start") {
    state.postTurnWorkInFlight = true;
    state.agentEndTerminalAt = null;
    return;
  }

  if (e.type === "auto_retry_end" || e.type === "auto_compaction_end") {
    state.postTurnWorkInFlight = false;
    return;
  }
}

/**
 * Returns true once the bridge has observed an `agent_end` with a
 * terminal stop reason, no post-turn work is in flight, and the
 * configured `graceMs` has elapsed since the terminal timestamp.
 *
 * `now` defaults to `Date.now()`; tests should pass it explicitly.
 */
export function shouldFireAgentEndWatchdog(
  state: AgentEndWatchdogState,
  now: number = Date.now(),
  graceMs: number = POST_TURN_GRACE_MS,
): boolean {
  if (state.agentEndTerminalAt === null) return false;
  if (state.postTurnWorkInFlight) return false;
  return now - state.agentEndTerminalAt >= graceMs;
}

import { describe, expect, test } from "bun:test";
import {
  type AgentEndWatchdogState,
  newAgentEndWatchdogState,
  POST_TURN_GRACE_MS,
  resetAgentEndWatchdog,
  shouldFireAgentEndWatchdog,
  updateAgentEndWatchdog,
} from "./agent-end-watchdog";

function feed(state: AgentEndWatchdogState, event: unknown, now: number): void {
  updateAgentEndWatchdog(state, event, now);
}

const T0 = 1_000_000_000_000; // arbitrary epoch ms; tests advance from here

describe("updateAgentEndWatchdog + shouldFireAgentEndWatchdog", () => {
  test("agent_end with stop sets terminal timestamp; fires after grace", () => {
    const s = newAgentEndWatchdogState();
    feed(
      s,
      {
        type: "agent_end",
        messages: [
          { role: "user", content: [] },
          { role: "assistant", content: [], stopReason: "stop" },
        ],
      },
      T0,
    );

    expect(s.agentEndTerminalAt).toBe(T0);
    expect(s.lastAgentStopReason).toBe("stop");
    expect(s.postTurnWorkInFlight).toBe(false);

    expect(shouldFireAgentEndWatchdog(s, T0)).toBe(false);
    expect(shouldFireAgentEndWatchdog(s, T0 + POST_TURN_GRACE_MS - 1)).toBe(false);
    expect(shouldFireAgentEndWatchdog(s, T0 + POST_TURN_GRACE_MS)).toBe(true);
    expect(shouldFireAgentEndWatchdog(s, T0 + POST_TURN_GRACE_MS + 5_000)).toBe(true);
  });

  test("agent_end with aborted is treated as terminal too", () => {
    const s = newAgentEndWatchdogState();
    feed(
      s,
      {
        type: "agent_end",
        messages: [{ role: "assistant", content: [], stopReason: "aborted" }],
      },
      T0,
    );

    expect(s.agentEndTerminalAt).toBe(T0);
    expect(s.lastAgentStopReason).toBe("aborted");
    expect(shouldFireAgentEndWatchdog(s, T0 + POST_TURN_GRACE_MS)).toBe(true);
  });

  test("agent_end with error does NOT set terminal timestamp", () => {
    const s = newAgentEndWatchdogState();
    feed(
      s,
      {
        type: "agent_end",
        messages: [{ role: "assistant", content: [], stopReason: "error" }],
      },
      T0,
    );

    expect(s.agentEndTerminalAt).toBeNull();
    expect(s.lastAgentStopReason).toBe("error");
    expect(shouldFireAgentEndWatchdog(s, T0 + POST_TURN_GRACE_MS)).toBe(false);
  });

  test("agent_end with toolUse / length / undefined leaves terminal cleared", () => {
    for (const stopReason of ["toolUse", "length", undefined]) {
      const s = newAgentEndWatchdogState();
      feed(
        s,
        {
          type: "agent_end",
          messages: [{ role: "assistant", content: [], stopReason }],
        },
        T0,
      );
      expect(s.agentEndTerminalAt).toBeNull();
      expect(s.lastAgentStopReason).toBe(stopReason);
      expect(shouldFireAgentEndWatchdog(s, T0 + POST_TURN_GRACE_MS)).toBe(false);
    }
  });

  test("auto_retry_start during grace window cancels exit; next agent_end resets timing", () => {
    const s = newAgentEndWatchdogState();
    // 1. First turn errors out.
    feed(
      s,
      {
        type: "agent_end",
        messages: [{ role: "assistant", content: [], stopReason: "error" }],
      },
      T0,
    );
    expect(s.agentEndTerminalAt).toBeNull();

    // 2. SDK starts retry shortly after.
    feed(s, { type: "auto_retry_start", attempt: 1, delayMs: 2_000 }, T0 + 50);
    expect(s.postTurnWorkInFlight).toBe(true);
    expect(shouldFireAgentEndWatchdog(s, T0 + 5_000)).toBe(false);

    // 3. Retry succeeds.
    feed(s, { type: "auto_retry_end", success: true }, T0 + 2_500);
    expect(s.postTurnWorkInFlight).toBe(false);
    // Must NOT fire here: we have not seen a fresh agent_end yet.
    expect(shouldFireAgentEndWatchdog(s, T0 + 2_500 + POST_TURN_GRACE_MS)).toBe(false);

    // 4. Agent loop resumes and ends cleanly.
    feed(
      s,
      {
        type: "agent_end",
        messages: [{ role: "assistant", content: [], stopReason: "stop" }],
      },
      T0 + 5_000,
    );
    expect(s.agentEndTerminalAt).toBe(T0 + 5_000);

    // 5. Watchdog fires once the new grace window elapses.
    expect(shouldFireAgentEndWatchdog(s, T0 + 5_000 + POST_TURN_GRACE_MS - 1)).toBe(false);
    expect(shouldFireAgentEndWatchdog(s, T0 + 5_000 + POST_TURN_GRACE_MS)).toBe(true);
  });

  test("auto_compaction_start cancels watchdog mid-grace; next agent_end re-arms it", () => {
    const s = newAgentEndWatchdogState();

    // 1. Clean stop.
    feed(
      s,
      {
        type: "agent_end",
        messages: [{ role: "assistant", content: [], stopReason: "stop" }],
      },
      T0,
    );
    expect(s.agentEndTerminalAt).toBe(T0);

    // 2. Compaction starts before grace elapses.
    feed(s, { type: "auto_compaction_start", reason: "threshold" }, T0 + 500);
    expect(s.postTurnWorkInFlight).toBe(true);
    expect(s.agentEndTerminalAt).toBeNull();

    // 3. Compaction finishes.
    feed(s, { type: "auto_compaction_end" }, T0 + 1_500);
    expect(s.postTurnWorkInFlight).toBe(false);
    // Must NOT fire from the original agent_end.
    expect(shouldFireAgentEndWatchdog(s, T0 + 1_500 + POST_TURN_GRACE_MS)).toBe(false);

    // 4. Next agent_end (after compaction processed the message stream).
    feed(
      s,
      {
        type: "agent_end",
        messages: [{ role: "assistant", content: [], stopReason: "stop" }],
      },
      T0 + 1_500,
    );
    expect(shouldFireAgentEndWatchdog(s, T0 + 1_500 + POST_TURN_GRACE_MS)).toBe(true);
  });

  test("tool_execution_* events do not arm the watchdog", () => {
    const s = newAgentEndWatchdogState();
    for (let i = 0; i < 30; i++) {
      feed(s, { type: "tool_execution_start", toolName: "bash" }, T0 + i * 1_000);
      feed(s, { type: "tool_execution_update", toolName: "bash" }, T0 + i * 1_000 + 100);
      feed(s, { type: "tool_execution_end", toolName: "bash" }, T0 + i * 1_000 + 200);
    }
    expect(s.agentEndTerminalAt).toBeNull();
    // Even after 60 s of tool activity with no agent_end, watchdog is silent.
    expect(shouldFireAgentEndWatchdog(s, T0 + 60_000)).toBe(false);
  });

  test("rate-limit retries exhausted: error agent_end + auto_retry_end(success=false) keeps watchdog cold", () => {
    const s = newAgentEndWatchdogState();
    feed(
      s,
      {
        type: "agent_end",
        messages: [{ role: "assistant", content: [], stopReason: "error" }],
      },
      T0,
    );
    feed(s, { type: "auto_retry_start", attempt: 1, delayMs: 1_000 }, T0 + 10);
    feed(s, { type: "auto_retry_end", success: false, finalError: "rate limit" }, T0 + 1_000);

    expect(s.agentEndTerminalAt).toBeNull();
    expect(s.postTurnWorkInFlight).toBe(false);
    // No fresh agent_end was emitted, so the watchdog stays silent.
    expect(shouldFireAgentEndWatchdog(s, T0 + 1_000 + POST_TURN_GRACE_MS)).toBe(false);
  });

  test("resetAgentEndWatchdog clears all state", () => {
    const s = newAgentEndWatchdogState();
    feed(
      s,
      {
        type: "agent_end",
        messages: [{ role: "assistant", content: [], stopReason: "stop" }],
      },
      T0,
    );
    expect(shouldFireAgentEndWatchdog(s, T0 + POST_TURN_GRACE_MS)).toBe(true);

    resetAgentEndWatchdog(s);

    expect(s.agentEndTerminalAt).toBeNull();
    expect(s.postTurnWorkInFlight).toBe(false);
    expect(s.lastAgentStopReason).toBeUndefined();
    expect(shouldFireAgentEndWatchdog(s, T0 + POST_TURN_GRACE_MS)).toBe(false);
  });

  test("agent_end with no assistant message leaves stopReason undefined and watchdog cold", () => {
    const s = newAgentEndWatchdogState();
    feed(
      s,
      {
        type: "agent_end",
        messages: [{ role: "user", content: [] }],
      },
      T0,
    );
    expect(s.agentEndTerminalAt).toBeNull();
    expect(s.lastAgentStopReason).toBeUndefined();
    expect(shouldFireAgentEndWatchdog(s, T0 + POST_TURN_GRACE_MS)).toBe(false);
  });

  test("agent_end with empty messages array leaves watchdog cold", () => {
    const s = newAgentEndWatchdogState();
    feed(s, { type: "agent_end", messages: [] }, T0);
    expect(s.agentEndTerminalAt).toBeNull();
    expect(shouldFireAgentEndWatchdog(s, T0 + POST_TURN_GRACE_MS)).toBe(false);
  });

  test("agent_end picks the LAST assistant message in the array", () => {
    const s = newAgentEndWatchdogState();
    feed(
      s,
      {
        type: "agent_end",
        messages: [
          { role: "assistant", content: [], stopReason: "error" },
          { role: "user", content: [] },
          { role: "assistant", content: [], stopReason: "stop" },
        ],
      },
      T0,
    );
    expect(s.lastAgentStopReason).toBe("stop");
    expect(s.agentEndTerminalAt).toBe(T0);
  });

  test("non-event values (null, primitives) are ignored", () => {
    const s = newAgentEndWatchdogState();
    feed(s, null, T0);
    feed(s, undefined, T0);
    feed(s, 42, T0);
    feed(s, "agent_end", T0);
    expect(s.agentEndTerminalAt).toBeNull();
    expect(s.lastAgentStopReason).toBeUndefined();
  });

  test("custom grace period overrides default", () => {
    const s = newAgentEndWatchdogState();
    feed(
      s,
      {
        type: "agent_end",
        messages: [{ role: "assistant", content: [], stopReason: "stop" }],
      },
      T0,
    );
    expect(shouldFireAgentEndWatchdog(s, T0 + 500, /* graceMs */ 1_000)).toBe(false);
    expect(shouldFireAgentEndWatchdog(s, T0 + 1_000, /* graceMs */ 1_000)).toBe(true);
  });
});

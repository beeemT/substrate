// fake-bridge.ts is a deterministic test fixture that speaks the same
// JSON-line protocol as omp-bridge.ts. It is controlled by environment
// variables so Go integration tests can exercise each code path.
//
// Environment variables:
//
//	FAKE_BRIDGE_MODE=echo_init — emit a session_meta envelope echoing
//	   the init message fields and SUBSTRATE_* env vars, then wait for
//	   the next message. This lets harness wiring tests assert that
//	   system_prompt, answer_timeout_ms, question_tool_policy, model,
//	   and SUBSTRATE_* variables reach the bridge process.
//	FAKE_BRIDGE_SESSION_ID=<id> — if set, include session_id in emitted
//	   session_meta/echo data so adapter-specific resume metadata can be
//	   asserted without provider-specific fixture modes.
//	FAKE_BRIDGE_ECHO_PATH=<path> — if set (with or without echo_init),
//	   write the captured init+env JSON object to this file path. The
//	   Go test sets it via t.Setenv so the child process inherits it.
//	ERROR_MODE=malformed      — emit one malformed (non-JSON) line to
//	   stdout before exiting with code 0.
//	ERROR_MODE=nonzero_exit   — exit immediately with code 1, emitting
//	   nothing to stdout.
//	(default)                 — normal behaviour: respond to each
//	   protocol message deterministically.
//
// Protocol:
//
//	stdin messages (JSON lines):
//	  {"type":"init", ...}              → ack / echo
//	  {"type":"prompt","text":"..."}    → input + assistant_output + lifecycle completed
//	  {"type":"message","text":"..."}   → assistant_output + lifecycle completed
//	  {"type":"answer","text":"..."}    → lifecycle answer_received
//	  {"type":"steer","text":"..."}     → assistant_output + lifecycle completed
//	  {"type":"compact","text":""}      → lifecycle compaction_start + compaction_end
//	  {"type":"abort"}                  → process.exit(0)
//
//	stdout envelopes:
//	  {"type":"event","event":{...}}    — standard event envelope
//	  {"type":"session_meta", ...}      — init echo (echo_init mode only)

import * as readline from "readline/promises";
import { writeFileSync } from "fs";
import { stdin as input, stdout as output } from "process";

// ---------------------------------------------------------------------------
// Error modes — checked once at startup.
// ---------------------------------------------------------------------------

if (process.env.ERROR_MODE === "nonzero_exit") {
  process.exit(1);
}

if (process.env.ERROR_MODE === "malformed") {
  output.write("this is not valid json\n");
  process.exit(0);
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function emit(event: Record<string, unknown>): void {
  output.write(JSON.stringify({ type: "event", event }) + "\n");
}

// ---------------------------------------------------------------------------
// Main loop
// ---------------------------------------------------------------------------

const echoInit = process.env.FAKE_BRIDGE_MODE === "echo_init";

const rl = readline.createInterface({ input, crlfDelay: Infinity });

for await (const raw of rl) {
  const line = raw.trim();
  if (!line) continue;

  let msg: { type?: string; text?: string };
  try {
    msg = JSON.parse(line);
  } catch {
    continue; // skip unparseable input
  }

  switch (msg.type) {
    case "init": {
      // Always collect init fields and SUBSTRATE_* env vars.
      const parsed = JSON.parse(line);
      const env: Record<string, string | undefined> = {};
      for (const [k, v] of Object.entries(process.env)) {
        if (k.startsWith("SUBSTRATE_")) env[k] = v;
      }
      const echoData = { init: parsed, env, session_id: process.env.FAKE_BRIDGE_SESSION_ID };

      // echo_init mode: also emit a session_meta envelope to stdout.
      if (echoInit) {
        output.write(
          JSON.stringify({ type: "session_meta", ...echoData }) + "\n",
        );
      }

      // FAKE_BRIDGE_ECHO_PATH: write init+env JSON to a file so the
      // Go harness test can read it back after StartSession returns.
      const echoPath = process.env.FAKE_BRIDGE_ECHO_PATH;
      if (echoPath) {
        writeFileSync(echoPath, JSON.stringify(echoData, null, 2));
      }

      break;
    }

    case "prompt": {
      emit({ type: "input", input_kind: "prompt", text: msg.text ?? "" });
      emit({
        type: "assistant_output",
        text: `echo: ${msg.text ?? ""}`,
      });
      emit({ type: "lifecycle", stage: "completed", summary: "done" });
      break;
    }

    case "message": {
      emit({
        type: "assistant_output",
        text: `followup: ${msg.text ?? ""}`,
      });
      emit({ type: "lifecycle", stage: "completed", summary: "done" });
      break;
    }

    case "answer": {
      emit({
        type: "lifecycle",
        stage: "completed",
        summary: "answer_received",
      });
      break;
    }

    case "steer": {
      emit({
        type: "assistant_output",
        text: `steer: ${msg.text ?? ""}`,
      });
      emit({ type: "lifecycle", stage: "completed", summary: "done" });
      break;
    }

    case "compact": {
      emit({
        type: "lifecycle",
        stage: "compaction_start",
        message: "compacting",
      });
      emit({
        type: "lifecycle",
        stage: "compaction_end",
        message: "compacted",
      });
      break;
    }

    case "abort": {
      rl.close();
      process.exit(0);
    }
  }
}

// Stdin closed (pipe EOF) — exit cleanly.
process.exit(0);

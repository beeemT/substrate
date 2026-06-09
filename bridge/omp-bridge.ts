/**
 * omp-bridge.ts - Bridge between Substrate (Go) and oh-my-pi agent (Bun)
 *
 * JSON-line protocol over stdio:
 *
 * Go → Bun (stdin):
 *   - {"type":"init","system_prompt":"..."} — optional init message (must be first if present)
 *   - {"type":"prompt","text":"..."} — initial prompt or continuation
 *   - {"type":"message","text":"..."} — follow-up message (human iteration)
 *   - {"type":"answer","text":"..."} — resolve pending question tool call
 *   - {"type":"abort"} — terminate session
 *   - {"type":"compact"} — request manual context compaction
 *
 * Bun → Go (stdout):
 *   - {"type":"event","event":{...}} — canonical session transcript entry
 */

import type { ThinkingLevel } from "@oh-my-pi/pi-agent-core";
import type { AgentSession } from "@oh-my-pi/pi-coding-agent";
import { Type } from "@sinclair/typebox";
import { createInterface } from "readline";
import {
  type AgentEndWatchdogState,
  newAgentEndWatchdogState,
  resetAgentEndWatchdog,
  shouldFireAgentEndWatchdog,
  updateAgentEndWatchdog,
} from "./agent-end-watchdog";
import { isBenignCompactSkip, isCompactionRedundant } from "./compact-helpers";

const mode = process.env.SUBSTRATE_BRIDGE_MODE ?? "agent";
const thinkingLevel: ThinkingLevel | undefined = process.env.SUBSTRATE_THINKING_LEVEL as
  | ThinkingLevel
  | undefined;
const worktreePath = process.env.SUBSTRATE_WORKTREE_PATH ?? process.cwd();
let systemPrompt: string | undefined;
let modelPattern: string | undefined;
let questionToolPolicy = "";

// Question tool routing is driven by a policy string from the Go harness
// ("" = default, "foreman", "human", "none"). The harness default for OMP is
// "both" — historically the bridge exposed both ask_foreman and the native
// user question tool when no policy was provided.
type QuestionToolTarget = "none" | "foreman" | "human" | "both";
const DEFAULT_QUESTION_TOOL_TARGET: QuestionToolTarget = "both";

function questionToolTarget(policy: string): QuestionToolTarget {
  switch (policy) {
    case "foreman":
      return "foreman";
    case "human":
      return "human";
    case "none":
      return "none";
    default:
      return DEFAULT_QUESTION_TOOL_TARGET;
  }
}

function exposesForemanQuestions(target: QuestionToolTarget): boolean {
  return target === "foreman" || target === "both";
}

function exposesHumanQuestions(target: QuestionToolTarget): boolean {
  return target === "human" || target === "both";
}

const agentToolNames =
  mode === "agent" ? ["read", "grep", "find", "edit", "write", "bash"] : ["read", "grep", "find"];

let pendingStructuredAnswerResolve: ((answer: any) => void) | null = null;
let pendingAnswerResolve: ((text: string) => void) | null = null;
let lastAssistantText = "";
let answerTimeoutMs = 10 * 60 * 1000; // default 10 min; 0 = no timeout
let session: AgentSession | null = null;
let activePromptRun: Promise<void> | null = null;
// Set when the SDK exhausts all rate-limit retries (auto_retry_end with
// success: false). prompt() resolves without throwing in this case, so the
// bridge must check this flag before declaring the session successful.
let retryExhausted = false;

// ── agent_end watchdog state ──
//
// See agent-end-watchdog.ts for the full rationale. Briefly: `agent_end`
// is the canonical "agent loop ended" signal; we use it as a defensive
// secondary completion trigger so the bridge can exit cleanly even when
// `await session.prompt()` never resolves (the post-turn SDK hang we
// observed on resumed sessions with the "Already compacted — skipping"
// path).
const watchdogState: AgentEndWatchdogState = newAgentEndWatchdogState();

function emit(event: object): void {
  process.stdout.write(`${JSON.stringify({ type: "event", event })}\n`);
}

async function flushStdout(): Promise<void> {
  if (process.stdout.writableNeedDrain) {
    await new Promise<void>((resolve) => process.stdout.once("drain", resolve));
  }
}

function emitLifecycle(
  stage: "started" | "completed" | "failed",
  payload: Record<string, unknown> = {},
): void {
  emit({ type: "lifecycle", stage, ...payload });
}

function emitInput(
  inputKind: "session_context" | "prompt" | "message" | "answer",
  text: string,
): void {
  if (text.trim() === "") return;
  emit({ type: "input", input_kind: inputKind, text });
}

function extractConfidence(text: string): { text: string; uncertain: boolean } {
  const lines = text.split("\n");
  const last = lines[lines.length - 1].trim();
  if (last === "CONFIDENCE: high") {
    return { text: lines.slice(0, -1).join("\n").trimEnd(), uncertain: false };
  }
  if (last === "CONFIDENCE: uncertain") {
    return { text: lines.slice(0, -1).join("\n").trimEnd(), uncertain: true };
  }
  return { text, uncertain: true };
}

function safeJson(value: unknown): string {
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return String(value);
  }
}

function truncateText(text: string, max = 8_000): string {
  if (text.length <= max) return text;
  return `${text.slice(0, max)}\n...[truncated ${text.length - max} chars]`;
}

function parseAnswerMessage(msg: Record<string, unknown>): any {
  if (typeof msg.answer === "object" && msg.answer !== null) return msg.answer;
  const raw = typeof msg.text === "string" ? msg.text : "";
  try {
    const parsed = JSON.parse(raw);
    if (parsed && typeof parsed === "object") return parsed;
  } catch {
    // plain text answer
  }
  return { text: raw };
}

function toOMPAskResult(
  answer: any,
  questionMetaByID: Map<string, { question: string; multi: boolean }> = new Map(),
): any {
  const structured = Array.isArray(answer?.structured_answers) ? answer.structured_answers : [];
  const results = structured.map((a: any) => {
    const id = String(a.question_id ?? "");
    const meta = questionMetaByID.get(id);
    const selectedOptions = Array.isArray(a.selected_options) ? a.selected_options.map(String) : [];
    return {
      id,
      question: String(a.question ?? meta?.question ?? ""),
      options: selectedOptions,
      multi: meta?.multi ?? false,
      selectedOptions,
      ...(a.custom_answer ? { customInput: String(a.custom_answer) } : {}),
    };
  });
  if (results.length > 0) {
    return { results };
  }
  return {
    results: [
      {
        id: "",
        question: "",
        options: [],
        multi: false,
        selectedOptions: [],
        customInput: String(answer?.text ?? ""),
      },
    ],
  };
}

function extractTextPayload(value: unknown): string {
  if (value === null || value === undefined) return "";
  if (typeof value === "string") return value;
  if (typeof value === "number" || typeof value === "boolean") return String(value);
  if (Array.isArray(value)) {
    return value.map(extractTextPayload).filter(Boolean).join("\n");
  }
  if (typeof value === "object") {
    const record = value as Record<string, unknown>;
    if (typeof record.text === "string") {
      return record.text;
    }
    if (Array.isArray(record.content)) {
      const text = record.content
        .map((block: any) => {
          if (block?.type === "text") return String(block.text ?? "");
          if (block?.type === "image") return "[image]";
          return safeJson(block);
        })
        .join("");
      if (text.trim() !== "") return text;
    }
    if (record.details !== undefined) {
      return safeJson(record.details);
    }
    return safeJson(record);
  }
  return String(value);
}

function mapEvent(e: unknown): object[] {
  const event = e as Record<string, unknown>;
  if (event.type === "message_update") {
    const assistantEvent = (event as Record<string, any>).assistantMessageEvent;
    // Emit one entry per complete block using the *_end events, which carry the
    // full content string. Individual *_delta events are ignored to avoid
    // flooding the session log with per-token entries.
    if (assistantEvent?.type === "text_end") {
      const content = String(assistantEvent.content ?? "");
      if (content.trim()) {
        return [{ type: "assistant_output", text: content }];
      }
    } else if (assistantEvent?.type === "thinking_end") {
      const content = String(assistantEvent.content ?? "");
      if (content.trim()) {
        return [{ type: "thinking_output", text: content }];
      }
    }
    return [];
  }

  if (event.type === "tool_execution_start") {
    const toolName = String((event as Record<string, any>).toolName ?? "tool");
    const args = truncateText(safeJson((event as Record<string, any>).args ?? {}));
    const intent =
      typeof (event as Record<string, any>).intent === "string"
        ? (event as Record<string, any>).intent
        : undefined;
    return [{ type: "tool_start", tool: toolName, text: args, intent }];
  }

  if (event.type === "tool_execution_update") {
    const toolName = String((event as Record<string, any>).toolName ?? "tool");
    const text = truncateText(extractTextPayload((event as Record<string, any>).partialResult));
    if (text.trim() === "") return [];
    return [{ type: "tool_output", tool: toolName, text }];
  }

  if (event.type === "tool_execution_end") {
    const toolName = String((event as Record<string, any>).toolName ?? "tool");
    const text = truncateText(extractTextPayload((event as Record<string, any>).result));
    const isError = Boolean((event as Record<string, any>).isError);
    return [{ type: "tool_result", tool: toolName, text, is_error: isError }];
  }

  if (event.type === "auto_retry_start") {
    const attempt = Number((event as any).attempt ?? 1);
    const maxAttempts = Number((event as any).maxAttempts ?? 0);
    const delayMs = Number((event as any).delayMs ?? 0);
    const delaySec = Math.round(delayMs / 1000);
    const attemptStr = maxAttempts > 0 ? `${attempt}/${maxAttempts}` : String(attempt);
    const msg = `Rate limited — retrying in ${delaySec}s (attempt ${attemptStr})`;
    return [{ type: "lifecycle", stage: "retry_wait", message: msg }];
  }

  if (event.type === "auto_retry_end") {
    if (!(event as any).success) {
      retryExhausted = true;
      const msg = "Rate limit retries exhausted — session produced no work";
      return [{ type: "lifecycle", stage: "retry_exhausted", message: msg }];
    }
    retryExhausted = false; // Successful retry resets the flag.
    return [{ type: "lifecycle", stage: "retry_resumed" }];
  }

  if (event.type === "auto_compaction_start") {
    const reason = String((event as any).reason ?? "threshold");
    const msg = reason === "overflow" ? "Context overflow — compacting…" : "Compacting context…";
    return [{ type: "lifecycle", stage: "compaction_start", message: msg }];
  }

  if (event.type === "auto_compaction_end") {
    // Skipped (no candidates) and aborted (session ended mid-compaction) are benign — no visible event.
    if ((event as any).skipped || (event as any).aborted) return [];
    const errorMessage = (event as any).errorMessage;
    if (errorMessage) {
      // "Already compacted" / "Nothing to compact" surface here as errors but are
      // semantically successful no-ops — same treatment as the manual-compact path.
      if (isBenignCompactSkip(String(errorMessage))) {
        return [{ type: "lifecycle", stage: "compaction_end", message: String(errorMessage) }];
      }
      return [{ type: "lifecycle", stage: "compaction_failed", message: String(errorMessage) }];
    }
    return [{ type: "lifecycle", stage: "compaction_end" }];
  }

  return [];
}

async function runPrompt(text: string, inputKind: "prompt" | "message"): Promise<void> {
  if (!session) {
    emitLifecycle("failed", { message: "Session not initialized" });
    return;
  }

  // Reset per-turn state: the watchdog timestamp from a previous prompt must
  // not survive into the next one (foreman mode reuses the bridge across
  // multiple prompts).
  resetAgentEndWatchdog(watchdogState);
  lastAssistantText = "";
  emitInput(inputKind, text);

  const promptPromise = session.prompt(text, { expandPromptTemplates: false });
  // Defensive: if the watchdog wins the race below and `session.prompt()`
  // later rejects (e.g. SDK aborts mid-stream after the watchdog already
  // fired), Bun would log an unhandled rejection on the now-detached promise.
  // Attach a no-op catch to mark it as handled. Promise.race below uses a
  // separate `.then(...)` chain that still propagates the rejection to the
  // main catch block when the prompt-resolved branch wins.
  promptPromise.catch(() => {});

  // The agent_end watchdog is a defensive measure for the post-turn SDK hang
  // observed on resumed agent sessions (SessionManager.open + "Already
  // compacted — skipping"). Foreman runs in-memory (SessionManager.inMemory)
  // and does not hit that code path. Skipping the watchdog in foreman mode
  // also avoids a false-positive failure mode: if the watchdog fired during
  // foreman, runPrompt would return while session.prompt() is still pending,
  // leaving the SDK in `isStreaming=true`. The next prompt would then throw
  // AgentBusyError and the foreman would be permanently broken.
  const watchdogEnabled = mode === "agent";

  // Watchdog: poll every 100 ms for a fired-but-not-resolved post-turn state.
  // The poll loop self-terminates when the prompt resolves (controller.abort)
  // or when shouldFireAgentEndWatchdog() returns true.
  const watchdogController = new AbortController();
  const watchdogPromise = (async (): Promise<"watchdog"> => {
    if (!watchdogEnabled) {
      // Foreman path: never fire. Pend forever so Promise.race ignores us.
      return new Promise<"watchdog">(() => {});
    }
    while (!watchdogController.signal.aborted) {
      await new Promise((resolve) => setTimeout(resolve, 100));
      if (watchdogController.signal.aborted) break;
      if (shouldFireAgentEndWatchdog(watchdogState)) {
        return "watchdog";
      }
    }
    // Aborted by the prompt-resolved branch: pend forever so Promise.race
    // doesn't pick the abort path. The losing branch is discarded.
    return new Promise<"watchdog">(() => {});
  })();

  let outcome: "prompt-resolved" | "watchdog" = "prompt-resolved";
  try {
    outcome = await Promise.race([
      promptPromise.then(() => "prompt-resolved" as const),
      watchdogPromise,
    ]);
  } catch (err) {
    watchdogController.abort();
    const errorMessage = err instanceof Error ? err.message : String(err);
    emitLifecycle("failed", { message: errorMessage });
    if (mode !== "foreman") {
      // Agent sessions are single-use: exit so BridgeSession.Wait() can return.
      // Without this the process returns to runLineLoop and blocks on stdin,
      // leaving the sub-plan stranded in_progress until sessTimeout fires.
      await flushStdout();
      process.exit(1);
    }
    return;
  } finally {
    watchdogController.abort();
  }

  if (mode === "foreman") {
    const { text: answer, uncertain } = extractConfidence(lastAssistantText);
    emit({ type: "foreman_proposed", text: answer, uncertain });
    return;
  }

  if (retryExhausted) {
    emitLifecycle("failed", {
      message: "Rate limit retries exhausted — session produced no work",
    });
    await flushStdout();
    process.exit(1);
    return;
  }

  if (outcome === "watchdog") {
    // session.prompt() never resolved despite agent_end firing with a
    // terminal stop reason and no post-turn work scheduled. Treat as a
    // post-turn SDK hang and exit cleanly so substrate's Wait() returns.
    emitLifecycle("completed", {
      summary: "Session complete (forced exit after agent_end watchdog)",
      forced: true,
      stop_reason: watchdogState.lastAgentStopReason ?? "unknown",
    });
  } else {
    emitLifecycle("completed", { summary: "Session complete" });
  }
  // Agent sessions are single-use: exit so BridgeSession.Wait() can return.
  // Without this the process waits for more stdin and Wait() hangs until
  // sessTimeout fires, leaving the sub-plan stranded in_progress.
  await flushStdout();
  process.exit(0);
}

function schedulePrompt(text: string, inputKind: "prompt" | "message"): void {
  const previous = activePromptRun;
  const run = async () => {
    if (previous) {
      await previous;
    }
    await runPrompt(text, inputKind);
  };

  const scheduled = run()
    .catch(async (err: unknown) => {
      const errorMessage = err instanceof Error ? err.message : String(err);
      emitLifecycle("failed", { message: errorMessage });
      if (mode !== "foreman") {
        await flushStdout();
        process.exit(1);
      }
    })
    .finally(() => {
      if (activePromptRun === scheduled) {
        activePromptRun = null;
      }
    });
  activePromptRun = scheduled;
}

function createAskForemanTool(): any {
  return {
    name: "ask_foreman",
    description: "Ask the foreman a question you cannot resolve from the plan or codebase.",
    parameters: {
      type: "object",
      properties: {
        question: {
          type: "string",
          description: "The question to ask the foreman",
        },
        context: {
          type: "string",
          description: "Surrounding context from your work (optional)",
        },
      },
      required: ["question"],
    },
    execute: async (_toolCallId: string, args: Record<string, unknown>) => {
      const question = String(args.question ?? "");
      const context = String(args.context ?? "");
      emit({ type: "question", question, context });
      const answer = await new Promise<string>((resolve) => {
        pendingAnswerResolve = resolve;
        if (answerTimeoutMs > 0) {
          setTimeout(() => {
            if (pendingAnswerResolve === resolve) {
              pendingAnswerResolve = null;
              resolve("[No answer received within timeout. Proceed with your best judgment.]");
            }
          }, answerTimeoutMs);
        }
      });
      return answer;
    },
  };
}

const optionItemSchema = Type.Object({
  label: Type.String(),
});

const questionItemSchema = Type.Object({
  id: Type.String(),
  question: Type.String(),
  options: Type.Array(optionItemSchema),
  multi: Type.Optional(Type.Boolean()),
  recommended: Type.Optional(Type.Number()),
});

const askSchema = Type.Object({
  questions: Type.Array(questionItemSchema, { minItems: 1 }),
});

function createNativeAskTool(): any {
  return {
    name: "ask",
    label: "Ask",
    description: "Ask the user one or more structured questions and wait for answers.",
    parameters: askSchema,
    async execute(_toolCallId: string, params: any) {
      const questions = Array.isArray(params?.questions) ? params.questions : [];
      const questionMetaByID = new Map<string, { question: string; multi: boolean }>();
      const structured = {
        questions: questions.map((q: any) => {
          const id = String(q.id ?? "");
          const question = String(q.question ?? "");
          const multi = Boolean(q.multi);
          questionMetaByID.set(id, { question, multi });
          return {
            id,
            question,
            options: Array.isArray(q.options)
              ? q.options.map((o: any) => ({ label: String(o?.label ?? "") }))
              : [],
            multi_select: multi,
            recommended_index: typeof q.recommended === "number" ? q.recommended : undefined,
          };
        }),
        supports_custom_answer: true,
        supports_annotations: false,
        native_response_format: "omp_ask",
      };
      emit({
        type: "question",
        question: questions[0]?.question ? String(questions[0].question) : "Structured question",
        context: "",
        source: "omp_ask",
        structured,
      });
      return await new Promise<any>((resolve) => {
        const resolveStructuredAnswer = (answer: any) =>
          resolve(toOMPAskResult(answer, questionMetaByID));
        pendingStructuredAnswerResolve = resolveStructuredAnswer;
        if (answerTimeoutMs > 0) {
          setTimeout(() => {
            if (pendingStructuredAnswerResolve === resolveStructuredAnswer) {
              pendingStructuredAnswerResolve = null;
              resolve(
                toOMPAskResult(
                  { text: "[No answer received within timeout. Proceed with your best judgment.]" },
                  questionMetaByID,
                ),
              );
            }
          }, answerTimeoutMs);
        }
      });
    },
  };
}

async function initSession(): Promise<void> {
  const { createAgentSession, SessionManager, Settings } = await import(
    "@oh-my-pi/pi-coding-agent"
  );

  const resumeSessionFile = process.env.SUBSTRATE_RESUME_SESSION_FILE ?? "";
  const sessionManager =
    mode === "foreman"
      ? SessionManager.inMemory()
      : resumeSessionFile
        ? await SessionManager.open(resumeSessionFile)
        : SessionManager.create(worktreePath);
  const questionTarget = questionToolTarget(questionToolPolicy);
  const customTools =
    mode === "agent"
      ? [
          ...(exposesForemanQuestions(questionTarget) ? [createAskForemanTool()] : []),
          ...(exposesHumanQuestions(questionTarget) ? [createNativeAskTool()] : []),
        ]
      : [];

  const sessionOpts: NonNullable<Parameters<typeof createAgentSession>[0]> = {
    cwd: worktreePath,
    sessionManager,
    ...(thinkingLevel ? { thinkingLevel } : {}),
    ...(modelPattern ? { modelPattern } : {}),
    toolNames: agentToolNames,
    spawns: "",
    enableMCP: false,
    customTools,
  };

  if (systemPrompt) {
    sessionOpts.systemPrompt = systemPrompt;
  }
  if (mode === "foreman") {
    sessionOpts.settings = Settings.isolated({ "compaction.enabled": false });
  }

  const result = await createAgentSession(sessionOpts);
  session = result.session;

  // Emit session metadata for the Go harness to persist.
  try {
    process.stdout.write(
      `${JSON.stringify({
        type: "session_meta",
        omp_session_id: sessionManager.getSessionId(),
        omp_session_file: sessionManager.getSessionFile() ?? "",
      })}\n`,
    );
  } catch {
    // Best-effort: session metadata is non-critical.
  }

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
    updateAgentEndWatchdog(watchdogState, event);
  });

  emitLifecycle("started", { message: "Session started" });
}

/**
 * LineQueue wraps readline with an explicit pull-based queue.
 *
 * Bun's readline drops lines that arrive between removing a `once("line")`
 * listener and registering a new `on("line")` listener. This queue installs
 * a single permanent listener at construction time and lets callers pull
 * lines via `next()`, which returns a promise that resolves with the next
 * available line (or null on EOF).
 */
class LineQueue {
  #queue: string[] = [];
  #waiters: ((line: string | null) => void)[] = [];
  #closed = false;

  constructor(rl: ReturnType<typeof createInterface>) {
    rl.on("line", (line: string) => {
      if (this.#waiters.length > 0) {
        this.#waiters.shift()!(line);
      } else {
        this.#queue.push(line);
      }
    });
    rl.on("close", () => {
      this.#closed = true;
      // Resolve all pending waiters with null (EOF).
      for (const waiter of this.#waiters.splice(0)) {
        waiter(null);
      }
    });
  }

  /** Returns the next line, or null when stdin is closed. */
  next(): Promise<string | null> {
    if (this.#queue.length > 0) {
      return Promise.resolve(this.#queue.shift()!);
    }
    if (this.#closed) {
      return Promise.resolve(null);
    }
    return new Promise<string | null>((resolve) => {
      this.#waiters.push(resolve);
    });
  }
}

async function handleLine(line: string): Promise<void> {
  let msg: Record<string, unknown>;
  try {
    msg = JSON.parse(line);
  } catch {
    emitLifecycle("failed", { message: `Invalid JSON: ${line}` });
    return;
  }

  if (typeof msg !== "object" || msg === null || Array.isArray(msg)) {
    emitLifecycle("failed", { message: `Expected JSON object, got: ${typeof msg}` });
    return;
  }

  switch (msg.type) {
    case "abort":
      if (pendingStructuredAnswerResolve) {
        pendingStructuredAnswerResolve({ text: "[Session aborted]" });
        pendingStructuredAnswerResolve = null;
      }
      if (pendingAnswerResolve) {
        pendingAnswerResolve("[Session aborted]");
        pendingAnswerResolve = null;
      }
      process.exit(0);
      break;
    case "answer":
      if (pendingStructuredAnswerResolve) {
        const parsed = parseAnswerMessage(msg);
        emitInput("answer", parsed.text);
        pendingStructuredAnswerResolve(parsed);
        pendingStructuredAnswerResolve = null;
        break;
      }
      if (pendingAnswerResolve) {
        const answer = String(msg.text ?? "");
        emitInput("answer", answer);
        pendingAnswerResolve(answer);
        pendingAnswerResolve = null;
      }
      break;
    case "compact":
      if (!session) break;
      // Pre-check: skip the SDK call entirely when it would be a redundant no-op.
      // session.compact() preemptively disconnects the agent and aborts any in-flight
      // work BEFORE checking if compaction is possible, so even a "harmless" throw
      // there leaves the session unable to drive a follow-up prompt. See compact-helpers.ts.
      if (isCompactionRedundant(session.sessionManager.getBranch())) {
        emit({
          type: "lifecycle",
          stage: "compaction_end",
          message: "Already compacted — skipping",
        });
        break;
      }
      emit({ type: "lifecycle", stage: "compaction_start", message: "Compacting context…" });
      try {
        await session.compact();
        emit({ type: "lifecycle", stage: "compaction_end" });
      } catch (err: unknown) {
        const errorMessage = err instanceof Error ? err.message : String(err);
        if (isBenignCompactSkip(errorMessage)) {
          // Session is already in (or close enough to) a compacted state — equivalent to success.
          emit({ type: "lifecycle", stage: "compaction_end", message: errorMessage });
        } else {
          emit({ type: "lifecycle", stage: "compaction_failed", message: errorMessage });
        }
      }
      break;
    case "prompt":
      schedulePrompt(String(msg.text ?? ""), "prompt");
      break;
    case "message":
      schedulePrompt(String(msg.text ?? ""), "message");
      break;
    case "steer":
      if (session) {
        // Fire-and-forget: steer interrupts the agent's active streaming turn.
        session
          .prompt(String(msg.text ?? ""), { streamingBehavior: "steer" })
          .catch((err: unknown) => {
            const errorMessage = err instanceof Error ? err.message : String(err);
            emitLifecycle("failed", { message: `Steer failed: ${errorMessage}` });
          });
        emitInput("message", String(msg.text ?? ""));
      }
      break;
    default:
      emitLifecycle("failed", { message: `Unknown message type: ${msg.type}` });
  }
}

async function main(): Promise<void> {
  const rl = createInterface({ input: process.stdin });
  const lines = new LineQueue(rl);

  // Read the first line — it may be an init message with the system prompt.
  const firstLine = await lines.next();

  if (firstLine !== null) {
    try {
      const parsed = JSON.parse(firstLine);
      if (
        typeof parsed === "object" &&
        parsed !== null &&
        !Array.isArray(parsed) &&
        parsed.type === "init"
      ) {
        systemPrompt = parsed.system_prompt || undefined;
        emitInput("session_context", systemPrompt ?? "");
        if (typeof parsed.answer_timeout_ms === "number" && parsed.answer_timeout_ms >= 0) {
          answerTimeoutMs = parsed.answer_timeout_ms;
        }
        if (typeof parsed.model === "string" && parsed.model) {
          modelPattern = parsed.model;
        }
        if (typeof parsed.question_tool_policy === "string") {
          questionToolPolicy = parsed.question_tool_policy;
        }
      } else {
        // Not an init message — process it as a regular command after session starts.
        await initSessionOrDie();
        await handleLine(firstLine);
        return runLineLoop(lines);
      }
    } catch {
      await initSessionOrDie();
      await handleLine(firstLine);
      return runLineLoop(lines);
    }
  }

  await initSessionOrDie();
  return runLineLoop(lines);
}

async function initSessionOrDie(): Promise<void> {
  try {
    await initSession();
  } catch (err) {
    const errorMessage = err instanceof Error ? err.message : String(err);
    emitLifecycle("failed", { message: `Failed to initialize session: ${errorMessage}` });
    process.exit(1);
  }
}

async function runLineLoop(lines: LineQueue): Promise<void> {
  while (true) {
    const line = await lines.next();
    if (line === null) break; // stdin closed
    await handleLine(line);
  }
}

process.on("SIGTERM", () => {
  process.exit(0);
});
process.on("SIGINT", () => {
  process.exit(0);
});

main().catch((err) => {
  const errorMessage = err instanceof Error ? err.message : String(err);
  emitLifecycle("failed", { message: errorMessage });
  process.exit(1);
});

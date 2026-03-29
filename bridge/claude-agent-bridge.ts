/**
 * claude-agent-bridge.ts - Bridge between Substrate (Go) and Claude Agent SDK (Bun)
 *
 * JSON-line protocol over stdio:
 *
 * Go → Bun (stdin):
 *   - {"type":"init","mode":"agent"|"foreman","system_prompt":"...","resume_session_id":"...","permission_mode":"...","model":"...","max_turns":N,"max_budget_usd":N}
 *   - {"type":"prompt","text":"..."}   — initial task
 *   - {"type":"message","text":"..."}  — follow-up message
 *   - {"type":"steer","text":"..."}    — interrupt + new direction
 *   - {"type":"answer","text":"..."}   — resolve pending ask_foreman tool call
 *   - {"type":"abort"}                 — terminate session
 *   - {"type":"compact"}               — request manual context compaction (/compact slash command)
 *
 * Bun → Go (stdout):
 *   - {"type":"session_meta","session_id":"..."}
 *   - {"type":"event","event":{...}}   — canonical session transcript entry
 */

import { createInterface } from "readline";
import { query, tool, createSdkMcpServer } from "@anthropic-ai/claude-agent-sdk";
import { z } from "zod";

// ---------------------------------------------------------------------------
// Module-level state
// ---------------------------------------------------------------------------

const worktreePath = process.env.SUBSTRATE_WORKTREE_PATH ?? process.cwd();

let claudeSessionID = "";
let pendingAnswerResolve: ((text: string) => void) | null = null;
let activeQuery: ReturnType<typeof query> | null = null;
let lastResultText = "";
let answerTimeoutMs = 10 * 60 * 1000; // default 10 min; 0 = no timeout

// ---------------------------------------------------------------------------
// Emit helpers (verbatim from omp-bridge.ts)
// ---------------------------------------------------------------------------

function emit(event: object): void {
	process.stdout.write(JSON.stringify({ type: "event", event }) + "\n");
}

function emitLifecycle(
	stage: "started" | "completed" | "failed" | "retry_wait" | "retry_resumed",
	payload: Record<string, unknown> = {},
): void {
	emit({ type: "lifecycle", stage, ...payload });
}

function emitInput(
	inputKind: "prompt" | "message" | "steer" | "answer",
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

// ---------------------------------------------------------------------------
// LineQueue (verbatim from omp-bridge.ts)
// ---------------------------------------------------------------------------

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
		return new Promise<string | null>(resolve => {
			this.#waiters.push(resolve);
		});
	}
}

// ---------------------------------------------------------------------------
// ask_foreman MCP tool + server (module-level; only wired in agent mode)
// ---------------------------------------------------------------------------

const askForemanTool = tool(
	"ask_foreman",
	"Ask the foreman a clarifying question you cannot resolve from the plan or codebase.",
	{
		question: z.string().describe("The question to ask"),
		context: z.string().optional().describe("Surrounding context (optional)"),
	},
	async ({ question, context }: { question: string; context?: string }) => {
		emit({ type: "question", question, context: context ?? "" });
		const answer = await new Promise<string>(resolve => {
			pendingAnswerResolve = resolve;
			if (answerTimeoutMs > 0) {
				setTimeout(() => {
					if (pendingAnswerResolve === resolve) {
						pendingAnswerResolve = null;
						resolve(
							"[No answer received within timeout. Proceed with your best judgment.]",
						);
					}
				}, answerTimeoutMs);
			}
		});
		return { content: [{ type: "text" as const, text: answer }] };
	},
);

const substrateMcpServer = createSdkMcpServer({
	name: "substrate",
	version: "1.0.0",
	tools: [askForemanTool],
});

// ---------------------------------------------------------------------------
// SDK message mapper
// ---------------------------------------------------------------------------

function mapSDKMessage(msg: any): void {
	if (msg.type === "system" && msg.subtype === "init") {
		claudeSessionID = msg.session_id ?? "";
		// Emit session_meta (top-level, not wrapped in "event")
		process.stdout.write(
			JSON.stringify({ type: "session_meta", session_id: claudeSessionID }) + "\n",
		);
		emitLifecycle("started");
		return;
	}

	if (msg.type === "assistant") {
		const content: any[] = Array.isArray(msg.message?.content)
			? msg.message.content
			: [];
		for (const block of content) {
			if (block.type === "text" && block.text) {
				emit({ type: "assistant_output", text: block.text });
			} else if (block.type === "thinking" && block.thinking) {
				emit({ type: "thinking_output", text: block.thinking });
			} else if (block.type === "tool_use") {
				emit({
					type: "tool_start",
					tool: block.name,
					text: truncateText(safeJson(block.input)),
				});
			}
		}
		return;
	}

	if (msg.type === "user" && msg.isSynthetic === true) {
		const content: any[] = Array.isArray(msg.message?.content)
			? msg.message.content
			: [];
		for (const block of content) {
			if (block.type === "tool_result") {
				emit({
					type: "tool_result",
					tool: block.tool_use_id,
					text: truncateText(extractTextPayload(block.content)),
					is_error: block.is_error ?? false,
				});
			}
		}
		return;
	}

	if (msg.type === "result" && msg.subtype === "success") {
		lastResultText = String(msg.result ?? "");
		emitLifecycle("completed", { summary: lastResultText });
		return;
	}

	if (msg.type === "result" && typeof msg.subtype === "string" && msg.subtype.startsWith("error")) {
		emitLifecycle("failed", { message: msg.errors?.[0] ?? msg.subtype });
		return;
	}

	if (msg.type === "system" && msg.subtype === "api_retry") {
		const attempt = Number(msg.attempt ?? 1);
		const maxRetries = Number(msg.max_retries ?? 0);
		const delayMs = Number(msg.retry_delay_ms ?? 0);
		const delaySec = Math.round(delayMs / 1000);
		const attemptStr = maxRetries > 0 ? `${attempt}/${maxRetries}` : String(attempt);
		emitLifecycle("retry_wait", {
			message: `Rate limited — retrying in ${delaySec}s (attempt ${attemptStr})`,
		});
		return;
	}

	// All other types: ignore
}

// ---------------------------------------------------------------------------
// Streaming user turns generator
// ---------------------------------------------------------------------------

async function* userTurns(lines: LineQueue): AsyncGenerator<any> {
	while (true) {
		const line = await lines.next();
		if (line === null) return; // stdin closed

		let msg: Record<string, unknown>;
		try {
			msg = JSON.parse(line);
		} catch {
			continue;
		}

		if (msg.type === "abort") {
			if (pendingAnswerResolve) {
				pendingAnswerResolve("[Session aborted]");
				pendingAnswerResolve = null;
			}
			process.exit(0);
		}

		if (msg.type === "answer") {
			if (pendingAnswerResolve) {
				const answer = String(msg.text ?? "");
				emitInput("answer", answer);
				pendingAnswerResolve(answer);
				pendingAnswerResolve = null;
			}
			continue;
		}

		if (msg.type === "compact") {
			emit({ type: "lifecycle", stage: "compaction_start", message: "Compacting context…" });
			yield {
				type: "user",
				message: {
					role: "user",
					content: [{ type: "text", text: "/compact" }],
				},
			};
			// /compact is processed within the SDK turn; no explicit end event.
			// The SDK handles compaction internally — auto_compaction events may
			// fire, which mapSDKMessage already ignores (they're Claude-internal).
			emit({ type: "lifecycle", stage: "compaction_end" });
			continue;
		}
		if (msg.type === "prompt" || msg.type === "message") {
			emitInput(msg.type as "prompt" | "message", String(msg.text ?? ""));
			yield {
				type: "user",
				message: {
					role: "user",
					content: [{ type: "text", text: String(msg.text ?? "") }],
				},
			};
		}

		if (msg.type === "steer") {
			emitInput("steer", String(msg.text ?? ""));
			if (activeQuery) {
				await activeQuery.interrupt();
			}
			yield {
				type: "user",
				message: {
					role: "user",
					content: [{ type: "text", text: String(msg.text ?? "") }],
				},
			};
		}
	}
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

async function main(): Promise<void> {
	const rl = createInterface({ input: process.stdin });
	const lines = new LineQueue(rl);

	// Read init message (always first)
	const firstLine = await lines.next();
	if (firstLine === null) {
		emitLifecycle("failed", { message: "stdin closed before init" });
		process.exit(1);
	}

	let initMsg: Record<string, unknown>;
	try {
		initMsg = JSON.parse(firstLine);
	} catch {
		emitLifecycle("failed", { message: `Invalid JSON in init: ${firstLine}` });
		process.exit(1);
	}

	if (initMsg.type !== "init") {
		emitLifecycle("failed", { message: `Expected init message, got: ${initMsg.type}` });
		process.exit(1);
	}

	const mode = String(initMsg.mode ?? "agent");
	const systemPromptText =
		typeof initMsg.system_prompt === "string" ? initMsg.system_prompt : undefined;
	const resumeSessionId =
		typeof initMsg.resume_session_id === "string" ? initMsg.resume_session_id : undefined;
	const permissionMode =
		typeof initMsg.permission_mode === "string" ? initMsg.permission_mode : undefined;
	const model =
		typeof initMsg.model === "string" && initMsg.model ? initMsg.model : undefined;
	const maxTurns =
		typeof initMsg.max_turns === "number" && initMsg.max_turns > 0
			? initMsg.max_turns
			: undefined;
	if (typeof initMsg.answer_timeout_ms === "number" && initMsg.answer_timeout_ms >= 0) {
		answerTimeoutMs = initMsg.answer_timeout_ms;
	}
	// maxBudgetUSD is not a direct SDK option; not forwarded

	const options: Record<string, unknown> = {
		cwd: worktreePath,
		settingSources: mode === "foreman" ? ["user"] : ["user", "project"],
	};

	if (systemPromptText) {
		options.systemPrompt = { type: "preset", preset: "claude_code", append: systemPromptText };
	}
	if (resumeSessionId) options.resume = resumeSessionId;
	if (permissionMode) {
		options.permissionMode = permissionMode;
	} else {
		options.permissionMode = mode === "foreman" ? "dontAsk" : "acceptEdits";
	}
	if (model) options.model = model;
	if (maxTurns) options.maxTurns = maxTurns;

	if (mode === "foreman") {
		options.allowedTools = ["Read", "Grep", "Glob"];
	} else {
		options.allowedTools = [
			"Read",
			"Write",
			"Edit",
			"Bash",
			"Glob",
			"Grep",
			"WebSearch",
			"WebFetch",
			"mcp__substrate__ask_foreman",
		];
		options.mcpServers = { substrate: substrateMcpServer };
	}

	const generator = userTurns(lines);

	let queryFailed = false;
	try {
		activeQuery = query({ prompt: generator as any, options: options as any });
		for await (const msg of activeQuery) {
			mapSDKMessage(msg as any);
		}
	} catch (err) {
		const errorMessage = err instanceof Error ? err.message : String(err);
		emitLifecycle("failed", { message: errorMessage });
		queryFailed = true;
	}

	// After the query loop completes, emit foreman_proposed if in foreman mode.
	// lifecycle/completed was already emitted inside mapSDKMessage for result/success.
	if (mode === "foreman" && lastResultText !== "" && !queryFailed) {
		const { text, uncertain } = extractConfidence(lastResultText);
		emit({ type: "foreman_proposed", text, uncertain });
	}
	// The SDK query is finite-lifetime. Once it completes, nothing remains.
	// Exit explicitly — the readline interface on stdin would keep the event loop alive.
	// Exit 1 when the catch above fired so Go's cmd.Wait() sees a non-zero code and
	// treats the subprocess as unexpectedly terminated, not as a clean abort.
	process.exit(queryFailed ? 1 : 0);

}

// Handle --version before entering the stdin event loop so that the binary
// can be smoke-tested without auth or an active Claude subscription.
if (process.argv.includes("--version")) {
	console.log("claude-agent-bridge");
	process.exit(0);
}

// ---------------------------------------------------------------------------
// Signal handlers and error boundary
// ---------------------------------------------------------------------------

process.on("SIGTERM", () => process.exit(0));
process.on("SIGINT", () => process.exit(0));

main().catch(err => {
	emitLifecycle("failed", {
		message: err instanceof Error ? err.message : String(err),
	});
	process.exit(1);
});

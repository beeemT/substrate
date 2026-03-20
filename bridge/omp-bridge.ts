/**
 * omp-bridge.ts - Bridge between Substrate (Go) and oh-my-pi agent (Bun)
 *
 * JSON-line protocol over stdio:
 *
 * Go → Bun (stdin):
 *   - {"type":"prompt","text":"..."} — initial prompt or continuation
 *   - {"type":"message","text":"..."} — follow-up message (human iteration)
 *   - {"type":"answer","text":"..."} — resolve pending ask_foreman tool call
 *   - {"type":"abort"} — terminate session
 *
 * Bun → Go (stdout):
 *   - {"type":"event","event":{...}} — canonical session transcript entry
 */

import { createInterface } from "readline";

const mode = process.env.SUBSTRATE_BRIDGE_MODE ?? "agent";
const thinkingLevel = process.env.SUBSTRATE_THINKING_LEVEL ?? "xhigh";
const systemPromptEnv = process.env.SUBSTRATE_SYSTEM_PROMPT ?? "";
const allowPushEnv = process.env.SUBSTRATE_ALLOW_PUSH ?? "false";
const worktreePath = process.env.SUBSTRATE_WORKTREE_PATH ?? process.cwd();

const systemPrompt = systemPromptEnv ? Buffer.from(systemPromptEnv, "base64").toString("utf-8") : undefined;
const allowPush = allowPushEnv === "true";

const agentToolNames = mode === "agent"
	? ["read", "grep", "find", "edit", "write", "bash"]
	: ["read", "grep", "find"];

let pendingAnswerResolve: ((text: string) => void) | null = null;
let lastAssistantText = "";
let session: Awaited<ReturnType<typeof createAgentSession>>["session"] | null = null;

function emit(event: object): void {
	process.stdout.write(JSON.stringify({ type: "event", event }) + "\n");
}

function emitLifecycle(stage: "started" | "completed" | "failed", payload: Record<string, unknown> = {}): void {
	emit({ type: "lifecycle", stage, ...payload });
}

function emitInput(inputKind: "prompt" | "message" | "answer", text: string): void {
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
		const intent = typeof (event as Record<string, any>).intent === "string" ? (event as Record<string, any>).intent : undefined;
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

	return [];
}

async function runPrompt(text: string, inputKind: "prompt" | "message"): Promise<void> {
	if (!session) {
		emitLifecycle("failed", { message: "Session not initialized" });
		return;
	}

	lastAssistantText = "";
	emitInput(inputKind, text);

	try {
		await session.prompt(text, { expandPromptTemplates: false });
		if (mode === "foreman") {
			const { text: answer, uncertain } = extractConfidence(lastAssistantText);
			emit({ type: "foreman_proposed", text: answer, uncertain });
		} else {
			emitLifecycle("completed", { summary: "Session complete" });
		}
	} catch (err) {
		const errorMessage = err instanceof Error ? err.message : String(err);
		emitLifecycle("failed", { message: errorMessage });
	}
}

function createAskForemanTool(): unknown {
	return {
		name: "ask_foreman",
		description: "Ask the foreman a question you cannot resolve from the plan or codebase.",
		parameters: {
			type: "object",
			properties: {
				question: {
					type: "string",
					description: "The question to ask the foreman"
				},
				context: {
					type: "string",
					description: "Surrounding context from your work (optional)"
				}
			},
			required: ["question"]
		},
		execute: async (_toolCallId: string, args: Record<string, unknown>) => {
			const question = String(args.question ?? "");
			const context = String(args.context ?? "");
			emit({ type: "question", question, context });
			const answer = await new Promise<string>(resolve => {
				pendingAnswerResolve = resolve;
			});
			return answer;
		}
	};
}

async function initSession(): Promise<void> {
	const { createAgentSession, SessionManager, Settings } = await import("@oh-my-pi/pi-coding-agent");

	const resumeSessionFile = process.env.SUBSTRATE_RESUME_SESSION_FILE ?? "";
	const sessionManager = mode === "foreman"
		? SessionManager.inMemory()
		: resumeSessionFile
			? SessionManager.open(resumeSessionFile)
			: SessionManager.create(worktreePath);
	const customTools = mode === "agent" ? [createAskForemanTool()] : [];

	const sessionOpts: Parameters<typeof createAgentSession>[0] = {
		cwd: worktreePath,
		sessionManager,
		thinkingLevel: thinkingLevel as any,
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
		process.stdout.write(JSON.stringify({
			type: "session_meta",
			omp_session_id: sessionManager.getSessionId(),
			omp_session_file: sessionManager.getSessionFile() ?? "",
		}) + "\n");
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
	});

	emitLifecycle("started", { message: "Session started" });
}

async function main(): Promise<void> {
	try {
		await initSession();
	} catch (err) {
		const errorMessage = err instanceof Error ? err.message : String(err);
		emitLifecycle("failed", { message: `Failed to initialize session: ${errorMessage}` });
		process.exit(1);
	}

	const rl = createInterface({ input: process.stdin });

	rl.on("line", async (line: string) => {
		let msg: Record<string, unknown>;
		try {
			msg = JSON.parse(line);
		} catch {
			emitLifecycle("failed", { message: `Invalid JSON: ${line}` });
			return;
		}

		switch (msg.type) {
			case "abort":
				rl.close();
				process.exit(0);
				break;
			case "answer":
				if (pendingAnswerResolve) {
					const answer = String(msg.text ?? "");
					emitInput("answer", answer);
					pendingAnswerResolve(answer);
					pendingAnswerResolve = null;
				}
				break;
			case "prompt":
				await runPrompt(String(msg.text ?? ""), "prompt");
				break;
			case "message":
				await runPrompt(String(msg.text ?? ""), "message");
				break;
			case "steer":
				if (session) {
					// Fire-and-forget: steer interrupts the active streaming turn.
					session.prompt(String(msg.text ?? ""), { streamingBehavior: "steer" }).catch((err: unknown) => {
						const errorMessage = err instanceof Error ? err.message : String(err);
						emitLifecycle("failed", { message: `Steer failed: ${errorMessage}` });
					});
					emitInput("message", String(msg.text ?? ""));
				}
				break;
			default:
				emitLifecycle("failed", { message: `Unknown message type: ${msg.type}` });
		}
	});

	rl.on("close", () => {
		process.exit(0);
	});

	process.on("SIGTERM", () => {
		process.exit(0);
	});
	process.on("SIGINT", () => {
		process.exit(0);
	});
}

main().catch(err => {
	const errorMessage = err instanceof Error ? err.message : String(err);
	emitLifecycle("failed", { message: errorMessage });
	process.exit(1);
});

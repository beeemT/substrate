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
 *   - {"type":"event","event":{"type":"progress","text":"..."}} — text delta
 *   - {"type":"event","event":{"type":"question","question":"...","context":"..."}} — agent called ask_foreman
 *   - {"type":"event","event":{"type":"foreman_proposed","text":"...","uncertain":true}} — foreman answer with confidence
 *   - {"type":"event","event":{"type":"complete","summary":"..."}} — turn completed
 */

import { createInterface } from "readline";

// Environment variables passed from Go
const mode = process.env.SUBSTRATE_BRIDGE_MODE ?? "agent"; // "agent" | "foreman"
const thinkingLevel = process.env.SUBSTRATE_THINKING_LEVEL ?? "xhigh";
const systemPromptEnv = process.env.SUBSTRATE_SYSTEM_PROMPT ?? "";
const allowPushEnv = process.env.SUBSTRATE_ALLOW_PUSH ?? "false";
const worktreePath = process.env.SUBSTRATE_WORKTREE_PATH ?? process.cwd();
const sessionLogPath = process.env.SUBSTRATE_SESSION_LOG_PATH ?? "";

const systemPrompt = systemPromptEnv
    ? Buffer.from(systemPromptEnv, "base64").toString("utf-8")
    : undefined;

const allowPush = allowPushEnv === "true";

// Tool names based on mode
const agentToolNames = mode === "agent"
    ? ["read", "grep", "find", "edit", "write", "bash"]
    : ["read", "grep", "find"]; // foreman and review: read-only

// Pending answer resolver for ask_foreman tool
let pendingAnswerResolve: ((text: string) => void) | null = null;

// Accumulated assistant text for foreman_proposed emission
let lastAssistantText = "";

// Track if we're in the middle of a turn
let turnInProgress = false;

// Session state
let session: Awaited<ReturnType<typeof createAgentSession>>["session"] | null = null;

/**
 * Emit an event to stdout (Go side)
 */
function emit(event: object): void {
    process.stdout.write(JSON.stringify({ type: "event", event }) + "\n");
}

/**
 * Extract confidence marker from foreman response
 * Returns the text with the marker stripped and the confidence level
 */
function extractConfidence(text: string): { text: string; uncertain: boolean } {
    const lines = text.split("\n");
    const last = lines[lines.length - 1].trim();
    
    if (last === "CONFIDENCE: high") {
        return { text: lines.slice(0, -1).join("\n").trimEnd(), uncertain: false };
    }
    if (last === "CONFIDENCE: uncertain") {
        return { text: lines.slice(0, -1).join("\n").trimEnd(), uncertain: true };
    }
    
    // Missing marker → conservative escalation
    return { text, uncertain: true };
}

/**
 * Map oh-my-pi AgentSessionEvent to bridge event format
 * Returns null for unhandled event types (caller filters before emitting)
 */
function mapEvent(e: unknown): object | null {
    const event = e as Record<string, unknown>;
    
    if (event.type === "message_update") {
        const assistantEvent = (event as Record<string, any>).assistantMessageEvent;
        if (assistantEvent?.type === "text_delta") {
            return { type: "progress", text: assistantEvent.delta };
        }
    }
    
    if (event.type === "tool_execution_start") {
        const toolName = (event as Record<string, any>).toolName;
        return { type: "progress", text: `tool: ${toolName}` };
    }
    
    if (event.type === "tool_execution_end") {
        const toolName = (event as Record<string, any>).toolName;
        return { type: "progress", text: `tool: ${toolName} (done)` };
    }
    
    // Unhandled event type - filtered by caller
    return null;
}

/**
 * Run a prompt and handle completion
 */
async function runPrompt(text: string): Promise<void> {
    if (!session) {
        emit({ type: "error", message: "Session not initialized" });
        return;
    }
    
    turnInProgress = true;
    lastAssistantText = "";
    
    try {
        await session.prompt(text, { expandPromptTemplates: false });
        
        // session.prompt() resolves when the turn is complete
        if (mode === "foreman") {
            const { text: answer, uncertain } = extractConfidence(lastAssistantText);
            emit({ type: "foreman_proposed", text: answer, uncertain });
        } else {
            emit({ type: "complete", summary: "Turn completed" });
        }
    } catch (err) {
        const errorMessage = err instanceof Error ? err.message : String(err);
        emit({ type: "error", message: errorMessage });
    } finally {
        turnInProgress = false;
    }
}

/**
 * Create the ask_foreman custom tool (agent mode only)
 * Blocks until the orchestrator sends {type:"answer"} on stdin
 */
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

/**
 * Initialize the agent session
 */
async function initSession(): Promise<void> {
    // Dynamic import for oh-my-pi SDK
    // The SDK will be available via bun's module resolution
    const { createAgentSession, SessionManager, Settings } = await import("@oh-my-pi/pi-coding-agent");
    
    const sessionManager = mode === "foreman" 
        ? SessionManager.inMemory() 
        : SessionManager.create(worktreePath);
    
    const customTools = mode === "agent" ? [createAskForemanTool()] : [];
    
    // Build session options
    const sessionOpts: Parameters<typeof createAgentSession>[0] = {
        cwd: worktreePath,
        sessionManager,
        thinkingLevel: thinkingLevel as any,
        toolNames: agentToolNames,
        spawns: "", // Prevent agent from spawning unmonitored sub-agents
        enableMCP: false,
        customTools,
    };
    
    // Add system prompt if provided
    if (systemPrompt) {
        sessionOpts.systemPrompt = systemPrompt;
    }
    
    // For foreman mode, disable compaction (Go-side restart with summary handles context management)
    if (mode === "foreman") {
        sessionOpts.settings = Settings.isolated({ "compaction.enabled": false });
    }
    
    const result = await createAgentSession(sessionOpts);
    session = result.session;
    
    // Subscribe to session events
    session.subscribe((event: unknown) => {
        const mapped = mapEvent(event);
        if (mapped !== null) {
            emit(mapped);
        }
        
        // Accumulate text for foreman_proposed / complete emission
        const e = event as Record<string, unknown>;
        if (e.type === "message_update") {
            const assistantEvent = (e as Record<string, any>).assistantMessageEvent;
            if (assistantEvent?.type === "text_delta") {
                lastAssistantText += assistantEvent.delta;
            }
        }
    });
    
    emit({ type: "session_ready" });
}

/**
 * Main entry point
 */
async function main(): Promise<void> {
    // Initialize session
    try {
        await initSession();
    } catch (err) {
        const errorMessage = err instanceof Error ? err.message : String(err);
        emit({ type: "error", message: `Failed to initialize session: ${errorMessage}` });
        process.exit(1);
    }
    
    // Set up stdin reader
    const rl = createInterface({ input: process.stdin });
    
    rl.on("line", async (line: string) => {
        let msg: Record<string, unknown>;
        
        try {
            msg = JSON.parse(line);
        } catch {
            emit({ type: "error", message: `Invalid JSON: ${line}` });
            return;
        }
        
        switch (msg.type) {
            case "abort":
                // Graceful shutdown
                rl.close();
                process.exit(0);
                break;
                
            case "answer":
                // Resolve pending ask_foreman tool call
                if (pendingAnswerResolve) {
                    pendingAnswerResolve(String(msg.text ?? ""));
                    pendingAnswerResolve = null;
                }
                break;
                
            case "prompt":
            case "message":
                // Run the prompt
                await runPrompt(String(msg.text ?? ""));
                break;
                
            default:
                emit({ type: "error", message: `Unknown message type: ${msg.type}` });
        }
    });
    
    // Handle stdin close (parent process terminated)
    rl.on("close", () => {
        process.exit(0);
    });
    
    // Handle termination signals
    process.on("SIGTERM", () => {
        process.exit(0);
    });
    
    process.on("SIGINT", () => {
        process.exit(0);
    });
}

// Run main
main().catch(err => {
    const errorMessage = err instanceof Error ? err.message : String(err);
    emit({ type: "error", message: errorMessage });
    process.exit(1);
});

/**
 * question-tool MCP server (package: question-mcp) — exposes a single
 * question tool to the agent and routes the question to substrate via a
 * Unix domain socket.
 *
 * Which question tool is exposed is controlled by the
 * SUBSTRATE_QUESTION_TOOL_MODE env var:
 *   - "human"  → expose `ask_user` (operator-directed questions)
 *   - default  → expose `ask_foreman` (foreman-routed questions)
 *
 * Socket path is read from SUBSTRATE_QUESTION_SOCKET.
 *
 * Protocol (newline-delimited JSON over the socket):
 *   Request:  {"type":"question","question":"..."}\n
 *   Response: {"type":"answer","answer":"...","confidence":"high|medium|low"}\n
 */

import net from "node:net";
import { createInterface } from "node:readline";
import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";
import { z } from "zod";

// Env vars set by the Go orchestrator.
const SOCKET_ENV = "SUBSTRATE_QUESTION_SOCKET";
const TOOL_MODE_ENV = "SUBSTRATE_QUESTION_TOOL_MODE";

// ---------------------------------------------------------------------------
// Socket communication
// ---------------------------------------------------------------------------

interface QuestionAnswer {
  type: "answer";
  answer: string;
  confidence: "high" | "medium" | "low";
}

/**
 * Connect to the substrate question socket, send a question, and wait for an answer.
 * The connection is established per-call to avoid stale connections.
 */
function askQuestion(question: string, context = ""): Promise<QuestionAnswer> {
  const socketPath = process.env[SOCKET_ENV];
  if (!socketPath) {
    return Promise.reject(
      new Error(`Environment variable ${SOCKET_ENV} is not set. Cannot reach substrate questions.`),
    );
  }

  return new Promise<QuestionAnswer>((resolve, reject) => {
    let settled = false;
    const settle = (fn: () => void) => {
      if (settled) return;
      settled = true;
      clearTimeout(timeout);
      fn();
    };

    const socket = net.createConnection(socketPath);

    // Safety net: reject if no answer arrives within 5 minutes.
    // Stored in `timeout` after the variable is declared so the close handler
    // can reference it via the closure.
    const timeout = setTimeout(
      () => {
        socket.destroy();
        settle(() => reject(new Error("Timed out waiting for question answer")));
      },
      5 * 60 * 1000,
    );

    socket.on("connect", () => {
      socket.write(`${JSON.stringify({ type: "question", question, context })}\n`);
    });

    // Use readline to buffer across multiple data events so a response split
    // across TCP/socket segments is reassembled before parsing.
    const rl = createInterface({ input: socket, crlfDelay: Infinity });

    rl.on("line", (line: string) => {
      if (line.trim() === "") return;
      rl.close();
      socket.destroy();

      try {
        const msg = JSON.parse(line);
        if (
          msg.type !== "answer" ||
          typeof msg.answer !== "string" ||
          !["high", "medium", "low"].includes(msg.confidence)
        ) {
          settle(() =>
            reject(
              new Error(
                `Invalid question response: expected {type:"answer",answer:string,confidence:"high"|"medium"|"low"}, got: ${line}`,
              ),
            ),
          );
          return;
        }
        settle(() => resolve(msg as QuestionAnswer));
      } catch {
        settle(() => reject(new Error(`Malformed JSON from question socket: ${line}`)));
      }
    });

    socket.on("error", (err: Error) => {
      settle(() => reject(new Error(`Socket error: ${err.message}`)));
    });

    socket.on("close", () => {
      // If we haven't settled yet, the connection closed before we got an answer.
      settle(() => reject(new Error("Question socket closed without sending an answer")));
    });
  });
}

// ---------------------------------------------------------------------------
// MCP Server
// ---------------------------------------------------------------------------

// The Go orchestrator decides which question tool this process should expose
// (operator-directed vs foreman-routed). The MCP helper itself just forwards
// the question over the substrate socket — the destination lives in Go.
type QuestionToolTarget = "foreman" | "human";

function questionToolTarget(): QuestionToolTarget {
  return process.env[TOOL_MODE_ENV] === "human" ? "human" : "foreman";
}

const questionTarget = questionToolTarget();
const toolName = questionTarget === "human" ? "ask_user" : "ask_foreman";
const toolDescription =
  questionTarget === "human"
    ? "Ask the operator a question and wait for their answer"
    : "Ask the substrate foreman a question and wait for an answer";
const questionDescription =
  questionTarget === "human"
    ? "The question to ask the operator"
    : "The question to ask the foreman";
// Server name is part of the harness contract: the Go orchestrator references
// these exact strings when registering the MCP server with the agent runtime.
const serverName = questionTarget === "human" ? "substrate-user" : "substrate-foreman";
const server = new McpServer(
  { name: serverName, version: "0.1.0" },
  {
    capabilities: {
      tools: {},
    },
  },
);

server.tool(
  toolName,
  toolDescription,
  {
    question: z.string().describe(questionDescription),
    context: z.string().optional().describe("Surrounding context (optional)"),
  },
  async ({ question, context }) => {
    try {
      const result = await askQuestion(question, context ?? "");
      return {
        content: [
          {
            type: "text" as const,
            text: `${result.answer}\n[confidence: ${result.confidence}]`,
          },
        ],
      };
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      return {
        content: [{ type: "text" as const, text: `Error: ${message}` }],
        isError: true,
      };
    }
  },
);

// ---------------------------------------------------------------------------
// Startup
// ---------------------------------------------------------------------------

async function main() {
  const socketPath = process.env[SOCKET_ENV];
  if (!socketPath) {
    process.stderr.write(
      `WARNING: ${SOCKET_ENV} not set — ${toolName} tool will fail at call time\n`,
    );
  }

  const transport = new StdioServerTransport();
  await server.connect(transport);
}

// Handle --version before entering the MCP event loop so the binary can be
// smoke-tested without a live substrate question socket or MCP client.
if (process.argv.includes("--version")) {
  console.log("question-mcp");
  process.exit(0);
}

main().catch((err) => {
  process.stderr.write(`Fatal: ${err instanceof Error ? err.message : err}\n`);
  process.exit(1);
});

/**
 * opencode-foreman-mcp - MCP server that exposes an `ask_foreman` tool.
 *
 * Communicates with the substrate foreman via a Unix domain socket
 * (path from SUBSTRATE_FOREMAN_SOCKET env var).
 *
 * Protocol (newline-delimited JSON over the socket):
 *   Request:  {"type":"question","question":"..."}\n
 *   Response: {"type":"answer","answer":"...","confidence":"high|medium|low"}\n
 */

import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";
import { z } from "zod";
import net from "node:net";
import { createInterface } from "node:readline";

const SOCKET_ENV = "SUBSTRATE_FOREMAN_SOCKET";

// ---------------------------------------------------------------------------
// Socket communication
// ---------------------------------------------------------------------------

interface ForemanAnswer {
  type: "answer";
  answer: string;
  confidence: "high" | "medium" | "low";
}

/**
 * Connect to the foreman socket, send a question, and wait for an answer.
 * The connection is established per-call to avoid stale connections.
 */
function askForeman(question: string): Promise<ForemanAnswer> {
  const socketPath = process.env[SOCKET_ENV];
  if (!socketPath) {
    return Promise.reject(
      new Error(
        `Environment variable ${SOCKET_ENV} is not set. Cannot reach the foreman.`,
      ),
    );
  }

  return new Promise<ForemanAnswer>((resolve, reject) => {
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
    const timeout = setTimeout(() => {
      socket.destroy();
      settle(() => reject(new Error("Timed out waiting for foreman answer")));
    }, 5 * 60 * 1000);

    socket.on("connect", () => {
      socket.write(JSON.stringify({ type: "question", question }) + "\n");
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
                `Invalid foreman response: expected {type:"answer",answer:string,confidence:"high"|"medium"|"low"}, got: ${line}`,
              ),
            ),
          );
          return;
        }
        settle(() => resolve(msg as ForemanAnswer));
      } catch {
        settle(() => reject(new Error(`Malformed JSON from foreman: ${line}`)));
      }
    });

    socket.on("error", (err: Error) => {
      settle(() => reject(new Error(`Socket error: ${err.message}`)));
    });

    socket.on("close", () => {
      // If we haven't settled yet, the connection closed before we got an answer.
      settle(() =>
        reject(new Error("Foreman socket closed without sending an answer")),
      );
    });
  });
}

// ---------------------------------------------------------------------------
// MCP Server
// ---------------------------------------------------------------------------

const server = new McpServer(
  { name: "substrate-foreman", version: "0.1.0" },
  {
    capabilities: {
      tools: {},
    },
  },
);

server.tool(
  "ask_foreman",
  "Ask the substrate foreman a question and wait for an answer",
  {
    question: z.string().describe("The question to ask the foreman"),
  },
  async ({ question }) => {
    try {
      const result = await askForeman(question);
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
      `WARNING: ${SOCKET_ENV} not set — ask_foreman tool will fail at call time\n`,
    );
  }

  const transport = new StdioServerTransport();
  await server.connect(transport);
}

main().catch((err) => {
  process.stderr.write(`Fatal: ${err instanceof Error ? err.message : err}\n`);
  process.exit(1);
});

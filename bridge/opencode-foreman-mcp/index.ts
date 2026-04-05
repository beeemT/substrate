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
    const socket = net.createConnection(socketPath);

    const timeout = setTimeout(() => {
      socket.destroy();
      reject(new Error("Timed out waiting for foreman answer"));
    }, 5 * 60 * 1000); // 5 minute timeout

    socket.on("connect", () => {
      socket.write(JSON.stringify({ type: "question", question }) + "\n");
    });

    socket.on("data", (chunk: Buffer) => {
      // We only expect one answer line; parse the first complete line.
      const text = chunk.toString("utf-8");
      for (const line of text.split("\n")) {
        if (line.trim() === "") continue;

        clearTimeout(timeout);
        socket.destroy();

        try {
          const msg = JSON.parse(line);
          if (
            msg.type !== "answer" ||
            typeof msg.answer !== "string" ||
            !["high", "medium", "low"].includes(msg.confidence)
          ) {
            reject(
              new Error(
                `Invalid foreman response: expected {type:"answer",answer:string,confidence:"high"|"medium"|"low"}, got: ${line}`,
              ),
            );
            return;
          }
          resolve(msg as ForemanAnswer);
        } catch {
          reject(new Error(`Malformed JSON from foreman: ${line}`));
          return;
        }
      }
    });

    socket.on("error", (err: Error) => {
      clearTimeout(timeout);
      reject(new Error(`Socket error: ${err.message}`));
    });

    socket.on("close", () => {
      clearTimeout(timeout);
      // If we haven't resolved yet, the connection closed prematurely.
      // The promise is still pending; it will be rejected by the timeout
      // or by an 'error' event that preceded the close.
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

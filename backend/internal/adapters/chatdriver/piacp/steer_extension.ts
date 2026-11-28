// agent-orchestrator: managed pi steering extension (do not edit)
// AO Chat steers Pi through this file; see steer.go. It does nothing unless
// AO_PI_STEER_DIR is set, so any other pi started in this workspace is
// unaffected.
import { rmSync } from "node:fs";
import { createConnection, createServer } from "node:net";
import { join } from "node:path";

const STEER_SESSIONS = Symbol.for("ao.pi.steer.sessions");
// A steer longer than this is refused rather than buffered without end.
const MAX_REQUEST_CHARS = 8 * 1024 * 1024;

// steerSessions returns this process's registry of steerable Pi sessions, or
// undefined when AO didn't start it. Pi runs every session of an AO session in
// one process and loads this file once per Pi session, so the first load
// starts the one socket and later loads share the registry.
//
// The folder is taken out of the environment on the first load, so a pi that
// the agent or its tools start from here neither opens nor takes over the
// session's socket.
function steerSessions() {
  if (globalThis[STEER_SESSIONS]) return globalThis[STEER_SESSIONS];
  const dir = process.env.AO_PI_STEER_DIR ?? "";
  delete process.env.AO_PI_STEER_DIR;
  if (!dir || process.platform === "win32") return undefined;
  const sessions = new Map();
  globalThis[STEER_SESSIONS] = sessions;
  const path = join(dir, "ao-steer.sock");
  // A socket that still answers belongs to another live Pi process; leave it
  // alone. One that does not is stale.
  const probe = createConnection(path);
  probe.on("connect", () => probe.destroy());
  probe.on("error", () => {
    try { rmSync(path, { force: true }); } catch {}
    const server = createServer((conn) => answerSteer(conn, sessions));
    server.on("error", () => {});
    server.listen(path, () => {
      process.once("exit", () => {
        try { rmSync(path, { force: true }); } catch {}
      });
    });
    server.unref();
  });
  return sessions;
}

// answerSteer reads one steer request and answers it once. A steer reaches
// only a running turn, after its current tool calls and before its next model
// call. With no turn running AO is told so and nothing is started:
// sendMessage never starts a turn by itself.
function answerSteer(conn, sessions) {
  let buffer = "";
  let handled = false;
  const answer = (reply) => {
    handled = true;
    buffer = "";
    conn.end(JSON.stringify(reply) + "\n");
  };
  conn.setEncoding("utf8");
  conn.on("error", () => {});
  conn.on("data", (chunk) => {
    if (handled) return;
    buffer += chunk;
    const newline = buffer.indexOf("\n");
    if (newline < 0) {
      if (buffer.length > MAX_REQUEST_CHARS) answer({ outcome: "error", reason: "the steer is too long" });
      return;
    }
    try {
      const request = JSON.parse(buffer.slice(0, newline));
      const text = typeof request.text === "string" ? request.text.trim() : "";
      const session = sessions.get(request.sessionId);
      if (!text) {
        answer({ outcome: "error", reason: "the steer has no text" });
      } else if (!session || session.ctx.isIdle()) {
        answer({ outcome: "promptRequired" });
      } else {
        session.pi.sendMessage({ customType: "ao-steer", content: text, display: true }, { deliverAs: "steer" });
        answer({ outcome: "injected" });
      }
    } catch (error) {
      answer({ outcome: "error", reason: String(error) });
    }
  });
}

export default function (pi) {
  // Pi's ACP adapter never emits session_start, so the socket opens when this
  // loads and the Pi session registers on its first turn, which brings its
  // context.
  const sessions = steerSessions();
  if (!sessions) return;
  let registered = "";
  const unregister = () => {
    if (registered && sessions.get(registered)?.pi === pi) sessions.delete(registered);
    registered = "";
  };
  const register = async (_event, ctx) => {
    const id = ctx.sessionManager.getSessionId() ?? "";
    if (!id) return;
    if (registered !== id) unregister();
    registered = id;
    sessions.set(id, { pi, ctx });
  };
  pi.on("session_start", register);
  pi.on("agent_start", register);
  pi.on("turn_start", register);
  pi.on("session_shutdown", async () => unregister());
}

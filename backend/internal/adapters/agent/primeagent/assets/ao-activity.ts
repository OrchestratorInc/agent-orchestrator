// agent-orchestrator: managed Prime Agent activity extension
import { spawnSync } from "node:child_process";

const hookTimeoutMs = 10_000;

function report(event, payload, cwd) {
  try {
    spawnSync("ao", ["hooks", "prime-agent", event], {
      cwd,
      encoding: "utf8",
      input: `${JSON.stringify(payload)}\n`,
      stdio: ["pipe", "ignore", "ignore"],
      timeout: hookTimeoutMs,
      windowsHide: true,
    });
  } catch {
    // Activity reporting is best effort and must never break Prime Agent.
  }
}

function bestEffort(handler) {
  return (...args) => {
    try {
      handler(...args);
    } catch {
      // Malformed lifecycle data must not escape into Prime Agent.
    }
  };
}

function nativeSessionId(context) {
  const getSessionId = context.sessionManager?.getSessionId;
  if (typeof getSessionId !== "function") {
    return "";
  }
  const sessionId = getSessionId.call(context.sessionManager);
  return typeof sessionId === "string" ? sessionId : "";
}

// The run's final assistant text. The event shape is the agent's, so anything
// unexpected only means "no text".
function finalAssistantText(event) {
  try {
    for (const message of [...(event?.messages ?? [])].reverse()) {
      if (message?.role !== "assistant") continue;
      const parts = typeof message.content === "string" ? [{ type: "text", text: message.content }] : message.content;
      const text = (Array.isArray(parts) ? parts : []).filter((part) => part?.type === "text" && typeof part.text === "string").map((part) => part.text).join("\n");
      if (text.trim()) return text;
    }
  } catch {}
  return "";
}

export default function aoActivityExtension(prime) {
  let pendingPrompt = "";

  prime.on("session_start", bestEffort((event, context) => {
    const payload = { reason: event.reason ?? "" };
    const sessionId = nativeSessionId(context);
    if (sessionId !== "") {
      payload.session_id = sessionId;
    }
    report("session-start", payload, context.cwd);
  }));

  prime.on("before_agent_start", bestEffort((event) => {
    pendingPrompt = typeof event.prompt === "string" ? event.prompt : "";
  }));

  prime.on("agent_start", bestEffort((_event, context) => {
    const prompt = pendingPrompt;
    pendingPrompt = "";
    report("user-prompt-submit", { prompt }, context.cwd);
  }));

  prime.on("agent_end", bestEffort((event, context) => {
    report("stop", { last_assistant_message: finalAssistantText(event) }, context.cwd);
  }));

  prime.on("session_shutdown", bestEffort((event, context) => {
    pendingPrompt = "";
    if (event.reason === "quit") {
      report("session-end", { reason: event.reason }, context.cwd);
    }
  }));
}

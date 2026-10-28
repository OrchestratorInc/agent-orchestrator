// agent-orchestrator: managed Strands activity plugin
import { spawnSync } from "node:child_process";
import {
  BeforeInvocationEvent,
  BeforeToolCallEvent,
  AfterInvocationEvent,
  HookOrder,
  InitializedEvent,
  Sandbox,
  Tool,
} from "@strands-agents/sdk";
import { makeShell } from "@strands-agents/sdk/vended-tools/shell";

// Preserve the released shell's schema, validation, output and error handling.
// Only its execution sandbox is scoped to the SDK's current tool cancellation.
class CancellationSandbox extends Sandbox {
  constructor(sandbox, signal) {
    super();
    this.sandbox = sandbox;
    this.signal = signal;
  }
  execute(command, options) {
    this.signal.throwIfAborted();
    return this.sandbox.execute(command, { ...options, signal: this.signal });
  }
}

class CancellationShell extends Tool {
  constructor() {
    super();
    const native = makeShell();
    this.name = native.name;
    this.description = native.description;
    this.toolSpec = native.toolSpec;
  }
  stream(context) {
    return makeShell(new CancellationSandbox(context.agent.sandbox, context.cancelSignal)).stream(context);
  }
}

function report(event, agent) {
  if (!process.env.AO_SESSION_ID) return;
  const session_id = agent.sessionId;
  try {
    spawnSync("ao", ["hooks", "strands", event], {
      cwd: process.cwd(),
      input: JSON.stringify({ session_id }) + "\n",
      stdio: ["pipe", "ignore", "ignore"],
      timeout: 2000,
      windowsHide: true,
    });
  } catch {
    // Observability must never break a native provider turn.
  }
}

// Native permissions run at INTERVENTION_INPUT and await the TUI broker from
// inside BeforeToolCallEvent. Bracket that hook instead of copying Cedar policy
// or scraping the terminal: an allowed call reaches the trailing hook before a
// zero-delay timer fires, while a displayed permission panel leaves it pending.
// The trailing hook runs as soon as the user answers, before tool execution.
export function bridgePermissionActivity(agent, emit) {
  const pending = new Map();
  const blocked = new Set();
  let batchReported = false;

  agent.addHook(BeforeToolCallEvent, (event) => {
    const id = event.toolUse.toolUseId;
    const candidate = { timer: undefined };
    candidate.timer = setTimeout(() => {
      if (pending.get(id) !== candidate) return;
      blocked.add(id);
      if (!batchReported) {
        batchReported = true;
        emit("permission-request");
      }
    }, 0);
    pending.set(id, candidate);
  }, { order: HookOrder.INTERVENTION_INPUT - 1 });

  agent.addHook(BeforeToolCallEvent, (event) => {
    const id = event.toolUse.toolUseId;
    const candidate = pending.get(id);
    if (!candidate) return;
    clearTimeout(candidate.timer);
    pending.delete(id);
    blocked.delete(id);
    if (batchReported && blocked.size === 0 && pending.size === 0) {
      batchReported = false;
      emit("active");
    }
  }, { order: HookOrder.INTERVENTION_INPUT + 1 });

  return () => {
    for (const candidate of pending.values()) clearTimeout(candidate.timer);
    pending.clear();
    blocked.clear();
    batchReported = false;
  };
}

// Capture the selected built-in before saved user plugins initialize. Reject
// replacement by a custom plugin rather than silently changing its semantics.
const selectedShells = new WeakMap();
export const shellGuard = {
  name: "agent-orchestrator:strands-shell-guard",
  initAgent(agent) { selectedShells.set(agent, agent.toolRegistry.get("shell")); },
};

export default {
  name: "agent-orchestrator:strands-activity",
  initAgent(agent) {
    // Model-triggered native profile reloads can drop AO plugins and private
    // instructions. Explicit human /setup takeover is outside this adapter.
    agent.toolRegistry.remove("strands_config");
    const shell = agent.toolRegistry.get("shell");
    if (!selectedShells.has(agent) || shell !== selectedShells.get(agent)) {
      throw new Error("strands: custom shell replacement is unsupported by AO cancellation");
    }
    if (shell) {
      const replacement = new CancellationShell();
      if (JSON.stringify(shell.toolSpec) !== JSON.stringify(replacement.toolSpec)) {
        throw new Error("strands: custom shell configuration is unsupported by AO cancellation");
      }
      agent.toolRegistry.addOrReplace([replacement]);
    }
    // A propagated plugin must not let a nested agent settle the root turn.
    const index = process.argv.indexOf("--session-id");
    if (index < 0 || !agent.sessionManager) return;
    if (agent.sessionId !== process.argv[index + 1] || agent.sandbox.cwd !== process.cwd()) {
      throw new Error("strands: changing native session or workspace inside AO is unsupported");
    }
    // SDK background-task registration follows consumer plugins. Inspect its
    // public management tool only after all plugins initialize, and again on
    // invocation so native configuration rebuilds cannot enable false settling.
    const requireForeground = () => {
      if (agent.toolRegistry.get("strands_manage_background_task")) {
        throw new Error("strands: background dispatch inside AO is unsupported");
      }
    };
    const resetPermissionActivity = bridgePermissionActivity(agent, (event) => report(event, agent));
    agent.addHook(InitializedEvent, () => { requireForeground(); report("session-start", agent); });
    agent.addHook(BeforeInvocationEvent, () => { resetPermissionActivity(); requireForeground(); report("active", agent); });
    agent.addHook(AfterInvocationEvent, () => { resetPermissionActivity(); report("stop", agent); });
  },
};

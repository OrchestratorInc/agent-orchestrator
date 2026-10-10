// agent-orchestrator: managed Strands activity plugin
import { spawnSync } from "node:child_process";
import { BeforeInvocationEvent, AfterInvocationEvent, InitializedEvent } from "@strands-agents/sdk";

function report(event) {
  if (!process.env.AO_SESSION_ID) return;
  const index = process.argv.indexOf("--session-id");
  const session_id = index >= 0 ? process.argv[index + 1] : undefined;
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

let rootAgent;
export default {
  name: "agent-orchestrator:strands-activity",
  initAgent(agent) {
    // A propagated plugin must not let a nested agent settle the root turn.
    if (rootAgent) return;
    rootAgent = agent;
    agent.addHook(InitializedEvent, () => report("session-start"));
    agent.addHook(BeforeInvocationEvent, () => report("active"));
    agent.addHook(AfterInvocationEvent, () => report("stop"));
  },
};

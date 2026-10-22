// Capability catalog, per-capability matrix and failure diagnosis for the AO
// harness audit. Pure data and formatting: no network, process or file access,
// so it can be imported by the runner, the suites and the fixture tests alike.
//
// Each capability maps the runner gates that prove it. A gate that does not
// pass gets a diagnosis: what AO was expected to do, what was observed, the
// most likely causes, and the source files to inspect first. Paths are relative
// to the AO checkout root; `{tui}`, `{chat}` and `{reviewer}` expand to the
// harness's adapter, Chat driver and reviewer directory names. A harness
// declares them in its contract (`adapters`); otherwise they are guessed from
// the agent id, which is right for most harnesses.

import { CHANNEL_CAUSES } from "./audit_instructions.mjs";

// Catalog `support` is what every harness owes AO, not what one harness does:
//   required  every harness with this surface must pass it
//   optional  depends on the native CLI; a truthful refusal is UNSUPPORTED
//   manual    exists in AO but the API-first runner does not drive it
// A harness contract can override a capability (`supports: { chat_steer: "no" }`)
// and list its surfaces (`surfaces: ["tui"]` for a harness with no Chat).
// Resolved values are "yes", "conditional", "no" and "manual".
export const CAPABILITIES = Object.freeze([
  {
    id: "install_detect",
    title: "Install, detect and version",
    surface: "inventory",
    support: "required",
    gates: ["local_binary", "local_version", "registered", "installed", "fresh_probe"],
    inspect: [
      "backend/internal/adapters/agent/{tui}/",
      "backend/internal/adapters/agent/registry/registry.go",
      "backend/internal/service/agent/readiness.go",
      "backend/internal/domain/agent_readiness.go",
    ],
  },
  {
    id: "auth_status",
    title: "Authentication status and launch readiness",
    surface: "inventory",
    support: "required",
    gates: ["authentication", "launch_readiness"],
    inspect: [
      "backend/internal/adapters/agent/{tui}/",
      "backend/internal/service/agentauth/plans.go",
    ],
  },
  {
    id: "model_catalog",
    title: "Model catalog, refresh and native cross-check",
    surface: "inventory",
    support: "required",
    gates: ["local_models_list", "ao_models_api", "ao_models_refresh", "model_efforts", "model_catalog_consistency"],
    inspect: [
      "backend/internal/adapters/agent/modelcatalog/catalog.go",
      "backend/internal/httpd/controllers/agents.go",
      "backend/internal/ports/agent.go",
    ],
  },
  {
    id: "tui_spawn",
    title: "TUI spawn in an isolated worktree",
    surface: "tui",
    support: "required",
    gates: ["local_session_spawn", "tui_spawn", "correct_working_directory", "proof_file_creation", "initial_prompt_exactly_once"],
    inspect: [
      "backend/internal/session_manager/manager.go",
      "backend/pkg/agentruntime/command.go",
      "backend/internal/adapters/agent/{tui}/",
      "backend/internal/adapters/workspace/gitworktree/workspace.go",
    ],
  },
  {
    id: "instructions",
    title: "System prompt delivery, AO rules and project AGENTS.md",
    surface: "tui+chat",
    support: "required",
    gates: ["hidden_ao_instructions", "project_agents_md", "tui_system_prompt_file", "instruction_channel", "chat_instruction_channel"],
    inspect: [
      "backend/internal/session_manager/prompt.go",
      "backend/internal/session_manager/manager.go",
      "backend/pkg/agentruntime/command.go",
      "backend/internal/adapters/chatdriver/{chat}/",
      ".agents/skills/ao-agent-harness/scripts/audit_instructions.mjs",
    ],
  },
  {
    id: "role_prompts",
    title: "Worker vs orchestrator system prompt (V1)",
    surface: "tui+chat",
    support: "required",
    gates: ["role_worker", "role_orchestrator"],
    inspect: [
      "backend/internal/session_manager/prompt.go",
      "backend/internal/session_manager/manager.go",
      "backend/pkg/agentruntime/command.go",
    ],
  },
  {
    id: "ao_skill",
    title: "AO skill (using-ao) pointer",
    surface: "tui",
    support: "required",
    gates: ["ao_skill_pointer"],
    inspect: [
      "backend/internal/skillassets/skillassets.go",
      "backend/internal/daemon/daemon.go",
      "backend/internal/session_manager/manager.go",
    ],
  },
  {
    id: "tui_messaging",
    title: "TUI follow-up message",
    surface: "tui",
    support: "required",
    gates: ["second_message"],
    inspect: ["backend/internal/session_manager/manager.go", "backend/internal/httpd/controllers/sessions.go"],
  },
  {
    id: "hooks_activity",
    title: "Hooks, activity states and native session-ID capture",
    surface: "tui",
    support: "required",
    gates: ["tui_hooks_installed", "activity_status", "native_session_id"],
    inspect: [
      "backend/internal/adapters/agent/{tui}/",
      "backend/internal/cli/hooks.go",
    ],
  },
  {
    id: "tui_cancel",
    title: "Stop a running TUI agent (archive the session)",
    surface: "tui",
    support: "required",
    gates: ["cancellation", "post_cancel_draft_cleanup"],
    inspect: [
      "backend/internal/session_manager/manager.go",
      "backend/internal/cli/agent_process.go",
      "backend/internal/cli/ptyhost.go",
    ],
  },
  {
    id: "tui_kill_restore",
    title: "TUI kill and exact native restore",
    surface: "tui",
    support: "required",
    gates: ["termination", "native_restore", "same_ao_session_workspace", "post_restore_terminal_ready", "post_restore_message", "history_file_continuity"],
    inspect: [
      "backend/pkg/agentruntime/command.go",
      "backend/internal/adapters/agent/{tui}/",
      "backend/internal/session_manager/manager.go",
    ],
  },
  {
    id: "usage",
    title: "Token usage and cost accounting",
    surface: "tui+chat",
    support: "required",
    gates: ["tui_usage_recorded", "chat_usage"],
    inspect: [
      "backend/internal/service/usage/collector.go",
      "backend/internal/service/usage/capabilities.go",
      "backend/internal/cli/hooks.go",
    ],
  },
  {
    id: "chat_spawn",
    title: "Chat spawn and controller readiness",
    surface: "chat",
    support: "required",
    gates: ["chat_spawn", "chat_controller_ready", "chat_initial_turn", "chat_instructions"],
    inspect: [
      "backend/internal/session_manager/chat_spawn.go",
      "backend/internal/service/chat/service.go",
      "backend/internal/adapters/chatdriver/{chat}/",
      "backend/internal/adapters/chatdriver/registry/registry.go",
    ],
  },
  {
    id: "chat_messaging",
    title: "Chat follow-up, turn lifecycle and idempotent send",
    surface: "chat",
    support: "required",
    gates: ["chat_turn_lifecycle", "chat_followup", "chat_idempotent_send"],
    inspect: [
      "backend/internal/service/chat/service.go",
      "backend/internal/service/chat/controller.go",
      "backend/internal/httpd/controllers/conversations.go",
    ],
  },
  {
    id: "chat_models_settings",
    title: "Chat model list and model/effort settings",
    surface: "chat",
    support: "required",
    gates: ["chat_models", "chat_settings"],
    inspect: [
      "backend/internal/adapters/chatdriver/{chat}/",
      "backend/internal/httpd/controllers/conversations.go",
    ],
  },
  {
    id: "chat_mcp",
    title: "AO MCP server attached to Chat",
    surface: "chat",
    support: "optional",
    gates: ["chat_mcp_servers"],
    inspect: [
      "backend/internal/session_manager/chat_spawn.go",
      "backend/internal/adapters/chatdriver/{chat}/",
      "backend/internal/cli/mcp.go",
    ],
  },
  {
    id: "chat_skills",
    title: "Chat skills listing",
    surface: "chat",
    support: "optional",
    gates: ["chat_skills"],
    inspect: [
      "backend/internal/adapters/chatdriver/{chat}/",
      "backend/internal/adapters/chatdriver/acp/skills.go",
      "backend/internal/httpd/controllers/conversations_skills.go",
    ],
  },
  {
    id: "chat_steer",
    title: "Chat steer into the running turn",
    surface: "chat",
    support: "optional",
    gates: ["chat_steer"],
    inspect: [
      "backend/internal/service/chat/steer.go",
      "backend/internal/adapters/chatdriver/{chat}/",
      "backend/internal/adapters/chatdriver/acp/steer.go",
    ],
  },
  {
    id: "chat_interrupt",
    title: "Chat interrupt of an active tool command",
    surface: "chat",
    support: "required",
    gates: ["chat_interrupt", "chat_post_interrupt_continuation"],
    inspect: [
      "backend/internal/service/chat/controller.go",
      "backend/internal/adapters/chatdriver/{chat}/",
      "backend/internal/adapters/chatdriver/acp/driver.go",
    ],
  },
  {
    id: "chat_approvals",
    title: "Chat approvals (reject and approve)",
    surface: "chat",
    support: "required",
    gates: ["chat_approval_reject", "chat_approval_approve"],
    inspect: [
      "backend/internal/service/chat/controller.go",
      "backend/internal/adapters/chatdriver/{chat}/",
      "backend/internal/adapters/chatdriver/acp/client.go",
      "backend/internal/adapters/chatdriver/{chat}/",
    ],
  },
  {
    id: "chat_kill_restore",
    title: "Chat kill and exact native restore",
    surface: "chat",
    support: "required",
    gates: ["chat_kill", "chat_restore", "chat_restore_continuation"],
    inspect: [
      "backend/internal/adapters/chatdriver/{chat}/",
      "backend/internal/adapters/chatdriver/persistenthost/host.go",
    ],
  },
  {
    id: "chat_compaction",
    title: "Chat compaction",
    surface: "chat",
    support: "optional",
    gates: ["chat_compact"],
    inspect: [
      "backend/internal/adapters/chatdriver/{chat}/",
      "backend/internal/adapters/chatdriver/acp/conversation.go",
      "backend/internal/adapters/chatdriver/acp/skills.go",
    ],
  },
  {
    id: "interface_switch",
    title: "TUI ↔ Chat interface switch with history continuity",
    surface: "tui+chat",
    support: "optional",
    gates: ["iface_status", "iface_tui_to_chat", "iface_chat_continuation", "iface_chat_to_tui", "iface_tui_continuation"],
    inspect: [
      "backend/internal/session_manager/interface_transition.go",
      "backend/internal/domain/session_interface_transition.go",
      "backend/internal/adapters/agent/{tui}/",
      "backend/internal/httpd/controllers/sessions_interface_transition.go",
    ],
  },
  // Present in AO but not driven by the API-first runner. Each says why and
  // where to verify it, so the matrix never reports them as silently covered.
  {
    id: "install_job", title: "In-page install job", surface: "ui", support: "manual", gates: [],
    manual: "Would reinstall the user's real agent; drive `POST /agents/{agent}/install` only on a disposable host (audit-journeys.md#mandatory-step-by-step-screenshots, install-run).",
    inspect: ["backend/internal/service/systeminstall/agentplans.go"],
  },
  {
    id: "login_flow", title: "Login/logout and multi-account", surface: "ui", support: "manual", gates: [],
    manual: "Needs interactive credentials; the runner never logs in or out. Use auth-action/auth-maintenance checkpoints.",
    inspect: ["backend/internal/service/agentauth/plans.go", "backend/internal/adapters/agent/{tui}/"],
  },
  {
    id: "reviewer", title: "Reviewer harness", surface: "review", support: "manual", gates: [],
    manual: "Needs a real PR head; use reviewer-select/running/result checkpoints.",
    inspect: ["backend/internal/adapters/reviewer/registry.go", "backend/internal/adapters/reviewer/{reviewer}/"],
  },
  {
    id: "attachments", title: "Attachments and images", surface: "chat", support: "manual", gates: [],
    manual: "A driver sends native images when its handshake advertises them, otherwise path references. Verify through the composer.",
    inspect: ["backend/internal/session_manager/chat_attachments.go", "backend/internal/adapters/chatdriver/acp/driver.go"],
  },
  {
    id: "history_edit", title: "Rollback, edit, retry and fork", surface: "chat", support: "manual", gates: [],
    manual: "Drivers either declare native rollback/fork or fall back to prompt replay. Rewrites conversation history, so verify on a scratch Chat session.",
    inspect: ["backend/internal/adapters/chatdriver/{chat}/"],
  },
  {
    id: "hibernation", title: "Chat hibernation and wake", surface: "chat", support: "manual", gates: [],
    manual: "Driven by the idle-hibernation timer (`PATCH /settings/chat-hibernation`); changes a global setting.",
    inspect: ["backend/internal/session_manager/manager.go"],
  },
  {
    id: "agent_switch", title: "Cross-harness provider switch", surface: "tui+chat", support: "manual", gates: [],
    manual: "Needs two authorized harnesses in one run; exercise `POST /sessions/{id}/provider-switch` separately (the target starts a fresh native thread seeded with a handoff note).",
    inspect: ["backend/internal/session_manager/provider_switch_tui.go", "backend/internal/httpd/controllers/conversations.go"],
  },
  {
    id: "subagents", title: "Subagent activity", surface: "tui+chat", support: "manual", gates: [],
    manual: "Depends on the model choosing to spawn a subagent; verify activity facts when it does.",
    inspect: ["backend/internal/lifecycle/subagent_activity.go", "backend/internal/cli/hooks.go"],
  },
]);

const GATE_TO_CAPABILITY = new Map(CAPABILITIES.flatMap((capability) => capability.gates.map((name) => [name, capability])));

// What each gate checks and the usual reasons it fails. Gates without an entry
// still get a diagnosis built from their reason and evidence.
const GATE_HINTS = Object.freeze({
  local_binary: { expected: "the native CLI resolves on PATH or at the contract path", causes: ["CLI not installed for the audit user", "local contract `binary` names the wrong executable"] },
  local_version: { expected: "`versionArgs` exits 0 and prints a version", causes: ["CLI needs an update or first-run migration", "contract versionArgs are wrong"] },
  local_session_spawn: { expected: "a direct native session writes the proof file in a scratch workspace", causes: ["native login missing", "contract sessionArgs do not run non-interactively"] },
  registered: { expected: "GET/POST /agents reports the harness as supported", causes: ["harness is not registered in this AO build", "wrong agent id"] },
  installed: { expected: "readiness snapshot has installation.state=installed", causes: ["AO resolves a different binary than the shell", "binary resolution cache is stale; POST /agents/refresh"] },
  fresh_probe: { expected: "installation and authentication observations are fresh", causes: ["readiness probe timed out", "probe cache did not refresh"] },
  authentication: { expected: "authentication.state is authorized and probe authStatus is authorized", causes: ["not logged in for the audit user", "credentials only in a different HOME/profile", "auth probe command output format changed"] },
  launch_readiness: { expected: "effectiveReadiness=ready", causes: ["a readiness requirement other than install/auth failed; read the snapshot reasons"] },
  ao_models_api: { expected: "GET /agents/{agent}/models returns at least one model", causes: ["model discovery failed and no static fallback exists", "provider model list call failed (auth or network)"] },
  ao_models_refresh: { expected: "POST /agents/{agent}/models/refresh succeeds and keeps a non-empty catalog", causes: ["refresh path errors while cached read works", "discovery depends on a binary-version fingerprint that changed"] },
  model_efforts: { expected: "at least one model advertises reasoning efforts with a valid defaultEffort", causes: ["catalog normalisation dropped `efforts`", "provider no longer reports efforts"] },
  model_catalog_consistency: { expected: "native and AO catalogs share at least one exact model id", causes: ["AO uses aliases where the CLI reports full ids", "stale AO catalog"] },
  tui_spawn: { expected: "POST /sessions mode=tui creates a session with the requested harness", causes: ["spawn validation rejected the body", "workspace creation failed", "launch argv invalid for this CLI version"] },
  correct_working_directory: { expected: "the provider's cwd equals the AO worktree", causes: ["launch does not chdir into the worktree", "symlinked tmp path not normalised"] },
  proof_file_creation: { expected: "the first turn writes valid proof JSON in the worktree", causes: ["agent saved it to the AO session artifacts folder (using-ao guidance) instead of the worktree", "agent stuck on a trust/permission prompt", "initial prompt never delivered (check the native session log)", "agent wrote markdown fences instead of raw JSON"] },
  initial_prompt_exactly_once: { expected: "initial prompt token handled exactly once", causes: ["prompt delivered twice (argv and paste)", "prompt lost on startup"] },
  hidden_ao_instructions: { expected: "the proof carries rulesMarker, a project rule that exists only in AO's system prompt (agentRules)", causes: ["system prompt flag not passed (append-system-prompt / model_instructions_file)", "agentRules not written into system.md", "no proof file at all (see proof_file_creation)"] },
  project_agents_md: { expected: "the proof carries agentsMarker, a convention that exists only in project AGENTS.md", causes: ["CLI does not read AGENTS.md (some read their own file, e.g. CLAUDE.md)", "worktree missing the committed AGENTS.md", "no proof file at all (see proof_file_creation)"] },
  tui_system_prompt_file: { expected: "<data>/prompts/<session>/system.md exists and carries the project agentRules", causes: ["prompt writer skipped this harness", "agentRules not merged into the system prompt"] },
  instruction_channel: { expected: "the CLI receives the session system prompt through the declared channel (flag in argv, protocol param, config file, or last-resort rule/plugin file)", causes: ["system.md generated but never passed to the CLI", "rule/config file written outside the path the CLI reads", "no channel declared or available"] },
  chat_instruction_channel: { expected: "the Chat driver delivers the system prompt through its channel and the agent uses it", causes: ["session parameter not sent at session/new", "plugin/rule file not passed (--plugin-dir) or not auto-applied"] },
  role_worker: { expected: "a worker session answers \"worker\" from its AO system prompt", causes: ["system prompt not passed to the CLI (see instruction_channel)", "worker prompt does not state the role", "CLI wrote nothing: session never became ready"] },
  role_orchestrator: { expected: "an orchestrator session answers \"orchestrator\" from its AO system prompt", causes: ["orchestrator prompt not used for this harness (falls back to the worker prompt)", "system prompt not passed to the CLI", "orchestrator spawn refused for this harness"] },
  ao_skill_pointer: { expected: "<data>/skills/using-ao/SKILL.md exists and system.md points at it", causes: ["skill assets not installed at daemon boot", "aoSkillPointer omitted for this harness"] },
  second_message: { expected: "POST /sessions/{id}/send makes the provider add secondMessageToken", causes: ["send rejected while not idle", "keystrokes delivered but not submitted", "UserPromptSubmit hook not firing"] },
  tui_hooks_installed: { expected: "AO hook commands are installed for the session", causes: ["hook installer skipped the worktree", "hook file written outside the worktree", "launch argv missing hook -c flags"] },
  activity_status: { expected: "activity went active then settled with statusReadiness=ready", causes: ["hooks not reporting to POST /sessions/{id}/activity", "`ao` binary not on the hook PATH", "terminal-surface fallback misclassified"] },
  native_session_id: { expected: "sessions.agent_session_id persisted from hooks", causes: ["SessionStart hook payload lacks the id", "hook could not reach the daemon"] },
  cancellation: { expected: "archiving the session (Kill) while a command runs stops that command and its delayed side effect never happens", causes: ["Kill stops the CLI but not its child process group", "the command was detached into the background", "agent refused or skipped the test command (see its session log)"] },
  termination: { expected: "kill sets isTerminated=true", causes: ["runtime pane did not exit", "kill blocked by an in-flight provision"] },
  native_restore: { expected: "restore relaunches with the same native session id", causes: ["restore fell back to a fresh/saved-prompt launch", "native transcript missing for that id"] },
  post_restore_message: { expected: "continuation after restore mutates the proof", causes: ["restored CLI not ready for input", "resume opened a different conversation"] },
  history_file_continuity: { expected: "restored provider recalls the history-only token and keeps earlier fields", causes: ["resume did not load native history", "proof file overwritten instead of updated"] },
  tui_usage_recorded: { expected: "GET /usage/sessions/{id} reports tokens after provider turns", causes: ["usage hook metadata not captured", "transcript backfill not run for this harness"] },
  chat_spawn: { expected: "POST /sessions mode=chat creates a chat session", causes: ["Chat driver not registered", "driver Probe failed (version, Node runtime, handshake)", "launch auth validation rejected credentials"] },
  chat_controller_ready: { expected: "conversation controller reaches ready", causes: ["driver process failed to start (see daemon log)", "ACP/app-server handshake timed out", "persistent chat host not adopted"] },
  chat_initial_turn: { expected: "initial Chat prompt writes the proof once in the session worktree", causes: ["initial prompt not dispatched as the first turn", "turn failed; read turns[].errorMessage"] },
  chat_instructions: { expected: "Chat provider used agentRules and AGENTS.md tokens", causes: ["the driver does not send the system prompt as a session parameter at session start", "the driver sends it but the native agent ignores that parameter"] },
  chat_turn_lifecycle: { expected: "follow-up turn observed running, then completed with assistant output", causes: ["turn events not normalised", "controller stuck busy"] },
  chat_followup: { expected: "conversation message mutates the proof", causes: ["message queued behind a stuck turn", "send rejected (read the error code)"] },
  chat_idempotent_send: { expected: "repeating a clientMessageId returns duplicate=true without a new turn", causes: ["idempotency key not persisted", "duplicate check scoped wrongly"] },
  chat_models: { expected: "GET conversation/models returns models and the selected settings", causes: ["driver lacks models capability", "model/list call failed"] },
  chat_settings: { expected: "PATCH conversation/settings model/effort reads back from the snapshot", causes: ["settings not persisted to conversations row", "effort rejected for this model"] },
  chat_mcp_servers: { expected: "snapshot lists the attached AO MCP server", causes: ["AO MCP server not attached at Chat spawn", "mcp.servers event not emitted"] },
  chat_skills: { expected: "GET conversation/skills returns a list", causes: ["skills capability advertised but listing fails"] },
  chat_steer: { expected: "steer lands in the running turn (or is truthfully refused when unsupported)", causes: ["steer capability advertised but provider rejects it", "steer accepted while capability absent"] },
  chat_interrupt: { expected: "interrupt stops the witnessed command; turn ends interrupted; no late side effect", causes: ["interrupt not forwarded to provider", "provider cancels turn but leaves the child process", "controller stays busy"] },
  chat_post_interrupt_continuation: { expected: "same conversation accepts and completes a new turn after interrupt", causes: ["controller not reset after interrupt", "native session closed on cancel"] },
  chat_approval_reject: { expected: "accept-edits surfaces an approval; reject prevents the side effect", causes: ["approval mode mapping wrong (no approval requested)", "decision not delivered to provider", "provider offered no reject option"] },
  chat_approval_approve: { expected: "approving the pending request lets exactly that command run", causes: ["decisionId mismatch", "approval resolved but provider never resumed"] },
  chat_kill: { expected: "kill terminates the chat session", causes: ["driver Terminate did not exit", "chat host left running"] },
  chat_restore: { expected: "restore reconnects the same provider conversation id", causes: ["Resume started a fresh native conversation", "history/checkpoint verification failed"] },
  chat_restore_continuation: { expected: "restored Chat recalls the history-only token", causes: ["native session/load not used", "checkpoint replay lost earlier turns"] },
  chat_compact: { expected: "compaction succeeds and the conversation still answers", causes: ["compaction capability advertised without a working /compact", "thread.compacted not observed"] },
  chat_usage: { expected: "Chat usage events recorded", causes: ["usage events not mapped for this driver"] },
  iface_status: { expected: "GET interface-transition reports supported=true for the live TUI session", causes: ["native conversation id not captured", "Chat driver unavailable for this harness", "read reasonCode"] },
  iface_tui_to_chat: { expected: "transition to chat reaches phase=completed and session.mode=chat", causes: ["drain timed out", "target Chat could not load the native conversation", "read transition.errorCode/errorDetail"] },
  iface_chat_continuation: { expected: "Chat after switch recalls the TUI history token", causes: ["Chat started a fresh conversation (history not shared)", "historyPolicy strict rejected the native transcript"] },
  iface_chat_to_tui: { expected: "transition back to tui completes and session.mode=tui", causes: ["TUI resume argv missing the native id", "chat controller did not stop"] },
  iface_tui_continuation: { expected: "TUI after switching back mutates the proof", causes: ["resumed TUI not ready for input", "resume opened a different conversation"] },
});

// Directory names default to the agent id without dashes ("my-agent" →
// "myagent", Chat driver "myagentacp"); a contract's `adapters` overrides them.
function expand(path, agent, contract) {
  const plain = agent.replaceAll("-", "");
  const dirs = { tui: plain, chat: `${plain}acp`, reviewer: plain, ...contract?.adapters };
  return path.replaceAll("{tui}", dirs.tui).replaceAll("{chat}", dirs.chat).replaceAll("{reviewer}", dirs.reviewer);
}

export function inspectPaths(capability, agent, contract = null) {
  return [...new Set((capability?.inspect ?? []).map((path) => expand(path, agent ?? "", contract)))];
}

export function capabilityForGate(name) {
  return GATE_TO_CAPABILITY.get(name) ?? null;
}

export function capabilitySupport(agent, id, contract = null) {
  return supportOf(CAPABILITIES.find((item) => item.id === id), contract);
}

function supportOf(capability, contract) {
  if (!capability) return "unknown";
  if (capability.support === "manual") return "manual";
  const declared = contract?.supports?.[capability.id];
  if (declared) return declared;
  const surfaces = contract?.surfaces;
  const needed = capability.surface.split("+").filter((surface) => surface === "tui" || surface === "chat");
  if (Array.isArray(surfaces) && needed.some((surface) => !surfaces.includes(surface))) return "no";
  return capability.support === "required" ? "yes" : "conditional";
}

// Walks gate evidence (API responses, command outcomes, nested groups) and
// returns short human-readable observations such as
// "POST /api/v1/sessions → HTTP 409 SESSION_BUSY: session is busy".
export function observedFromEvidence(evidence, limit = 6) {
  const lines = [];
  const seen = new Set();
  const visit = (value, depth) => {
    if (!value || typeof value !== "object" || depth > 4 || lines.length >= limit) return;
    if (Array.isArray(value)) {
      for (const item of value.slice(0, 10)) visit(item, depth + 1);
      return;
    }
    if (typeof value.method === "string" && typeof value.path === "string" && "status" in value) {
      const body = value.response && typeof value.response === "object" ? value.response : {};
      const error = body.error && typeof body.error === "object" ? body.error : body;
      const code = error.code || body.code || "";
      const message = error.message || body.message || value.error || "";
      const line = `${value.method} ${value.path} → ${value.status ? `HTTP ${value.status}` : "no response"}${code ? ` ${code}` : ""}${message ? `: ${String(message).slice(0, 240)}` : ""}`;
      if (!seen.has(line)) { seen.add(line); lines.push(line); }
    } else if ("code" in value && ("stderr" in value || "stdout" in value)) {
      const stderr = String(value.stderr || "").trim().split("\n").slice(-3).join(" | ");
      const line = `command exited ${value.code}${value.timedOut ? " (timed out)" : ""}${stderr ? `: ${stderr.slice(0, 240)}` : ""}`;
      if (!seen.has(line)) { seen.add(line); lines.push(line); }
    }
    for (const [key, item] of Object.entries(value)) {
      if (key === "response" || key === "stdout" || key === "stderr") continue;
      visit(item, depth + 1);
    }
  };
  visit(evidence, 0);
  return lines;
}

const LOG_SIGNAL = /\b(error|err=|fail(ed|ure)?|panic|fatal|warn(ing)?|level=(ERROR|WARN)|refused|timeout|timed out|denied)\b/i;

// Daemon log lines most likely to explain the failure: lines naming the session
// or agent that also look like an error, falling back to recent error lines.
export function daemonLogExcerpt(logs, { sessionId = null, agent = null, max = 25 } = {}) {
  const lines = String(logs || "").split("\n").filter((line) => line.trim());
  const scoped = lines.filter((line) => LOG_SIGNAL.test(line)
    && ((sessionId && line.includes(sessionId)) || (agent && line.includes(agent))));
  const picked = scoped.length ? scoped : lines.filter((line) => LOG_SIGNAL.test(line));
  return picked.slice(-max).map((line) => line.slice(0, 600));
}

const INSTRUCTION_GATES = new Set(["hidden_ao_instructions", "tui_system_prompt_file", "instruction_channel", "chat_instructions", "chat_instruction_channel", "role_worker", "role_orchestrator"]);

export function diagnoseGate(item, { agent = null, contract = null, sessionId = null, daemonLogs = "", instructionChannel = undefined } = {}) {
  if (!item || item.status === "PASS") return undefined;
  const capability = capabilityForGate(item.name);
  const hint = GATE_HINTS[item.name] ?? {};
  const support = supportOf(capability, contract);
  const observed = [item.reason, ...observedFromEvidence(item.evidence)].filter(Boolean);
  const causes = [...(hint.causes ?? [])];
  // The likeliest cause of an instruction failure depends on how AO delivers
  // the prompt to this harness; a flag hint is wrong for a rule-file harness.
  if (INSTRUCTION_GATES.has(item.name) && CHANNEL_CAUSES[instructionChannel]) {
    causes.unshift(`${instructionChannel} channel: ${CHANNEL_CAUSES[instructionChannel]}`);
  }
  if (support === "yes" && item.status === "UNSUPPORTED") {
    causes.unshift(`${capability.id} is required of every harness (or the contract declares it); UNSUPPORTED here is a missing integration`);
  }
  return {
    capability: capability?.id ?? null,
    capabilityTitle: capability?.title ?? null,
    declaredSupport: support,
    expected: hint.expected ?? null,
    observed,
    likelyCauses: causes,
    inspect: inspectPaths(capability, agent, contract),
    daemonLogExcerpt: daemonLogExcerpt(daemonLogs, { sessionId, agent }),
  };
}

// One line per failure for the runner log and terminal: enough to start fixing
// without opening the JSON.
export function diagnosisLine(agent, item, diagnosis) {
  const parts = [`${agent || "runner"}: ${item.name} ${item.status}${diagnosis?.failureClass ? ` (${diagnosis.failureClass})` : ""}`];
  if (diagnosis?.capabilityTitle) parts.push(`[${diagnosis.capabilityTitle}]`);
  if (diagnosis?.expected) parts.push(`expected ${diagnosis.expected}`);
  if (diagnosis?.observed?.length) parts.push(`observed ${diagnosis.observed.slice(0, 2).join("; ")}`);
  if (diagnosis?.likelyCauses?.length) parts.push(`likely ${diagnosis.likelyCauses[0]}`);
  if (diagnosis?.inspect?.length) parts.push(`inspect ${diagnosis.inspect[0]}`);
  return parts.join(" — ");
}

const STATUS_ORDER = ["FAIL", "BLOCKED", "NOT_RUN", "UNSUPPORTED", "PASS"];

// Rolls gate results up to one row per catalog capability. A capability with
// no recorded gate is NOT_RUN (or MANUAL for runner-excluded capabilities),
// never PASS.
export function capabilityMatrix(result, contract = null) {
  const byName = new Map((result?.gates ?? []).map((item) => [item.name, item]));
  return CAPABILITIES.map((capability) => {
    const support = supportOf(capability, contract);
    if (!capability.gates.length) {
      return { id: capability.id, title: capability.title, surface: capability.surface, support, status: "MANUAL", gates: [], reason: capability.manual };
    }
    const gates = capability.gates.map((name) => byName.get(name)).filter(Boolean);
    if (!gates.length) {
      return { id: capability.id, title: capability.title, surface: capability.surface, support, status: "NOT_RUN", gates: [], reason: "no gate for this capability was recorded in this run" };
    }
    const status = STATUS_ORDER.find((value) => gates.some((item) => item.status === value)) ?? "NOT_RUN";
    const worst = gates.find((item) => item.status === status);
    return {
      id: capability.id, title: capability.title, surface: capability.surface, support, status,
      gates: gates.map((item) => ({ name: item.name, status: item.status })),
      reason: status === "PASS" ? `${gates.length} gate(s) passed` : `${worst.name}: ${worst.reason}`,
      ...(worst?.evidencePath ? { evidencePath: worst.evidencePath } : {}),
    };
  });
}

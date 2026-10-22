// Capability suites that run after the TUI lifecycle: TUI extras (static
// evidence, no provider turns), the Chat suite and the TUI ↔ Chat interface
// switch. Each returns gates in the runner's format; every non-pass gate says
// what was expected and carries the API evidence that disproved it.
//
// This module imports runner helpers from audit_ao.mjs, which imports the
// suites back. The cycle is safe because neither module touches the other's
// bindings at load time.

import { access, mkdir, readFile, rm } from "node:fs/promises";
import { homedir } from "node:os";
import { join, resolve } from "node:path";
import {
  CANCELLATION_PROMPT,
  CANCELLATION_WITNESS,
  apiEvidence,
  cancellationProcessState,
  expandHome,
  gate,
  gateRecorder,
  hash,
  normalizedPath,
  parseCancellationStart,
  refreshRestoreInstructions,
  parseProof,
  request,
  runCommand,
  sessionState,
  sleep,
  token,
  waitForRestoredTerminalReady,
  waitUntil,
  workspaceFile,
} from "./audit_ao.mjs";
import { capabilitySupport } from "./audit_capabilities.mjs";
import { checkInstructionDelivery, instructionSpec } from "./audit_instructions.mjs";

export const EXTRA_GATES = ["ao_models_refresh", "model_efforts", "tui_system_prompt_file", "instruction_channel", "instruction_channel_restore", "ao_skill_pointer", "tui_hooks_installed", "tui_usage_recorded"];

export const CHAT_GATES = [
  "chat_spawn",
  "chat_controller_ready",
  "chat_initial_turn",
  "chat_instructions",
  "chat_instruction_channel",
  "chat_mcp_servers",
  "chat_turn_lifecycle",
  "chat_followup",
  "chat_idempotent_send",
  "chat_models",
  "chat_settings",
  "chat_skills",
  "chat_usage",
  "chat_steer",
  "chat_interrupt",
  "chat_post_interrupt_continuation",
  "chat_approval_reject",
  "chat_approval_approve",
  "chat_kill",
  "chat_restore",
  "chat_restore_continuation",
  "chat_system_prompt_restore",
  "chat_compact",
];

export const IFACE_GATES = ["iface_status", "iface_tui_to_chat", "iface_chat_continuation", "iface_chat_to_tui", "iface_tui_continuation"];

export const ROLE_GATES = ["role_worker", "role_orchestrator"];

export const SUITES = Object.freeze({ extras: EXTRA_GATES, chat: CHAT_GATES, interface: IFACE_GATES, roles: ROLE_GATES });

const CHAT_PROOF = "AO_AUDIT_CHAT_PROOF.json";
const TERMINAL_TURN_STATES = new Set(["completed", "recovered", "interrupted", "failed", "cancelled"]);
const TRANSITION_TERMINAL_PHASES = new Set(["completed", "failed", "cancelled", "recovery_required"]);

export function notRunGates(names, reason) {
  return names.map((name) => gate(name, "NOT_RUN", reason));
}

// Records a gate and, when a prerequisite failed, records every later gate in
// the suite as NOT_RUN naming that prerequisite, so the report shows the chain.
function suiteRecorder(names, gates, onIssue, context) {
  const record = gateRecorder(gates, onIssue, context);
  const skipRest = async (reason) => {
    const present = new Set(gates.map((item) => item.name));
    for (const name of names) if (!present.has(name)) await record(gate(name, "NOT_RUN", reason));
  };
  return { record, skipRest };
}

async function sqliteValue(dataDir, sql, timeoutMs = 10_000) {
  if (!dataDir) return { ok: false, value: "", reason: "--data-dir is required" };
  const db = join(resolve(expandHome(dataDir)), "ao.db");
  const outcome = await runCommand("sqlite3", ["-readonly", db, sql], { timeoutMs });
  if (!outcome.ok) return { ok: false, value: "", reason: outcome.stderr || `sqlite3 exited ${outcome.code}` };
  return { ok: true, value: outcome.stdout.trim() };
}

function quote(value) {
  return `'${String(value).replaceAll("'", "''")}'`;
}

async function readTextFile(path) {
  try {
    return { exists: true, text: await readFile(path, "utf8") };
  } catch (error) {
    return { exists: false, text: "", error: error?.code || String(error) };
  }
}

// ---------------------------------------------------------------- extras

// Evidence that needs no provider turn: model refresh/efforts, prompt and
// skill files on disk, hook installation and usage accounting for the TUI
// session the lifecycle already drove.
export async function auditExtras({ baseURL, agent, dataDir, sessionId, workspacePath, scenario, contract = null, statusOf = () => undefined, timeoutMs, onIssue }) {
  const gates = [];
  const { record } = suiteRecorder(EXTRA_GATES, gates, onIssue, () => ({ agent, baseURL, sessionId }));
  const bounded = Math.min(timeoutMs, 60_000);

  const refresh = await request({ baseURL, method: "POST", path: `agents/${encodeURIComponent(agent)}/models/refresh`, body: {}, timeoutMs: bounded });
  const models = Array.isArray(refresh.response?.models) ? refresh.response.models : [];
  await record(gate(
    "ao_models_refresh",
    refresh.ok && models.length ? "PASS" : "FAIL",
    refresh.ok && models.length ? `catalog refresh returned ${models.length} model(s)` : "model catalog refresh failed or returned no models",
    { ...apiEvidence(refresh), response: undefined, modelIds: models.map((item) => item?.id).filter(Boolean) },
  ));
  const withEfforts = models.filter((item) => Array.isArray(item?.efforts) && item.efforts.length);
  const badDefault = withEfforts.filter((item) => item.defaultEffort && !item.efforts.includes(item.defaultEffort));
  await record(gate(
    "model_efforts",
    !models.length ? "NOT_RUN" : withEfforts.length && !badDefault.length ? "PASS" : "FAIL",
    !models.length ? "no refreshed catalog to inspect"
      : withEfforts.length && !badDefault.length ? `${withEfforts.length} model(s) advertise reasoning efforts`
      : badDefault.length ? `defaultEffort is not in efforts for: ${badDefault.map((item) => item.id).join(", ")}`
      : "no model advertises reasoning efforts; effort pickers will be empty",
    { efforts: withEfforts.map((item) => ({ id: item.id, efforts: item.efforts, defaultEffort: item.defaultEffort })) },
  ));

  if (!sessionId) {
    for (const name of ["tui_system_prompt_file", "instruction_channel", "instruction_channel_restore", "ao_skill_pointer", "tui_hooks_installed", "tui_usage_recorded"]) {
      await record(gate(name, "NOT_RUN", "no TUI session was created"));
    }
    return gates;
  }

  const promptPath = join(resolve(expandHome(dataDir)), "prompts", sessionId, "system.md");
  const prompt = await readTextFile(promptPath);
  const carriesRules = prompt.text.includes(scenario.hiddenInitialToken);
  await record(gate(
    "tui_system_prompt_file",
    prompt.exists && carriesRules ? "PASS" : "FAIL",
    !prompt.exists ? `system prompt file is missing (${prompt.error})`
      : carriesRules ? "system.md exists and carries the project agentRules token (delivery to the CLI is checked by instruction_channel)"
      : "system.md exists but does not contain the project agentRules token",
    { path: promptPath, bytes: prompt.text.length, hiddenTokenSha256: hash(scenario.hiddenInitialToken) },
  ));

  // Generating system.md is not delivery: prove the CLI receives it through
  // the declared channel, at launch and again after restore.
  const tuiSpec = instructionSpec(agent, contract, "tui");
  const delivery = { spec: tuiSpec, dataDir: resolve(expandHome(dataDir)), workspacePath, sessionId };
  const initialDelivery = await checkInstructionDelivery({ ...delivery, token: scenario.hiddenInitialToken,
    effectGate: "hidden_ao_instructions", effectPassed: statusOf("hidden_ao_instructions") === "PASS" });
  await record(gate("instruction_channel", initialDelivery.status, initialDelivery.reason, initialDelivery.evidence));
  if (statusOf("native_restore") !== "PASS") {
    await record(gate("instruction_channel_restore", "NOT_RUN", "the TUI session was not killed and restored in this run"));
  } else {
    const restored = await checkInstructionDelivery({ ...delivery, token: scenario.hiddenRestoreToken,
      effectGate: "system_prompt_restore", effectPassed: statusOf("system_prompt_restore") === "PASS",
      promptText: tuiSpec?.channel === "flag" ? (await readTextFile(promptPath)).text : null });
    await record(gate("instruction_channel_restore", restored.status, restored.reason, restored.evidence));
  }

  const skillPath = join(resolve(expandHome(dataDir)), "skills", "using-ao", "SKILL.md");
  const skill = await readTextFile(skillPath);
  const pointed = prompt.text.includes("using-ao");
  await record(gate(
    "ao_skill_pointer",
    skill.exists && pointed ? "PASS" : "FAIL",
    !skill.exists ? `AO skill asset is missing at ${skillPath}`
      : pointed ? "using-ao skill is installed and system.md points at it"
      : "using-ao skill is installed but system.md does not reference it",
    { skillPath, systemPromptPath: promptPath },
  ));

  if (agent === "claude-code") {
    const hooksPath = join(workspacePath || "", ".claude", "settings.local.json");
    const hooks = workspacePath ? await readTextFile(hooksPath) : { exists: false, text: "", error: "no workspace path" };
    const events = ["SessionStart", "UserPromptSubmit", "Stop"].filter((name) => hooks.text.includes(name));
    const installed = hooks.exists && hooks.text.includes("hooks claude-code") && events.length === 3;
    await record(gate(
      "tui_hooks_installed",
      installed ? "PASS" : "FAIL",
      installed ? "Claude hooks file wires SessionStart/UserPromptSubmit/Stop to `ao hooks claude-code`"
        : hooks.exists ? `hooks file is missing AO hook commands (found events: ${events.join(", ") || "none"})`
        : `hooks file is missing (${hooks.error})`,
      { path: hooksPath },
    ));
  } else {
    // Codex receives hooks as launch -c flags rather than a file, so the
    // persisted native id (set only by the SessionStart hook) is the witness.
    const native = await sqliteValue(dataDir, `SELECT agent_session_id FROM sessions WHERE id=${quote(sessionId)} LIMIT 1;`);
    await record(gate(
      "tui_hooks_installed",
      native.ok && native.value ? "PASS" : "FAIL",
      native.ok && native.value ? "hook-delivered native session id is persisted" : `no hook-delivered native session id (${native.reason || "empty"})`,
      { nativeSessionIdSha256: native.value ? hash(native.value) : undefined, qualification: "Codex hooks ride launch argv; persistence proves they fired." },
    ));
  }

  const usage = await waitUntil(async () => {
    const response = await request({ baseURL, path: `usage/sessions/${encodeURIComponent(sessionId)}`, timeoutMs: bounded });
    const totals = response.response?.totals ?? {};
    const tokens = (totals.inputTokens ?? 0) + (totals.outputTokens ?? 0);
    return { done: response.ok && tokens > 0, response, tokens };
  }, Math.min(timeoutMs, 30_000), 2_000);
  await record(gate(
    "tui_usage_recorded",
    usage.done ? "PASS" : "FAIL",
    usage.done ? `usage recorded ${usage.tokens} token(s) for the TUI session` : "no token usage recorded for the TUI session after provider turns",
    usage.response ? apiEvidence(usage.response) : undefined,
  ));
  return gates;
}

// ---------------------------------------------------------------- chat helpers

function snapshotOf(response) {
  const body = response?.response;
  return body?.conversation ?? body ?? null;
}

async function conversation(baseURL, sessionId, timeoutMs) {
  return request({ baseURL, path: `sessions/${encodeURIComponent(sessionId)}/conversation`, timeoutMs });
}

function runningTurns(snapshot) {
  return (snapshot?.turns ?? []).filter((turn) => turn.state === "running" || turn.state === "queued");
}

function pendingApprovals(snapshot) {
  return (snapshot?.activities ?? []).filter((item) => item.activityKind === "approval" && item.status === "pending" && item.requestId);
}

function chatIdle(snapshot) {
  return snapshot?.controller === "ready" && !runningTurns(snapshot).length && !pendingApprovals(snapshot).length;
}

function snapshotSummary(snapshot) {
  if (!snapshot) return null;
  const turns = snapshot.turns ?? [];
  return {
    controller: snapshot.controller,
    mode: snapshot.mode,
    capabilities: snapshot.capabilities,
    settings: snapshot.settings,
    threadState: snapshot.threadState,
    turnStates: turns.slice(-6).map((turn) => ({ id: turn.id, state: turn.state, errorMessage: turn.errorMessage })),
    pendingApprovals: pendingApprovals(snapshot).map((item) => ({ requestId: item.requestId, summary: item.summary })),
    lastAssistantText: [...(snapshot.messages ?? [])].reverse().find((item) => item.role === "assistant")?.text?.slice(-400),
  };
}

// Polls the conversation and the proof file together until `accept(proof,
// snapshot)` holds and the controller is idle. Remembers every controller and
// turn state it saw so a failure can say where the turn stopped.
async function waitChat({ baseURL, sessionId, file = CHAT_PROOF, timeoutMs, accept, intervalMs = 500 }) {
  const seen = { controller: new Set(), turnStates: new Set(), streaming: false };
  const outcome = await waitUntil(async () => {
    const [convResponse, fileResponse] = await Promise.all([
      conversation(baseURL, sessionId, 30_000),
      workspaceFile(baseURL, sessionId, file, 30_000),
    ]);
    const snapshot = snapshotOf(convResponse);
    if (snapshot?.controller) seen.controller.add(snapshot.controller);
    for (const turn of snapshot?.turns ?? []) seen.turnStates.add(turn.state);
    if ((snapshot?.messages ?? []).some((item) => item.role === "assistant" && item.streaming)) seen.streaming = true;
    const proof = parseProof(fileResponse);
    return { done: Boolean(accept(proof, snapshot) && chatIdle(snapshot)), proof, snapshot, convResponse, fileResponse };
  }, timeoutMs, intervalMs);
  return {
    ...outcome,
    seen: { controller: [...seen.controller], turnStates: [...seen.turnStates], streaming: seen.streaming },
    evidence: {
      conversation: outcome.convResponse ? { ...apiEvidence(outcome.convResponse), response: undefined } : undefined,
      snapshot: snapshotSummary(outcome.snapshot),
      proof: outcome.fileResponse ? apiEvidence(outcome.fileResponse) : undefined,
      seenControllerStates: [...seen.controller],
      seenTurnStates: [...seen.turnStates],
      timedOut: Boolean(outcome.timeout),
    },
  };
}

async function sendChat(baseURL, sessionId, text, clientMessageId = token("ao-audit-msg"), timeoutMs = 60_000) {
  return request({ baseURL, method: "POST", path: `sessions/${encodeURIComponent(sessionId)}/conversation/messages`, body: { text, clientMessageId }, timeoutMs });
}

function chatInitialPrompt(chat) {
  return [
    `AO chat audit request token: ${chat.initialToken}`,
    `History-only token (do not write this token to any file yet): ${chat.historyToken}`,
    `Create ${CHAT_PROOF} in your current working directory as raw JSON, without markdown fences.`,
    "It must be in that working directory (run pwd), not in the AO session artifacts folder: this is a harness test file.",
    `If ${CHAT_PROOF} already exists, preserve it and increment initialPromptExecutions; otherwise set initialPromptExecutions to 1.`,
    "Include: cwd (the output of pwd), initialPromptToken (the request token above) and initialPromptExecutions.",
    "Follow this project's conventions for PROOF.json files. Do not include the history-only token. Then stop.",
  ].join("\n");
}

function updateProof(fields) {
  const list = Object.entries(fields).map(([key, value]) => `${key} = ${JSON.stringify(value)}`).join(", ");
  return `Update ${CHAT_PROOF}: set ${list}. Preserve every existing field, write raw JSON only, then stop.`;
}

function pickDecision(decisions, wanted) {
  const list = Array.isArray(decisions) ? decisions : [];
  const kinds = wanted === "reject" ? ["reject_once", "reject_always"] : ["allow_once", "allow_always"];
  const pattern = wanted === "reject" ? /reject|deny|decline|cancel|abort|no\b/i : /accept|allow|approve|yes|proceed/i;
  return list.find((item) => item?.kind === kinds[0])
    ?? list.find((item) => item?.kind === kinds[1])
    ?? list.find((item) => !item?.kind && (pattern.test(item?.id ?? "") || pattern.test(item?.label ?? "")))
    ?? null;
}

// ---------------------------------------------------------------- chat suite

export async function auditChatSuite({ baseURL, agent, projectId, dataDir, timeoutMs, scenario, only = null, contract = null, onIssue, onSession }) {
  const gates = [];
  const context = { agent, baseURL, sessionId: null };
  const { record, skipRest } = suiteRecorder(CHAT_GATES, gates, onIssue, () => context);
  const chat = {
    initialToken: token("AO_CHAT_INITIAL"),
    historyToken: token("AO_CHAT_HISTORY"),
    secondToken: token("AO_CHAT_SECOND"),
    steerToken: token("AO_CHAT_STEER"),
    afterInterruptToken: token("AO_CHAT_AFTER_INTERRUPT"),
    afterCompactToken: token("AO_CHAT_AFTER_COMPACT"),
    restoreHiddenToken: token("AO_CHAT_RESTORE_HIDDEN"),
  };
  const support = capabilitySupport(agent, "chat_spawn");

  const spawn = await request({
    baseURL, method: "POST", path: "sessions", timeoutMs,
    body: {
      projectId, kind: "worker", harness: agent, mode: "chat", approvalMode: "bypass-permissions",
      displayName: `audit-chat-${agent}`.slice(0, 100), prompt: chatInitialPrompt(chat),
    },
  });
  const session = spawn.response?.session;
  const sessionId = typeof session?.id === "string" ? session.id : "";
  if (sessionId) {
    context.sessionId = sessionId;
    await onSession?.({ agent, sessionId });
  }
  if (!spawn.ok || !sessionId || session.mode !== "chat") {
    const refusal = spawn.status >= 400 && spawn.status < 500 && support !== "yes";
    await record(gate("chat_spawn", refusal ? "UNSUPPORTED" : "FAIL",
      refusal ? "AO truthfully refused a Chat session for a harness without a registered Chat driver"
        : session && session.mode !== "chat" ? `AO created mode=${session.mode} instead of chat (silent fallback)` : "AO did not create the Chat session",
      apiEvidence(spawn)));
    await skipRest("Chat spawn did not produce a chat session");
    return { gates, sessionId };
  }
  await record(gate("chat_spawn", "PASS", "AO created a mode=chat session", apiEvidence(spawn)));

  const workspaceResponse = await request({ baseURL, path: `desktop/sessions/${encodeURIComponent(sessionId)}/workspace`, timeoutMs });
  const workspacePath = workspaceResponse.response?.workspacePath;

  const first = await waitChat({ baseURL, sessionId, timeoutMs, accept: (proof) => proof?.initialPromptToken === chat.initialToken });
  const readySeen = first.seen.controller.includes("ready");
  await record(gate("chat_controller_ready", readySeen ? "PASS" : "FAIL",
    readySeen ? `controller reached ready (states seen: ${first.seen.controller.join(" → ")})`
      : `controller never reached ready (states seen: ${first.seen.controller.join(" → ") || "none"})`,
    first.evidence));
  if (!readySeen) {
    await skipRest("Chat controller never became ready");
    return { gates, sessionId, workspacePath };
  }
  const capabilities = new Set(first.snapshot?.capabilities ?? []);

  let cwdMatches = false;
  if (typeof first.proof?.cwd === "string" && typeof workspacePath === "string") {
    cwdMatches = (await normalizedPath(first.proof.cwd)) === (await normalizedPath(workspacePath));
  }
  const initialOK = first.done && first.proof?.initialPromptExecutions === 1 && cwdMatches;
  await record(gate("chat_initial_turn", initialOK ? "PASS" : "FAIL",
    initialOK ? "initial Chat prompt ran once in the AO worktree"
      : !first.proof ? `${CHAT_PROOF} was not written before timeout`
      : !cwdMatches ? `provider cwd ${first.proof.cwd} is not the AO worktree ${workspacePath}`
      : `initialPromptExecutions is ${first.proof.initialPromptExecutions}, expected 1`,
    { ...first.evidence, workspace: apiEvidence(workspaceResponse), reportedCwd: first.proof?.cwd }));
  const instructionsOK = first.proof?.rulesMarker === scenario.hiddenInitialToken && first.proof?.agentsMarker === scenario.agentsToken;
  await record(gate("chat_instructions", instructionsOK ? "PASS" : "FAIL",
    instructionsOK ? "Chat provider consumed AO agentRules and project AGENTS.md"
      : `instruction tokens mismatched (rulesMarker from agentRules: ${first.proof?.rulesMarker === scenario.hiddenInitialToken ? "ok" : "missing"}, agentsMarker from AGENTS.md: ${first.proof?.agentsMarker === scenario.agentsToken ? "ok" : "missing"})`,
    { hiddenTokenSha256: hash(scenario.hiddenInitialToken), agentsTokenSha256: hash(scenario.agentsToken) }));
  const chatSpec = instructionSpec(agent, contract, "chat");
  const chatDelivery = await checkInstructionDelivery({ spec: chatSpec, token: scenario.hiddenInitialToken, dataDir: resolve(expandHome(dataDir || ".")),
    workspacePath, sessionId, effectGate: "chat_instructions", effectPassed: first.proof?.rulesMarker === scenario.hiddenInitialToken });
  await record(gate("chat_instruction_channel", chatDelivery.status, chatDelivery.reason, chatDelivery.evidence));
  if (!first.done) {
    await skipRest("initial Chat turn did not complete");
    return { gates, sessionId, workspacePath, capabilities: [...capabilities] };
  }

  const mcpServers = Array.isArray(first.snapshot?.mcpServers) ? first.snapshot.mcpServers : [];
  const mcpSupport = capabilitySupport(agent, "chat_mcp");
  await record(gate("chat_mcp_servers",
    mcpServers.length ? "PASS" : mcpSupport === "yes" ? "FAIL" : "UNSUPPORTED",
    mcpServers.length ? `snapshot lists ${mcpServers.length} MCP server(s)`
      : mcpSupport === "yes" ? "snapshot lists no MCP servers although AO attaches its MCP server to this driver"
      : "driver does not report MCP server status in the snapshot",
    { mcpServers }));

  // Under --only the core chain (spawn → initial turn) always runs; optional
  // steps run only when one of their gates is in focus.
  const want = (...names) => !only || names.some((name) => only.has(name));
  const followupRan = want("chat_turn_lifecycle", "chat_followup", "chat_idempotent_send");
  const latestSnapshot = followupRan ? (await auditChatFollowup({ baseURL, sessionId, chat, timeoutMs, record })) ?? first.snapshot : first.snapshot;
  if (want("chat_models", "chat_settings")) await auditChatModelSettings({ baseURL, sessionId, dataDir, timeoutMs, record });

  if (capabilities.has("skills")) {
    const skills = await request({ baseURL, path: `sessions/${encodeURIComponent(sessionId)}/conversation/skills`, timeoutMs });
    const list = skills.response?.skills;
    await record(gate("chat_skills", skills.ok && Array.isArray(list) ? "PASS" : "FAIL",
      skills.ok && Array.isArray(list) ? `skills capability listed ${list.length} skill(s)` : "skills capability is advertised but listing failed",
      { ...apiEvidence(skills), response: undefined, skills: Array.isArray(list) ? list.map((item) => item?.name ?? item?.id) : undefined }));
  } else {
    await record(gate("chat_skills", "UNSUPPORTED", "snapshot capabilities do not advertise skills", { capabilities: [...capabilities] }));
  }

  const usage = await request({ baseURL, path: `usage/sessions/${encodeURIComponent(sessionId)}`, timeoutMs });
  const totals = usage.response?.totals ?? {};
  const snapUsage = latestSnapshot?.usage ?? {};
  const usageTokens = (totals.inputTokens ?? 0) + (totals.outputTokens ?? 0) + (snapUsage.inputTokens ?? 0) + (snapUsage.outputTokens ?? 0);
  await record(gate("chat_usage", usageTokens > 0 ? "PASS" : "FAIL",
    usageTokens > 0 ? `Chat usage recorded (${usageTokens} tokens across usage API and snapshot)` : "no Chat token usage in the usage API or conversation snapshot",
    { usage: apiEvidence(usage), snapshotUsage: snapUsage }));

  if (want("chat_steer")) await auditChatSteer({ baseURL, sessionId, capabilities, chat, timeoutMs, record, agent });
  if (want("chat_interrupt", "chat_post_interrupt_continuation")) await auditChatInterrupt({ baseURL, sessionId, chat, timeoutMs, record });
  if (want("chat_approval_reject", "chat_approval_approve")) await auditChatApprovals({ baseURL, sessionId, timeoutMs, record });
  if (want("chat_kill", "chat_restore", "chat_restore_continuation", "chat_system_prompt_restore", "chat_compact")) {
    await auditChatKillRestore({ baseURL, sessionId, projectId, dataDir, workspacePath, chat, chatSpec, followupRan, timeoutMs, record });
  }

  const restored = gates.find((item) => item.name === "chat_restore_continuation")?.status === "PASS";
  if (!want("chat_compact")) {
    // Out of --only focus: the final skipRest records it and the runner's scope drops it.
  } else if (!capabilities.has("compaction")) {
    await record(gate("chat_compact", "UNSUPPORTED", "snapshot capabilities do not advertise compaction", { capabilities: [...capabilities] }));
  } else if (!restored) {
    await record(gate("chat_compact", "NOT_RUN", "restored Chat conversation was not usable"));
  } else {
    const compact = await request({ baseURL, method: "POST", path: `sessions/${encodeURIComponent(sessionId)}/conversation/compact`, body: {}, timeoutMs });
    const compacted = compact.ok ? await waitChat({ baseURL, sessionId, timeoutMs, accept: (_, snapshot) => Boolean(snapshot?.compactedAt) }) : { done: false, evidence: {} };
    const after = compacted.done ? await sendChat(baseURL, sessionId, updateProof({ afterCompactToken: chat.afterCompactToken })) : null;
    const answered = after?.ok ? await waitChat({ baseURL, sessionId, timeoutMs, accept: (proof) => proof?.afterCompactToken === chat.afterCompactToken }) : { done: false, evidence: {} };
    await record(gate("chat_compact", compact.ok && compacted.done && answered.done ? "PASS" : "FAIL",
      !compact.ok ? "compact request was rejected"
        : !compacted.done ? "compactedAt never appeared on the snapshot"
        : !answered.done ? "conversation did not answer after compaction" : "conversation compacted and kept working",
      { compact: apiEvidence(compact), compacted: compacted.evidence, after: answered.evidence }));
  }
  await skipRest("prerequisite Chat step did not complete");
  return { gates, sessionId, workspacePath, capabilities: [...capabilities] };
}

async function auditChatFollowup({ baseURL, sessionId, chat, timeoutMs, record }) {
  // Follow-up: poll quickly so the running state and streaming are visible.
  const clientMessageId = token("ao-audit-followup");
  const followup = await sendChat(baseURL, sessionId, updateProof({ secondMessageToken: chat.secondToken }), clientMessageId);
  const second = followup.ok
    ? await waitChat({ baseURL, sessionId, timeoutMs, intervalMs: 200, accept: (proof) => proof?.secondMessageToken === chat.secondToken })
    : { done: false, seen: { turnStates: [], streaming: false }, evidence: {} };
  const sawRunning = second.seen.turnStates.includes("running");
  const finalTurn = (second.snapshot?.turns ?? []).at(-1);
  await record(gate("chat_turn_lifecycle",
    sawRunning && finalTurn?.state === "completed" ? "PASS" : followup.ok ? "FAIL" : "NOT_RUN",
    !followup.ok ? "follow-up was not accepted"
      : sawRunning && finalTurn?.state === "completed" ? `turn observed running → completed${second.seen.streaming ? " with streaming output" : ""}`
      : `turn states seen: ${second.seen.turnStates.join(", ") || "none"}; last turn ${finalTurn?.state ?? "missing"}${finalTurn?.errorMessage ? ` (${finalTurn.errorMessage})` : ""}`,
    { streamingObserved: second.seen.streaming, ...second.evidence }));
  await record(gate("chat_followup", followup.ok && second.done ? "PASS" : "FAIL",
    followup.ok && second.done ? "follow-up message mutated the Chat proof" : !followup.ok ? "follow-up message was rejected" : "follow-up did not mutate the Chat proof before timeout",
    { send: apiEvidence(followup), ...second.evidence }));

  if (followup.ok) {
    const turnsBefore = (second.snapshot?.turns ?? []).length;
    const repeat = await sendChat(baseURL, sessionId, updateProof({ secondMessageToken: chat.secondToken }), clientMessageId);
    await sleep(1_500);
    const after = snapshotOf(await conversation(baseURL, sessionId, 30_000));
    const turnsAfter = (after?.turns ?? []).length;
    const idempotent = repeat.ok && repeat.response?.duplicate === true && turnsAfter === turnsBefore;
    await record(gate("chat_idempotent_send", idempotent ? "PASS" : "FAIL",
      idempotent ? "repeated clientMessageId was recognised as a duplicate"
        : `repeat send returned duplicate=${repeat.response?.duplicate}; turns ${turnsBefore} → ${turnsAfter}`,
      { send: apiEvidence(repeat), turnsBefore, turnsAfter }));
    if (!idempotent) await waitChat({ baseURL, sessionId, timeoutMs, accept: () => true });
  } else {
    await record(gate("chat_idempotent_send", "NOT_RUN", "follow-up was not accepted"));
  }
  return second.snapshot;
}

async function auditChatModelSettings({ baseURL, sessionId, dataDir, timeoutMs, record }) {
  // Models and settings.
  const modelsResponse = await request({ baseURL, path: `sessions/${encodeURIComponent(sessionId)}/conversation/models`, timeoutMs });
  const chatModels = Array.isArray(modelsResponse.response?.models) ? modelsResponse.response.models : [];
  const selected = modelsResponse.response?.selected ?? {};
  await record(gate("chat_models", modelsResponse.ok && chatModels.length ? "PASS" : "FAIL",
    modelsResponse.ok && chatModels.length ? `Chat lists ${chatModels.length} model(s); selected ${selected.model || "(provider default)"}` : "Chat model list failed or was empty",
    { ...apiEvidence(modelsResponse), response: undefined, modelIds: chatModels.map((item) => item.id), selected }));
  const target = chatModels.find((item) => item.id === selected.model) ?? chatModels.find((item) => item.default) ?? chatModels[0];
  if (target) {
    const efforts = Array.isArray(target.efforts) ? target.efforts : [];
    const effort = efforts.find((item) => item !== selected.reasoningEffort) ?? efforts[0];
    const wanted = { model: target.id, ...(effort ? { reasoningEffort: effort } : {}) };
    const patch = await request({ baseURL, method: "PATCH", path: `sessions/${encodeURIComponent(sessionId)}/conversation/settings`, body: wanted, timeoutMs });
    const readback = snapshotOf(await conversation(baseURL, sessionId, 30_000))?.settings ?? {};
    const persisted = patch.ok && readback.model === wanted.model && (!effort || readback.reasoningEffort === effort);
    const column = await sqliteValue(dataDir, `SELECT reasoning_effort FROM conversations WHERE session_id=${quote(sessionId)} LIMIT 1;`);
    await record(gate("chat_settings", persisted ? "PASS" : "FAIL",
      persisted ? `model ${wanted.model}${effort ? ` / effort ${effort}` : ""} persisted and read back`
        : `requested ${JSON.stringify(wanted)}, snapshot settings are ${JSON.stringify(readback)}`,
      { patch: apiEvidence(patch), readback, storedReasoningEffort: column.ok ? column.value : column.reason }));
  } else {
    await record(gate("chat_settings", "NOT_RUN", "no Chat model to select"));
  }
}

async function auditChatSteer({ baseURL, sessionId, capabilities, chat, timeoutMs, record, agent }) {
  const path = `sessions/${encodeURIComponent(sessionId)}/conversation/steer`;
  if (!capabilities.has("steer")) {
    // Without the capability AO must refuse instead of silently queueing.
    const refusal = await request({ baseURL, method: "POST", path, body: { text: "AO audit steer refusal probe", clientMessageId: token("ao-audit-steer-probe") }, timeoutMs: 30_000 });
    const declared = capabilitySupport(agent, "chat_steer");
    await record(gate("chat_steer", refusal.ok ? "FAIL" : declared === "yes" ? "FAIL" : "UNSUPPORTED",
      refusal.ok ? "steer was accepted although the snapshot does not advertise steer"
        : declared === "yes" ? "AO declares steer for this driver but the session does not advertise it"
        : "steer is not advertised and AO refused it truthfully",
      { refusal: apiEvidence(refusal), capabilities: [...capabilities] }));
    return;
  }
  const start = await sendChat(baseURL, sessionId, [
    "Run this exact foreground shell command and wait for it to exit: node -e \"setTimeout(()=>{},20000)\"",
    `After it exits, ${updateProof({ steerBaseline: true }).replace(/^Update/, "update")}`,
  ].join("\n"));
  const running = start.ok ? await waitUntil(async () => {
    const snapshot = snapshotOf(await conversation(baseURL, sessionId, 30_000));
    const turn = (snapshot?.turns ?? []).find((item) => item.state === "running");
    return { done: Boolean(turn), turn, snapshot };
  }, Math.min(timeoutMs, 60_000), 250) : { done: false };
  if (!running.done) {
    await record(gate("chat_steer", "FAIL", start.ok ? "no running turn appeared to steer into" : "steer baseline message was rejected", { send: apiEvidence(start) }));
    await waitChat({ baseURL, sessionId, timeoutMs, accept: () => true });
    return;
  }
  const steer = await request({ baseURL, method: "POST", path, body: { text: `Also, in the same task, set steerToken = ${JSON.stringify(chat.steerToken)} in ${CHAT_PROOF}.`, clientMessageId: token("ao-audit-steer") }, timeoutMs: 30_000 });
  const done = await waitChat({ baseURL, sessionId, timeoutMs, accept: (proof) => proof?.steerBaseline === true && proof?.steerToken === chat.steerToken });
  const sameTurn = steer.response?.providerTurnId && running.turn.providerTurnId ? steer.response.providerTurnId === running.turn.providerTurnId : null;
  await record(gate("chat_steer", steer.ok && done.done && sameTurn !== false ? "PASS" : "FAIL",
    !steer.ok ? "steer was rejected while a turn was running"
      : !done.done ? "steered instruction did not reach the proof"
      : sameTurn === false ? `steer landed in turn ${steer.response.providerTurnId}, not the running turn ${running.turn.providerTurnId}`
      : "steer reached the running turn and its instruction was applied",
    { steer: apiEvidence(steer), runningTurnId: running.turn.providerTurnId, ...done.evidence }));
}

async function auditChatInterrupt({ baseURL, sessionId, chat, timeoutMs, record }) {
  const spec = CANCELLATION_WITNESS;
  const start = await sendChat(baseURL, sessionId, CANCELLATION_PROMPT);
  const active = start.ok ? await waitUntil(async () => {
    const [convResponse, startWitness] = await Promise.all([
      conversation(baseURL, sessionId, 30_000),
      workspaceFile(baseURL, sessionId, spec.startPath, 30_000),
    ]);
    const snapshot = snapshotOf(convResponse);
    const witness = parseCancellationStart(startWitness, spec);
    return { done: Boolean(witness && runningTurns(snapshot).length), witness, snapshot, startWitness };
  }, Math.min(timeoutMs, 120_000), 250) : { done: false };
  const processBefore = active.witness ? await cancellationProcessState(active.witness.pid, active.witness.processIdentitySha256) : null;
  if (!active.done || !processBefore?.running || !processBefore.sameIdentity) {
    await record(gate("chat_interrupt", "FAIL",
      !start.ok ? "cancellation task was rejected"
        : !active.witness ? "provider never started the witnessed foreground command (no start witness file)"
        : "witnessed command was not running in an active turn before interrupt",
      { send: apiEvidence(start), snapshot: snapshotSummary(active.snapshot), witness: active.witness, process: processBefore }));
    await waitChat({ baseURL, sessionId, timeoutMs, accept: () => true });
  } else {
    const interrupt = await request({ baseURL, method: "POST", path: `sessions/${encodeURIComponent(sessionId)}/conversation/interrupt`, body: {}, timeoutMs: 30_000 });
    const settled = await waitChat({ baseURL, sessionId, file: spec.startPath, timeoutMs: Math.min(timeoutMs, 60_000), accept: () => true });
    const lastTurn = (settled.snapshot?.turns ?? []).at(-1);
    const stopped = await waitUntil(async () => {
      const state = await cancellationProcessState(active.witness.pid, active.witness.processIdentitySha256);
      return { done: !state.running || !state.sameIdentity, state };
    }, 10_000, 250);
    await sleep(spec.lateSideEffectDelayMs + 1_500);
    const late = await workspaceFile(baseURL, sessionId, spec.latePath, 30_000);
    const lateAbsent = late.ok === false && late.status === 404;
    const turnInterrupted = ["interrupted", "cancelled"].includes(lastTurn?.state);
    const passed = interrupt.ok && settled.done && turnInterrupted && stopped.done && lateAbsent;
    await record(gate("chat_interrupt", passed ? "PASS" : "FAIL",
      passed ? "interrupt stopped the witnessed command, ended the turn and prevented the late side effect"
        : !interrupt.ok ? "interrupt request was rejected"
        : !settled.done ? "controller did not settle after interrupt"
        : !turnInterrupted ? `turn ended ${lastTurn?.state ?? "unknown"}, expected interrupted/cancelled`
        : !stopped.done ? `witnessed command pid ${active.witness.pid} is still running after interrupt (process not reaped)`
        : "late side-effect file appeared: the command kept running after interrupt",
      { interrupt: apiEvidence(interrupt), turn: lastTurn, process: stopped.state, late: apiEvidence(late), ...settled.evidence }));
  }
  const after = await sendChat(baseURL, sessionId, updateProof({ afterInterruptToken: chat.afterInterruptToken }));
  const continued = after.ok ? await waitChat({ baseURL, sessionId, timeoutMs, accept: (proof) => proof?.afterInterruptToken === chat.afterInterruptToken }) : { done: false, evidence: {} };
  await record(gate("chat_post_interrupt_continuation", after.ok && continued.done ? "PASS" : "FAIL",
    after.ok && continued.done ? "same conversation completed a new turn after interrupt" : "conversation did not complete a turn after interrupt",
    { send: apiEvidence(after), ...continued.evidence }));
}

async function auditChatApprovals({ baseURL, sessionId, timeoutMs, record }) {
  const settingsPath = `sessions/${encodeURIComponent(sessionId)}/conversation/settings`;
  const mode = await request({ baseURL, method: "PATCH", path: settingsPath, body: { approvalMode: "accept-edits" }, timeoutMs });
  if (!mode.ok) {
    for (const name of ["chat_approval_reject", "chat_approval_approve"]) {
      await record(gate(name, "FAIL", "could not switch the conversation to accept-edits", apiEvidence(mode)));
    }
    return;
  }
  // The target sits OUTSIDE the worktree (under ~/.ao): accept-edits runs
  // Codex in a workspace-write sandbox, so a write inside the worktree is
  // allowed without asking and would prove nothing.
  const outside = process.env.AO_AUDIT_APPROVAL_DIR || join(homedir(), ".ao", "audit-approvals");
  await mkdir(outside, { recursive: true });
  const exists = (path) => access(path).then(() => true, () => false);
  for (const [name, wanted] of [["chat_approval_reject", "reject"], ["chat_approval_approve", "approve"]]) {
    const file = join(outside, `${sessionId}-${wanted}-${token("x").slice(2, 10)}.txt`);
    const prompt = [
      `Use your shell/command tool (not a file-editing tool) to run exactly: node -e "require('fs').writeFileSync(${JSON.stringify(file).replaceAll('"', "'")},'ao-audit')"`,
      "If the command is denied, do not retry it and do not create the file any other way; just stop.",
    ].join("\n");
    const send = await sendChat(baseURL, sessionId, prompt);
    const pending = send.ok ? await waitUntil(async () => {
      const convResponse = await conversation(baseURL, sessionId, 30_000);
      const written = await exists(file);
      const snapshot = snapshotOf(convResponse);
      const approval = pendingApprovals(snapshot)[0];
      const settledWithoutApproval = !approval && runningTurns(snapshot).length === 0 && (snapshot?.turns ?? []).length > 0 && chatIdle(snapshot);
      return { done: Boolean(approval) || (settledWithoutApproval && written), approval, snapshot, written };
    }, Math.min(timeoutMs, 120_000), 500) : { done: false };
    if (!pending.approval) {
      const executed = pending.written === true;
      await record(gate(name, "FAIL",
        !send.ok ? "approval test message was rejected"
          : executed ? "a write outside the worktree ran without any approval request in accept-edits mode (approval mapping missing)"
          : "no approval request appeared before timeout",
        { send: apiEvidence(send), snapshot: snapshotSummary(pending.snapshot) }));
      await waitChat({ baseURL, sessionId, timeoutMs, accept: () => true });
      continue;
    }
    const decisions = pending.approval.detail?.decisions;
    const decision = pickDecision(decisions, wanted);
    if (!decision) {
      await record(gate(name, "BLOCKED", `provider offered no ${wanted} decision`, { requestId: pending.approval.requestId, decisions }));
      const fallback = pickDecision(decisions, wanted === "reject" ? "approve" : "reject") ?? decisions?.[0];
      if (fallback) await request({ baseURL, method: "POST", path: `sessions/${encodeURIComponent(sessionId)}/conversation/approvals/${encodeURIComponent(pending.approval.requestId)}/resolve`, body: { decisionId: fallback.id }, timeoutMs });
      await waitChat({ baseURL, sessionId, timeoutMs, accept: () => true });
      continue;
    }
    const sideEffectBefore = await exists(file);
    const resolveResponse = await request({
      baseURL, method: "POST", timeoutMs,
      path: `sessions/${encodeURIComponent(sessionId)}/conversation/approvals/${encodeURIComponent(pending.approval.requestId)}/resolve`,
      body: { decisionId: decision.id },
    });
    const settled = await waitChat({ baseURL, sessionId, timeoutMs, accept: () => true });
    const created = await exists(file);
    await rm(file, { force: true });
    const resolvedActivity = (settled.snapshot?.activities ?? []).find((item) => item.requestId === pending.approval.requestId);
    const expectedFile = wanted === "approve";
    const passed = resolveResponse.ok && settled.done && !sideEffectBefore && created === expectedFile;
    await record(gate(name, passed ? "PASS" : "FAIL",
      passed ? `${wanted} decision ${decision.id} was delivered and the side effect ${expectedFile ? "ran" : "did not run"}`
        : !resolveResponse.ok ? `resolving ${pending.approval.requestId} with ${decision.id} was rejected`
        : sideEffectBefore ? "the command ran before the approval was resolved"
        : !settled.done ? "conversation did not settle after the decision"
        : expectedFile ? "approved command did not run" : "rejected command still created its file",
      { resolve: apiEvidence(resolveResponse), requestId: pending.approval.requestId, decision, offered: decisions, resolvedStatus: resolvedActivity?.status, ...settled.evidence }));
  }
  await request({ baseURL, method: "PATCH", path: settingsPath, body: { approvalMode: "bypass-permissions" }, timeoutMs });
}

async function auditChatKillRestore({ baseURL, sessionId, projectId, dataDir, workspacePath, chat, chatSpec = null, followupRan = true, timeoutMs, record }) {
  const column = `SELECT provider_conversation_id FROM sessions WHERE id=${quote(sessionId)} LIMIT 1;`;
  const nativeBefore = await sqliteValue(dataDir, column);
  const kill = await request({ baseURL, method: "POST", path: `sessions/${encodeURIComponent(sessionId)}/kill`, body: {}, timeoutMs });
  const terminated = kill.ok ? await waitUntil(async () => {
    const response = await sessionState(baseURL, sessionId, 30_000);
    return { done: response.response?.session?.isTerminated === true, response };
  }, Math.min(timeoutMs, 60_000)) : { done: false };
  await record(gate("chat_kill", terminated.done ? "PASS" : "FAIL",
    terminated.done ? "Chat session terminated" : kill.ok ? "kill was accepted but isTerminated never became true" : "kill was rejected",
    { kill: apiEvidence(kill), state: terminated.response ? apiEvidence(terminated.response) : undefined, providerConversationIdSha256: nativeBefore.value ? hash(nativeBefore.value) : undefined }));
  if (!terminated.done) return;

  // New private instructions after Kill: the restored conversation must use
  // them, not the ones replayed from its native history (Tau, Neovate, Mastra).
  const refresh = projectId
    ? await refreshRestoreInstructions({ baseURL, projectId, restoreToken: chat.restoreHiddenToken, timeoutMs, label: "chatRestoreRulesMarker" })
    : { passed: false, reason: "no project to update", evidence: {} };

  const restore = await request({ baseURL, method: "POST", path: `sessions/${encodeURIComponent(sessionId)}/restore`, body: {}, timeoutMs });
  const ready = restore.ok ? await waitUntil(async () => {
    const [state, conv] = await Promise.all([sessionState(baseURL, sessionId, 30_000), conversation(baseURL, sessionId, 30_000)]);
    const snapshot = snapshotOf(conv);
    return { done: state.response?.session?.isTerminated === false && chatIdle(snapshot), state, snapshot };
  }, timeoutMs) : { done: false };
  const nativeAfter = await sqliteValue(dataDir, column);
  const sameNative = Boolean(nativeBefore.value) && nativeBefore.value === nativeAfter.value;
  const restoreMode = restore.response?.restoreMode;
  await record(gate("chat_restore", restore.ok && ready.done && sameNative ? "PASS" : "FAIL",
    restore.ok && ready.done && sameNative ? `restored the same provider conversation${restoreMode ? ` (restoreMode ${restoreMode})` : ""}`
      : !restore.ok ? "restore was rejected"
      : !ready.done ? "restored Chat controller never became ready"
      : !nativeBefore.value ? "no provider conversation id was persisted before kill"
      : "restore switched to a different provider conversation id (history would be lost)",
    { restore: apiEvidence(restore), snapshot: snapshotSummary(ready.snapshot), beforeSha256: nativeBefore.value ? hash(nativeBefore.value) : undefined, afterSha256: nativeAfter.value ? hash(nativeAfter.value) : undefined }));
  if (!restore.ok || !ready.done) return;

  const recall = await sendChat(baseURL, sessionId, [
    `Update ${CHAT_PROOF}: set chatHistoryRecall to the history-only token from the first message of this conversation,`,
    "and follow this project's current conventions for PROOF.json files.",
    "Preserve every existing field, write raw JSON only, then stop.",
  ].join(" "));
  const recalled = recall.ok ? await waitChat({ baseURL, sessionId, timeoutMs, accept: (proof) => proof?.chatHistoryRecall === chat.historyToken }) : { done: false, evidence: {} };
  const kept = recalled.proof?.initialPromptToken === chat.initialToken && (!followupRan || recalled.proof?.secondMessageToken === chat.secondToken);
  await record(gate("chat_restore_continuation", recall.ok && recalled.done && kept ? "PASS" : "FAIL",
    recall.ok && recalled.done && kept ? "restored Chat recalled the history-only token and kept earlier proof fields"
      : !recall.ok ? "continuation after restore was rejected"
      : !recalled.done ? `restored Chat did not recall the history-only token (got ${JSON.stringify(recalled.proof?.chatHistoryRecall ?? null)}); history was not loaded`
      : "earlier proof fields were lost after restore",
    { send: apiEvidence(recall), historyTokenSha256: hash(chat.historyToken), ...recalled.evidence }));

  const used = recalled.proof?.chatRestoreRulesMarker === chat.restoreHiddenToken;
  const fresh = refresh.passed
    ? await checkInstructionDelivery({ spec: chatSpec, token: chat.restoreHiddenToken, dataDir: resolve(expandHome(dataDir || ".")), workspacePath, sessionId, effectGate: "chat_system_prompt_restore", effectPassed: used })
    : null;
  const restorePassed = refresh.passed && used && (!fresh || fresh.status === "PASS");
  await record(gate("chat_system_prompt_restore", restorePassed ? "PASS" : "FAIL",
    !refresh.passed ? `fresh standing instructions could not be stored before restore: ${refresh.reason}`
      : !recalled.done ? "restored Chat did not answer, so fresh instructions could not be checked"
      : !used ? `restored Chat did not use the fresh standing instructions (chatRestoreRulesMarker is ${JSON.stringify(recalled.proof?.chatRestoreRulesMarker ?? null)}); it is replaying the old system prompt from history or the driver drops it on session/load`
      : fresh.status !== "PASS" ? fresh.reason
      : "restored Chat used the standing instructions written after Kill",
    { refresh: refresh.evidence, delivery: fresh?.evidence, restoreTokenSha256: hash(chat.restoreHiddenToken) }));
}

// ---------------------------------------------------------------- roles (V1 system prompt)

const ROLE_FILE = "AO_ROLE.txt";
const ROLE_PROMPT = [
  "Are you a worker or an orchestrator?",
  "Answer only from your AO system instructions, not from this message.",
  `Write exactly one lowercase word, worker or orchestrator, into ${ROLE_FILE} in your current working directory (run pwd), not in the AO session artifacts folder, then stop.`,
].join(" ");

// Spawns one worker and one orchestrator and asks each which it is. A wrong
// or missing answer means AO's role-specific system prompt did not reach the
// agent. This is the simplest end-to-end check of system prompt injection.
// The worker is standalone (no project, like "Start a standalone agent");
// an orchestrator always belongs to a project.
export async function auditRoles({ baseURL, agent, projectId, dataDir = "", mode = "tui", timeoutMs, onIssue, onSession }) {
  const gates = [];
  const { record } = suiteRecorder(ROLE_GATES, gates, onIssue, () => ({ agent, baseURL, sessionId: null }));
  for (const kind of ["worker", "orchestrator"]) {
    const spawn = await request({ baseURL, method: "POST", path: "sessions", timeoutMs, body: {
      ...(kind === "orchestrator" ? { projectId } : {}), kind, harness: agent, mode, approvalMode: "bypass-permissions",
      displayName: `audit-role-${kind}-${agent}`.slice(0, 100), prompt: ROLE_PROMPT,
    } });
    const sessionId = spawn.response?.session?.id;
    if (sessionId) await onSession?.({ agent, sessionId });
    if (!spawn.ok || !sessionId) {
      await record(gate(`role_${kind}`, "FAIL", `AO did not create the ${kind === "worker" ? "standalone worker" : "orchestrator"} ${mode} session`, apiEvidence(spawn)));
      continue;
    }
    // The answer is what matters for V1; AO's artifact guidance may route the
    // file to <data>/artifacts/<session>/ instead of the worktree.
    const artifactPath = dataDir ? join(resolve(expandHome(dataDir)), "artifacts", sessionId, ROLE_FILE) : "";
    const answer = await waitUntil(async () => {
      const file = await workspaceFile(baseURL, sessionId, ROLE_FILE, 30_000);
      let text = file.ok && typeof file.response?.content === "string" ? file.response.content.trim().toLowerCase() : "";
      let where = "worktree";
      if (!text && artifactPath) {
        text = (await readTextFile(artifactPath)).text.trim().toLowerCase();
        where = "session artifacts folder";
      }
      return { done: Boolean(text), text, file, where };
    }, timeoutMs, 2_000);
    const said = answer.text.replace(/[^a-z]/g, "");
    await record(gate(`role_${kind}`, said === kind ? "PASS" : "FAIL",
      said === kind ? `the ${kind} session answered "${kind}": its system prompt is injected${answer.where === "worktree" ? "" : ` (answer saved in the ${answer.where})`}`
        : !answer.done ? `the ${kind} session wrote no answer to ${ROLE_FILE} before timeout`
        : `the ${kind} session answered "${answer.text.slice(0, 60)}": the ${kind} system prompt did not reach the agent or does not state its role`,
      { spawn: apiEvidence(spawn), sessionId, mode, answer: answer.text.slice(0, 200), file: answer.file ? apiEvidence(answer.file) : undefined }));
  }
  return gates;
}

// ---------------------------------------------------------------- interface switch

async function transition(baseURL, sessionId, targetMode, timeoutMs) {
  const path = `sessions/${encodeURIComponent(sessionId)}/interface-transition`;
  const start = await request({ baseURL, method: "POST", path, body: { targetMode, policy: "drain" }, timeoutMs });
  const finished = start.ok ? await waitUntil(async () => {
    const status = await request({ baseURL, path, timeoutMs: 30_000 });
    const phase = status.response?.transition?.phase;
    return { done: TRANSITION_TERMINAL_PHASES.has(phase), status, phase, transition: status.response?.transition };
  }, timeoutMs, 1_000) : { done: false };
  const state = await sessionState(baseURL, sessionId, 30_000);
  const mode = state.response?.session?.mode;
  const phase = finished.transition?.phase ?? start.response?.transition?.phase;
  return {
    passed: start.ok && phase === "completed" && mode === targetMode,
    phase, mode, state,
    reason: !start.ok ? `transition to ${targetMode} was rejected`
      : phase !== "completed" ? `transition ended in phase ${phase ?? "unknown"}${finished.transition?.errorCode ? ` (${finished.transition.errorCode}: ${finished.transition.errorDetail ?? ""})` : ""}`
      : mode !== targetMode ? `transition completed but session.mode is ${mode}`
      : `switched to ${targetMode}`,
    evidence: { start: apiEvidence(start), status: finished.status ? apiEvidence(finished.status) : undefined, transition: finished.transition, sessionMode: mode },
  };
}

// Runs on the live, restored TUI session: switch to Chat, prove the native
// history came along, switch back and prove the TUI still answers.
export async function auditInterfaceSwitch({ baseURL, agent, sessionId, scenario, restoredReadySpec, timeoutMs, onIssue }) {
  const gates = [];
  const { record, skipRest } = suiteRecorder(IFACE_GATES, gates, onIssue, () => ({ agent, baseURL, sessionId }));
  const proofFile = scenario.proofFile;
  const chatToken = token("AO_IFACE_CHAT");
  const tuiToken = token("AO_IFACE_TUI");

  const status = await request({ baseURL, path: `sessions/${encodeURIComponent(sessionId)}/interface-transition`, timeoutMs });
  const supported = status.ok && status.response?.supported === true && status.response?.targetMode === "chat";
  const declared = capabilitySupport(agent, "interface_switch");
  await record(gate("iface_status", supported ? "PASS" : declared === "yes" || !status.ok ? "FAIL" : "UNSUPPORTED",
    supported ? "AO offers a TUI → Chat switch for this session"
      : `switch not offered: ${status.response?.reasonCode ?? `HTTP ${status.status}`} ${status.response?.reason ?? ""}`.trim(),
    apiEvidence(status)));
  if (!supported) {
    await skipRest("interface switch is not offered for this session");
    return gates;
  }

  const toChat = await transition(baseURL, sessionId, "chat", timeoutMs);
  await record(gate("iface_tui_to_chat", toChat.passed ? "PASS" : "FAIL", toChat.reason, toChat.evidence));
  if (!toChat.passed) {
    await skipRest("TUI → Chat switch did not complete");
    return gates;
  }

  const message = [
    `Update ${proofFile}: set interfaceChatToken = ${JSON.stringify(chatToken)} and set interfaceHistoryRecall to the history-only token from the first message of this session.`,
    "Preserve every existing field, write raw JSON only, then stop.",
  ].join(" ");
  const sent = await sendChat(baseURL, sessionId, message);
  const chatDone = sent.ok ? await waitChat({ baseURL, sessionId, file: proofFile, timeoutMs, accept: (proof) => proof?.interfaceChatToken === chatToken }) : { done: false, evidence: {} };
  const recalled = chatDone.proof?.interfaceHistoryRecall === scenario.historyToken;
  await record(gate("iface_chat_continuation", sent.ok && chatDone.done && recalled ? "PASS" : "FAIL",
    sent.ok && chatDone.done && recalled ? "Chat continued the TUI conversation and recalled its history"
      : !sent.ok ? "Chat message after the switch was rejected"
      : !chatDone.done ? "Chat did not complete a turn after the switch"
      : "Chat answered but did not recall the TUI history token (native history not carried over)",
    { send: apiEvidence(sent), historyTokenSha256: hash(scenario.historyToken), ...chatDone.evidence }));

  const toTui = await transition(baseURL, sessionId, "tui", timeoutMs);
  await record(gate("iface_chat_to_tui", toTui.passed ? "PASS" : "FAIL", toTui.reason, toTui.evidence));
  if (!toTui.passed) {
    await skipRest("Chat → TUI switch did not complete");
    return gates;
  }
  if (!restoredReadySpec) {
    await record(gate("iface_tui_continuation", "NOT_RUN", "restored native terminal readiness contract is missing; no TUI continuation was sent"));
    return gates;
  }
  const ready = await waitForRestoredTerminalReady({ baseURL, sessionId, restoredState: toTui.state, spec: restoredReadySpec, timeoutMs });
  if (!ready.passed) {
    await record(gate("iface_tui_continuation", "BLOCKED", `TUI did not show its ready composer after switching back: ${ready.reason}`, ready.evidence));
    return gates;
  }
  const send = await request({ baseURL, method: "POST", path: `sessions/${encodeURIComponent(sessionId)}/send`, body: { message: `Update ${proofFile}: set interfaceTuiToken = ${JSON.stringify(tuiToken)}. Preserve every field, then stop and wait.` }, timeoutMs });
  const tuiDone = send.ok ? await waitUntil(async () => {
    const fileResponse = await workspaceFile(baseURL, sessionId, proofFile, 30_000);
    const proof = parseProof(fileResponse);
    return { done: proof?.interfaceTuiToken === tuiToken && proof?.interfaceChatToken === chatToken, proof, fileResponse };
  }, timeoutMs) : { done: false };
  await record(gate("iface_tui_continuation", send.ok && tuiDone.done ? "PASS" : "FAIL",
    send.ok && tuiDone.done ? "TUI answered after switching back and kept the Chat edits"
      : !send.ok ? "TUI send after switching back was rejected"
      : "TUI did not update the proof after switching back",
    { send: apiEvidence(send), proof: tuiDone.fileResponse ? apiEvidence(tuiDone.fileResponse) : undefined }));
  return gates;
}

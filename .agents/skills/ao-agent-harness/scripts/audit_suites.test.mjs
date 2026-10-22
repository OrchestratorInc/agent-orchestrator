import assert from "node:assert/strict";
import { mkdirSync, mkdtempSync, realpathSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { delimiter, join } from "node:path";
import test from "node:test";

import { CHAT_GATES, IFACE_GATES, auditChatSuite, auditExtras, auditInterfaceSwitch, auditRoles } from "./audit_suites.mjs";

const scenario = { hiddenInitialToken: "AO_HIDDEN_X", agentsToken: "AO_AGENTS_X", historyToken: "AO_HISTORY_X", proofFile: "AO_AUDIT_PROOF.json" };

function json(status, body) {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

async function withFetch(handler, run) {
  const original = globalThis.fetch;
  globalThis.fetch = async (url, options = {}) => handler(new URL(url), options, options.body ? JSON.parse(options.body) : undefined);
  try { return await run("http://ao.test"); } finally { globalThis.fetch = original; }
}

// Read-only sqlite boundary as a deterministic executable, as in the mux fixtures.
function withFakeSqlite(value) {
  const bin = mkdtempSync(join(tmpdir(), "ao-suite-sqlite-"));
  writeFileSync(join(bin, "sqlite3"), `#!${process.execPath}\nconsole.log(${JSON.stringify(value)});\n`, { mode: 0o700 });
  const previous = process.env.PATH;
  process.env.PATH = bin + delimiter + previous;
  return () => { process.env.PATH = previous; };
}

const byName = (gates) => Object.fromEntries(gates.map((item) => [item.name, item]));

// A small in-memory AO Chat session: turns complete synchronously and apply
// "set key = value" edits to the proof file, approvals wait for a decision.
// `replayOldPrompt` models a driver that restores native history but never
// resends the system prompt, so rules changed after Kill are ignored.
function fakeChatDaemon({ capabilities = ["streaming", "approvals", "interrupt", "resume"], providerAfterRestore = null, replayOldPrompt = false } = {}) {
  const workspace = realpathSync(mkdtempSync(join(tmpdir(), "ao-suite-ws-")));
  const state = { mode: "chat", terminated: false, controller: "ready", turns: [], activities: [], messages: [], files: {}, clientIds: new Set(), settings: { model: "m1", reasoningEffort: "low" }, history: "", provider: "conv-1",
    rules: `For hiddenInstructionToken use exactly: ${scenario.hiddenInitialToken}`, sessionRules: "" };
  let turnSeq = 0;
  const finishTurn = (status = "completed") => { state.turns.at(-1).state = status; };
  const proof = () => JSON.parse(state.files["AO_AUDIT_CHAT_PROOF.json"] ?? "{}");
  const applyText = (text) => {
    turnSeq += 1;
    state.turns.push({ id: `t${turnSeq}`, providerTurnId: `p${turnSeq}`, state: "running", requestedAt: "now" });
    state.messages.push({ role: "assistant", text: "working", streaming: false });
    const approval = /AO_AUDIT_APPROVAL_(REJECT|APPROVE)\.txt/.exec(text);
    if (approval) {
      state.activities.push({ activityKind: "approval", status: "pending", requestId: `r${turnSeq}`, summary: "run node", file: approval[0], detail: { decisions: [{ id: "accept", label: "Allow", kind: "allow_once" }, { id: "decline", label: "Deny", kind: "reject_once" }] } });
      return;
    }
    if (text.includes("Update AO_AUDIT_CHAT_PROOF.json")) {
      const next = proof();
      for (const match of text.matchAll(/(\w+) = ("[^"]*"|true|false)/g)) next[match[1]] = JSON.parse(match[2]);
      if (text.includes("set chatHistoryRecall to the history-only token")) next.chatHistoryRecall = state.history;
      if (text.includes("chatRestoreInstructionToken")) next.chatRestoreInstructionToken = /chatRestoreHiddenToken use exactly: (\S+)/.exec(state.sessionRules)?.[1] ?? null;
      state.files["AO_AUDIT_CHAT_PROOF.json"] = JSON.stringify(next);
    }
    finishTurn();
  };
  const handler = (url, options, body) => {
    const path = url.pathname.replace("/api/v1/", "");
    const method = options.method ?? "GET";
    if (method === "POST" && path === "sessions") {
      const initial = /request token: (\S+)/.exec(body.prompt)[1];
      state.history = /History-only token[^:]*: (\S+)/.exec(body.prompt)[1];
      state.sessionRules = state.rules;
      state.files["AO_AUDIT_CHAT_PROOF.json"] = JSON.stringify({ cwd: workspace, initialPromptToken: initial, initialPromptExecutions: 1, hiddenInstructionToken: scenario.hiddenInitialToken, projectAgentsToken: scenario.agentsToken });
      turnSeq += 1;
      state.turns.push({ id: "t0", state: "completed", requestedAt: "now" });
      return json(201, { session: { id: "chat-1", mode: "chat", harness: body.harness } });
    }
    if (path === "desktop/sessions/chat-1/workspace") return json(200, { workspacePath: workspace });
    if (path === "sessions/chat-1/workspace/file") {
      const content = state.files[url.searchParams.get("path")];
      return content === undefined ? json(404, { code: "NOT_FOUND" }) : json(200, { content });
    }
    if (path === "sessions/chat-1" && method === "GET") return json(200, { session: { id: "chat-1", mode: state.mode, isTerminated: state.terminated } });
    if (path === "sessions/chat-1/conversation") {
      return json(200, { controller: state.controller, mode: "chat", capabilities, turns: state.turns, messages: state.messages, activities: state.activities, settings: state.settings, mcpServers: [], usage: { inputTokens: 5, outputTokens: 5 } });
    }
    if (path === "sessions/chat-1/conversation/messages") {
      if (state.clientIds.has(body.clientMessageId)) return json(200, { duplicate: true, state: "completed" });
      state.clientIds.add(body.clientMessageId);
      applyText(body.text);
      return json(202, { duplicate: false, state: "running", providerTurnId: `p${turnSeq}` });
    }
    if (path === "sessions/chat-1/conversation/models") return json(200, { models: [{ id: "m1", displayName: "M1", default: true, efforts: ["low", "high"], defaultEffort: "low" }], selected: state.settings });
    if (path === "sessions/chat-1/conversation/settings") { Object.assign(state.settings, body); return json(200, state.settings); }
    if (path === "sessions/chat-1/conversation/steer") return json(409, { error: { code: "CHAT_STEER_UNSUPPORTED", message: "steer not supported" } });
    const resolve = /^sessions\/chat-1\/conversation\/approvals\/(\w+)\/resolve$/.exec(path);
    if (resolve) {
      const activity = state.activities.find((item) => item.requestId === resolve[1]);
      activity.status = "resolved";
      if (body.decisionId === "accept") state.files[activity.file] = "ao-audit";
      finishTurn();
      return json(200, { ok: true });
    }
    if (path === "usage/sessions/chat-1") return json(200, { sessionId: "chat-1", incomplete: false, totals: { inputTokens: 10, outputTokens: 2 }, harnesses: [] });
    if (path === "sessions/chat-1/kill") { state.terminated = true; return json(200, { ok: true }); }
    if (path === "projects/p") return json(200, { project: { id: "p", config: { agentRules: state.rules } } });
    if (path === "projects/p/config") { state.rules = body.config.agentRules; return json(200, { project: { id: "p", config: body.config } }); }
    if (path === "sessions/chat-1/restore") {
      state.terminated = false;
      if (!replayOldPrompt) state.sessionRules = state.rules;
      if (providerAfterRestore) state.provider = providerAfterRestore;
      return json(200, { restoreMode: "native" });
    }
    return json(404, { error: { code: "NOT_FOUND", message: path } });
  };
  return { handler, state };
}

test("Chat suite records a silent TUI fallback as FAIL and every later gate as NOT_RUN", async () => {
  const issues = [];
  const result = await withFetch(() => json(201, { session: { id: "s1", mode: "tui" } }), (baseURL) => auditChatSuite({
    baseURL, agent: "codex", projectId: "p", dataDir: "", timeoutMs: 1_000, scenario, onIssue: (item) => issues.push(item),
  }));
  const gates = byName(result.gates);
  assert.equal(gates.chat_spawn.status, "FAIL");
  assert.match(gates.chat_spawn.reason, /mode=tui instead of chat \(silent fallback\)/);
  assert.equal(result.gates.length, CHAT_GATES.length);
  assert.ok(result.gates.slice(1).every((item) => item.status === "NOT_RUN" && /Chat spawn/.test(item.reason)));
  assert.deepEqual(issues.map((item) => item.gate.name), ["chat_spawn"]);
});

test("Chat suite accepts a truthful refusal only for harnesses without declared Chat", async () => {
  const refuse = () => json(422, { error: { code: "CHAT_UNSUPPORTED", message: "no chat driver" } });
  const other = await withFetch(refuse, (baseURL) => auditChatSuite({ baseURL, agent: "example-agent", projectId: "p", dataDir: "", timeoutMs: 1_000, scenario }));
  assert.equal(byName(other.gates).chat_spawn.status, "UNSUPPORTED");
  const codex = await withFetch(refuse, (baseURL) => auditChatSuite({ baseURL, agent: "codex", projectId: "p", dataDir: "", timeoutMs: 1_000, scenario }));
  assert.equal(byName(codex.gates).chat_spawn.status, "FAIL");
});

test("Chat suite drives spawn, follow-up, settings, approvals and restore against a fake daemon", async () => {
  const restorePath = withFakeSqlite("conv-1");
  try {
    const fake = fakeChatDaemon();
    const result = await withFetch(fake.handler, (baseURL) => auditChatSuite({
      baseURL, agent: "claude-code", projectId: "p", dataDir: tmpdir(), timeoutMs: 2_000, scenario,
    }));
    const gates = byName(result.gates);
    for (const name of ["chat_spawn", "chat_controller_ready", "chat_initial_turn", "chat_instructions", "chat_followup", "chat_idempotent_send",
      "chat_models", "chat_settings", "chat_usage", "chat_post_interrupt_continuation", "chat_approval_reject", "chat_approval_approve",
      "chat_kill", "chat_restore", "chat_restore_continuation", "chat_instruction_channel", "chat_system_prompt_restore"]) {
      assert.equal(gates[name].status, "PASS", `${name}: ${gates[name].reason}`);
    }
    // Claude's MCP status, skills, steer and compaction are conditional: absent
    // capabilities are truthful refusals, not passes or failures.
    for (const name of ["chat_mcp_servers", "chat_skills", "chat_steer", "chat_compact"]) assert.equal(gates[name].status, "UNSUPPORTED", name);
    assert.equal(gates.chat_interrupt.status, "FAIL");
    assert.match(gates.chat_interrupt.reason, /never started the witnessed foreground command/);
    assert.equal(fake.state.settings.reasoningEffort, "high", "a different offered effort is selected");
    assert.equal(fake.state.settings.approvalMode, "bypass-permissions", "approval mode is restored after the approval cases");
    assert.equal(fake.state.files["AO_AUDIT_APPROVAL_REJECT.txt"], undefined);
    assert.equal(fake.state.files["AO_AUDIT_APPROVAL_APPROVE.txt"], "ao-audit");
  } finally { restorePath(); }
});

test("Chat restore that loads a different provider conversation fails with a named cause", async () => {
  let calls = 0;
  const bin = mkdtempSync(join(tmpdir(), "ao-suite-sqlite-seq-"));
  const counter = join(bin, "count");
  writeFileSync(counter, "0");
  // First read (before kill) returns conv-1, later reads return conv-2.
  writeFileSync(join(bin, "sqlite3"), `#!${process.execPath}
const fs = require("node:fs"); const n = Number(fs.readFileSync(${JSON.stringify(counter)}, "utf8")) + 1;
fs.writeFileSync(${JSON.stringify(counter)}, String(n));
const sql = process.argv[4] || "";
console.log(sql.includes("provider_conversation_id") ? (n > 2 ? "conv-2" : "conv-1") : "");
`, { mode: 0o700 });
  const previous = process.env.PATH;
  process.env.PATH = bin + delimiter + previous;
  try {
    const fake = fakeChatDaemon();
    const result = await withFetch((url, options, body) => { calls += 1; return fake.handler(url, options, body); }, (baseURL) => auditChatSuite({
      baseURL, agent: "codex", projectId: "p", dataDir: tmpdir(), timeoutMs: 2_000, scenario,
    }));
    const restore = byName(result.gates).chat_restore;
    assert.equal(restore.status, "FAIL");
    assert.match(restore.reason, /different provider conversation id/);
    assert.ok(calls > 0);
  } finally { process.env.PATH = previous; }
});

test("interface switch reports the daemon's refusal reason", async () => {
  const result = await withFetch((url) => {
    assert.equal(url.pathname, "/api/v1/sessions/tui-1/interface-transition");
    return json(200, { supported: false, targetMode: "chat", reasonCode: "NATIVE_ID_MISSING", reason: "no native conversation id" });
  }, (baseURL) => auditInterfaceSwitch({ baseURL, agent: "codex", sessionId: "tui-1", scenario, restoredReadySpec: null, timeoutMs: 1_000 }));
  const gates = byName(result);
  assert.equal(gates.iface_status.status, "FAIL");
  assert.match(gates.iface_status.reason, /NATIVE_ID_MISSING no native conversation id/);
  assert.equal(result.length, IFACE_GATES.length);
});

test("interface switch fails continuity when Chat does not recall TUI history", async () => {
  const workspace = { files: { "AO_AUDIT_PROOF.json": JSON.stringify({ initialPromptToken: "x" }) } };
  let mode = "tui";
  let phase = "requested";
  const result = await withFetch((url, options, body) => {
    const path = url.pathname.replace("/api/v1/", "");
    if (path === "sessions/tui-1/interface-transition" && options.method === "POST") {
      mode = body.targetMode; phase = "completed";
      return json(202, { ok: true, sessionId: "tui-1", transition: { id: "x1", phase: "requested" } });
    }
    if (path === "sessions/tui-1/interface-transition") {
      return json(200, { supported: true, targetMode: mode === "tui" ? "chat" : "tui", transition: { id: "x1", phase } });
    }
    if (path === "sessions/tui-1") return json(200, { session: { id: "tui-1", mode } });
    if (path === "sessions/tui-1/conversation/messages") {
      const proof = JSON.parse(workspace.files["AO_AUDIT_PROOF.json"]);
      proof.interfaceChatToken = /interfaceChatToken = "([^"]+)"/.exec(body.text)[1];
      proof.interfaceHistoryRecall = "WRONG";
      workspace.files["AO_AUDIT_PROOF.json"] = JSON.stringify(proof);
      return json(202, { state: "completed" });
    }
    if (path === "sessions/tui-1/conversation") return json(200, { controller: "ready", mode: "chat", turns: [{ id: "t", state: "completed" }], messages: [], activities: [], settings: {} });
    if (path === "sessions/tui-1/workspace/file") return json(200, { content: workspace.files[url.searchParams.get("path")] });
    return json(404, {});
  }, (baseURL) => auditInterfaceSwitch({ baseURL, agent: "claude-code", sessionId: "tui-1", scenario, restoredReadySpec: null, timeoutMs: 3_000 }));
  const gates = byName(result);
  assert.equal(gates.iface_status.status, "PASS");
  assert.equal(gates.iface_tui_to_chat.status, "PASS");
  assert.equal(gates.iface_chat_continuation.status, "FAIL");
  assert.match(gates.iface_chat_continuation.reason, /did not recall the TUI history token/);
  assert.equal(gates.iface_chat_to_tui.status, "PASS");
  assert.equal(gates.iface_tui_continuation.status, "NOT_RUN");
});

test("extras checks prompt, skill and Claude hook files on disk", async () => {
  const dataDir = mkdtempSync(join(tmpdir(), "ao-suite-data-"));
  const workspacePath = mkdtempSync(join(tmpdir(), "ao-suite-wt-"));
  mkdirSync(join(dataDir, "prompts", "tui-1"), { recursive: true });
  writeFileSync(join(dataDir, "prompts", "tui-1", "system.md"), `rules ${scenario.hiddenInitialToken}\nsee skills/using-ao/SKILL.md`);
  mkdirSync(join(dataDir, "skills", "using-ao"), { recursive: true });
  writeFileSync(join(dataDir, "skills", "using-ao", "SKILL.md"), "# using-ao");
  mkdirSync(join(workspacePath, ".claude"));
  writeFileSync(join(workspacePath, ".claude", "settings.local.json"), JSON.stringify({ hooks: { SessionStart: "ao hooks claude-code session-start", UserPromptSubmit: "x", Stop: "y" } }));
  const result = await withFetch((url) => {
    if (url.pathname.endsWith("/models/refresh")) return json(200, { models: [{ id: "m1", efforts: ["low", "high"], defaultEffort: "medium" }] });
    if (url.pathname === "/api/v1/usage/sessions/tui-1") return json(200, { totals: { inputTokens: 3, outputTokens: 4 } });
    return json(404, {});
  }, (baseURL) => auditExtras({ baseURL, agent: "claude-code", dataDir, sessionId: "tui-1", workspacePath, scenario, timeoutMs: 1_000 }));
  const gates = byName(result);
  assert.equal(gates.ao_models_refresh.status, "PASS");
  assert.equal(gates.model_efforts.status, "FAIL");
  assert.match(gates.model_efforts.reason, /defaultEffort is not in efforts for: m1/);
  assert.equal(gates.tui_system_prompt_file.status, "PASS");
  assert.equal(gates.ao_skill_pointer.status, "PASS");
  assert.equal(gates.tui_hooks_installed.status, "PASS");
  assert.equal(gates.tui_usage_recorded.status, "PASS");
});

test("restored Chat that replays the old system prompt fails chat_system_prompt_restore with the cause (Tau, Neovate, Mastra)", async () => {
  const restorePath = withFakeSqlite("conv-1");
  try {
    const fake = fakeChatDaemon({ replayOldPrompt: true });
    const result = await withFetch(fake.handler, (baseURL) => auditChatSuite({
      baseURL, agent: "codex", projectId: "p", dataDir: tmpdir(), timeoutMs: 2_000, scenario, only: new Set(["chat_system_prompt_restore"]),
    }));
    const gates = byName(result.gates);
    assert.equal(gates.chat_restore_continuation.status, "PASS", "history itself was restored");
    assert.equal(gates.chat_system_prompt_restore.status, "FAIL");
    assert.match(gates.chat_system_prompt_restore.reason, /replaying the old system prompt/);
    assert.match(fake.state.rules, /chatRestoreHiddenToken use exactly: AO_CHAT_RESTORE_HIDDEN_/, "fresh rules were written after Kill");
  } finally { restorePath(); }
});

test("roles: a worker and an orchestrator must each name their role (V1 system prompt check)", async () => {
  const spawned = [];
  const run = (answers) => withFetch((url, options, body) => {
    const path = url.pathname.replace("/api/v1/", "");
    if (path === "sessions" && options.method === "POST") {
      const id = `${body.kind}-1`;
      spawned.push({ id, kind: body.kind, mode: body.mode, projectId: body.projectId });
      return json(201, { session: { id, kind: body.kind, mode: body.mode } });
    }
    const file = /^sessions\/(\w+)-1\/workspace\/file$/.exec(path);
    if (file) return answers[file[1]] === undefined ? json(404, {}) : json(200, { content: `${answers[file[1]]}\n` });
    return json(404, {});
  }, (baseURL) => auditRoles({ baseURL, agent: "codex", projectId: "p", mode: "tui", timeoutMs: 1_500 }));

  const good = byName(await run({ worker: "worker", orchestrator: "Orchestrator." }));
  assert.equal(good.role_worker.status, "PASS");
  assert.equal(good.role_orchestrator.status, "PASS");
  assert.deepEqual(spawned.map((item) => [item.kind, item.mode, item.projectId]), [["worker", "tui", undefined], ["orchestrator", "tui", "p"]], "worker is standalone; orchestrator is in the project");

  const bad = byName(await run({ worker: "worker", orchestrator: "worker" }));
  assert.equal(bad.role_orchestrator.status, "FAIL");
  assert.match(bad.role_orchestrator.reason, /answered "worker": the orchestrator system prompt did not reach the agent/);

  const silent = byName(await run({ worker: undefined, orchestrator: "orchestrator" }));
  assert.match(silent.role_worker.reason, /wrote no answer/);
});

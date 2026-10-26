#!/usr/bin/env node
// Live audit of one or more AO harnesses. Builds AO from the checkout, runs it
// on an isolated daemon, and drives each harness the way AO uses it:
//
//   cli       the native CLI on its own: binary, version, login, model list
//   ao        AO sees it: registered, installed, authenticated, ready, models
//   tui       spawn a scratch session, prompt, system prompt, follow-up,
//             stop by archiving, kill, exact restore with history
//   chat      the same through Chat, plus model settings
//   roles     a worker and an orchestrator each name their own role (V1)
//
// Everything harness-specific comes from the harness's entry in the contract
// file (assets/local-contracts.json). The run writes, under --out:
//   report.md / report.json   every check with PASS/FAIL/BLOCKED/SKIP and why
//   evidence/<agent>/*.json   API responses and command output behind each check
//   daemon.log                the AO daemon's output
// and a "Fix" section that sorts every failure: AO implementation (with the
// source folders to open), harness contract, or something only the user can do.
//
//   node scripts/audit.mjs --agent my-agent [--agent codex] [--repo /path/to/ao]

import { spawn } from "node:child_process";
import { createHash, randomUUID } from "node:crypto";
import { constants as fsConstants } from "node:fs";
import { access, mkdir, mkdtemp, readFile, realpath, rm, writeFile } from "node:fs/promises";
import { createServer } from "node:net";
import { homedir } from "node:os";
import { delimiter, dirname, isAbsolute, join, resolve } from "node:path";
import { StringDecoder } from "node:string_decoder";
import { fileURLToPath } from "node:url";
import { stripVTControlCharacters } from "node:util";

const SKILL_DIR = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const SETTLED = new Set(["idle", "waiting_input", "blocked"]);
const PROOF = "AO_AUDIT_PROOF.json";
const CHAT_PROOF = "AO_AUDIT_CHAT_PROOF.json";
const ROLE_FILE = "AO_ROLE.txt";

// ------------------------------------------------------------------ helpers

const now = () => new Date().toISOString();
const sleep = (ms) => new Promise((done) => setTimeout(done, ms));
const token = (prefix) => `${prefix}_${randomUUID().replaceAll("-", "").slice(0, 16)}`;
const expandHome = (value) => (value === "~" || value?.startsWith("~/") ? join(homedir(), value.slice(1)) : value);

export function redact(value) {
  return String(value ?? "")
    .replaceAll(homedir(), "~")
    .replace(/\bBearer\s+[^\s"'<>]+/gi, "Bearer [REDACTED]")
    .replace(/\b(?:sk|key|token)-[A-Za-z0-9._-]{8,}\b/gi, "[REDACTED]")
    .replace(/((?:api[_-]?key|access[_-]?token|refresh[_-]?token|password|secret|authorization|cookie)\s*["']?\s*[:=]\s*["']?)[^\s"'&,;}]+/gi, "$1[REDACTED]")
    .replace(/[\w.%+-]+@[\w-]+\.[\w.-]+/g, "[email]");
}

// What a ready-cue regex sees: cursor-forward moves become spaces (TUIs space
// words that way) and window-title sequences are dropped.
export function screenText(output) {
  return stripVTControlCharacters(String(output)
    .replace(/\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)/g, "")
    .replace(/\x1b\[(\d*)C/g, (_, count) => " ".repeat(Math.min(Number(count || 1), 200))))
    .replaceAll("\r\n", "\n").replaceAll("\r", "\n");
}

async function waitUntil(check, timeoutMs, intervalMs = 1_000) {
  const deadline = Date.now() + timeoutMs;
  let last = { done: false };
  while (Date.now() < deadline) {
    last = await check();
    if (last.done) return last;
    await sleep(intervalMs);
  }
  return { ...last, done: false, timedOut: true };
}

function run(command, args, { cwd, env, timeoutMs = 60_000, outputLimit = 1 << 20 } = {}) {
  return new Promise((done) => {
    let stdout = "";
    let stderr = "";
    let timedOut = false;
    let child;
    try {
      child = spawn(command, args, { cwd, env: env ?? process.env, stdio: ["ignore", "pipe", "pipe"], detached: process.platform !== "win32" });
    } catch (error) {
      done({ ok: false, code: null, stdout: "", stderr: error.message });
      return;
    }
    const timer = setTimeout(() => {
      timedOut = true;
      try { process.kill(-child.pid, "SIGKILL"); } catch { child.kill("SIGKILL"); }
    }, timeoutMs);
    child.stdout.on("data", (chunk) => { stdout = `${stdout}${chunk}`.slice(-outputLimit); });
    child.stderr.on("data", (chunk) => { stderr = `${stderr}${chunk}`.slice(-outputLimit); });
    child.on("error", (error) => { clearTimeout(timer); done({ ok: false, code: null, stdout, stderr: `${stderr}${error.message}` }); });
    child.on("close", (code) => { clearTimeout(timer); done({ ok: code === 0 && !timedOut, code, timedOut, stdout: stdout.trim(), stderr: stderr.trim() }); });
  });
}

async function onPath(binary) {
  if (!binary) return "";
  const candidates = isAbsolute(binary) || binary.includes("/")
    ? [resolve(expandHome(binary))]
    : (process.env.PATH || "").split(delimiter).filter(Boolean).flatMap((dir) =>
      (process.platform === "win32" ? (process.env.PATHEXT || ".EXE;.CMD").split(";") : [""]).map((ext) => join(dir, `${binary}${ext}`)));
  for (const candidate of candidates) {
    try { await access(candidate, fsConstants.X_OK); return await realpath(candidate); } catch { /* next */ }
  }
  return "";
}

function modelIds(payload) {
  let list = payload;
  for (let depth = 0; depth < 3 && list && !Array.isArray(list) && typeof list === "object"; depth += 1) {
    list = list.models ?? list.data ?? list.items ?? list.options;
  }
  return [...new Set((Array.isArray(list) ? list : [])
    .map((entry) => (typeof entry === "string" ? entry : entry?.id ?? entry?.model ?? entry?.value ?? entry?.slug))
    .filter((id) => typeof id === "string" && id.trim()))];
}

async function samePath(a, b) {
  if (typeof a !== "string" || typeof b !== "string") return false;
  const real = async (path) => { try { return await realpath(path); } catch { return resolve(path); } };
  return (await real(a)) === (await real(b));
}

// ------------------------------------------------------------------ AO API

function api(baseURL) {
  const call = async (method, path, body, timeoutMs = 60_000) => {
    const started = Date.now();
    try {
      const response = await fetch(`${baseURL}/api/v1/${path}`, {
        method,
        headers: body === undefined ? undefined : { "content-type": "application/json" },
        body: body === undefined ? undefined : JSON.stringify(body),
        signal: AbortSignal.timeout(timeoutMs),
      });
      const text = await response.text();
      let data = null;
      try { data = text ? JSON.parse(text) : null; } catch { data = { text: text.slice(0, 4000) }; }
      return { ok: response.ok, status: response.status, method, path, data, ms: Date.now() - started };
    } catch (error) {
      return { ok: false, status: 0, method, path, data: null, error: error.message, ms: Date.now() - started };
    }
  };
  const session = (id) => call("GET", `sessions/${encodeURIComponent(id)}`, undefined, 30_000);
  return {
    get: (path, timeoutMs) => call("GET", path, undefined, timeoutMs),
    post: (path, body = {}, timeoutMs) => call("POST", path, body, timeoutMs),
    patch: (path, body, timeoutMs) => call("PATCH", path, body, timeoutMs),
    session,
    file: (id, path) => call("GET", `sessions/${encodeURIComponent(id)}/workspace/file?${new URLSearchParams({ path })}`, undefined, 30_000),
    conversation: (id) => call("GET", `sessions/${encodeURIComponent(id)}/conversation`, undefined, 30_000),
  };
}

function jsonFile(response) {
  if (!response?.ok || typeof response.data?.content !== "string") return null;
  try {
    const value = JSON.parse(response.data.content);
    return value && typeof value === "object" && !Array.isArray(value) ? value : null;
  } catch { return null; }
}

// ------------------------------------------------------------------ results

// Checks are recorded in order. Each check keeps its evidence (API responses,
// command output) in its own file, so the report itself stays readable.
class AgentRun {
  constructor(agent, contract, evidenceDir) {
    Object.assign(this, { agent, contract, evidenceDir, checks: [], sessions: [] });
  }

  async add(id, status, reason, evidence) {
    const item = { id, status, reason };
    if (evidence !== undefined) {
      item.evidence = join(this.evidenceDir, `${String(this.checks.length + 1).padStart(2, "0")}-${id}.json`);
      await writeFile(item.evidence, `${redact(JSON.stringify(evidence, null, 2))}\n`);
    }
    this.checks.push(item);
    log(`  ${status.padEnd(7)} ${id}: ${reason}`);
    return status === "PASS";
  }

  async skip(ids, reason) {
    for (const id of ids) await this.add(id, "SKIP", reason);
  }

  passed(id) {
    return this.checks.find((item) => item.id === id)?.status === "PASS";
  }
}

let logFile = "";
function log(line) {
  console.log(line);
  if (logFile) writeFile(logFile, `${redact(line)}\n`, { flag: "a" }).catch(() => {});
}

// ------------------------------------------------------------------ cli

async function auditCli(run_, contract) {
  const binary = await onPath(contract.binary);
  if (!binary) {
    await run_.add("cli_installed", "BLOCKED", `\`${contract.binary}\` is not on PATH; install it (or fix \`binary\` in the contract)`);
    return false;
  }
  await run_.add("cli_installed", "PASS", `found ${binary}`);
  const version = await run(binary, contract.versionArgs ?? ["--version"], { timeoutMs: 30_000 });
  const versionText = (version.stdout || version.stderr).split("\n")[0];
  await run_.add("cli_version", version.ok && versionText ? "PASS" : "FAIL",
    version.ok && versionText ? versionText : `\`${contract.binary} ${(contract.versionArgs ?? []).join(" ")}\` exited ${version.code}`, version);
  if (contract.integration?.args) {
    const login = await run(binary, contract.integration.args, { timeoutMs: 30_000 });
    const text = `${login.stdout}\n${login.stderr}`;
    const loggedIn = text.includes(contract.integration.expect);
    await run_.add("cli_login", loggedIn ? "PASS" : "BLOCKED",
      loggedIn ? "the CLI reports a logged-in account" : `not logged in: \`${contract.binary} ${contract.integration.args.join(" ")}\` did not print "${contract.integration.expect}"`,
      { code: login.code, output: text.slice(-2000) });
  }
  if (contract.models?.args) {
    const models = await run(binary, contract.models.args, { timeoutMs: 60_000, outputLimit: 16 << 20 });
    let ids = [];
    if (models.ok) {
      ids = contract.models.format === "json"
        ? (() => { try { return modelIds(JSON.parse(models.stdout)); } catch { return []; } })()
        : models.stdout.split("\n").map((line) => line.trim()).filter(Boolean);
    }
    run_.cliModels = ids;
    await run_.add("cli_models", ids.length ? "PASS" : "FAIL",
      ids.length ? `the CLI lists ${ids.length} model(s)` : "the CLI model command listed nothing", { code: models.code, stderr: models.stderr.slice(-2000), ids: ids.slice(0, 50) });
  }
  return true;
}

// ------------------------------------------------------------------ daemon

async function freePort() {
  return new Promise((done, fail) => {
    const server = createServer();
    server.once("error", fail);
    server.listen(0, "127.0.0.1", () => {
      const { port } = server.address();
      server.close(() => done(port));
    });
  });
}

async function startDaemon({ repo, root, aoBinary, timeoutMs }) {
  let binary = aoBinary ? resolve(expandHome(aoBinary)) : "";
  if (!binary) {
    binary = join(root, "bin", process.platform === "win32" ? "ao.exe" : "ao");
    log(`Building AO from ${repo} (the first build downloads Go modules and can take minutes)`);
    // go.work pulls in ./cloud, which breaks the daemon build.
    const build = await run("go", ["build", "-o", binary, "./cmd/ao"], { cwd: join(repo, "backend"), env: { ...process.env, GOWORK: "off" }, timeoutMs: 900_000 });
    if (!build.ok) throw new Error(`AO build failed: ${(build.stderr || build.stdout).split("\n").slice(-12).join("\n")}`);
  }
  const port = await freePort();
  const baseURL = `http://127.0.0.1:${port}`;
  const env = {
    ...process.env,
    AO_DATA_DIR: join(root, "data"),
    AO_RUN_FILE: join(root, "running.json"),
    AO_PORT: String(port),
    AO_TELEMETRY_EVENTS: "off",
    AO_TELEMETRY_METRICS: "off",
    AO_TELEMETRY_REMOTE: "off",
  };
  // An inherited CI=1 makes some TUIs hide their input box.
  delete env.CI;
  // ACP Chat drivers run through AO's packaged bridge, which `go build` does
  // not include; use the checkout's built copy when there is one.
  const acpRuntime = join(repo, "frontend", "resources", "acp-runtime");
  if (!env.AO_ACP_RUNTIME_DIR && await access(join(acpRuntime, "package.json")).then(() => true, () => false)) env.AO_ACP_RUNTIME_DIR = acpRuntime;
  await mkdir(env.AO_DATA_DIR, { recursive: true });
  const child = spawn(binary, ["daemon"], { cwd: repo, env, stdio: ["ignore", "pipe", "pipe"], detached: process.platform !== "win32" });
  let logs = "";
  const keep = (chunk) => { logs = `${logs}${chunk}`.slice(-1 << 20); };
  child.stdout.on("data", keep);
  child.stderr.on("data", keep);
  let exited = false;
  child.on("close", () => { exited = true; });
  const ready = await waitUntil(async () => {
    if (exited) return { done: true };
    try { return { done: (await fetch(`${baseURL}/readyz`, { signal: AbortSignal.timeout(2_000) })).ok }; } catch { return { done: false }; }
  }, timeoutMs, 300);
  if (!ready.done || exited) {
    child.kill("SIGKILL");
    throw new Error(`AO daemon did not become ready: ${logs.split("\n").slice(-15).join("\n")}`);
  }
  return {
    baseURL,
    dataDir: env.AO_DATA_DIR,
    acpRuntime: env.AO_ACP_RUNTIME_DIR ?? null,
    logs: () => logs,
    stop: async () => {
      if (exited) return;
      child.kill("SIGTERM");
      if (!(await waitUntil(async () => ({ done: exited }), 8_000, 200)).done) child.kill("SIGKILL");
    },
  };
}

async function sqlite(dataDir, sql) {
  const out = await run("sqlite3", ["-readonly", join(dataDir, "ao.db"), sql], { timeoutMs: 10_000 });
  return out.ok ? out.stdout.trim() : "";
}

// ------------------------------------------------------------------ project

async function setupProject(ao, root, agent, tokens) {
  const projectId = `audit-${agent.replace(/[^a-z0-9-]/gi, "-").toLowerCase()}-${randomUUID().slice(0, 6)}`.slice(0, 63);
  const path = join(root, "projects", projectId);
  await mkdir(path, { recursive: true });
  await writeFile(join(path, "AGENTS.md"), `# AO audit project\n\nProject convention: every file you create or update whose name ends in PROOF.json must include the field "agentsMarker" with the value "${tokens.agents}".\n`);
  await writeFile(join(path, "README.md"), "# Disposable AO harness audit project\n");
  // AO resolves the default branch through origin/HEAD; without an origin,
  // spawn fails with DEFAULT_BRANCH_UNRESOLVED.
  for (const args of [
    ["init", "-q", "-b", "main"], ["add", "."],
    ["-c", "user.name=AO Audit", "-c", "user.email=audit@localhost", "commit", "-q", "-m", "audit fixture"],
    ["init", "-q", "--bare", "-b", "main", `${path}.origin.git`],
    ["remote", "add", "origin", `${path}.origin.git`], ["push", "-q", "origin", "main"], ["remote", "set-head", "origin", "main"],
  ]) {
    const out = await run("git", args, { cwd: path, timeoutMs: 30_000 });
    if (!out.ok) throw new Error(`git ${args.join(" ")}: ${out.stderr}`);
  }
  const response = await ao.post("projects", {
    path, projectId, name: `AO harness audit: ${agent}`,
    config: { defaultBranch: "main", agentRules: `Project convention: every file you create or update whose name ends in PROOF.json must include the field "rulesMarker" with the value "${tokens.rules}".` },
  });
  if (!response.ok) throw new Error(`could not register the audit project: HTTP ${response.status} ${JSON.stringify(response.data)}`);
  return projectId;
}

function firstPrompt(file, tokens) {
  return [
    `AO audit request token: ${tokens.initial}`,
    `History-only token (do not write it to any file yet): ${tokens.history}`,
    `Create ${file} in your current working directory (run pwd; not the AO session artifacts folder) as raw JSON.`,
    `If ${file} already exists, keep it and increment initialPromptExecutions; otherwise set it to 1.`,
    "Include cwd (the output of pwd), initialPromptToken (the request token) and initialPromptExecutions.",
    "Follow this project's conventions for PROOF.json files. Do not include the history-only token. Then stop and wait.",
  ].join("\n");
}

async function checkFirstProof(run_, prefix, proof, workspacePath, tokens, evidence) {
  await run_.add(`${prefix}_prompt`, proof?.initialPromptToken === tokens.initial && proof?.initialPromptExecutions === 1 ? "PASS" : "FAIL",
    !proof ? "the agent did not write the proof file before the timeout (prompt not delivered or not submitted)"
      : proof.initialPromptToken !== tokens.initial ? "the proof does not carry the request token"
      : proof.initialPromptExecutions === 1 ? "the first prompt was delivered and ran exactly once" : `the first prompt ran ${proof.initialPromptExecutions} times`, evidence);
  if (!proof) return;
  await run_.add(`${prefix}_worktree`, await samePath(proof.cwd, workspacePath) ? "PASS" : "FAIL",
    await samePath(proof.cwd, workspacePath) ? "the agent ran in the session's AO worktree" : `the agent ran in ${proof.cwd}, not the AO worktree ${workspacePath}`);
  await run_.add(`${prefix}_system_prompt`, proof.rulesMarker === tokens.rules ? "PASS" : "FAIL",
    proof.rulesMarker === tokens.rules ? "the agent followed a project rule it could only get from AO's system prompt"
      : "the agent did not follow the project rule from AO's system prompt: AO did not pass it, or the CLI ignored the channel AO used");
  await run_.add(`${prefix}_agents_md`, proof.agentsMarker === tokens.agents ? "PASS" : "FAIL",
    proof.agentsMarker === tokens.agents ? "the agent followed the project's AGENTS.md" : "the agent did not follow the project's AGENTS.md (the CLI may read a different file)");
}

// ------------------------------------------------------------------ tui

// Reads a session's terminal through AO's mux until every pattern matches the
// screen or the time is up. With no patterns it just captures the screen,
// which is the evidence for a prompt that never ran (an auth error, a dialog).
async function waitTerminalCue(baseURL, state, patterns, timeoutMs) {
  const target = state?.data?.session;
  const id = target?.terminalHandleId;
  if (!id) return { ok: false, reason: "the session has no terminal", screen: "" };
  const regexes = patterns.map((pattern) => new RegExp(pattern));
  if (!regexes.length) regexes.push(/(?!)/);
  const url = new URL(baseURL);
  url.protocol = "ws:";
  url.pathname = "/mux";
  const socket = new WebSocket(url);
  const decoder = new StringDecoder("utf8");
  let output = "";
  let failure = "";
  socket.addEventListener("open", () => socket.send(JSON.stringify({ ch: "terminal", type: "open", id, role: "secondary" })));
  socket.addEventListener("error", () => { failure = "terminal connection failed"; });
  socket.addEventListener("message", (event) => {
    let frame;
    try { frame = JSON.parse(String(event.data)); } catch { return; }
    if (frame.ch !== "terminal" || frame.id !== id) return;
    if (frame.type === "data") output = (output + decoder.write(Buffer.from(frame.data, "base64"))).slice(-262_144);
    if (frame.type === "exited" || frame.type === "error") failure = `terminal ${frame.type}`;
  });
  const seen = await waitUntil(async () => ({ done: Boolean(failure) || regexes.every((regex) => regex.test(screenText(output).slice(-65_536))) }), timeoutMs, 100);
  try { socket.close(); } catch { /* already closed */ }
  const screen = redact(screenText(output)).slice(-1500);
  return failure ? { ok: false, reason: failure, screen } : seen.done ? { ok: true, screen } : { ok: false, reason: "the ready pattern never matched the screen", screen };
}

async function auditTui(run_, { ao, baseURL, dataDir, projectId, tokens, timeoutMs }) {
  const model = run_.contract.model ? { model: run_.contract.model } : {};
  const ids = ["tui_spawn", "tui_prompt", "tui_worktree", "tui_system_prompt", "tui_agents_md", "tui_activity", "tui_native_id", "tui_follow_up", "tui_usage", "tui_stop", "tui_restore", "tui_restore_history"];
  const spawned = await ao.post("sessions", { projectId, kind: "worker", harness: run_.agent, mode: "tui", approvalMode: "bypass-permissions", displayName: `audit-${run_.agent}`, prompt: firstPrompt(PROOF, tokens), ...model });
  const sessionId = spawned.data?.session?.id;
  if (sessionId) run_.sessions.push(sessionId);
  if (!(await run_.add("tui_spawn", spawned.ok && sessionId && spawned.data.session.mode === "tui" ? "PASS" : "FAIL",
    spawned.ok && sessionId ? "AO created a TUI session" : `AO refused the TUI session: HTTP ${spawned.status} ${spawned.data?.error?.code ?? ""}`.trim(), spawned))) {
    return run_.skip(ids.slice(1), "no TUI session");
  }
  const workspacePath = (await ao.get(`desktop/sessions/${encodeURIComponent(sessionId)}/workspace`)).data?.workspacePath;

  let sawActive = false;
  const first = await waitUntil(async () => {
    const [state, file] = await Promise.all([ao.session(sessionId), ao.file(sessionId, PROOF)]);
    const activity = state.data?.session?.activity?.state;
    if (activity === "active") sawActive = true;
    return { done: Boolean(jsonFile(file)) && SETTLED.has(activity), proof: jsonFile(file), state };
  }, timeoutMs, 2_000);
  await checkFirstProof(run_, "tui", first.proof, workspacePath, tokens, { session: first.state?.data?.session, proof: first.proof });
  if (!first.proof) {
    // The screen usually says why: an auth error, a model the account cannot
    // use, a trust or update dialog.
    const screen = await waitTerminalCue(baseURL, first.state, [], 4_000);
    await writeFile(run_.checks.at(-1).evidence ?? join(run_.evidenceDir, "tui_prompt-screen.json"), `${redact(JSON.stringify({ session: first.state?.data?.session, screen: screen.screen.split("\n").filter((line) => line.trim()).slice(-40) }, null, 2))}\n`);
    // Name a provider-side cause the screen shows, so the report files it under the user.
    const cause = screen.screen.replace(/\s+/g, " ").match(/authentication failed|free limit reached|limit (?:reached|exhausted)|rate limit[a-z ]*|quota[a-z ]*|insufficient (?:credits|balance)|invalid api key|unauthori[sz]ed/i)?.[0];
    run_.checks.at(-1).reason = cause
      ? `the CLI shows "${cause}": the provider rejected the request (login, key or quota), not AO`
      : `${run_.checks.at(-1).reason}; the terminal screen is saved in the evidence file`;
    const artifacts = join(dataDir, "artifacts", sessionId, PROOF);
    if (await access(artifacts).then(() => true, () => false)) {
      run_.checks.at(-1).reason += `; it was written to the AO artifacts folder instead (${artifacts})`;
    }
    return run_.skip(ids.slice(2).filter((id) => !run_.checks.some((item) => item.id === id)), "the first prompt produced no proof file");
  }
  const activity = first.state?.data?.session?.activity?.state;
  await run_.add("tui_activity", sawActive && SETTLED.has(activity) ? "PASS" : "FAIL",
    sawActive && SETTLED.has(activity) ? `session status is tracked: AO showed the turn working, then ${activity}` : `session status is not tracked: AO never showed the turn as working, so the CLI's hooks did not report to \`ao hooks ${run_.agent}\` (last state ${activity})`, first.state);
  const nativeId = await sqlite(dataDir, `SELECT agent_session_id FROM sessions WHERE id='${sessionId.replaceAll("'", "''")}';`);
  await run_.add("tui_native_id", nativeId ? "PASS" : "FAIL", nativeId ? "AO stored the CLI's own session ID (needed for restore)" : "AO did not store the CLI's session ID: its SessionStart hook or ID capture did not run");

  const second = token("AO_SECOND");
  const sent = await ao.post(`sessions/${encodeURIComponent(sessionId)}/send`, { message: `Update ${PROOF}: add the field secondMessageToken with the value ${second}. Keep every other field, then stop and wait.` });
  const followed = sent.ok ? await waitUntil(async () => {
    const [file, state] = await Promise.all([ao.file(sessionId, PROOF), ao.session(sessionId)]);
    return { done: jsonFile(file)?.secondMessageToken === second && SETTLED.has(state.data?.session?.activity?.state) };
  }, timeoutMs, 2_000) : { done: false };
  await run_.add("tui_follow_up", followed.done ? "PASS" : "FAIL", followed.done ? "a follow-up message reached the running agent" : sent.ok ? "the follow-up was accepted but the agent never acted on it (typed but not submitted?)" : `AO refused the follow-up: HTTP ${sent.status}`, sent);

  const usage = await waitUntil(async () => {
    const response = await ao.get(`usage/sessions/${encodeURIComponent(sessionId)}`);
    const totals = response.data?.totals ?? {};
    return { done: (totals.inputTokens ?? 0) + (totals.outputTokens ?? 0) > 0, response };
  }, 30_000, 3_000);
  await run_.add("tui_usage", usage.done ? "PASS" : "FAIL", usage.done ? "AO recorded token usage" : "AO recorded no token usage for the session", usage.response);

  // Stop: in AO a running agent is stopped by archiving its session (Kill).
  // The agent starts a command that writes STARTED, sleeps, then writes LATE;
  // after Kill the command must be gone, so LATE never appears.
  const started = "AO_AUDIT_STARTED.txt";
  const late = "AO_AUDIT_LATE.txt";
  await ao.post(`sessions/${encodeURIComponent(sessionId)}/send`, { message: `Run this shell command in the foreground and wait for it to finish: sh -c 'echo started > ${started}; sleep 45; echo late > ${late}'` });
  const running = await waitUntil(async () => ({ done: Boolean((await ao.file(sessionId, started)).ok) }), 120_000, 1_000);
  const kill = await ao.post(`sessions/${encodeURIComponent(sessionId)}/kill`);
  const killed = await waitUntil(async () => ({ done: (await ao.session(sessionId)).data?.session?.isTerminated === true }), 60_000, 1_000);
  if (running.done && killed.done) {
    await sleep(55_000);
    const lateExists = await access(join(workspacePath, late)).then(() => true, () => false);
    await run_.add("tui_stop", lateExists ? "FAIL" : "PASS",
      lateExists ? "the command the agent was running kept going after Kill and wrote its late file: AO did not stop the agent's child processes" : "Kill stopped the agent and the command it was running", { kill });
  } else {
    await run_.add("tui_stop", killed.done ? "SKIP" : "FAIL", killed.done ? "the agent never started the test command, so stopping a running command was not checked" : "Kill did not terminate the session", { kill });
  }
  if (!killed.done) return run_.skip(["tui_restore", "tui_restore_history"], "the session was not killed");

  const restore = await ao.post(`sessions/${encodeURIComponent(sessionId)}/restore`);
  const restored = await waitUntil(async () => {
    const state = await ao.session(sessionId);
    return { done: state.data?.session?.isTerminated === false && state.data?.session?.statusReadiness === "ready", state };
  }, timeoutMs, 2_000);
  const nativeAfter = await sqlite(dataDir, `SELECT agent_session_id FROM sessions WHERE id='${sessionId.replaceAll("'", "''")}';`);
  const exact = restore.ok && restored.done && restore.data?.restoreMode === "native" && nativeAfter === nativeId;
  await run_.add("tui_restore", exact ? "PASS" : "FAIL",
    exact ? "AO restored the same CLI conversation (native restore)"
      : !restore.ok ? `AO refused the restore: HTTP ${restore.status}`
      : !restored.done ? "the restored session never became ready"
      : restore.data?.restoreMode !== "native" ? `AO restored in ${restore.data?.restoreMode} mode, not native: the conversation history is lost`
      : "the restored session has a different CLI session ID", { restore, state: restored.state?.data?.session });
  if (!exact) return run_.skip(["tui_restore_history"], "no native restore");

  const patterns = run_.contract.restoredReady?.patterns;
  if (Array.isArray(patterns) && patterns.length) {
    const cue = await waitTerminalCue(baseURL, restored.state, patterns, 60_000);
    if (!cue.ok) {
      await run_.add("tui_restore_history", "FAIL", `the restored CLI never showed its input box (${cue.reason}); fix restoredReady in the contract if the screen below is ready`, { screen: cue.screen });
      return;
    }
  } else {
    await sleep(10_000);
  }
  await ao.post(`sessions/${encodeURIComponent(sessionId)}/send`, { message: `Update ${PROOF}: add the field historyToken with the history-only token from my first message (from memory, not from a file). Keep every other field, then stop and wait.` });
  const history = await waitUntil(async () => ({ done: jsonFile(await ao.file(sessionId, PROOF))?.historyToken === tokens.history }), timeoutMs, 2_000);
  const after = jsonFile(await ao.file(sessionId, PROOF));
  await run_.add("tui_restore_history", history.done && after?.secondMessageToken === second ? "PASS" : "FAIL",
    history.done ? "the restored agent remembered the first message and kept the file" : "the restored agent did not remember the first message: history was not loaded, or the message was lost", { proof: after });
}

// ------------------------------------------------------------------ chat

function chatIdle(snapshot) {
  const turns = snapshot?.turns ?? [];
  const approvals = (snapshot?.activities ?? []).filter((item) => item.activityKind === "approval" && item.status === "pending");
  return snapshot?.controller === "ready" && !turns.some((turn) => turn.state === "running" || turn.state === "queued") && !approvals.length;
}

// Waits for `accept(proof)` with the Chat idle. A turn that fails after the
// first `turnsBefore` turns ends the wait early and its error is returned, so
// a provider error is reported as itself, not as a timeout.
async function waitChat(ao, sessionId, accept, timeoutMs, turnsBefore = Infinity) {
  const outcome = await waitUntil(async () => {
    const [conversation, file] = await Promise.all([ao.conversation(sessionId), ao.file(sessionId, CHAT_PROOF)]);
    const snapshot = conversation.data?.conversation ?? conversation.data;
    const proof = jsonFile(file);
    const failed = (snapshot?.turns ?? []).slice(turnsBefore).find((turn) => turn.state === "failed");
    return { done: (Boolean(accept(proof)) || Boolean(failed)) && chatIdle(snapshot), proof, snapshot, failed };
  }, timeoutMs, 1_000);
  return { ...outcome, done: outcome.done && !outcome.failed, error: outcome.failed?.errorMessage ?? "" };
}

const turnCount = async (ao, sessionId) => {
  const conversation = (await ao.conversation(sessionId)).data;
  return ((conversation?.conversation ?? conversation)?.turns ?? []).length;
};

const sendChat = (ao, sessionId, text) => ao.post(`sessions/${encodeURIComponent(sessionId)}/conversation/messages`, { text, clientMessageId: token("audit") });

function chatSummary(snapshot) {
  return snapshot && {
    controller: snapshot.controller,
    turns: (snapshot.turns ?? []).slice(-4).map((turn) => ({ state: turn.state, error: turn.errorMessage })),
    lastAssistant: [...(snapshot.messages ?? [])].reverse().find((item) => item.role === "assistant")?.text?.slice(-400),
  };
}

async function auditChat(run_, { ao, dataDir, projectId, tokens, timeoutMs }) {
  const ids = ["chat_spawn", "chat_prompt", "chat_worktree", "chat_system_prompt", "chat_agents_md", "chat_follow_up", "chat_models", "chat_restore", "chat_restore_history"];
  const spawned = await ao.post("sessions", { projectId, kind: "worker", harness: run_.agent, mode: "chat", approvalMode: "bypass-permissions", displayName: `audit-chat-${run_.agent}`, prompt: firstPrompt(CHAT_PROOF, tokens), ...(run_.contract.model ? { model: run_.contract.model } : {}) });
  const sessionId = spawned.data?.session?.id;
  if (sessionId) run_.sessions.push(sessionId);
  if (!(await run_.add("chat_spawn", spawned.ok && sessionId && spawned.data.session.mode === "chat" ? "PASS" : "FAIL",
    spawned.ok && sessionId ? (spawned.data.session.mode === "chat" ? "AO created a Chat session" : `AO created a ${spawned.data.session.mode} session instead of Chat`)
      : `AO refused the Chat session: HTTP ${spawned.status} ${spawned.data?.error?.code ?? ""} ${spawned.data?.error?.message ?? ""}`.trim(), spawned))) {
    return run_.skip(ids.slice(1), "no Chat session");
  }
  const workspacePath = (await ao.get(`desktop/sessions/${encodeURIComponent(sessionId)}/workspace`)).data?.workspacePath;
  const first = await waitChat(ao, sessionId, (proof) => proof?.initialPromptToken === tokens.initial, timeoutMs, 0);
  await checkFirstProof(run_, "chat", first.proof, workspacePath, tokens, { conversation: chatSummary(first.snapshot), proof: first.proof });
  if (!first.proof && first.error) run_.checks.find((item) => item.id === "chat_prompt").reason = `the first Chat turn failed: ${first.error}`;
  if (!first.proof) return run_.skip(ids.slice(2).filter((id) => !run_.checks.some((item) => item.id === id)), "the first Chat turn produced no proof file");

  const second = token("AO_CHAT_SECOND");
  const beforeFollowUp = await turnCount(ao, sessionId);
  const sent = await sendChat(ao, sessionId, `Update ${CHAT_PROOF}: add the field secondMessageToken with the value ${second}. Keep every other field, then stop.`);
  const followed = sent.ok ? await waitChat(ao, sessionId, (proof) => proof?.secondMessageToken === second, timeoutMs, beforeFollowUp) : { done: false };
  await run_.add("chat_follow_up", followed.done ? "PASS" : "FAIL", followed.done ? "a follow-up Chat message was answered" : followed.error ? `the follow-up turn failed: ${followed.error}` : sent.ok ? "the follow-up turn never finished" : `AO refused the follow-up: HTTP ${sent.status}`, { send: sent, conversation: chatSummary(followed.snapshot) });

  // Models come either as a list or, for some ACP drivers, as a "model" config option.
  const listed = await ao.get(`sessions/${encodeURIComponent(sessionId)}/conversation/models`);
  const options = await ao.get(`sessions/${encodeURIComponent(sessionId)}/conversation/config-options`);
  const listModels = (listed.data?.models ?? []).map((item) => item.id).filter(Boolean);
  const modelOption = (options.data?.options ?? []).find((option) => option.category === "model" || option.id === "model");
  const optionModels = (modelOption?.choices ?? []).map((choice) => choice.value).filter(Boolean);
  if (listModels.length) {
    const target = listModels.find((id) => id !== listed.data?.selected?.model) ?? listModels[0];
    const patch = await ao.patch(`sessions/${encodeURIComponent(sessionId)}/conversation/settings`, { model: target });
    const now_ = (await ao.conversation(sessionId)).data;
    const model = (now_?.conversation ?? now_)?.settings?.model;
    await run_.add("chat_models", patch.ok && model === target ? "PASS" : "FAIL", patch.ok && model === target ? `Chat lists ${listModels.length} model(s) and switched to ${target}` : `switching the Chat model to ${target} did not stick (now ${model})`, { listed: listModels, patch });
    // Switch back: the picked model may not be one this account can run.
    if (listed.data?.selected?.model) await ao.patch(`sessions/${encodeURIComponent(sessionId)}/conversation/settings`, { model: listed.data.selected.model });
  } else if (optionModels.length) {
    const target = optionModels.find((value) => value !== modelOption.currentValue) ?? optionModels[0];
    const patch = await ao.patch(`sessions/${encodeURIComponent(sessionId)}/conversation/config-options/${encodeURIComponent(modelOption.id)}`, { value: target });
    const current = ((await ao.get(`sessions/${encodeURIComponent(sessionId)}/conversation/config-options`)).data?.options ?? []).find((option) => option.id === modelOption.id)?.currentValue;
    await run_.add("chat_models", patch.ok && current === target ? "PASS" : "FAIL", patch.ok && current === target ? `Chat offers ${optionModels.length} model(s) and switched to ${target}` : `switching the Chat model to ${target} did not stick (now ${current})`, { choices: optionModels, patch });
    // Switch back: the picked model may not be one this account can run.
    if (modelOption.currentValue) await ao.patch(`sessions/${encodeURIComponent(sessionId)}/conversation/config-options/${encodeURIComponent(modelOption.id)}`, { value: modelOption.currentValue });
  } else {
    await run_.add("chat_models", "FAIL", "Chat offers no models (neither a model list nor a model option)", { listed, options });
  }

  const providerId = async () => sqlite(dataDir, `SELECT provider_conversation_id FROM sessions WHERE id='${sessionId.replaceAll("'", "''")}';`);
  const before = await providerId();
  const kill = await ao.post(`sessions/${encodeURIComponent(sessionId)}/kill`);
  const killed = await waitUntil(async () => ({ done: (await ao.session(sessionId)).data?.session?.isTerminated === true }), 60_000, 1_000);
  if (!killed.done) {
    await run_.add("chat_restore", "FAIL", "Kill did not terminate the Chat session", { kill });
    return run_.skip(["chat_restore_history"], "the Chat session was not killed");
  }
  const restore = await ao.post(`sessions/${encodeURIComponent(sessionId)}/restore`);
  const ready = await waitUntil(async () => {
    const state = await ao.session(sessionId);
    const conversation = await ao.conversation(sessionId);
    return { done: state.data?.session?.isTerminated === false && chatIdle(conversation.data?.conversation ?? conversation.data) };
  }, timeoutMs, 2_000);
  const after = await providerId();
  const same = restore.ok && ready.done && before && before === after;
  await run_.add("chat_restore", same ? "PASS" : "FAIL",
    same ? "AO restored the same Chat conversation" : !restore.ok ? `AO refused the restore: HTTP ${restore.status}` : !ready.done ? "the restored Chat never became ready" : "the restored Chat is a different conversation: history is lost", { restore });
  if (!same) return run_.skip(["chat_restore_history"], "no restore");
  const beforeRecall = await turnCount(ao, sessionId);
  const recall = await sendChat(ao, sessionId, `Update ${CHAT_PROOF}: add the field historyToken with the history-only token from my first message (from memory, not from a file). Keep every other field, then stop.`);
  const recalled = recall.ok ? await waitChat(ao, sessionId, (proof) => proof?.historyToken === tokens.history, timeoutMs, beforeRecall) : { done: false };
  await run_.add("chat_restore_history", recalled.done ? "PASS" : "FAIL", recalled.done ? "the restored Chat remembered the first message" : recalled.error ? `the turn after restore failed: ${recalled.error}` : "the restored Chat did not remember the first message", { conversation: chatSummary(recalled.snapshot) });
}

// ------------------------------------------------------------------ roles

// V1 system prompt check: a standalone worker and a project orchestrator are
// each asked what they are and must answer from AO's system prompt alone.
async function auditRoles(run_, { ao, dataDir, projectId, mode, timeoutMs }) {
  const prompt = `Are you a worker or an orchestrator? Answer only from your AO system instructions, not from this message. Save exactly one lowercase word, worker or orchestrator, as ${ROLE_FILE} in your AO session artifacts folder, then stop.`;
  for (const kind of ["worker", "orchestrator"]) {
    const spawned = await ao.post("sessions", { ...(kind === "orchestrator" ? { projectId } : {}), kind, harness: run_.agent, mode, approvalMode: "bypass-permissions", displayName: `audit-${kind}-${run_.agent}`, prompt, ...(run_.contract.model ? { model: run_.contract.model } : {}) });
    const sessionId = spawned.data?.session?.id;
    if (!sessionId) {
      await run_.add(`role_${kind}`, "FAIL", `AO did not create the ${kind} session: HTTP ${spawned.status}`, spawned);
      continue;
    }
    run_.sessions.push(sessionId);
    const answer = await waitUntil(async () => {
      const file = await ao.file(sessionId, ROLE_FILE);
      let text = file.ok ? String(file.data?.content ?? "") : "";
      if (!text) text = await readFile(join(dataDir, "artifacts", sessionId, ROLE_FILE), "utf8").catch(() => "");
      return { done: Boolean(text.trim()), text: text.trim().toLowerCase().replace(/[^a-z]/g, "") };
    }, timeoutMs, 2_000);
    await run_.add(`role_${kind}`, answer.text === kind ? "PASS" : "FAIL",
      answer.text === kind ? `the ${kind} answered "${kind}": its role prompt reached the agent`
        : answer.done ? `the ${kind} answered "${answer.text}": AO's ${kind} prompt did not reach the agent` : `the ${kind} gave no answer before the timeout`);
  }
}

// ------------------------------------------------------------------ triage

// Where to start for each failing check. {tui} and {chat} are the harness's
// AO adapter and Chat driver folders (contract `adapters`, else guessed).
const FIX_HINTS = {
  ao_registered: ["backend/internal/adapters/agent/registry/", "run scripts/harness_touchpoints.mjs --harness {id}"],
  ao_installed: ["backend/internal/adapters/agent/{tui}/ (binary resolution)", "backend/internal/service/systeminstall/agentplans.go"],
  ao_authenticated: ["backend/internal/adapters/agent/{tui}/ (auth probe)", "backend/internal/service/agentauth/"],
  ao_models: ["backend/internal/adapters/agent/{tui}/ (model catalog)", "backend/internal/adapters/agent/modelcatalog/"],
  tui_spawn: ["backend/internal/adapters/agent/{tui}/ (launch command)", "backend/internal/session_manager/manager.go"],
  tui_prompt: ["backend/internal/adapters/agent/{tui}/ (prompt delivery, ready cue)"],
  tui_worktree: ["backend/internal/adapters/agent/{tui}/ (working directory)"],
  tui_system_prompt: ["backend/internal/adapters/agent/{tui}/ (system-prompt flag or file)", "backend/internal/session_manager/prompt.go"],
  tui_agents_md: ["backend/internal/adapters/agent/{tui}/ (project instruction file)"],
  tui_activity: ["backend/internal/adapters/agent/{tui}/ (GetAgentHooks: are the hooks installed?)", "backend/internal/adapters/agent/activitydispatch/dispatch.go (are the callbacks mapped?)", "backend/internal/cli/hooks.go"],
  tui_native_id: ["backend/internal/adapters/agent/{tui}/ (SessionStart hook or SessionInfo)", "backend/internal/cli/hooks.go"],
  tui_follow_up: ["backend/internal/adapters/agent/{tui}/ (input submission)"],
  tui_usage: ["backend/internal/adapters/agent/{tui}/ (usage from hooks or transcript)"],
  tui_stop: ["backend/internal/session_manager/manager.go (Kill)", "backend/internal/adapters/agent/{tui}/"],
  tui_restore: ["backend/internal/session_manager/native_restore.go", "backend/internal/adapters/agent/{tui}/ (resume arguments)"],
  tui_restore_history: ["backend/internal/adapters/agent/{tui}/ (resume arguments)", "contract restoredReady"],
  chat_spawn: ["backend/internal/adapters/chatdriver/{chat}/", "backend/internal/session_manager/chat_spawn.go"],
  chat_prompt: ["backend/internal/adapters/chatdriver/{chat}/", "backend/internal/service/chat/controller.go"],
  chat_worktree: ["backend/internal/adapters/chatdriver/{chat}/ (session cwd)"],
  chat_system_prompt: ["backend/internal/adapters/chatdriver/{chat}/ (system prompt at session start)"],
  chat_agents_md: ["backend/internal/adapters/chatdriver/{chat}/"],
  chat_follow_up: ["backend/internal/adapters/chatdriver/{chat}/", "backend/internal/service/chat/controller.go"],
  chat_models: ["backend/internal/adapters/chatdriver/{chat}/ (models or config options)"],
  chat_restore: ["backend/internal/adapters/chatdriver/{chat}/ (load or resume session)"],
  chat_restore_history: ["backend/internal/adapters/chatdriver/{chat}/ (load or resume session)"],
  role_worker: ["backend/internal/session_manager/prompt.go", "the harness's system-prompt channel"],
  role_orchestrator: ["backend/internal/session_manager/prompt.go", "the harness's system-prompt channel"],
};

const USER_TEXT = /authentication failed|not logged in|log ?in|unauthori[sz]ed|api key|quota|rate.?limit|limit exhausted|\b429\b|billing|subscription|not on PATH|install it/i;

export function triage(agent, contract, check) {
  if (/CHAT_DRIVER_UNAVAILABLE/.test(check.reason)) return { owner: "setup", action: "The machine is missing AO's Chat bridge: run `npm ci && npm run build:acp-runtime` in frontend/ (or set AO_ACP_RUNTIME_DIR), then rerun." };
  if (check.status === "BLOCKED" || USER_TEXT.test(check.reason)) return { owner: "user", action: "Only the user can do this (login, API key, quota or install). Ask them, then rerun." };
  if (check.id.startsWith("cli_")) return { owner: "contract", action: "The CLI itself fails outside AO: fix the contract's command, or this is an upstream CLI problem." };
  if (/restoredReady|ready pattern/.test(check.reason)) return { owner: "contract", action: "Compare the screen in the evidence file with the contract's restoredReady pattern and fix the pattern." };
  const plain = agent.replaceAll("-", "");
  const dirs = { tui: plain, chat: `${plain}acp`, ...contract?.adapters };
  const where = (FIX_HINTS[check.id] ?? ["backend/internal/adapters/agent/{tui}/"]).map((hint) => hint.replaceAll("{tui}", dirs.tui).replaceAll("{chat}", dirs.chat).replaceAll("{id}", agent));
  return { owner: "ao", action: "Fix in the AO implementation, add a Go test that fails without the fix, then rerun this agent.", where };
}

// ------------------------------------------------------------------ report

function markdown(report) {
  const lines = [`# AO harness audit`, "", `Started ${report.startedAt} · AO checkout ${report.repo} (${report.commit}) · Chat bridge: ${report.acpRuntime ? "found" : "not built"}`, ""];
  lines.push("| Agent | Pass | Fail | Blocked | Skip |", "|---|---|---|---|---|");
  for (const result of report.agents) {
    const count = (status) => result.checks.filter((item) => item.status === status).length;
    lines.push(`| ${result.agent} | ${count("PASS")} | ${count("FAIL")} | ${count("BLOCKED")} | ${count("SKIP")} |`);
  }
  for (const result of report.agents) {
    lines.push("", `## ${result.agent}`, "", "| Check | Result | Why | Evidence |", "|---|---|---|---|");
    for (const item of result.checks) {
      lines.push(`| ${item.id} | ${item.status} | ${item.reason.replaceAll("|", "\\|")} | ${item.evidence ? `\`evidence/${result.agent}/${item.evidence.split("/").pop()}\`` : ""} |`);
    }
    const failures = result.checks.filter((item) => item.status === "FAIL" || item.status === "BLOCKED");
    if (!failures.length) continue;
    lines.push("", `### Fix (${result.agent})`, "");
    for (const owner of ["ao", "contract", "setup", "user"]) {
      const items = failures.filter((item) => item.triage.owner === owner);
      if (!items.length) continue;
      lines.push(`**${{ ao: "AO implementation", contract: "Harness contract or upstream CLI", setup: "Machine setup", user: "Needs the user" }[owner]}**`, "");
      for (const item of items) lines.push(`- \`${item.id}\`: ${item.reason}${item.triage.where ? `. Look in: ${item.triage.where.map((path) => `\`${path}\``).join(", ")}` : ""}`);
      lines.push("", `_${items[0].triage.action}_`, "");
    }
  }
  return `${redact(lines.join("\n"))}\n`;
}

// ------------------------------------------------------------------ main

function parseArgs(argv) {
  const options = { agents: [], repo: "", contracts: join(SKILL_DIR, "assets", "local-contracts.json"), out: "", ao: "", timeoutSeconds: 300, roles: true, keep: false };
  for (let index = 0; index < argv.length; index += 1) {
    const flag = argv[index];
    const value = () => argv[++index] ?? (() => { throw new Error(`${flag} needs a value`); })();
    if (flag === "--agent") options.agents.push(value());
    else if (flag === "--repo") options.repo = value();
    else if (flag === "--contracts") options.contracts = value();
    else if (flag === "--out") options.out = value();
    else if (flag === "--ao") options.ao = value();
    else if (flag === "--timeout-seconds") options.timeoutSeconds = Number(value());
    else if (flag === "--no-roles") options.roles = false;
    else if (flag === "--keep") options.keep = true;
    else if (flag === "--help" || flag === "-h") options.help = true;
    else throw new Error(`unknown option ${flag}`);
  }
  return options;
}

async function findRepo(start) {
  for (let dir = resolve(start); dir !== dirname(dir); dir = dirname(dir)) {
    if (await access(join(dir, "backend", "cmd", "ao")).then(() => true, () => false)) return dir;
  }
  return "";
}

async function main() {
  const options = parseArgs(process.argv.slice(2));
  if (options.help) {
    console.log("usage: audit.mjs [--agent ID]... [--repo AO_CHECKOUT] [--out DIR] [--contracts FILE] [--ao PREBUILT_AO] [--timeout-seconds N] [--no-roles] [--keep]");
    return;
  }
  const repo = options.repo ? resolve(expandHome(options.repo)) : await findRepo(SKILL_DIR);
  if (!repo) throw new Error("AO checkout not found; pass --repo");
  const contracts = JSON.parse(await readFile(resolve(expandHome(options.contracts)), "utf8")).agents ?? {};
  const agents = options.agents.length ? options.agents : Object.keys(contracts);
  const out = resolve(expandHome(options.out || join(homedir(), "Desktop", "agents", "ao-audit-reports", new Date().toISOString().replace(/[:.]/g, "-").slice(0, 19))));
  await mkdir(out, { recursive: true });
  logFile = join(out, "run.log");
  // Short root: Unix socket paths break past ~104 bytes (a long TMPDIR does that).
  const root = await mkdtemp(process.platform === "win32" ? join(homedir(), "ao-audit-") : "/tmp/ao-audit-");
  const timeoutMs = options.timeoutSeconds * 1000;
  const commit = (await run("git", ["-C", repo, "log", "-1", "--format=%h %cs"], { timeoutMs: 10_000 })).stdout;
  const report = { startedAt: now(), repo, commit, out, agents: [] };
  log(`Report: ${join(out, "report.md")}`);

  let daemon = null;
  try {
    daemon = await startDaemon({ repo, root, aoBinary: options.ao, timeoutMs: 120_000 });
    report.acpRuntime = daemon.acpRuntime;
    const ao = api(daemon.baseURL);
    for (const agent of agents) {
      const contract = contracts[agent];
      const evidenceDir = join(out, "evidence", agent);
      await mkdir(evidenceDir, { recursive: true });
      const run_ = new AgentRun(agent, contract ?? {}, evidenceDir);
      report.agents.push(run_);
      log(`\n${agent}`);
      if (!contract) {
        await run_.add("contract", "FAIL", `no entry for ${agent} in ${options.contracts}; copy assets/contract-template.json`);
        continue;
      }
      try {
        await auditCli(run_, contract);
        const probe = await ao.post(`agents/${encodeURIComponent(agent)}/probe`);
        const readiness = await ao.post("agents/readiness/ensure", { agentIds: [agent], purpose: "launch" });
        const snapshot = readiness.data?.agents?.find((item) => item?.id === agent);
        const registered = probe.ok && probe.data?.supported === true;
        await run_.add("ao_registered", registered ? "PASS" : "FAIL", registered ? "AO knows this harness" : "this AO build does not know the harness: it is not registered", probe);
        if (!registered) continue;
        const installed = snapshot?.installation?.state === "installed";
        await run_.add("ao_installed", installed ? "PASS" : "BLOCKED", installed ? "AO found the installed CLI" : snapshot?.installation?.reason || "AO did not find the CLI", readiness);
        const auth = snapshot?.authentication?.state;
        const authorized = auth === "authorized" || auth === "not_applicable";
        await run_.add("ao_authenticated", authorized ? "PASS" : auth === "configured" || auth === "unknown" ? "FAIL" : "BLOCKED",
          authorized ? `AO verified the login (${auth})`
            : auth === "configured" ? "credentials exist but AO could not verify them: the adapter needs a real auth probe"
            : snapshot?.authentication?.reason || `AO reports authentication ${auth ?? "unknown"}`, readiness);
        const models = await ao.get(`agents/${encodeURIComponent(agent)}/models`);
        const aoModels = models.ok ? modelIds(models.data) : [];
        const freeForm = models.ok && models.data?.selectionMode === "text";
        await run_.add("ao_models", aoModels.length || freeForm ? "PASS" : "FAIL",
          aoModels.length ? `AO lists ${aoModels.length} model(s)${run_.cliModels?.length && !aoModels.some((id) => run_.cliModels.includes(id)) ? ", none of them in the CLI's own list" : ""}`
            : freeForm ? "AO uses free-form model entry (the CLI has no model list)" : "AO lists no models for this harness", models);
        if (!installed || !authorized) {
          await run_.add("live", "SKIP", "AO cannot launch the harness yet (see above); live checks did not run");
          continue;
        }
        const tokens = { initial: token("AO_INITIAL"), history: token("AO_HISTORY"), rules: token("AO_RULES"), agents: token("AO_AGENTS_MD") };
        const projectId = await setupProject(ao, root, agent, tokens);
        const surfaces = Array.isArray(contract.surfaces) && contract.surfaces.length ? contract.surfaces : ["tui"];
        const context = { ao, baseURL: daemon.baseURL, dataDir: daemon.dataDir, projectId, tokens, timeoutMs };
        if (surfaces.includes("tui")) await auditTui(run_, context);
        if (surfaces.includes("chat")) await auditChat(run_, { ...context, tokens: { ...tokens, initial: token("AO_CHAT_INITIAL"), history: token("AO_CHAT_HISTORY") } });
        if (options.roles) await auditRoles(run_, { ...context, mode: surfaces.includes("tui") ? "tui" : "chat" });
      } catch (error) {
        await run_.add("runner", "FAIL", `the audit stopped: ${error.message}`, { stack: error.stack });
      } finally {
        for (const sessionId of run_.sessions) await ao.post(`sessions/${encodeURIComponent(sessionId)}/kill`, {}, 15_000);
      }
    }
  } catch (error) {
    report.error = error.message;
    log(`error: ${error.message}`);
  } finally {
    if (daemon) {
      await writeFile(join(out, "daemon.log"), redact(daemon.logs()));
      await daemon.stop();
    }
    if (!options.keep) await rm(root, { recursive: true, force: true }).catch(() => {});
  }
  for (const result of report.agents) {
    for (const item of result.checks) if (item.status === "FAIL" || item.status === "BLOCKED") item.triage = triage(result.agent, result.contract, item);
  }
  const serializable = { ...report, agents: report.agents.map(({ agent, checks }) => ({ agent, checks })) };
  await writeFile(join(out, "report.json"), `${redact(JSON.stringify(serializable, null, 2))}\n`);
  await writeFile(join(out, "report.md"), report.error ? `# AO harness audit\n\nThe audit could not start: ${redact(report.error)}\n` : markdown(serializable));
  log(`\nReport: ${join(out, "report.md")}`);
  process.exitCode = report.error || serializable.agents.some((result) => result.checks.some((item) => item.status === "FAIL")) ? 1 : 0;
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main().catch((error) => {
    console.error(error.message);
    process.exitCode = 2;
  });
}

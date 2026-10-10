#!/usr/bin/env node

import { createHash, randomUUID } from "node:crypto";
import { spawn } from "node:child_process";
import { constants as fsConstants } from "node:fs";
import { access, lstat, mkdir, mkdtemp, readFile, realpath, rename, writeFile } from "node:fs/promises";
import { createServer } from "node:net";
import { homedir, tmpdir } from "node:os";
import { delimiter, dirname, extname, isAbsolute, join, parse, relative, resolve, sep } from "node:path";
import { performance } from "node:perf_hooks";
import { isDeepStrictEqual, stripVTControlCharacters } from "node:util";
import { StringDecoder } from "node:string_decoder";
import { pathToFileURL } from "node:url";
import { authenticationAction, createEvidenceStore, redactOutput, sanitizeEvidence } from "./audit_evidence.mjs";
export { createEvidenceStore } from "./audit_evidence.mjs";

const LIVE_GATES = [
  "tui_spawn",
  "correct_working_directory",
  "initial_prompt_exactly_once",
  "hidden_ao_instructions",
  "project_agents_md",
  "proof_file_creation",
  "second_message",
  "activity_status",
  "cancellation",
  "native_session_id",
  "termination",
  "native_restore",
  "same_ao_session_workspace",
  "post_restore_terminal_ready",
  "post_restore_message",
  "history_file_continuity",
  "system_prompt_restore",
];

const LOCAL_GATES = [
  "local_binary",
  "local_version",
  "local_integration",
  "local_session_spawn",
  "local_models_list",
];

const SETTLED_STATES = new Set(["idle", "waiting_input", "blocked"]);
const CANCELLATION_PROMPT = "For cancellation testing, run a foreground command that waits for 120 seconds. Do not modify files. Wait for the command to finish.";

function now() {
  return new Date().toISOString();
}

function sleep(ms) {
  return new Promise((done) => setTimeout(done, ms));
}

function expandHome(value) {
  if (value === "~") return homedir();
  if (value.startsWith("~/")) return join(homedir(), value.slice(2));
  return value;
}

function token(prefix) {
  return `${prefix}_${randomUUID().replaceAll("-", "")}`;
}

function hash(value) {
  return createHash("sha256").update(value).digest("hex");
}

function gate(name, status, reason, evidence = undefined) {
  return { name, status, reason, ...(evidence === undefined ? {} : { evidence }) };
}

function firstFailedGate(gates) {
  return gates.find((item) => item.status !== "PASS")?.name ?? "";
}

export function overallStatus(results) {
  const statuses = results.map((item) => item.status);
  if (statuses.includes("FAIL")) return "FAIL";
  if (statuses.includes("BLOCKED")) return "BLOCKED";
  if (statuses.includes("NOT_RUN")) return "NOT_RUN";
  return statuses.length > 0 && statuses.every((status) => status === "PASS") ? "PASS" : "NOT_RUN";
}

async function request({ baseURL, method = "GET", path, body, timeoutMs = 30_000 }) {
  const startedAt = now();
  const started = performance.now();
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutMs);
  try {
    const response = await fetch(`${baseURL.replace(/\/$/, "")}/api/v1/${path.replace(/^\//, "")}`, {
      method,
      headers: body === undefined ? undefined : { "content-type": "application/json" },
      body: body === undefined ? undefined : JSON.stringify(body),
      signal: controller.signal,
    });
    const text = await response.text();
    let payload = null;
    if (text) {
      try {
        payload = JSON.parse(text);
      } catch {
        payload = { unparsed: text };
      }
    }
    return {
      ok: response.ok,
      status: response.status,
      requestId: response.headers.get("x-request-id") ?? "",
      method,
      path: `/api/v1/${path.replace(/^\//, "")}`,
      requestBody: body,
      response: payload,
      startedAt,
      finishedAt: now(),
      durationMs: Math.round(performance.now() - started),
    };
  } catch (error) {
    return {
      ok: false,
      status: 0,
      requestId: "",
      method,
      path: `/api/v1/${path.replace(/^\//, "")}`,
      requestBody: body,
      response: null,
      error: error instanceof Error ? error.message : String(error),
      startedAt,
      finishedAt: now(),
      durationMs: Math.round(performance.now() - started),
    };
  } finally {
    clearTimeout(timer);
  }
}

function apiEvidence(response) {
  return {
    method: response.method,
    path: response.path,
    status: response.status,
    requestId: response.requestId,
    response: response.response,
    error: response.error,
    startedAt: response.startedAt,
    finishedAt: response.finishedAt,
    durationMs: response.durationMs,
    requestBodySha256: response.requestBody === undefined ? undefined : hash(JSON.stringify(response.requestBody)),
  };
}

export async function discoverAgents({ baseURL, timeoutMs = 30_000 }) {
  const response = await request({ baseURL, path: "agents", timeoutMs });
  if (!response.ok) {
    const error = new Error(`GET /api/v1/agents failed: HTTP ${response.status || "network"}`);
    error.evidence = apiEvidence(response);
    throw error;
  }
  const supported = response.response?.supported;
  if (!Array.isArray(supported)) {
    const error = new Error("GET /api/v1/agents returned no supported inventory");
    error.evidence = apiEvidence(response);
    throw error;
  }
  return [...new Set(supported.map((item) => item?.id).filter((id) => typeof id === "string" && id.trim()))].sort();
}

function preflightOutcome(probe, readiness, agent) {
  const gates = [];
  const probeEvidence = apiEvidence(probe);
  const readinessEvidence = apiEvidence(readiness);
  const snapshot = readiness.response?.agents?.find((item) => item?.id === agent);

  if (!probe.ok) {
    gates.push(gate("registered", "FAIL", "targeted AO probe failed", probeEvidence));
    return gates;
  }
  if (probe.response?.supported !== true) {
    gates.push(gate("registered", "FAIL", "agent is not registered in this AO build", probeEvidence));
    return gates;
  }
  gates.push(gate("registered", "PASS", "agent is registered", probeEvidence));

  if (!readiness.ok || !snapshot) {
    gates.push(gate("installed", "BLOCKED", "launch readiness returned no snapshot", readinessEvidence));
    return gates;
  }
  if (probe.response?.installed !== true || snapshot.installation?.state !== "installed") {
    gates.push(gate("installed", "BLOCKED", snapshot.installation?.reason || "agent executable is not installed", readinessEvidence));
    return gates;
  }
  gates.push(gate("installed", "PASS", "AO resolved the installed executable", readinessEvidence));

  const fresh = snapshot.installation?.freshness === "fresh" && snapshot.authentication?.freshness === "fresh";
  gates.push(gate(
    "fresh_probe",
    fresh ? "PASS" : "BLOCKED",
    fresh ? "installation and authentication observations are fresh" : "readiness observations are not fresh",
    { probe: probeEvidence, readiness: readinessEvidence },
  ));
  if (!fresh) return gates;

  const authState = snapshot.authentication?.state;
  const probeAuthState = probe.response?.agent?.authStatus;
  const authenticated = (authState === "authorized" || authState === "not_applicable")
    && probeAuthState === "authorized";
  gates.push(gate(
    "authentication",
    authenticated ? "PASS" : "BLOCKED",
    authenticated ? `authentication state is ${authState}` : snapshot.authentication?.reason || `authentication state is ${authState || "unknown"}`,
    { probe: probeEvidence, readiness: readinessEvidence },
  ));
  if (!authenticated) return gates;

  const ready = snapshot.effectiveReadiness === "ready";
  gates.push(gate(
    "launch_readiness",
    ready ? "PASS" : "BLOCKED",
    ready ? "AO reports effective launch readiness" : `effective readiness is ${snapshot.effectiveReadiness || "unknown"}`,
    readinessEvidence,
  ));
  return gates;
}

function markNotRun(gates, reason) {
  const present = new Set(gates.map((item) => item.name));
  for (const name of LIVE_GATES) {
    if (!present.has(name)) gates.push(gate(name, "NOT_RUN", reason));
  }
}

function resultFrom(agent, gates, extra = {}) {
  const userAction = gates.map(item => authenticationAction(agent, item)).find(Boolean);
  return {
    agent,
    status: overallStatus(gates),
    failedGate: firstFailedGate(gates),
    gates,
    ...(userAction ? { userAction } : {}),
    ...extra,
  };
}

function gateRecorder(gates, onIssue, context) {
  return async (...items) => {
    gates.push(...items);
    for (const item of items) {
      if (item.status === "FAIL" || item.status === "BLOCKED") {
        await onIssue?.({ ...context(), gate: item });
      }
    }
  };
}

async function waitUntil(check, timeoutMs, intervalMs = 1_000) {
  const deadline = performance.now() + timeoutMs;
  let last;
  while (performance.now() < deadline) {
    last = await check();
    if (last?.done) return last;
    await sleep(intervalMs);
  }
  return { done: false, ...(last ?? {}), timeout: true };
}

async function sessionState(baseURL, sessionId, timeoutMs) {
  return request({ baseURL, path: `sessions/${encodeURIComponent(sessionId)}`, timeoutMs });
}

export async function interruptActiveTurn({ baseURL, sessionId, startResponse, timeoutMs = 30_000, interruptSpec = null }) {
  if (!interruptSpec) return { passed: false, status: "NOT_RUN", observedActive: false, reason: "native interrupt contract is missing; no cancellation input was sent" };
  if (!startResponse?.ok) return { passed: false, observedActive: false, reason: "cancellation test message was not accepted" };
  const boundedTimeout = Math.min(timeoutMs, 30_000);
  const active = await waitUntil(async () => {
    const stateResponse = await sessionState(baseURL, sessionId, boundedTimeout);
    return {
      done: stateResponse.response?.session?.activity?.state === "active",
      stateResponse,
    };
  }, boundedTimeout, Math.min(250, Math.max(10, Math.floor(boundedTimeout / 10))));
  if (!active.done) {
    return {
      passed: false,
      observedActive: false,
      reason: "the cancellation turn was never observed active",
      activeState: active.stateResponse,
    };
  }
  return interruptViaMux({ baseURL, sessionId, activeState: active.stateResponse, timeoutMs, spec: interruptSpec });
}

async function interruptViaMux({ baseURL, sessionId, activeState, timeoutMs, spec }) {
  const state = activeState.response?.session;
  const terminalId = state?.terminalHandleId;
  const generation = state?.terminalGeneration;
  const configuredWindowMs = spec?.settleTimeoutMs === undefined ? 5_000 : spec.settleTimeoutMs;
  const confirmationWindowMs = Math.min(configuredWindowMs, timeoutMs);
  const started = performance.now();
  const deadline = started + Math.min(timeoutMs, Math.max(30_000, configuredWindowMs));
  const websocket = {
    transport: "websocket", sessionId, terminalHandleId: terminalId,
    terminalGeneration: generation, startedAt: now(), opened: false,
    inputBytes: 0, pongObserved: false, frames: [], timedOut: false,
    qualification: "A system pong acknowledges mux processing, not provider cancellation; cancellation also requires the same session to settle.",
  };
  let socket;
  let failure = "";
  let closing = false;
  let settledState;
  const trace = (direction, frame) => {
    if (websocket.frames.length >= 128) return;
    websocket.frames.push({ direction, at: now(), ch: frame.ch, type: frame.type,
      ...(frame.id ? { id: frame.id } : {}),
      ...(frame.error ? { error: redactOutput(frame.error).slice(0, 1024) } : {}) });
  };
  const waitFor = async (condition, label) => {
    while (!condition()) {
      if (failure) throw new Error(failure);
      if (performance.now() >= deadline) {
        websocket.timedOut = true;
        throw new Error("timed out waiting for " + label);
      }
      await sleep(10);
    }
    if (failure) throw new Error(failure);
  };
  try {
    if (!Number.isInteger(configuredWindowMs) || configuredWindowMs < 1 || configuredWindowMs > 60_000) {
      throw new Error("runtime interrupt.settleTimeoutMs must be an integer from 1 to 60000");
    }
    const input = typeof spec?.input === "string" ? Buffer.from(spec.input, "utf8") : Buffer.alloc(0);
    if (!input.length || input.length > 64) throw new Error("runtime interrupt.input must contain 1 to 64 bytes");
    if (!activeState.ok || state?.id !== sessionId || state.isTerminated !== false
      || typeof terminalId !== "string" || !terminalId
      || state.activity?.state !== "active") throw new Error("active session has no valid terminal target");
    const url = new URL(baseURL);
    if (!["127.0.0.1", "localhost", "[::1]"].includes(url.hostname)) throw new Error("terminal mux must use the audit loopback host");
    url.protocol = url.protocol === "https:" ? "wss:" : "ws:";
    url.pathname = "/mux"; url.search = ""; url.hash = "";
    websocket.url = url.href;
    socket = new WebSocket(url);
    socket.addEventListener("error", () => { if (!closing) failure = "terminal WebSocket transport error"; });
    socket.addEventListener("close", event => {
      websocket.closeCode = event.code;
      if (!closing) failure = "terminal WebSocket closed before cancellation confirmation";
    });
    socket.addEventListener("message", event => {
      if (closing) return;
      let frame;
      try { frame = JSON.parse(String(event.data)); }
      catch { failure = "terminal mux returned invalid JSON"; return; }
      trace("received", frame);
      if (frame.ch === "terminal" && frame.id === terminalId) {
        if (frame.type === "opened") websocket.opened = true;
        if (frame.type === "error") failure = "terminal mux error: " + redactOutput(frame.error || "unspecified").slice(0, 1024);
        if (frame.type === "exited") failure = "terminal exited before cancellation confirmation";
      }
      if (frame.ch === "system" && frame.type === "pong") websocket.pongObserved = true;
    });
    await waitFor(() => socket.readyState === WebSocket.OPEN, "WebSocket connection");
    const open = { ch: "terminal", type: "open", id: terminalId, role: "secondary" };
    socket.send(JSON.stringify(open)); trace("sent", open);
    await waitFor(() => websocket.opened, "matching terminal opened frame");
    const preInput = await sessionState(baseURL, sessionId, Math.max(1, deadline - performance.now()));
    websocket.preInputState = apiEvidence(preInput);
    const current = preInput.response?.session;
    if (!preInput.ok || current?.id !== sessionId || current.isTerminated !== false
      || current.terminalHandleId !== terminalId || current.terminalGeneration !== generation
      || current.activity?.state !== "active") throw new Error("session ceased to be active or its terminal handle/generation changed before input");
    if (failure) throw new Error(failure);
    const data = { ch: "terminal", type: "data", id: terminalId, data: input.toString("base64") };
    socket.send(JSON.stringify(data)); trace("sent", data);
    websocket.inputBytes = input.length;
    websocket.inputSha256 = hash(input);
    websocket.inputSentAt = now();
    const inputSent = performance.now();
    const ping = { ch: "system", type: "ping" };
    socket.send(JSON.stringify(ping)); trace("sent", ping);
    await waitFor(() => websocket.pongObserved, "mux system pong after input");
    const settledDeadline = Math.min(deadline, inputSent + confirmationWindowMs);
    let settled = false;
    while (performance.now() < settledDeadline) {
      if (failure) throw new Error(failure);
      settledState = await sessionState(baseURL, sessionId, Math.max(1, settledDeadline - performance.now()));
      const current = settledState.response?.session;
      if (!settledState.ok) throw new Error("session state HTTP request failed during cancellation confirmation");
      if (current?.id !== sessionId || current.isTerminated !== false
        || current.terminalHandleId !== terminalId || current.terminalGeneration !== generation) {
        throw new Error("session terminated or terminal handle/generation changed during cancellation confirmation");
      }
      const observedAfterInputMs = Math.round(performance.now() - inputSent);
      if (SETTLED_STATES.has(current.activity?.state)) {
        settled = true;
        websocket.settledAt = now();
        websocket.settleDurationMs = observedAfterInputMs;
        websocket.settledWithinDefaultWindow = observedAfterInputMs <= 5_000;
        break;
      }
      if (observedAfterInputMs >= 5_000 && !websocket.unsettledAfterDefaultWindow) {
        websocket.unsettledAfterDefaultWindow = { observedAfterInputMs, state: apiEvidence(settledState) };
      }
      await sleep(Math.min(100, Math.max(10, Math.floor(confirmationWindowMs / 10))));
    }
    if (failure) throw new Error(failure);
    if (!settled) {
      websocket.timedOut = true;
      throw new Error("the same active TUI session did not settle after mux input");
    }
    return { passed: true, observedActive: true,
      reason: "AO terminal mux sent configured input to the observed active session and the same terminal settled",
      activeState, settledState, confirmationWindowMs, websocket };
  } catch (error) {
    websocket.error = redactOutput(error.message);
    return { passed: false, observedActive: true, reason: websocket.error,
      activeState, settledState, confirmationWindowMs, websocket };
  } finally {
    closing = true;
    websocket.finishedAt = now();
    websocket.durationMs = Math.round(performance.now() - started);
    if (socket) {
      try {
        if (socket.readyState === WebSocket.OPEN && websocket.opened) {
          socket.send(JSON.stringify({ ch: "terminal", type: "close", id: terminalId }));
        }
        socket.close();
      } catch { /* The error/close outcome is already retained above. */ }
    }
  }
}

export async function waitForRestoredTerminalReady({ baseURL, sessionId, restoredState, spec, timeoutMs = 30_000 }) {
  return observeNativeTerminalReady({ baseURL, sessionId, restoredState, spec, timeoutMs });
}

// Resolve only this audit session's declared recovery file; never inspect content.
function draftPersistencePath(spec, { dataDir, sessionId, workspacePath, nativeSessionId }) {
  const template = spec?.path;
  const bindings = {
    "{sessionId}": sessionId,
    "{workspaceSha256:24}": typeof workspacePath === "string" && workspacePath ? hash(resolve(workspacePath)).slice(0, 24) : "",
    "{nativeSessionIdSha256:24}": typeof nativeSessionId === "string" && nativeSessionId.trim() ? hash(nativeSessionId.trim()).slice(0, 24) : "",
  };
  if (typeof dataDir !== "string" || !dataDir || !/^[a-zA-Z0-9_-]{1,256}$/.test(sessionId)
    || typeof template !== "string" || !template || template.length > 1024 || isAbsolute(template)
    || /[\\\x00-\x1f\x7f]/.test(template)
    || Object.entries(bindings).some(([key, value]) => !value || !template.includes(key))) {
    throw new Error("draft persistence requires a bounded relative path bound to the observed session, workspace and native identity");
  }
  let relativePath = template;
  for (const [key, value] of Object.entries(bindings)) relativePath = relativePath.replaceAll(key, value);
  const components = relativePath.split("/");
  if (/[{}]/.test(relativePath) || components.length > 32 || components.some(part => !part || part === "." || part === "..")) {
    throw new Error("draft persistence path contains traversal, empty components or unknown placeholders");
  }
  return { path: resolve(dataDir, relativePath), relativePath };
}

async function waitForDraftPersistence(witness, wanted, deadline) {
  let lastState = "unknown";
  while (performance.now() < deadline) {
    let timer;
    try {
      const remaining = Math.max(1, deadline - performance.now());
      const state = await Promise.race([
        (async () => {
          const root = parse(witness.path).root;
          const components = witness.path.slice(root.length).split(sep);
          let current = root;
          for (let index = -1; index < components.length; index++) {
            if (index >= 0) current = join(current, components[index]);
            const leaf = index === components.length - 1;
            let metadata;
            try { metadata = await lstat(current); }
            catch (error) {
              if (error.code === "ENOENT") return leaf ? "absent" : "missing-parent";
              throw new Error("draft persistence metadata check failed: " + (error.code || "unknown"));
            }
            if (metadata.isSymbolicLink() || (leaf ? !metadata.isFile() : !metadata.isDirectory())) {
              throw new Error("draft persistence path contains a symlink or nonregular file/directory");
            }
          }
          return "present";
        })(),
        new Promise((_, reject) => { timer = setTimeout(() => reject(new Error("draft persistence metadata check timed out")), remaining); }),
      ]);
      lastState = state;
      if (state === wanted && performance.now() < deadline) return { relativePath: witness.relativePath, state, observedAt: now() };
    } finally { clearTimeout(timer); }
    await sleep(Math.min(25, Math.max(1, deadline - performance.now())));
  }
  throw new Error("draft persistence did not become " + wanted + " before timeout (last state: " + lastState + ")");
}

// Only completeLifecycle calls this, for its new exclusive audit session and
// just-submitted cancellation turn. This is not a general draft-clearing tool.
async function cleanupCancelledAuditDraft({ baseURL, sessionId, dataDir, workspacePath, nativeSessionId, cancellation, startResponse, spec, readySpec, timeoutMs }) {
  try {
    if (!cancellation.passed || !cancellation.observedActive || !startResponse?.ok
      || startResponse.requestBody?.message !== CANCELLATION_PROMPT) {
      throw new Error("successful cancellation of the runner's own turn is required before draft cleanup");
    }
    if (spec?.input !== "\x03") throw new Error("postCancelCleanup.input must be exactly one Ctrl+C");
    if (typeof spec.draftPattern !== "string" || spec.draftPattern.length > 4096
      || !spec.draftPattern.includes("{prompt}") || !/^[imsu]*$/.test(spec.flags || "")) {
      throw new Error("postCancelCleanup requires a bounded current-composer draftPattern containing {prompt} and valid flags");
    }
    const draftSource = spec.draftPattern.replaceAll("{prompt}", CANCELLATION_PROMPT.replace(/[.*+?^$()|[\]{}\\]/g, "\\$&"));
    const draftExpression = new RegExp(draftSource, spec.flags || "");
    const draftPattern = new RegExp("(?:" + draftExpression.source + ")(?![\\s\\S])", draftExpression.flags);
    const active = cancellation.activeState?.response?.session;
    const settled = cancellation.settledState?.response?.session;
    if (typeof active?.lastUserMessageAt !== "string" || !active.lastUserMessageAt.trim()) {
      throw new Error("post-cancel cleanup requires an observed nonempty lastUserMessageAt");
    }
    if (!cancellation.activeState?.ok || !cancellation.settledState?.ok
      || active?.id !== sessionId || settled?.id !== sessionId || active.isTerminated !== false || settled.isTerminated !== false
      || active.terminalHandleId !== settled.terminalHandleId || active.terminalGeneration !== settled.terminalGeneration
      || !["idle", "waiting_input"].includes(settled.activity?.state)
      || (active.lastUserMessageAt ?? null) !== (settled.lastUserMessageAt ?? null)) {
      throw new Error("original audit session, terminal generation, settled state or latest user turn changed during cancellation");
    }
    return await observeNativeTerminalReady({
      baseURL, sessionId, restoredState: cancellation.settledState, spec: readySpec, timeoutMs,
      cleanup: { draftPattern, lastUserMessageAt: active.lastUserMessageAt ?? null,
        persistence: Object.hasOwn(spec, "persistence") ? draftPersistencePath(spec.persistence, { dataDir, sessionId, workspacePath, nativeSessionId }) : null,
        patternSha256: hash(JSON.stringify({ draftPattern: spec.draftPattern, flags: spec.flags || "" })) },
    });
  } catch (error) {
    return { passed: false, reason: redactOutput(error.message), evidence: { inputSent: false, inputBytes: 0 } };
  }
}

async function observeNativeTerminalReady({ baseURL, sessionId, restoredState, spec, timeoutMs = 30_000, cleanup = null }) {
  const phase = cleanup ? "post-cancel" : "restored";
  const target = restoredState?.response?.session;
  const terminalId = target?.terminalHandleId;
  const generation = target?.terminalGeneration;
  const started = performance.now();
  const deadline = started + Math.min(timeoutMs, 30_000);
  const evidence = {
    transport: "websocket", sessionId, terminalHandleId: terminalId, terminalGeneration: generation,
    startedAt: now(), opened: false, outputFrames: 0, outputBytes: 0, matchedPatterns: 0,
    inputSent: false, inputBytes: 0, timedOut: false,
    ...(cleanup ? { outputFramesAfterInput: 0 } : {}),
    ...(cleanup?.persistence ? { persistence: {} } : {}),
    ...(cleanup ? { auditPromptSha256: hash(CANCELLATION_PROMPT), draftPatternSha256: cleanup.patternSha256,
      lastUserMessageAtObserved: cleanup.lastUserMessageAt !== null } : {}),
    qualification: "Native terminal cue from the runtime contract; API idle/readiness alone is insufficient.",
  };
  let socket;
  let closing = false;
  let failure = "";
  let output = "";
  let decoder = new StringDecoder("utf8");
  let patterns = [];
  try {
    if (!Array.isArray(spec?.patterns) || !spec.patterns.length || spec.patterns.length > 8
      || spec.patterns.some(pattern => typeof pattern !== "string" || !pattern || pattern.length > 4096)) {
      throw new Error("restoredReady.patterns must contain 1 to 8 bounded regular expressions");
    }
    if (!/^[imsu]*$/.test(spec.flags || "")) throw new Error("restoredReady.flags must use only i, m, s or u");
    patterns = spec.patterns.map(pattern => {
      const parsed = new RegExp(pattern, spec.flags || "");
      return cleanup ? new RegExp("(?:" + parsed.source + ")(?![\\s\\S])", parsed.flags) : parsed;
    });
    if (cleanup && patterns.some(pattern => pattern.test(""))) {
      throw new Error("post-cancel empty-composer patterns must not match empty output");
    }
    evidence.patternsSha256 = hash(JSON.stringify({ patterns: spec.patterns, flags: spec.flags || "" }));
    if (typeof generation !== "string" || !generation.trim()) throw new Error(phase + " session has no valid terminal generation");
    if (!restoredState.ok || target?.id !== sessionId || target.isTerminated !== false
      || typeof terminalId !== "string" || !terminalId) throw new Error(phase + " session has no valid terminal target");
    const url = new URL(baseURL);
    if (!["127.0.0.1", "localhost", "[::1]"].includes(url.hostname)) throw new Error("terminal mux must use the audit loopback host");
    url.protocol = url.protocol === "https:" ? "wss:" : "ws:";
    url.pathname = "/mux"; url.search = ""; url.hash = "";
    evidence.url = url.href;
    socket = new WebSocket(url);
    socket.addEventListener("error", () => { if (!closing) failure = phase + " terminal WebSocket transport error"; });
    socket.addEventListener("close", () => { if (!closing) failure = phase + " terminal WebSocket closed before native readiness"; });
    socket.addEventListener("message", event => {
      if (closing) return;
      let frame;
      try { frame = JSON.parse(String(event.data)); }
      catch { failure = phase + " terminal mux returned invalid JSON"; return; }
      if (frame.ch !== "terminal" || frame.id !== terminalId) return;
      if (frame.type === "opened") { evidence.opened = true; evidence.openedAt = now(); }
      if (frame.type === "error" || frame.type === "exited") {
        failure = phase + " terminal " + frame.type + ": " + redactOutput(frame.error || "terminal exited").slice(0, 1024);
      }
      if (frame.type === "data" && typeof frame.data === "string") {
        const bytes = Buffer.from(frame.data, "base64");
        evidence.outputFrames++;
        if (cleanup && evidence.inputSent) evidence.outputFramesAfterInput++;
        evidence.outputBytes += bytes.length;
        output = (output + decoder.write(bytes)).slice(-262144);
      }
    });
    let openSent = false;
    while (performance.now() < deadline) {
      if (failure) throw new Error(failure);
      if (!openSent && socket.readyState === WebSocket.OPEN) {
        socket.send(JSON.stringify({ ch: "terminal", type: "open", id: terminalId, role: "secondary" }));
        openSent = true;
      }
      const visible = stripVTControlCharacters(output).replaceAll("\r\n", "\n").replaceAll("\r", "\n").slice(-65536);
      const emptyComposer = (!cleanup || (visible.trim().length > 0 && (!evidence.inputSent || evidence.outputFramesAfterInput > 0)))
        && patterns.every(pattern => pattern.test(visible));
      if (cleanup && evidence.opened && evidence.outputFrames && !evidence.inputSent
        && !emptyComposer && cleanup.draftPattern.exec(visible)?.[0].includes(CANCELLATION_PROMPT)) {
        if (cleanup.persistence) evidence.persistence.beforeInput = await waitForDraftPersistence(cleanup.persistence, "present", deadline);
        const preInput = await sessionState(baseURL, sessionId, Math.max(1, deadline - performance.now()));
        evidence.preInputState = apiEvidence(preInput);
        const current = preInput.response?.session;
        if (!preInput.ok || current?.id !== sessionId || current.isTerminated !== false
          || current.terminalHandleId !== terminalId || current.terminalGeneration !== generation
          || !["idle", "waiting_input"].includes(current.activity?.state)
          || (current.lastUserMessageAt ?? null) !== cleanup.lastUserMessageAt) {
          throw new Error("post-cancel session, terminal generation, settled state or latest user turn changed before cleanup");
        }
        if (failure) throw new Error(failure);
        if (performance.now() >= deadline) throw new Error("post-cancel cleanup deadline elapsed before input");
        // Recheck the composer after the awaited metadata and API reads, before any input.
        const latestVisible = stripVTControlCharacters(output).replaceAll("\r\n", "\n").replaceAll("\r", "\n").slice(-65536);
        if (patterns.every(pattern => pattern.test(latestVisible)) || !cleanup.draftPattern.exec(latestVisible)?.[0].includes(CANCELLATION_PROMPT)) continue;
        evidence.draftCueSha256 = hash(latestVisible);
        evidence.draftMatchedAt = now();
        // Never allow earlier empty frames to satisfy the post-input check.
        output = "";
        decoder = new StringDecoder("utf8");
        socket.send(JSON.stringify({ ch: "terminal", type: "data", id: terminalId, data: Buffer.from("\x03").toString("base64") }));
        evidence.inputSent = true;
        evidence.inputBytes = 1;
        evidence.inputSha256 = hash(Buffer.from("\x03"));
        evidence.inputSentAt = now();
        continue;
      }
      if (evidence.opened && evidence.outputFrames && emptyComposer) {
        if (cleanup?.persistence) evidence.persistence.afterEmpty = await waitForDraftPersistence(cleanup.persistence, "absent", deadline);
        const currentResponse = await sessionState(baseURL, sessionId, Math.max(1, deadline - performance.now()));
        evidence.confirmation = apiEvidence(currentResponse);
        const current = currentResponse.response?.session;
        if (!currentResponse.ok || current?.id !== sessionId || current.isTerminated !== false
          || current.terminalHandleId !== terminalId || current.terminalGeneration !== generation
          || !["idle", "waiting_input"].includes(current.activity?.state)) {
          throw new Error(phase + " session changed terminal or was not settled when the native cue appeared");
        }
        if (cleanup && (current.lastUserMessageAt ?? null) !== cleanup.lastUserMessageAt) {
          throw new Error("latest user turn changed during post-cancel cleanup");
        }
        if (failure) throw new Error(failure);
        if (cleanup && performance.now() >= deadline) throw new Error("post-cancel cleanup deadline elapsed before confirmation");
        if (cleanup && !patterns.every(pattern => pattern.test(stripVTControlCharacters(output).replaceAll("\r\n", "\n").replaceAll("\r", "\n").slice(-65536)))) continue;
        evidence.matchedPatterns = patterns.length;
        evidence.matchedAt = now();
        evidence.visibleOutputSha256 = hash(visible);
        return { passed: true, reason: cleanup
          ? evidence.inputSent ? "one Ctrl+C cleared the audit cancellation draft and the same terminal displayed the empty-composer cue"
            : "the audit composer was already empty; no cleanup input was sent"
          : "the restored native terminal displayed the configured ready cue on the same terminal generation", evidence };
      }
      await sleep(10);
    }
    evidence.timedOut = true;
    throw new Error(cleanup ? "post-cancel owned-draft or empty-composer cue was not observed before timeout"
      : "the restored native terminal ready cue was not observed before timeout");
  } catch (error) {
    evidence.error = redactOutput(error.message);
    return { passed: false, reason: evidence.error, evidence };
  } finally {
    closing = true;
    evidence.finishedAt = now();
    evidence.durationMs = Math.round(performance.now() - started);
    if (socket) {
      try {
        if (socket.readyState === WebSocket.OPEN && evidence.opened) {
          socket.send(JSON.stringify({ ch: "terminal", type: "close", id: terminalId }));
        }
        socket.close();
      } catch { /* The original failure is retained. */ }
    }
  }
}

async function workspaceFile(baseURL, sessionId, path, timeoutMs) {
  const query = new URLSearchParams({ path });
  return request({
    baseURL,
    path: `sessions/${encodeURIComponent(sessionId)}/workspace/file?${query}`,
    timeoutMs,
  });
}

function parseProof(response) {
  if (!response.ok || typeof response.response?.content !== "string") return null;
  try {
    const parsed = JSON.parse(response.response.content);
    return parsed && typeof parsed === "object" && !Array.isArray(parsed) ? parsed : null;
  } catch {
    return null;
  }
}

async function normalizedPath(path) {
  try {
    return await realpath(path);
  } catch {
    return resolve(path);
  }
}

export function runCommand(command, args, options = {}) {
  return new Promise((done) => {
    const startedAt = now();
    const started = performance.now();
    const outputLimit = options.outputLimit ?? 65_536;
    let stdout = "";
    let stderr = "";
    let child;
    let timedOut = false;
    let settled = false;
    let stdoutBytes = 0;
    let stderrBytes = 0;
    const stdoutHasher = createHash("sha256");
    const stderrHasher = createHash("sha256");
    let timer;
    let forceKillTimer;
    let deferredFinish;
    const append = (current, chunk) => `${current}${chunk.toString()}`.slice(-outputLimit);
    const finish = (code, error = "") => {
      if (settled) return;
      // A timed-out leader may exit while a same-group descendant ignores
      // SIGTERM. Keep the bounded group escalation alive before returning.
      if (timedOut && forceKillTimer) {
        deferredFinish = [code, error];
        return;
      }
      settled = true;
      clearTimeout(timer);
      clearTimeout(forceKillTimer);
      if (error) stderr = append(stderr, `\n${error}`);
      done({
        ok: !timedOut && !error && code === 0,
        code,
        timedOut,
        stdout: stdout.trim(),
        stderr: stderr.trim(),
        stdoutBytes,
        stderrBytes,
        stdoutSha256: stdoutHasher.digest("hex"),
        stderrSha256: stderrHasher.digest("hex"),
        stdoutTruncated: stdoutBytes > outputLimit,
        stderrTruncated: stderrBytes > outputLimit,
        startedAt,
        finishedAt: now(),
        durationMs: Math.round(performance.now() - started),
      });
    };
    const kill = (signal) => {
      try {
        if (process.platform !== "win32" && child?.pid) process.kill(-child.pid, signal);
        else child?.kill(signal);
      } catch {
        child?.kill(signal);
      }
    };
    try {
      child = spawn(command, args, {
        cwd: options.cwd,
        env: options.env ?? process.env,
        detached: process.platform !== "win32",
        stdio: ["ignore", "pipe", "pipe"],
      });
    } catch (error) {
      finish(null, error instanceof Error ? error.message : String(error));
      return;
    }
    timer = setTimeout(() => {
      timedOut = true;
      kill("SIGTERM");
      forceKillTimer = setTimeout(() => {
        kill("SIGKILL");
        forceKillTimer = undefined;
        if (deferredFinish) finish(...deferredFinish);
      }, options.killGraceMs ?? 500);
    }, options.timeoutMs ?? 30_000);
    child.stdout.on("data", (chunk) => {
      stdoutBytes += chunk.length;
      stdoutHasher.update(chunk);
      stdout = append(stdout, chunk);
    });
    child.stderr.on("data", (chunk) => {
      stderrBytes += chunk.length;
      stderrHasher.update(chunk);
      stderr = append(stderr, chunk);
    });
    child.once("error", (error) => {
      finish(null, error.message);
    });
    child.once("close", (code) => {
      finish(code);
    });
  });
}

function sanitizedArgs(args) {
  let redactNext = false;
  return (args || []).map((argument) => {
    const value = String(argument);
    if (redactNext) {
      redactNext = false;
      return "[REDACTED]";
    }
    if (/^--?(?:api-?key|token|password|secret|credential)$/i.test(value)) {
      redactNext = true;
      return value;
    }
    if (/^(?:--?(?:api-?key|token|password|secret|credential))=/i.test(value)) {
      return `${value.split("=", 1)[0]}=[REDACTED]`;
    }
    return redactOutput(value);
  });
}

function commandEvidence(outcome, includeOutput = true, details = {}) {
  const evidence = {
    exitCode: outcome.code,
    timedOut: outcome.timedOut === true,
    startedAt: outcome.startedAt,
    finishedAt: outcome.finishedAt,
    durationMs: outcome.durationMs,
    stdoutBytes: outcome.stdoutBytes ?? Buffer.byteLength(outcome.stdout),
    stderrBytes: outcome.stderrBytes ?? Buffer.byteLength(outcome.stderr),
    stdoutSha256: outcome.stdoutSha256 ?? hash(outcome.stdout),
    stderrSha256: outcome.stderrSha256 ?? hash(outcome.stderr),
    stdoutTruncated: outcome.stdoutTruncated === true,
    stderrTruncated: outcome.stderrTruncated === true,
    authenticationRequired: !outcome.ok && /\b(?:session expired|not (?:logged|signed) in|authentication required|please (?:log|sign) in|sign in to continue|invalid (?:api key|credentials)|expired (?:token|credentials))\b/i.test(`${outcome.stderr}\n${outcome.stdout}`),
    ...details,
  };
  if (includeOutput) {
    const stdout = redactOutput(outcome.stdout);
    const stderr = redactOutput(outcome.stderr);
    evidence.stdout = stdout.slice(-8_192);
    evidence.stderr = stderr.slice(-8_192);
    evidence.stdoutTruncated ||= outcome.stdout.length > 8_192 || stdout.length > 8_192;
    evidence.stderrTruncated ||= outcome.stderr.length > 8_192 || stderr.length > 8_192;
  }
  return evidence;
}

async function resolveExecutable(binary, environment = process.env) {
  if (typeof binary !== "string" || !binary.trim()) return "";
  const candidates = [];
  if (isAbsolute(binary) || binary.includes("/") || (process.platform === "win32" && binary.includes("\\"))) {
    candidates.push(resolve(expandHome(binary)));
  } else {
    const extensions = process.platform === "win32"
      ? (environment.PATHEXT || ".EXE;.CMD;.BAT;.COM").split(";")
      : [""];
    for (const directory of (environment.PATH || "").split(delimiter).filter(Boolean)) {
      for (const extension of extensions) candidates.push(join(directory, `${binary}${extension}`));
    }
  }
  for (const candidate of candidates) {
    try {
      await access(candidate, fsConstants.X_OK);
      return await realpath(candidate);
    } catch {
      // Continue searching PATH.
    }
  }
  return "";
}

function modelIdsFromPayload(payload) {
  const catalog = (() => {
    if (Array.isArray(payload)) return payload;
    let current = payload;
    for (let depth = 0; depth < 3 && current && typeof current === "object"; depth += 1) {
      const key = ["models", "data", "items", "options"].find((candidate) => candidate in current);
      if (!key) return [];
      current = current[key];
      if (Array.isArray(current)) return current;
    }
    return [];
  })();
  const values = catalog.map((entry) => {
    if (typeof entry === "string") return entry.trim();
    if (!entry || typeof entry !== "object") return "";
    for (const key of ["id", "model", "value"]) {
      if (typeof entry[key] === "string") return entry[key].trim();
    }
    return "";
  });
  return [...new Set(values.filter(Boolean))];
}

function modelsFromCommand(outcome, format) {
  if (!outcome.ok) return [];
  const output = outcome.stdout.trim() || outcome.stderr.trim();
  if (!output) return [];
  if (format === "json") {
    try {
      return modelIdsFromPayload(JSON.parse(output));
    } catch {
      return [];
    }
  }
  return [...new Set(output.split(/\r?\n/).map((line) => line.trim()).filter(Boolean))];
}

function localNotRun(agent, reason) {
  const gates = LOCAL_GATES.map((name) => gate(name, "NOT_RUN", reason));
  return resultFrom(agent, gates, { models: [] });
}

function commandArgs(spec, prompt = "") {
  const args = Array.isArray(spec?.args) ? spec.args.map(String) : [];
  let replaced = false;
  const expanded = args.map((argument) => {
    if (!argument.includes("{prompt}")) return argument;
    replaced = true;
    return argument.replaceAll("{prompt}", prompt);
  });
  if (prompt && !replaced) expanded.push(prompt);
  return expanded;
}

export async function auditLocalAgent({ agent, contract, root = tmpdir(), live = true, timeoutMs = 30_000, onIssue }) {
  if (!contract) return localNotRun(agent, "no local CLI contract was supplied for this agent");
  const gates = [];
  const record = gateRecorder(gates, onIssue, () => ({ agent }));
  const executable = await resolveExecutable(contract.binary);
  if (!executable) {
    await record(gate("local_binary", "BLOCKED", `local executable ${contract.binary || "(missing)"} was not found`));
    for (const name of LOCAL_GATES.slice(1)) await record(gate(name, "NOT_RUN", "local executable prerequisite failed"));
    return resultFrom(agent, gates, { models: [] });
  }
  await record(gate("local_binary", "PASS", "local executable resolved", { executable }));

  if (!Array.isArray(contract.versionArgs)) {
    await record(gate("local_version", "NOT_RUN", "local version command is not defined"));
  } else {
    const outcome = await runCommand(executable, contract.versionArgs.map(String), { timeoutMs });
    const passed = outcome.ok && Boolean(outcome.stdout.trim() || outcome.stderr.trim());
    await record(gate("local_version", passed ? "PASS" : "FAIL", passed ? "local version command succeeded" : "local version command failed or returned no version", commandEvidence(outcome, true, { arguments: sanitizedArgs(contract.versionArgs) })));
  }

  if (!contract.integration || !Array.isArray(contract.integration.args)) {
    await record(gate("local_integration", "NOT_RUN", "local integration command is not defined"));
  } else {
    const outcome = await runCommand(executable, commandArgs(contract.integration), { timeoutMs });
    const combined = `${outcome.stdout}\n${outcome.stderr}`;
    const passed = outcome.ok && (!contract.integration.expect || combined.includes(String(contract.integration.expect)));
    await record(gate("local_integration", passed ? "PASS" : "FAIL", passed ? "local integration probe succeeded" : "local integration probe failed or its expected evidence was absent", commandEvidence(outcome, !passed, {
      arguments: sanitizedArgs(contract.integration.args),
      expectedSha256: contract.integration.expect ? hash(String(contract.integration.expect)) : undefined,
      expectedObserved: contract.integration.expect ? combined.includes(String(contract.integration.expect)) : true,
    })));
  }

  let localWorkspace = "";
  if (gates.some(item => authenticationAction(agent, item))) {
    await record(gate("local_session_spawn", "NOT_RUN", "user must resolve the local login error before a provider turn"));
  } else if (!live) {
    await record(gate("local_session_spawn", "NOT_RUN", "direct local session disabled by --no-live"));
  } else if (!contract.session || !Array.isArray(contract.session.args)) {
    await record(gate("local_session_spawn", "NOT_RUN", "local session command is not defined"));
  } else {
    const safeAgent = agent.replace(/[^a-z0-9-]+/gi, "-").toLowerCase();
    localWorkspace = await mkdtemp(join(resolve(root), `local-${safeAgent}-`));
    const proofFile = String(contract.session.proofFile || "LOCAL_AUDIT_PROOF.json");
    const proofPath = resolve(localWorkspace, proofFile);
    const proofRelative = relative(localWorkspace, proofPath);
    const safeProofPath = !isAbsolute(proofFile)
      && proofRelative !== ".."
      && !proofRelative.startsWith(`..${sep}`);
    if (!safeProofPath) {
      await record(gate("local_session_spawn", "FAIL", "local session proofFile must be a safe relative path inside the disposable workspace"));
    } else {
      const sessionToken = token("LOCAL_SESSION");
      const prompt = [
        `Local CLI audit token: ${sessionToken}`,
        `Create ${proofFile} in the current working directory as raw JSON with fields token and cwd, then exit.`,
      ].join("\n");
      const outcome = await runCommand(executable, commandArgs(contract.session, prompt), { cwd: localWorkspace, timeoutMs });
      let proof = null;
      let proofContained = false;
      try {
        const actualProofPath = await realpath(proofPath);
        const actualRelative = relative(await realpath(localWorkspace), actualProofPath);
        proofContained = actualRelative !== ".." && !actualRelative.startsWith(`..${sep}`);
        if (proofContained) proof = JSON.parse(await readFile(actualProofPath, "utf8"));
      } catch {
        proof = null;
      }
      const cwdMatches = typeof proof?.cwd === "string"
        && (await normalizedPath(proof.cwd)) === (await normalizedPath(localWorkspace));
      const passed = outcome.ok && proof?.token === sessionToken && cwdMatches;
      await record(gate("local_session_spawn", passed ? "PASS" : "FAIL", passed ? "direct local CLI session produced the requested proof" : "direct local CLI session did not produce valid token and workspace evidence", {
        ...commandEvidence(outcome, !passed, {
          arguments: sanitizedArgs(contract.session.args),
          promptSha256: hash(prompt),
        }),
        expectedTokenSha256: hash(sessionToken),
        proofObserved: Boolean(proof),
        proofContained,
        cwdMatches,
      }));
    }
  }

  let models = [];
  if (!contract.models || !Array.isArray(contract.models.args)) {
    await record(gate("local_models_list", "NOT_RUN", "local model-list command is not defined"));
  } else {
    const outcome = await runCommand(executable, commandArgs(contract.models), { timeoutMs });
    models = modelsFromCommand(outcome, contract.models.format);
    const passed = outcome.ok && models.length > 0;
    await record(gate("local_models_list", passed ? "PASS" : "FAIL", passed ? `local CLI listed ${models.length} model(s)` : "local model-list command failed or returned no models", {
      ...commandEvidence(outcome, !passed, { arguments: sanitizedArgs(contract.models.args) }),
      models,
    }));
  }
  return resultFrom(agent, gates, { executable, localWorkspace, models, contractSha256: hash(JSON.stringify(contract)) });
}

export async function loadLocalContracts(path) {
  if (!path) return {};
  const parsed = JSON.parse(await readFile(resolve(expandHome(path)), "utf8"));
  if (parsed?.schemaVersion !== 1 || !parsed.agents || typeof parsed.agents !== "object" || Array.isArray(parsed.agents)) {
    throw new Error("local contract must contain schemaVersion 1 and an agents object");
  }
  return parsed.agents;
}

export function mergeAuditResults(local, ao) {
  const localModels = Array.isArray(local?.models) ? local.models : [];
  const aoModels = Array.isArray(ao?.aoModels) ? ao.aoModels : [];
  const aoSet = new Set(aoModels);
  const shared = localModels.filter((model) => aoSet.has(model));
  let comparison;
  if (localModels.length === 0 || aoModels.length === 0) {
    comparison = gate("model_catalog_consistency", "NOT_RUN", "both local and AO model catalogs are required for comparison", { localModels, aoModels, shared });
  } else if (shared.length === 0) {
    comparison = gate("model_catalog_consistency", "FAIL", "local and AO model catalogs have no shared model IDs", { localModels, aoModels, shared });
  } else {
    comparison = gate("model_catalog_consistency", "PASS", `${shared.length} model ID(s) are shared by local CLI and AO`, { localModels, aoModels, shared });
  }
  const gates = [...(local?.gates || []), ...(ao?.gates || []), comparison];
  return {
    ...ao,
    ...((ao?.userAction || local?.userAction) ? { userAction: ao?.userAction || local?.userAction } : {}),
    agent: ao?.agent || local?.agent,
    status: overallStatus(gates),
    failedGate: firstFailedGate(gates),
    gates,
    local: {
      executable: local?.executable,
      workspacePath: local?.localWorkspace,
      models: localModels,
      contractSha256: local?.contractSha256,
    },
    aoModels,
    modelCatalogComparison: comparison.evidence,
  };
}

async function freePort() {
  const server = createServer();
  await new Promise((resolveReady, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolveReady);
  });
  const address = server.address();
  const port = typeof address === "object" && address ? address.port : 0;
  await new Promise((resolveClose) => server.close(resolveClose));
  if (!port) throw new Error("could not allocate a loopback port for AO");
  return port;
}

async function defaultReadinessCheck(baseURL) {
  const checkedAt = now();
  try {
    const response = await fetch(`${baseURL}/readyz`, { signal: AbortSignal.timeout(2_000) });
    return { ok: response.ok, status: response.status, checkedAt };
  } catch (error) {
    return { ok: false, status: 0, checkedAt, error: error instanceof Error ? error.message : String(error) };
  }
}

export async function startManagedDaemon({
  root,
  repoRoot = process.cwd(),
  aoPath = "",
  aoCommand,
  port,
  readinessCheck = defaultReadinessCheck,
  timeoutMs = 30_000,
  onStartupFailure,
}) {
  const dataDir = join(root, "data");
  const runFile = join(root, "running.json");
  await mkdir(dataDir, { recursive: true });

  let command = aoCommand;
  let build = null;
  if (!command) {
    if (aoPath) {
      command = [resolve(expandHome(aoPath))];
    } else {
      const binaryDir = join(root, "bin");
      const binary = join(binaryDir, process.platform === "win32" ? "ao.exe" : "ao");
      await mkdir(binaryDir, { recursive: true });
      build = await runCommand("go", ["build", "-o", binary, "./cmd/ao"], {
        cwd: join(resolve(repoRoot), "backend"),
        timeoutMs: Math.max(timeoutMs, 120_000),
      });
      if (!build.ok) {
        const error = new Error(`could not build AO daemon: ${build.stderr || build.stdout}`);
        error.evidence = { command: ["go", "build", "-o", binary, "./cmd/ao"], build: commandEvidence(build) };
        throw error;
      }
      command = [binary];
    }
  }
  if (!Array.isArray(command) || command.length === 0) throw new Error("AO daemon command is empty");

  const selectedPort = port ?? await freePort();
  const baseURL = `http://127.0.0.1:${selectedPort}`;
  const environment = {
    ...process.env,
    AO_RUN_FILE: runFile,
    AO_DATA_DIR: dataDir,
    AO_PORT: String(selectedPort),
    AO_TELEMETRY_EVENTS: "off",
    AO_TELEMETRY_METRICS: "off",
    AO_TELEMETRY_REMOTE: "off",
  };
  const child = spawn(command[0], [...command.slice(1), "daemon"], {
    cwd: resolve(repoRoot),
    env: environment,
    stdio: ["ignore", "pipe", "pipe"],
  });
  let logs = "";
  let readiness = { ok: false, status: 0, checkedAt: "" };
  const appendLog = (chunk) => {
    logs = `${logs}${chunk.toString()}`.slice(-65_536);
  };
  child.stdout.on("data", appendLog);
  child.stderr.on("data", appendLog);
  let exit = null;
  const exited = new Promise((resolveExit) => {
    child.once("error", (error) => {
      exit = { code: null, error: error.message };
      resolveExit(exit);
    });
    child.once("close", (code, signal) => {
      exit = { code, signal };
      resolveExit(exit);
    });
  });

  const ready = await waitUntil(async () => {
    if (exit) return { done: true, failed: true };
    try {
      const info = JSON.parse(await readFile(runFile, "utf8"));
      if (info.pid !== child.pid || info.port !== selectedPort) return { done: false };
      const observed = await readinessCheck(baseURL);
      readiness = typeof observed === "boolean"
        ? { ok: observed, status: observed ? 200 : 0, checkedAt: now() }
        : observed;
      return { done: readiness.ok === true, info };
    } catch {
      return { done: false };
    }
  }, timeoutMs, 100);

  const stop = async () => {
    if (exit) return exit;
    child.kill("SIGTERM");
    const graceful = await new Promise((resolveStop) => {
      const timer = setTimeout(() => resolveStop(null), 5_000);
      timer.unref?.();
      exited.then((value) => {
        clearTimeout(timer);
        resolveStop(value);
      });
    });
    if (graceful) return graceful;
    child.kill("SIGKILL");
    return exited;
  };
  if (!ready.done || ready.failed) {
    const error = new Error(`AO daemon did not become ready${exit ? ` (exit ${exit.code ?? exit.error})` : ""}: ${logs.trim() || "no daemon output"}`);
    error.evidence = { command, baseURL, pid: child.pid, readiness, exit, daemonLogs: redactOutput(logs), build: build ? commandEvidence(build) : null };
    try { await onStartupFailure?.(error); }
    finally { error.evidence.cleanupExit = await stop(); }
    throw error;
  }
  return {
    baseURL,
    dataDir,
    runFile,
    pid: child.pid,
    port: selectedPort,
    command,
    build,
    readiness,
    logs: () => logs,
    stop,
  };
}

async function nativeSessionID(dataDir, sessionId, timeoutMs) {
  if (!dataDir) return { ok: false, reason: "--data-dir is required for native-ID evidence", value: "" };
  const db = join(resolve(expandHome(dataDir)), "ao.db");
  const escaped = sessionId.replaceAll("'", "''");
  const query = `SELECT agent_session_id FROM sessions WHERE id='${escaped}' LIMIT 1;`;
  const output = await runCommand("sqlite3", ["-readonly", db, query], { timeoutMs });
  if (!output.ok) {
    return { ok: false, reason: output.stderr || `sqlite3 exited ${output.code}`, value: "", db };
  }
  const value = output.stdout.trim();
  if (!value) return { ok: false, reason: "AO has not persisted a provider-native session ID", value: "", db };
  return { ok: true, reason: "provider-native ID is present", value, db, sha256: hash(value) };
}

function makeScenario(agent, supplied = {}) {
  return {
    proofFile: supplied.proofFile ?? "AO_AUDIT_PROOF.json",
    initialToken: supplied.initialToken ?? token("AO_INITIAL"),
    historyToken: supplied.historyToken ?? token("AO_HISTORY"),
    hiddenInitialToken: supplied.hiddenInitialToken ?? token("AO_HIDDEN_INITIAL"),
    hiddenRestoreToken: supplied.hiddenRestoreToken ?? token("AO_HIDDEN_RESTORE"),
    agentsToken: supplied.agentsToken ?? token("AO_AGENTS_MD"),
    secondToken: supplied.secondToken ?? token("AO_SECOND"),
    agent,
  };
}

function initialPrompt(scenario) {
  return [
    `AO audit request token: ${scenario.initialToken}`,
    `History-only token (do not write this token to any file yet): ${scenario.historyToken}`,
    `Create ${scenario.proofFile} in your current working directory as raw JSON, without markdown fences.`,
    "Read the project AGENTS.md and your hidden AO standing instructions.",
    `If ${scenario.proofFile} already exists, preserve it and increment initialPromptExecutions; otherwise set initialPromptExecutions to 1.`,
    "The JSON must contain: cwd (actual current working directory), initialPromptToken, initialPromptExecutions, hiddenInstructionToken, and projectAgentsToken.",
    "Set those fields from the tokens you actually received. Do not include the history-only token. Then stop and wait.",
  ].join("\n");
}

function addLiveNotRun(gates, reason) {
  markNotRun(gates, reason);
  return gates;
}

export async function auditAgent({
  baseURL,
  agent,
  projectId,
  live = true,
  diagnosticAfterNativeProof = false,
  localResult = null,
  interruptSpec = null,
  restoredReadySpec = null,
  dataDir = "",
  modelsPath = "agents/{agent}/models",
  timeoutMs = 180_000,
  scenario: suppliedScenario = {},
  onIssue,
  onSession,
}) {
  const scenario = makeScenario(agent, suppliedScenario);
  const probe = await request({
    baseURL,
    method: "POST",
    path: `agents/${encodeURIComponent(agent)}/probe`,
    body: {},
    timeoutMs,
  });
  const readiness = await request({
    baseURL,
    method: "POST",
    path: "agents/readiness/ensure",
    body: { agentIds: [agent], purpose: "launch" },
    timeoutMs,
  });
  const gates = [];
  const context = { agent, baseURL, sessionId: null };
  const record = gateRecorder(gates, onIssue, () => context);
  await record(...preflightOutcome(probe, readiness, agent));
  const nativeProofPassed = localResult?.agent === agent && !localResult.userAction
    && ["local_binary", "local_version", "local_integration", "local_session_spawn"]
      .every(name => localResult.gates?.find(item => item.name === name)?.status === "PASS");
  const installedAndFresh = ["registered", "installed", "fresh_probe"]
    .every(name => gates.find(item => item.name === name)?.status === "PASS");
  const snapshot = readiness.response?.agents?.find(item => item?.id === agent);
  const rawAuthStates = [probe.response?.agent?.authStatus, snapshot?.authentication?.state];
  const uncertainAuth = rawAuthStates.every(state => ["configured", "unknown", "authorized", "not_applicable"].includes(state))
    && rawAuthStates.some(state => state === "configured" || state === "unknown");
  const strictPreflightPassed = gates.every(item => item.status === "PASS");
  const authOnlyBlocked = gates.some(item => item.name === "authentication" && item.status === "BLOCKED")
    && gates.every(item => item.status === "PASS" || (item.name === "authentication" && item.status === "BLOCKED"));
  const diagnosticPrerequisites = diagnosticAfterNativeProof && nativeProofPassed
    && probe.ok && readiness.ok && installedAndFresh;
  const continuePastAuth = diagnosticPrerequisites && authOnlyBlocked && uncertainAuth;
  const diagnostic = {
    enabled: diagnosticAfterNativeProof, nativeProofPassed,
    continuedPastAuthentication: continuePastAuth,
    continuedPastEmptyModels: false, lifecycleEligible: false,
    qualification: "Diagnostic only; original strict gate results are preserved and a full audit PASS is not claimed.",
  };
  const finish = (items, extra = {}) => resultFrom(agent, items, {
    ...extra, ...(diagnosticAfterNativeProof ? { diagnostic } : {}),
  });
  if (!strictPreflightPassed && !continuePastAuth) {
    await record(gate("ao_models_api", "NOT_RUN", "AO readiness prerequisite did not pass"));
    return finish(addLiveNotRun(gates, "preflight prerequisite did not pass"), { projectId });
  }

  const modelPath = String(modelsPath || "agents/{agent}/models")
    .replace(/^\/?api\/v1\//, "")
    .replaceAll("{agent}", encodeURIComponent(agent));
  const modelSegments = modelPath.split("/");
  const modelPathIsSpecific = !/[?#]/.test(modelPath)
    && modelSegments.length >= 3
    && modelSegments[0] === "agents"
    && modelSegments[1] === encodeURIComponent(agent)
    && modelSegments.slice(2).includes("models")
    && modelSegments.every((segment) => segment && segment !== "." && segment !== "..");
  const modelResponse = modelPathIsSpecific
    ? await request({ baseURL, path: modelPath, timeoutMs })
    : {
      ok: false,
      status: 0,
      requestId: "",
      method: "GET",
      path: `/api/v1/${modelPath}`,
      response: null,
      error: "configured AO model path is not model-specific",
      startedAt: now(),
      finishedAt: now(),
      durationMs: 0,
    };
  const aoModels = modelResponse.ok ? modelIdsFromPayload(modelResponse.response) : [];
  const modelsOK = modelResponse.ok && aoModels.length > 0;
  await record(gate(
    "ao_models_api",
    modelsOK ? "PASS" : "FAIL",
    modelsOK ? `AO model API listed ${aoModels.length} model(s)` : "AO model API failed or returned no models",
    { ...apiEvidence(modelResponse), models: aoModels },
  ));
  const continuePastEmptyModels = diagnosticPrerequisites
    && (strictPreflightPassed || continuePastAuth) && modelResponse.ok && aoModels.length === 0;
  diagnostic.continuedPastEmptyModels = continuePastEmptyModels;
  diagnostic.lifecycleEligible = diagnosticPrerequisites
    && (strictPreflightPassed || continuePastAuth) && (modelsOK || continuePastEmptyModels);
  if (!modelsOK && !continuePastEmptyModels) {
    return finish(addLiveNotRun(gates, "AO model API prerequisite did not pass"), { projectId, aoModels });
  }
  if (!live) {
    return finish(addLiveNotRun(gates, "live lifecycle disabled by --no-live"), { projectId, aoModels });
  }

  const spawnResponse = await request({
    baseURL,
    method: "POST",
    path: "sessions",
    body: {
      projectId,
      kind: "worker",
      harness: agent,
      mode: "tui",
      approvalMode: "bypass-permissions",
      displayName: `audit-${agent}`.slice(0, 100),
      prompt: initialPrompt(scenario),
    },
    timeoutMs,
  });
  const session = spawnResponse.response?.session;
  const sessionId = session?.id;
  if (typeof sessionId === "string" && sessionId) {
    context.sessionId = sessionId;
    await onSession?.({ agent, sessionId });
  }
  const spawnOK = spawnResponse.ok && typeof sessionId === "string" && sessionId && session.mode === "tui" && session.harness === agent;
  await record(gate(
    "tui_spawn",
    spawnOK ? "PASS" : "FAIL",
    spawnOK ? "AO created the requested TUI session" : "AO did not create the requested TUI session",
    apiEvidence(spawnResponse),
  ));
  if (!spawnOK) {
    return finish(addLiveNotRun(gates, "TUI spawn failed"), { projectId, aoModels });
  }

  const workspaceResponse = await request({
    baseURL,
    path: `desktop/sessions/${encodeURIComponent(sessionId)}/workspace`,
    timeoutMs,
  });
  const workspacePath = workspaceResponse.response?.workspacePath;

  let observedActive = session.activity?.state === "active";
  const firstProof = await waitUntil(async () => {
    const [stateResponse, fileResponse] = await Promise.all([
      sessionState(baseURL, sessionId, Math.min(timeoutMs, 30_000)),
      workspaceFile(baseURL, sessionId, scenario.proofFile, Math.min(timeoutMs, 30_000)),
    ]);
    const current = stateResponse.response?.session;
    if (current?.activity?.state === "active") observedActive = true;
    const proof = parseProof(fileResponse);
    const settled = current && SETTLED_STATES.has(current.activity?.state);
    const ready = current?.statusReadiness === "ready";
    return { done: Boolean(proof && settled && ready), proof, stateResponse, fileResponse, current };
  }, timeoutMs);
  const proof = firstProof.proof;
  const proofEvidence = firstProof.fileResponse ? apiEvidence(firstProof.fileResponse) : undefined;

  await record(gate(
    "proof_file_creation",
    proof ? "PASS" : "FAIL",
    proof ? `${scenario.proofFile} contains valid JSON` : `valid ${scenario.proofFile} was not observed before timeout`,
    proofEvidence,
  ));

  let cwdMatches = false;
  if (proof && typeof proof.cwd === "string" && typeof workspacePath === "string") {
    cwdMatches = (await normalizedPath(proof.cwd)) === (await normalizedPath(workspacePath));
  }
  await record(gate(
    "correct_working_directory",
    cwdMatches ? "PASS" : "FAIL",
    cwdMatches ? "provider reported AO's session workspace" : "provider cwd does not match AO's session workspace",
    { workspace: apiEvidence(workspaceResponse), reportedCwd: proof?.cwd },
  ));
  await record(gate(
    "initial_prompt_exactly_once",
    proof?.initialPromptToken === scenario.initialToken && proof?.initialPromptExecutions === 1 ? "PASS" : "FAIL",
    proof?.initialPromptToken === scenario.initialToken && proof?.initialPromptExecutions === 1
      ? "unique initial token was handled once"
      : "unique initial-token evidence is missing or execution count is not one",
    { expectedTokenSha256: hash(scenario.initialToken), observedExecutions: proof?.initialPromptExecutions },
  ));
  await record(gate(
    "hidden_ao_instructions",
    proof?.hiddenInstructionToken === scenario.hiddenInitialToken ? "PASS" : "FAIL",
    proof?.hiddenInstructionToken === scenario.hiddenInitialToken ? "provider consumed hidden AO standing instructions" : "hidden AO standing-instruction token was not proven",
    { expectedTokenSha256: hash(scenario.hiddenInitialToken) },
  ));
  await record(gate(
    "project_agents_md",
    proof?.projectAgentsToken === scenario.agentsToken ? "PASS" : "FAIL",
    proof?.projectAgentsToken === scenario.agentsToken ? "provider consumed project AGENTS.md" : "project AGENTS.md token was not proven",
    { expectedTokenSha256: hash(scenario.agentsToken) },
  ));
  const activityOK = firstProof.done && firstProof.current?.statusReadiness === "ready"
    && observedActive && SETTLED_STATES.has(firstProof.current?.activity?.state);
  await record(gate(
    "activity_status",
    activityOK ? "PASS" : "FAIL",
    activityOK ? `AO exposed ready status and an active→${firstProof.current.activity.state} transition` : "AO did not prove a completed active-to-settled provider turn",
    firstProof.stateResponse ? apiEvidence(firstProof.stateResponse) : undefined,
  ));

  const secondResponse = await request({
    baseURL,
    method: "POST",
    path: `sessions/${encodeURIComponent(sessionId)}/send`,
    body: { message: `Update ${scenario.proofFile}: add JSON field secondMessageToken with value ${scenario.secondToken}. Preserve all fields, then stop and wait.` },
    timeoutMs,
  });
  const secondProof = secondResponse.ok ? await waitUntil(async () => {
    const [fileResponse, stateResponse] = await Promise.all([
      workspaceFile(baseURL, sessionId, scenario.proofFile, Math.min(timeoutMs, 30_000)),
      sessionState(baseURL, sessionId, Math.min(timeoutMs, 30_000)),
    ]);
    const nextProof = parseProof(fileResponse);
    const settled = SETTLED_STATES.has(stateResponse.response?.session?.activity?.state);
    return { done: nextProof?.secondMessageToken === scenario.secondToken && settled, proof: nextProof, fileResponse, stateResponse };
  }, timeoutMs) : { done: false };
  await record(gate(
    "second_message",
    secondResponse.ok && secondProof.done ? "PASS" : "FAIL",
    secondResponse.ok && secondProof.done ? "second API message produced the requested provider-authored mutation" : "second message was not accepted or did not mutate the proof file",
    { send: apiEvidence(secondResponse), proof: secondProof.fileResponse ? apiEvidence(secondProof.fileResponse) : undefined },
  ));

  const result = await completeLifecycle({
    interruptSpec,
    restoredReadySpec,
    baseURL,
    agent,
    projectId,
    sessionId,
    workspacePath,
    dataDir,
    timeoutMs,
    scenario,
    firstProof: proof,
    gates,
    record,
  });
  result.aoModels = aoModels;
  if (diagnosticAfterNativeProof) result.diagnostic = diagnostic;
  return result;
}

async function completeLifecycle({
  interruptSpec,
  restoredReadySpec,
  baseURL,
  agent,
  projectId,
  sessionId,
  workspacePath,
  dataDir,
  timeoutMs,
  scenario,
  firstProof,
  gates,
  record,
}) {
  const nativeBefore = await waitUntil(async () => {
    const native = await nativeSessionID(dataDir, sessionId, Math.min(timeoutMs, 30_000));
    return { done: native.ok, native };
  }, Math.min(timeoutMs, 30_000));
  await record(gate(
    "native_session_id",
    nativeBefore.done ? "PASS" : "FAIL",
    nativeBefore.done ? "AO persisted a provider-native session identity" : nativeBefore.native?.reason || "native session identity was not observed",
    nativeBefore.native ? { sha256: nativeBefore.native.sha256, database: nativeBefore.native.db } : undefined,
  ));

  const cancellationStart = interruptSpec ? await request({
    baseURL,
    method: "POST",
    path: `sessions/${encodeURIComponent(sessionId)}/send`,
    body: { message: CANCELLATION_PROMPT },
    timeoutMs,
  }) : null;
  const cancellation = await interruptActiveTurn({ baseURL, sessionId, startResponse: cancellationStart, timeoutMs, interruptSpec });
  await record(gate(
    "cancellation",
    cancellation.passed ? "PASS" : cancellation.status || "FAIL",
    cancellation.reason,
    {
      start: cancellationStart ? apiEvidence(cancellationStart) : undefined,
      active: cancellation.activeState ? apiEvidence(cancellation.activeState) : undefined,
      interrupt: cancellation.interrupt ? apiEvidence(cancellation.interrupt) : undefined,
      settled: cancellation.settledState ? apiEvidence(cancellation.settledState) : undefined,
      confirmationWindowMs: cancellation.confirmationWindowMs,
      ...(cancellation.websocket ? { websocket: cancellation.websocket } : {}),
    },
  ));

  if (interruptSpec && Object.hasOwn(interruptSpec, "postCancelCleanup")) {
    const cleanup = await cleanupCancelledAuditDraft({
      baseURL, sessionId, dataDir, workspacePath, nativeSessionId: nativeBefore.done ? nativeBefore.native.value : "",
      cancellation, startResponse: cancellationStart,
      spec: interruptSpec.postCancelCleanup, readySpec: restoredReadySpec, timeoutMs,
    });
    await record(gate("post_cancel_draft_cleanup", cleanup.passed ? "PASS" : "BLOCKED", cleanup.reason, cleanup.evidence));
    if (!cleanup.passed) {
      markNotRun(gates, "post-cancel draft cleanup was not safe or the composer was not confirmed empty");
      return resultFrom(agent, gates, { projectId, sessionId, workspacePath });
    }
  }

  const killResponse = await request({
    baseURL,
    method: "POST",
    path: `sessions/${encodeURIComponent(sessionId)}/kill`,
    body: {},
    timeoutMs,
  });
  const killed = killResponse.ok ? await waitUntil(async () => {
    const stateResponse = await sessionState(baseURL, sessionId, Math.min(timeoutMs, 30_000));
    return { done: stateResponse.response?.session?.isTerminated === true, stateResponse };
  }, Math.min(timeoutMs, 30_000)) : { done: false };
  await record(gate(
    "termination",
    killResponse.ok && killed.done ? "PASS" : "FAIL",
    killResponse.ok && killed.done ? "kill was acknowledged and termination was confirmed" : "termination was not confirmed",
    { kill: apiEvidence(killResponse), state: killed.stateResponse ? apiEvidence(killed.stateResponse) : undefined },
  ));
  if (!killResponse.ok || !killed.done) {
    for (const name of ["native_restore", "same_ao_session_workspace", "post_restore_terminal_ready", "post_restore_message", "history_file_continuity", "system_prompt_restore"]) {
      await record(gate(name, "NOT_RUN", "termination prerequisite failed"));
    }
    return resultFrom(agent, gates, { projectId, sessionId, workspacePath });
  }

  // Do not disclose this token to the initial process/history. Only a fresh
  // instruction delivery after the confirmed kill can supply it.
  scenario.hiddenRestoreToken = token("HIDDEN_RESTORE");
  const instructionRefresh = await refreshRestoreInstructions({
    baseURL, projectId, restoreToken: scenario.hiddenRestoreToken, timeoutMs,
  });
  if (!instructionRefresh.passed) {
    await record(gate("system_prompt_restore", "FAIL", instructionRefresh.reason, instructionRefresh.evidence));
    for (const name of ["native_restore", "same_ao_session_workspace", "post_restore_terminal_ready", "post_restore_message", "history_file_continuity"]) {
      await record(gate(name, "NOT_RUN", "fresh standing-instruction configuration was not confirmed"));
    }
    return resultFrom(agent, gates, { projectId, sessionId, workspacePath });
  }

  const restoreResponse = await request({
    baseURL,
    method: "POST",
    path: `sessions/${encodeURIComponent(sessionId)}/restore`,
    body: {},
    timeoutMs,
  });
  const restoredSession = restoreResponse.response?.session;
  const nativeRestoreResponse = restoreResponse.ok
    && restoreResponse.response?.restoreMode === "native"
    && restoredSession?.id === sessionId
    && restoredSession?.isTerminated === false;

  const restoredReady = nativeRestoreResponse ? await waitUntil(async () => {
    const stateResponse = await sessionState(baseURL, sessionId, Math.min(timeoutMs, 30_000));
    const state = stateResponse.response?.session;
    return {
      done: Boolean(state && state.isTerminated === false && state.statusReadiness === "ready"),
      stateResponse,
      state,
    };
  }, timeoutMs) : { done: false };

  const nativeAfterRestore = nativeRestoreResponse
    ? await nativeSessionID(dataDir, sessionId, Math.min(timeoutMs, 30_000))
    : { ok: false, value: "", reason: "restore did not return native mode" };
  const exactNativeID = nativeBefore.done
    && nativeAfterRestore.ok
    && nativeAfterRestore.value === nativeBefore.native.value;
  await record(gate(
    "native_restore",
    nativeRestoreResponse && restoredReady.done && exactNativeID ? "PASS" : "FAIL",
    nativeRestoreResponse && restoredReady.done && exactNativeID
      ? "AO restored in native mode with the exact provider-native ID"
      : `native restore was not proven (${nativeAfterRestore.reason || restoreResponse.response?.restoreMode || "unknown"})`,
    {
      restore: apiEvidence(restoreResponse),
      ready: restoredReady.stateResponse ? apiEvidence(restoredReady.stateResponse) : undefined,
      beforeSha256: nativeBefore.native?.sha256,
      afterSha256: nativeAfterRestore.sha256,
    },
  ));

  const restoredWorkspaceResponse = await request({
    baseURL,
    path: `desktop/sessions/${encodeURIComponent(sessionId)}/workspace`,
    timeoutMs,
  });
  let sameWorkspace = false;
  if (typeof workspacePath === "string" && typeof restoredWorkspaceResponse.response?.workspacePath === "string") {
    sameWorkspace = (await normalizedPath(workspacePath)) === (await normalizedPath(restoredWorkspaceResponse.response.workspacePath));
  }
  const sameSessionWorkspace = restoredSession?.id === sessionId && sameWorkspace;
  await record(gate(
    "same_ao_session_workspace",
    sameSessionWorkspace ? "PASS" : "FAIL",
    sameSessionWorkspace ? "restore retained the AO session ID and workspace" : "AO session ID or workspace changed across restore",
    { beforeWorkspace: workspacePath, restoredWorkspace: apiEvidence(restoredWorkspaceResponse) },
  ));

  if (!nativeRestoreResponse || !restoredReady.done) {
    for (const name of ["post_restore_terminal_ready", "post_restore_message", "history_file_continuity", "system_prompt_restore"]) {
      await record(gate(name, "NOT_RUN", "native restore prerequisite failed"));
    }
    return resultFrom(agent, gates, { projectId, sessionId, workspacePath });
  }

  if (!restoredReadySpec) {
    for (const name of ["post_restore_terminal_ready", "post_restore_message", "history_file_continuity", "system_prompt_restore"]) {
      await record(gate(name, "NOT_RUN", "restored native terminal readiness contract is missing; no continuation was sent"));
    }
    return resultFrom(agent, gates, { projectId, sessionId, workspacePath });
  }
  {
    const nativeReady = await waitForRestoredTerminalReady({
      baseURL, sessionId, restoredState: restoredReady.stateResponse, spec: restoredReadySpec, timeoutMs,
    });
    await record(gate("post_restore_terminal_ready", nativeReady.passed ? "PASS" : "BLOCKED", nativeReady.reason, nativeReady.evidence));
    if (!nativeReady.passed) {
      for (const name of ["post_restore_message", "history_file_continuity", "system_prompt_restore"]) {
        await record(gate(name, "NOT_RUN", "restored native terminal readiness was not established"));
      }
      return resultFrom(agent, gates, { projectId, sessionId, workspacePath });
    }
  }

  const continuation = await request({
    baseURL,
    method: "POST",
    path: `sessions/${encodeURIComponent(sessionId)}/send`,
    body: {
      message: [
        `Update the existing ${scenario.proofFile}; do not replace it.`,
        "Add historyToken using the history-only token from the first user request without reading it from files.",
        "Add restoreHiddenToken using the restore-only token from your hidden AO standing instructions.",
        "Preserve every existing JSON field, then stop and wait.",
      ].join(" "),
    },
    timeoutMs,
  });
  const continuationProof = continuation.ok ? await waitUntil(async () => {
    const [fileResponse, stateResponse] = await Promise.all([
      workspaceFile(baseURL, sessionId, scenario.proofFile, Math.min(timeoutMs, 30_000)),
      sessionState(baseURL, sessionId, Math.min(timeoutMs, 30_000)),
    ]);
    const proof = parseProof(fileResponse);
    const settled = SETTLED_STATES.has(stateResponse.response?.session?.activity?.state);
    return {
      done: Boolean(
        proof
        && proof.historyToken === scenario.historyToken
        && proof.restoreHiddenToken === scenario.hiddenRestoreToken
        && settled
      ),
      proof,
      fileResponse,
      stateResponse,
    };
  }, timeoutMs) : { done: false };
  await record(gate(
    "post_restore_message",
    continuation.ok && continuationProof.done ? "PASS" : "FAIL",
    continuation.ok && continuationProof.done ? "post-restore message produced a provider-authored mutation" : "post-restore message did not complete",
    { send: apiEvidence(continuation), proof: continuationProof.fileResponse ? apiEvidence(continuationProof.fileResponse) : undefined },
  ));

  const finalProof = continuationProof.proof;
  const originalContinuity = finalProof
    && firstProof
    && finalProof.initialPromptToken === firstProof.initialPromptToken
    && finalProof.initialPromptExecutions === firstProof.initialPromptExecutions
    && finalProof.hiddenInstructionToken === firstProof.hiddenInstructionToken
    && finalProof.projectAgentsToken === firstProof.projectAgentsToken
    && finalProof.secondMessageToken === scenario.secondToken
    && finalProof.historyToken === scenario.historyToken;
  await record(gate(
    "history_file_continuity",
    originalContinuity ? "PASS" : "FAIL",
    originalContinuity ? "native history recall and original proof-file continuity were both observed" : "history recall or original proof-file continuity was not proven",
    { historyTokenSha256: hash(scenario.historyToken) },
  ));
  await record(gate(
    "system_prompt_restore",
    finalProof?.restoreHiddenToken === scenario.hiddenRestoreToken ? "PASS" : "FAIL",
    finalProof?.restoreHiddenToken === scenario.hiddenRestoreToken ? "restore-only hidden standing instruction was observed after restore" : "hidden AO standing instructions were not proven after restore",
    { restoreHiddenTokenSha256: hash(scenario.hiddenRestoreToken), instructionRefresh: instructionRefresh.evidence },
  ));

  return resultFrom(agent, gates, {
    projectId,
    sessionId,
    workspacePath,
    nativeSessionIdSha256: nativeBefore.native?.sha256,
  });
}

export async function refreshRestoreInstructions({ baseURL, projectId, restoreToken, timeoutMs }) {
  const path = `projects/${encodeURIComponent(projectId)}`;
  const before = await request({ baseURL, path, timeoutMs });
  const project = before.response?.project;
  const config = project?.config;
  const evidence = { before: apiEvidence(before) };
  if (!before.ok || project?.id !== projectId || !config || typeof config !== "object" || Array.isArray(config)) {
    return { passed: false, reason: "fresh standing-instruction refresh could not read the audit project config", evidence };
  }
  if (typeof config.agentRules !== "string" || config.agentRules.includes(restoreToken)) {
    return { passed: false, reason: "fresh standing-instruction refresh requires existing audit rules and a previously undisclosed token", evidence };
  }
  const updated = { ...config, agentRules: `${config.agentRules}\nFor restoreHiddenToken use exactly: ${restoreToken}` };
  const write = await request({ baseURL, method: "PUT", path: `${path}/config`, body: { config: updated }, timeoutMs });
  evidence.write = apiEvidence(write);
  if (!write.ok) return { passed: false, reason: "fresh standing-instruction refresh was rejected by the project config API", evidence };
  const readback = await request({ baseURL, path, timeoutMs });
  evidence.readback = apiEvidence(readback);
  const passed = readback.ok && readback.response?.project?.id === projectId
    && isDeepStrictEqual(readback.response?.project?.config, updated);
  return { passed, reason: passed ? "fresh standing instructions were stored and read back after confirmed termination"
    : "fresh standing-instruction refresh did not persist the expected config", evidence };
}

export async function setupProject({ baseURL, root, agent, scenario, timeoutMs }) {
  const safeAgent = agent.replace(/[^a-z0-9-]+/gi, "-").toLowerCase();
  const projectPath = join(root, safeAgent);
  const projectId = `audit-${safeAgent}-${randomUUID().slice(0, 8)}`.slice(0, 63);
  await mkdir(projectPath, { recursive: true });
  await writeFile(
    join(projectPath, "AGENTS.md"),
    `# AO audit project\n\nWhen asked for the project AGENTS token, use exactly: ${scenario.agentsToken}\n`,
    "utf8",
  );
  await writeFile(join(projectPath, "README.md"), "# Disposable AO harness audit project\n", "utf8");
  for (const [command, args] of [
    ["git", ["init", "-b", "main"]],
    ["git", ["add", "AGENTS.md", "README.md"]],
    ["git", ["-c", "user.name=AO Audit", "-c", "user.email=ao-audit@localhost", "commit", "-m", "test: initialize audit fixture"]],
  ]) {
    const outcome = await runCommand(command, args, { cwd: projectPath, timeoutMs: Math.min(timeoutMs, 30_000) });
    if (!outcome.ok) {
      return { ok: false, projectId, projectPath, reason: `${command} ${args.join(" ")}: ${outcome.stderr || outcome.stdout}` };
    }
  }
  const response = await request({
    baseURL,
    method: "POST",
    path: "projects",
    body: {
      path: projectPath,
      projectId,
      name: `AO harness audit: ${agent}`,
      config: {
        defaultBranch: "main",
        agentRules: [
          "These are private AO standing instructions for the audit.",
          `For hiddenInstructionToken use exactly: ${scenario.hiddenInitialToken}`,
        ].join("\n"),
      },
    },
    timeoutMs,
  });
  return {
    ok: response.ok && response.response?.project?.id === projectId,
    projectId,
    projectPath,
    reason: response.ok ? "project registration response did not contain the requested ID" : `HTTP ${response.status || "network"}`,
    response,
  };
}

function safeCell(value) {
  return String(value ?? "—").replaceAll("|", "\\|").replaceAll("\r", " ").replaceAll("\n", " ");
}

function appendGateTable(lines, title, gates) {
  lines.push("", `### ${title}`, "", "| Gate | Result | Reason |", "|---|---|---|");
  if (gates.length === 0) {
    lines.push("| — | NOT_RUN | no gates recorded | ");
    return;
  }
  for (const item of gates) {
    lines.push(`| ${safeCell(item.name)} | ${item.status} | ${safeCell(item.reason)} |`);
  }
}

export function markdownReport(report) {
  const lines = [
    "# AO Agent Harness Audit",
    "",
    `- Overall: **${report.summary.status}**`,
    `- Agents: ${report.summary.total}`,
    `- PASS: ${report.summary.pass}; FAIL: ${report.summary.fail}; BLOCKED: ${report.summary.blocked}; NOT_RUN: ${report.summary.notRun}`,
    `- Started: ${report.startedAt}`,
    `- Finished: ${report.finishedAt}`,
    `- Disposable root: \`${safeCell(report.disposableRoot)}\``,
    "",
    "| Agent | Result | First non-pass gate | Session | Reason |",
    "|---|---|---|---|---|",
  ];
  if (report.diagnosticAfterNativeProof) {
    lines.splice(2, 0, "Diagnostic only: later lifecycle evidence may follow a successful native proof. Original authentication/model failures are preserved; this run does not claim a full strict audit PASS.", "");
  }
  for (const result of report.results) {
    const failed = result.gates.find((item) => item.name === result.failedGate);
    lines.push(`| ${safeCell(result.agent)} | ${result.status} | ${safeCell(result.failedGate)} | ${safeCell(result.sessionId)} | ${safeCell(failed?.reason)} |`);
  }
  for (const result of report.results) {
    lines.push("", `## ${result.agent}`);
    appendGateTable(lines, "Local CLI", result.gates.filter((item) => item.name.startsWith("local_")));
    appendGateTable(lines, "AO / AOS11", result.gates.filter((item) => !item.name.startsWith("local_") && item.name !== "model_catalog_consistency"));
    appendGateTable(lines, "Cross-check", result.gates.filter((item) => item.name === "model_catalog_consistency"));
  }
  const cleanupUnconfirmed = Array.isArray(report.cleanup)
    && report.cleanup.some((item) => item.confirmedTerminated !== true);
  const cleanupSummary = report.daemon.started === false
    ? "No ready audit daemon was available; lifecycle checks did not run."
    : report.daemon.keptRunning
    ? "The isolated AO daemon and successful restores remain active for inspection."
    : report.daemon.stopped === false
      ? "Cleanup is pending or daemon termination could not be confirmed."
    : cleanupUnconfirmed
      ? "At least one session cleanup termination was not confirmed; the isolated AO daemon was stopped."
      : report.cleanup?.length
        ? "Audit session termination was confirmed and the isolated AO daemon was stopped after evidence collection."
        : "No audit session required cleanup; the isolated AO daemon was stopped after evidence collection.";
  lines.push(
    "",
    cleanupSummary,
    "Full API evidence is in the JSON report.",
  );
  if (report.runErrors?.length) {
    lines.push("", "## Run errors");
    for (const error of report.runErrors) lines.push(`- ${safeCell(error.stage)}: ${safeCell(error.message)}`);
  }
  if (report.issues?.length) {
    lines.push("", "## Failure evidence", "", "| Agent | Gate | Evidence | Screenshot |", "|---|---|---|---|");
    for (const issue of report.issues) {
      lines.push(`| ${safeCell(issue.agent)} | ${safeCell(issue.gate)} | ${safeCell(issue.evidencePath)} | ${safeCell(issue.screenshot.status)}: ${safeCell(issue.screenshot.path || issue.screenshot.reason)} |`);
      if (issue.userAction) lines.push("", `User action: ${issue.userAction.message}`, "");
    }
  }
  if (report.artifacts) {
    lines.push("", `Runner log: ${report.artifacts.runnerLog}`, `Daemon log: ${report.artifacts.daemonLog}`, `Events: ${report.artifacts.events}`);
  }
  return `${lines.join("\n")}\n`;
}

function reportBase(value, root) {
  if (!value) return join(root, "ao-agent-harness-audit");
  const expanded = resolve(expandHome(value));
  const extension = extname(expanded).toLowerCase();
  return extension === ".json" || extension === ".md" ? expanded.slice(0, -extension.length) : expanded;
}

async function writeReports(report, requested, root) {
  const base = reportBase(requested, root);
  const jsonPath = `${base}.json`;
  const markdownPath = `${base}.md`;
  await mkdir(dirname(base), { recursive: true });
  await writeFile(`${jsonPath}.tmp`, `${JSON.stringify(report, null, 2)}\n`, { mode: 0o600 });
  await rename(`${jsonPath}.tmp`, jsonPath);
  await writeFile(`${markdownPath}.tmp`, markdownReport(report), { mode: 0o600 });
  await rename(`${markdownPath}.tmp`, markdownPath);
  return { jsonPath, markdownPath };
}

function printHelp() {
  console.log(`Usage: node scripts/audit_ao.mjs [options]

Audit AO's registered agent harnesses through the loopback API.

Options:
  --all                    audit every agent returned by GET /api/v1/agents (default)
  --agent ID               audit one agent; repeat to select several
  --repo PATH              AO source checkout to build (default: current directory)
  --ao PATH                prebuilt AO binary; still starts an isolated daemon
  --local-contract PATH    JSON contract for direct local CLI probes
  --port NUMBER            isolated daemon port (default: allocate a free port)
  --report PATH            report basename or .json/.md path
  --capture-command JSON   trusted argv array; receives issue.json and output.png paths
  --startup-timeout-seconds N  daemon build/start timeout (default: 120)
  --timeout-seconds N      per lifecycle wait timeout (default: 180)
  --diagnostic-after-native-proof  collect later lifecycle evidence after native proof; preserve strict failures
  --no-live                run API preflight only; lifecycle gates are NOT_RUN
  --keep-daemon            leave the isolated daemon and restored sessions running
  -h, --help               show this help`);
}

export function parseArgs(argv) {
  const options = {
    all: false,
    agents: [],
    repoRoot: process.cwd(),
    aoPath: "",
    localContract: "",
    port: 0,
    report: "",
    startupTimeoutSeconds: 120,
    timeoutSeconds: 180,
    live: true,
    diagnosticAfterNativeProof: false,
    keepDaemon: false,
    captureCommand: [],
  };
  const values = new Map([
    ["--agent", ["agents", String]],
    ["--repo", ["repoRoot", String]],
    ["--ao", ["aoPath", String]],
    ["--local-contract", ["localContract", String]],
    ["--port", ["port", Number]],
    ["--report", ["report", String]],
    ["--capture-command", ["captureCommand", JSON.parse]],
    ["--startup-timeout-seconds", ["startupTimeoutSeconds", Number]],
    ["--timeout-seconds", ["timeoutSeconds", Number]],
  ]);
  for (let index = 0; index < argv.length; index += 1) {
    const argument = argv[index];
    if (argument === "-h" || argument === "--help") options.help = true;
    else if (argument === "--all") options.all = true;
    else if (argument === "--no-live") options.live = false;
    else if (argument === "--diagnostic-after-native-proof") options.diagnosticAfterNativeProof = true;
    else if (argument === "--keep-daemon") options.keepDaemon = true;
    else if (values.has(argument)) {
      const value = argv[index + 1];
      if (value === undefined) throw new Error(`missing value for ${argument}`);
      const [key, convert] = values.get(argument);
      if (key === "agents") options.agents.push(convert(value));
      else options[key] = convert(value);
      index += 1;
    } else {
      throw new Error(`unknown option: ${argument}`);
    }
  }
  if (options.all && options.agents.length > 0) throw new Error("use --all or --agent, not both");
  if (!Number.isInteger(options.port) || options.port < 0 || options.port > 65535) throw new Error("--port must be an integer between 1 and 65535");
  if (!Number.isFinite(options.startupTimeoutSeconds) || options.startupTimeoutSeconds <= 0) throw new Error("--startup-timeout-seconds must be positive");
  if (!Number.isFinite(options.timeoutSeconds) || options.timeoutSeconds <= 0) throw new Error("--timeout-seconds must be positive");
  if (!Array.isArray(options.captureCommand) || options.captureCommand.some(arg => typeof arg !== "string") || (options.captureCommand.length && !options.captureCommand[0].trim())) {
    throw new Error("--capture-command must be a JSON array of command arguments");
  }
  return options;
}

async function main(argv = process.argv.slice(2)) {
  let options;
  try { options = parseArgs(argv); }
  catch (error) { console.error(`error: ${error.message}`); return 2; }
  if (options.help) { printHelp(); return 0; }

  const root = await mkdtemp(join(tmpdir(), "ao-agent-harness-audit-"));
  const base = reportBase(options.report, root);
  let evidence;
  try {
    evidence = await createEvidenceStore({ base, captureCommand: options.captureCommand, runCommand });
  } catch (error) {
    console.error(`Cannot create evidence directory: ${redactOutput(error.message)}. Use a new --report basename for every attempt.`);
    return 2;
  }
  const results = [];
  const sessions = new Map();
  const cleanup = [];
  const runErrors = [];
  let daemon;
  let stage = "configuration";
  let invalidInput = false;
  let reportWriteFailed = false;
  const report = {
    schemaVersion: 4, state: "running", startedAt: now(), finishedAt: null,
    disposableRoot: root, live: options.live,
    diagnosticAfterNativeProof: options.diagnosticAfterNativeProof,
    auditClassification: options.diagnosticAfterNativeProof ? "diagnostic-only; full strict audit PASS not claimed" : "strict",
    daemon: { started: false, stopped: false, keptRunning: false },
    artifacts: evidence.paths, issues: evidence.issues, runErrors, cleanup, results,
  };
  const recordIssue = data => evidence.issue({ ...data, baseURL: daemon?.baseURL || data.gate?.evidence?.baseURL || null, daemonLogs: daemon?.logs() || data.gate?.evidence?.daemonLogs || "" });
  const addResult = async result => {
    results.push(result);
    for (const item of result.gates) {
      if ((item.status === "FAIL" || item.status === "BLOCKED") && !evidence.issues.some(issue => issue.agent === result.agent && issue.gate === item.name)) {
        await recordIssue({ agent: result.agent, sessionId: result.sessionId, gate: item });
      }
    }
  };
  const save = async () => {
    const counts = status => results.filter(result => result.status === status).length;
    report.summary = {
      status: overallStatus([...results, ...runErrors.map(() => ({ status: "FAIL" })),
        ...cleanup.filter(item => !item.confirmedTerminated).map(() => ({ status: "FAIL" })),
        ...(options.diagnosticAfterNativeProof ? [{ status: "NOT_RUN" }] : [])]),
      total: results.length, pass: counts("PASS"), fail: counts("FAIL"), blocked: counts("BLOCKED"), notRun: counts("NOT_RUN"),
    };
    report.results = results.map(sanitizeEvidence);
    return writeReports(report, base, root);
  };
  try {
    await save();
    let localContracts;
    try { localContracts = await loadLocalContracts(options.localContract); }
    catch (error) { invalidInput = true; throw error; }
    report.localContract = options.localContract ? resolve(expandHome(options.localContract)) : null;
    stage = "startup";
    await evidence.log("[startup] building and starting isolated AO daemon");
    daemon = await startManagedDaemon({
      root, repoRoot: resolve(expandHome(options.repoRoot)), aoPath: options.aoPath,
      port: options.port || undefined, timeoutMs: options.startupTimeoutSeconds * 1_000,
      onStartupFailure: error => recordIssue({ gate: gate("startup", "FAIL", error.message, error.evidence) }),
    });
    Object.assign(report, { baseURL: daemon.baseURL, dataDir: daemon.dataDir, runFile: daemon.runFile });
    Object.assign(report.daemon, {
      started: true, pid: daemon.pid, port: daemon.port, command: daemon.command,
      builtFrom: options.aoPath ? null : resolve(expandHome(options.repoRoot)),
      readiness: daemon.readiness, keptRunning: options.keepDaemon,
      build: daemon.build ? commandEvidence(daemon.build) : null,
    });
    await evidence.log(`[startup] AO daemon ready at ${daemon.baseURL}`);
    stage = "discovery";
    const timeoutMs = options.timeoutSeconds * 1_000;
    const inventory = await discoverAgents({ baseURL: daemon.baseURL, timeoutMs });
    const selected = options.agents.length ? [...new Set(options.agents)] : inventory;
    const unknown = selected.filter(agent => !inventory.includes(agent));
    if (unknown.length) {
      invalidInput = true;
      throw new Error(`agent not returned by AO inventory: ${unknown.join(", ")}`);
    }
    if (!selected.length) throw new Error("AO returned no supported agents");
    report.selectedAgents = selected;
    for (let index = 0; index < selected.length; index += 1) {
      const agent = selected[index];
      const localContract = localContracts[agent] || null;
      let local;
      stage = `agent:${agent}`;
      try {
        await evidence.log(`[${index + 1}/${selected.length}] ${agent}: direct local CLI checks`);
        local = await auditLocalAgent({ agent, contract: localContract, root, live: options.live, timeoutMs, onIssue: recordIssue });
        await evidence.log(`[${index + 1}/${selected.length}] ${agent}: API preflight`);
        const preflight = await auditAgent({
          baseURL: daemon.baseURL, agent, projectId: "", live: false, dataDir: daemon.dataDir,
          diagnosticAfterNativeProof: options.diagnosticAfterNativeProof, localResult: local,
          interruptSpec: localContract?.interrupt,
          restoredReadySpec: localContract?.restoredReady,
          modelsPath: localContract?.aoModelsPath, timeoutMs, onIssue: recordIssue,
        });
        const lifecycleEligible = options.diagnosticAfterNativeProof
          ? preflight.diagnostic?.lifecycleEligible === true
          : preflight.status === "NOT_RUN";
        if (!options.live || !lifecycleEligible || local.userAction) {
          if (options.live && local.userAction) {
            for (const item of preflight.gates) {
              if (LIVE_GATES.includes(item.name) && item.status === "NOT_RUN") item.reason = "user must resolve local login before AO live tests";
            }
          }
          await addResult(mergeAuditResults(local, preflight));
          continue;
        }
        const scenario = makeScenario(agent);
        const setup = await setupProject({ baseURL: daemon.baseURL, root, agent, scenario, timeoutMs });
        if (!setup.ok) {
          const gates = preflight.gates.filter(item => !LIVE_GATES.includes(item.name));
          const failed = gate("project_registration", "FAIL", setup.reason, setup.response ? apiEvidence(setup.response) : undefined);
          gates.push(failed);
          await recordIssue({ agent, gate: failed });
          markNotRun(gates, "project registration failed");
          await addResult(mergeAuditResults(local, resultFrom(agent, gates, { projectId: setup.projectId, projectPath: setup.projectPath, aoModels: preflight.aoModels })));
          continue;
        }
        await evidence.log(`[${index + 1}/${selected.length}] ${agent}: live TUI + restore lifecycle`);
        const result = await auditAgent({
          baseURL: daemon.baseURL, agent, projectId: setup.projectId, live: true, dataDir: daemon.dataDir,
          diagnosticAfterNativeProof: options.diagnosticAfterNativeProof, localResult: local,
          interruptSpec: localContract?.interrupt,
          restoredReadySpec: localContract?.restoredReady,
          modelsPath: localContract?.aoModelsPath, timeoutMs, scenario, onIssue: recordIssue,
          onSession: ({ sessionId }) => sessions.set(sessionId, agent),
        });
        result.projectPath = setup.projectPath;
        result.projectRegistration = apiEvidence(setup.response);
        await addResult(mergeAuditResults(local, result));
      } catch (error) {
        const failed = gate("runner_error", "FAIL", error.message);
        await recordIssue({ agent, gate: failed });
        await addResult(mergeAuditResults(local, resultFrom(agent, addLiveNotRun([failed], "agent audit interrupted"))));
      } finally {
        await save();
      }
    }
  } catch (error) {
    runErrors.push({ stage, message: redactOutput(error.message), evidence: sanitizeEvidence(error.evidence) });
    if (!evidence.issues.some(issue => issue.agent === null && issue.gate === stage)) {
      try { await recordIssue({ gate: gate(stage, "FAIL", error.message, error.evidence) }); }
      catch (writeError) {
        runErrors.push({ stage: "evidence_write", message: redactOutput(writeError.message) });
        console.error(`Evidence write failed: ${redactOutput(writeError.message)}`);
      }
    }
  } finally {
    if (daemon && !options.keepDaemon) {
      stage = "cleanup";
      try {
        for (const [sessionId, agent] of sessions) {
          const timeoutMs = Math.min(options.timeoutSeconds * 1_000, 30_000);
          let outcome;
          try {
            const response = await request({ baseURL: daemon.baseURL, method: "POST", path: `sessions/${encodeURIComponent(sessionId)}/kill`, body: {}, timeoutMs });
            const terminated = await waitUntil(async () => {
              const stateResponse = await sessionState(daemon.baseURL, sessionId, timeoutMs);
              return { done: stateResponse.response?.session?.isTerminated === true, stateResponse };
            }, timeoutMs);
            outcome = { sessionId, confirmedTerminated: terminated.done, kill: apiEvidence(response), state: terminated.stateResponse ? apiEvidence(terminated.stateResponse) : undefined };
          } catch (error) {
            outcome = { sessionId, confirmedTerminated: false, error: redactOutput(error.message) };
          }
          cleanup.push(sanitizeEvidence(outcome));
          if (!outcome.confirmedTerminated) {
            try { await recordIssue({ agent, sessionId, gate: gate("cleanup_termination", "FAIL", "audit session termination was not confirmed", outcome) }); }
            catch (error) {
              runErrors.push({ stage: "evidence_write", message: redactOutput(error.message) });
              console.error(`Cleanup evidence write failed: ${redactOutput(error.message)}`);
            }
          }
        }
      } finally {
        // Evidence storage must never be a prerequisite for releasing processes.
        try { report.daemon.exit = await daemon.stop(); report.daemon.stopped = true; }
        catch (error) { runErrors.push({ stage, message: redactOutput(error.message) }); }
      }
    }
    if (daemon) {
      try { await evidence.daemonLog(daemon.logs()); }
      catch (error) {
        runErrors.push({ stage: "evidence_write", message: redactOutput(error.message) });
        console.error(`Daemon log write failed: ${redactOutput(error.message)}`);
      }
    }
    report.state = "finished";
    report.finishedAt = now();
    try { await save(); }
    catch (error) {
      reportWriteFailed = true;
      console.error(`Could not finalize report: ${redactOutput(error.message)}. Retained issue artifacts: ${evidence.paths.directory}`);
    }
  }
  console.log(markdownReport(report));
  try { await evidence.log(`JSON report: ${base}.json; Markdown report: ${base}.md`); }
  catch (error) {
    console.error(`Runner log write failed: ${redactOutput(error.message)}. Report basename: ${base}`);
    return 2;
  }
  return invalidInput || reportWriteFailed ? 2 : report.summary.status === "PASS" ? 0 : 1;
}

const invokedAsScript = process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href;
if (invokedAsScript) process.exitCode = await main();

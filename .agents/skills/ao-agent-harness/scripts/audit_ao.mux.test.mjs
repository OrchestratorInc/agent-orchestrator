import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { createServer } from "node:http";
import { mkdtempSync, mkdirSync, writeFileSync, rmSync, symlinkSync, existsSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, dirname, delimiter, resolve } from "node:path";
import test from "node:test";
import * as auditModule from "./audit_ao.mjs";
const { auditAgent, interruptActiveTurn, waitForRestoredTerminalReady } = auditModule;

// Real RFC6455 transport with AO's documented /mux JSON messages.
async function withMuxFixture(scenario, run, lifecycleHooks = {}) {
  const received = [];
  const sockets = new Set();
  let opened = false;
  let inputReceived = false;
  let inputReceivedAt = 0;
  let cleanupInputs = 0;
  let killRequests = 0;
  let upgrades = 0;
  let cancellationStarted = false;
  let nativeCueSent = false;
  let killed = false;
  let restored = false;
  const messages = [];
  const timers = [];
  let projectConfig = { agentRules: "original audit rules", defaultBranch: "main" };
  let continued = false;
  const target = "opaque:terminal/fixture";
  const cleanupScenario = scenario.startsWith("lifecycle-cleanup");
  function send(socket, body) {
    const payload = Buffer.from(JSON.stringify(body));
    const header = payload.length < 126 ? Buffer.from([0x81, payload.length])
      : Buffer.from([0x81, 126, payload.length >> 8, payload.length & 255]);
    socket.write(Buffer.concat([header, payload]));
  }
  const server = createServer((req, res) => {
    if (scenario.startsWith("lifecycle") && req.url === "/api/v1/projects/project") {
      res.end(JSON.stringify({ project: { id: "project", config: projectConfig } })); return;
    }
    if (scenario.startsWith("lifecycle") && req.url === "/api/v1/projects/project/config") {
      let raw = "";
      req.on("data", part => { raw += part; });
      req.on("end", () => {
        assert.equal(killed, true, "standing instructions must refresh only after confirmed kill");
        if (scenario !== "lifecycle-drops-refreshed-instructions") projectConfig = JSON.parse(raw).config;
        res.end(JSON.stringify({ project: { id: "project", config: JSON.parse(raw).config } }));
      });
      return;
    }
    if (req.url.endsWith("/kill")) { killRequests++; lifecycleHooks.onKill?.(); }
    if (scenario.startsWith("lifecycle") && req.url !== "/api/v1/sessions/fixture") {
      let body;
      if (req.url.endsWith("/probe")) body = { supported: true, installed: true, agent: { authStatus: "configured" } };
      else if (req.url.endsWith("/readiness/ensure")) body = { agents: [{ id: "fixture", installation: { state: "installed", freshness: "fresh" }, authentication: { state: "configured", freshness: "fresh" } }] };
      else if (req.url.endsWith("/models")) body = { models: [] };
      else if (req.url === "/api/v1/sessions") body = { session: { id: "fixture", mode: "tui", harness: "fixture", activity: { state: "active" } } };
      else if (req.url.endsWith("/workspace")) body = { workspacePath: lifecycleHooks.workspacePath || "/tmp/fixture-workspace" };
      else if (req.url.includes("/workspace/file")) body = { content: scenario === "lifecycle-retains-old-instructions" ? JSON.stringify({
        cwd: "/tmp/fixture-workspace", initialPromptToken: "initial", initialPromptExecutions: 1,
        hiddenInstructionToken: "initial-hidden", projectAgentsToken: "project-agents", secondMessageToken: "second",
        ...(continued ? { historyToken: "history", restoreHiddenToken: "old-history-instruction" } : {}),
      }) : "{}" };
      else if (req.url.endsWith("/send")) {
        let raw = "";
        req.on("data", part => { raw += part; });
        req.on("end", () => {
          messages.push(JSON.parse(raw).message);
          if (JSON.parse(raw).message.startsWith("For cancellation testing")) {
            cancellationStarted = true; res.end("{}");
          } else if (scenario === "lifecycle-retains-old-instructions") {
            if (JSON.parse(raw).message.startsWith("Update the existing")) continued = true;
            res.end("{}");
          } else { res.writeHead(422); res.end("{}"); }
        });
        return;
      }
      else if (["lifecycle-ready-blocked", "lifecycle-drops-refreshed-instructions", "lifecycle-retains-old-instructions"].includes(scenario) && req.url.endsWith("/kill")) { killed = true; body = { ok: true }; }
      else if (["lifecycle-ready-blocked", "lifecycle-drops-refreshed-instructions", "lifecycle-retains-old-instructions"].includes(scenario) && req.url.endsWith("/restore")) {
        killed = false; restored = true; cancellationStarted = false;
        body = { restoreMode: "native", session: { id: "fixture", isTerminated: false } };
      }
      if (body) { res.setHeader("content-type", "application/json"); res.end(JSON.stringify(body)); return; }
    }
    if (req.url !== "/api/v1/sessions/fixture") { res.writeHead(404); res.end("{}"); return; }
    if (scenario === "state-http-error" && opened) {
      res.writeHead(503); res.end("{}"); return;
    }
    const active = !scenario.startsWith("ready-") && (!scenario.startsWith("lifecycle") || cancellationStarted || scenario === "lifecycle-activity-active") && scenario !== "never-active" && !(opened && scenario === "inactive-before-input")
      && !(inputReceived && !["never-settles", "lifecycle-cleanup-never-settles"].includes(scenario)
        && !(scenario === "delayed-settle" && Date.now() - inputReceivedAt < 5200));
    res.setHeader("content-type", "application/json");
    res.end(JSON.stringify({ session: {
      id: cleanupScenario && upgrades > 1 && scenario.endsWith("-foreign") ? "foreign-session" : "fixture", isTerminated: killed, statusReadiness: "ready", activity: { state: cleanupScenario && upgrades > 1 && scenario.endsWith("-reactivated") ? "active" : active ? "active" : "idle" },
      ...(cleanupScenario && !scenario.endsWith("-marker-absent") ? { lastUserMessageAt: lifecycleHooks.lastUserMessageAt || (scenario.endsWith("-marker-null") ? null : scenario.endsWith("-marker-blank") ? " " : inputReceived && scenario.endsWith("-newer-during-cancel") || upgrades > 1 && scenario.endsWith("-newer-user") ? "2026-10-10T02:00:00Z" : "2026-10-10T01:00:00Z") } : {}),
      terminalHandleId: (opened && scenario === "changed-handle" || cleanupScenario && upgrades > 1 && scenario.endsWith("-handle")) ? "other-terminal" : target,
      terminalGeneration: restored ? "restored-epoch" : (opened && ["changed-generation", "ready-generation-change"].includes(scenario) || cleanupScenario && upgrades > 1 && scenario.endsWith("-generation")) ? "epoch-2" : "epoch-1",
    } }));
  });
  server.on("upgrade", (req, socket, head) => {
    upgrades++;
    assert.equal(req.url, "/mux");
    sockets.add(socket);
    const accept = createHash("sha1").update(req.headers["sec-websocket-key"] + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11").digest("base64");
    socket.write("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: " + accept + "\r\n\r\n");
    let pending = head;
    const consume = chunk => {
      pending = Buffer.concat([pending, chunk]);
      while (pending.length >= 2) {
        const opcode = pending[0] & 15;
        const masked = Boolean(pending[1] & 128);
        let length = pending[1] & 127;
        let offset = 2;
        if (length === 126) { if (pending.length < 4) return; length = pending.readUInt16BE(2); offset = 4; }
        assert.notEqual(length, 127, "fixture frames must be bounded");
        if (pending.length < offset + (masked ? 4 : 0) + length) return;
        const mask = masked ? pending.subarray(offset, offset + 4) : null;
        offset += masked ? 4 : 0;
        const body = Buffer.from(pending.subarray(offset, offset + length));
        pending = pending.subarray(offset + length);
        if (mask) for (let i = 0; i < body.length; i++) body[i] ^= mask[i % 4];
        if (opcode === 8) { socket.end(Buffer.from([0x88, 0])); return; }
        assert.equal(opcode, 1);
        assert.equal(masked, true, "real WebSocket clients mask input");
        const frame = JSON.parse(body.toString());
        received.push(frame);
        if (frame.type === "open") {
          assert.deepEqual(frame, { ch: "terminal", type: "open", id: target, role: "secondary" });
          opened = true;
          if (cleanupScenario && upgrades > 1) {
            send(socket, { ch: "terminal", id: target, type: "opened" });
            const draft = "> For cancellation testing, run a foreground command that waits for 120 seconds. Do not modify files. Wait for the command to finish.\n────────\n  [model] | native session\n";
            const ready = ">  \n────────\n  [model] | native session\n";
            send(socket, { ch: "terminal", id: target, type: "data",
              data: Buffer.from((scenario.endsWith("-stale-cue") ? ready : "") + (scenario.endsWith("-already-empty") ? ready : draft + (scenario.endsWith("-foreign-draft") ? "> unrelated user draft\n────────\n  [model] | native session\n" : ""))).toString("base64") });
            continue;
          }
          if (scenario.startsWith("ready-") || ["lifecycle-ready-blocked", "lifecycle-retains-old-instructions"].includes(scenario)) {
            send(socket, { ch: "terminal", id: target, type: "opened" });
            if (scenario === "ready-error") { send(socket, { ch: "terminal", id: target, type: "error", error: "restore attach failed" }); continue; }
            const output = text => ({ ch: "terminal", id: target, type: "data", data: Buffer.from(text).toString("base64") });
            send(socket, output("<ao-session-delivery>\nEchoed continuation before native startup\n</ao-session-delivery>\n"));
            if (["ready-echo-only", "lifecycle-ready-blocked"].includes(scenario)) continue;
            if (scenario === "ready-draft") { send(socket, output("> [Pasted text #1 3 lines]\n────────\n  [model] | native session\n")); continue; }
            timers.push(setTimeout(() => {
              const frame = output("\x1b[32m>  \x1b[0m\n────────\n  [model] | native session\n");
              if (scenario === "ready-foreign") frame.id = "foreign-terminal";
              if (scenario === "ready-utf8") {
                const bytes = Buffer.from("\x1b[32m>  \x1b[0m\n────────\n  [model] | native session\n");
                const split = bytes.indexOf(Buffer.from("─")) + 1;
                send(socket, { ...frame, data: bytes.subarray(0, split).toString("base64") });
                send(socket, { ...frame, data: bytes.subarray(split).toString("base64") });
              } else send(socket, frame);
              nativeCueSent = true;
            }, 40));
            continue;
          }
          if (scenario === "socket-close") { socket.end(); return; }
          if (scenario === "open-error") { send(socket, { ch: "terminal", id: target, type: "error", error: "input disabled" }); continue; }
          send(socket, { ch: "terminal", id: "foreign-terminal", type: "opened" });
          if (scenario !== "wrong-opened") send(socket, { ch: "terminal", id: target, type: "opened" });
        } else if (frame.type === "data") {
          assert.equal(frame.ch, "terminal");
          assert.equal(frame.id, target);
          if (cleanupScenario && upgrades > 1) {
            cleanupInputs++;
            assert.deepEqual(Buffer.from(frame.data, "base64"), Buffer.from("\x03"));
            const timer = lifecycleHooks.onCleanupInput?.({ emit: text => send(socket, { ch: "terminal", id: target, type: "data", data: Buffer.from(text).toString("base64") }) });
            if (timer) timers.push(timer);
            if (!scenario.endsWith("-stale-cue")) {
              send(socket, { ch: "terminal", id: scenario.endsWith("-foreign-output") ? "foreign-terminal" : target, type: "data",
                data: Buffer.from(scenario.endsWith("-draft-remains")
                  ? "> remaining draft\n────────\n  [model] | native session\n"
                  : ">  \n────────\n  [model] | native session\n").toString("base64") });
            }
            continue;
          }
          assert.deepEqual(Buffer.from(frame.data, "base64"), Buffer.from(scenario === "ctrl-c" ? "\x03" : "\x1b"));
          inputReceived = true;
          inputReceivedAt = Date.now();
          if (scenario === "input-error") send(socket, { ch: "terminal", id: target, type: "error", error: "session input is disabled" });
        } else if (frame.ch === "system" && frame.type === "ping") {
          if (scenario !== "no-pong") send(socket, { ch: "system", type: "pong" });
        } else assert.deepEqual(frame, { ch: "terminal", type: "close", id: target });
      }
    };
    socket.on("data", consume);
    socket.on("error", () => {});
    if (head.length) consume(Buffer.alloc(0));
  });
  await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
  try {
    await run({ baseURL: "http://127.0.0.1:" + server.address().port,
      received, messages, get cleanupInputs() { return cleanupInputs; }, get killRequests() { return killRequests; }, get projectConfig() { return projectConfig; }, get nativeCueSent() { return nativeCueSent; }, get inputReceived() { return inputReceived; }, get upgrades() { return upgrades; } });
  } finally {
    for (const timer of timers) clearTimeout(timer);
    for (const socket of sockets) socket.destroy();
    await new Promise(resolve => server.close(resolve));
  }
}

for (const scenario of ["escape", "ctrl-c"]) {
  test("mux cancellation sends configured " + scenario + " to the exact active terminal and observes settling", async () => {
    await withMuxFixture(scenario, async fixture => {
      const result = await interruptActiveTurn({ baseURL: fixture.baseURL, sessionId: "fixture",
        startResponse: { ok: true }, timeoutMs: 500, interruptSpec: { input: scenario === "ctrl-c" ? "\x03" : "\x1b" } });
      assert.equal(result.passed, true, JSON.stringify(result));
      assert.equal(fixture.inputReceived, true);
      assert.equal(result.interrupt, undefined, "do not manufacture HTTP interruption evidence");
      assert.equal(result.websocket.transport, "websocket");
      assert.equal(result.websocket.opened, true);
      assert.equal(result.websocket.pongObserved, true);
      assert.equal(result.websocket.inputBytes, 1);
      assert.equal(result.settledState.response.session.activity.state, "idle");
    });
  });
}

for (const scenario of ["open-error", "socket-close", "wrong-opened", "changed-handle", "changed-generation", "inactive-before-input", "state-http-error", "never-active"]) {
  test("mux cancellation sends no input for " + scenario, async () => {
    await withMuxFixture(scenario, async fixture => {
      const result = await interruptActiveTurn({ baseURL: fixture.baseURL, sessionId: "fixture",
        startResponse: { ok: true }, timeoutMs: 120, interruptSpec: { input: "\x1b" } });
      assert.equal(result.passed, false);
      assert.equal(fixture.inputReceived, false, JSON.stringify(fixture.received));
      if (scenario === "never-active") assert.equal(fixture.upgrades, 0);
      else assert.equal(result.websocket.transport, "websocket");
    });
  });
}

for (const scenario of ["input-error", "no-pong", "never-settles"]) {
  test("mux cancellation fails with retained WS evidence for " + scenario, async () => {
    await withMuxFixture(scenario, async fixture => {
      const result = await interruptActiveTurn({ baseURL: fixture.baseURL, sessionId: "fixture",
        startResponse: { ok: true }, timeoutMs: 120, interruptSpec: { input: "\x1b" } });
      assert.equal(result.passed, false);
      assert.equal(fixture.inputReceived, true);
      assert.equal(result.websocket.inputBytes, 1);
      assert.equal(typeof result.websocket.error, "string");
      assert.ok(result.websocket.error.length > 0);
    });
  });
}

test("auditAgent carries runtime interrupt config and WS evidence through the lifecycle boundary", async () => {
  await withMuxFixture("lifecycle", async fixture => {
    const result = await auditAgent({
      baseURL: fixture.baseURL, agent: "fixture", projectId: "project", timeoutMs: 500,
      diagnosticAfterNativeProof: true, interruptSpec: { input: "\x1b" },
      localResult: { agent: "fixture", gates: ["local_binary", "local_version", "local_integration", "local_session_spawn"].map(name => ({ name, status: "PASS" })) },
    });
    const gate = name => result.gates.find(item => item.name === name);
    assert.equal(fixture.inputReceived, true);
    assert.equal(gate("cancellation").status, "PASS", JSON.stringify(gate("cancellation")));
    assert.equal(gate("activity_status").status, "PASS");
    assert.equal(gate("cancellation").evidence.websocket.transport, "websocket");
    assert.equal(gate("cancellation").evidence.interrupt, undefined);
    assert.equal(gate("authentication").status, "BLOCKED");
    assert.equal(gate("ao_models_api").status, "FAIL");
    assert.equal(gate("termination").status, "FAIL");
    assert.equal(result.diagnostic.lifecycleEligible, true);
  });
});

const nativeReadySpec = { patterns: ["(?:^|\\n)>[ \\t]*\\n─+\\n[ \\t]*\\[model\\][^\\n]*\\n*$"] };
const restoredFixtureState = { ok: true, response: { session: {
  id: "fixture", isTerminated: false, activity: { state: "idle" },
  terminalHandleId: "opaque:terminal/fixture", terminalGeneration: "epoch-1",
} } };

test("restored native readiness ignores early echoed text and awaits the genuine empty composer", async () => {
  await withMuxFixture("ready-success", async fixture => {
    assert.equal(typeof waitForRestoredTerminalReady, "function");
    const result = await waitForRestoredTerminalReady({
      baseURL: fixture.baseURL, sessionId: "fixture", restoredState: restoredFixtureState,
      spec: nativeReadySpec, timeoutMs: 500,
    });
    assert.equal(result.passed, true, JSON.stringify(result));
    assert.equal(fixture.nativeCueSent, true);
    assert.equal(fixture.inputReceived, false);
    assert.equal(result.evidence.transport, "websocket");
    assert.equal(result.evidence.matchedPatterns, 1);
    assert.ok(result.evidence.outputFrames >= 2);
  });
});

for (const scenario of ["ready-echo-only", "ready-draft", "ready-foreign", "ready-generation-change", "ready-error"]) {
  test("restored native readiness refuses " + scenario, async () => {
    await withMuxFixture(scenario, async fixture => {
      assert.equal(typeof waitForRestoredTerminalReady, "function");
    const result = await waitForRestoredTerminalReady({
        baseURL: fixture.baseURL, sessionId: "fixture", restoredState: restoredFixtureState,
        spec: nativeReadySpec, timeoutMs: 150,
      });
      assert.equal(result.passed, false);
      assert.equal(fixture.inputReceived, false);
      assert.equal(result.evidence.transport, "websocket");
      assert.ok(result.reason);
    });
  });
}

test("restored native readiness decodes UTF-8 split across actual terminal frames", async () => {
  await withMuxFixture("ready-utf8", async fixture => {
    const result = await waitForRestoredTerminalReady({
      baseURL: fixture.baseURL, sessionId: "fixture", restoredState: restoredFixtureState,
      spec: nativeReadySpec, timeoutMs: 300,
    });
    assert.equal(result.passed, true, JSON.stringify(result));
  });
});

test("missing restored native cue blocks the lifecycle before any continuation send", async () => {
  await withMuxFixture("lifecycle-ready-blocked", async fixture => {
    const result = await auditAgent({
      baseURL: fixture.baseURL, agent: "fixture", projectId: "project", timeoutMs: 300,
      diagnosticAfterNativeProof: true, restoredReadySpec: nativeReadySpec,
      localResult: { agent: "fixture", gates: ["local_binary", "local_version", "local_integration", "local_session_spawn"].map(name => ({ name, status: "PASS" })) },
    });
    const gate = name => result.gates.find(item => item.name === name);
    assert.equal(gate("post_restore_terminal_ready")?.status, "BLOCKED", JSON.stringify(result));
    for (const name of ["post_restore_message", "history_file_continuity", "system_prompt_restore"]) assert.equal(gate(name)?.status, "NOT_RUN");
    assert.equal(fixture.messages.some(message => message.startsWith("Update the existing")), false);
    assert.equal(gate("authentication").status, "BLOCKED");
  });
});

test("an active session with a proof file but no settled completion cannot pass activity status", async () => {
  await withMuxFixture("lifecycle-activity-active", async fixture => {
    const result = await auditAgent({
      baseURL: fixture.baseURL, agent: "fixture", projectId: "project", timeoutMs: 100,
      diagnosticAfterNativeProof: true,
      localResult: { agent: "fixture", gates: ["local_binary", "local_version", "local_integration", "local_session_spawn"].map(name => ({ name, status: "PASS" })) },
    });
    const activity = result.gates.find(item => item.name === "activity_status");
    assert.equal(activity.evidence.response.session.activity.state, "active");
    assert.equal(activity.status, "FAIL", JSON.stringify(activity));
  });
});

test("mux cancellation keeps the default confirmation window and accepts a bounded 60s contract", async () => {
  for (const [settleTimeoutMs, expected] of [[undefined, 5000], [60000, 60000]]) {
    await withMuxFixture("escape", async fixture => {
      const result = await interruptActiveTurn({ baseURL: fixture.baseURL, sessionId: "fixture",
        startResponse: { ok: true }, timeoutMs: 90000, interruptSpec: { input: "\x1b", ...(settleTimeoutMs === undefined ? {} : { settleTimeoutMs }) } });
      assert.equal(result.passed, true, JSON.stringify(result));
      assert.equal(result.confirmationWindowMs, expected);
      assert.equal(typeof result.websocket.settleDurationMs, "number");
    });
  }
});

for (const value of [0, -1, 60001, 1.5, "60000", null]) {
  test("mux cancellation refuses invalid settleTimeoutMs " + JSON.stringify(value), async () => {
    await withMuxFixture("escape", async fixture => {
      const result = await interruptActiveTurn({ baseURL: fixture.baseURL, sessionId: "fixture",
        startResponse: { ok: true }, timeoutMs: 500, interruptSpec: { input: "\x1b", settleTimeoutMs: value } });
      assert.equal(result.passed, false);
      assert.match(result.reason, /settleTimeoutMs.*integer.*1.*60000/);
      assert.equal(fixture.inputReceived, false);
      assert.equal(fixture.upgrades, 0);
    });
  });
}

test("mux cancellation fails when the configured window expires without settling", async () => {
  await withMuxFixture("never-settles", async fixture => {
    const result = await interruptActiveTurn({ baseURL: fixture.baseURL, sessionId: "fixture",
      startResponse: { ok: true }, timeoutMs: 500, interruptSpec: { input: "\x1b", settleTimeoutMs: 80 } });
    assert.equal(result.passed, false);
    assert.equal(result.confirmationWindowMs, 80);
    assert.equal(result.websocket.timedOut, true);
    assert.equal(result.settledState.response.session.activity.state, "active");
  });
});

test("mux cancellation bounds the configured window by the run timeout", async () => {
  await withMuxFixture("never-settles", async fixture => {
    const result = await interruptActiveTurn({ baseURL: fixture.baseURL, sessionId: "fixture",
      startResponse: { ok: true }, timeoutMs: 120, interruptSpec: { input: "\x1b", settleTimeoutMs: 60000 } });
    assert.equal(result.passed, false);
    assert.equal(result.confirmationWindowMs, 120);
    assert.equal(result.websocket.timedOut, true);
  });
});

test("mux cancellation preserves active evidence after 5s before a later bounded settle", async () => {
  await withMuxFixture("delayed-settle", async fixture => {
    const result = await interruptActiveTurn({ baseURL: fixture.baseURL, sessionId: "fixture",
      startResponse: { ok: true }, timeoutMs: 8000, interruptSpec: { input: "\x1b", settleTimeoutMs: 6500 } });
    assert.equal(result.passed, true, JSON.stringify(result));
    assert.ok(result.websocket.settleDurationMs >= 5200);
    assert.ok(result.websocket.unsettledAfterDefaultWindow.observedAfterInputMs >= 5000);
    assert.equal(result.websocket.unsettledAfterDefaultWindow.state.response.session.activity.state, "active");
    assert.equal(result.websocket.settledWithinDefaultWindow, false);
    assert.equal(result.settledState.response.session.activity.state, "idle");
  });
});

test("restored native readiness rejects an absent or blank generation before mux attachment", async () => {
  for (const generation of [undefined, "", "   "]) {
    await withMuxFixture("ready-success", async fixture => {
      const result = await waitForRestoredTerminalReady({
        baseURL: fixture.baseURL, sessionId: "fixture",
        restoredState: { ...restoredFixtureState, response: { session: { ...restoredFixtureState.response.session, terminalGeneration: generation } } },
        spec: nativeReadySpec, timeoutMs: 300,
      });
      assert.equal(result.passed, false);
      assert.match(result.reason, /generation/);
      assert.equal(fixture.upgrades, 0);
      assert.equal(fixture.inputReceived, false);
    });
  }
});


test("missing native contracts skip cancellation and restored input without inventing routes", async () => {
  await withMuxFixture("lifecycle-ready-blocked", async fixture => {
    const result = await auditAgent({
      baseURL: fixture.baseURL, agent: "fixture", projectId: "project", timeoutMs: 300,
      diagnosticAfterNativeProof: true,
      localResult: { agent: "fixture", gates: ["local_binary", "local_version", "local_integration", "local_session_spawn"].map(name => ({ name, status: "PASS" })) },
    });
    const gate = name => result.gates.find(item => item.name === name);
    assert.equal(gate("cancellation")?.status, "NOT_RUN");
    assert.equal(fixture.messages.some(message => message.startsWith("For cancellation testing")), false);
    assert.equal(gate("post_restore_terminal_ready")?.status, "NOT_RUN");
    for (const name of ["post_restore_message", "history_file_continuity", "system_prompt_restore"]) assert.equal(gate(name)?.status, "NOT_RUN");
    assert.equal(fixture.messages.some(message => message.startsWith("Update the existing")), false);
    assert.equal(fixture.upgrades, 0);
  });
});


test("dropped project config update fails instruction refresh before restore", async () => {
  await withMuxFixture("lifecycle-drops-refreshed-instructions", async fixture => {
    const result = await auditAgent({
      baseURL: fixture.baseURL, agent: "fixture", projectId: "project", timeoutMs: 300,
      diagnosticAfterNativeProof: true,
      localResult: { agent: "fixture", gates: ["local_binary", "local_version", "local_integration", "local_session_spawn"].map(name => ({ name, status: "PASS" })) },
    });
    const gate = name => result.gates.find(item => item.name === name);
    assert.equal(gate("system_prompt_restore")?.status, "FAIL");
    assert.match(gate("system_prompt_restore")?.reason || "", /fresh.*instruction|instruction.*refresh/);
    assert.equal(gate("native_restore")?.status, "NOT_RUN");
    assert.equal(fixture.messages.some(message => message.startsWith("Update the existing")), false);
  });
});


test("restored provider history cannot substitute for newly configured standing instructions", async () => {
  await withMuxFixture("lifecycle-retains-old-instructions", async fixture => {
    const result = await auditAgent({
      baseURL: fixture.baseURL, agent: "fixture", projectId: "project", timeoutMs: 300,
      diagnosticAfterNativeProof: true, restoredReadySpec: nativeReadySpec,
      scenario: { initialToken: "initial", hiddenInitialToken: "initial-hidden", agentsToken: "project-agents", secondToken: "second", historyToken: "history" },
      localResult: { agent: "fixture", gates: ["local_binary", "local_version", "local_integration", "local_session_spawn"].map(name => ({ name, status: "PASS" })) },
    });
    const gate = name => result.gates.find(item => item.name === name);
    assert.match(fixture.projectConfig.agentRules, /restoreHiddenToken use exactly: HIDDEN_RESTORE_/);
    assert.equal(gate("post_restore_terminal_ready")?.status, "PASS");
    assert.equal(fixture.messages.some(message => message.startsWith("Update the existing")), true);
    assert.equal(gate("history_file_continuity")?.status, "PASS", JSON.stringify(gate("history_file_continuity")));
    assert.equal(gate("system_prompt_restore")?.status, "FAIL");
    assert.equal(gate("post_restore_message")?.status, "FAIL");
  });
});

async function auditCleanup(fixture, options = {}) {
  return auditAgent({
    baseURL: fixture.baseURL, agent: "fixture", projectId: "project", timeoutMs: options.timeoutMs || 150,
    dataDir: options.dataDir || "",
    diagnosticAfterNativeProof: true,
    interruptSpec: { input: "\x1b", postCancelCleanup: { input: "\x03", draftPattern: "(?:^|\\n)> {prompt}\\n─+\\n[ \\t]*\\[model\\][^\\n]*\\n*$", ...options.cleanupSpec }, ...options.interruptSpec },
    restoredReadySpec: options.withoutReadySpec ? null : options.readySpec || nativeReadySpec,
    localResult: { agent: "fixture", gates: ["local_binary", "local_version", "local_integration", "local_session_spawn"].map(name => ({ name, status: "PASS" })) },
  });
}

test("post-cancel cleanup clears the audit draft exactly once before lifecycle kill", async () => {
  await withMuxFixture("lifecycle-cleanup-success", async fixture => {
    const result = await auditCleanup(fixture);
    const cleanup = result.gates.find(item => item.name === "post_cancel_draft_cleanup");
    assert.equal(cleanup?.status, "PASS", JSON.stringify(result));
    assert.equal(fixture.cleanupInputs, 1);
    assert.equal(fixture.killRequests, 1);
    assert.equal(cleanup.evidence.inputBytes, 1);
    assert.equal(cleanup.evidence.inputSent, true);
    assert.equal(cleanup.evidence.matchedPatterns, 1);
    assert.ok(cleanup.evidence.outputFramesAfterInput > 0);
    assert.equal(result.gates.find(item => item.name === "cancellation").status, "PASS");
  });
});

for (const scenario of ["foreign", "handle", "generation", "reactivated", "newer-user", "newer-during-cancel", "never-settles", "foreign-draft"]) {
  test("post-cancel cleanup refuses unsafe " + scenario + " without clearing or lifecycle kill", async () => {
    await withMuxFixture("lifecycle-cleanup-" + scenario, async fixture => {
      const result = await auditCleanup(fixture);
      assert.equal(result.gates.find(item => item.name === "post_cancel_draft_cleanup")?.status, "BLOCKED", JSON.stringify(result));
      assert.equal(fixture.cleanupInputs, 0);
      assert.equal(fixture.killRequests, 0);
      assert.equal(result.gates.find(item => item.name === "termination")?.status, "NOT_RUN");
      assert.equal(result.gates.find(item => item.name === "native_restore")?.status, "NOT_RUN");
      if (scenario === "never-settles") assert.equal(result.gates.find(item => item.name === "cancellation")?.status, "FAIL");
    });
  });
}

for (const scenario of ["draft-remains", "stale-cue", "foreign-output"]) {
  test("post-cancel cleanup never retries or kills when empty composer is unproven: " + scenario, async () => {
    await withMuxFixture("lifecycle-cleanup-" + scenario, async fixture => {
      const result = await auditCleanup(fixture);
      assert.equal(result.gates.find(item => item.name === "post_cancel_draft_cleanup")?.status, "BLOCKED", JSON.stringify(result));
      assert.equal(fixture.cleanupInputs, 1);
      assert.equal(fixture.killRequests, 0);
      assert.equal(result.gates.find(item => item.name === "cancellation")?.status, "PASS");
    });
  });
}

test("post-cancel cleanup skips input when the audit composer is already empty", async () => {
  await withMuxFixture("lifecycle-cleanup-already-empty", async fixture => {
    const result = await auditCleanup(fixture);
    const cleanup = result.gates.find(item => item.name === "post_cancel_draft_cleanup");
    assert.equal(cleanup?.status, "PASS", JSON.stringify(result));
    assert.equal(fixture.cleanupInputs, 0);
    assert.equal(cleanup.evidence.inputSent, false);
    assert.equal(fixture.killRequests, 1);
  });
});

for (const input of ["", "\x03\x03", "\x1b", "clear", null]) {
  test("post-cancel cleanup rejects any input other than one Ctrl+C: " + JSON.stringify(input), async () => {
    await withMuxFixture("lifecycle-cleanup-success", async fixture => {
      const result = await auditCleanup(fixture, { cleanupSpec: { input } });
      assert.equal(result.gates.find(item => item.name === "post_cancel_draft_cleanup")?.status, "BLOCKED");
      assert.equal(fixture.cleanupInputs, 0);
      assert.equal(fixture.killRequests, 0);
    });
  });
}

test("post-cancel cleanup requires the configured empty-composer contract before sending", async () => {
  await withMuxFixture("lifecycle-cleanup-success", async fixture => {
    const result = await auditCleanup(fixture, { withoutReadySpec: true });
    assert.equal(result.gates.find(item => item.name === "post_cancel_draft_cleanup")?.status, "BLOCKED");
    assert.equal(fixture.cleanupInputs, 0);
    assert.equal(fixture.killRequests, 0);
  });
});

for (const draftPattern of [undefined, "", "Long draft", "{prompt}["]) {
  test("post-cancel cleanup requires a valid draft cue tied to the audit prompt: " + JSON.stringify(draftPattern), async () => {
    await withMuxFixture("lifecycle-cleanup-success", async fixture => {
      const result = await auditCleanup(fixture, { cleanupSpec: { draftPattern } });
      assert.equal(result.gates.find(item => item.name === "post_cancel_draft_cleanup")?.status, "BLOCKED");
      assert.equal(fixture.cleanupInputs, 0);
      assert.equal(fixture.killRequests, 0);
    });
  });
}

for (const cleanupSpec of [
  { draftPattern: "{prompt}" },
  { draftPattern: "^> {prompt}$", flags: "m" },
  { draftPattern: "(?:{prompt}|)" },
  { draftPattern: "(?:{prompt}|> unrelated user draft\\n─+\\n[ \\t]*\\[model\\][^\\n]*\\n*$)" },
]) {
  test("post-cancel cleanup never treats a history-only draft pattern as current composer: " + JSON.stringify(cleanupSpec), async () => {
    await withMuxFixture("lifecycle-cleanup-foreign-draft", async fixture => {
      const result = await auditCleanup(fixture, { cleanupSpec });
      assert.equal(result.gates.find(item => item.name === "post_cancel_draft_cleanup")?.status, "BLOCKED");
      assert.equal(fixture.cleanupInputs, 0);
      assert.equal(fixture.killRequests, 0);
    });
  });
}

for (const scenario of ["marker-absent", "marker-null", "marker-blank"]) {
  test("post-cancel cleanup refuses missing latest-user metadata: " + scenario, async () => {
    await withMuxFixture("lifecycle-cleanup-" + scenario, async fixture => {
      const result = await auditCleanup(fixture);
      assert.equal(result.gates.find(item => item.name === "post_cancel_draft_cleanup")?.status, "BLOCKED");
      assert.equal(fixture.cleanupInputs, 0);
      assert.equal(fixture.killRequests, 0);
    });
  });
}

test("post-cancel cleanup requires fresh nonempty output even with a nullable readiness pattern", async () => {
  await withMuxFixture("lifecycle-cleanup-stale-cue", async fixture => {
    const result = await auditCleanup(fixture, {
      readySpec: { patterns: ["(?:" + nativeReadySpec.patterns[0] + "|^$)"] },
    });
    assert.equal(result.gates.find(item => item.name === "post_cancel_draft_cleanup")?.status, "BLOCKED");
    assert.equal(fixture.killRequests, 0);
    assert.equal(fixture.cleanupInputs, 0);
  });
});

test("post-cancel cleanup does not accept a historical empty cue with multiline flags", async () => {
  await withMuxFixture("lifecycle-cleanup-stale-cue", async fixture => {
    const result = await auditCleanup(fixture, { readySpec: { patterns: ["^>[ \\t]*$"], flags: "m" } });
    assert.equal(result.gates.find(item => item.name === "post_cancel_draft_cleanup")?.status, "BLOCKED");
    assert.equal(fixture.cleanupInputs, 1);
    assert.equal(fixture.killRequests, 0);
  });
});

const persistenceTemplate = "agents/example/{sessionId}/drafts/{workspaceSha256:24}/{nativeSessionIdSha256:24}.json";

// Real files and mux transport; only the read-only sqlite CLI boundary is a
// deterministic executable, so these fixtures need no database dependency.
async function withPersistenceFixture(options, run) {
  const root = mkdtempSync(join(tmpdir(), "ao-draft-witness-"));
  const dataDir = join(root, "data");
  const bin = join(root, "bin");
  mkdirSync(dataDir); mkdirSync(bin);
  const nativeID = options.missingNativeID ? "" : "native-owned-fixture";
  const workspacePath = join(root, "workspace-alias");
  mkdirSync(join(root, "workspace-real"));
  symlinkSync(join(root, "workspace-real"), workspacePath, "dir");
  writeFileSync(join(bin, "sqlite3"), "#!" + process.execPath + "\n" +
    "if (process.argv[2] !== '-readonly' || process.argv[3] !== " + JSON.stringify(join(dataDir, "ao.db")) + ") process.exit(2);\n" +
    "console.log(" + JSON.stringify(nativeID) + ");\n", { mode: 0o700 });
  const hash24 = value => createHash("sha256").update(value).digest("hex").slice(0, 24);
  const relativePath = "agents/example/fixture/drafts/" + hash24(resolve(workspacePath)) + "/" + hash24(nativeID.trim()) + ".json";
  const witnessPath = join(dataDir, relativePath);
  mkdirSync(dirname(witnessPath), { recursive: true });
  if (options.present !== false) writeFileSync(witnessPath, "saved native draft");
  if (options.symlinkLeaf) {
    rmSync(witnessPath);
    writeFileSync(join(root, "foreign"), "must not inspect contents");
    symlinkSync(join(root, "foreign"), witnessPath);
  }
  if (options.directoryLeaf) { rmSync(witnessPath); mkdirSync(witnessPath); }
  if (options.symlinkParent) {
    const parent = dirname(witnessPath);
    rmSync(parent, { recursive: true });
    mkdirSync(join(root, "foreign-parent"));
    symlinkSync(join(root, "foreign-parent"), parent, "dir");
  }
  if (options.missingParent) rmSync(dirname(witnessPath), { recursive: true });
  const previousPath = process.env.PATH;
  process.env.PATH = bin + delimiter + previousPath;
  const hooks = { workspacePath };
  let removedAt = 0;
  let witnessAtKill = null;
  hooks.onKill = () => { witnessAtKill = { present: existsSync(witnessPath), killedAt: Date.now(), removedAt }; };
  hooks.onCleanupInput = ({ emit }) => options.removeAfterInput ? setTimeout(() => {
    rmSync(witnessPath);
    removedAt = Date.now();
    if (options.newerUser) hooks.lastUserMessageAt = "2026-10-10T03:00:00Z";
    if (options.foreignDraft) emit("> foreign draft after clear\n────────\n  [model] | native session\n");
  }, 80) : undefined;
  try {
    await withMuxFixture("lifecycle-cleanup-" + (options.alreadyEmpty ? "already-empty" : "success"), async fixture => {
      const result = await auditCleanup(fixture, { dataDir, timeoutMs: 400,
        cleanupSpec: { persistence: { path: options.path ?? persistenceTemplate } } });
      await run({ fixture, result, witnessPath, removedAt, relativePath, witnessAtKill });
    }, hooks);
  } finally {
    process.env.PATH = previousPath;
    rmSync(root, { recursive: true, force: true });
  }
}

test("persistence witness waits for actual draft removal after empty UI before lifecycle kill", async () => {
  await withPersistenceFixture({ removeAfterInput: true }, async ({ fixture, result, witnessPath, removedAt, witnessAtKill }) => {
    const cleanup = result.gates.find(item => item.name === "post_cancel_draft_cleanup");
    assert.equal(cleanup?.status, "PASS", JSON.stringify(result));
    assert.equal(fixture.cleanupInputs, 1);
    assert.ok(removedAt > 0, "kill must wait for actual removal, not just empty UI");
    assert.equal(existsSync(witnessPath), false);
    assert.equal(witnessAtKill?.present, false, "recovery file must be absent when kill arrives");
    assert.ok(witnessAtKill?.removedAt > 0 && witnessAtKill.killedAt >= witnessAtKill.removedAt, "removal must precede kill");
    assert.equal(fixture.killRequests, 1);
    assert.equal(cleanup.evidence.persistence.beforeInput.state, "present");
    assert.equal(cleanup.evidence.persistence.afterEmpty.state, "absent");
  });
});

for (const [name, options, expectedInputs] of [
  ["persisted draft remains", {}, 1],
  ["wrong absent leaf", { present: false }, 0],
  ["leaf symlink", { symlinkLeaf: true }, 0],
  ["parent symlink", { symlinkParent: true }, 0],
  ["nonregular leaf", { directoryLeaf: true }, 0],
  ["missing native identity", { missingNativeID: true }, 0],
  ["new user during removal", { removeAfterInput: true, newerUser: true }, 1],
  ["foreign composer during removal", { removeAfterInput: true, foreignDraft: true }, 1],
  ["traversal path", { path: "../" + persistenceTemplate }, 0],
  ["absolute path", { path: "/tmp/" + persistenceTemplate }, 0],
  ["unbound path", { path: "some-missing-file.json" }, 0],
  ["already empty but persisted", { alreadyEmpty: true }, 0],
  ["already empty with missing parent", { alreadyEmpty: true, missingParent: true }, 0],
]) {
  test("persistence witness blocks " + name + " without lifecycle kill", async () => {
    await withPersistenceFixture(options, async ({ fixture, result }) => {
      assert.equal(result.gates.find(item => item.name === "post_cancel_draft_cleanup")?.status, "BLOCKED", JSON.stringify(result));
      assert.equal(fixture.cleanupInputs, expectedInputs);
      assert.equal(fixture.killRequests, 0);
      assert.equal(result.gates.find(item => item.name === "termination")?.status, "NOT_RUN");
    });
  });
}

test("persistence witness allows already-empty composer only with absent file and no input", async () => {
  await withPersistenceFixture({ alreadyEmpty: true, present: false }, async ({ fixture, result }) => {
    const cleanup = result.gates.find(item => item.name === "post_cancel_draft_cleanup");
    assert.equal(cleanup?.status, "PASS", JSON.stringify(result));
    assert.equal(fixture.cleanupInputs, 0);
    assert.equal(fixture.killRequests, 1);
    assert.equal(cleanup.evidence.persistence.afterEmpty.state, "absent");
  });
});

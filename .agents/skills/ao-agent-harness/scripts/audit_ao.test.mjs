import assert from "node:assert/strict";
import { chmod, mkdtemp, readFile, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

import * as auditModule from "./audit_ao.mjs";

const { auditAgent, discoverAgents, overallStatus, setupProject, startManagedDaemon } = auditModule;

async function withFetch(handler, run) {
  const original = globalThis.fetch;
  globalThis.fetch = async (url, options = {}) => handler(new URL(url), options);
  try {
    await run("http://ao.test");
  } finally {
    globalThis.fetch = original;
  }
}

function json(status, body, requestId = "req-test") {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json", "x-request-id": requestId },
  });
}

test("discoverAgents uses AO supported inventory instead of a hardcoded list", async () => {
  await withFetch((url) => {
    assert.equal(url.pathname, "/api/v1/agents");
    return json(200, { supported: [{ id: "zeta" }, { id: "alpha" }], installed: [], authorized: [] });
  }, async (baseURL) => {
    assert.deepEqual(await discoverAgents({ baseURL }), ["alpha", "zeta"]);
  });
});

test("auditAgent fails closed when auth is configured but unverified", async () => {
  await withFetch(async (url, options) => {
    if (url.pathname === "/api/v1/agents/codex/probe") {
      assert.equal(options.method, "POST");
      return json(200, { agent: { id: "codex", authStatus: "configured" }, supported: true, installed: true });
    }
    if (url.pathname === "/api/v1/agents/readiness/ensure") {
      assert.deepEqual(JSON.parse(options.body), { agentIds: ["codex"], purpose: "launch" });
      return json(200, { agents: [{ id: "codex", installation: { state: "installed", freshness: "fresh" }, authentication: { state: "configured", freshness: "fresh" }, effectiveReadiness: "unknown" }] });
    }
    return json(404, { code: "NOT_FOUND" });
  }, async (baseURL) => {
    const result = await auditAgent({ baseURL, agent: "codex", projectId: "audit", live: false });
    assert.equal(result.status, "BLOCKED");
    assert.equal(result.failedGate, "authentication");
  });
});

test("auditAgent does not spawn unless registry, install, auth, and readiness pass", async () => {
  let spawnCalls = 0;
  await withFetch(async (url) => {
    if (url.pathname === "/api/v1/agents/missing/probe") {
      return json(200, { agent: { id: "missing" }, supported: false, installed: false });
    }
    if (url.pathname === "/api/v1/agents/readiness/ensure") {
      return json(200, { agents: [] });
    }
    if (url.pathname === "/api/v1/sessions") spawnCalls += 1;
    return json(404, { code: "NOT_FOUND" });
  }, async (baseURL) => {
    const result = await auditAgent({ baseURL, agent: "missing", projectId: "audit", live: true });
    assert.equal(result.status, "FAIL");
    assert.equal(result.failedGate, "registered");
    assert.equal(spawnCalls, 0);
  });
});

test("overallStatus requires every mandatory gate to pass", () => {
  assert.equal(overallStatus([{ status: "PASS" }, { status: "PASS" }]), "PASS");
  assert.equal(overallStatus([{ status: "PASS" }, { status: "BLOCKED" }]), "BLOCKED");
  assert.equal(overallStatus([{ status: "PASS" }, { status: "FAIL" }]), "FAIL");
  assert.equal(overallStatus([]), "NOT_RUN");
});

test("startManagedDaemon launches an isolated AO daemon before API testing", async () => {
  const root = await mkdtemp(join(tmpdir(), "ao-audit-daemon-test-"));
  const fake = join(root, "fake-ao.mjs");
  await writeFile(fake, `#!/usr/bin/env node
import { writeFile } from "node:fs/promises";
if (process.argv[2] !== "daemon") process.exit(2);
await writeFile(process.env.AO_RUN_FILE, JSON.stringify({ pid: process.pid, port: Number(process.env.AO_PORT) }));
setInterval(() => {}, 1000);
`, "utf8");
  await chmod(fake, 0o755);

  const daemon = await startManagedDaemon({
    root,
    aoCommand: [process.execPath, fake],
    port: 43123,
    readinessCheck: async (baseURL) => baseURL === "http://127.0.0.1:43123",
    timeoutMs: 5_000,
  });
  try {
    assert.equal(daemon.baseURL, "http://127.0.0.1:43123");
    assert.equal(daemon.dataDir, join(root, "data"));
    assert.equal(daemon.runFile, join(root, "running.json"));
    const runInfo = JSON.parse(await readFile(daemon.runFile, "utf8"));
    assert.equal(runInfo.port, 43123);
    assert.equal(runInfo.pid, daemon.pid);
    assert.equal(daemon.readiness.ok, true);
    assert.equal(typeof daemon.readiness.checkedAt, "string");
  } finally {
    const stoppingAt = Date.now();
    await daemon.stop();
    assert.ok(Date.now() - stoppingAt < 2_000, "managed daemon stop should not retain its fallback timer");
  }
});

test("setupProject pins the disposable repository default branch", async () => {
  const root = await mkdtemp(join(tmpdir(), "ao-audit-project-test-"));
  await withFetch(async (url, options) => {
    assert.equal(url.pathname, "/api/v1/projects");
    const body = JSON.parse(options.body);
    assert.equal(body.config.defaultBranch, "main");
    assert.equal(body.config.agentRules.includes("hidden-restore-token"), false);
    return json(201, { project: { id: body.projectId } });
  }, async (baseURL) => {
    const result = await setupProject({
      baseURL,
      root,
      agent: "sample-agent",
      scenario: {
        agentsToken: "agents-token",
        hiddenInitialToken: "hidden-initial-token",
        hiddenRestoreToken: "hidden-restore-token",
      },
      timeoutMs: 5_000,
    });
    assert.equal(result.ok, true);
  });
});

test("auditLocalAgent proves binary, version, integration, direct session, and model listing", async () => {
  assert.equal(typeof auditModule.auditLocalAgent, "function");
  const root = await mkdtemp(join(tmpdir(), "ao-audit-local-test-"));
  const fake = join(root, "fake-agent.mjs");
  await writeFile(fake, `#!/usr/bin/env node
import { writeFile } from "node:fs/promises";
const [command, ...args] = process.argv.slice(2);
if (command === "--version") console.log("fake-agent 1.2.3");
else if (command === "integration") console.log("integration-ok");
else if (command === "models") console.log(JSON.stringify({ models: [{ id: "model-a" }, { id: "model-b" }] }));
else if (command === "session") {
  const prompt = args.join(" ");
  const token = prompt.match(/LOCAL_SESSION_[a-f0-9]+/)?.[0];
  await writeFile("LOCAL_AUDIT_PROOF.json", JSON.stringify({ token, cwd: process.cwd() }));
} else process.exitCode = 2;
`, "utf8");
  await chmod(fake, 0o755);

  const result = await auditModule.auditLocalAgent({
    agent: "fake-agent",
    root,
    timeoutMs: 5_000,
    contract: {
      binary: fake,
      versionArgs: ["--version"],
      integration: { args: ["integration"], expect: "integration-ok" },
      session: { args: ["session", "{prompt}"], proofFile: "LOCAL_AUDIT_PROOF.json" },
      models: { args: ["models"], format: "json" },
    },
  });

  assert.equal(result.status, "PASS");
  assert.deepEqual(result.gates.map(({ name, status }) => ({ name, status })), [
    { name: "local_binary", status: "PASS" },
    { name: "local_version", status: "PASS" },
    { name: "local_integration", status: "PASS" },
    { name: "local_session_spawn", status: "PASS" },
    { name: "local_models_list", status: "PASS" },
  ]);
  assert.deepEqual(result.models, ["model-a", "model-b"]);
  const integrationEvidence = result.gates.find((item) => item.name === "local_integration").evidence;
  assert.equal(integrationEvidence.expectedObserved, true);
  assert.equal(typeof integrationEvidence.stdoutSha256, "string");
  assert.equal("stdout" in integrationEvidence, false);
  assert.equal(typeof integrationEvidence.startedAt, "string");
  assert.equal(typeof integrationEvidence.durationMs, "number");
  assert.equal(integrationEvidence.timedOut, false);
  assert.equal(typeof result.contractSha256, "string");
  const sessionEvidence = result.gates.find((item) => item.name === "local_session_spawn").evidence;
  assert.deepEqual(sessionEvidence.arguments, ["session", "{prompt}"]);
  assert.equal(typeof sessionEvidence.promptSha256, "string");
});

test("auditLocalAgent reports every local gate NOT_RUN when no contract is available", async () => {
  assert.equal(typeof auditModule.auditLocalAgent, "function");
  const result = await auditModule.auditLocalAgent({ agent: "unknown", contract: null });
  assert.equal(result.status, "NOT_RUN");
  assert.equal(result.gates.length, 5);
  assert.ok(result.gates.every((item) => item.status === "NOT_RUN"));
});

test("auditLocalAgent does not start a direct provider session when live mode is disabled", async () => {
  const root = await mkdtemp(join(tmpdir(), "ao-audit-no-live-test-"));
  const marker = join(root, "session-started");
  const result = await auditModule.auditLocalAgent({
    agent: "node",
    root,
    live: false,
    contract: {
      binary: process.execPath,
      session: { args: ["-e", `require("fs").writeFileSync(${JSON.stringify(marker)}, "started")`] },
    },
  });
  assert.equal(result.gates.find((item) => item.name === "local_session_spawn").status, "NOT_RUN");
  await assert.rejects(readFile(marker, "utf8"), { code: "ENOENT" });
});

test("auditAgent requires a working AO model catalog API", async () => {
  await withFetch(async (url, options) => {
    if (url.pathname === "/api/v1/agents/codex/probe") {
      return json(200, { agent: { id: "codex", authStatus: "authorized" }, supported: true, installed: true });
    }
    if (url.pathname === "/api/v1/agents/readiness/ensure") {
      return json(200, { agents: [{ id: "codex", installation: { state: "installed", freshness: "fresh" }, authentication: { state: "authorized", freshness: "fresh" }, effectiveReadiness: "ready" }] });
    }
    if (url.pathname === "/api/v1/agents/codex/models") {
      assert.equal(options.method, "GET");
      return json(200, { models: [{ id: "gpt-a" }, { id: "gpt-b" }] });
    }
    return json(404, { code: "NOT_FOUND" });
  }, async (baseURL) => {
    const result = await auditAgent({ baseURL, agent: "codex", projectId: "audit", live: false });
    assert.equal(result.gates.find((item) => item.name === "ao_models_api")?.status, "PASS");
    assert.deepEqual(result.aoModels, ["gpt-a", "gpt-b"]);
  });
});

test("auditAgent fails the AO model gate when the API returns no models", async () => {
  await withFetch(async (url) => {
    if (url.pathname === "/api/v1/agents/codex/probe") {
      return json(200, { agent: { id: "codex", authStatus: "authorized" }, supported: true, installed: true });
    }
    if (url.pathname === "/api/v1/agents/readiness/ensure") {
      return json(200, { agents: [{ id: "codex", installation: { state: "installed", freshness: "fresh" }, authentication: { state: "authorized", freshness: "fresh" }, effectiveReadiness: "ready" }] });
    }
    if (url.pathname === "/api/v1/agents/codex/models") return json(200, { models: [] });
    return json(404, { code: "NOT_FOUND" });
  }, async (baseURL) => {
    const result = await auditAgent({ baseURL, agent: "codex", projectId: "audit", live: false });
    assert.equal(result.status, "FAIL");
    assert.equal(result.failedGate, "ao_models_api");
  });
});

test("auditAgent does not mistake unrelated AO object IDs for model IDs", async () => {
  await withFetch(async (url) => {
    if (url.pathname === "/api/v1/agents/codex/probe") {
      return json(200, { agent: { id: "codex", authStatus: "authorized" }, supported: true, installed: true });
    }
    if (url.pathname === "/api/v1/agents/readiness/ensure") {
      return json(200, { agents: [{ id: "codex", installation: { state: "installed", freshness: "fresh" }, authentication: { state: "authorized", freshness: "fresh" }, effectiveReadiness: "ready" }] });
    }
    if (url.pathname === "/api/v1/agents/codex/models") return json(200, { id: "codex", status: "ready" });
    return json(404, { code: "NOT_FOUND" });
  }, async (baseURL) => {
    const result = await auditAgent({ baseURL, agent: "codex", projectId: "audit", live: false });
    assert.equal(result.gates.find((item) => item.name === "ao_models_api")?.status, "FAIL");
  });
});

test("auditAgent rejects scalar values where a model catalog array is required", async () => {
  await withFetch(async (url) => {
    if (url.pathname === "/api/v1/agents/codex/probe") {
      return json(200, { agent: { id: "codex", authStatus: "authorized" }, supported: true, installed: true });
    }
    if (url.pathname === "/api/v1/agents/readiness/ensure") {
      return json(200, { agents: [{ id: "codex", installation: { state: "installed", freshness: "fresh" }, authentication: { state: "authorized", freshness: "fresh" }, effectiveReadiness: "ready" }] });
    }
    if (url.pathname === "/api/v1/agents/codex/models") return json(200, { models: "temporarily unavailable" });
    return json(404, { code: "NOT_FOUND" });
  }, async (baseURL) => {
    const result = await auditAgent({ baseURL, agent: "codex", projectId: "audit", live: false });
    assert.equal(result.gates.find((item) => item.name === "ao_models_api")?.status, "FAIL");
  });
});

test("loadLocalContracts reads a versioned per-agent probe contract", async () => {
  assert.equal(typeof auditModule.loadLocalContracts, "function");
  const root = await mkdtemp(join(tmpdir(), "ao-audit-contract-test-"));
  const path = join(root, "local-contract.json");
  await writeFile(path, JSON.stringify({ schemaVersion: 1, agents: { codex: { binary: "codex" } } }));
  const contracts = await auditModule.loadLocalContracts(path);
  assert.deepEqual(contracts, { codex: { binary: "codex" } });
});

test("mergeAuditResults records local and AO model catalog consistency", () => {
  assert.equal(typeof auditModule.mergeAuditResults, "function");
  const local = {
    agent: "codex",
    status: "PASS",
    failedGate: "",
    models: ["gpt-a", "gpt-b"],
    gates: [{ name: "local_models_list", status: "PASS", reason: "listed" }],
  };
  const ao = {
    agent: "codex",
    status: "PASS",
    failedGate: "",
    aoModels: ["gpt-b", "gpt-c"],
    gates: [{ name: "ao_models_api", status: "PASS", reason: "listed" }],
  };
  const result = auditModule.mergeAuditResults(local, ao);
  assert.equal(result.status, "PASS");
  assert.deepEqual(result.modelCatalogComparison.shared, ["gpt-b"]);
  assert.equal(result.gates.at(-1).name, "model_catalog_consistency");
  assert.equal(result.gates.at(-1).status, "PASS");
});

test("mergeAuditResults fails when local and AO model IDs do not overlap", () => {
  const result = auditModule.mergeAuditResults(
    { agent: "codex", models: ["local-only"], gates: [] },
    { agent: "codex", aoModels: ["ao-only"], gates: [] },
  );
  assert.equal(result.status, "FAIL");
  assert.equal(result.failedGate, "model_catalog_consistency");
});

test("markdownReport separates Local CLI, AO, and cross-check gates", () => {
  assert.equal(typeof auditModule.markdownReport, "function");
  const markdown = auditModule.markdownReport({
    summary: { status: "PASS", total: 1, pass: 1, fail: 0, blocked: 0, notRun: 0 },
    startedAt: "start",
    finishedAt: "finish",
    disposableRoot: "/tmp/audit",
    daemon: { keptRunning: false },
    results: [{
      agent: "codex",
      status: "PASS",
      failedGate: "",
      gates: [
        { name: "local_binary", status: "PASS", reason: "found" },
        { name: "ao_models_api", status: "PASS", reason: "listed" },
        { name: "model_catalog_consistency", status: "PASS", reason: "overlap" },
      ],
    }],
  });
  assert.match(markdown, /### Local CLI/);
  assert.match(markdown, /### AO \/ AOS11/);
  assert.match(markdown, /### Cross-check/);
});

test("markdownReport does not claim cleanup succeeded when termination was unconfirmed", () => {
  const markdown = auditModule.markdownReport({
    summary: { status: "FAIL", total: 0, pass: 0, fail: 0, blocked: 0, notRun: 0 },
    startedAt: "start",
    finishedAt: "finish",
    disposableRoot: "/tmp/audit",
    daemon: { keptRunning: false },
    cleanup: [{ sessionId: "session-1", confirmedTerminated: false }],
    results: [],
  });
  assert.match(markdown, /cleanup termination was not confirmed/);
  assert.doesNotMatch(markdown, /sessions were terminated/);
});

test("parseArgs accepts the local CLI contract option", () => {
  assert.equal(typeof auditModule.parseArgs, "function");
  const options = auditModule.parseArgs(["--agent", "codex", "--local-contract", "/tmp/local.json"]);
  assert.deepEqual(options.agents, ["codex"]);
  assert.equal(options.localContract, "/tmp/local.json");
});

test("runCommand marks a SIGTERM-handled timeout as failed and bounds output", async () => {
  assert.equal(typeof auditModule.runCommand, "function");
  const root = await mkdtemp(join(tmpdir(), "ao-audit-timeout-test-"));
  const script = join(root, "timeout.mjs");
  await writeFile(script, `
process.on("SIGTERM", () => process.exit(0));
process.stdout.write("x".repeat(100000));
setInterval(() => {}, 1000);
`);
  const result = await auditModule.runCommand(process.execPath, [script], { timeoutMs: 500 });
  assert.equal(result.ok, false);
  assert.equal(result.timedOut, true);
  assert.ok(result.stdout.length <= 65_536);
  assert.equal(result.stdoutBytes, 100_000);
  assert.equal(result.stdoutTruncated, true);
  assert.ok(result.durationMs >= 0);
});

test("auditLocalAgent rejects proof files outside its disposable workspace", async () => {
  const result = await auditModule.auditLocalAgent({
    agent: "unsafe",
    contract: {
      binary: process.execPath,
      session: { args: ["--version"], proofFile: "../outside.json" },
    },
  });
  const sessionGate = result.gates.find((item) => item.name === "local_session_spawn");
  assert.equal(sessionGate.status, "FAIL");
  assert.match(sessionGate.reason, /safe relative path/);
});

test("auditLocalAgent rejects a symlinked proof file that resolves outside the workspace", async () => {
  const root = await mkdtemp(join(tmpdir(), "ao-audit-symlink-test-"));
  const fake = join(root, "fake-agent.mjs");
  await writeFile(fake, `#!/usr/bin/env node
import { symlink, writeFile } from "node:fs/promises";
const prompt = process.argv.slice(2).join(" ");
const token = prompt.match(/LOCAL_SESSION_[a-f0-9]+/)?.[0];
await writeFile("../outside.json", JSON.stringify({ token, cwd: process.cwd() }));
await symlink("../outside.json", "LOCAL_AUDIT_PROOF.json");
`);
  await chmod(fake, 0o755);
  const result = await auditModule.auditLocalAgent({
    agent: "symlink-agent",
    root,
    contract: { binary: fake, session: { args: ["{prompt}"] } },
  });
  const sessionGate = result.gates.find((item) => item.name === "local_session_spawn");
  assert.equal(sessionGate.status, "FAIL");
  assert.equal(sessionGate.evidence.proofContained, false);
});

test("interruptActiveTurn refuses to interrupt a session never observed active", async () => {
  assert.equal(typeof auditModule.interruptActiveTurn, "function");
  let interruptCalls = 0;
  await withFetch(async (url) => {
    if (url.pathname.endsWith("/interrupt")) interruptCalls += 1;
    if (url.pathname === "/api/v1/sessions/session-1") {
      return json(200, { session: { activity: { state: "idle" } } });
    }
    return json(200, { ok: true });
  }, async (baseURL) => {
    const result = await auditModule.interruptActiveTurn({
      baseURL,
      sessionId: "session-1",
      startResponse: { ok: true },
      timeoutMs: 20,
      interruptSpec: { input: "\x1b" },
    });
    assert.equal(result.passed, false);
    assert.equal(result.observedActive, false);
    assert.equal(interruptCalls, 0);
  });
});

test("interruptActiveTurn does not invent an HTTP endpoint without a native interrupt contract", async () => {
  let stateReads = 0;
  let interruptCalls = 0;
  await withFetch(async (url) => {
    if (url.pathname.endsWith("/interrupt")) {
      interruptCalls += 1;
      return json(200, { ok: true });
    }
    if (url.pathname === "/api/v1/sessions/session-1") {
      stateReads += 1;
      return json(200, { session: { activity: { state: stateReads === 1 ? "active" : "idle" } } });
    }
    return json(404, { code: "NOT_FOUND" });
  }, async (baseURL) => {
    const result = await auditModule.interruptActiveTurn({
      baseURL,
      sessionId: "session-1",
      startResponse: { ok: true },
      timeoutMs: 100,
    });
    assert.equal(result.passed, false);
    assert.equal(result.status, "NOT_RUN");
    assert.match(result.reason, /interrupt contract/);
    assert.equal(interruptCalls, 0);
    assert.equal(stateReads, 0);
  });
});

test("auditAgent rejects an AO model path that is not model-specific", async () => {
  let wrongPathCalls = 0;
  await withFetch(async (url) => {
    if (url.pathname === "/api/v1/agents/codex/probe") {
      return json(200, { agent: { id: "codex", authStatus: "authorized" }, supported: true, installed: true });
    }
    if (url.pathname === "/api/v1/agents/readiness/ensure") {
      return json(200, { agents: [{ id: "codex", installation: { state: "installed", freshness: "fresh" }, authentication: { state: "authorized", freshness: "fresh" }, effectiveReadiness: "ready" }] });
    }
    if (url.pathname === "/api/v1/agents") wrongPathCalls += 1;
    return json(200, { supported: [{ id: "codex" }] });
  }, async (baseURL) => {
    const result = await auditAgent({ baseURL, agent: "codex", projectId: "audit", live: false, modelsPath: "agents" });
    assert.equal(result.gates.find((item) => item.name === "ao_models_api")?.status, "FAIL");
    assert.equal(wrongPathCalls, 0);
  });
});

test("auditAgent rejects dot segments in an otherwise model-looking AO path", async () => {
  let modelCalls = 0;
  await withFetch(async (url) => {
    if (url.pathname === "/api/v1/agents/codex/probe") {
      return json(200, { agent: { id: "codex", authStatus: "authorized" }, supported: true, installed: true });
    }
    if (url.pathname === "/api/v1/agents/readiness/ensure") {
      return json(200, { agents: [{ id: "codex", installation: { state: "installed", freshness: "fresh" }, authentication: { state: "authorized", freshness: "fresh" }, effectiveReadiness: "ready" }] });
    }
    modelCalls += 1;
    return json(200, { models: [{ id: "gpt-a" }] });
  }, async (baseURL) => {
    const result = await auditAgent({ baseURL, agent: "codex", projectId: "audit", live: false, modelsPath: "agents/{agent}/models/../status" });
    assert.equal(result.gates.find((item) => item.name === "ao_models_api")?.status, "FAIL");
    assert.equal(modelCalls, 0);
  });
});


for (const outcome of ["stored", "drops-fresh-rules", "write-error"]) {
  test("restore instruction refresh preserves project config and verifies " + outcome, async () => {
    assert.equal(typeof auditModule.refreshRestoreInstructions, "function");
    const initialConfig = { defaultBranch: "main", env: { KEEP: "yes" }, agentRules: "initial rules", worker: { model: "fixture-model" } };
    let stored = structuredClone(initialConfig);
    let writes = 0;
    await withFetch(async (url, options) => {
      if (options.method === "PUT") {
        assert.equal(url.pathname, "/api/v1/projects/project/config");
        writes++;
        const body = JSON.parse(options.body);
        assert.deepEqual({ ...body.config, agentRules: initialConfig.agentRules }, initialConfig);
        assert.equal(body.config.agentRules.includes("fresh-after-kill"), true);
        if (outcome === "write-error") return json(503, { error: "unavailable" });
        if (outcome === "stored") stored = body.config;
        return json(200, { project: { id: "project", config: body.config } });
      }
      assert.equal(url.pathname, "/api/v1/projects/project");
      return json(200, { project: { id: "project", config: stored } });
    }, async baseURL => {
      const result = await auditModule.refreshRestoreInstructions({ baseURL, projectId: "project", restoreToken: "fresh-after-kill", timeoutMs: 500 });
      assert.equal(result.passed, outcome === "stored", JSON.stringify(result));
      assert.equal(writes, 1);
    });
  });
}

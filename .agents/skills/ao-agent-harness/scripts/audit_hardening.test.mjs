// Regressions for failures seen across harness PRs #5507–#6553: each test
// names the failure it prevents.
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

import { auditAgent, auditTempBase, hostPreflight, mergeAuditResults, probeTerminalRefusal, setupProject } from "./audit_ao.mjs";
import { diagnoseGate } from "./audit_capabilities.mjs";
import { buildFixLoop, classifyFailure } from "./audit_fixloop.mjs";
import { classifyCiLog, evidenceViolations, migrationProblems, sharedChanges } from "./harness_pr_check.mjs";

function json(status, body) {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

async function withFetch(handler, run) {
  const original = globalThis.fetch;
  globalThis.fetch = async (url, options = {}) => handler(new URL(url), options, options.body ? JSON.parse(options.body) : undefined);
  try { return await run("http://ao.test"); } finally { globalThis.fetch = original; }
}

test("long TMPDIR is replaced so native sockets fit (ZCode listen EINVAL, MiniMax TMPDIR)", () => {
  if (process.platform === "win32") return;
  assert.equal(auditTempBase("/var/folders/xy/abcdefghijklmnop/T"), "/tmp");
  assert.equal(auditTempBase("/tmp"), "/tmp");
});

test("host preflight fails fast on a socket path that cannot fit and reports missing UI prerequisites", async () => {
  const deep = join(mkdtempSync(join(tmpdir(), "ao-h-")), "x".repeat(90));
  execFileSync("mkdir", ["-p", deep]);
  const result = await hostPreflight({ root: deep, repoRoot: deep, aoPath: "/bin/ao" });
  const byName = Object.fromEntries(result.checks.map((item) => [item.name, item]));
  assert.equal(byName.socket_path.status, "FAIL");
  assert.match(byName.socket_path.detail, /set TMPDIR to a short directory/);
  assert.equal(byName.ui_capture.status, "ADVISORY");
  assert.match(byName.ui_capture.detail, /frontend\/node_modules is absent.*no --capture-command/);
  assert.equal(byName.tool_go, undefined, "go is not required with --ao");
  assert.ok(result.failed.some((item) => item.name === "socket_path"));
});

test("scratch project has origin/HEAD so spawn cannot fail with DEFAULT_BRANCH_UNRESOLVED (Pool)", async () => {
  const root = mkdtempSync(join(tmpdir(), "ao-origin-"));
  const result = await withFetch((url, options, body) => json(201, { project: { id: body.projectId } }), (baseURL) => setupProject({
    baseURL, root, agent: "example", timeoutMs: 10_000, scenario: { agentsToken: "a", hiddenInitialToken: "h" },
  }));
  assert.equal(result.ok, true);
  const head = execFileSync("git", ["symbolic-ref", "refs/remotes/origin/HEAD"], { cwd: result.projectPath, encoding: "utf8" }).trim();
  assert.equal(head, "refs/remotes/origin/main");
});

function preflightHandler(models) {
  return (url) => {
    if (url.pathname.endsWith("/probe")) return json(200, { supported: true, installed: true, agent: { authStatus: "authorized" } });
    if (url.pathname.endsWith("/readiness/ensure")) return json(200, { agents: [{ id: "free", installation: { state: "installed", freshness: "fresh" }, authentication: { state: "authorized", freshness: "fresh" }, effectiveReadiness: "ready" }] });
    if (url.pathname.endsWith("/models")) return json(200, models);
    return json(404, {});
  };
}

test("free-form model entry passes only when AO and the contract both declare it (OpenInterpreter, ZCode, Mastra)", async () => {
  const text = { selectionMode: "text", models: [] };
  const declared = await withFetch(preflightHandler(text), (baseURL) => auditAgent({ baseURL, agent: "free", projectId: "p", live: false, modelSelection: "text" }));
  const modelsGate = declared.gates.find((item) => item.name === "ao_models_api");
  assert.equal(modelsGate.status, "PASS");
  assert.match(modelsGate.reason, /free-form model entry/);
  assert.equal(mergeAuditResults({ agent: "free", gates: [], models: [] }, declared).gates.find((item) => item.name === "model_catalog_consistency").status, "UNSUPPORTED");

  const undeclared = await withFetch(preflightHandler(text), (baseURL) => auditAgent({ baseURL, agent: "free", projectId: "p", live: false }));
  assert.match(undeclared.gates.find((item) => item.name === "ao_models_api").reason, /contract does not declare modelSelection: "text"/);

  const emptyCatalog = await withFetch(preflightHandler({ selectionMode: "catalog", models: [] }), (baseURL) => auditAgent({ baseURL, agent: "free", projectId: "p", live: false, modelSelection: "text" }));
  const failed = emptyCatalog.gates.find((item) => item.name === "ao_models_api");
  assert.equal(failed.status, "FAIL");
  assert.match(failed.reason, /selectionMode catalog but returned no models/);

  const slugs = await withFetch(preflightHandler({ selectionMode: "catalog", models: [{ slug: "gpt-x" }] }), (baseURL) => auditAgent({ baseURL, agent: "free", projectId: "p", live: false }));
  assert.deepEqual(slugs.aoModels, ["gpt-x"], "catalog entries keyed by slug are read (OpenInterpreter)");
});

test("Chat-only harness: typed TUI refusal makes TUI gates UNSUPPORTED; a launched TUI is a FAIL (ZCode, Pool, Docker Agent)", async () => {
  const refused = await withFetch(() => json(400, { error: { code: "TERMINAL_UNSUPPORTED", message: "chat only" } }), (baseURL) => probeTerminalRefusal({ baseURL, agent: "pool", projectId: "p", timeoutMs: 1_000 }));
  assert.ok(refused.length > 10);
  assert.ok(refused.every((item) => item.status === "UNSUPPORTED"));
  assert.match(refused[0].reason, /HTTP 400 TERMINAL_UNSUPPORTED/);

  const sessions = [];
  const launched = await withFetch(() => json(201, { session: { id: "s1", mode: "tui" } }), (baseURL) => probeTerminalRefusal({
    baseURL, agent: "pool", projectId: "p", timeoutMs: 1_000, onSession: ({ sessionId }) => sessions.push(sessionId),
  }));
  assert.equal(launched[0].name, "tui_spawn");
  assert.equal(launched[0].status, "FAIL");
  assert.deepEqual(sessions, ["s1"], "the accidental TUI session is registered for cleanup");
});

test("quota, provider auth codes and native takeover gates go to the user, not the fix loop", () => {
  const cases = [
    { name: "local_session_spawn", status: "FAIL", reason: "HTTP429 code1310: Weekly/Monthly Limit Exhausted" },
    { name: "chat_spawn", status: "FAIL", reason: "ACP authenticate returned -32000 Authentication required" },
    { name: "proof_file_creation", status: "FAIL", reason: "native prompt waits on 'Enter: take over' for a limited account" },
    { name: "authentication", status: "BLOCKED", reason: "credentials configured, but AO could not verify them" },
  ];
  for (const item of cases) assert.equal(classifyFailure({ ...item, diagnosis: diagnoseGate(item, { agent: "x" }) }), "needs_user", item.reason);
});

test("known upstream limitations stop the loop with a maintainer decision instead of retries (Strands, Stakpak cancel)", () => {
  const report = { results: [{
    agent: "stakpak",
    knownUpstream: { cancellation: "stakpak/agent#767: executor kills only the immediate child" },
    gates: [{ name: "cancellation", status: "FAIL", reason: "late side effect appeared" }],
  }] };
  const loop = buildFixLoop(report, {});
  assert.equal(loop.items[0].failureClass, "unsupported_upstream");
  assert.equal(loop.items[0].rerun, null);
  assert.match(loop.items[0].upstream, /stakpak\/agent#767/);
  assert.equal(loop.status, "upstream_decision");
});

test("run-level failures lead the fix loop", () => {
  const loop = buildFixLoop({ results: [], runErrors: [{ stage: "host_preflight", message: "host preflight failed: socket_path (128 bytes)" }] }, { command: "node r.mjs" });
  assert.equal(loop.next.gate, "host_preflight");
  assert.equal(loop.next.failureClass, "environment");
});

test("PR check: committed evidence, migrations, shared code and CI logs are classified", () => {
  assert.deepEqual(evidenceViolations([
    "docs/research/2026-10-10-hermes/summary.json",
    "docs/harnesses/hermes.md",
    "frontend/src/renderer/assets/agents/hermes.svg",
    "frontend/src/renderer/assets/agents/hermes.png",
    "notes/run/issue-0003.json",
    "shots/04-final.png",
  ]).map((item) => item.file), ["docs/research/2026-10-10-hermes/summary.json", "notes/run/issue-0003.json", "shots/04-final.png"]);

  assert.deepEqual(migrationProblems(["0203_allow_hermes.sql", "20261021000000000_allow_tau.sql"], ["0202_a.sql", "0203_awake.sql", "20261019220421510_b.sql"])
    .map((item) => item.why), ["number 203 already used on base by 0203_awake.sql"]);
  assert.match(migrationProblems(["0201_x.sql"], ["20261019220421510_b.sql"])[0].why, /below base's latest/);

  assert.deepEqual(sharedChanges([
    "backend/internal/adapters/chatdriver/acp/driver.go",
    "backend/internal/adapters/chatdriver/acp/driver_test.go",
    "backend/internal/adapters/agent/registry/registry.go",
    "backend/internal/adapters/agent/hermes/hermes.go",
  ]), ["backend/internal/adapters/chatdriver/acp/driver.go"]);

  assert.equal(classifyCiLog("driver.go:13:1: File is not properly formatted (goimports)").kind, "lint");
  assert.equal(classifyCiLog("--- FAIL: TestEveryRefreshHarnessResumesExactConversation/codebuddy\n missing explicit refresh capability").kind, "harness_matrix");
  assert.equal(classifyCiLog("https://pypi.org/pypi/tau-ai==0.4.7/json returned status 404").kind, "live_feed");
  assert.equal(classifyCiLog("fork/exec /tmp/x: text file busy").kind, "known_flake");
});

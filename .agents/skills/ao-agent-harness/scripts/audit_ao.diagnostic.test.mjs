import assert from "node:assert/strict";
import { chmod, mkdtemp, readFile, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";
import { parseArgs, runCommand } from "./audit_ao.mjs";

test("diagnostic continuation requires an explicit option", () => {
  assert.equal(parseArgs([]).diagnosticAfterNativeProof, false);
  assert.equal(parseArgs(["--diagnostic-after-native-proof"]).diagnosticAfterNativeProof, true);
});

const cases = [
  { name: "diagnostic metadata survives the lifecycle termination-failure return", lifecycle: true, spawn: true, auth: "BLOCKED", models: "FAIL" },
  { name: "default still blocks configured auth after native proof", optIn: false, spawn: false, auth: "BLOCKED" },
  { name: "opt-in preserves blocked configured auth and empty models while collecting spawn evidence", spawn: true, auth: "BLOCKED", models: "FAIL" },
  { name: "opt-in permits unknown auth only with native proof", state: "unknown", spawn: true, auth: "BLOCKED", models: "FAIL" },
  { name: "unauthorized readiness always blocks", state: "unauthorized", spawn: false, auth: "BLOCKED" },
  { name: "unauthorized targeted probe always blocks", probeAuth: "unauthorized", spawn: false, auth: "BLOCKED" },
  { name: "missing direct proof prevents continuation", local: "missing-proof", spawn: false, auth: "BLOCKED" },
  { name: "failed local version prevents continuation", local: "bad-version", spawn: false, auth: "BLOCKED" },
  { name: "failed local integration prevents continuation", local: "bad-integration", spawn: false, auth: "BLOCKED" },
  { name: "local login action prevents continuation", local: "login", spawn: false, auth: "BLOCKED" },
  { name: "failed probe HTTP prevents continuation", probeHTTP: 503, spawn: false },
  { name: "failed readiness HTTP prevents continuation", readinessHTTP: 503, spawn: false },
  { name: "missing installation prevents continuation", installed: false, spawn: false },
  { name: "stale observations prevent continuation", freshness: "stale", spawn: false },
  { name: "failed models HTTP prevents continuation despite native proof", modelsHTTP: 503, spawn: false, auth: "BLOCKED", models: "FAIL" },
];

for (const tc of cases) {
  test(tc.name, async () => {
    const root = await mkdtemp(join(tmpdir(), "ao-diagnostic-fixture-"));
    const fakeAO = join(root, "fake-ao.mjs");
    const fakeLocal = join(root, "fake-local.mjs");
    const contractPath = join(root, "contract.json");
    const base = join(root, "report");
    const fixture = {
      state: "configured", installed: true, freshness: "fresh", probeHTTP: 200,
      readinessHTTP: 200, modelsHTTP: 200, local: "proof", ...tc,
    };
    await writeFile(fakeAO, `#!/usr/bin/env node
import { createServer } from "node:http";
import { writeFile } from "node:fs/promises";
const fixture = ${JSON.stringify(fixture)};
const server = createServer(async (req, res) => {
  let body = {};
  let status = 200;
  if (req.url === "/readyz") body = { ready: true };
  else if (req.url === "/api/v1/agents") body = { supported: [{ id: "fixture" }] };
  else if (req.url.endsWith("/probe")) {
    status = fixture.probeHTTP;
    body = { supported: true, installed: fixture.installed, agent: { authStatus: fixture.probeAuth || fixture.state } };
  } else if (req.url.endsWith("/readiness/ensure")) {
    status = fixture.readinessHTTP;
    body = { agents: [{ id: "fixture",
      installation: { state: fixture.installed ? "installed" : "missing", freshness: fixture.freshness },
      authentication: { state: fixture.state, freshness: fixture.freshness },
      effectiveReadiness: "unknown" }] };
  } else if (req.url.endsWith("/models")) {
    status = fixture.modelsHTTP; body = { models: [] };
  } else if (req.url === "/api/v1/projects") {
    let raw = ""; for await (const part of req) raw += part;
    body = { project: { id: JSON.parse(raw).projectId } };
  } else if (req.url === "/api/v1/sessions") {
    if (fixture.lifecycle) body = { session: { id: "diagnostic-session", mode: "tui", harness: "fixture" } };
    else { status = 422; body = { error: "fixture deliberately stops after spawn request" }; }
  } else if (fixture.lifecycle && req.url.includes("/workspace/file")) {
    body = { content: "{}" };
  } else if (fixture.lifecycle && req.url.endsWith("/workspace")) {
    body = { workspacePath: "/tmp/diagnostic-fixture-workspace" };
  } else if (fixture.lifecycle && req.url === "/api/v1/sessions/diagnostic-session") {
    body = { session: { id: "diagnostic-session", isTerminated: false, statusReadiness: "ready", activity: { state: "idle" } } };
  } else { status = 404; body = { error: "unexpected route" }; }
  res.statusCode = status;
  res.setHeader("content-type", "application/json");
  res.end(JSON.stringify(body));
});
server.listen(Number(process.env.AO_PORT), "127.0.0.1", async () => {
  await writeFile(process.env.AO_RUN_FILE, JSON.stringify({ pid: process.pid, port: Number(process.env.AO_PORT) }));
});
process.on("SIGTERM", () => server.close(() => process.exit(0)));
`);
    await chmod(fakeAO, 0o755);
    await writeFile(fakeLocal, `#!/usr/bin/env node
import { writeFile } from "node:fs/promises";
const fixture = ${JSON.stringify(fixture)};
const [command, prompt] = process.argv.slice(2);
if (command === "--version") { console.log("fixture 1.0"); if (fixture.local === "bad-version") process.exitCode = 1; }
else if (command === "--help") {
  console.log("integration-ready");
  if (fixture.local === "bad-integration") process.exitCode = 1;
  if (fixture.local === "login") { console.error("authentication required; please sign in"); process.exitCode = 1; }
} else if (command === "session" && fixture.local !== "missing-proof") {
  await writeFile("LOCAL_AUDIT_PROOF.json", JSON.stringify({ token: prompt.match(/LOCAL_SESSION_[a-f0-9]+/)[0], cwd: process.cwd() }));
}
`);
    await chmod(fakeLocal, 0o755);
    await writeFile(contractPath, JSON.stringify({ schemaVersion: 1, agents: {
      fixture: { binary: fakeLocal, versionArgs: ["--version"],
        integration: { args: ["--help"], expect: "integration-ready" },
        session: { args: ["session", "{prompt}"], proofFile: "LOCAL_AUDIT_PROOF.json" } },
    }}));
    const args = [fileURLToPath(new URL("./audit_ao.mjs", import.meta.url)),
      "--ao", fakeAO, "--repo", root, "--agent", "fixture", "--local-contract", contractPath,
      "--report", base, "--startup-timeout-seconds", "5", "--timeout-seconds", "1"];
    if (tc.optIn !== false) args.push("--diagnostic-after-native-proof");
    const outcome = await runCommand(process.execPath, args, { timeoutMs: 12000 });
    assert.equal(outcome.code, 1, outcome.stderr);
    const report = JSON.parse(await readFile(base + ".json", "utf8"));
    const result = report.results[0];
    const gate = name => result.gates.find(item => item.name === name);
    if (tc.lifecycle) {
      assert.equal(gate("runner_error"), undefined, JSON.stringify(result));
      assert.equal(gate("termination")?.status, "FAIL");
    }
    assert.equal(gate("tui_spawn").status, tc.lifecycle ? "PASS" : tc.spawn ? "FAIL" : "NOT_RUN");
    assert.equal(gate("local_models_list").status, "NOT_RUN");
    assert.notEqual(report.summary.status, "PASS");
    assert.equal(report.daemon.stopped, true);
    if (tc.auth) assert.equal(gate("authentication").status, tc.auth);
    if (tc.models) assert.equal(gate("ao_models_api").status, tc.models);
    if (tc.spawn) {
      assert.equal(gate("local_session_spawn").status, "PASS");
      assert.deepEqual(result.aoModels, []);
      assert.equal(result.diagnostic.lifecycleEligible, true);
      assert.equal(report.diagnosticAfterNativeProof, true);
      assert.match(await readFile(base + ".md", "utf8"), /Diagnostic only/);
      assert.ok(report.issues.some(issue => issue.gate === "authentication" && issue.status === "BLOCKED"));
      assert.ok(report.issues.some(issue => issue.gate === "ao_models_api" && issue.status === "FAIL"));
    }
  });
}

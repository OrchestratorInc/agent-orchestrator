import assert from "node:assert/strict";
import { chmod, mkdir, mkdtemp, readFile, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";
import { auditLocalAgent, auditAgent, runCommand } from "./audit_ao.mjs";
import * as auditModule from "./audit_ao.mjs";

test("failed local probes retain useful redacted diagnostics instead of only hashes", async () => {
  const result = await auditLocalAgent({
    agent: "fixture", live: false,
    contract: {
      binary: process.execPath,
      integration: { args: ["-e", 'console.error("session expired; sign in. api_key=fixture-secret-value"); process.exitCode = 1;'] },
    },
  });
  const evidence = result.gates.find(g => g.name === "local_integration").evidence;
  assert.match(evidence.stderr || "", /session expired; sign in/);
  assert.doesNotMatch(JSON.stringify(result), /fixture-secret-value/);
});

test("unverified AO authentication produces a user handoff without starting a session", async () => {
  const original = globalThis.fetch;
  const paths = [];
  globalThis.fetch = async url => {
    const path = new URL(url).pathname;
    paths.push(path);
    const body = path.endsWith("/probe")
      ? { supported: true, installed: true, agent: { authStatus: "configured" } }
      : { agents: [{ id: "fixture", installation: { state: "installed", freshness: "fresh" }, authentication: { state: "configured", freshness: "fresh", reason: "Credentials are unverified" }, effectiveReadiness: "unknown" }] };
    return new Response(JSON.stringify(body), { status: 200 });
  };
  try {
    const result = await auditAgent({ baseURL: "http://fixture.test", agent: "fixture", live: true });
    assert.equal(result.status, "BLOCKED");
    assert.equal(result.userAction?.type, "authentication");
    assert.equal(result.userAction?.agent, "fixture");
    assert.match(result.userAction?.message || "", /verify|sign in/i);
    assert.equal(result.userAction?.requiresFreshProbe, true);
    assert.equal(paths.some(p => p === "/api/v1/sessions"), false);
  } finally { globalThis.fetch = original; }
});

test("daemon startup failure still writes machine-readable evidence and a log", async () => {
  const root = await mkdtemp(join(tmpdir(), "ao-fatal-evidence-test-"));
  const base = join(root, "report");
  const outcome = await runCommand(process.execPath, [
    fileURLToPath(new URL("./audit_ao.mjs", import.meta.url)),
    "--ao", join(root, "missing-ao"), "--repo", root,
    "--port", "43129", "--no-live", "--report", base,
    "--startup-timeout-seconds", "1",
  ], { timeoutMs: 5_000 });
  assert.equal(outcome.code, 1);
  const report = JSON.parse(await readFile(`${base}.json`, "utf8"));
  assert.equal(report.summary.status, "FAIL");
  assert.equal(report.runErrors[0].stage, "startup");
  assert.equal(report.issues[0].screenshot.status, "unavailable");
  assert.match(report.issues[0].screenshot.reason, /headless|capture/i);
  assert.match(await readFile(report.artifacts.runnerLog, "utf8"), /startup|ENOENT/);
  assert.match(await readFile(`${base}.md`, "utf8"), /startup/i);
  assert.doesNotMatch(await readFile(`${base}.md`, "utf8"), /daemon was stopped/);
});

test("capture hook gets issue context and its PNG is retained with the original failure", async () => {
  const root = await mkdtemp(join(tmpdir(), "ao-capture-test-"));
  const script = join(root, "capture.mjs");
  await writeFile(script, `import { readFile, writeFile } from 'node:fs/promises';
const [context, output] = process.argv.slice(2);
const issue = JSON.parse(await readFile(context, 'utf8'));
if (issue.agent !== 'fixture' || issue.gate !== 'proof_file_creation') process.exit(2);
await writeFile(output, Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII=', 'base64'));`);
  assert.equal(typeof auditModule.createEvidenceStore, "function");
  const store = await auditModule.createEvidenceStore({ base: join(root, "report"), captureCommand: [process.execPath, script], runCommand });
  const issue = await store.issue({ agent: "fixture", gate: { name: "proof_file_creation", status: "FAIL", reason: "proof missing", evidence: { status: 404, api_key: "fixture-sensitive" } } });
  assert.equal(issue.status, "FAIL");
  assert.equal(issue.screenshot.status, "captured");
  assert.equal((await readFile(issue.screenshot.path)).subarray(1, 4).toString(), "PNG");
  assert.doesNotMatch(await readFile(issue.evidencePath, "utf8"), /fixture-sensitive/);
  assert.match(await readFile(store.paths.events, "utf8"), /issue/);
});

test("a failed screenshot hook preserves the audit failure and reports capture failure", async () => {
  const root = await mkdtemp(join(tmpdir(), "ao-capture-failed-test-"));
  assert.equal(typeof auditModule.createEvidenceStore, "function");
  const store = await auditModule.createEvidenceStore({ base: join(root, "report"), captureCommand: [process.execPath, "-e", 'console.error("capture unavailable"); process.exit(1)'], runCommand });
  const issue = await store.issue({ agent: "fixture", gate: { name: "tui_spawn", status: "FAIL", reason: "spawn failed" } });
  assert.equal(issue.status, "FAIL");
  assert.equal(issue.screenshot.status, "failed");
  assert.match(issue.screenshot.reason, /capture unavailable/);
  assert.equal(JSON.parse(await readFile(issue.evidencePath, "utf8")).reason, "spawn failed");
});

test("capture exit zero without a PNG cannot claim screenshot coverage", async () => {
  const root = await mkdtemp(join(tmpdir(), "ao-capture-empty-test-"));
  const store = await auditModule.createEvidenceStore({ base: join(root, "report"), captureCommand: [process.execPath, "-e", "process.exit(0)"], runCommand });
  const issue = await store.issue({ agent: "fixture", gate: { name: "tui_spawn", status: "FAIL", reason: "spawn failed" } });
  assert.equal(issue.screenshot.status, "failed");
  assert.equal(issue.status, "FAIL");
  assert.equal(issue.screenshot.path, undefined);
});

test("ordinary local network failure is not presented as an expired login", async () => {
  const result = await auditLocalAgent({
    agent: "fixture", live: false,
    contract: { binary: process.execPath, integration: { args: ["-e", 'console.error("connection timed out"); process.exit(1)'] } },
  });
  assert.equal(result.userAction, undefined);
  assert.equal(result.gates.find(g => g.name === "local_integration").status, "FAIL");
});

test("failed probe diagnostics keep the final error and disclose truncation", async () => {
  const result = await auditLocalAgent({
    agent: "fixture", live: false,
    contract: { binary: process.execPath, integration: { args: ["-e", 'console.error("x".repeat(12000) + "\\nfinal diagnostic line"); process.exitCode = 1'] } },
  });
  const evidence = result.gates.find(g => g.name === "local_integration").evidence;
  assert.match(evidence.stderr, /final diagnostic line/);
  assert.equal(evidence.stderrTruncated, true);
  assert.ok(evidence.stderr.length <= 8192);
});

test("startup evidence is captured while the failed daemon is still available", async () => {
  const root = await mkdtemp(join(tmpdir(), "ao-startup-capture-test-"));
  const marker = join(root, "state");
  const script = join(root, "never-ready.mjs");
  await writeFile(script, `import { writeFileSync } from 'node:fs';
writeFileSync(${JSON.stringify(marker)}, 'running');
writeFileSync(process.env.AO_RUN_FILE, JSON.stringify({ pid: process.pid, port: Number(process.env.AO_PORT) }));
console.error('startup failed');
process.on('SIGTERM', () => { writeFileSync(${JSON.stringify(marker)}, 'stopped'); process.exit(0); });
setInterval(() => {}, 1000);`);
  let captured = false;
  await assert.rejects(auditModule.startManagedDaemon({
    root, aoCommand: [process.execPath, script], port: 43127, timeoutMs: 500,
    readinessCheck: async () => false,
    onStartupFailure: async error => {
      assert.match(error.evidence.daemonLogs, /startup failed/);
      assert.equal(await readFile(marker, "utf8"), "running");
      captured = true;
    },
  }), /did not become ready/);
  assert.equal(captured, true);
  assert.equal(await readFile(marker, "utf8"), "stopped");
});

test("an unusable report target exits as invalid input without starting a daemon", async () => {
  const root = await mkdtemp(join(tmpdir(), "ao-destination-test-"));
  const base = join(root, "report");
  await mkdir(`${base}.json`);
  const outcome = await runCommand(process.execPath, [
    fileURLToPath(new URL("./audit_ao.mjs", import.meta.url)), "--no-live", "--report", base,
  ], { timeoutMs: 3_000 });
  assert.equal(outcome.code, 2);
  assert.doesNotMatch(outcome.stderr, /building and starting/);
});

test("discovery failure retains API response and request ID", async () => {
  const original = globalThis.fetch;
  globalThis.fetch = async () => new Response(JSON.stringify({ detail: "unsupported db schema" }), { status: 503, headers: { "x-request-id": "fixture-request-123" } });
  try {
    await assert.rejects(auditModule.discoverAgents({ baseURL: "http://fixture.test" }), error => {
      assert.equal(error.evidence?.requestId, "fixture-request-123");
      assert.equal(error.evidence?.response.detail, "unsupported db schema");
      return true;
    });
  } finally { globalThis.fetch = original; }
});

test("daemon shutdown still runs if evidence storage disappears during cleanup", async () => {
  const root = await mkdtemp(join(tmpdir(), "ao-cleanup-evidence-test-"));
  const fake = join(root, "fake-ao.mjs");
  const capture = join(root, "capture.mjs");
  const pidFile = join(root, "pid");
  const stopped = join(root, "stopped");
  await writeFile(fake, `#!/usr/bin/env node
import { createServer } from 'node:http';
import { writeFile } from 'node:fs/promises';
const server = createServer(async (req, res) => {
  let input = ''; for await (const chunk of req) input += chunk;
  let body;
  if (req.url === '/readyz') body = { ready: true };
  else if (req.url === '/api/v1/agents') body = { supported: [{ id: 'fixture' }] };
  else if (req.url.endsWith('/probe')) body = { supported: true, installed: true, agent: { authStatus: 'authorized' } };
  else if (req.url.endsWith('/readiness/ensure')) body = { agents: [{ id: 'fixture', installation: { state: 'installed', freshness: 'fresh' }, authentication: { state: 'authorized', freshness: 'fresh' }, effectiveReadiness: 'ready' }] };
  else if (req.url.endsWith('/models')) body = { models: [{ id: 'model-a' }] };
  else if (req.url === '/api/v1/projects') body = { project: { id: JSON.parse(input).projectId } };
  else if (req.url === '/api/v1/sessions') body = { session: { id: 'cleanup-fixture', mode: 'wrong', harness: 'fixture' } };
  else if (req.url.endsWith('/kill')) { res.statusCode = 500; body = { error: 'kill failed' }; }
  else body = { session: { id: 'cleanup-fixture', isTerminated: false } };
  res.setHeader('content-type', 'application/json'); res.end(JSON.stringify(body));
});
server.listen(Number(process.env.AO_PORT), '127.0.0.1', async () => {
  await writeFile(${JSON.stringify(pidFile)}, String(process.pid));
  await writeFile(process.env.AO_RUN_FILE, JSON.stringify({ pid: process.pid, port: Number(process.env.AO_PORT) }));
});
process.on('SIGTERM', async () => { await writeFile(${JSON.stringify(stopped)}, 'stopped'); server.close(() => process.exit(0)); });`);
  await chmod(fake, 0o755);
  await writeFile(capture, `import { readFile, rename } from 'node:fs/promises'; import { dirname } from 'node:path';
const issue = JSON.parse(await readFile(process.argv[2], 'utf8'));
if (issue.gate === 'cleanup_termination') await rename(dirname(process.argv[2]), dirname(process.argv[2]) + '.moved');`);
  try {
    const outcome = await runCommand(process.execPath, [
      fileURLToPath(new URL("./audit_ao.mjs", import.meta.url)), "--ao", fake, "--repo", root,
      "--report", join(root, "report"), "--timeout-seconds", "2",
      "--capture-command", JSON.stringify([process.execPath, capture]),
    ], { timeoutMs: 12_000 });
    assert.notEqual(outcome.code, 0);
    assert.equal(await readFile(stopped, "utf8"), "stopped");
  } finally {
    try { process.kill(Number(await readFile(pidFile, "utf8")), "SIGTERM"); } catch {}
  }
});

test("local failure evidence is captured before the next probe changes the state", async () => {
  const root = await mkdtemp(join(tmpdir(), "ao-capture-order-test-"));
  const marker = join(root, "models-started");
  const observed = [];
  await auditLocalAgent({
    agent: "fixture", live: false,
    onIssue: async ({ gate }) => {
      observed.push(gate.name);
      await assert.rejects(readFile(marker), { code: "ENOENT" });
    },
    contract: {
      binary: process.execPath,
      integration: { args: ["-e", "process.exit(1)"] },
      models: { args: ["-e", `require('fs').writeFileSync(${JSON.stringify(marker)}, 'started'); console.log('model-a')`] },
    },
  });
  assert.deepEqual(observed, ["local_integration"]);
  assert.equal(await readFile(marker, "utf8"), "started");
});

test("expired local login requests user help and skips the dependent provider turn", async () => {
  const root = await mkdtemp(join(tmpdir(), "ao-login-test-"));
  const marker = join(root, "turn-started");
  const result = await auditLocalAgent({
    agent: "fixture", live: true, root,
    contract: {
      binary: process.execPath,
      integration: { args: ["-e", 'console.error("session expired; sign in"); process.exit(1)'] },
      session: { args: ["-e", `require('fs').writeFileSync(${JSON.stringify(marker)}, 'started')`] },
    },
  });
  assert.equal(result.userAction?.type, "authentication");
  assert.equal(result.gates.find(g => g.name === "local_session_spawn").status, "NOT_RUN");
  await assert.rejects(readFile(marker), { code: "ENOENT" });
  const merged = auditModule.mergeAuditResults(result, { agent: "fixture", gates: [] });
  assert.equal(merged.userAction?.type, "authentication");
});

test("CLI persists auth handoff, redacted daemon logs, and confirmed daemon exit", async () => {
  const root = await mkdtemp(join(tmpdir(), "ao-cli-evidence-test-"));
  const fake = join(root, "fake-ao.mjs");
  await writeFile(fake, `#!/usr/bin/env node
import { createServer } from 'node:http';
import { writeFile } from 'node:fs/promises';
const server = createServer((req, res) => {
  const path = req.url;
  let body;
  if (path === '/readyz') body = { ready: true };
  else if (path === '/api/v1/agents') body = { supported: [{ id: 'fixture' }] };
  else if (path.endsWith('/probe')) body = { supported: true, installed: true, agent: { authStatus: 'configured' } };
  else if (path.endsWith('/readiness/ensure')) body = { agents: [{ id: 'fixture', installation: { state: 'installed', freshness: 'fresh' }, authentication: { state: 'configured', freshness: 'fresh', reason: 'Verification needed' }, effectiveReadiness: 'unknown' }] };
  else { res.statusCode = 404; body = { error: 'unexpected route' }; }
  res.setHeader('content-type', 'application/json'); res.end(JSON.stringify(body));
});
server.listen(Number(process.env.AO_PORT), '127.0.0.1', async () => {
  console.log('fixture daemon started api_key=fixture-daemon-secret');
  await writeFile(process.env.AO_RUN_FILE, JSON.stringify({ pid: process.pid, port: Number(process.env.AO_PORT) }));
});
process.on('SIGTERM', () => { console.log('fixture daemon stopped'); server.close(() => process.exit(0)); });
`);
  await chmod(fake, 0o755);
  const base = join(root, "report");
  const args = [fileURLToPath(new URL("./audit_ao.mjs", import.meta.url)), "--ao", fake, "--repo", root, "--no-live", "--report", base];
  const outcome = await runCommand(process.execPath, args, { timeoutMs: 8_000 });
  assert.equal(outcome.code, 1, outcome.stderr);
  const raw = await readFile(`${base}.json`, "utf8");
  const report = JSON.parse(raw);
  assert.equal(report.state, "finished");
  assert.equal(report.summary.status, "BLOCKED");
  assert.equal(report.daemon.stopped, true);
  assert.match(await readFile(report.artifacts.events, "utf8"), /needs_user_action/);
  const logs = await readFile(report.artifacts.daemonLog, "utf8");
  assert.match(logs, /fixture daemon stopped/);
  assert.doesNotMatch(logs + raw + outcome.stderr, /fixture-daemon-secret/);
  assert.equal(report.issues[0].screenshot.status, "unavailable");
  const repeated = await runCommand(process.execPath, args, { timeoutMs: 3_000 });
  assert.equal(repeated.code, 2);
  assert.equal(await readFile(`${base}.json`, "utf8"), raw, "a rerun must not overwrite the original evidence");
});

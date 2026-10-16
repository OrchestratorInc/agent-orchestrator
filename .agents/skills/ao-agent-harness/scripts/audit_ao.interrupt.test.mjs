import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { existsSync } from "node:fs";
import { chmod, mkdtemp, readFile, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";

const runner = fileURLToPath(new URL("./audit_ao.mjs", import.meta.url));

function startRunner(args) {
  const child = spawn(process.execPath, [runner, ...args], { stdio: ["ignore", "pipe", "pipe"] });
  let stderr = "";
  child.stderr.on("data", chunk => { stderr += chunk; });
  child.stdout.resume();
  const exited = new Promise(resolve => child.once("close", code => resolve({ code, stderr })));
  return { child, exited };
}

async function waitForFile(path, timeoutMs = 10_000) {
  const deadline = Date.now() + timeoutMs;
  while (!existsSync(path)) {
    if (Date.now() > deadline) throw new Error(`timed out waiting for ${path}`);
    await new Promise(resolve => setTimeout(resolve, 25));
  }
}

function alive(pid) {
  try { process.kill(pid, 0); return true; } catch { return false; }
}

// POSIX signal delivery; Windows has no catchable SIGINT/SIGTERM for a child.
const posixOnly = { skip: process.platform === "win32" };

test("SIGINT during daemon startup stops the spawned daemon and finalizes the report", posixOnly, async () => {
  const root = await mkdtemp(join(tmpdir(), "ao-interrupt-startup-"));
  const fake = join(root, "fake-ao.mjs");
  const pidFile = join(root, "pid");
  const stopped = join(root, "stopped");
  // Never writes the run file, so the runner stays in its readiness wait.
  await writeFile(fake, `#!/usr/bin/env node
import { writeFileSync } from 'node:fs';
writeFileSync(${JSON.stringify(pidFile)}, String(process.pid));
process.on('SIGTERM', () => { writeFileSync(${JSON.stringify(stopped)}, 'stopped'); process.exit(0); });
setInterval(() => {}, 1000);
`);
  await chmod(fake, 0o755);
  const base = join(root, "report");
  const { child, exited } = startRunner(["--ao", fake, "--repo", root, "--no-live", "--report", base, "--startup-timeout-seconds", "60"]);
  let daemonPid;
  try {
    await waitForFile(pidFile);
    daemonPid = Number(await readFile(pidFile, "utf8"));
    child.kill("SIGINT");
    const { code, stderr } = await exited;
    assert.equal(code, 130, stderr);
    assert.match(stderr, /SIGINT received/);
    const report = JSON.parse(await readFile(`${base}.json`, "utf8"));
    assert.equal(report.state, "finished");
    assert.equal(report.interrupted?.signal, "SIGINT");
    assert.equal(report.summary.status, "FAIL");
    assert.ok(report.runErrors.some(item => item.stage === "interrupted"));
    assert.equal(await readFile(stopped, "utf8"), "stopped");
    assert.equal(alive(daemonPid), false);
  } finally {
    if (child.exitCode === null) child.kill("SIGKILL");
    if (daemonPid && alive(daemonPid)) process.kill(daemonPid, "SIGKILL");
  }
});

test("SIGTERM during a live turn kills the created session before stopping the daemon", posixOnly, async () => {
  const root = await mkdtemp(join(tmpdir(), "ao-interrupt-live-"));
  const fake = join(root, "fake-ao.mjs");
  const pidFile = join(root, "pid");
  const created = join(root, "session-created");
  const order = join(root, "order");
  await writeFile(fake, `#!/usr/bin/env node
import { createServer } from 'node:http';
import { appendFileSync, writeFileSync } from 'node:fs';
let killed = false;
const server = createServer(async (req, res) => {
  let input = ''; for await (const chunk of req) input += chunk;
  let body;
  if (req.url === '/readyz') body = { ready: true };
  else if (req.url === '/api/v1/agents') body = { supported: [{ id: 'fixture' }] };
  else if (req.url.endsWith('/probe')) body = { supported: true, installed: true, agent: { authStatus: 'authorized' } };
  else if (req.url.endsWith('/readiness/ensure')) body = { agents: [{ id: 'fixture', installation: { state: 'installed', freshness: 'fresh' }, authentication: { state: 'authorized', freshness: 'fresh' }, effectiveReadiness: 'ready' }] };
  else if (req.url.endsWith('/models')) body = { models: [{ id: 'model-a' }] };
  else if (req.url === '/api/v1/projects') body = { project: { id: JSON.parse(input).projectId } };
  else if (req.url === '/api/v1/sessions' && req.method === 'POST') {
    writeFileSync(${JSON.stringify(created)}, 'created');
    body = { session: { id: 'interrupt-fixture', mode: 'tui', harness: 'fixture', activity: { state: 'active' } } };
  } else if (req.url.endsWith('/kill')) { killed = true; appendFileSync(${JSON.stringify(order)}, 'kill\\n'); body = { ok: true }; }
  else if (req.url.includes('/files')) { res.statusCode = 404; body = { error: 'missing' }; }
  else body = { session: { id: 'interrupt-fixture', mode: 'tui', harness: 'fixture', isTerminated: killed, activity: { state: killed ? 'exited' : 'active' } } };
  res.setHeader('content-type', 'application/json'); res.end(JSON.stringify(body));
});
server.listen(Number(process.env.AO_PORT), '127.0.0.1', () => {
  writeFileSync(${JSON.stringify(pidFile)}, String(process.pid));
  writeFileSync(process.env.AO_RUN_FILE, JSON.stringify({ pid: process.pid, port: Number(process.env.AO_PORT) }));
});
process.on('SIGTERM', () => { appendFileSync(${JSON.stringify(order)}, 'daemon-stop\\n'); server.close(() => process.exit(0)); setTimeout(() => process.exit(0), 200); });
`);
  await chmod(fake, 0o755);
  const base = join(root, "report");
  const { child, exited } = startRunner(["--ao", fake, "--repo", root, "--report", base, "--timeout-seconds", "60"]);
  let daemonPid;
  try {
    await waitForFile(created);
    daemonPid = Number(await readFile(pidFile, "utf8"));
    child.kill("SIGTERM");
    const { code, stderr } = await exited;
    assert.equal(code, 143, stderr);
    const report = JSON.parse(await readFile(`${base}.json`, "utf8"));
    assert.equal(report.state, "finished");
    assert.equal(report.interrupted?.signal, "SIGTERM");
    assert.equal(report.cleanup[0]?.sessionId, "interrupt-fixture");
    assert.equal(report.cleanup[0]?.confirmedTerminated, true);
    assert.equal(report.daemon.stopped, true);
    assert.deepEqual((await readFile(order, "utf8")).trim().split("\n"), ["kill", "daemon-stop"]);
    assert.equal(alive(daemonPid), false);
  } finally {
    if (child.exitCode === null) child.kill("SIGKILL");
    if (daemonPid && alive(daemonPid)) process.kill(daemonPid, "SIGKILL");
  }
});

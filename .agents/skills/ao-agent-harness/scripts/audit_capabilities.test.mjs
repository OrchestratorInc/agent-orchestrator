import assert from "node:assert/strict";
import { access } from "node:fs/promises";
import { dirname, join, resolve } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

import {
  CAPABILITIES,
  capabilityForGate,
  capabilityMatrix,
  daemonLogExcerpt,
  diagnoseGate,
  diagnosisLine,
  inspectPaths,
  observedFromEvidence,
} from "./audit_capabilities.mjs";
import { SUITES } from "./audit_suites.mjs";
import { markdownReport, parseArgs } from "./audit_ao.mjs";

const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), "../../../..");

test("every suite gate maps to a catalog capability", () => {
  for (const name of Object.values(SUITES).flat()) {
    assert.ok(capabilityForGate(name), `${name} has no capability`);
  }
});

test("catalog inspect paths exist in this checkout for codex and claude-code", async () => {
  const missing = [];
  for (const capability of CAPABILITIES) {
    for (const agent of ["codex", "claude-code"]) {
      for (const path of inspectPaths(capability, agent)) {
        try { await access(join(repoRoot, path)); } catch { missing.push(`${capability.id}/${agent}: ${path}`); }
      }
    }
  }
  assert.deepEqual(missing, [], "update audit_capabilities.mjs when AO files move");
});

test("diagnoseGate explains a failed API gate with expected, observed, causes and files", () => {
  const diagnosis = diagnoseGate({
    name: "chat_interrupt",
    status: "FAIL",
    reason: "interrupt request was rejected",
    evidence: {
      interrupt: {
        method: "POST", path: "/api/v1/sessions/s1/conversation/interrupt", status: 409,
        response: { error: { code: "CHAT_NOT_RUNNING", message: "no running turn" } },
      },
    },
  }, {
    agent: "codex",
    sessionId: "s1",
    daemonLogs: "info boot\nlevel=ERROR session=s1 interrupt failed: broken pipe\nlevel=ERROR other noise",
  });
  assert.equal(diagnosis.capability, "chat_interrupt");
  assert.equal(diagnosis.declaredSupport, "yes");
  assert.match(diagnosis.expected, /interrupt stops the witnessed command/);
  assert.deepEqual(diagnosis.observed, [
    "interrupt request was rejected",
    "POST /api/v1/sessions/s1/conversation/interrupt → HTTP 409 CHAT_NOT_RUNNING: no running turn",
  ]);
  assert.ok(diagnosis.likelyCauses.length > 0);
  assert.ok(diagnosis.inspect.includes("backend/internal/adapters/chatdriver/codexappserver/conversation.go"));
  assert.deepEqual(diagnosis.daemonLogExcerpt, ["level=ERROR session=s1 interrupt failed: broken pipe"]);
  const line = diagnosisLine("codex", { name: "chat_interrupt", status: "FAIL" }, diagnosis);
  assert.match(line, /codex: chat_interrupt FAIL — \[Chat interrupt of an active tool command\] — expected .* — observed .* — likely .* — inspect /);
});

test("diagnoseGate expands harness directories and skips passing gates", () => {
  assert.equal(diagnoseGate({ name: "tui_spawn", status: "PASS" }, { agent: "codex" }), undefined);
  const claude = diagnoseGate({ name: "tui_hooks_installed", status: "FAIL", reason: "missing" }, { agent: "claude-code" });
  assert.ok(claude.inspect.includes("backend/internal/adapters/agent/claudecode/hooks.go"));
});

test("observedFromEvidence summarises command outcomes and network errors", () => {
  assert.deepEqual(observedFromEvidence({
    run: { code: 1, timedOut: true, stdout: "", stderr: "first\nsecond\nlogin required" },
    api: { method: "GET", path: "/api/v1/agents", status: 0, error: "ECONNREFUSED", response: null },
  }), ["command exited 1 (timed out): first | second | login required", "GET /api/v1/agents → no response: ECONNREFUSED"]);
});

test("daemonLogExcerpt falls back to recent error lines when none name the session", () => {
  assert.deepEqual(daemonLogExcerpt("ok\nwarn: disk slow\nfine", { sessionId: "zzz" }), ["warn: disk slow"]);
});

test("capabilityMatrix rolls gates up per capability and never reports missing gates as PASS", () => {
  const matrix = capabilityMatrix({
    agent: "claude-code",
    gates: [
      { name: "chat_spawn", status: "PASS", reason: "ok" },
      { name: "chat_controller_ready", status: "FAIL", reason: "never ready", evidencePath: "/tmp/issue-0001.json" },
      { name: "chat_steer", status: "UNSUPPORTED", reason: "not advertised" },
    ],
  });
  const row = (id) => matrix.find((item) => item.id === id);
  assert.equal(row("chat_spawn").status, "FAIL");
  assert.equal(row("chat_spawn").evidencePath, "/tmp/issue-0001.json");
  assert.match(row("chat_spawn").reason, /chat_controller_ready: never ready/);
  assert.equal(row("chat_steer").status, "UNSUPPORTED");
  assert.equal(row("chat_steer").support, "conditional");
  assert.equal(row("tui_spawn").status, "NOT_RUN");
  assert.equal(row("reviewer").status, "MANUAL");
});

test("markdownReport prints the capability matrix and a diagnosis for each issue", () => {
  const diagnosis = diagnoseGate({ name: "chat_restore", status: "FAIL", reason: "restore switched to a different provider conversation id" }, { agent: "codex" });
  const markdown = markdownReport({
    summary: { status: "FAIL", total: 1, pass: 0, fail: 1, blocked: 0, notRun: 0 },
    startedAt: "s", finishedAt: "f", disposableRoot: "/tmp/a", daemon: { keptRunning: false },
    results: [{ agent: "codex", status: "FAIL", failedGate: "chat_restore", gates: [{ name: "chat_restore", status: "FAIL", reason: "restore switched" }] }],
    issues: [{ agent: "codex", gate: "chat_restore", status: "FAIL", reason: "restore switched", evidencePath: "/tmp/a/issue-0001.json", screenshot: { status: "unavailable", reason: "headless" }, diagnosis }],
  });
  assert.match(markdown, /### Capability matrix/);
  assert.match(markdown, /\| Chat kill and exact native restore \| yes \| FAIL \|/);
  assert.match(markdown, /### Chat\n/);
  assert.match(markdown, /## Failure diagnosis/);
  assert.match(markdown, /- Expected: restore reconnects the same provider conversation id/);
  assert.match(markdown, /- Inspect: `backend\/internal\/adapters\/chatdriver\/codexappserver\/driver.go`/);
});

test("parseArgs selects capability suites", () => {
  assert.deepEqual(parseArgs([]).suites, ["extras", "chat", "interface"]);
  assert.deepEqual(parseArgs(["--suites", "chat"]).suites, ["chat"]);
  assert.deepEqual(parseArgs(["--suites", "none"]).suites, []);
  assert.throws(() => parseArgs(["--suites", "chat,bogus"]), /unknown suite: bogus/);
});

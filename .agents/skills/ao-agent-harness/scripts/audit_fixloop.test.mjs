import assert from "node:assert/strict";
import test from "node:test";

import { diagnoseGate } from "./audit_capabilities.mjs";
import { MAX_ATTEMPTS, buildFixLoop, classifyFailure, compareAttempts, failureSignature } from "./audit_fixloop.mjs";
import { markdownReport, parseArgs, resolveOnly } from "./audit_ao.mjs";

const accepted = { send: { method: "POST", path: "/api/v1/sessions/s/send", status: 202, response: {} } };
const withDiagnosis = (item, agent = "codex") => ({ ...item, diagnosis: diagnoseGate(item, { agent }) });

test("classifyFailure separates user, environment, contract, AO, runner and provider failures", () => {
  const cases = [
    [{ name: "authentication", status: "BLOCKED", reason: "authentication state is unauthorized" }, "needs_user"],
    [{ name: "local_session_spawn", status: "FAIL", reason: "direct local CLI session failed", evidence: { authenticationRequired: true } }, "needs_user"],
    [{ name: "chat_spawn", status: "FAIL", reason: "AO did not create the Chat session", evidence: { method: "POST", path: "/api/v1/sessions", status: 402, response: { error: { message: "insufficient credits" } } } }, "needs_user"],
    [{ name: "startup", status: "FAIL", reason: "listen tcp 127.0.0.1:4100: EADDRINUSE" }, "environment"],
    [{ name: "runner_error", status: "FAIL", reason: "TypeError: Cannot read properties of undefined (reading 'gates')" }, "runner_bug"],
    [{ name: "cancellation", status: "FAIL", reason: "the cancellation turn settled or timed out without a valid command-start witness" }, "contract"],
    [{ name: "post_restore_message", status: "NOT_RUN", reason: "restored native terminal readiness contract is missing; no continuation was sent" }, "contract"],
    [{ name: "termination", status: "NOT_RUN", reason: "native session-ID prerequisite failed" }, "prerequisite"],
    [{ name: "hidden_ao_instructions", status: "FAIL", reason: "hidden AO standing-instruction token was not proven", evidence: accepted }, "ao_bug"],
    [{ name: "chat_restore", status: "FAIL", reason: "restore switched to a different provider conversation id" }, "ao_bug"],
    [{ name: "second_message", status: "FAIL", reason: "second message did not mutate the proof file", evidence: accepted }, "flaky_provider"],
  ];
  for (const [item, expected] of cases) assert.equal(classifyFailure(withDiagnosis(item)), expected, `${item.name}: ${item.reason}`);
  assert.equal(classifyFailure({ name: "chat_steer", status: "UNSUPPORTED", reason: "x" }), null);
});

test("a token miss that repeats after its retry is escalated to ao_bug", () => {
  const item = withDiagnosis({ name: "second_message", status: "FAIL", reason: "did not mutate", evidence: accepted });
  assert.equal(classifyFailure(item, { previous: { name: "second_message", status: "FAIL" } }), "ao_bug");
});

test("failureSignature ignores volatile tokens, ids and ports", () => {
  const a = { name: "chat_restore", status: "FAIL", reason: "token AO_CHAT_HISTORY_0123456789abcdef missing at 127.0.0.1:4100 after 1834 ms" };
  const b = { name: "chat_restore", status: "FAIL", reason: "token AO_CHAT_HISTORY_fedcba9876543210 missing at 127.0.0.1:5200 after 77 ms" };
  assert.equal(failureSignature(a), failureSignature(b));
  assert.notEqual(failureSignature(a), failureSignature({ ...a, reason: "restore was rejected" }));
});

function report(gates, agent = "codex") {
  return { startedAt: "t", state: "finished", results: [{ agent, gates: gates.map((item) => withDiagnosis(item, agent)) }] };
}

test("buildFixLoop orders actionable work, hands login to the user and builds reruns", () => {
  const loop = buildFixLoop(report([
    { name: "authentication", status: "BLOCKED", reason: "authentication state is unauthorized" },
    { name: "chat_interrupt", status: "FAIL", reason: "interrupt request was rejected" },
    { name: "startup", status: "FAIL", reason: "EADDRINUSE" },
    { name: "chat_kill", status: "NOT_RUN", reason: "prerequisite Chat step did not complete" },
    { name: "tui_spawn", status: "PASS", reason: "ok" },
  ]), { base: "/tmp/run 1/attempt-001", command: "node audit_ao.mjs --repo /src" });
  assert.equal(loop.status, "continue");
  assert.deepEqual(loop.items.map((item) => item.failureClass), ["needs_user", "environment", "ao_bug", "prerequisite"]);
  assert.equal(loop.next.gate, "startup");
  assert.equal(loop.userActions[0].agent, "codex");
  const interrupt = loop.items.find((item) => item.gate === "chat_interrupt");
  assert.ok(interrupt.fixTargets.includes("backend/internal/service/chat/controller.go"));
  assert.equal(interrupt.rerun, "node audit_ao.mjs --repo /src --agent codex --only chat_interrupt --previous '/tmp/run 1/attempt-001.json' --report '/tmp/run 1/attempt-001-chat_interrupt'");
  assert.equal(loop.rerunAll[0].command.includes("--only startup,chat_interrupt"), true);
  assert.equal(loop.items.find((item) => item.failureClass === "needs_user").rerun, null);
});

test("buildFixLoop stops after identical evidence or the attempt budget", () => {
  const failing = [{ name: "chat_restore", status: "FAIL", reason: "restore switched to a different provider conversation id" }];
  const first = report(failing);
  first.fixLoop = buildFixLoop(first, {});
  assert.equal(first.fixLoop.items[0].attempt, 1);
  assert.equal(first.fixLoop.status, "continue");

  const second = report(failing);
  const repeat = buildFixLoop(second, { previousReport: first });
  assert.equal(repeat.items[0].attempt, 2);
  assert.equal(repeat.items[0].repeated, true);
  assert.equal(repeat.items[0].exhausted, true, "same evidence after a fix stops the loop");
  assert.equal(repeat.status, "blocked");
  assert.match(repeat.items[0].action, /^Stop:/);

  let previous = first;
  for (let attempt = 2; attempt <= MAX_ATTEMPTS + 1; attempt += 1) {
    const next = report([{ ...failing[0], reason: `restore failed differently #${"x".repeat(attempt)}` }]);
    next.fixLoop = buildFixLoop(next, { previousReport: previous });
    assert.equal(next.fixLoop.items[0].attempt, attempt);
    assert.equal(next.fixLoop.items[0].exhausted, attempt > MAX_ATTEMPTS);
    previous = next;
  }
});

test("the planned flaky-provider retry is not counted as a repeated failed fix", () => {
  const miss = [{ name: "second_message", status: "FAIL", reason: "did not mutate", evidence: accepted }];
  const first = report(miss);
  first.fixLoop = buildFixLoop(first, {});
  assert.equal(first.fixLoop.items[0].failureClass, "flaky_provider");
  const retry = buildFixLoop(report(miss), { previousReport: first });
  assert.equal(retry.items[0].failureClass, "ao_bug");
  assert.equal(retry.items[0].exhausted, false);
});

test("compareAttempts reports fixed, still failing and regressed gates only where both attempts ran", () => {
  const before = report([
    { name: "chat_interrupt", status: "FAIL", reason: "x" },
    { name: "chat_restore", status: "FAIL", reason: "y" },
    { name: "tui_spawn", status: "PASS", reason: "ok" },
    { name: "chat_compact", status: "PASS", reason: "ok" },
  ]);
  const after = report([
    { name: "chat_interrupt", status: "PASS", reason: "ok" },
    { name: "chat_restore", status: "FAIL", reason: "y" },
    { name: "tui_spawn", status: "FAIL", reason: "broke" },
  ]);
  const changes = compareAttempts(after, before);
  assert.deepEqual(changes.fixed.map((item) => item.gate), ["chat_interrupt"]);
  assert.deepEqual(changes.stillFailing.map((item) => [item.gate, item.sameEvidence]), [["chat_restore", true]]);
  assert.deepEqual(changes.regressed.map((item) => item.gate), ["tui_spawn"]);
  assert.equal(compareAttempts(after, null), null);
});

test("resolveOnly plans the smallest run for gates and capability ids", () => {
  const chat = resolveOnly(["chat_interrupt"]);
  assert.equal(chat.needsTui, false);
  assert.deepEqual(chat.suites, ["chat"]);
  const capability = resolveOnly(["chat_approvals"]);
  assert.deepEqual([...capability.focus], ["chat_approval_reject", "chat_approval_approve"]);
  assert.equal(resolveOnly(["native_restore"]).needsTui, true);
  assert.deepEqual(resolveOnly(["iface_chat_continuation"]).suites, ["interface"]);
  assert.equal(resolveOnly(["iface_chat_continuation"]).needsTui, true);
  assert.throws(() => resolveOnly(["reviewer"]), /MANUAL/);
  assert.throws(() => resolveOnly(["bogus"]), /not a gate or capability id/);
  assert.equal(resolveOnly([]), null);
  assert.deepEqual([...parseArgs(["--only", "chat_steer,chat_compact"]).scope.focus], ["chat_steer", "chat_compact"]);
  assert.equal(parseArgs(["--previous", "/tmp/a.json"]).previous, "/tmp/a.json");
});

test("markdownReport shows changes and the next fix-loop actions", () => {
  const before = report([{ name: "chat_interrupt", status: "FAIL", reason: "x" }]);
  const now = report([{ name: "chat_interrupt", status: "PASS", reason: "ok" }, { name: "chat_restore", status: "FAIL", reason: "restore was rejected" }]);
  const markdown = markdownReport({
    ...now, summary: { status: "FAIL", total: 1, pass: 0, fail: 1, blocked: 0, notRun: 0 }, finishedAt: "f", disposableRoot: "/tmp", daemon: {},
    changes: compareAttempts(now, before),
    fixLoop: buildFixLoop(now, { previousReport: before, base: "/tmp/a2", command: "node r.mjs" }),
  });
  assert.match(markdown, /## Changes since previous attempt/);
  assert.match(markdown, /\| FIXED \| codex \| chat_interrupt \| FAIL → PASS \|/);
  assert.match(markdown, /## Next actions \(fix loop\)/);
  assert.match(markdown, /\| codex \| chat_restore \| ao_bug \| 1\/3 \|/);
  assert.match(markdown, /node r\.mjs --agent codex --only chat_restore/);
});

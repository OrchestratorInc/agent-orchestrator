import assert from "node:assert/strict";
import { execFileSync, spawn } from "node:child_process";
import { mkdirSync, mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

import { diagnoseGate } from "./audit_capabilities.mjs";
import { checkInstructionDelivery, expandArtifact, instructionSpec } from "./audit_instructions.mjs";

const TOKEN = "AO_HIDDEN_0123456789abcdef";
const CURSOR_RULE = "{dataDir}/cursor/acp-plugins/{sessionIdSha256}/rules/ao-standing.mdc";

function cursorLayout({ content = `---\nalwaysApply: true\n---\nFor hiddenInstructionToken use exactly: ${TOKEN}\n` } = {}) {
  const dataDir = mkdtempSync(join(tmpdir(), "ao-instr-"));
  const path = expandArtifact(CURSOR_RULE, { dataDir, sessionId: "s1" });
  mkdirSync(join(path, ".."), { recursive: true });
  if (content !== null) writeFileSync(path, content);
  return { dataDir, path };
}

test("specs come only from the harness contract, per surface or shared", () => {
  assert.equal(instructionSpec("codex", null, "tui"), null);
  assert.equal(instructionSpec("cursor", null, "tui"), null);
  const contract = { instructions: { tui: { channel: "none" }, chat: { channel: "rule-file", artifact: CURSOR_RULE } } };
  assert.equal(instructionSpec("cursor", contract, "tui").channel, "none");
  assert.equal(instructionSpec("cursor", contract, "chat").artifact, CURSOR_RULE);
  assert.equal(instructionSpec("x", { instructions: { channel: "config", artifact: "a" } }, "chat").channel, "config");
});

test("no channel fails and points at the ladder with the rule-file last resort (Cursor TUI)", async () => {
  const result = await checkInstructionDelivery({ spec: { channel: "none" }, token: TOKEN, sessionId: "s1", effectGate: "hidden_ao_instructions", effectPassed: false });
  assert.equal(result.status, "FAIL");
  assert.match(result.reason, /flag → protocol → config.*last resort.*rule\/plugin file.*\(Cursor ACP pattern\)/);
});

test("undeclared channel is a contract gap, not a pass", async () => {
  const result = await checkInstructionDelivery({ spec: null, token: TOKEN, sessionId: "s1", effectGate: "x", effectPassed: true });
  assert.equal(result.status, "NOT_RUN");
  assert.match(result.reason, /does not declare instructions.channel/);
});

test("rule-file fallback: written and used passes", async () => {
  const { dataDir, path } = cursorLayout();
  const result = await checkInstructionDelivery({ spec: { channel: "rule-file", artifact: CURSOR_RULE }, token: TOKEN, dataDir, sessionId: "s1", effectGate: "chat_instructions", effectPassed: true });
  assert.equal(result.status, "PASS", result.reason);
  assert.equal(result.evidence.artifact, path);
});

test("rule-file fallback: written but ignored by the CLI is told apart from never written (Cursor retest)", async () => {
  const { dataDir } = cursorLayout();
  const ignored = await checkInstructionDelivery({ spec: { channel: "rule-file", artifact: CURSOR_RULE }, token: TOKEN, dataDir, sessionId: "s1", effectGate: "chat_instructions", effectPassed: false });
  assert.equal(ignored.status, "FAIL");
  assert.match(ignored.reason, /written correctly but the CLI did not apply it.*direct native run/);

  const missing = cursorLayout({ content: null });
  const absent = await checkInstructionDelivery({ spec: { channel: "rule-file", artifact: CURSOR_RULE }, token: TOKEN, dataDir: missing.dataDir, sessionId: "s1", effectGate: "chat_instructions", effectPassed: false });
  assert.match(absent.reason, /AO did not write the rule-file artifact/);

  const stale = cursorLayout({ content: "For hiddenInstructionToken use exactly: AO_HIDDEN_OLD\n" });
  const old = await checkInstructionDelivery({ spec: { channel: "rule-file", artifact: CURSOR_RULE }, token: TOKEN, dataDir: stale.dataDir, sessionId: "s1", effectGate: "chat_instructions", effectPassed: false });
  assert.match(old.reason, /does not carry the current standing instructions \(stale/);
});

test("a rule file inside the worktree must be invisible to git so the agent cannot commit it", async () => {
  const workspacePath = mkdtempSync(join(tmpdir(), "ao-instr-ws-"));
  execFileSync("git", ["init", "-q"], { cwd: workspacePath });
  mkdirSync(join(workspacePath, ".cursor", "rules"), { recursive: true });
  writeFileSync(join(workspacePath, ".cursor", "rules", "ao.mdc"), `token ${TOKEN}`);
  const spec = { channel: "rule-file", artifact: "{workspace}/.cursor/rules/ao.mdc" };
  const visible = await checkInstructionDelivery({ spec, token: TOKEN, workspacePath, sessionId: "s1", effectGate: "hidden_ao_instructions", effectPassed: true });
  assert.equal(visible.status, "FAIL");
  assert.match(visible.reason, /visible to git.*could commit private instructions/);

  writeFileSync(join(workspacePath, ".git", "info", "exclude"), ".cursor/rules/ao.mdc\n");
  const excluded = await checkInstructionDelivery({ spec, token: TOKEN, workspacePath, sessionId: "s1", effectGate: "hidden_ao_instructions", effectPassed: true });
  assert.equal(excluded.status, "PASS", excluded.reason);
});

test("flag channel reads the live process argv and requires the regenerated prompt", { skip: process.platform === "win32" }, async () => {
  const sessionId = `flagtest${process.pid}`;
  const child = spawn(process.execPath, ["-e", "setTimeout(()=>{},20000)", "--", "--append-system-prompt-file", `/data/prompts/${sessionId}/system.md`], { stdio: "ignore" });
  try {
    await new Promise((done) => setTimeout(done, 300));
    const spec = { channel: "flag" };
    const passed = await checkInstructionDelivery({ spec, token: TOKEN, sessionId, effectGate: "hidden_ao_instructions", effectPassed: true });
    assert.equal(passed.status, "PASS", passed.reason);
    const ignored = await checkInstructionDelivery({ spec, token: TOKEN, sessionId, effectGate: "hidden_ao_instructions", effectPassed: false });
    assert.match(ignored.reason, /carries the system prompt but the agent did not use it/);
    const stale = await checkInstructionDelivery({ spec, token: TOKEN, sessionId, effectGate: "hidden_ao_instructions", effectPassed: false, promptText: "old rules only" });
    assert.match(stale.reason, /system.md does not carry the current standing instructions/);
  } finally { child.kill(); }
  const gone = await checkInstructionDelivery({ spec: { channel: "flag" }, token: TOKEN, sessionId: "no-such-session-xyz", effectGate: "hidden_ao_instructions", effectPassed: false });
  assert.match(gone.reason, /argv dropped it/);
});

test("instruction failures name the channel-specific first suspect", () => {
  const item = { name: "chat_instructions", status: "FAIL", reason: "instruction tokens mismatched" };
  assert.match(diagnoseGate(item, { agent: "cursor", instructionChannel: "rule-file" }).likelyCauses[0], /^rule-file channel: .*never loads it/);
  assert.match(diagnoseGate(item, { agent: "codex", instructionChannel: "protocol" }).likelyCauses[0], /^protocol channel: .*developer instructions/);
  assert.doesNotMatch(diagnoseGate({ name: "chat_kill", status: "FAIL", reason: "x" }, { instructionChannel: "flag" }).likelyCauses[0] ?? "", /channel/);
});

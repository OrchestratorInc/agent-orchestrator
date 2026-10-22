// Keeps the bundled Codex/Claude starter contract loadable and its restored
// ready cues honest. Frames come from AO's own adapter tests
// (claudecode_test.go, codex/terminal_activity_test.go).
import assert from "node:assert/strict";
import { dirname, join } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";
import { stripVTControlCharacters } from "node:util";

import { loadLocalContracts } from "./audit_ao.mjs";

const contractPath = join(dirname(fileURLToPath(import.meta.url)), "..", "assets", "local-contract.codex-claude.json");
const rule = `\x1b[38;5;244m${"─".repeat(48)}\x1b[39m`;

const FRAMES = {
  "claude-code": [
    ["empty composer", `${rule}\n\x1b[39m❯ \x1b[7m \x1b[0m\n${rule}\n⏵⏵ bypass permissions on`, true],
    ["draft", `${rule}\n❯ keep this draft\n${rule}\n⏵⏵ bypass permissions on`, false],
    ["active turn footer", `${rule}\n❯ \n${rule}\n esc to interrupt`, false],
  ],
  codex: [
    ["current placeholder", "› Ask Codex to do anything\n\nGPT-6-Sol medium · ~/project\n? for shortcuts\n", true],
    ["dim rotating placeholder", "\x1b[1m›\x1b[0m \x1b[2mWrite tests for @filename\x1b[0m\n\ngpt-5.6-sol low · ~/project\n", true],
    ["draft after placeholder text", "› Ask Codex to do anything else\n\nGPT-6-Sol medium · ~/project\n? for shortcuts\n", false],
    ["stale cue followed by output", "› Ask Codex to do anything\n\nGPT · ~/p\n? for shortcuts\nworking on it\nmore\nlines\n", false],
  ],
};

test("bundled contract loads and declares every field the live audit needs", async () => {
  const agents = await loadLocalContracts(contractPath);
  for (const id of ["codex", "claude-code"]) {
    const contract = agents[id];
    assert.ok(contract.binary && contract.versionArgs && contract.session?.args?.includes("{prompt}"), id);
    assert.equal(contract.interrupt, undefined, `${id} is stopped the AO way, by archiving the session`);
    assert.ok(contract.restoredReady.patterns.every((pattern) => pattern.length <= 4096), id);
    assert.equal(contract.instructions.tui.channel, "flag");
  }
  assert.deepEqual(agents.codex.models.args, ["debug", "models"]);
});

test("restored ready cues match an empty composer only", async () => {
  const agents = await loadLocalContracts(contractPath);
  for (const [id, frames] of Object.entries(FRAMES)) {
    const patterns = agents[id].restoredReady.patterns.map((source) => new RegExp(source));
    for (const [name, frame, ready] of frames) {
      assert.equal(patterns.every((pattern) => pattern.test(stripVTControlCharacters(frame))), ready, `${id}: ${name}`);
    }
  }
});

test("screen text keeps cursor-forward spacing and drops window-title sequences (live Codex restore frame)", async () => {
  const { screenText } = await import("./audit_ao.mjs");
  assert.equal(screenText("Conversation\x1b[1Cinterrupted"), "Conversation interrupted");
  assert.equal(screenText("› Ask Codex to do anything\x1b]0;⠹ Create AO audit proof file | audit-proof\x07\n"), "› Ask Codex to do anything\n");
});

// Keeps the bundled harness contracts and the template loadable, and the
// restored ready cues of the reference harnesses honest. Frames come from AO's own adapter tests
// (claudecode_test.go, codex/terminal_activity_test.go).
import assert from "node:assert/strict";
import { dirname, join } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";
import { stripVTControlCharacters } from "node:util";

import { loadLocalContracts } from "./audit_ao.mjs";
import { INSTRUCTION_CHANNELS } from "./audit_instructions.mjs";

const contractPath = join(dirname(fileURLToPath(import.meta.url)), "..", "assets", "local-contracts.json");
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

test("every bundled contract and the template declare the fields the live audit needs", async () => {
  const agents = { ...await loadLocalContracts(contractPath), ...await loadLocalContracts(join(dirname(contractPath), "contract-template.json")) };
  for (const [id, contract] of Object.entries(agents)) {
    assert.ok(contract.binary && contract.versionArgs && contract.session?.args?.includes("{prompt}"), id);
    assert.equal(contract.interrupt, undefined, `${id} is stopped the AO way, by archiving the session`);
    assert.ok(contract.restoredReady.patterns.every((pattern) => pattern.length <= 4096), id);
    assert.ok(Array.isArray(contract.surfaces) && contract.surfaces.length, `${id} lists its surfaces`);
    assert.ok(contract.adapters?.tui || contract.adapters?.chat, `${id} names its AO adapter directories`);
    for (const surface of contract.surfaces) {
      assert.ok(INSTRUCTION_CHANNELS.includes(contract.instructions?.[surface]?.channel), `${id} declares its ${surface} instruction channel`);
    }
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

// How AO's standing instructions (system prompt) reach the native agent, and
// how to prove each delivery channel. A token proven in the agent's output
// shows the effect; these checks show the mechanism, so a failure says
// whether AO never delivered the prompt or the CLI ignored what AO delivered.
//
// Channel ladder, best first. Use the first one the native CLI supports:
//   flag       native system-prompt flag or file argument
//              (Claude --append-system-prompt-file, Codex -c model_instructions_file=)
//   protocol   Chat session parameter (Claude ACP _meta.systemPrompt.append,
//              Codex app-server developer instructions)
//   config     native config/env file that the CLI loads additively
//   rule-file  LAST RESORT: a session-scoped rule/plugin file the CLI
//              auto-applies (the Cursor ACP pattern: --plugin-dir with an
//              alwaysApply rule). Only when nothing above exists, and only
//              with fresh and restored token proof that the CLI loads it.
//   none       no channel: AO must say so and fail closed, never append the
//              private instructions to the visible user prompt.

import { execFile } from "node:child_process";
import { createHash } from "node:crypto";
import { readFile } from "node:fs/promises";
import { isAbsolute, relative, resolve } from "node:path";

export const INSTRUCTION_CHANNELS = Object.freeze(["flag", "protocol", "config", "rule-file", "none"]);

// Contract shape: `instructions: { channel, artifact }` for both surfaces, or
// `instructions: { tui: {...}, chat: {...} }` per surface. Every harness
// declares its own; an undeclared channel is reported as a contract gap.
export function instructionSpec(agent, contract, surface) {
  const declared = contract?.instructions;
  if (declared?.[surface]) return declared[surface];
  if (typeof declared?.channel === "string") return declared;
  return null;
}

export function expandArtifact(template, { dataDir = "", workspacePath = "", sessionId = "" }) {
  return template
    .replaceAll("{dataDir}", dataDir)
    .replaceAll("{workspace}", workspacePath)
    .replaceAll("{sessionIdSha256}", createHash("sha256").update(sessionId).digest("hex"))
    .replaceAll("{sessionId}", sessionId);
}

function run(command, args, cwd) {
  return new Promise((done) => {
    execFile(command, args, { cwd, encoding: "utf8", timeout: 10_000, maxBuffer: 16 * 1024 * 1024 }, (error, stdout, stderr) => {
      done({ ok: !error, stdout: stdout ?? "", stderr: stderr ?? "" });
    });
  });
}

// The command lines of every process that names this session's prompt
// directory or carries the token inline. POSIX only.
async function processesCarrying({ sessionId, token }) {
  if (process.platform === "win32") return null;
  const listed = await run("ps", ["-axww", "-o", "args="]);
  if (!listed.ok) return null;
  return listed.stdout.split("\n").filter((line) => line.includes(`prompts/${sessionId}/`) || (token && line.includes(token)));
}

const channelLabel = (spec) => spec?.channel ?? "undeclared";

/**
 * Proves one delivery of `token`. `effectPassed` is whether the matching
 * effect gate (agent used the token) passed, so a written-but-ignored file is
 * told apart from a file that was never written.
 */
export async function checkInstructionDelivery({ spec, token, dataDir, workspacePath, sessionId, effectGate, effectPassed, promptText = null }) {
  const evidence = { channel: channelLabel(spec), tokenSha256: createHash("sha256").update(token).digest("hex") };
  if (!spec || !INSTRUCTION_CHANNELS.includes(spec.channel)) {
    return { status: "NOT_RUN", reason: "contract does not declare instructions.channel (flag, protocol, config, rule-file or none)", evidence };
  }
  if (spec.channel === "none") {
    return {
      status: "FAIL",
      reason: "no instruction channel: AO's standing instructions never reach this agent. Walk the ladder (flag → protocol → config); as a last resort write a session-scoped rule/plugin file the CLI auto-applies (Cursor ACP pattern) and prove it with fresh and restored tokens",
      evidence,
    };
  }
  if (spec.channel === "protocol") {
    return {
      status: effectPassed ? "PASS" : "FAIL",
      reason: effectPassed ? `delivered as a Chat session parameter; effect proven by ${effectGate}` : `Chat session parameter not consumed (${effectGate} failed): check the driver sends the system prompt at session start and on load`,
      evidence,
    };
  }
  if (spec.channel === "flag") {
    const lines = await processesCarrying({ sessionId, token });
    if (lines === null) return { status: "NOT_RUN", reason: "process arguments are unreadable on this platform", evidence };
    evidence.matchingProcesses = lines.length;
    if (promptText !== null && !promptText.includes(token)) {
      return { status: "FAIL", reason: "the session's system.md does not carry the current standing instructions; AO did not regenerate the prompt (e.g. on restore)", evidence };
    }
    if (!lines.length) {
      return { status: "FAIL", reason: "no running process for this session carries the system-prompt flag or file; the launch/restore argv dropped it", evidence };
    }
    return {
      status: effectPassed ? "PASS" : "FAIL",
      reason: effectPassed ? "launch argv carries the session system prompt and the agent used it"
        : `launch argv carries the system prompt but the agent did not use it (${effectGate} failed): the CLI may ignore or override this flag`,
      evidence,
    };
  }
  // config and rule-file: an artifact AO writes for the CLI to load.
  if (typeof spec.artifact !== "string" || !spec.artifact) {
    return { status: "NOT_RUN", reason: `instructions.artifact is required for channel ${spec.channel} (path template with {dataDir}, {workspace}, {sessionId}, {sessionIdSha256})`, evidence };
  }
  const path = resolve(expandArtifact(spec.artifact, { dataDir, workspacePath, sessionId }));
  evidence.artifact = path;
  let text;
  try { text = await readFile(path, "utf8"); } catch (error) {
    return { status: "FAIL", reason: `AO did not write the ${spec.channel} artifact (${error.code ?? error.message})`, evidence };
  }
  if (!text.includes(token)) {
    return { status: "FAIL", reason: `the ${spec.channel} artifact exists but does not carry the current standing instructions (stale, or written from the wrong prompt)`, evidence };
  }
  if (workspacePath) {
    const inside = relative(resolve(workspacePath), path);
    if (inside && !inside.startsWith("..") && !isAbsolute(inside)) {
      const status = await run("git", ["status", "--porcelain", "--", inside], resolve(workspacePath));
      evidence.gitStatus = status.stdout.trim();
      if (status.stdout.trim()) {
        return { status: "FAIL", reason: `the ${spec.channel} artifact is inside the worktree and visible to git (${inside}); the agent could commit private instructions. Write it under the AO data dir or add it to .git/info/exclude`, evidence };
      }
    }
  }
  return {
    status: effectPassed ? "PASS" : "FAIL",
    reason: effectPassed ? `${spec.channel} artifact carries the instructions and the agent used them`
      : `the ${spec.channel} artifact is written correctly but the CLI did not apply it (${effectGate} failed). Prove with a direct native run that the CLI loads it from this path (plugin/rule discovery flag, alwaysApply); Cursor ignored AO's rule in the 27 Sep retest`,
    evidence,
  };
}

// Channel-specific first suspects for an instruction failure.
export const CHANNEL_CAUSES = Object.freeze({
  flag: "launch or restore argv lacks the system-prompt flag (--append-system-prompt-file / -c model_instructions_file=)",
  protocol: "Chat driver did not send the system prompt as a session parameter (Claude _meta.systemPrompt.append, Codex developer instructions), or dropped it on session/load",
  config: "native config/env file not written, or overridden by the user's own config/.env (Hermes dotenv replaced AO instructions)",
  "rule-file": "rule/plugin file written but the CLI never loads it from that path (Cursor retest); verify its plugin/rule discovery flag with a native repro",
  none: "no delivery channel; follow the ladder, with a session-scoped rule/plugin file as the last resort",
});

// Fix loop support: classify each non-pass gate so an agent knows whether it
// may act, compare an attempt with the previous one, and write the
// machine-readable next actions an agent iterates on. Pure data; the runner
// owns all I/O.
//
// Classes, in the order an agent should handle them:
//   needs_user      login, credentials, quota, paid account, installing the real
//                   agent: stop this agent and hand the exact step to the user
//   unsupported_upstream  the native CLI itself lacks it, proven by a direct
//                   native repro and recorded in the contract's knownUpstream:
//                   keep the FAIL, link the upstream issue, stop retrying
//   environment     host, build, port, missing tool: fix the isolated setup
//   contract        local CLI contract (interrupt input, ready cue, args)
//   ao_bug          AO code or wiring: fix it with a regression test
//   runner_bug      the audit itself is wrong: fix only with a proving fixture
//   flaky_provider  model ignored a tokenised instruction once: retry once
//   prerequisite    NOT_RUN because an earlier gate failed: fix that gate first
//
// Classification is a heuristic over gate name, status and evidence text. It
// is a starting point for the agent, not a verdict; the rules in SKILL.md
// ("Fix loop") bound what an agent may change.

import { createHash } from "node:crypto";
import { capabilityForGate, inspectPaths } from "./audit_capabilities.mjs";

export const FAILURE_CLASSES = Object.freeze(["needs_user", "unsupported_upstream", "environment", "contract", "ao_bug", "runner_bug", "flaky_provider", "prerequisite"]);

// A fix attempt budget per gate; after this, or after the same failure
// repeats with identical evidence, the loop stops with a blocker.
export const MAX_ATTEMPTS = 3;

// Provider quota (HTTP 429, "Weekly/Monthly Limit Exhausted") and native
// account gates ("take over" another active session) need the user too: no
// code change fixes them (#6003, #6517, #6541, #6553).
const NEEDS_USER_TEXT = /\b(not (?:logged|signed) in|log ?in|sign ?in|unauthori[sz]ed|authentication (?:required|is not verified|state is (?:unauthorized|configured|unknown))|credentials configured, but|invalid (?:api key|credentials)|expired (?:token|credentials)|api key|subscription|billing|payment required|quota|insufficient credits|limit exhausted|rate.?limit(?:ed)?|HTTP 4(?:01|03|29)\b|\b429\b|-32000|take ?over|executable is not installed|not_installed)\b/i;
const ENVIRONMENT_TEXT = /\b(EADDRINUSE|ECONNREFUSED|ENOENT|EACCES|spawn \S+ ENOENT|sqlite3|go: |go build|build failed|compile|cannot find module|readyz|startup|timed out waiting for AO|no response|disk|ENOSPC|GOWORK)\b/i;
const RUNNER_TEXT = /\b(TypeError|ReferenceError|SyntaxError|RangeError|is not a function|Cannot read properties|undefined is not)\b/;
const CONTRACT_TEXT = /\b(contract|interrupt input|readiness contract|ready cue|composer cue|restoredReady|witness contract)\b/i;

// Gates whose evidence is a model-authored token in a proof file. A single
// miss after an accepted request can be the model, not AO; a repeat is real.
const TOKEN_GATES = new Set([
  "proof_file_creation", "initial_prompt_exactly_once", "second_message", "post_restore_message",
  "chat_initial_turn", "chat_followup", "chat_post_interrupt_continuation",
  "chat_steer", "chat_compact", "iface_tui_continuation",
]);

// Gates where a token miss usually means AO did not deliver the instruction
// or history, so they are AO bugs from the first failure.
const DELIVERY_GATES = new Set([
  "hidden_ao_instructions", "project_agents_md", "chat_instructions",
  "instruction_channel", "chat_instruction_channel",
  "role_worker", "role_orchestrator",
  "history_file_continuity", "chat_restore_continuation", "iface_chat_continuation",
]);

const ENVIRONMENT_GATES = new Set(["host_preflight", "startup", "startup_cleanup", "discovery", "configuration", "project_registration", "cleanup_termination", "evidence_write", "local_version"]);
const CONTRACT_GATES = new Set(["local_integration", "local_session_spawn", "local_models_list", "post_cancel_draft_cleanup", "post_restore_terminal_ready"]);

function text(item) {
  return [item.reason, ...(item.diagnosis?.observed ?? [])].filter(Boolean).join(" \n ");
}

function acceptedRequest(item) {
  return (item.diagnosis?.observed ?? []).some((line) => /→ HTTP 2\d\d/.test(line));
}

export function classifyFailure(item, { previous = null, knownUpstream = null } = {}) {
  if (!item || item.status === "PASS" || item.status === "UNSUPPORTED") return null;
  const body = text(item);
  if (item.status !== "NOT_RUN" && knownUpstream?.[item.name]) return "unsupported_upstream";
  if (item.status === "NOT_RUN") {
    if (CONTRACT_TEXT.test(body) && /missing|does not declare|is required/i.test(body)) return "contract";
    return "prerequisite";
  }
  if (item.userAction || item.name === "authentication" || item.evidence?.authenticationRequired || NEEDS_USER_TEXT.test(body)) return "needs_user";
  if (item.name === "local_binary" || item.name === "installed") return "needs_user";
  if (item.name === "runner_error" && RUNNER_TEXT.test(body)) return "runner_bug";
  if (ENVIRONMENT_GATES.has(item.name) || item.name === "runner_error" || ENVIRONMENT_TEXT.test(body)) return "environment";
  if (item.name === "cancellation") {
    // Archive path: a command that outlives the archived session is AO's to
    // fix; an agent that never started the command is retried once.
    if (/after the session was archived|appeared after archive|archive did not terminate/.test(body)) return "ao_bug";
    if (/never started the witnessed/.test(body)) return "flaky_provider";
    return "contract";
  }
  if (CONTRACT_GATES.has(item.name) || (item.status === "BLOCKED" && CONTRACT_TEXT.test(body))) return "contract";
  if (DELIVERY_GATES.has(item.name)) return "ao_bug";
  if (TOKEN_GATES.has(item.name) && acceptedRequest(item)) {
    // A second identical miss is no longer the model's fault.
    return previous && previous.status !== "PASS" ? "ao_bug" : "flaky_provider";
  }
  return "ao_bug";
}

const ACTIONS = Object.freeze({
  needs_user: "Stop this agent. Hand the user the exact step (for example `! <cli> login` in the audit account), then rerun with a fresh report. Never log in, enter credentials or reinstall the user's agent yourself.",
  environment: "Fix the isolated setup (rebuild AO with GOWORK=off, free/choose a port, install the missing tool in the audit environment), then rerun the same scope.",
  contract: "Correct the local CLI contract (interrupt input, restoredReady cue, session/version/model args) from the native CLI's real behavior, then rerun the affected gates.",
  ao_bug: "Fix the AO code in the files listed under fixTargets, add a regression test that fails without the fix, rebuild, then rerun only this capability.",
  runner_bug: "Prove the runner is wrong with a fixture in scripts/*.test.mjs first; only then change the runner. Never relax a gate to make it pass.",
  flaky_provider: "Rerun this capability once with fresh tokens. If it fails again it is reclassified as ao_bug.",
  unsupported_upstream: "Known native limitation (see the contract's knownUpstream note). Keep the FAIL, link the upstream issue in the PR, make AO refuse or guard it truthfully, and do not retry until a new native version ships.",
  prerequisite: "Skip: an earlier gate stopped this one. Fix the first failing gate in the chain.",
});

// Evidence signature for "same failure again": volatile tokens, ids, ports,
// timestamps and durations are normalised so only the failure shape remains.
export function failureSignature(item) {
  const normalised = [item.name, item.status, text(item)].join("|")
    .replace(/\b(AO_[A-Z_]+_[0-9a-f]{8,}|[0-9a-f]{16,}|[0-9a-f-]{36})\b/gi, "<id>")
    .replace(/\b\d+(\.\d+)?\b/g, "<n>")
    .replace(/(127\.0\.0\.1|localhost):<n>/g, "<host>");
  return createHash("sha256").update(normalised).digest("hex").slice(0, 16);
}

function previousIndex(previousReport) {
  const gates = new Map();
  const loop = new Map();
  for (const result of previousReport?.results ?? []) {
    for (const item of result.gates ?? []) gates.set(`${result.agent}/${item.name}`, item);
  }
  for (const entry of previousReport?.fixLoop?.items ?? []) loop.set(`${entry.agent}/${entry.gate}`, entry);
  return { gates, loop };
}

const STATUS_RANK = { PASS: 0, UNSUPPORTED: 0, NOT_RUN: 1, BLOCKED: 2, FAIL: 3 };

// Compares this attempt with the previous report. Only gates recorded in both
// are compared, so a narrower --only rerun never reports skipped gates as
// regressions.
export function compareAttempts(report, previousReport) {
  if (!previousReport) return null;
  const { gates: before } = previousIndex(previousReport);
  const changes = { fixed: [], stillFailing: [], regressed: [], newlyFailing: [], improved: [] };
  for (const result of report.results ?? []) {
    for (const item of result.gates ?? []) {
      const key = `${result.agent}/${item.name}`;
      const old = before.get(key);
      const now = STATUS_RANK[item.status] ?? 1;
      const entry = { agent: result.agent, gate: item.name, before: old?.status ?? null, after: item.status };
      if (!old) { if (now > 1) changes.newlyFailing.push(entry); continue; }
      const was = STATUS_RANK[old.status] ?? 1;
      if (was > 0 && now === 0) changes.fixed.push(entry);
      else if (was === 0 && now > 1) changes.regressed.push(entry);
      else if (was > 1 && now > 1) changes.stillFailing.push({ ...entry, sameEvidence: failureSignature(old) === failureSignature(item) });
      else if (now < was) changes.improved.push(entry);
    }
  }
  return {
    previousStartedAt: previousReport.startedAt ?? null,
    ...changes,
  };
}

// `command` is the runner invocation without agent/scope/report flags, e.g.
// "node scripts/audit_ao.mjs --repo /src/ao --local-contract c.json". Each
// rerun gets a fresh report basename next to this attempt's.
function rerunCommand({ command, agent, gates, base }) {
  const label = gates.length === 1 ? gates[0] : "retry";
  return `${command} --agent ${agent} --only ${gates.join(",")} --previous ${shellQuote(`${base}.json`)} --report ${shellQuote(`${base}-${label}`)}`;
}

export function shellQuote(value) {
  return /^[\w./:@=,+-]+$/.test(value) ? value : `'${String(value).replaceAll("'", "'\\''")}'`;
}

// Builds the fix-loop plan for this attempt: one item per non-pass gate that
// is actionable, with its class, the action, where to fix it, how to rerun it,
// and whether its attempt budget is spent.
export function buildFixLoop(report, { previousReport = null, base = "report", command = "node scripts/audit_ao.mjs", agents = [] } = {}) {
  const { gates: before, loop: priorLoop } = previousIndex(previousReport);
  const items = [];
  const userActions = [];
  for (const result of report.results ?? []) {
    for (const item of result.gates ?? []) {
      const key = `${result.agent}/${item.name}`;
      const previous = before.get(key) ?? null;
      const failureClass = classifyFailure(item, { previous, knownUpstream: result.knownUpstream });
      if (!failureClass) continue;
      const prior = priorLoop.get(key);
      const signature = failureSignature(item);
      // Same evidence after a fix means the fix did nothing: stop instead of
      // guessing again. The one exception is the planned flaky-provider retry,
      // which repeats without a fix by design.
      const repeated = Boolean(prior && prior.signature === signature);
      const attempt = prior ? prior.attempt + 1 : 1;
      const capability = capabilityForGate(item.name);
      const exhausted = !["needs_user", "prerequisite", "unsupported_upstream"].includes(failureClass)
        && (attempt > MAX_ATTEMPTS || (repeated && prior.failureClass !== "flaky_provider"));
      const entry = {
        agent: result.agent,
        gate: item.name,
        status: item.status,
        failureClass,
        capability: capability?.id ?? null,
        reason: item.reason,
        ...(failureClass === "unsupported_upstream" ? { upstream: result.knownUpstream[item.name] } : {}),
        action: exhausted ? `Stop: attempt budget spent or the same evidence repeated. Report a blocker with ${item.evidencePath ?? "the issue file"} and the attempted fixes.` : ACTIONS[failureClass],
        fixTargets: failureClass === "ao_bug" ? (item.diagnosis?.inspect ?? inspectPaths(capability, result.agent))
          : failureClass === "contract" ? ["the --local-contract JSON for this agent", "references/local-cli-contract.md"]
          : failureClass === "runner_bug" ? [".agents/skills/ao-agent-harness/scripts/"] : [],
        likelyCauses: item.diagnosis?.likelyCauses ?? [],
        evidencePath: item.evidencePath ?? null,
        signature,
        attempt,
        repeated,
        exhausted,
        rerun: ["prerequisite", "needs_user", "unsupported_upstream"].includes(failureClass) ? null : rerunCommand({ command, agent: result.agent, gates: [item.name], base }),
      };
      items.push(entry);
      if (failureClass === "needs_user" && !userActions.some((action) => action.agent === result.agent)) {
        userActions.push({ agent: result.agent, gate: item.name, message: result.userAction?.message ?? item.userAction?.message ?? `${result.agent}: ${item.reason}` });
      }
    }
  }
  const order = (entry) => FAILURE_CLASSES.indexOf(entry.failureClass);
  items.sort((a, b) => order(a) - order(b));
  // Run-level failures (host preflight, build, startup) stop every agent, so
  // they come first.
  for (const error of report.runErrors ?? []) {
    const item = { name: error.stage, status: "FAIL", reason: error.message };
    const failureClass = classifyFailure(item) ?? "environment";
    items.unshift({
      agent: null, gate: error.stage, status: "FAIL", failureClass, capability: null, reason: error.message,
      action: ACTIONS[failureClass], fixTargets: [], likelyCauses: [], evidencePath: null,
      signature: failureSignature(item), attempt: 1, repeated: false, exhausted: false,
      // A run-level failure stopped every agent: rerun the whole selection.
      rerun: failureClass === "needs_user" ? null
        : `${command} ${agents.length ? agents.map((agent) => `--agent ${agent}`).join(" ") : "--all"} --previous ${shellQuote(`${base}.json`)} --report ${shellQuote(`${base}-retry`)}`,
    });
  }
  const actionable = items.filter((entry) => !entry.exhausted && !["needs_user", "prerequisite", "unsupported_upstream"].includes(entry.failureClass));
  // One rerun per agent covering all of its actionable gates, for when the
  // agent fixed several things before rerunning.
  const rerunAll = [...new Set(actionable.filter((entry) => entry.agent).map((entry) => entry.agent))].map((agent) => ({
    agent, command: rerunCommand({ command, agent, gates: actionable.filter((entry) => entry.agent === agent).map((entry) => entry.gate), base }),
  }));
  return {
    schemaVersion: 1,
    maxAttempts: MAX_ATTEMPTS,
    rerunAll,
    // The agent may keep iterating only while actionable work remains.
    // done: nothing left the agent can change. Upstream-only leftovers still
    // need a maintainer decision, recorded in the PR.
    status: actionable.length ? "continue" : userActions.length ? "needs_user" : items.some((entry) => entry.exhausted) ? "blocked"
      : items.some((entry) => entry.failureClass === "unsupported_upstream") ? "upstream_decision" : "done",
    next: actionable[0] ?? null,
    userActions,
    items,
  };
}

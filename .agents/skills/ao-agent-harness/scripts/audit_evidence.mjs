import { appendFile, lstat, mkdir, readFile, writeFile } from "node:fs/promises";
import { homedir } from "node:os";
import { dirname, join } from "node:path";

const secretKey = /^(?:authorization|cookie|set-cookie|password|secret|credential|api[_-]?key|access[_-]?token|refresh[_-]?token|id[_-]?token|token)$/i;

export function redactOutput(value) {
  return String(value)
    .replaceAll(homedir(), "~")
    .replace(/\b(?:sk|key|token)-[A-Za-z0-9._-]{8,}\b/gi, "[REDACTED]")
    .replace(/\bBearer\s+[^\s"'<>]+/gi, "Bearer [REDACTED]")
    .replace(/((?:api[_-]?key|access[_-]?token|refresh[_-]?token|id[_-]?token|token|password|secret|credential|authorization|cookie)\s*["']?\s*[:=]\s*["']?)[^\s"'&,;}]+/gi, "$1[REDACTED]")
    .replace(/(https?:\/\/)[^\s/@]+:[^\s/@]+@/gi, "$1[REDACTED]@")
    .replace(/[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}/gi, "[REDACTED_EMAIL]");
}

export function sanitizeEvidence(value) {
  if (typeof value === "string") return redactOutput(value);
  if (Array.isArray(value)) return value.map(sanitizeEvidence);
  if (value && typeof value === "object") {
    return Object.fromEntries(Object.entries(value).map(([key, item]) => [key,
      secretKey.test(key) ? "[REDACTED]" : sanitizeEvidence(item)]));
  }
  return value;
}

export function authenticationAction(agent, gate) {
  const aoBlocked = gate.name === "authentication" && gate.status === "BLOCKED";
  const localLoginError = gate.name.startsWith("local_") && gate.status === "FAIL" && gate.evidence?.authenticationRequired;
  if (!aoBlocked && !localLoginError) return undefined;
  return {
    type: "authentication", agent, requiresFreshProbe: true,
    message: `${agent}: ${localLoginError ? "the local CLI reported a login error" : "authentication is not verified"} (${redactOutput(gate.reason)}). Please verify or sign in using the provider's official CLI/app in the audit user's account, then rerun this agent. Do not send passwords, tokens, or OTPs in chat.`,
  };
}

// One directory per run. The host supplies capture; no browser framework is bundled.
export async function createEvidenceStore({ base, captureCommand = [], runCommand }) {
  const directory = `${base}.artifacts`;
  const paths = {
    directory,
    runnerLog: join(directory, "runner.log"),
    daemonLog: join(directory, "daemon.log"),
    events: join(directory, "events.jsonl"),
  };
  await mkdir(dirname(base), { recursive: true });
  // Refuse reuse so a rerun cannot silently destroy earlier failure evidence.
  await mkdir(directory, { mode: 0o700 });
  // Reserve both outputs before any daemon/provider can start; never follow an
  // existing report symlink or overwrite an earlier run without its artifacts.
  for (const extension of ["json", "md"]) {
    await writeFile(`${base}.${extension}`, "", { flag: "wx", mode: 0o600 });
  }
  for (const path of [paths.runnerLog, paths.daemonLog, paths.events]) {
    await writeFile(path, "", { mode: 0o600 });
  }
  const issues = [];
  const event = async data => appendFile(paths.events, `${JSON.stringify(data)}\n`);
  const log = async message => {
    const line = `${new Date().toISOString()} ${redactOutput(message)}`;
    await appendFile(paths.runnerLog, `${line}\n`);
    console.error(line);
  };
  const daemonLog = async logs => writeFile(paths.daemonLog, redactOutput(logs).slice(-65_536), { mode: 0o600 });
  const issue = async ({ agent = null, sessionId = null, baseURL = null, gate, daemonLogs = "" }) => {
    const id = String(issues.length + 1).padStart(4, "0");
    const evidencePath = join(directory, `issue-${id}.json`);
    const screenshotPath = join(directory, `issue-${id}.png`);
    const action = authenticationAction(agent, gate);
    const item = {
      id, recordedAt: new Date().toISOString(), agent, sessionId, baseURL,
      gate: gate.name, status: gate.status, reason: redactOutput(gate.reason),
      evidence: sanitizeEvidence(gate.evidence), evidencePath,
      daemonLogTail: redactOutput(daemonLogs).slice(-65_536),
      screenshot: { status: "unavailable", reason: "Headless runner: no capture command configured." },
      ...(action ? { userAction: action } : {}),
    };
    issues.push(item);
    await writeFile(evidencePath, `${JSON.stringify(item, null, 2)}\n`, { mode: 0o600 });
    await daemonLog(daemonLogs);
    if (action) {
      await event({ ...action, type: "needs_user_action", actionType: action.type, issueId: id, evidencePath });
      await log(`NEEDS_USER_ACTION ${action.message}`);
    }
    if (captureCommand.length) {
      const outcome = await runCommand(captureCommand[0], [...captureCommand.slice(1), evidencePath, screenshotPath], { timeoutMs: 10_000 });
      try {
        if (!outcome.ok) throw new Error(outcome.timedOut ? "capture timed out after 10 seconds" : outcome.stderr || `capture exited ${outcome.code}`);
        const metadata = await lstat(screenshotPath);
        if (!metadata.isFile() || metadata.size > 10 * 1024 * 1024) throw new Error("capture must produce a regular PNG file no larger than 10 MiB");
        const bytes = await readFile(screenshotPath);
        if (!bytes.subarray(0, 8).equals(Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]))) throw new Error("capture did not produce a PNG");
        item.screenshot = { status: "captured", path: screenshotPath, capturedAt: new Date().toISOString() };
      } catch (error) {
        item.screenshot = { status: "failed", reason: redactOutput(error.message).slice(-8_192) };
      }
      await writeFile(evidencePath, `${JSON.stringify(item, null, 2)}\n`, { mode: 0o600 });
    }
    await event({ type: "issue", ...item });
    await log(`${agent || "runner"}: ${gate.name} ${gate.status}; evidence=${evidencePath}; screenshot=${item.screenshot.status}`);
    return item;
  };
  return { paths, issues, issue, log, daemonLog };
}

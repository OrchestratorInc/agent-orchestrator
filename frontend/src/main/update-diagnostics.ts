import { closeSync, constants, fchmodSync, fstatSync, mkdirSync, openSync, renameSync, rmSync, writeSync } from "node:fs";
import path from "node:path";

export const UPDATE_DIAGNOSTICS_FILE = "updater.jsonl";
export const UPDATE_DIAGNOSTICS_MAX_BYTES = 1024 * 1024;
type Fields = Record<string, string | number | boolean | null | undefined>;

// Local troubleshooting only: never forwarded to renderer telemetry. Preserve
// native error detail, but do not persist signed URLs or authorization headers.
function text(value: string, limit = 4096): string {
  return value
    .replace(/https?:\/\/[^\s<>"']+/gi, "[redacted-url]")
    .replace(/\b(?:authorization["']?\s*[:=]\s*["']?|bearer\s+)[^\r\n]+/gi, "[redacted-authorization]")
    .slice(0, limit);
}

function errorDetail(error: unknown, depth = 0): unknown {
  if (!(error instanceof Error)) return text(String(error));
  const native = error as Error & { code?: unknown; domain?: unknown };
  return {
    name: text(error.name, 128),
    message: text(error.message),
    stack: text(error.stack ?? ""),
    ...(native.code === undefined ? {} : { code: text(String(native.code), 128) }),
    ...(native.domain === undefined ? {} : { domain: text(String(native.domain), 256) }),
    ...(depth === 0 && error.cause !== undefined ? { cause: errorDetail(error.cause, depth + 1) } : {}),
  };
}

// Synchronous, bounded writes keep the last failure available even when AO
// immediately quits. All I/O fails open: diagnostics must never affect updates.
// Retain one previous file across both rotation and process launches.
export function createUpdateDiagnostics(stateDir: string, runningVersion: string) {
  const directory = path.join(stateDir, "logs");
  const file = path.join(directory, UPDATE_DIAGNOSTICS_FILE);
  return (event: string, fields: Fields = {}, error?: unknown): void => {
    let fd: number | undefined;
    try {
      const details = Object.fromEntries(Object.entries(fields).map(([key, value]) =>
        [key, typeof value === "string" ? text(value) : value]));
      const line = `${JSON.stringify({
        timestamp: new Date().toISOString(), pid: process.pid, uptimeMs: Math.round(process.uptime() * 1000),
        runningVersion: text(runningVersion, 128), platform: process.platform, arch: process.arch,
        event, ...details, ...(error === undefined ? {} : { error: errorDetail(error) }),
      })}\n`;
      // Call sites use small scalar records; reject an unexpectedly huge record
      // rather than allow either unbounded storage or a partial JSON line.
      if (Buffer.byteLength(line) > 64 * 1024) return;
      mkdirSync(directory, { recursive: true, mode: 0o700 });
      const open = () => openSync(file, constants.O_CREAT | constants.O_APPEND | constants.O_WRONLY | constants.O_NOFOLLOW, 0o600);
      fd = open();
      const stat = fstatSync(fd);
      if (!stat.isFile()) return;
      if (stat.size + Buffer.byteLength(line) > UPDATE_DIAGNOSTICS_MAX_BYTES) {
        closeSync(fd);
        fd = undefined;
        rmSync(`${file}.1`, { force: true });
        renameSync(file, `${file}.1`);
        fd = open();
      }
      fchmodSync(fd, 0o600);
      writeSync(fd, line);
    } catch {
      // No console recursion, renderer error, or change to updater control flow.
    } finally {
      if (fd !== undefined) {
        try { closeSync(fd); } catch { /* Diagnostics are best effort. */ }
      }
    }
  };
}

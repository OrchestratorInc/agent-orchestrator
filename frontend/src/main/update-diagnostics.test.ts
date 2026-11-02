// @vitest-environment node
import { afterEach, beforeEach, expect, it } from "vitest";
import { mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, statSync, symlinkSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import { createUpdateDiagnostics, UPDATE_DIAGNOSTICS_FILE, UPDATE_DIAGNOSTICS_MAX_BYTES } from "./update-diagnostics";

let stateDir: string;
beforeEach(() => { stateDir = mkdtempSync(path.join(os.tmpdir(), "ao-update-diagnostics-")); });
afterEach(() => { rmSync(stateDir, { force: true, recursive: true }); });
const logFile = () => path.join(stateDir, "logs", UPDATE_DIAGNOSTICS_FILE);
const readRecords = () => readFileSync(logFile(), "utf8").trim().split("\n").map(line => JSON.parse(line));

it("preserves records across process launches with version, process, and timestamp context", () => {
  createUpdateDiagnostics(stateDir, "1.0.0")("native-preparation-started", { targetVersion: "2.0.0", bytes: null });
  createUpdateDiagnostics(stateDir, "1.0.1")("retry-relaunch-requested");
  expect(readRecords()).toMatchObject([
    { runningVersion: "1.0.0", targetVersion: "2.0.0", bytes: null, pid: process.pid, timestamp: expect.any(String), uptimeMs: expect.any(Number) },
    { runningVersion: "1.0.1", event: "retry-relaunch-requested" },
  ]);
  if (process.platform !== "win32") expect(statSync(logFile()).mode & 0o777).toBe(0o600);
});

it("retains native error detail and cause while redacting network credentials", () => {
  const error = Object.assign(new Error("code signature rejected https://user:password@host/update?token=secret", {
    cause: new Error("ditto: missing file"),
  }), { code: -1, domain: "SQRLUpdaterErrorDomain" });
  createUpdateDiagnostics(stateDir, "1.0.0")("native-error", { note: "Authorization: Bearer secret", quotedHeader: '"Authorization": "token secret"' }, error);
  expect(readRecords()[0]).toMatchObject({
    note: "[redacted-authorization]",
    error: { name: "Error", message: "code signature rejected [redacted-url]", code: "-1", domain: "SQRLUpdaterErrorDomain", cause: { message: "ditto: missing file" } },
  });
  expect(readFileSync(logFile(), "utf8")).not.toMatch(/password|secret|user@/);
});

it("rotates bounded JSONL files without losing the newest failure", () => {
  const record = createUpdateDiagnostics(stateDir, "1.0.0");
  for (let i = 0; i < 600; i++) record("native-error", { sample: "x".repeat(4096), sequence: i });
  record("latest-failure", {}, new Error("last failure"));
  expect(readdirSync(path.dirname(logFile())).sort()).toEqual([UPDATE_DIAGNOSTICS_FILE, `${UPDATE_DIAGNOSTICS_FILE}.1`]);
  expect(statSync(logFile()).size).toBeLessThanOrEqual(UPDATE_DIAGNOSTICS_MAX_BYTES);
  expect(statSync(`${logFile()}.1`).size).toBeLessThanOrEqual(UPDATE_DIAGNOSTICS_MAX_BYTES);
  expect(readRecords().at(-1)).toMatchObject({ event: "latest-failure", error: { message: "last failure" } });
  for (const line of readFileSync(`${logFile()}.1`, "utf8").trim().split("\n")) expect(() => JSON.parse(line)).not.toThrow();
});

it("does not throw when the state directory cannot hold logs", () => {
  writeFileSync(path.join(stateDir, "logs"), "not a directory");
  expect(() => createUpdateDiagnostics(stateDir, "1.0.0")("native-error", {}, new Error("failure"))).not.toThrow();
});

it.skipIf(process.platform === "win32")("does not follow a log symlink", () => {
  const target = path.join(stateDir, "other-file");
  writeFileSync(target, "untouched");
  mkdirSync(path.dirname(logFile()));
  symlinkSync(target, logFile());
  createUpdateDiagnostics(stateDir, "1.0.0")("native-error");
  expect(readFileSync(target, "utf8")).toBe("untouched");
});

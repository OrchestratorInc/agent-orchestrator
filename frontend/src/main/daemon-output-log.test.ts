// @vitest-environment node
import { afterEach, beforeEach, expect, it } from "vitest";
import { mkdtempSync, readFileSync, readdirSync, rmSync, statSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import { createDaemonOutputLog, DAEMON_OUTPUT_LOG_FILE } from "./daemon-output-log";

let stateDir: string;
beforeEach(() => { stateDir = mkdtempSync(path.join(os.tmpdir(), "ao-daemon-output-log-")); });
afterEach(() => { rmSync(stateDir, { force: true, recursive: true }); });
const logFile = () => path.join(stateDir, "logs", DAEMON_OUTPUT_LOG_FILE);

it("keeps daemon output and AO markers, including a Go fatal error, across app launches", () => {
	createDaemonOutputLog(stateDir).mark("daemon spawned pid=41");
	const log = createDaemonOutputLog(stateDir);
	log.output("fatal error: concurrent map writes\n\ngoroutine 7 [running]:\n");
	log.mark("daemon pid=41 exited code=2 signal=null");
	const text = readFileSync(logFile(), "utf8");
	expect(text).toMatch(/AO: daemon spawned pid=41\n/);
	expect(text).toContain("fatal error: concurrent map writes\n\ngoroutine 7 [running]:\n");
	expect(text).toMatch(/AO: daemon pid=41 exited code=2 signal=null\n$/);
	if (process.platform !== "win32") expect(statSync(logFile()).mode & 0o777).toBe(0o600);
});

it("redacts URLs and authorization values", () => {
	createDaemonOutputLog(stateDir).output("GET https://host/x?token=secret Authorization: Bearer secret\n");
	expect(readFileSync(logFile(), "utf8")).not.toMatch(/secret/);
});

it("rotates to one previous file and keeps the newest output", () => {
	const log = createDaemonOutputLog(stateDir, 1024);
	for (let i = 0; i < 50; i++) log.output(`${"x".repeat(100)} ${i}\n`);
	log.output("last line\n");
	expect(readdirSync(path.dirname(logFile())).sort()).toEqual([DAEMON_OUTPUT_LOG_FILE, `${DAEMON_OUTPUT_LOG_FILE}.1`]);
	expect(statSync(logFile()).size).toBeLessThanOrEqual(1024);
	expect(readFileSync(logFile(), "utf8")).toMatch(/last line\n$/);
});

it("fails open when the state directory cannot be created", () => {
	const blocked = path.join(stateDir, "file");
	writeFileSync(blocked, "");
	expect(() => createDaemonOutputLog(blocked).mark("z")).not.toThrow();
	expect(readdirSync(stateDir)).toEqual(["file"]);
});

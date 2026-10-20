// @vitest-environment node
import { chmodSync, mkdtempSync, readFileSync, readdirSync, rmSync, statSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import { afterEach, describe, expect, it, vi } from "vitest";
import { createDesktopLogWriter, initializeDesktopLog } from "./desktop-log";

const dirs: string[] = [];
const dataDir = () => { const dir = mkdtempSync(path.join(os.tmpdir(), "desktop-log-test-")); dirs.push(dir); return dir; };
afterEach(() => { vi.restoreAllMocks(); for (const dir of dirs.splice(0)) rmSync(dir, { recursive: true, force: true }); });
describe("desktop.log", () => {
	it("writes private log entries under the data directory", () => {
		const dir = dataDir(); const write = createDesktopLogWriter(dir);
		write("warn", ["problem %s", "details", { reason: 1 }]);
		expect(readFileSync(path.join(dir, "desktop.log"), "utf8")).toContain("[warn] problem details { reason: 1 }");
		expect(statSync(path.join(dir, "desktop.log")).mode & 0o777).toBe(0o600);
	});
	it("caps entries at 5 MiB and keeps exactly one backup", () => {
		const dir = dataDir(); const write = createDesktopLogWriter(dir);
		write("log", ["x".repeat(6 * 1024 * 1024)]);
		expect(statSync(path.join(dir, "desktop.log")).size).toBe(5 * 1024 * 1024);
		write("warn", ["second"]); write("error", ["y".repeat(6 * 1024 * 1024)]);
		expect(readdirSync(dir).sort()).toEqual(["desktop.log", "desktop.log.1"]);
		expect(readFileSync(path.join(dir, "desktop.log.1"), "utf8")).toContain("second");
		for (const file of readdirSync(dir)) {
			expect(statSync(path.join(dir, file)).size).toBeLessThanOrEqual(5 * 1024 * 1024);
			expect(statSync(path.join(dir, file)).mode & 0o777).toBe(0o600);
		}
	});
	it("rotates an existing full log and tightens existing permissions", () => {
		const dir = dataDir(); const file = path.join(dir, "desktop.log");
		writeFileSync(file, Buffer.alloc(5 * 1024 * 1024)); chmodSync(file, 0o644);
		createDesktopLogWriter(dir)("log", ["next"]);
		expect(statSync(`${file}.1`).mode & 0o777).toBe(0o600);
		expect(readFileSync(file, "utf8")).toContain("next");
	});
	it("records console output and uncaught errors while retaining the original console", () => {
		const dir = dataDir();
		const originals = ["log", "warn", "error"].map((level) => vi.spyOn(console, level as "log").mockImplementation(() => undefined));
		const on = vi.spyOn(process, "on").mockReturnValue(process);
		initializeDesktopLog(dir);
		console.log("hello"); console.warn("warning"); console.error(new Error("failure"));
		const listener = on.mock.calls.find(([event]) => event === "uncaughtExceptionMonitor")?.[1];
		expect(listener).toBeDefined(); listener?.(new Error("uncaught"));
		const text = readFileSync(path.join(dir, "desktop.log"), "utf8");
		for (const word of ["hello", "warning", "failure", "uncaught"]) expect(text).toContain(word);
		for (const original of originals) expect(original).toHaveBeenCalledOnce();
	});
});

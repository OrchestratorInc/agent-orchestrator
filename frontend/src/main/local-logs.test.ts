// @vitest-environment node
import { EventEmitter } from "node:events";
import { mkdtempSync, readFileSync, readdirSync, rmSync, statSync, symlinkSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
	createRotatingLogWriter,
	displayLogsDirectory,
	installMainLog,
	logsDirectory,
	MAIN_LOG_FILE,
	redactLogText,
} from "./local-logs";

let dir: string;
beforeEach(() => { dir = mkdtempSync(path.join(os.tmpdir(), "ao-local-logs-")); });
afterEach(() => { rmSync(dir, { force: true, recursive: true }); });

describe("log paths", () => {
	it("puts logs next to running.json for packaged and dev state dirs", () => {
		const home = path.join(path.sep, "home", "u");
		expect(logsDirectory(path.join(home, ".ao"))).toBe(path.join(home, ".ao", "logs"));
		expect(logsDirectory(path.join(home, ".ao", "dev"))).toBe(path.join(home, ".ao", "dev", "logs"));
	});

	it("shortens the home directory for display only when the folder is inside it", () => {
		const home = path.join(path.sep, "home", "u");
		expect(displayLogsDirectory(path.join(home, ".ao", "logs"), home)).toBe(`~${path.sep}.ao${path.sep}logs`);
		expect(displayLogsDirectory(path.join(path.sep, "home", "uv", "logs"), home)).toBe(path.join(path.sep, "home", "uv", "logs"));
		expect(displayLogsDirectory(path.join(path.sep, "srv", "ao", "logs"), "")).toBe(path.join(path.sep, "srv", "ao", "logs"));
	});
});

describe("redactLogText", () => {
	it.each([
		["url userinfo", "clone https://bob:hunter2@github.com/o/r.git failed", "clone https://[redacted]@github.com/o/r.git failed"],
		["authorization header", '{"Authorization": "Bearer abc.def"}', '{"Authorization": "Bearer [redacted]"}'],
		["bare bearer", "sent Bearer eyabc123 upstream", "sent Bearer [redacted] upstream"],
		["query secret", "GET https://cp.example/x?access_token=abc&page=2", "GET https://cp.example/x?access_token=[redacted]&page=2"],
		["signed url", "https://s3.example/f?X-Amz-Signature=deadbeef&X-Amz-Expires=60", "https://s3.example/f?X-Amz-Signature=[redacted]&X-Amz-Expires=60"],
		["assignment", "refresh_token=abc123 status=401", "refresh_token=[redacted] status=401"],
		["json", '{"password":"pw","user":"u"}', '{"password":[redacted],"user":"u"}'],
		["github token", "token ghp_abcdefghijklmnopqrstuvwxyz0123 leaked", "token [redacted] leaked"],
		["openai key", "key sk-proj-abcdefgh1234", "key [redacted]"],
		["jwt", "id eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0In0.sig_part", "id [redacted]"],
		["counters stay", "input_tokens=12 output_tokens=4 token_count=9", "input_tokens=12 output_tokens=4 token_count=9"],
		["plain url stays", "cloud request failed: https://api.example.com/v1/runs 502", "cloud request failed: https://api.example.com/v1/runs 502"],
	])("%s", (_name, input, expected) => {
		expect(redactLogText(input)).toBe(expected);
	});

	it("shortens the home directory to ~", () => {
		expect(redactLogText("opened /Users/alice/.ao/data and /Users/alice/code", "/Users/alice")).toBe("opened ~/.ao/data and ~/code");
	});
});

describe("createRotatingLogWriter", () => {
	it("rotates whole entries and keeps only the configured backups", () => {
		const file = path.join(dir, "logs", "test.log");
		const write = createRotatingLogWriter(file, { maxBytes: 20, backups: 2 });
		for (const entry of ["record-1 aaaaa\n", "record-2 bbbbb\n", "record-3 ccccc\n", "record-4 ddddd\n"]) write(entry);
		expect(readFileSync(file, "utf8")).toBe("record-4 ddddd\n");
		expect(readFileSync(`${file}.1`, "utf8")).toBe("record-3 ccccc\n");
		expect(readFileSync(`${file}.2`, "utf8")).toBe("record-2 bbbbb\n");
		expect(readdirSync(path.dirname(file)).sort()).toEqual(["test.log", "test.log.1", "test.log.2"]);
		if (process.platform !== "win32") {
			expect(statSync(file).mode & 0o777).toBe(0o600);
			expect(statSync(path.dirname(file)).mode & 0o777).toBe(0o700);
		}
	});

	it("narrows an existing file to owner-only", () => {
		if (process.platform === "win32") return;
		const file = path.join(dir, "wide.log");
		writeFileSync(file, "old\n", { mode: 0o644 });
		createRotatingLogWriter(file, { maxBytes: 1024, backups: 1 })("new\n");
		expect(statSync(file).mode & 0o777).toBe(0o600);
	});

	it("does not follow a symlink or throw when it cannot write", () => {
		if (process.platform === "win32") return;
		const target = path.join(dir, "elsewhere");
		writeFileSync(target, "");
		const file = path.join(dir, "linked.log");
		symlinkSync(target, file);
		expect(() => createRotatingLogWriter(file, { maxBytes: 1024, backups: 1 })("secret\n")).not.toThrow();
		expect(readFileSync(target, "utf8")).toBe("");

		writeFileSync(path.join(dir, "blocked"), "not a directory");
		expect(() => createRotatingLogWriter(path.join(dir, "blocked", "x.log"), { maxBytes: 1024, backups: 1 })("x\n")).not.toThrow();
	});
});

describe("installMainLog", () => {
	function install() {
		const sink = { log: vi.fn(), info: vi.fn(), warn: vi.fn(), error: vi.fn() };
		const originals = { ...sink };
		const processRef = new EventEmitter();
		const mainLog = installMainLog({
			directory: path.join(dir, "logs"),
			homeDir: "/Users/alice",
			target: sink,
			processRef: processRef as unknown as Pick<NodeJS.Process, "on">,
			now: () => new Date("2026-11-04T10:00:00.000Z"),
		});
		const read = () => readFileSync(path.join(dir, "logs", MAIN_LOG_FILE), "utf8");
		return { sink, originals, processRef, mainLog, read };
	}

	it("keeps console output and appends timestamped, levelled, redacted entries", () => {
		const { sink, originals, read, mainLog } = install();
		sink.warn("AO: cloud request failed", { status: 502, url: "https://u:p@cp.example/v1?token=abc" });
		sink.error("AO: could not open /Users/alice/.ao/data", new Error("EACCES"));
		sink.info("AO: ready on port %d", 3001);
		expect(originals.warn).toHaveBeenCalledWith("AO: cloud request failed", { status: 502, url: "https://u:p@cp.example/v1?token=abc" });
		const text = read();
		expect(text).toContain("2026-11-04T10:00:00.000Z WARN  AO: cloud request failed { status: 502, url: 'https://[redacted]@cp.example/v1?token=[redacted]' }\n");
		expect(text).toContain("2026-11-04T10:00:00.000Z ERROR AO: could not open ~/.ao/data Error: EACCES");
		expect(text).toContain("2026-11-04T10:00:00.000Z INFO  AO: ready on port 3001\n");
		expect(text).not.toMatch(/alice|u:p@|token=abc/);
		expect(mainLog.file).toBe(path.join(dir, "logs", MAIN_LOG_FILE));
	});

	it("keeps output passed through skipFile out of the file but still on the console", () => {
		const { sink, originals, read, mainLog } = install();
		sink.log("kept");
		mainLog.skipFile(() => sink.error("daemon stderr line"));
		sink.log("kept again");
		expect(originals.error).toHaveBeenCalledWith("daemon stderr line");
		expect(read()).not.toContain("daemon stderr line");
		expect(read().trim().split("\n")).toHaveLength(2);
	});

	it("records uncaught exceptions without handling them", () => {
		const { processRef, read } = install();
		expect(processRef.listenerCount("uncaughtException")).toBe(0);
		processRef.emit("uncaughtExceptionMonitor", new Error("boom"), "uncaughtException");
		expect(read()).toContain("FATAL uncaughtException Error: boom");
	});
});

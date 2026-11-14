// @vitest-environment node
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { existsSync, mkdtempSync, readFileSync, rmSync, statSync, symlinkSync, writeFileSync, mkdirSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import {
	createLifecycleLog,
	errorFacts,
	LIFECYCLE_LOG_FILE,
	LIFECYCLE_RUN_MARKER_FILE,
	linuxProcessContext,
	markRunStarted,
	observeUnhandledRejections,
	scrubText,
} from "./lifecycle-log";

let dir: string;
beforeEach(() => { dir = mkdtempSync(path.join(os.tmpdir(), "ao-lifecycle-log-")); });
afterEach(() => { rmSync(dir, { force: true, recursive: true }); });
const logFile = () => path.join(dir, LIFECYCLE_LOG_FILE);
const readRecords = (file = logFile()) => readFileSync(file, "utf8").trim().split("\n").map((line) => JSON.parse(line));
const home = "/home/alice";

describe("scrubText", () => {
	it.each([
		["url", "fetch https://api.example.com/v1?key=abc failed", "api.example.com", "[url]"],
		["bearer", "Bearer sk-live-123456 rejected", "sk-live-123456", "[credential]"],
		["assignment", "token=ghp_abcdefghijklmnop invalid", "ghp_abcdefghijklmnop", "token=[redacted]"],
		["quoted multi-word value", 'login password="correct horse battery staple" rejected', "horse", 'password=[redacted] rejected'],
		["single-quoted value", "api_key: 'two secret words' then ok", "secret words", "api_key: [redacted] then ok"],
		["opaque", "id 0123456789abcdef0123456789abcdef01", "0123456789abcdef0123456789abcdef01", "[opaque]"],
		["quoted prompt", 'Unexpected token in "please refactor the billing module and add tests"', "billing module", '"[text]"'],
		["home", "ENOENT /home/alice/.ao/data/x", "/home/alice", "~/.ao/data/x"],
	])("removes %s", (_name, input, gone, kept) => {
		const out = scrubText(input, home);
		expect(out).not.toContain(gone);
		expect(out).toContain(kept);
	});

	it("keeps ordinary stack frame paths readable", () => {
		expect(scrubText("at startDaemonInner (/tmp/.mount_ao/resources/app.asar/.vite/build/main.js:10:5)", home))
			.toBe("at startDaemonInner (/tmp/.mount_ao/resources/app.asar/.vite/build/main.js:10:5)");
	});

	it("truncates", () => {
		expect(scrubText("x ".repeat(500), home, 10)).toHaveLength(11);
	});
});

describe("errorFacts", () => {
	it("keeps name, code and frames, never non-frame stack text", () => {
		const error = Object.assign(new Error("write EPIPE"), { code: "EPIPE" });
		error.stack = "Error: write EPIPE\n    secret prompt line\n    at foo (/home/alice/app/main.js:1:2)";
		expect(errorFacts(error, home)).toEqual({ name: "Error", message: "write EPIPE", code: "EPIPE", frames: ["at foo (~/app/main.js:1:2)"] });
	});

	it("describes non-errors by type", () => {
		expect(errorFacts(42, home)).toEqual({ type: "number" });
		expect(errorFacts(undefined, home)).toEqual({ type: "undefined" });
	});
});

describe("createLifecycleLog", () => {
	it("appends JSONL records with context and only valid scalar fields", () => {
		const record = createLifecycleLog({ directory: dir, version: "0.14.10", homeDir: home, pid: 42, now: () => new Date("2026-01-02T03:04:05Z") });
		record("daemon_exit", { code: null, signal: "SIGBUS", uptimeMs: 10, current: true, "bad key": "x", ignored: undefined, nan: Number.NaN });
		record("Bad Event");
		record("uncaught_exception", { origin: "uncaughtException" }, new Error("boom https://x.test/secret"));
		const records = readRecords();
		expect(records).toHaveLength(2);
		expect(records[0]).toEqual({ ts: "2026-01-02T03:04:05.000Z", pid: 42, version: "0.14.10", event: "daemon_exit", code: null, signal: "SIGBUS", uptimeMs: 10, current: true, nan: null });
		expect(records[1].error.message).toBe("boom [url]");
		expect(statSync(logFile()).mode & 0o777).toBe(0o600);
	});

	it("rotates at the size cap and keeps two generations", () => {
		const record = createLifecycleLog({ directory: dir, version: "v", homeDir: home, maxBytes: 300 });
		for (let n = 0; n < 40; n++) record("tick", { n });
		for (const suffix of ["", ".1", ".2"]) expect(statSync(logFile() + suffix).size).toBeLessThanOrEqual(300);
		expect(existsSync(`${logFile()}.3`)).toBe(false);
		expect(readRecords().at(-1).n).toBe(39);
	});

	it("never follows a symlinked log and never throws", () => {
		const target = path.join(mkdtempSync(path.join(os.tmpdir(), "ao-lifecycle-target-")), "t");
		writeFileSync(target, "");
		mkdirSync(dir, { recursive: true });
		symlinkSync(target, logFile());
		expect(() => createLifecycleLog({ directory: dir, version: "v", homeDir: home })("app_start")).not.toThrow();
		expect(readFileSync(target, "utf8")).toBe("");
		rmSync(path.dirname(target), { recursive: true, force: true });
	});
});

describe("markRunStarted", () => {
	it("reports a previous run that never cleared its marker, then clears its own", () => {
		const record = createLifecycleLog({ directory: dir, version: "2", homeDir: home });
		writeFileSync(path.join(dir, LIFECYCLE_RUN_MARKER_FILE), JSON.stringify({ pid: 111, startedAt: "2026-01-01T00:00:00.000Z", version: "1" }));
		const clear = markRunStarted({ directory: dir, record, version: "2", pid: 222, processAlive: (pid) => pid === 111 });
		expect(readRecords()).toMatchObject([{ event: "previous_run_unclean", previousPid: 111, previousVersion: "1", previousPidAlive: true }]);
		expect(JSON.parse(readFileSync(path.join(dir, LIFECYCLE_RUN_MARKER_FILE), "utf8")).pid).toBe(222);
		clear();
		expect(existsSync(path.join(dir, LIFECYCLE_RUN_MARKER_FILE))).toBe(false);
	});

	it("records nothing after a clean exit and leaves another run's marker alone", () => {
		const record = createLifecycleLog({ directory: dir, version: "2", homeDir: home });
		const clear = markRunStarted({ directory: dir, record, version: "2", pid: 1, processAlive: () => false });
		expect(existsSync(logFile())).toBe(false);
		writeFileSync(path.join(dir, LIFECYCLE_RUN_MARKER_FILE), JSON.stringify({ pid: 2 }));
		clear();
		expect(existsSync(path.join(dir, LIFECYCLE_RUN_MARKER_FILE))).toBe(true);
	});
});

describe("observeUnhandledRejections", () => {
	it("sees the rejection and returns the original emit result unchanged", () => {
		const seen: unknown[] = [];
		const target = { emit: (event: string | symbol, ..._args: unknown[]) => event === "unhandledRejection" ? false : true };
		const restore = observeUnhandledRejections(target, (reason) => seen.push(reason));
		expect(target.emit("unhandledRejection", "why")).toBe(false);
		expect(target.emit("other")).toBe(true);
		expect(seen).toEqual(["why"]);
		restore();
		target.emit("unhandledRejection", "later");
		expect(seen).toEqual(["why"]);
	});

	it("keeps emitting when the observer throws", () => {
		const target = { emit: (..._args: unknown[]) => true };
		observeUnhandledRejections(target, () => { throw new Error("observer"); });
		expect(target.emit()).toBe(true);
	});
});

it("linuxProcessContext reads the unified cgroup and OOM adjustment", () => {
	const files: Record<string, string> = {
		"/proc/self/cgroup": "0::/user.slice/user-1000.slice/user@1000.service/app.slice/app-Hyprland-ao-123.scope\n",
		"/proc/self/oom_score_adj": "200\n",
	};
	expect(linuxProcessContext((file) => files[file] ?? "")).toEqual({
		cgroup: "/user.slice/user-1000.slice/user@1000.service/app.slice/app-Hyprland-ao-123.scope",
		oomScoreAdj: 200,
	});
	expect(linuxProcessContext(() => { throw new Error("no proc"); })).toEqual({});
});

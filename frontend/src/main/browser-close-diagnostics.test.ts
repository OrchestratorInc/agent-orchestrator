import { mkdtempSync, readFileSync, rmSync, statSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import { afterEach, describe, expect, it } from "vitest";
import { BROWSER_CLOSE_LOG_MAX_BYTES, createBrowserCloseDiagnostics } from "./browser-close-diagnostics";

const directories: string[] = [];
afterEach(() => { for (const directory of directories.splice(0)) rmSync(directory, { recursive: true, force: true }); });
function setup() {
	const directory = mkdtempSync(path.join(os.tmpdir(), "ao-close-log-"));
	directories.push(directory);
	return { directory, file: path.join(directory, "logs", "browser-tab-close.jsonl"), log: createBrowserCloseDiagnostics(directory, "test-version") };
}
const failure = {
	error: Object.assign(new Error("https://alice:password@example.test/private?token=secret Cookie: private"), { code: "AGENT_BROWSER_START_FAILED" }),
	sessionId: "private-project-session", tabId: "private-tab", tabCount: 2, tabPresent: true, wasActive: true, agentTab: false,
};

describe("persistent browser close diagnostics", () => {
	it("persists allowlisted metadata without exception text, stacks, identifiers or extra fields", () => {
		const { file, log } = setup();
		log({ ...failure, url: "https://secret.test", title: "private title" } as typeof failure);
		log(failure);
		const raw = readFileSync(file, "utf8");
		const [first, second] = raw.trim().split("\n").map((line) => JSON.parse(line));
		expect(first).toEqual({ timestamp: expect.any(String), event: "browser_tab_close_failed", appVersion: "test-version", code: "AGENT_BROWSER_START_FAILED", errorType: "Error", session: expect.stringMatching(/^[a-f0-9]{24}$/), tab: expect.stringMatching(/^[a-f0-9]{24}$/), tabCount: 2, tabPresent: true, wasActive: true, agentTab: false });
		expect(first.session).toBe(second.session);
		expect(raw).not.toMatch(/private|secret|password|Cookie|https|stack/);
		if (process.platform !== "win32") expect(statSync(file).mode & 0o777).toBe(0o600);
		createBrowserCloseDiagnostics(path.dirname(path.dirname(file)), "test-version")(failure);
		const third = JSON.parse(readFileSync(file, "utf8").trim().split("\n")[2]);
		expect(third.session).not.toBe(first.session);
	});

	it("maps arbitrary error codes and non-Error failures to UNKNOWN", () => {
		const { file, log } = setup();
		for (const error of [{ code: "token=private", name: "private" }, "private", null]) log({ ...failure, error } as typeof failure);
		const lines = readFileSync(file, "utf8").trim().split("\n").map((line) => JSON.parse(line));
		expect(lines.map((line) => line.code)).toEqual(["UNKNOWN", "UNKNOWN", "UNKNOWN"]);
		expect(lines.map((line) => line.errorType)).toEqual(["UNKNOWN", "UNKNOWN", "UNKNOWN"]);
	});

	it("rotates at the size limit and retains only one previous file", () => {
		const { file, log } = setup();
		log(failure);
		writeFileSync(file, "x".repeat(BROWSER_CLOSE_LOG_MAX_BYTES), { mode: 0o600 });
		log(failure);
		expect(statSync(`${file}.1`).size).toBe(BROWSER_CLOSE_LOG_MAX_BYTES);
		expect(readFileSync(file, "utf8").trim().split("\n")).toHaveLength(1);
		writeFileSync(file, "y".repeat(BROWSER_CLOSE_LOG_MAX_BYTES));
		log(failure);
		expect(readFileSync(`${file}.1`, "utf8").startsWith("y")).toBe(true);
	});

	it("does not throw when the log directory cannot be written", () => {
		const { directory } = setup();
		writeFileSync(path.join(directory, "logs"), "not a directory");
		expect(() => createBrowserCloseDiagnostics(directory, "test-version")(failure)).not.toThrow();
	});
});

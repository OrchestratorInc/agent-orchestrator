// @vitest-environment node
import { EventEmitter } from "node:events";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import { PassThrough, Writable } from "node:stream";
import type { ChildProcessWithoutNullStreams } from "node:child_process";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { DaemonStatus } from "../shared/daemon-status";
import { TRIAGE_CHANNELS, type TriageChoice, type TriageRun } from "../shared/triage";
import {
	MAX_TRIAGE_COMMAND_BYTES, MAX_TRIAGE_FRAME_BYTES, registerTriageIpc,
	resolveTriageBinary, scrubTriageDiagnostics, TriageService,
	type TriageServiceOptions,
} from "./triage-service";

type RecordedCommand = { id: string; type: string; [key: string]: unknown };
class FakeChild extends EventEmitter {
	readonly commands: RecordedCommand[] = [];
	readonly stdout = new PassThrough();
	readonly stderr = new PassThrough();
	readonly stdin = new Writable({ write: (chunk, _encoding, callback) => {
		this.commands.push(JSON.parse(chunk.toString()) as RecordedCommand);
		callback();
	} });
	readonly pid = 987654;
	readonly kill = vi.fn();
	respond(command: RecordedCommand, type: string, value: Record<string, unknown> = {}) {
		this.stdout.write(JSON.stringify({ version: 1, id: command.id, type, ...value }) + "\n");
	}
	event(run: TriageRun) { this.stdout.write(JSON.stringify({ version: 1, type: "run", run }) + "\n"); }
}

function run(id = "run-1"): TriageRun {
	return {
		id, version: 1, createdAt: "2026-10-19T10:00:00Z", updatedAt: "2026-10-19T10:00:00Z",
		request: { harness: "codex", model: "configured-model", publishAuthorized: false },
		state: "running", activity: "Investigating", messages: [],
	};
}

const configured: TriageChoice = {
	harness: "codex", label: "Codex", models: [{ id: "configured-model", label: "Configured model", efforts: ["high"], default: true }],
	accounts: [{ id: "account-1", label: "Work", default: true }], available: true, sessionMode: "chat",
};

const directories: string[] = [];
const services: TriageService[] = [];
afterEach(() => {
	for (const service of services.splice(0)) service.closeOnExit();
	for (const directory of directories.splice(0)) rmSync(directory, { recursive: true, force: true });
	vi.useRealTimers();
});

function setup(extra: { missing?: boolean; timeout?: number; requestDaemon?: TriageServiceOptions["requestDaemon"] } = {}) {
	const directory = mkdtempSync(path.join(os.tmpdir(), "ao-triage-bridge-"));
	directories.push(directory);
	const binary = path.join(directory, process.platform === "win32" ? "ao.exe" : "ao");
	if (!extra.missing) writeFileSync(binary, "fake binary; never executed");
	const child = new FakeChild();
	const spawnProcess = vi.fn(() => child as unknown as ChildProcessWithoutNullStreams);
	const terminateProcess = vi.fn();
	let daemonStatus: DaemonStatus = { state: "error", code: "binary_missing", message: "daemon missing" };
	const context = vi.fn(() => ({ appVersion: "0.13.0", buildMode: "development", daemonStatus, daemonOutput: "startup API_KEY=secret-key\nport failed" }));
	const service = new TriageService({
		dataDir: path.join(directory, "data"), resolveBinary: () => binary, context,
		spawnProcess, terminateProcess, requestTimeoutMs: extra.timeout ?? 30_000, shutdownTimeoutMs: 100,
		requestDaemon: extra.requestDaemon,
	});
	services.push(service);
	return { service, child, spawnProcess, terminateProcess, binary, directory, context,
		setDaemonStatus: (status: DaemonStatus) => { daemonStatus = status; } };
}

describe("support helper ownership", () => {
	it("uses one direct app-owned process for concurrent requests and correlates responses", async () => {
		const { service, child, spawnProcess, directory, binary } = setup();
		const catalog = service.catalog();
		const listing = service.list();
		expect(spawnProcess).toHaveBeenCalledTimes(1);
		expect(spawnProcess).toHaveBeenCalledWith(binary, ["support-host", "--data-dir", path.join(directory, "data")], expect.objectContaining({ shell: false, cwd: directory, windowsHide: true }));
		expect(child.commands[0].id).not.toBe(child.commands[1].id);
		child.respond(child.commands[1], "runs", { runs: [run()] });
		child.respond(child.commands[0], "catalog", { choices: [configured] });
		await expect(catalog).resolves.toEqual([configured]);
		await expect(listing).resolves.toEqual([run()]);
		// Duplicate responses cannot resolve another pending request.
		child.respond(child.commands[0], "catalog", { choices: [] });
		expect(spawnProcess).toHaveBeenCalledTimes(1);
	});

	it("keeps investigations alive after a view unsubscribes and quits only on shutdown", async () => {
		const { service, child, terminateProcess } = setup();
		const first = vi.fn();
		const second = vi.fn();
		const unsubscribe = service.subscribe(first);
		service.subscribe(second);
		const listing = service.list();
		child.respond(child.commands[0], "runs");
		await listing;
		child.event(run());
		unsubscribe();
		child.event(run("run-2"));
		expect(first).toHaveBeenCalledTimes(1);
		expect(second).toHaveBeenCalledTimes(2);
		expect(child.commands.map((command) => command.type)).toEqual(["list"]);
		expect(terminateProcess).not.toHaveBeenCalled();
		const closing = service.shutdown();
		expect(service.shutdown()).toBe(closing);
		expect(child.commands[1].type).toBe("quit");
		child.respond(child.commands[1], "quit");
		child.emit("exit", 0, null);
		await closing;
		expect(child.stdin.writableEnded).toBe(true);
		expect(terminateProcess).not.toHaveBeenCalled();
		await expect(service.list()).rejects.toThrow("shutting down");
	});

	it("does not spawn a helper just to close the app", async () => {
		const { service, spawnProcess } = setup();
		await service.shutdown();
		expect(spawnProcess).not.toHaveBeenCalled();
	});

	it("rejects pending work on helper exit and never retries with another process", async () => {
		const { service, child, spawnProcess, terminateProcess } = setup();
		const pending = service.list();
		child.stderr.write("Authorization: Bearer private-bearer\nprocess failed");
		child.emit("exit", 1, null);
		await expect(pending).rejects.toThrow("support helper exited");
		await expect(service.catalog()).rejects.toThrow("Restart AO");
		expect(spawnProcess).toHaveBeenCalledTimes(1);
		expect(terminateProcess).toHaveBeenCalledWith(child);
	});
});

describe("support helper framing", () => {
	it("accepts split UTF-8 frames and several frames in one read", async () => {
		const { service, child } = setup();
		const first = service.get("first");
		const second = service.get("second");
		const firstRun = { ...run("first"), activity: "Checking café" };
		const bytes = Buffer.from(JSON.stringify({ version: 1, id: child.commands[0].id, type: "run", run: firstRun }) + "\n");
		const split = bytes.indexOf(Buffer.from("é")) + 1;
		child.stdout.write(bytes.subarray(0, split));
		child.stdout.write(Buffer.concat([bytes.subarray(split), Buffer.from(JSON.stringify({ version: 1, id: child.commands[1].id, type: "run", run: run("second") }) + "\n")]));
		await expect(first).resolves.toEqual(firstRun);
		await expect(second).resolves.toEqual(run("second"));
	});

	it.each(["not-json\n", '{"version":2,"type":"runs"}\n', '{"version":1,"type":"run","run":{}}\n'])("fails closed for malformed output %s", async (output) => {
		const { service, child, spawnProcess } = setup();
		const pending = service.list();
		child.stdout.write(output);
		await expect(pending).rejects.toThrow("invalid response");
		expect(child.stdin.writableEnded).toBe(true);
		await expect(service.list()).rejects.toThrow("invalid response");
		expect(spawnProcess).toHaveBeenCalledTimes(1);
	});

	it("bounds both response frames and outgoing commands", async () => {
		const { service, child } = setup();
		const pending = service.list();
		child.stdout.write(Buffer.alloc(MAX_TRIAGE_FRAME_BYTES + 1, 32));
		await expect(pending).rejects.toThrow("oversized response");
		const other = setup();
		await expect(other.service.reply("run-1", "x".repeat(MAX_TRIAGE_COMMAND_BYTES))).rejects.toThrow("too large");
		expect(other.spawnProcess).not.toHaveBeenCalled();
	});

	it("times out only the request and accepts a later progress event without cancelling", async () => {
		vi.useFakeTimers();
		const { service, child } = setup({ timeout: 50 });
		const listener = vi.fn();
		service.subscribe(listener);
		const pending = service.get("run-1");
		const rejection = expect(pending).rejects.toThrow("may still be running");
		await vi.advanceTimersByTimeAsync(51);
		await rejection;
		child.respond(child.commands[0], "run", { run: run() });
		child.event(run());
		expect(listener).toHaveBeenCalledWith(run());
		expect(child.commands.map((command) => command.type)).toEqual(["get"]);
	});

	it("bounds pending requests and write backpressure", async () => {
		const { service, child } = setup();
		const pending = Array.from({ length: 64 }, () => service.list());
		await expect(service.list()).rejects.toThrow("Too many support requests");
		for (const command of child.commands) child.respond(command, "runs");
		await Promise.all(pending);
		Object.defineProperty(child.stdin, "writableLength", { value: 4 * MAX_TRIAGE_COMMAND_BYTES });
		await expect(service.list()).rejects.toThrow("busy");
		expect(child.commands).toHaveLength(64);
	});

	it("rejects a mismatched response type and propagates host errors without starting again", async () => {
		const { service, child, spawnProcess } = setup();
		const failed = service.get("run-1");
		child.respond(child.commands[0], "error", { error: "Provider failed API_KEY=private-value" });
		await expect(failed).rejects.toThrow("Provider failed API_KEY=[redacted]");
		const pending = service.list();
		child.respond(child.commands[1], "catalog");
		await expect(pending).rejects.toThrow("wrong response");
		expect(spawnProcess).toHaveBeenCalledTimes(1);
	});
});

describe("support origin and configured choices", () => {
	it("collects a preparation with trusted desktop context before any agent or publication authorization", async () => {
		const { service, child, context } = setup();
		const pending = service.prepare({ surface: "help", daemonState: "ready", sessionId: "affected" });
		expect(context).toHaveBeenCalledOnce();
		expect(child.commands[0]).toMatchObject({ type: "prepare", origin: { surface: "help", sessionId: "affected", daemonState: "error", appVersion: "0.13.0" } });
		expect(child.commands[0]).not.toHaveProperty("start");
		expect(child.commands[0]).not.toHaveProperty("publishAuthorized");
		const prepared = { ...run("preflight"), preparation: true, state: "collecting", request: { harness: "", publishAuthorized: false } };
		child.respond(child.commands[0], "run", { run: prepared });
		await expect(pending).resolves.toEqual(prepared);
		const start = service.start({ harness: "codex", preparationId: "preflight", mode: "configured-mode", permissions: "default", publishAuthorized: true });
		expect(child.commands[1]).toMatchObject({ type: "get", runId: "preflight" });
		child.respond(child.commands[1], "run", { run: prepared });
		await vi.waitFor(() => expect(child.commands).toHaveLength(3));
		expect(child.commands[2].start).toMatchObject({ preparationId: "preflight", harness: "codex", mode: "configured-mode", permissions: "default", publishAuthorized: true });
		child.respond(child.commands[2], "run", { run: run("preflight") });
		await expect(start).resolves.toMatchObject({ id: "preflight", state: "running" });
	});
	it("retains bounded cached measurements and session facts without changing measured flags", async () => {
		const { service, child } = setup();
		const pending = service.start({ harness: "codex", publishAuthorized: false, origin: {
			sessionId: "affected", session: { id: "affected", status: "working" },
			machine: { app: { cpuMeasured: false, cpuPercent: 0 }, system: { memoryTotalBytes: 8000 }, fetchedAt: "2026-10-19T10:00:00Z" },
		} });
		expect(child.commands[0].start).toMatchObject({ origin: { session: { id: "affected", status: "working" }, machine: { app: { cpuMeasured: false, cpuPercent: 0 }, system: { memoryTotalBytes: 8000 } } } });
		child.respond(child.commands[0], "run", { run: run() });
		await pending;
		await expect(service.start({ harness: "codex", publishAuthorized: false, origin: { session: { text: "x".repeat(64 * 1024) } } })).rejects.toThrow("snapshot is too large");
		expect(child.commands).toHaveLength(1);
	});

	it("accepts an explicitly configured native-default model without inventing a model ID", async () => {
		const { service, child } = setup();
		const choice = { ...configured, models: [{ id: "", label: "Configured default" }] };
		const pending = service.configure([choice]);
		expect(child.commands[0].choices).toEqual([choice]);
		child.respond(child.commands[0], "catalog", { choices: [choice] });
		await pending;
	});

	it.each([true, false])("preserves the model-cache missing marker %s without treating it as a model", async (modelsMissing) => {
		const { service, child } = setup();
		const choice = { ...configured, models: [], modelsMissing };
		const pending = service.configure([choice]);
		expect(child.commands[0].choices).toEqual([choice]);
		child.respond(child.commands[0], "catalog", { choices: [choice] });
		await pending;
		const catalog = service.catalog();
		child.respond(child.commands[1], "catalog", { choices: [choice] });
		await expect(catalog).resolves.toEqual([choice]);
	});

	it("fills origin from the existing desktop snapshot and ignores renderer machine or daemon overrides", async () => {
		const { service, child, context, setDaemonStatus } = setup();
		setDaemonStatus({ state: "starting", code: "not_ready", details: "read /Users/private-user/.ao/running.json" });
		const pending = service.start({ harness: "codex", model: "configured-model", publishAuthorized: true, observation: "The board is blank", origin: {
			surface: "startup", sessionId: "session-1", daemonState: "ready", appVersion: "forged", machine: { fullEnvironment: "secret" },
		} });
		expect(context).toHaveBeenCalledTimes(1);
		const request = child.commands[0].start as TriageRun["request"];
		expect(request).toMatchObject({ observation: "The board is blank", publishAuthorized: true, origin: {
			surface: "startup", sessionId: "session-1", appVersion: "0.13.0", buildMode: "development", daemonState: "starting", daemonCode: "not_ready",
			machine: { platform: process.platform, arch: process.arch },
		} });
		expect(JSON.stringify(request)).not.toContain("private-user");
		expect(JSON.stringify(request)).not.toContain("secret-key");
		expect(JSON.stringify(request)).not.toContain("fullEnvironment");
		child.respond(child.commands[0], "run", { run: run() });
		await pending;
	});

	it("carries ordinary input responses and reads a snapshot after an acknowledgement", async () => {
		const { service, child, spawnProcess } = setup();
		await expect(service.get("../\0bad")).rejects.toThrow("Invalid support request");
		expect(spawnProcess).not.toHaveBeenCalled();
		const input = { requestId: "approval", decisionId: "allow-once" };
		const pending = service.reply("run-1", "", input);
		expect(child.commands[0]).toMatchObject({ type: "reply", runId: "run-1", text: "", ...input });
		child.respond(child.commands[0], "ack");
		await vi.waitFor(() => expect(child.commands[1]?.type).toBe("get"));
		child.respond(child.commands[1], "run", { run: run() });
		await expect(pending).resolves.toEqual(run());
	});

	it("routes terminal bytes and dimensions without a semantic answer protocol", async () => {
		const { service, child } = setup();
		const output = vi.fn();
		const dispose = service.subscribeTerminal(output);
		const input = service.terminalInput("run-1", "\r");
		expect(child.commands[0]).toMatchObject({ type: "terminal-input", runId: "run-1", text: "\r" });
		child.respond(child.commands[0], "ack");
		await input;
		const resize = service.terminalResize("run-1", 24, 80);
		expect(child.commands[1]).toMatchObject({ type: "terminal-resize", rows: 24, cols: 80 });
		child.respond(child.commands[1], "ack");
		await resize;
		const data = Buffer.from("hello").toString("base64");
		child.stdout.write(JSON.stringify({ version: 1, type: "terminal-output", runId: "run-1", data, sequence: 5 }) + "\n");
		expect(output).toHaveBeenCalledExactlyOnceWith({ runId: "run-1", data, sequence: 5 });
		dispose();
		child.stdout.write(JSON.stringify({ version: 1, type: "terminal-output", runId: "run-1", data, sequence: 10 }) + "\n");
		expect(output).toHaveBeenCalledTimes(1);
		await expect(service.terminalResize("run-1", 0, 80)).rejects.toThrow("Invalid terminal size");
	});

	it("includes scrubbed startup details when the bundled binary is unavailable", async () => {
		const { service, spawnProcess, setDaemonStatus } = setup({ missing: true });
		setDaemonStatus({ state: "error", code: "binary_missing", details: "Cannot read /Users/private-user/Library/AO\nAuthorization: Bearer private-secret" });
		const result = service.catalog().catch((error: unknown) => error as Error);
		const error = await result;
		expect(error).toBeInstanceOf(Error);
		const message = (error as Error).message;
		expect(message).toContain("GitHub report");
		expect(message).toContain("binary_missing");
		expect(message).toContain("0.13.0");
		expect(message).toContain("port failed");
		expect(message).not.toContain("private-secret");
		expect(message).not.toContain("private-user");
		expect(message).not.toContain("secret-key");
		expect(spawnProcess).not.toHaveBeenCalled();
	});
});

describe("ordinary standalone investigation launch", () => {
	const ready: DaemonStatus = { state: "ready", port: 3037, pid: 12345, executablePath: "/app/ao" };
	const launch = { clientRequestId: "triage-run-1", prompt: "Follow the attached playbook and read the diagnostics.",
		attachments: [{ mimeType: "text/plain", data: Buffer.from("private diagnostic bundle").toString("base64") }] };
	const handedOff = (): TriageRun => ({ ...run(), state: "session_starting", execution: "session",
		request: { harness: "codex", model: "configured-model", effort: "high", permissions: "accept-edits", mode: "native-agent-mode", sessionMode: "tui", accountId: "configured-default", publishAuthorized: false } });
	const health = () => ({ status: 200, body: { service: "agent-orchestrator-daemon", status: "ok", pid: ready.pid, executablePath: ready.executablePath } });
	const request = { harness: "codex", preparationId: "run-1", publishAuthorized: false };

	it("creates a real projectless worker with durable context and normal interface/account defaults", async () => {
		const requestDaemon = vi.fn<NonNullable<TriageServiceOptions["requestDaemon"]>>().mockResolvedValueOnce(health()).mockResolvedValueOnce({ status: 201, body: { session: { id: "standalone-12" } } });
		const { service, child, setDaemonStatus } = setup({ requestDaemon });
		setDaemonStatus(ready);
		const starting = service.start(request);
		expect(child.commands[0]).toMatchObject({ type: "handoff", start: request });
		child.respond(child.commands[0], "session-launch", { run: handedOff(), launch });
		await vi.waitFor(() => expect(child.commands[1]).toMatchObject({ type: "session-create-intent", runId: "run-1" }));
		expect(requestDaemon).toHaveBeenCalledOnce();
		child.respond(child.commands[1], "run", { run: { ...handedOff(), sessionLaunchStarted: true } });
		await vi.waitFor(() => expect(child.commands[2]).toMatchObject({ type: "session-linked", runId: "run-1", sessionId: "standalone-12" }));
		const linked = { ...handedOff(), sessionId: "standalone-12", state: "session" };
		child.respond(child.commands[2], "run", { run: linked });
		const result = await starting;
		expect(result).toEqual(linked);
		expect(result).not.toHaveProperty("launch");
		expect(requestDaemon.mock.calls).toEqual([
			[3037, "GET", "/healthz"],
			[3037, "POST", "/api/v1/sessions", { clientRequestId: launch.clientRequestId, kind: "worker", harness: "codex", displayName: "Report a Problem", mode: "tui", prompt: launch.prompt, attachments: launch.attachments, model: "configured-model", effort: "high", approvalMode: "accept-edits" }],
		]);
		expect(child.commands.some((command) => command.type === "start")).toBe(false);
	});

	it("reuses the frozen create request after an unknown POST outcome without an offline replacement", async () => {
		const requestDaemon = vi.fn<NonNullable<TriageServiceOptions["requestDaemon"]>>()
			.mockResolvedValueOnce(health()).mockRejectedValueOnce(new Error("connection closed after sending"))
			.mockResolvedValueOnce(health()).mockResolvedValueOnce({ status: 201, body: { session: { id: "standalone-12" } } });
		const { service, child, setDaemonStatus } = setup({ requestDaemon });
		setDaemonStatus(ready);
		const first = service.start(request);
		child.respond(child.commands[0], "session-launch", { run: handedOff(), launch });
		await vi.waitFor(() => expect(child.commands[1]?.type).toBe("session-create-intent"));
		child.respond(child.commands[1], "run", { run: { ...handedOff(), sessionLaunchStarted: true } });
		await expect(first).rejects.toThrow("retry the same standalone session");
		const retry = service.start(request);
		child.respond(child.commands[2], "session-launch", { run: { ...handedOff(), sessionLaunchStarted: true }, launch });
		await vi.waitFor(() => expect(child.commands[3]?.type).toBe("session-create-intent"));
		child.respond(child.commands[3], "run", { run: { ...handedOff(), sessionLaunchStarted: true } });
		await vi.waitFor(() => expect(child.commands[4]?.type).toBe("session-linked"));
		child.respond(child.commands[4], "run", { run: { ...handedOff(), sessionId: "standalone-12" } });
		await expect(retry).resolves.toHaveProperty("sessionId", "standalone-12");
		expect(requestDaemon.mock.calls[1]).toEqual(requestDaemon.mock.calls[3]);
		expect(child.commands.map((command) => command.type)).toEqual(["handoff", "session-create-intent", "handoff", "session-create-intent", "session-linked"]);
	});

	it("does not switch an uncertain normal launch to the offline provider when the daemon stops", async () => {
		const { service, child } = setup();
		const starting = service.start(request);
		expect(child.commands[0].type).toBe("get");
		child.respond(child.commands[0], "run", { run: { ...handedOff(), sessionLaunchStarted: true } });
		await expect(starting).rejects.toThrow("may already exist");
		expect(child.commands.map((command) => command.type)).toEqual(["get"]);
	});

	it.each(["pid", "service", "executablePath"])("uses the same run offline after a daemon %s mismatch before any create request", async (field) => {
		const requestDaemon = vi.fn<NonNullable<TriageServiceOptions["requestDaemon"]>>().mockResolvedValue({ status: 200, body: { ...health().body, [field]: "unexpected" } });
		const { service, child, setDaemonStatus } = setup({ requestDaemon });
		setDaemonStatus(ready);
		const starting = service.start(request);
		child.respond(child.commands[0], "session-launch", { run: handedOff(), launch });
		await vi.waitFor(() => expect(child.commands[1]?.type).toBe("start"));
		expect(child.commands[1].start).toMatchObject({ ...handedOff().request, preparationId: "run-1" });
		child.respond(child.commands[1], "run", { run: run() });
		await expect(starting).resolves.toEqual(run());
		expect(requestDaemon).toHaveBeenCalledOnce();
		expect(child.commands.map((command) => command.type)).toEqual(["handoff", "start"]);
	});

	it("continues an unsent handoff through the independent agent when the daemon is down", async () => {
		const { service, child } = setup();
		const starting = service.start(request);
		child.respond(child.commands[0], "run", { run: handedOff() });
		await vi.waitFor(() => expect(child.commands[1]?.type).toBe("start"));
		child.respond(child.commands[1], "run", { run: run() });
		await expect(starting).resolves.toEqual(run());
		expect(child.commands.map((command) => command.type)).toEqual(["get", "start"]);
	});

	it("reopens the linked ordinary session even while its daemon is unavailable", async () => {
		const { service, child } = setup();
		const starting = service.start(request);
		const linked = { ...handedOff(), sessionId: "standalone-12" };
		child.respond(child.commands[0], "run", { run: linked });
		await expect(starting).resolves.toEqual(linked);
		expect(child.commands.map((command) => command.type)).toEqual(["get"]);
	});

	it("rechecks the daemon after persisting create intent and never sends attachments to its replacement", async () => {
		const requestDaemon = vi.fn<NonNullable<TriageServiceOptions["requestDaemon"]>>().mockResolvedValue(health());
		const { service, child, setDaemonStatus } = setup({ requestDaemon });
		setDaemonStatus(ready);
		const starting = service.start(request);
		child.respond(child.commands[0], "session-launch", { run: handedOff(), launch });
		await vi.waitFor(() => expect(child.commands[1]?.type).toBe("session-create-intent"));
		setDaemonStatus({ ...ready, pid: 54321 });
		child.respond(child.commands[1], "run", { run: { ...handedOff(), sessionLaunchStarted: true } });
		await expect(starting).rejects.toThrow("daemon changed");
		expect(requestDaemon).toHaveBeenCalledOnce();
		expect(child.commands.map((command) => command.type)).toEqual(["handoff", "session-create-intent"]);
	});

	it("opens a known created session even if the agent's tool temporarily prevents saving its report link", async () => {
		const requestDaemon = vi.fn<NonNullable<TriageServiceOptions["requestDaemon"]>>().mockResolvedValueOnce(health()).mockResolvedValueOnce({ status: 201, body: { session: { id: "standalone-12" } } });
		const { service, child, setDaemonStatus } = setup({ requestDaemon });
		setDaemonStatus(ready);
		const starting = service.start(request);
		child.respond(child.commands[0], "session-launch", { run: handedOff(), launch });
		await vi.waitFor(() => expect(child.commands[1]?.type).toBe("session-create-intent"));
		child.respond(child.commands[1], "run", { run: { ...handedOff(), sessionLaunchStarted: true } });
		await vi.waitFor(() => expect(child.commands[2]?.type).toBe("session-linked"));
		child.respond(child.commands[2], "error", { error: "report is temporarily locked" });
		await expect(starting).resolves.toMatchObject({ execution: "session", sessionId: "standalone-12" });
		const reopening = service.get("run-1");
		child.respond(child.commands[3], "run", { run: handedOff() });
		await expect(reopening).resolves.toEqual(handedOff());
		expect(child.commands.some((command) => command.type === "start")).toBe(false);
	});

});

describe("desktop support IPC", () => {
	it("guards every typed channel before calling the service", async () => {
		const { service, child, spawnProcess } = setup();
		const handlers = new Map<string, (event: unknown, ...args: unknown[]) => unknown>();
		const trusted = {};
		registerTriageIpc({
			ipcMain: { handle: (channel, handler) => { handlers.set(channel, handler as never); } }, service,
			isTrustedSender: (event) => event === trusted,
		});
		expect(handlers.size).toBe(10);
		for (const handler of handlers.values()) expect(() => handler({})).toThrow("Untrusted support request");
		expect(spawnProcess).not.toHaveBeenCalled();
		const pending = handlers.get(TRIAGE_CHANNELS.list)!(trusted);
		child.respond(child.commands[0], "runs");
		await expect(pending).resolves.toEqual([]);
	});
});

describe("support binary and diagnostic boundaries", () => {
	it("resolves only the installed or checkout-owned AO binary", () => {
		const input = { resourcesPath: "/app/resources", appPath: "/checkout/frontend", homeDir: "/home/user", platform: "darwin" as const };
		expect(resolveTriageBinary({ ...input, isPackaged: true })).toBe("/app/resources/daemon/ao");
		expect(resolveTriageBinary({ ...input, isPackaged: false })).toBe("/checkout/frontend/daemon/ao");
	});

	it("scrubs quoted secrets, authorization headers, home names and URL credentials", () => {
		const value = scrubTriageDiagnostics('API_KEY="private value"\nCookie: session=private; other=also-private\nBearer opaque-value\n--password hidden\nhttps://name:password@example.test/path?token=private#private\n/Users/private-name/.ao');
		expect(value).not.toMatch(/private|opaque-value|hidden|name:password/);
		expect(value).toContain("https://example.test/path");
		expect(value).toContain("[home]/.ao");
	});
});

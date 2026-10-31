// @vitest-environment node
import { EventEmitter } from "node:events";
import { readFileSync } from "node:fs";
import path from "node:path";
import vm from "node:vm";
import ts from "typescript";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { daemonExitWasUngraceful, planDaemonAutoRestart } from "./daemon-auto-restart";
import { keepDaemonAlive } from "./daemon-owner";
import { createListenPortScanner, parseRunFile } from "../shared/daemon-discovery";
import { browserDaemonOwnershipDecision, browserRuntimeRecoveryDecision, keepAliveForReplacement, portHolderPidToReplace, shouldReplaceDaemonForStartup } from "../shared/daemon-takeover";

const source = ts.createSourceFile("main.ts", readFileSync(new URL("../main.ts", import.meta.url), "utf8"), ts.ScriptTarget.Latest, true);
const functions = new Set([
	"startDaemon", "startDaemonInner", "killDaemon", "readRunFileState", "cancelDaemonAutoRestart",
	"scheduleDaemonAutoRestart", "daemonAutoRestartMarkerMatches", "stopDaemon", "reportDaemonRestartFailure", "startDaemonForRestart", "restartDaemon",
	"refreshDaemonStatus", "handleBrowserRuntimeRejection",
]);
const controllerSource = source.statements.filter((node) => {
	if (ts.isFunctionDeclaration(node)) return functions.has(node.name?.text ?? "");
	if (ts.isVariableStatement(node)) return node.declarationList.declarations.every((declaration) =>
		ts.isIdentifier(declaration.name) && (/^daemon/.test(declaration.name.text) || declaration.name.text === "forceReplaceAdoptedRequested"));
	if (!ts.isExpressionStatement(node) || !ts.isCallExpression(node.expression)) return false;
	const call = node.expression;
	const target = call.expression.getText(source);
	const channel = call.arguments[0];
	return ts.isStringLiteral(channel) && (
		(target === "ipcMain.handle" && ["daemon:start", "daemon:stop", "daemon:restart", "daemon:getStatus"].includes(channel.text)) ||
		(target === "app.on" && channel.text === "before-quit")
	);
}).map((node) => node.getText(source)).join("\n");
const controller = ts.transpileModule(controllerSource, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText;

function deferred<T>() {
	let resolve!: (value: T) => void;
	const promise = new Promise<T>((done) => { resolve = done; });
	return { promise, resolve };
}

function setup() {
	let marker: string | Error = JSON.stringify({ pid: 42, port: 3001 });
	const read = vi.fn(async () => {
		if (marker instanceof Error) throw marker;
		return marker;
	});
	const unlink = vi.fn(async (): Promise<void> => { marker = Object.assign(new Error("missing"), { code: "ENOENT" }); });
	const children: Array<EventEmitter & { pid: number; stdout: EventEmitter; stderr: EventEmitter; stdin: { on: () => void; end: () => void }; kill: ReturnType<typeof vi.fn>; unref: ReturnType<typeof vi.fn> }> = [];
	const spawn = vi.fn((_command: string, _args: string[], _options: { env: Record<string, string>; stdio: unknown }) => {
		const child = Object.assign(new EventEmitter(), {
			pid: 42 + children.length, stdout: new EventEmitter(), stderr: new EventEmitter(),
			stdin: { on() {}, end() {} }, kill: vi.fn(), unref: vi.fn(),
		});
		children.push(child);
		return child;
	});
	const ipc = new Map<string, () => Promise<unknown> | unknown>();
	const app = new EventEmitter();
	Object.assign(app, { isPackaged: false, getAppPath: () => "/owned/lab" });
	const replace = vi.fn(async () => undefined);
	const inspect = vi.fn(async (): Promise<unknown> => null);
	const direct = vi.fn(async (): Promise<unknown> => null);
	const stage = vi.fn(async (): Promise<void> => undefined);
	const supervisorDisposed = vi.fn();
	const establishSupervisorLink = vi.fn();
	const daemonEnv = vi.fn((keep: boolean) => ({ AO_OWNER: keep ? "persistent" : "app" }));
	const env: Record<string, string | undefined> = {};
	const context = vm.createContext({
		console, Date, setTimeout, clearTimeout, setInterval, clearInterval, Buffer, path,
		openSync: vi.fn(() => 73), closeSync: vi.fn(),
		process: { env, platform: "linux", resourcesPath: "/owned/lab", kill: vi.fn() },
		os: { homedir: () => "/owned/home" }, app, ipcMain: { handle: (name: string, handler: () => unknown) => ipc.set(name, handler) },
		browserQuitRequested: false, daemonOutput: "", supervisorLink: { dispose: supervisorDisposed }, appRunId: "test-app",
		browserRuntimeRecoveryDecision, browserRuntimeRecoveryAttempted: false,
		forceReplaceAdoptedRequested: false, cachedShellEnv: {}, earlyShellEnvProbe: null,
		repairDevAcpRuntime: async () => undefined, ensureShellEnv: async () => undefined,
		resolveDaemonLaunch: () => ({ command: "/owned/ao", args: [], cwd: "/owned/data", source: "override", shell: false }),
		daemonLaunchEnv: () => ({}), inspectExistingDaemon: inspect, browserDaemonOwnershipDecision,
		shouldReplaceDaemonForStartup, keepAliveForReplacement, gracefullyReplaceDaemonForBrowser: replace,
		shouldLinkOnAttach: () => false, establishSupervisorLink, resolveDaemonFromPort: direct,
		resolvedDaemonPort: () => 3001, readDaemonProbe: async () => null, daemonIdentityError: () => null,
		runFilePath: () => "/owned/running.json", parseRunFile, readFile: read, rm: unlink,
		portHolderPidToReplace, readProcessStartMs: async () => null, processAlive: () => false,
		existsSync: () => true, ensureBundledTmuxStaged: stage, keepDaemonAlive,
		spawn, daemonEnv, browserRuntimeToken: "test-only", createListenPortScanner,
		RUN_FILE_FRESHNESS_SKEW_MS: 1000, RUN_FILE_POLL_MS: 100, PORT_DISCOVERY_TIMEOUT_MS: 5000,
		DAEMON_RESTART_STOP_TIMEOUT_MS: 5000, appendDaemonOutput: vi.fn(), slowDaemonStartupStatus: () => ({ state: "starting" }),
		planDaemonAutoRestart, daemonExitWasUngraceful, disposeBrowserRuntimeLink: vi.fn(), captureSideChatState: async () => undefined,
		desktopQuit: { handleBeforeQuit: () => false }, chatDraftRisks: [], browserCleanupComplete: true,
		disposeBrowserStreamLink: vi.fn(), trayLifecycle: { dispose: vi.fn() }, abortCloudArtifactSaves: vi.fn(),
	});
	vm.runInContext(`${controller}\nfunction setDaemonStatus(status) { daemonStatus = status; }`, context);
	return {
		read, unlink, spawn, children, replace, inspect, direct, stage, supervisorDisposed, establishSupervisorLink, daemonEnv, env,
		setMarker(value: string | Error) { marker = value; },
		start: () => ipc.get("daemon:start")!(), stop: () => ipc.get("daemon:stop")!(), restart: () => ipc.get("daemon:restart")!(),
		status: () => vm.runInContext("daemonStatus", context),
		refresh: () => ipc.get("daemon:getStatus")!(),
		rejectRuntime: () => vm.runInContext('handleBrowserRuntimeRejection("invalid-token")', context),
		quit: () => app.emit("before-quit", { preventDefault: vi.fn() }),
		crash(code: number | null = 1, signal: string | null = null) { children.at(-1)!.emit("exit", code, signal); },
	};
}

beforeEach(() => vi.useFakeTimers());
afterEach(() => vi.useRealTimers());

describe("main.ts daemon lifecycle wiring with controlled I/O", () => {
	it("restarts a crashed child through the actual startup path", async () => {
		const h = setup();
		await h.start();
		h.crash();
		await vi.advanceTimersByTimeAsync(1000);
		expect(h.spawn).toHaveBeenCalledTimes(2);
	});

	it.each(["stop", "restart", "quit"] as const)("invalidates an exit-classification read on %s", async (action) => {
		const h = setup();
		await h.start();
		const held = deferred<string>();
		h.read.mockImplementationOnce(() => held.promise);
		h.crash();
		await h[action]();
		const starts = h.spawn.mock.calls.length;
		held.resolve(JSON.stringify({ pid: 42, port: 3001 }));
		await vi.advanceTimersByTimeAsync(2000);
		expect(h.spawn).toHaveBeenCalledTimes(starts);
	});

	it.each(["stop", "restart", "quit"] as const)("invalidates a timer read on %s", async (action) => {
		const h = setup();
		await h.start();
		h.crash();
		await vi.advanceTimersByTimeAsync(0);
		const held = deferred<string>();
		h.read.mockImplementationOnce(() => held.promise);
		await vi.advanceTimersByTimeAsync(1000);
		await h[action]();
		const starts = h.spawn.mock.calls.length;
		held.resolve(JSON.stringify({ pid: 42, port: 3001 }));
		await vi.advanceTimersByTimeAsync(1000);
		expect(h.spawn).toHaveBeenCalledTimes(starts);
	});

	it("does not resurrect after stop while stale-marker removal is in flight", async () => {
		const h = setup();
		await h.start();
		const held = deferred<void>();
		h.unlink.mockImplementationOnce(() => held.promise);
		h.stage.mockImplementationOnce(() => held.promise);
		h.crash();
		await vi.advanceTimersByTimeAsync(1000);
		h.stop();
		held.resolve();
		await vi.advanceTimersByTimeAsync(2000);
		expect(h.spawn).toHaveBeenCalledTimes(1);
		expect(h.unlink).not.toHaveBeenCalled();
		expect(h.status()).toEqual({ state: "stopped" });
	});

	it("recovers a signal crash whose marker was initially absent", async () => {
		const h = setup();
		await h.start();
		h.setMarker(Object.assign(new Error("missing"), { code: "ENOENT" }));
		h.crash(null, "SIGKILL");
		await vi.advanceTimersByTimeAsync(1000);
		expect(h.spawn).toHaveBeenCalledTimes(2);
	});

	it.each(["removed", "successor", "malformed", "unreadable"])("stands down when the crash marker becomes %s", async (state) => {
		const h = setup();
		await h.start();
		h.crash(null, "SIGKILL");
		await vi.advanceTimersByTimeAsync(0);
		h.setMarker(state === "successor" ? JSON.stringify({ pid: 99, port: 3001 }) : state === "malformed" ? "{" : Object.assign(new Error(state), { code: state === "removed" ? "ENOENT" : "EACCES" }));
		await vi.advanceTimersByTimeAsync(2000);
		expect(h.spawn).toHaveBeenCalledTimes(1);
		expect(h.unlink).not.toHaveBeenCalled();
		expect(h.replace).not.toHaveBeenCalled();
	});

	it("does not treat an unreadable or malformed initial marker as missing on signal", async () => {
		for (const marker of ["{", Object.assign(new Error("denied"), { code: "EACCES" })]) {
			const h = setup();
			await h.start();
			h.setMarker(marker);
			h.crash(null, "SIGKILL");
			await vi.advanceTimersByTimeAsync(2000);
			expect(h.spawn).toHaveBeenCalledTimes(1);
		}
	});

	it("honors a graceful nonzero CLI stop", async () => {
		const h = setup();
		await h.start();
		h.setMarker(Object.assign(new Error("missing"), { code: "ENOENT" }));
		h.crash(1);
		await vi.advanceTimersByTimeAsync(2000);
		expect(h.spawn).toHaveBeenCalledTimes(1);
	});

	it("manual Start cancels a pending timer and resets the crash budget", async () => {
		const h = setup();
		await h.start();
		h.crash();
		await vi.advanceTimersByTimeAsync(0);
		await h.start();
		h.setMarker(JSON.stringify({ pid: 43, port: 3001 }));
		h.crash();
		await vi.advanceTimersByTimeAsync(1000);
		expect(h.spawn).toHaveBeenCalledTimes(3);
	});

	it("does not replace a successor discovered after the timer validation", async () => {
		const h = setup();
		await h.start();
		h.inspect.mockResolvedValueOnce({ status: { state: "ready", port: 3001 }, owner: "app", appRunId: "successor" });
		h.crash();
		await vi.advanceTimersByTimeAsync(2000);
		expect(h.spawn).toHaveBeenCalledTimes(1);
		expect(h.replace).not.toHaveBeenCalled();
	});

	it("manual Start supersedes an automatic startup awaiting I/O", async () => {
		const h = setup();
		await h.start();
		const held = deferred<void>();
		h.stage.mockImplementationOnce(() => held.promise);
		h.crash();
		await vi.advanceTimersByTimeAsync(1000);
		const manualStart = h.start();
		held.resolve();
		await manualStart;
		await vi.advanceTimersByTimeAsync(0);
		expect(h.spawn).toHaveBeenCalledTimes(2);
	});

	it("uses the full bounded backoff through repeated actual child exits", async () => {
		const h = setup();
		await h.start();
		for (const delay of [1000, 2000, 4000, 8000, 16000, 30000]) {
			h.setMarker(JSON.stringify({ pid: h.children.at(-1)!.pid, port: 3001 }));
			h.crash();
			const count = h.spawn.mock.calls.length;
			await vi.advanceTimersByTimeAsync(delay - 1);
			expect(h.spawn).toHaveBeenCalledTimes(count);
			await vi.advanceTimersByTimeAsync(1);
			expect(h.spawn).toHaveBeenCalledTimes(count + 1);
		}
		h.setMarker(JSON.stringify({ pid: h.children.at(-1)!.pid, port: 3001 }));
		h.crash();
		await vi.advanceTimersByTimeAsync(30000);
		expect(h.spawn).toHaveBeenCalledTimes(7);
	});

	it("never replaces a direct-probe successor", async () => {
		const h = setup();
		await h.start();
		h.direct.mockResolvedValueOnce({ state: "ready", port: 3001 });
		h.crash();
		await vi.advanceTimersByTimeAsync(1000);
		expect(h.spawn).toHaveBeenCalledTimes(1);
		expect(h.replace).not.toHaveBeenCalled();
	});

	it("revalidates the marker after asynchronous startup preparation", async () => {
		const h = setup();
		await h.start();
		const held = deferred<void>();
		h.stage.mockImplementationOnce(() => held.promise);
		h.crash();
		await vi.advanceTimersByTimeAsync(1000);
		h.setMarker(JSON.stringify({ pid: 99, port: 3001 }));
		held.resolve();
		await vi.advanceTimersByTimeAsync(0);
		expect(h.spawn).toHaveBeenCalledTimes(1);
		expect(h.unlink).not.toHaveBeenCalled();
	});

	it.each(["stop", "restart", "quit"] as const)("invalidates the final pre-spawn read on %s", async (action) => {
		const h = setup();
		await h.start();
		const held = deferred<string>();
		h.stage.mockImplementationOnce(async () => { h.read.mockImplementationOnce(() => held.promise); });
		h.crash();
		await vi.advanceTimersByTimeAsync(1000);
		await h[action]();
		const count = h.spawn.mock.calls.length;
		held.resolve(JSON.stringify({ pid: 42, port: 3001 }));
		await vi.advanceTimersByTimeAsync(0);
		expect(h.spawn).toHaveBeenCalledTimes(count);
	});

	it("does not overwrite explicit stop with a late preparation failure", async () => {
		const h = setup();
		await h.start();
		const held = deferred<void>();
		h.stage.mockImplementationOnce(async () => { await held.promise; throw new Error("staging failed"); });
		h.crash();
		await vi.advanceTimersByTimeAsync(1000);
		h.stop();
		held.resolve();
		await vi.advanceTimersByTimeAsync(0);
		expect(h.status()).toEqual({ state: "stopped" });
		expect(h.spawn).toHaveBeenCalledTimes(1);
	});

	it("status polling after a crash does not take over a successor", async () => {
		const h = setup();
		await h.start();
		h.crash();
		await vi.advanceTimersByTimeAsync(0);
		h.inspect.mockResolvedValue({ status: { state: "ready", port: 3001, pid: 99 }, owner: "app", appRunId: "successor" });
		h.setMarker(JSON.stringify({ pid: 99, port: 3001 }));
		await h.refresh();
		await vi.advanceTimersByTimeAsync(2000);
		expect(h.spawn).toHaveBeenCalledTimes(1);
		expect(h.replace).not.toHaveBeenCalled();
		expect(h.status()).toEqual({ state: "ready", port: 3001, pid: 99 });
	});

	it("automatic runtime rejection recovery after a crash does not take over a successor", async () => {
		const h = setup();
		await h.start();
		h.crash();
		await vi.advanceTimersByTimeAsync(0);
		h.setMarker(JSON.stringify({ pid: 99, port: 3001 }));
		h.rejectRuntime();
		await vi.advanceTimersByTimeAsync(2000);
		expect(h.spawn).toHaveBeenCalledTimes(1);
		expect(h.replace).not.toHaveBeenCalled();
	});

	it("preserves normal startup takeover before any owned child has crashed", async () => {
		const h = setup();
		h.inspect.mockResolvedValue({ status: { state: "ready", port: 3001, pid: 99 }, owner: "app", appRunId: "previous-launch" });
		await h.refresh();
		expect(h.replace).toHaveBeenCalledTimes(1);
		expect(h.spawn).toHaveBeenCalledTimes(1);
	});

	it("disposes the dead child's supervisor reconnect loop before classifying its exit", async () => {
		const h = setup();
		await h.start();
		const held = deferred<string>();
		h.read.mockImplementationOnce(() => held.promise);
		h.crash();
		expect(h.supervisorDisposed).toHaveBeenCalledTimes(1);
		held.resolve(JSON.stringify({ pid: 99, port: 3001 }));
		await vi.advanceTimersByTimeAsync(2000);
		expect(h.spawn).toHaveBeenCalledTimes(1);
	});

	it.each([
		{ name: "inherited persistent", inherit: true, owner: "persistent", initialEnv: undefined, nextEnv: undefined, keep: true },
		{ name: "inherited headless", inherit: true, owner: undefined, initialEnv: undefined, nextEnv: undefined, keep: true },
		{ name: "environment-selected persistent", inherit: false, owner: undefined, initialEnv: "1", nextEnv: "1", keep: true },
		{ name: "ordinary app-owned", inherit: false, owner: undefined, initialEnv: undefined, nextEnv: undefined, keep: false },
		{ name: "persistent after its environment flag is removed", inherit: false, owner: undefined, initialEnv: "1", nextEnv: undefined, keep: true },
		{ name: "app-owned after its environment flag is enabled", inherit: false, owner: undefined, initialEnv: undefined, nextEnv: "1", keep: false },
	])("preserves captured $name launch mode through automatic recovery", async ({ inherit, owner, initialEnv, nextEnv, keep }) => {
		const h = setup();
		h.env.AO_KEEP_DAEMON = initialEnv;
		if (inherit) h.inspect.mockResolvedValueOnce({ status: { state: "ready", port: 3001, pid: 99 }, owner, appRunId: "previous-launch" });
		await h.start();
		h.setMarker(JSON.stringify({ pid: 42, port: 3001, startedAt: new Date().toISOString() }));
		await vi.advanceTimersByTimeAsync(100);
		h.env.AO_KEEP_DAEMON = nextEnv;
		h.crash();
		await vi.advanceTimersByTimeAsync(1000);
		h.setMarker(JSON.stringify({ pid: 43, port: 3001, startedAt: new Date().toISOString() }));
		await vi.advanceTimersByTimeAsync(100);
		expect(h.spawn).toHaveBeenCalledTimes(2);
		expect(h.daemonEnv.mock.calls.map(([value]) => value)).toEqual([keep, keep]);
		for (const [, , options] of h.spawn.mock.calls) {
			expect(options.env.AO_OWNER).toBe(keep ? "persistent" : "app");
			expect(options.stdio).toEqual(keep ? ["pipe", 73, 73] : "pipe");
		}
		for (const child of h.children) expect(child.unref).toHaveBeenCalledTimes(keep ? 1 : 0);
		expect(h.establishSupervisorLink).toHaveBeenCalledTimes(keep ? 0 : 2);
	});
});

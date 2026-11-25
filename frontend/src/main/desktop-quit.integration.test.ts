// @vitest-environment node
import { EventEmitter } from "node:events";
import { readFileSync } from "node:fs";
import vm from "node:vm";
import ts from "typescript";
import { expect, it, vi } from "vitest";
import { createDesktopQuitController } from "./desktop-quit";
import { requestDesktopShutdown } from "./desktop-shutdown";
import { parseRunFile } from "../shared/daemon-discovery";

// Run the actual constructor, draft guard and daemon-shutdown wiring from main.
const source = ts.createSourceFile("main.ts", readFileSync(new URL("../main.ts", import.meta.url), "utf8"), ts.ScriptTarget.Latest, true);
const wiring = source.statements.filter((node) => {
	if (ts.isFunctionDeclaration(node)) return ["shutdownDaemonForDesktopQuit", "cancelPendingQuit"].includes(node.name?.text ?? "");
	if (ts.isVariableStatement(node)) return node.declarationList.declarations.some((declaration) =>
		ts.isIdentifier(declaration.name) && ["desktopQuit", "quitAttempt"].includes(declaration.name.text));
	if (!ts.isExpressionStatement(node) || !ts.isCallExpression(node.expression)) return false;
	const call = node.expression;
	return call.expression.getText(source) === "app.on" && call.arguments[0]?.getText(source) === '"before-quit"';
}).map((node) => node.getText(source)).join("\n");
const script = ts.transpileModule(wiring, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText;

it("preserves agents when the draft dialog confirms after the outside-quit deadline", async () => {
	const deadlineMs = 500;
	let cleanupFinished!: () => void;
	const finished = new Promise<void>((resolve) => { cleanupFinished = resolve; });
	const app = Object.assign(new EventEmitter(), { quit: vi.fn(), exit: vi.fn() });
	const request = vi.fn(async () => ({ ok: true, status: 202 }));
	const context = vm.createContext({
		app, console, setTimeout, clearTimeout, AbortSignal,
		process: { platform: "darwin", env: {} },
		createDesktopQuitController: (options: Parameters<typeof createDesktopQuitController>[0]) =>
			createDesktopQuitController({ ...options, externalQuitDeadlineMs: deadlineMs }),
		trayController: {}, mainWindow: null, createWindowPromise: null,
		browserQuitRequested: false, preserveAgentsOnQuit: false,
		isUpdateRestartRequested: () => false,
		chatDraftRisks: ["unsaved"], chatDraftQuitConfirmed: false, chatDraftDialog: {},
		confirmUnsafeChatDraftLeave: () => {
			// Like showMessageBoxSync: wall time passes without running JS timers.
			Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, deadlineMs + 25);
			return true;
		},
		dialog: { showMessageBoxSync: vi.fn() },
		cancelDaemonAutoRestart: vi.fn(),
		lifecycleLog: vi.fn(),
		agentQuitCleanupComplete: false, agentQuitCleanupPromise: null,
		requestDesktopShutdown, keepDaemonAlive: () => false, appRunId: "test-run",
		daemonStatus: { state: "ready", port: 3002 }, daemonProcess: null,
		desktopAgentShutdownAccepted: false,
		runFilePath: () => "/isolated/running.json", parseRunFile,
		readFile: async () => {
			// The daemon ownership read yields to the event loop, as real fs I/O does.
			await new Promise((resolve) => setTimeout(resolve, 10));
			return JSON.stringify({ pid: 42, port: 3002, owner: "app", appRunId: "test-run" });
		},
		fetch: request,
		disposeBrowserRuntimeLink: vi.fn(), disposeBrowserStreamLink: vi.fn(),
		trayLifecycle: { dispose: vi.fn() }, browserCleanupComplete: true,
	});
	app.quit.mockImplementation(() => {
		app.emit("before-quit", { preventDefault: vi.fn() });
		if (context.agentQuitCleanupComplete) cleanupFinished();
	});
	vm.runInContext(script, context);
	try {
		app.emit("before-quit", { preventDefault: vi.fn() });
		await finished;
		expect(app.exit).not.toHaveBeenCalled();
		expect(request).toHaveBeenCalledWith("http://127.0.0.1:3002/shutdown", expect.objectContaining({
			method: "POST", body: JSON.stringify({ stopAgents: false, appRunId: "test-run" }),
		}));
		expect(context.desktopAgentShutdownAccepted).toBe(true);
	} finally {
		vm.runInContext("desktopQuit.cancelQuit()", context);
	}
});

it("sends no shutdown when the window cancels an outside quit during the ownership read", async () => {
	const app = Object.assign(new EventEmitter(), { quit: vi.fn(), exit: vi.fn() });
	const request = vi.fn(async () => ({ ok: true, status: 202 }));
	let readDone!: () => void;
	const read = new Promise<void>((resolve) => { readDone = resolve; });
	const context: vm.Context = vm.createContext({
		app, console, setTimeout, clearTimeout, AbortSignal,
		process: { platform: "darwin", env: {} },
		createDesktopQuitController,
		trayController: {}, mainWindow: null, createWindowPromise: null,
		browserQuitRequested: false, preserveAgentsOnQuit: false,
		isUpdateRestartRequested: () => false,
		chatDraftRisks: [], chatDraftQuitConfirmed: false, chatDraftDialog: {},
		confirmUnsafeChatDraftLeave: () => true,
		dialog: { showMessageBoxSync: vi.fn() },
		cancelDaemonAutoRestart: vi.fn(),
		lifecycleLog: vi.fn(),
		agentQuitCleanupComplete: false, agentQuitCleanupPromise: null,
		requestDesktopShutdown, keepDaemonAlive: () => false, appRunId: "test-run",
		daemonStatus: { state: "ready", port: 3002 }, daemonProcess: null,
		desktopAgentShutdownAccepted: false,
		runFilePath: () => "/isolated/running.json", parseRunFile,
		readFile: async () => {
			await new Promise((resolve) => setTimeout(resolve, 10));
			// The draft guard keeps the window open while the read is in flight,
			// as the will-prevent-unload and close handlers in main do.
			vm.runInContext("if (desktopQuit.windowCloseCanceled()) cancelPendingQuit()", context);
			readDone();
			return JSON.stringify({ pid: 42, port: 3002, owner: "app", appRunId: "test-run" });
		},
		fetch: request,
		disposeBrowserRuntimeLink: vi.fn(), disposeBrowserStreamLink: vi.fn(),
		trayLifecycle: { dispose: vi.fn() }, browserCleanupComplete: true,
	});
	vm.runInContext(script, context);
	try {
		app.emit("before-quit", { preventDefault: vi.fn() });
		await read;
		await vi.waitFor(() => expect(context.agentQuitCleanupPromise).toBeNull());
		expect(request).not.toHaveBeenCalled();
		expect(app.quit).not.toHaveBeenCalled();
		expect(context.agentQuitCleanupComplete).toBe(false);
		expect(context.desktopAgentShutdownAccepted).toBe(false);
		expect(context.preserveAgentsOnQuit).toBe(false);
		expect(app.exit).not.toHaveBeenCalled();
	} finally {
		vm.runInContext("desktopQuit.cancelQuit()", context);
	}
});

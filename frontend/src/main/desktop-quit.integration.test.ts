// @vitest-environment node
import { EventEmitter } from "node:events";
import { readFileSync } from "node:fs";
import vm from "node:vm";
import ts from "typescript";
import { expect, it, vi } from "vitest";
import { createDesktopQuitController } from "./desktop-quit";
import { requestDesktopShutdown } from "./desktop-shutdown";
import { createExitFeedbackGate } from "./exit-feedback";
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
		exitFeedbackGate: { intercept: () => false, reset: vi.fn() },
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
		exitFeedbackGate: { intercept: () => false, reset: vi.fn() },
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

// The first-day exit feedback gate, as main builds it, with a dialog that would show.
function feedbackGate() {
	const request = vi.fn(() => true);
	const gate = createExitFeedbackGate({
		decide: () => "no_project" as const, request, markShown: vi.fn(), notifySecondClose: vi.fn(),
		flush: async () => undefined, setTimeout, clearTimeout: (handle) => clearTimeout(handle as ReturnType<typeof setTimeout>),
	});
	return { gate, request };
}

function feedbackContext(gate: ReturnType<typeof createExitFeedbackGate>) {
	const app = Object.assign(new EventEmitter(), { quit: vi.fn(), exit: vi.fn(), dock: { hide: vi.fn() } });
	const shutdown = vi.fn(async () => ({ ok: true, status: 202 }));
	const mainWindow = { close: vi.fn() };
	const context: vm.Context = vm.createContext({
		app, console, setTimeout, clearTimeout, AbortSignal,
		process: { platform: "darwin", env: {} },
		createDesktopQuitController,
		trayController: {}, mainWindow, createWindowPromise: null,
		browserQuitRequested: false, preserveAgentsOnQuit: false,
		isUpdateRestartRequested: () => false, clearUpdateRestartRequest: vi.fn(),
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
		readFile: async () => JSON.stringify({ pid: 42, port: 3002, owner: "app", appRunId: "test-run" }),
		fetch: shutdown,
		disposeBrowserRuntimeLink: vi.fn(), disposeBrowserStreamLink: vi.fn(),
		trayLifecycle: { dispose: vi.fn() }, browserCleanupComplete: true,
		exitFeedbackGate: gate,
	});
	app.quit.mockImplementation(() => { app.emit("before-quit", { preventDefault: vi.fn() }); });
	vm.runInContext(script, context);
	return { app, context, shutdown, mainWindow };
}

it("never holds an outside quit for the exit feedback prompt", async () => {
	const { gate, request } = feedbackGate();
	const { app, context, shutdown, mainWindow } = feedbackContext(gate);
	try {
		app.emit("before-quit", { preventDefault: vi.fn() });
		await vi.waitFor(() => expect(context.agentQuitCleanupComplete).toBe(true));
		expect(request).not.toHaveBeenCalled();
		expect(mainWindow.close).not.toHaveBeenCalled();
		expect(shutdown).toHaveBeenCalledWith("http://127.0.0.1:3002/shutdown", expect.objectContaining({
			body: JSON.stringify({ stopAgents: false, appRunId: "test-run" }),
		}));
	} finally {
		vm.runInContext("desktopQuit.cancelQuit()", context);
	}
});

it("still only hides to the tray when an in-app Quit resumes after the exit feedback prompt", async () => {
	const { gate, request } = feedbackGate();
	const { app, context, shutdown, mainWindow } = feedbackContext(gate);
	vm.runInContext("desktopQuit.backgroundQuit()", context);
	expect(request).toHaveBeenCalledOnce();
	expect(mainWindow.close).not.toHaveBeenCalled();
	gate.rendererState("shown");
	gate.rendererState("done");
	await vi.waitFor(() => expect(mainWindow.close).toHaveBeenCalledOnce());
	expect(context.preserveAgentsOnQuit).toBe(false);
	expect(context.agentQuitCleanupPromise).toBeNull();
	expect(shutdown).not.toHaveBeenCalled();
	expect(app.exit).not.toHaveBeenCalled();
	// A later outside quit still exits, without asking again.
	app.emit("before-quit", { preventDefault: vi.fn() });
	await vi.waitFor(() => expect(context.agentQuitCleanupComplete).toBe(true));
	expect(request).toHaveBeenCalledOnce();
	vm.runInContext("desktopQuit.cancelQuit()", context);
});

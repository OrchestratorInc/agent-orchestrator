// @vitest-environment node
import { EventEmitter } from "node:events";
import { readFileSync } from "node:fs";
import vm from "node:vm";
import ts from "typescript";
import { describe, expect, it, vi } from "vitest";
import { createDesktopQuitController } from "./desktop-quit";
import { requestDesktopShutdown } from "./desktop-shutdown";
import { isTrayEnabled } from "./tray-lifecycle";

// Execute the production handlers, following daemon-auto-restart's VM harness.
// This catches wiring mistakes that isolated controller tests cannot observe.
const source = ts.createSourceFile("main.ts", readFileSync(new URL("../main.ts", import.meta.url), "utf8"), ts.ScriptTarget.Latest, true);
const functions = new Set(["focusMainWindow", "createWindow", "isDesktopExiting", "shutdownDaemonForDesktopQuit"]);
const handlers = new Set(["before-quit", "window-all-closed", "second-instance"]);
const statements = source.statements.filter((node) => {
	if (ts.isFunctionDeclaration(node)) return functions.has(node.name?.text ?? "");
	if (ts.isVariableStatement(node)) return node.declarationList.declarations.some((decl) => decl.name.getText(source) === "desktopQuit");
	if (!ts.isExpressionStatement(node) || !ts.isCallExpression(node.expression)) return false;
	const call = node.expression;
	return call.expression.getText(source) === "app.on" && ts.isStringLiteral(call.arguments[0]) && handlers.has(call.arguments[0].text);
}).map((node) => node.getText(source));
const windowStatements: string[] = [];
function collectWindowHandlers(node: ts.Node): void {
	if (ts.isExpressionStatement(node) && ts.isCallExpression(node.expression)) {
		const call = node.expression;
		if (call.expression.getText(source) === "mainWindow.on" && ts.isStringLiteral(call.arguments[0]) && ["close", "session-end"].includes(call.arguments[0].text)) {
			windowStatements.push(node.getText(source));
		}
	}
	ts.forEachChild(node, collectWindowHandlers);
}
collectWindowHandlers(source);
const transpile = (text: string) => ts.transpileModule(text, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText;

function setup(platform: NodeJS.Platform = "win32", withTray = true) {
	const app = Object.assign(new EventEmitter(), { quit: vi.fn(), exit: vi.fn(), dock: { hide: vi.fn(), show: vi.fn() } });
	const fetch = vi.fn(async (_url: string, _init: RequestInit) => ({ ok: true, status: 202 }));
	const trayLifecycle = { dispose: vi.fn() };
	const confirmLeave = vi.fn(() => false);
	const guard = vi.fn(() => false);
	const navigated = vi.fn();
	const makeEvent = () => ({ prevented: false, preventDefault() { this.prevented = true; } });
	class FakeWindow extends EventEmitter {
		show = vi.fn();
		focus = vi.fn();
		restore = vi.fn();
		isMinimized = vi.fn(() => false);
		close() {
			const event = makeEvent();
			this.emit("close", event);
			if (event.prevented) return;
			context.mainWindow = null;
			app.emit("window-all-closed");
		}
	}
	const createWindowInternal = vi.fn(async () => {
		await Promise.resolve();
		context.mainWindow = new FakeWindow();
		vm.runInContext(transpile(windowStatements.join("\n")), context);
	});
	const context = vm.createContext({
		process: { platform, env: {} }, app, console, Promise, AbortSignal,
		createDesktopQuitController, requestDesktopShutdown, isTrayEnabled,
		browserQuitRequested: false, windowsSessionEnding: false,
		agentQuitCleanupPromise: null as Promise<unknown> | null, agentQuitCleanupComplete: false,
		browserCleanupComplete: false, browserQuitCleanupPromise: null,
		preserveAgentsOnQuit: false, desktopAgentShutdownAccepted: false,
		createWindowPromise: null, mainWindow: new FakeWindow() as FakeWindow | null,
		createWindowInternal, trayController: withTray ? {} : null, trayLifecycle,
		chatDraftRisks: [] as string[], chatDraftQuitConfirmed: false, chatDraftWindowCloseConfirmed: false,
		chatDraftDialog: undefined, confirmUnsafeChatDraftLeave: confirmLeave,
		shouldPreventUnsafeChatDraftClose: guard, dialog: { showMessageBoxSync: vi.fn() },
		isUpdateRestartRequested: () => context.restarting,
		clearUpdateRestartRequest: () => { context.restarting = false; }, restarting: false,
		cancelDaemonAutoRestart: vi.fn(), keepDaemonAlive: () => false,
		appRunId: "desktop-1", daemonStatus: { state: "ready", pid: 100, port: 3002 },
		daemonProcess: null, daemonStoppingProcess: null,
		runFilePath: () => "/isolated/running.json", parseRunFile: JSON.parse,
		readFile: async () => JSON.stringify({ owner: "app", appRunId: "desktop-1", port: 3002 }),
		fetch, lifecycleLog: vi.fn(), disposeBrowserRuntimeLink: vi.fn(), disposeBrowserStreamLink: vi.fn(),
		disposeAllBrowserViewHosts: async () => undefined,
		remoteRegistry: { closeAll: async () => undefined }, cloudAgentProxy: { close: async () => undefined },
		telemetryPolicyController: null, flushDaemonUsage: async () => undefined,
		sideLaunchState: undefined, updateQuitDeadlineArmed: false, finishUpdateQuit: vi.fn(),
		handleCloudDeepLinkAndFocus: navigated,
		parseOpenFolderPathArg: (argv: string[]) => argv.find((arg) => arg.startsWith("folder:"))?.slice(7),
		getShellWebContents: (): { isLoading(): boolean; send: ReturnType<typeof vi.fn> } | null => null,
		pendingFolderPath: undefined, OPEN_FOLDER_PATH_CHANNEL: "open-folder",
	});
	app.quit.mockImplementation(() => {
		const event = makeEvent();
		app.emit("before-quit", event);
		if (!event.prevented) app.emit("quit");
	});
	vm.runInContext(transpile(statements.join("\n")), context);
	vm.runInContext(transpile(windowStatements.join("\n")), context);
	const controller = vm.runInContext("desktopQuit", context) as ReturnType<typeof createDesktopQuitController>;
	const focus = () => vm.runInContext("focusMainWindow()", context);
	const settled = async () => { for (let turn = 0; turn < 30; turn++) await Promise.resolve(); };
	return { app, context, controller, fetch, trayLifecycle, confirmLeave, guard, createWindowInternal, focus, settled, navigated };
}

describe("desktop tray event wiring", () => {
	it.each(["close", "ordinary quit"])("preserves the daemon on Windows %s", async (action) => {
		const h = setup();
		if (action === "close") h.context.mainWindow!.close();
		else h.controller.backgroundQuit();
		await h.settled();
		expect(h.context.mainWindow).toBeNull();
		expect(h.fetch).not.toHaveBeenCalled();
		expect(h.trayLifecycle.dispose).not.toHaveBeenCalled();
		expect(h.context.browserQuitRequested).toBe(false);
	});

	it.each(["win32", "linux"] as const)("fully quits %s when there is no tray", async (platform) => {
		const h = setup(platform, false);
		h.context.mainWindow!.close();
		await h.settled();
		expect(JSON.parse(h.fetch.mock.calls[0][1].body as string)).toEqual({ stopAgents: true, appRunId: "desktop-1" });
		expect(h.context.browserQuitRequested).toBe(true);
	});

	it.each([false, true])("distinguishes full quit from update restart (update=%s)", async (restarting) => {
		const h = setup();
		h.context.restarting = restarting;
		h.controller.quitCompletely();
		h.focus();
		await h.settled();
		expect(JSON.parse(h.fetch.mock.calls[0][1].body as string)).toEqual({ stopAgents: !restarting, appRunId: "desktop-1" });
		expect(h.createWindowInternal).not.toHaveBeenCalled();
		expect(h.context.browserQuitRequested).toBe(true);
		expect(h.trayLifecycle.dispose).toHaveBeenCalled();
	});

	it("does not intercept an unmarked Windows exit", async () => {
		const h = setup();
		h.app.quit();
		await h.settled();
		expect(h.fetch.mock.calls[0][0]).toContain("/shutdown");
		expect(h.context.browserQuitRequested).toBe(true);
	});

	it("clears canceled update intent before a later full quit", async () => {
		const h = setup();
		h.context.restarting = true;
		h.context.chatDraftRisks = ["unsent"];
		h.controller.quitCompletely();
		expect(h.context.restarting).toBe(false);
		expect(h.fetch).not.toHaveBeenCalled();
		h.context.chatDraftRisks = [];
		h.controller.backgroundQuit();
		expect(h.context.mainWindow).toBeNull();
		h.controller.quitCompletely();
		await h.settled();
		expect(JSON.parse(h.fetch.mock.calls[0][1].body as string).stopAgents).toBe(true);
	});

	it("allows canceling drafts before close and full quit, then backgrounds normally", async () => {
		const h = setup();
		h.context.chatDraftRisks = ["unsent"];
		h.guard.mockReturnValueOnce(true);
		h.controller.backgroundQuit();
		expect(h.context.mainWindow).not.toBeNull();
		h.controller.quitCompletely();
		expect(h.fetch).not.toHaveBeenCalled();
		h.context.chatDraftRisks = [];
		h.controller.backgroundQuit();
		await h.settled();
		expect(h.context.mainWindow).toBeNull();
		expect(h.fetch).not.toHaveBeenCalled();
	});

	it("recreates one dashboard for concurrent launches and tray clicks", async () => {
		const h = setup();
		h.context.mainWindow!.close();
		h.app.emit("second-instance", {}, ["ao.exe"]);
		h.app.emit("second-instance", {}, ["ao.exe"]);
		h.focus();
		await h.settled();
		expect(h.createWindowInternal).toHaveBeenCalledOnce();
		expect(h.context.mainWindow).not.toBeNull();
		expect(h.fetch).not.toHaveBeenCalled();
	});

	it("queues a folder while recreating the dashboard", async () => {
		const h = setup();
		h.context.mainWindow!.close();
		h.app.emit("second-instance", {}, ["ao.exe", "folder:C:/project"]);
		await h.settled();
		expect(h.context.pendingFolderPath).toBe("C:/project");
		expect(h.createWindowInternal).toHaveBeenCalledOnce();
	});

	it.each([false, true])("routes a folder according to renderer loading state (loading=%s)", (loading) => {
		const h = setup();
		const send = vi.fn();
		h.context.getShellWebContents = () => ({ isLoading: () => loading, send });
		h.app.emit("second-instance", {}, ["ao.exe", "folder:C:/project"]);
		if (loading) {
			expect(send).not.toHaveBeenCalled();
			expect(h.context.pendingFolderPath).toBe("C:/project");
		} else {
			expect(send).toHaveBeenCalledWith("open-folder", "C:/project");
		}
	});

	it("restores the existing minimized dashboard", () => {
		const h = setup();
		const window = h.context.mainWindow!;
		window.isMinimized.mockReturnValue(true);
		h.app.emit("second-instance", {}, ["ao.exe"]);
		expect(window.restore).toHaveBeenCalledOnce();
		expect(window.show).toHaveBeenCalledOnce();
		expect(window.focus).toHaveBeenCalledOnce();
		expect(h.createWindowInternal).not.toHaveBeenCalled();
	});

	it("keeps the tray usable and retries a failed dashboard recreation", async () => {
		const h = setup();
		const error = vi.spyOn(console, "error").mockImplementation(() => undefined);
		try {
			h.context.mainWindow!.close();
			h.createWindowInternal.mockRejectedValueOnce(new Error("renderer unavailable"));
			h.focus();
			await h.settled();
			expect(error).toHaveBeenCalled();
			expect(h.context.trayController).not.toBeNull();
			h.focus();
			await h.settled();
			expect(h.context.mainWindow).not.toBeNull();
			expect(h.createWindowInternal).toHaveBeenCalledTimes(2);
			expect(h.fetch).not.toHaveBeenCalled();
		} finally { error.mockRestore(); }
	});

	it("forwards deep links and ignores launches during full exit", async () => {
		const h = setup();
		h.app.emit("second-instance", {}, ["ao.exe", "ao-app://sessions/p/s"]);
		expect(h.navigated).toHaveBeenCalledWith("ao-app://sessions/p/s");
		h.controller.quitCompletely();
		h.app.emit("second-instance", {}, ["ao.exe", "ao-app://sessions/p/other"]);
		await h.settled();
		expect(h.navigated).toHaveBeenCalledOnce();
	});

	it("does not latch a shutdown query, but exits without draft prompts on session end", async () => {
		const h = setup();
		h.context.mainWindow!.emit("query-session-end", { preventDefault: vi.fn() });
		expect(h.context.windowsSessionEnding).toBe(false);
		h.context.chatDraftRisks = ["unsent"];
		h.context.mainWindow!.emit("session-end");
		// A final renderer draft report must not bring back a shutdown prompt.
		h.context.chatDraftQuitConfirmed = false;
		h.context.chatDraftWindowCloseConfirmed = false;
		h.context.mainWindow!.close();
		h.focus();
		await h.settled();
		expect(h.confirmLeave).not.toHaveBeenCalled();
		expect(h.guard).not.toHaveBeenCalled();
		expect(h.context.windowsSessionEnding).toBe(true);
		expect(h.createWindowInternal).not.toHaveBeenCalled();
		expect(h.context.browserQuitRequested).toBe(true);
	});
});

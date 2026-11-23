// @vitest-environment node
import { EventEmitter } from "node:events";
import { readFileSync } from "node:fs";
import vm from "node:vm";
import ts from "typescript";
import { describe, expect, it, vi } from "vitest";
import { createDesktopQuitController } from "./desktop-quit";
import { requestDesktopShutdown } from "./desktop-shutdown";
import { isTrayEnabled } from "./tray-lifecycle";
import { TRAY_RENDERER_READY_CHANNEL } from "../shared/tray";
import { parseSharedArtifactLink } from "../../../packages/shared/artifacts/shared-artifact-link";
import { SET_CHAT_DRAFT_RISK_CHANNEL, parseChatDraftBoundaryKinds, parseChatDraftDialogCopy } from "../shared/chat-draft-risk";

// Execute the production handlers, following daemon-auto-restart's VM harness.
// This catches wiring mistakes that isolated controller tests cannot observe.
const source = ts.createSourceFile("main.ts", readFileSync(new URL("../main.ts", import.meta.url), "utf8"), ts.ScriptTarget.Latest, true);
const functions = new Set(["focusMainWindow", "createWindow", "isDesktopExiting", "shutdownDaemonForDesktopQuit", "confirmDesktopQuit", "commitDesktopQuit"]);
const handlers = new Set(["before-quit", "window-all-closed", "second-instance"]);
const statements = source.statements.filter((node) => {
	if (ts.isFunctionDeclaration(node)) return functions.has(node.name?.text ?? "");
	if (ts.isVariableStatement(node)) return node.declarationList.declarations.some((decl) => decl.name.getText(source) === "desktopQuit");
	if (!ts.isExpressionStatement(node) || !ts.isCallExpression(node.expression)) return false;
	const call = node.expression;
	const target = call.expression.getText(source);
	return (target === "app.on" && ts.isStringLiteral(call.arguments[0]) && handlers.has(call.arguments[0].text)) ||
		(target === "ipcMain.on" && ["TRAY_RENDERER_READY_CHANNEL", "SET_CHAT_DRAFT_RISK_CHANNEL"].includes(call.arguments[0]?.getText(source)));
}).map((node) => node.getText(source));
const windowStatements: string[] = [];
function collectWindowHandlers(node: ts.Node): void {
	if (ts.isExpressionStatement(node) && ts.isCallExpression(node.expression)) {
		const call = node.expression;
		if ((call.expression.getText(source) === "mainWindow.on" && ts.isStringLiteral(call.arguments[0]) && ["close", "query-session-end", "session-end"].includes(call.arguments[0].text)) ||
			(call.expression.getText(source) === "shellWebContents.on" && ts.isStringLiteral(call.arguments[0]) && call.arguments[0].text === "will-prevent-unload")) {
			windowStatements.push(node.getText(source));
		}
	}
	ts.forEachChild(node, collectWindowHandlers);
}
collectWindowHandlers(source);
const transpile = (text: string) => ts.transpileModule(text, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText;

function setup(platform: NodeJS.Platform = "win32", withTray = true) {
	const app = Object.assign(new EventEmitter(), { isPackaged: false, quit: vi.fn(), exit: vi.fn(), dock: { hide: vi.fn(), show: vi.fn() } });
	const ipcMain = new EventEmitter();
	const fetch = vi.fn(async (_url: string, _init: RequestInit) => ({ ok: true, status: 202 }));
	const trayLifecycle = { dispose: vi.fn(), handleRendererReady: vi.fn() };
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
		process: { platform, env: {} }, app, ipcMain, console, Promise, AbortSignal,
		createDesktopQuitController, requestDesktopShutdown, isTrayEnabled,
		browserQuitRequested: false, windowsSessionEnding: false, desktopQuitCommitted: false,
		agentQuitCleanupPromise: null as Promise<unknown> | null, agentQuitCleanupComplete: false,
		browserCleanupComplete: false, browserQuitCleanupPromise: null,
		preserveAgentsOnQuit: false, desktopAgentShutdownAccepted: false,
		createWindowPromise: null, mainWindow: new FakeWindow() as FakeWindow | null,
		createWindowInternal, trayController: withTray ? {} : null, trayLifecycle,
		chatDraftRisks: [] as string[], chatDraftQuitConfirmed: false, chatDraftWindowCloseConfirmed: false,
		chatDraftDialog: undefined, confirmUnsafeChatDraftLeave: confirmLeave,
		SET_CHAT_DRAFT_RISK_CHANNEL, parseChatDraftBoundaryKinds, parseChatDraftDialogCopy,
		shellWebContents: new EventEmitter(),
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
		pendingFolderPath: null as string | null, OPEN_FOLDER_PATH_CHANNEL: "open-folder",
		TRAY_RENDERER_READY_CHANNEL, pendingSessionDeepLink: null, pendingBillingSection: null,
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
	const rendererReady = (sender = context.getShellWebContents()) => ipcMain.emit(TRAY_RENDERER_READY_CHANNEL, { sender });
	const draftContents = { isLoading: () => false, isDestroyed: () => false, send: vi.fn() };
	const reportDraftRisk = (risky = true) => {
		context.getShellWebContents = () => context.mainWindow ? draftContents : null;
		ipcMain.emit(SET_CHAT_DRAFT_RISK_CHANNEL, { sender: draftContents }, risky ? ["persistence-failed"] : [],
			{ title: "Unsaved draft", message: "Leave?", detail: "Copy your draft first.", stay: "Stay", leave: "Leave" });
	};
	return { app, context, controller, fetch, trayLifecycle, confirmLeave, guard, createWindowInternal, focus, settled, navigated, rendererReady, reportDraftRisk };
}

function deferred<T>() {
	let resolve!: (value: T) => void;
	let reject!: (reason: unknown) => void;
	const promise = new Promise<T>((done, fail) => { resolve = done; reject = fail; });
	return { promise, resolve, reject };
}

describe("full quit commitment", () => {
	const handshake = JSON.stringify({ owner: "app", appRunId: "desktop-1", port: 3002 });

	it.each(["win32", "darwin"] as const)("keeps %s usable after Stay during preparation, then allows a later full quit", async (platform) => {
		const h = setup(platform);
		const read = deferred<string>();
		h.context.readFile = vi.fn().mockReturnValueOnce(read.promise).mockResolvedValue(handshake);
		h.controller.quitCompletely();
		h.reportDraftRisk();
		h.controller.quitCompletely();
		read.resolve(handshake);
		await h.settled();
		expect(h.confirmLeave).toHaveBeenCalledOnce();
		expect(h.fetch).not.toHaveBeenCalled();
		expect(h.trayLifecycle.dispose).not.toHaveBeenCalled();
		expect(h.context.agentQuitCleanupPromise).toBeNull();
		expect(h.context.agentQuitCleanupComplete).toBe(false);
		h.focus();
		h.app.emit("second-instance", {}, ["ao.exe"]);
		expect(h.context.mainWindow!.focus).toHaveBeenCalledTimes(2);
		h.reportDraftRisk(false);
		h.context.mainWindow!.close();
		h.focus();
		await h.settled();
		expect(h.createWindowInternal).toHaveBeenCalledOnce();
		h.controller.quitCompletely();
		await h.settled();
		expect(h.fetch.mock.calls.filter(([url]) => url.endsWith("/shutdown"))).toHaveLength(1);
		expect(JSON.parse(h.fetch.mock.calls[0][1].body as string).stopAgents).toBe(true);
		expect(h.context.browserQuitRequested).toBe(true);
	});

	it("allows Leave for a draft reported during preparation before sending shutdown", async () => {
		const h = setup();
		const read = deferred<string>();
		h.context.readFile = () => read.promise;
		h.controller.quitCompletely();
		h.reportDraftRisk();
		h.confirmLeave.mockReturnValue(true);
		read.resolve(handshake);
		await h.settled();
		expect(h.confirmLeave).toHaveBeenCalledOnce();
		expect(h.fetch).toHaveBeenCalled();
		expect(h.context.browserQuitRequested).toBe(true);
	});

	it.each(["request", "browser cleanup"])("ignores late cancellation while %s is pending after shutdown was sent", async (phase) => {
		const h = setup();
		const pending = deferred<{ ok: boolean; status: number }>();
		const retired = deferred<void>();
		if (phase === "request") h.fetch.mockReturnValueOnce(pending.promise);
		else h.context.remoteRegistry.closeAll = () => retired.promise;
		const quit = vi.fn();
		h.app.on("quit", quit);
		h.controller.quitCompletely();
		await h.settled();
		expect(h.fetch.mock.calls[0][0]).toContain("/shutdown");
		h.reportDraftRisk();
		h.controller.quitCompletely();
		const close = { preventDefault: vi.fn() };
		h.context.mainWindow!.emit("close", close);
		const unload = { preventDefault: vi.fn() };
		h.context.shellWebContents.emit("will-prevent-unload", unload);
		expect(close.preventDefault).not.toHaveBeenCalled();
		expect(unload.preventDefault).toHaveBeenCalled();
		expect(h.guard).not.toHaveBeenCalled();
		pending.resolve({ ok: true, status: 202 });
		retired.resolve();
		await h.settled();
		expect(h.confirmLeave).not.toHaveBeenCalled();
		expect(h.fetch.mock.calls.filter(([url]) => url.endsWith("/shutdown"))).toHaveLength(1);
		expect(h.trayLifecycle.dispose).toHaveBeenCalled();
		expect(quit).toHaveBeenCalled();
	});

	it("finishes Ronish's pending-shutdown reproduction without offering a late Stay", async () => {
		const h = setup();
		const pending = deferred<{ ok: boolean; status: number }>();
		h.fetch.mockReturnValueOnce(pending.promise);
		const quit = vi.fn();
		h.app.on("quit", quit);
		h.controller.quitCompletely();
		await h.settled();
		h.reportDraftRisk();
		pending.resolve({ ok: true, status: 202 });
		await h.settled();
		expect(h.confirmLeave).not.toHaveBeenCalled();
		expect(quit).toHaveBeenCalled();
		expect(h.context.browserQuitRequested).toBe(true);
	});

	it.each(["unowned", "read failure"])("allows a late Stay before local teardown when ownership preparation yields %s", async (outcome) => {
		const h = setup();
		const read = deferred<string>();
		const error = vi.spyOn(console, "error").mockImplementation(() => undefined);
		try {
			h.context.readFile = () => read.promise;
			h.controller.quitCompletely();
			h.reportDraftRisk();
			if (outcome === "read failure") read.reject(new Error("unreadable ownership record"));
			else read.resolve(JSON.stringify({ owner: "persistent", port: 3002 }));
			await h.settled();
			expect(h.confirmLeave).toHaveBeenCalledOnce();
			expect(h.fetch).not.toHaveBeenCalled();
			expect(h.trayLifecycle.dispose).not.toHaveBeenCalled();
			h.focus();
			expect(h.context.mainWindow!.focus).toHaveBeenCalledOnce();
		} finally { error.mockRestore(); }
	});

	it("finishes a committed quit even if the shutdown request fails after a late draft report", async () => {
		const h = setup();
		const pending = deferred<{ ok: boolean; status: number }>();
		const error = vi.spyOn(console, "error").mockImplementation(() => undefined);
		try {
			h.fetch.mockReturnValueOnce(pending.promise);
			const quit = vi.fn();
			h.app.on("quit", quit);
			h.controller.quitCompletely();
			await h.settled();
			h.reportDraftRisk();
			pending.reject(new Error("shutdown response timed out"));
			await h.settled();
			expect(h.confirmLeave).not.toHaveBeenCalled();
			expect(quit).toHaveBeenCalled();
		} finally { error.mockRestore(); }
	});

	it("clears update preservation after Stay during preparation", async () => {
		const h = setup();
		const read = deferred<string>();
		h.context.readFile = () => read.promise;
		h.context.restarting = true;
		h.context.preserveAgentsOnQuit = true;
		h.controller.quitCompletely();
		h.reportDraftRisk();
		read.resolve(handshake);
		await h.settled();
		expect(h.fetch).not.toHaveBeenCalled();
		expect(h.context.restarting).toBe(false);
		expect(h.context.preserveAgentsOnQuit).toBe(false);
		h.reportDraftRisk(false);
		h.controller.quitCompletely();
		await h.settled();
		expect(JSON.parse(h.fetch.mock.calls[0][1].body as string).stopAgents).toBe(true);
	});

	it("honors committed Windows session end while preparation is pending", async () => {
		const h = setup();
		const read = deferred<string>();
		h.context.readFile = () => read.promise;
		h.controller.quitCompletely();
		h.reportDraftRisk();
		h.context.mainWindow!.emit("session-end");
		read.resolve(handshake);
		await h.settled();
		expect(h.confirmLeave).not.toHaveBeenCalled();
		expect(h.context.browserQuitRequested).toBe(true);
	});
});

function setupDeepLinks() {
	const h = setup();
	Object.assign(h.app, { whenReady: vi.fn(async () => undefined) });
	Object.assign(h.context, {
		URL, parseSharedArtifactLink, SHARED_ARTIFACT_VIEWER_ORIGINS: ["https://orchestrator.inc"],
		windowsAwaitingFirstShow: new Set(), pendingSharedArtifacts: [], MAX_PENDING_SHARED_ARTIFACTS: 20,
		openSharedArtifact: vi.fn(), OPEN_SETTINGS_SHORTCUT_CHANNEL: "open-settings",
	});
	const names = new Set(["focusCloudWindow", "isShareDeepLink", "isBillingDeepLink", "handleCloudDeepLinkAndFocus"]);
	const declarations = source.statements.filter((node) => ts.isFunctionDeclaration(node) && names.has(node.name?.text ?? ""));
	vm.runInContext(transpile(declarations.map((node) => node.getText(source)).join("\n")), h.context);
	return { ...h, open: (url: string) => h.context.handleCloudDeepLinkAndFocus(url) as Promise<void> };
}

describe("tray-resident deep links", () => {
	const viewer = "https://orchestrator.inc/artifact/abcdefghijklmnopqrstuvwxyz";
	const link = `ao-app://shared-artifact?url=${encodeURIComponent(viewer)}`;

	it("recreates the closed dashboard and queues a published artifact for its renderer", async () => {
		const h = setupDeepLinks();
		h.context.mainWindow!.close();
		await h.open(link);
		expect(h.createWindowInternal).toHaveBeenCalledOnce();
		expect(h.context.pendingSharedArtifacts).toEqual([viewer]);
		expect(h.context.openSharedArtifact).not.toHaveBeenCalled();
	});

	it("delivers a published artifact to the existing dashboard without duplicating its queue entry", async () => {
		const h = setupDeepLinks();
		const contents = { isLoading: () => false, isDestroyed: () => false, send: vi.fn() };
		h.context.getShellWebContents = () => contents;
		await h.open(link);
		await h.open(link);
		expect(contents.send).toHaveBeenCalledWith("cloud:openSharedArtifact", viewer);
		expect(h.context.pendingSharedArtifacts).toEqual([viewer]);
		expect(h.createWindowInternal).not.toHaveBeenCalled();
	});

	it("recreates the closed dashboard for a billing link and queues its destination", async () => {
		const h = setupDeepLinks();
		h.context.mainWindow!.close();
		await h.open("ao-app://billing");
		await h.settled();
		expect(h.createWindowInternal).toHaveBeenCalledOnce();
		expect(h.context.pendingBillingSection).toBe("billing");
	});

	it("ignores artifact links once desktop shutdown has started", async () => {
		const h = setupDeepLinks();
		h.context.browserQuitRequested = true;
		await h.open(link);
		expect(h.createWindowInternal).not.toHaveBeenCalled();
		expect(h.context.pendingSharedArtifacts).toEqual([]);
		expect(h.context.openSharedArtifact).not.toHaveBeenCalled();
	});

	it("does not reopen for an artifact if shutdown starts while awaiting app readiness", async () => {
		const h = setupDeepLinks();
		h.context.mainWindow!.close();
		let ready!: () => void;
		h.context.app.whenReady.mockImplementation(() => new Promise<void>((resolve) => { ready = resolve; }));
		const pending = h.open(link);
		h.context.browserQuitRequested = true;
		ready();
		await pending;
		expect(h.createWindowInternal).not.toHaveBeenCalled();
		expect(h.context.pendingSharedArtifacts).toEqual([]);
		expect(h.context.openSharedArtifact).not.toHaveBeenCalled();
	});

	it("does not open a fallback artifact window if recreation fails during shutdown", async () => {
		const h = setupDeepLinks();
		h.context.mainWindow!.close();
		h.createWindowInternal.mockImplementationOnce(async () => {
			h.context.browserQuitRequested = true;
			throw new Error("window closed during shutdown");
		});
		await h.open(link);
		expect(h.context.pendingSharedArtifacts).toEqual([]);
		expect(h.context.openSharedArtifact).not.toHaveBeenCalled();
	});
});

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

	it("does not replay a stale queued folder after directly delivering a newer launch", () => {
		const h = setup();
		let loading = true;
		const contents = { isLoading: () => loading, send: vi.fn() };
		h.context.getShellWebContents = () => contents;
		h.app.emit("second-instance", {}, ["ao.exe", "folder:C:/folder-A"]);
		expect(h.context.pendingFolderPath).toBe("C:/folder-A");
		loading = false;
		h.app.emit("second-instance", {}, ["ao.exe", "folder:C:/folder-B"]);
		h.rendererReady();
		expect(contents.send.mock.calls).toEqual([["open-folder", "C:/folder-B"]]);
		expect(h.context.pendingFolderPath).toBeNull();
	});

	it("delivers only the newest folder queued while the renderer loads", () => {
		const h = setup();
		let loading = true;
		const contents = { isLoading: () => loading, send: vi.fn() };
		h.context.getShellWebContents = () => contents;
		h.app.emit("second-instance", {}, ["ao.exe", "folder:C:/folder-A"]);
		h.app.emit("second-instance", {}, ["ao.exe", "folder:C:/folder-B"]);
		loading = false;
		h.rendererReady();
		expect(contents.send.mock.calls).toEqual([["open-folder", "C:/folder-B"]]);
		expect(h.context.pendingFolderPath).toBeNull();
	});

	it("flushes a queued folder only once across repeated readiness signals", () => {
		const h = setup();
		const contents = { isLoading: () => true, send: vi.fn() };
		h.context.getShellWebContents = () => contents;
		h.app.emit("second-instance", {}, ["ao.exe", "folder:C:/folder-A"]);
		h.rendererReady();
		h.rendererReady();
		expect(contents.send.mock.calls).toEqual([["open-folder", "C:/folder-A"]]);
		expect(h.context.pendingFolderPath).toBeNull();
	});

	it("keeps a queued folder until readiness comes from the current renderer", () => {
		const h = setup();
		const contents = { isLoading: () => true, send: vi.fn() };
		h.context.getShellWebContents = () => contents;
		h.app.emit("second-instance", {}, ["ao.exe", "folder:C:/folder-A"]);
		const otherContents = { isLoading: () => false, send: vi.fn() };
		h.rendererReady(otherContents);
		expect(otherContents.send).not.toHaveBeenCalled();
		expect(contents.send).not.toHaveBeenCalled();
		expect(h.context.pendingFolderPath).toBe("C:/folder-A");
		h.rendererReady();
		expect(contents.send.mock.calls).toEqual([["open-folder", "C:/folder-A"]]);
		expect(h.context.pendingFolderPath).toBeNull();
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

import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { readFile, readdir } from "node:fs/promises";
import { createServer } from "node:http";
import path from "node:path";
import { app, BrowserWindow, WebContentsView } from "electron";
import { AgentBrowserRuntime } from "./agent-browser-runtime";
import { createBrowserViewHost } from "./browser-view-host";
import type { BrowserTabsState } from "./browser-view-host";

function requiredEnvironmentVariable(name: string): string {
	const value = process.env[name];
	if (!value) throw new Error(`${name} is required`);
	return value;
}

const fixturePath = requiredEnvironmentVariable("AO_BROWSER_POSTCONDITION_FIXTURE");
const userDataPath = requiredEnvironmentVariable("AO_BROWSER_POSTCONDITION_USER_DATA");
app.setPath("userData", userDataPath);

async function run(): Promise<void> {
	await app.whenReady();
	const fixture = await readFile(fixturePath, "utf8");
	const dragFixture = await readFile(path.join(path.dirname(fixturePath), "drag-regions.html"), "utf8");
	const server = createServer((request, response) => {
		if (request.url?.startsWith("/native-tab?")) {
			const name = new URL(request.url, "http://fixture").searchParams.get("name");
			assert.ok(name === "Third" || name === "Fourth" || name === "Transient");
			response.writeHead(200, { "content-type": "text/html; charset=utf-8" });
			response.end(`<!doctype html><title>${name} tab</title><h1>${name} page</h1>
				<button onclick="this.textContent='${name} changed'">Change ${name}</button>`);
			return;
		}
		if (request.url === "/native-popup-opener") {
			response.writeHead(200, { "content-type": "text/html; charset=utf-8" });
			response.end(`<!doctype html><title>Native popup opener</title>
				<a href="/native-popup-child" target="_blank">Open report link</a>
				<button onclick="window.open('/native-popup-child', '_blank')">Open report window</button>`);
			return;
		}
		if (request.url === "/native-popup-child") {
			response.writeHead(200, { "content-type": "text/html; charset=utf-8" });
			response.end(`<!doctype html><title>Native popup child</title><h1>Child report</h1>
				<button onclick="this.textContent='Child changed'">Change child</button>`);
			return;
		}
		if (request.url === "/drag-regions") {
			response.writeHead(200, { "content-type": "text/html; charset=utf-8" });
			response.end(dragFixture);
			return;
		}
		if (request.url === "/popup") {
			response.writeHead(200, { "content-type": "text/html; charset=utf-8" });
			response.end(`<!doctype html><title>OAuth popup</title><script>
				window.opener?.postMessage({kind: 'popup-ready', hasOpener: !!window.opener}, '*');
				addEventListener('message', event => {
					if (event.data !== 'complete') return;
					window.opener.postMessage({kind: 'auth-complete'}, '*');
					window.close();
				});
			</script>`);
			return;
		}
		if (request.url === "/fixture") {
			response.writeHead(200, {
				"content-type": "text/html; charset=utf-8",
				"content-length": Buffer.byteLength(fixture),
				connection: "close",
			});
			response.end(fixture);
			return;
		}
		if (request.url === "/destination") {
			const destination = "<!doctype html><title>Destination</title><p>Navigation completed</p>";
			response.writeHead(200, {
				"content-type": "text/html; charset=utf-8",
				"content-length": Buffer.byteLength(destination),
				connection: "close",
			});
			response.end(destination);
			return;
		}
		response.statusCode = 404;
		response.end("not found");
	});

	await new Promise<void>((resolve, reject) => {
		server.once("error", reject);
		server.listen(0, "127.0.0.1", resolve);
	});
	const address = server.address();
	if (!address || typeof address === "string") throw new Error("fixture server did not expose a TCP address");
	const fixtureURL = `http://127.0.0.1:${address.port}/fixture`;

	const window = new BrowserWindow({ show: true, width: 800, height: 600 });
	// Initialize the owning window before attaching a WebContentsView. On
	// Windows and Xvfb, a never-navigated BrowserWindow can leave the child
	// renderer unscheduled even though the native view is marked visible.
	await window.loadURL("data:text/html,<title>AO browser integration host</title>");
	window.show();
	let browserView: WebContentsView | undefined;
	let forceInitialPresentation = true;
	const browserViews = new Map<number, WebContentsView>();
	const IntegrationWebContentsView = function (options: ConstructorParameters<typeof WebContentsView>[0]) {
		const view = new WebContentsView(options);
		browserView = view;
		browserViews.set(view.webContents.id, view);
		// Warm the initial blank view before this fixture can show the panel.
		// Native scenarios then honor AO's real active/inactive tab layout.
		const setBounds = view.setBounds.bind(view);
		view.setBounds = (bounds) => setBounds(forceInitialPresentation ? { x: 0, y: 0, width: 800, height: 600 } : bounds);
		const setVisible = view.setVisible.bind(view);
		view.setVisible = (visible) => setVisible(forceInitialPresentation || visible);
		const loadURL = view.webContents.loadURL.bind(view.webContents);
		view.webContents.loadURL = async (url, loadOptions) => {
			let timeout: ReturnType<typeof setTimeout> | undefined;
			let onDOMReady: (() => void) | undefined;
			let domReady: Promise<void> | undefined;
			if (url === "about:blank") {
				domReady = new Promise<void>((resolve) => {
					onDOMReady = resolve;
					view.webContents.once("dom-ready", onDOMReady);
				});
			}
			const deadline = new Promise<never>((_resolve, reject) => {
				timeout = setTimeout(() => reject(new Error(`timed out loading ${url}`)), 15_000);
			});
			const pendingLoad = loadURL(url, loadOptions);
			void pendingLoad.catch(() => undefined);
			try {
				if (url !== "about:blank") {
					await Promise.race([pendingLoad, deadline]);
					return;
				}
				// Electron 33 can leave a redundant initial about:blank load promise
				// unresolved after the document itself is ready. This exception is only
				// for the initialization URL; real fixture navigations await loadURL so
				// their committed navigation event cannot leak into the action baseline.
				if (!domReady) throw new Error("about:blank readiness observer was not installed");
				await Promise.race([pendingLoad, domReady, deadline]);
			} finally {
				if (timeout) clearTimeout(timeout);
				if (onDOMReady) view.webContents.off("dom-ready", onDOMReady);
			}
		};
		return view;
	} as unknown as typeof WebContentsView;

	async function waitForFirstFrame(contents: WebContentsView["webContents"]): Promise<void> {
		const deadline = Date.now() + 10_000;
		for (;;) {
			try {
				if (!(await contents.capturePage()).isEmpty()) return;
			} catch (error) {
				if (Date.now() >= deadline) throw error;
			}
			if (Date.now() >= deadline) throw new Error("browser view did not produce a frame");
			await new Promise((resolve) => setTimeout(resolve, 50));
		}
	}

	function activeWebContents(): WebContentsView["webContents"] {
		if (!browserView) throw new Error("AO integration fixture has no active browser view");
		return browserView.webContents;
	}

	const runtime = {
		runAction: async (
			_sessionId: string,
			action: string,
			args: Record<string, unknown>,
		): Promise<Record<string, unknown>> => {
			switch (action) {
				case "tab-select":
					return {};
				case "open":
					await activeWebContents().loadURL(String(args.url));
					return {};
				case "snapshot":
					// A real provider snapshot cannot complete before Chromium has produced
					// a frame. Force the same readiness before dispatching native input.
					// Electron 44 rejects capturePage() with UnknownVizError until the
					// first frame exists (Electron 33 resolved an empty image), so retry
					// briefly until a non-empty frame is captured.
					await waitForFirstFrame(activeWebContents());
					return {
						snapshot: [
							'- button "Continue" [ref=e1]',
							'- button "Do nothing" [ref=e2]',
							'- button "Leave guarded page" [ref=e3]',
						].join("\n"),
						refs: {
							e1: { role: "button", name: "Continue" },
							e2: { role: "button", name: "Do nothing" },
							e3: { role: "button", name: "Leave guarded page" },
						},
					};
				case "click": {
					const element = args.ref === "e1" ? "continue" : args.ref === "e2" ? "no-effect" : "guarded";
					// Dispatch a real Chromium click through the DevTools input domain used by
					// browser automation runtimes. This gives the page the user activation that
					// beforeunload requires. Await the page's click event before returning so
					// the fixture synchronizes on browser evidence instead of renderer timing.
					const contents = activeWebContents();
					const clickObserved = contents.executeJavaScript(`
						new Promise((resolve) => {
							document.getElementById(${JSON.stringify(element)}).addEventListener(
								"click",
								() => resolve(true),
								{ once: true },
							);
						})
					`);
					const { x, y } = (await contents.executeJavaScript(`
						(() => {
							const rect = document.getElementById(${JSON.stringify(element)}).getBoundingClientRect();
							return { x: Math.round(rect.left + rect.width / 2), y: Math.round(rect.top + rect.height / 2) };
						})()
					`)) as { x: number; y: number };
					if (!contents.debugger.isAttached()) contents.debugger.attach("1.3");
					await contents.debugger.sendCommand("Input.dispatchMouseEvent", {
						type: "mouseMoved",
						x,
						y,
					});
					await contents.debugger.sendCommand("Input.dispatchMouseEvent", {
						type: "mousePressed",
						x,
						y,
						button: "left",
						clickCount: 1,
					});
					await contents.debugger.sendCommand("Input.dispatchMouseEvent", {
						type: "mouseReleased",
						x,
						y,
						button: "left",
						clickCount: 1,
					});
					let clickTimeout: ReturnType<typeof setTimeout> | undefined;
					try {
						await Promise.race([
							clickObserved,
							new Promise<never>((_resolve, reject) => {
								clickTimeout = setTimeout(
									() => reject(new Error(`Chromium did not dispatch a click to ${element}`)),
									5_000,
								);
							}),
						]);
					} finally {
						if (clickTimeout) clearTimeout(clickTimeout);
					}
					return { clicked: true };
				}
				default:
					throw new Error(`unexpected integration action: ${action}`);
			}
		},
		closeSession: async () => undefined,
		dispose: async () => undefined,
	} as unknown as AgentBrowserRuntime;

	const shellWebContents = {
		id: 1,
		getZoomFactor: () => 1,
		focus: () => undefined,
		send: () => undefined,
		on: () => undefined,
	} as never;
	const ipcHandlers = new Map<string, (...args: unknown[]) => unknown>();
	const ipcEvents = new Map<string, (...args: unknown[]) => unknown>();
	const hostOptions = {
		mainWindow: {
			contentView: window.contentView,
			getContentBounds: () => ({ x: 0, y: 0, width: 800, height: 600 }),
		} as never,
		shellWebContents,
		ipcMain: {
			handle: (channel: string, handler: (...args: unknown[]) => unknown) => ipcHandlers.set(channel, handler),
			on: (channel: string, handler: (...args: unknown[]) => unknown) => ipcEvents.set(channel, handler),
			removeHandler: (channel: string) => ipcHandlers.delete(channel),
			off: (channel: string) => ipcEvents.delete(channel),
		} as never,
		shell: { openExternal: async () => undefined },
		WebContentsView: IntegrationWebContentsView,
		annotatePreloadPath: path.join(path.dirname(fixturePath), "empty-preload.cjs"),
		rendererOrigin: "http://127.0.0.1:1",
		agentBrowserRuntime: runtime,
	};
	const host = createBrowserViewHost(hostOptions);
	let nativeRuntime: AgentBrowserRuntime | undefined;
	let nativeHost: ReturnType<typeof createBrowserViewHost> | undefined;

	try {
		// Exercise Chromium's actual CSS cascade: a mock of insertCSS cannot
		// prove that user-origin rules defeat author !important and inline styles.
		const dragURL = `http://127.0.0.1:${address.port}/drag-regions`;
		for (const zoom of [1, 2]) {
			await host.execute("integration-session", "open", { url: dragURL });
			const contents = activeWebContents();
			contents.setZoomFactor(zoom);
			await contents.executeJavaScript(`
				const dynamic = document.createElement('div');
				dynamic.id = 'dynamic';
				dynamic.style.setProperty('app-region', 'drag', 'important');
				document.body.append(dynamic);
			`);
			const deadline = Date.now() + 5_000;
			for (;;) {
				const regions = await contents.executeJavaScript(`
					[
						...[document.documentElement, document.body,
							document.querySelector('#page'), document.querySelector('#button'),
							document.querySelector('#dynamic')]
							.map(element => getComputedStyle(element).getPropertyValue('app-region')),
						getComputedStyle(document.querySelector('#page'), '::before').getPropertyValue('app-region'),
						getComputedStyle(document.querySelector('#page'), '::after').getPropertyValue('app-region'),
					]
				`) as string[];
				if (regions.every(region => region === "no-drag") || Date.now() >= deadline) {
					assert.deepEqual(regions, Array(7).fill("no-drag"),
						`page drag regions must be disabled at zoom ${zoom}, including inline, dynamic and pseudo-element rules`);
					break;
				}
				await new Promise(resolve => setTimeout(resolve, 25));
			}
		}
		activeWebContents().setZoomFactor(1);
		process.stdout.write("browser native CSS drag-region override integration passed\n");

		await host.execute("integration-session", "open", { url: fixtureURL });
		const satisfied = (await host.execute("integration-session", "act", {
			instruction: "continue",
			postcondition: { kind: "navigation", timeoutMs: 1_000 },
		})) as Record<string, unknown>;
		assert.deepEqual(satisfied.postcondition, {
			status: "satisfied",
			kind: "navigation",
		});

		await host.execute("integration-session", "open", { url: fixtureURL });
		const unmet = (await host.execute("integration-session", "act", {
			instruction: "do nothing",
			postcondition: { kind: "navigation", timeoutMs: 250 },
		})) as Record<string, unknown>;
		assert.deepEqual(unmet.postcondition, {
			status: "unmet",
			kind: "navigation",
			reason: "timeout",
		});

		await host.execute("integration-session", "open", { url: fixtureURL });
		const cancelled = (await host.execute("integration-session", "act", {
			instruction: "leave guarded page",
			postcondition: { kind: "navigation", timeoutMs: 1_000 },
		})) as Record<string, unknown>;
		assert.deepEqual(cancelled.postcondition, {
			status: "cancelled",
			kind: "navigation",
			reason: "beforeunload",
		});
		assert.equal((cancelled.after as { url: string }).url, fixtureURL);
		await host.execute("integration-session", "snapshot", {
			interactive: true,
		});

		// Exercise the production host with real Chromium window handles. Unit
		// fakes cannot prove that the child retains its native opener linkage.
		const opener = activeWebContents();
		await opener.executeJavaScript("guardEnabled = false");
		await opener.loadURL(`http://127.0.0.1:${address.port}/destination`);
		const popupURL = `http://localhost:${address.port}/popup`;
		async function deadline<T>(pending: Promise<T>): Promise<T> {
			let timer: ReturnType<typeof setTimeout> | undefined;
			try {
				return await Promise.race([
					pending,
					new Promise<never>((_resolve, reject) => {
						timer = setTimeout(() => reject(new Error("popup integration timed out")), 10_000);
					}),
				]);
			} finally {
				if (timer) clearTimeout(timer);
			}
		}
		for (const bootstrapBlank of [false, true]) {
			const hasHandle = await opener.executeJavaScript(`
				window.popupReady = new Promise(resolve => addEventListener('message', event => {
					if (event.data?.kind === 'popup-ready') resolve(event.data);
				}));
				window.authPopup = window.open(${JSON.stringify(bootstrapBlank ? "about:blank" : popupURL)}, '_blank');
				${bootstrapBlank ? `window.authPopup.location = ${JSON.stringify(popupURL)};` : ""}
				!!window.authPopup;
			`, true);
			assert.equal(hasHandle, true, "window.open must return a real WindowProxy");
			assert.deepEqual(await deadline(opener.executeJavaScript("window.popupReady")), {
				kind: "popup-ready",
				hasOpener: true,
			});
			const popup = activeWebContents();
			assert.notEqual(popup, opener);
			assert.equal(popup.session, opener.session, "popup must share its opener's browser profile");
			assert.deepEqual(await popup.executeJavaScript("({require: typeof require, process: typeof process})"),
				{require: "undefined", process: "undefined"});
			const tabs = await host.execute("integration-session", "tabs") as BrowserTabsState;
			assert.equal(tabs.tabs.length, 2);
			const closed = new Promise<void>(resolve => popup.once("destroyed", () => resolve()));
			await opener.executeJavaScript(`
				window.authComplete = new Promise(resolve => addEventListener('message', event => {
					if (event.data?.kind === 'auth-complete') resolve(true);
				}));
				window.authPopup.postMessage('complete', '*');
				undefined;
			`);
			assert.equal(await deadline(opener.executeJavaScript("window.authComplete")), true);
			await deadline(closed);
			// Main can observe destruction before the opener updates its WindowProxy.
			assert.equal(await deadline(opener.executeJavaScript(`
				new Promise(resolve => {
					const checkClosed = () => {
						if (window.authPopup.closed) resolve(true);
						else setTimeout(checkClosed, 25);
					};
					checkClosed();
				})
			`)), true);
			const remaining = await host.execute("integration-session", "tabs") as BrowserTabsState;
			assert.equal(remaining.tabs.length, 1, "self-closed guest must leave AO's tab registry");
			assert.equal(remaining.activeTabId, remaining.tabs[0].id);
		}
		assert.equal(await opener.executeJavaScript("window.open('file:///etc/passwd') === null", true), true);
		assert.equal((await host.execute("integration-session", "tabs") as BrowserTabsState).tabs.length, 1);
		process.stdout.write("browser popup handle, cross-origin messaging, shared browser profile and self-close integration passed\n");

		host.destroyAll();
		const nativeRuntimeDataDir = path.join(userDataPath, "native-runtime");
		nativeRuntime = new AgentBrowserRuntime({
			binaryPath: path.resolve("agent-browser", process.platform === "win32" ? "agent-browser.exe" : "agent-browser"),
			dataDir: nativeRuntimeDataDir,
		});
		nativeHost = createBrowserViewHost({ ...hostOptions, agentBrowserRuntime: nativeRuntime });
		const liveHost = nativeHost;
		const shownSessions = new Set<string>();
		async function nativeCommand(sessionId: string, action: string, args: Record<string, unknown> = {}) {
			const initialOpen = action === "open" && !shownSessions.has(sessionId);
			if (initialOpen) forceInitialPresentation = true;
			try {
				const result = await liveHost.execute(sessionId, action, args);
				if (initialOpen) {
					const ensure = ipcHandlers.get("browser:ensure");
					const setBounds = ipcEvents.get("browser:setBounds");
					assert.ok(ensure && setBounds);
					const { viewId } = await ensure({ sender: shellWebContents }, sessionId) as { viewId: string };
					forceInitialPresentation = false;
					setBounds({ sender: shellWebContents }, {
						viewId, revision: 1, rect: { x: 0, y: 0, width: 800, height: 600 }, visible: true,
					});
					shownSessions.add(sessionId);
					await waitForFirstFrame(activeWebContents());
				}
				return result;
			} finally {
				if (initialOpen) forceInitialPresentation = false;
			}
		}
		async function nativeSnapshot(sessionId: string, expected: string, tabId: string) {
			const result = await nativeCommand(sessionId, "snapshot", { interactive: true }) as {
				text: string; target: { tabId: string };
			};
			assert.equal(result.target.tabId, tabId);
			assert.ok(result.text.includes(expected), `Expected ${expected} in ${tabId}: ${result.text}`);
			return result.text;
		}
		function refFor(snapshot: string, name: string): string {
			const line = snapshot.split("\n").find(line => line.includes(`"${name}"`));
			const ref = line?.match(/\[ref=(e\d+)\]/)?.[1];
			assert.ok(ref, `Missing ${name} in snapshot: ${snapshot}`);
			return ref;
		}
		for (const name of ["Open report link", "Open report window"]) {
			for (let attempt = 0; attempt < 3; attempt++) {
				const sessionId = `native-popup-${name.endsWith("link") ? "link" : "window"}-${attempt}`;
				await nativeCommand(sessionId, "open", { url: `http://127.0.0.1:${address.port}/native-popup-opener` });
				const openerSnapshot = await nativeSnapshot(sessionId, name, "t1");
				window.show();
				window.focus();
				await waitForFirstFrame(activeWebContents());
				await nativeCommand(sessionId, "click", { ref: refFor(openerSnapshot, name) });
				try {
					await deadline((async () => {
						for (;;) {
							const state = await nativeCommand(sessionId, "tabs") as BrowserTabsState;
							if (state.activeTabId === "t2" && state.tabs.length === 2) return;
							await new Promise(resolve => setTimeout(resolve, 10));
						}
					})());
				} catch (error) {
					throw new Error(`${name} attempt ${attempt + 1}: ${String(error)}; last created view URL=${activeWebContents().getURL()}; tabs=${JSON.stringify(await nativeCommand(sessionId, "tabs"))}`);
				}
				const childSnapshot = await nativeSnapshot(sessionId, "Change child", "t2");
				assert.equal((await nativeCommand(sessionId, "get", { property: "title" }) as { value: string }).value, "Native popup child");
				await nativeCommand(sessionId, "click", { ref: refFor(childSnapshot, "Change child") });
				await nativeCommand(sessionId, "wait", { text: "Child changed", timeoutMs: 2_500 });
				await nativeSnapshot(sessionId, "Child changed", "t2");
				await nativeCommand(sessionId, "tab-select", { tabId: "t1" });
				await nativeSnapshot(sessionId, "Open report link", "t1");
				assert.equal((await nativeCommand(sessionId, "get", { property: "title" }) as { value: string }).value, "Native popup opener");
				await nativeCommand(sessionId, "tab-select", { tabId: "t2" });
				await nativeSnapshot(sessionId, "Child changed", "t2");
				await nativeCommand(sessionId, "tab-close", { tabId: "t2" });
				await nativeSnapshot(sessionId, "Open report link", "t1");
				await nativeCommand(sessionId, "__destroy-session");
				process.stdout.write(`browser native popup ${name.endsWith("link") ? "link" : "window"} attempt ${attempt + 1} passed\n`);
			}
		}
		async function waitForTabs(sessionId: string, expectedIds: string[], activeTabId?: string) {
			return deadline((async () => {
				for (;;) {
					const state = await nativeCommand(sessionId, "tabs") as BrowserTabsState;
					if (state.tabs.map(tab => tab.id).join(",") === expectedIds.join(",") &&
						(activeTabId === undefined || state.activeTabId === activeTabId)) return state;
					await new Promise(resolve => setTimeout(resolve, 10));
				}
			})());
		}
		async function stopNativeDaemon(sessionId: string): Promise<void> {
			const prefix = `ao-${createHash("sha256").update(sessionId).digest("hex").slice(0, 4)}-`;
			const pidFiles: string[] = [];
			for (const root of await readdir(nativeRuntimeDataDir)) {
				if (!root.startsWith("r-")) continue;
				const namespaces = path.join(nativeRuntimeDataDir, root, "s", "namespaces");
				for (const namespace of await readdir(namespaces)) {
					if (namespace.startsWith(prefix)) pidFiles.push(path.join(namespaces, namespace, "run", `${namespace}.pid`));
				}
			}
			assert.equal(pidFiles.length, 1, "restart must identify exactly this fixture's native daemon");
			const pid = Number((await readFile(pidFiles[0], "utf8")).trim());
			assert.ok(Number.isSafeInteger(pid) && pid > 0 && pid !== process.pid);
			process.kill(pid, "SIGKILL");
			await deadline((async () => {
				for (;;) {
					try { process.kill(pid, 0); }
					catch (error) {
						if ((error as NodeJS.ErrnoException).code === "ESRCH") return;
						throw error;
					}
					await new Promise(resolve => setTimeout(resolve, 10));
				}
			})());
		}
		for (const restart of [false, true]) {
			const sessionId = `native-tab-gap-${restart ? "restart" : "selection"}`;
			process.stdout.write(`browser native tab gap ${restart ? "restart" : "queued selection"} scenario starting\n`);
			await nativeCommand(sessionId, "open", { url: `http://127.0.0.1:${address.port}/native-popup-opener` });
			const gapOpener = activeWebContents();
			async function openGapPopup(name: "Third" | "Fourth" | "Transient", ids: string[]) {
				await gapOpener.executeJavaScript(`window.open(${JSON.stringify(new URL(`/native-tab?name=${name}`, fixtureURL).href)}, '_blank'); undefined`, true);
				await waitForTabs(sessionId, ids, ids[ids.length - 1]);
				const contents = activeWebContents();
				await deadline((async () => {
					while (await contents.executeJavaScript("document.title") !== `${name} tab`)
						await new Promise(resolve => setTimeout(resolve, 10));
				})());
				return contents;
			}
			// Popup activation also queues a native synchronization. Hold that
			// boundary until t2 is gone so the daemon cannot discover it early.
			const originalRunAction = nativeRuntime.runAction.bind(nativeRuntime);
			let backgroundEntered: (() => void) | undefined;
			let releaseBackground: (() => void) | undefined;
			const backgroundStarted = new Promise<void>(resolve => { backgroundEntered = resolve; });
			const backgroundHeld = new Promise<void>(resolve => { releaseBackground = resolve; });
			let targetProvider: Parameters<AgentBrowserRuntime["runAction"]>[3] | undefined;
			nativeRuntime.runAction = async (...args: Parameters<AgentBrowserRuntime["runAction"]>) => {
				if (args[0] === sessionId) {
					targetProvider = args[3];
					backgroundEntered?.();
					await backgroundHeld;
				}
				return originalRunAction(...args);
			};
			let third: WebContentsView["webContents"];
			let fourth: WebContentsView["webContents"];
			try {
				const transient = await openGapPopup("Transient", ["t1", "t2"]);
				await deadline(backgroundStarted);
				const transientClosed = new Promise<void>(resolve => transient.once("destroyed", () => resolve()));
				void transient.executeJavaScript("window.close()").catch(() => undefined);
				await deadline(transientClosed);
				await waitForTabs(sessionId, ["t1"], "t1");
				third = await openGapPopup("Third", ["t1", "t3"]);
				fourth = await openGapPopup("Fourth", ["t1", "t3", "t4"]);
			} finally {
				releaseBackground?.();
				nativeRuntime.runAction = originalRunAction;
			}
			await nativeCommand(sessionId, "tab-select", { tabId: "t3" });
			assert.equal(browserViews.get(third.id)?.getBounds().x, 0, "AO t3 must occupy the visible panel");
			assert.ok((browserViews.get(fourth.id)?.getBounds().x ?? 0) < 0, "inactive AO t4 must be parked outside the panel");
			assert.ok(targetProvider, "background synchronization must expose this session's real target provider");
			const nativeTabs = (await nativeRuntime.runAction(sessionId, "tabs", {}, targetProvider)).tabs as Array<{ tabId: string; targetId: string }>;
			assert.equal(nativeTabs.find(tab => tab.targetId === "t3")?.tabId, "t2", "fixture must prove AO t3 differs from native t2");
			assert.equal(nativeTabs.find(tab => tab.targetId === "t4")?.tabId, "t3", "fixture must prove native t3 means AO t4");
			let snapshot = await nativeSnapshot(sessionId, "Change Third", "t3");
			if (restart) {
				await stopNativeDaemon(sessionId);
				// Read before explicit selection: the host still caches t3 while
				// a freshly connected native daemon may default to opener t1.
				try {
					const result = await nativeCommand(sessionId, "get", { property: "title" }) as { value: string; target: { tabId: string } };
					assert.equal(result.target.tabId, "t3");
					assert.equal(result.value, "Third tab", "restart must not read the opener under the cached child identity");
				} catch (error) {
					assert.equal((error as { code?: string }).code, "BROWSER_TARGET_MISMATCH", "a restart may fail closed, but must not return another page");
				}
				await nativeCommand(sessionId, "tab-select", { tabId: "t3" });
				snapshot = await nativeSnapshot(sessionId, "Change Third", "t3");
			}
			assert.equal(browserViews.get(third.id)?.getVisible(), true, "selected AO t3 must be drawn");
			assert.equal(browserViews.get(fourth.id)?.getVisible(), false, "inactive AO t4 must not cover the selected page");
			await nativeCommand(sessionId, "click", { ref: refFor(snapshot, "Change Third") });
			try {
				await nativeCommand(sessionId, "wait", { text: "Third changed", timeoutMs: 2_500 });
				await nativeSnapshot(sessionId, "Third changed", "t3");
			} catch (error) {
				const pages = await Promise.all([third, fourth].map(async (contents) => ({
					state: await contents.executeJavaScript("({button: document.querySelector('button').textContent, focused: document.hasFocus()})"),
					visible: browserViews.get(contents.id)?.getVisible(),
				})));
				throw new Error(`${String(error)}; pages=${JSON.stringify(pages)}; windowFocused=${window.isFocused()}; tabs=${JSON.stringify(await nativeCommand(sessionId, "tabs"))}`);
			}
			assert.equal(await third.executeJavaScript("document.querySelector('button').textContent"), "Third changed");
			assert.equal(await fourth.executeJavaScript("document.querySelector('button').textContent"), "Change Fourth");
			assert.equal(await gapOpener.executeJavaScript("document.title"), "Native popup opener");
			if (!restart) {
				const state = await nativeCommand(sessionId, "tabs") as BrowserTabsState;
				const ensure = ipcHandlers.get("browser:ensure");
				const select = ipcHandlers.get("browser:selectTab");
				assert.ok(ensure && select);
				await ensure({ sender: shellWebContents }, sessionId);
				const runAction = nativeRuntime.runAction.bind(nativeRuntime);
				let entered: (() => void) | undefined;
				let release: (() => void) | undefined;
				const started = new Promise<void>(resolve => { entered = resolve; });
				const held = new Promise<void>(resolve => { release = resolve; });
				nativeRuntime.runAction = async (...args: Parameters<AgentBrowserRuntime["runAction"]>) => {
					if (args[0] === sessionId && args[1] === "tab-select" && args[2].tabId === "t4") {
						entered?.();
						await held;
					}
					return runAction(...args);
				};
				const first = nativeCommand(sessionId, "tab-select", { tabId: "t4" });
				try {
					await deadline(started);
					const second = nativeCommand(sessionId, "tab-select", { tabId: "t1" });
					// Let the admitted command enqueue behind the held native operation
					// before the independent renderer selects its final page.
					await new Promise<void>(resolve => setImmediate(resolve));
					const human = Promise.resolve(select({ sender: shellWebContents }, { viewId: state.viewId, tabId: "t3" }));
					release?.();
					await Promise.all([first, second, human]);
				} finally {
					release?.();
					nativeRuntime.runAction = runAction;
				}
				await waitForTabs(sessionId, ["t1", "t3", "t4"], "t3");
				await nativeSnapshot(sessionId, "Third changed", "t3");
			}
			const thirdClosed = new Promise<void>(resolve => third.once("destroyed", () => resolve()));
			await nativeCommand(sessionId, "tab-close", { tabId: "t3" });
			await deadline(thirdClosed);
			assert.equal(third.isDestroyed(), true, "close must remove exact AO t3 despite native numbering drift");
			assert.equal(fourth.isDestroyed(), false, "closing AO t3 must leave native t3 / AO t4 intact");
			await waitForTabs(sessionId, ["t1", "t4"]);
			await nativeCommand(sessionId, "tab-select", { tabId: "t4" });
			await nativeSnapshot(sessionId, "Change Fourth", "t4");
			await nativeCommand(sessionId, "tab-select", { tabId: "t1" });
			await nativeSnapshot(sessionId, "Open report link", "t1");
			await nativeCommand(sessionId, "__destroy-session");
			process.stdout.write(`browser native tab numbering gap, exact close and ${restart ? "daemon restart" : "queued renderer selection"} integration passed\n`);
		}

		process.stdout.write("browser action postcondition integration passed\n");
	} finally {
		nativeHost?.destroyAll();
		await nativeRuntime?.dispose();
		// Tear down native views synchronously before exiting. Electron can leave a
		// graceful app-level dispose pending after a beforeunload interaction even
		// though all assertions completed; the standalone process must not turn that
		// shutdown delay into a false test timeout.
		host.destroyAll();
		void host.dispose().catch(() => undefined);
		window.destroy();
		server.close();
		server.closeAllConnections();
	}
}

function reportIntegrationResult(code: number): void {
	// The outer runner owns process-tree termination. Keeping the Electron main
	// process alive until it consumes this marker lets it terminate Chromium's
	// renderer children too, even after the fixture exercised beforeunload.
	process.stdout.write(`AO_BROWSER_POSTCONDITION_RESULT:${code}\n`);
}

void run().then(
	() => reportIntegrationResult(0),
	(error) => {
		console.error(error);
		reportIntegrationResult(1);
	},
);

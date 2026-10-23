import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { createServer } from "node:http";
import path from "node:path";
import { app, BrowserWindow, WebContentsView } from "electron";
import type { ChromeApi } from "../browser-extension/chrome-api";
import { TrustedInput } from "../browser-extension/trusted-input";
import type { AgentBrowserRuntime } from "./agent-browser-runtime";
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

// Exercise the extension's actual input loop against Chromium, rather than a
// fake that cannot observe click default actions such as form submission.
async function testCancelledTrustedClick(): Promise<void> {
	const window = new BrowserWindow({ show: false, width: 500, height: 400 });
	try {
		await window.loadURL(`data:text/html,${encodeURIComponent(`<!doctype html>
			<style>html,body{margin:0}button{width:100vw;height:100vh}</style>
			<form><button>Submit</button></form><script>
			window.events = [];
			for (const type of ["mousedown","mouseup","click","submit"]) {
				document.addEventListener(type, event => {
					if (event.target.closest("form")) window.events.push(type);
					if (type === "submit") event.preventDefault();
				});
			}
		</script>`)}`);
		const debuggerApi = window.webContents.debugger;
		let controller = new AbortController();
		let cancelOnPress = false;
		const chrome = {
			scripting: {
				executeScript: async ({ func, args }: { func: (action: "track" | "release") => boolean | "idle"; args: ["track" | "release"] }) => [{
					result: await window.webContents.executeJavaScriptInIsolatedWorld(999, [{ code: `(${func.toString()})(${JSON.stringify(args[0])})` }]),
				}],
			},
			debugger: {
				attach: async () => debuggerApi.attach("1.3"),
				detach: async () => debuggerApi.detach(),
				onEvent: { addListener() {}, removeListener() {} },
				onDetach: { addListener() {}, removeListener() {} },
				sendCommand: async (_target: unknown, method: string, params: Record<string, unknown>) => {
					const result = await debuggerApi.sendCommand(method, params);
					if (method === "Input.dispatchMouseEvent" && params.type === "mousePressed" && cancelOnPress) controller.abort();
					return result;
				},
			},
		} as unknown as ChromeApi;
		const input = new TrustedInput(chrome);
		// Sanity check: normal trusted input activates the full-viewport button.
		await input.click(1, { x: 50, y: 50 });
		assert.deepEqual(await window.webContents.executeJavaScript("window.events"), ["mousedown", "mouseup", "click", "submit"]);
		await window.webContents.executeJavaScript("window.events = []");
		cancelOnPress = true;
		await assert.rejects(input.click(1, { x: 50, y: 50 }, { signal: controller.signal }),
			(error: unknown) => (error as { code?: string }).code === "BROWSER_COMMAND_CANCELED");
		assert.deepEqual(await window.webContents.executeJavaScript("window.events"), ["mousedown"]);
		// Pointer capture routes releases back to the button even outside its box.
		await window.webContents.executeJavaScript(`
			document.querySelector("button").addEventListener("pointerdown", event => {
				event.target.setPointerCapture(event.pointerId);
			});
			window.events = [];
		`);
		controller = new AbortController();
		cancelOnPress = false;
		await input.click(1, { x: 50, y: 50 });
		assert.deepEqual(await window.webContents.executeJavaScript("window.events"), ["mousedown", "mouseup", "click", "submit"]);
		await window.webContents.executeJavaScript("window.events = []");
		cancelOnPress = true;
		await assert.rejects(input.click(1, { x: 50, y: 50 }, { signal: controller.signal }),
			(error: unknown) => (error as { code?: string }).code === "BROWSER_COMMAND_CANCELED");
		const capturedEvents = await window.webContents.executeJavaScript("window.events") as string[];
		assert.equal(capturedEvents.includes("click"), false, "cancelled captured pointer must not click");
		assert.equal(capturedEvents.includes("submit"), false, "cancelled captured pointer must not submit");
		// Cleanup must not leave the browser's mouse button stuck down.
		controller = new AbortController();
		cancelOnPress = false;
		await window.webContents.executeJavaScript("window.events = []");
		await input.click(1, { x: 50, y: 50 });
		assert.deepEqual(await window.webContents.executeJavaScript("window.events"), ["mousedown", "mouseup", "click", "submit"]);
		process.stdout.write("cancelled trusted click does not activate its target in Chromium\n");
	} finally {
		window.destroy();
	}
}

async function run(): Promise<void> {
	await app.whenReady();
	const fixture = await readFile(fixturePath, "utf8");
	const dragFixture = await readFile(path.join(path.dirname(fixturePath), "drag-regions.html"), "utf8");
	const server = createServer((request, response) => {
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
	await testCancelledTrustedClick();
	// Initialize the owning window before attaching a WebContentsView. On
	// Windows and Xvfb, a never-navigated BrowserWindow can leave the child
	// renderer unscheduled even though the native view is marked visible.
	await window.loadURL("data:text/html,<title>AO browser integration host</title>");
	window.show();
	let browserView: WebContentsView | undefined;
	const IntegrationWebContentsView = function (options: ConstructorParameters<typeof WebContentsView>[0]) {
		const view = new WebContentsView(options);
		browserView = view;
		// AO normally hides a new view off-screen until the renderer asks to show
		// the panel. This standalone fixture has no renderer, so keep a real surface
		// attached; Chromium otherwise may defer initialization indefinitely on
		// Windows and under Xvfb.
		const setBounds = view.setBounds.bind(view);
		view.setBounds = () => setBounds({ x: 0, y: 0, width: 800, height: 600 });
		const setVisible = view.setVisible.bind(view);
		view.setVisible = () => setVisible(true);
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
		focus: () => undefined,
		send: () => undefined,
		on: () => undefined,
	} as never;
	const host = createBrowserViewHost({
		mainWindow: {
			contentView: window.contentView,
			getContentBounds: () => ({ x: 0, y: 0, width: 800, height: 600 }),
		} as never,
		shellWebContents,
		ipcMain: {
			handle: () => undefined,
			on: () => undefined,
			removeHandler: () => undefined,
			off: () => undefined,
		} as never,
		shell: { openExternal: async () => undefined },
		WebContentsView: IntegrationWebContentsView,
		annotatePreloadPath: path.join(path.dirname(fixturePath), "empty-preload.cjs"),
		rendererOrigin: "http://127.0.0.1:1",
		agentBrowserRuntime: runtime,
	});

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
						"page drag regions must be disabled, including inline, dynamic and pseudo-element rules");
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

		process.stdout.write("browser action postcondition integration passed\n");
	} finally {
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

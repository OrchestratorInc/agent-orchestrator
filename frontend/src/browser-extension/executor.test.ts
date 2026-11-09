import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ChromeApi, Tab } from "./chrome-api";
import { ConsentManager } from "./consent";
import { handleContentRequest } from "./content";
import { Executor, POLICY_KEY } from "./executor";
import { NetworkCapture } from "./network";
import { DEFAULT_SITE_POLICY } from "./policy";
import { BridgeError } from "./protocol";
import { SessionTabRegistry } from "./sessions";
import { RefRegistry } from "./snapshot";
import { TrustedInput } from "./trusted-input";
import { Bridge } from "./background";

type Callback = (...args: never[]) => unknown;

function event() {
	const listeners = new Set<Callback>();
	return {
		addListener: (callback: Callback) => listeners.add(callback),
		removeListener: (callback: Callback) => listeners.delete(callback),
		fire: (...args: unknown[]) => [...listeners].map((listener) => (listener as (...a: unknown[]) => unknown)(...args)),
	};
}

function storage() {
	const data: Record<string, unknown> = {};
	return {
		data,
		get: async (keys: string | string[]) => Object.fromEntries((Array.isArray(keys) ? keys : [keys]).filter((key) => key in data).map((key) => [key, structuredClone(data[key])])),
		set: async (items: Record<string, unknown>) => void Object.assign(data, structuredClone(items)),
		remove: async (keys: string | string[]) => void (Array.isArray(keys) ? keys : [keys]).forEach((key) => delete data[key]),
	};
}

// A 1x1 PNG data URL, as chrome.tabs.captureVisibleTab returns.
const CAPTURE_PNG = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==";

// Pages served to the fake tab by URL.
const PAGES: Record<string, string> = {
	"https://shop.test/": `<h1>Shop</h1><label for="q">Search</label><input id="q"><button id="buy">Buy now</button><a href="/help">Help</a>`,
	"https://shop.test/done": `<h1>Thanks</h1>`,
};

class FakeChrome {
	nextTab = 100;
	nextGroup = 1;
	nextWindow = 10;
	tabs = new Map<number, Tab>();
	groups = new Map<number, { id: number; title?: string; color?: string; windowId: number }>();
	injected = new Set<number>();
	registries = new Map<number, RefRegistry>();
	debuggerCalls: { method: string; params?: Record<string, unknown> }[] = [];
	attached = 0;
	windowsCreated: string[] = [];
	holdLoad = false;
	/** When set, tabs.update leaves the commit to the test. */
	deferCommit?: () => void;
	holdCommit = false;
	windowsRemoved: number[] = [];
	windowStates = new Map<number, string>();
	windowUpdates: Array<{ windowId: number; state?: string; focused?: boolean }> = [];
	granted = { origins: true, debugger: true };
	events = {
		tabsRemoved: event(),
		tabsUpdated: event(),
		tabsActivated: event(),
		groupsRemoved: event(),
		windowsRemoved: event(),
		runtimeMessage: event(),
		navCommitted: event(),
		navFragment: event(),
		navDomContentLoaded: event(),
		navCompleted: event(),
		navError: event(),
		debuggerEvent: event(),
		debuggerDetach: event(),
		requestStarted: event(),
		requestCompleted: event(),
	};
	local = storage();
	session = storage();
	frames = [{ frameId: 0, parentFrameId: -1, url: "" }];
	/** Which tab was in front of its window at each capture. */
	captures: number[] = [];
	/** Runs during a capture, for example a user switching tabs. */
	duringCapture?: () => void;

	activate(tabId: number) {
		const tab = this.tabs.get(tabId)!;
		for (const other of this.tabs.values()) if (other.windowId === tab.windowId) other.active = other.id === tabId;
		this.events.tabsActivated.fire({ tabId, windowId: tab.windowId });
	}
	/** Answers content requests for specific frames (tabId, frameId). */
	frameResponder?: (frameId: number, message: Record<string, unknown>) => unknown;

	load(tabId: number, url: string) {
		if (this.holdCommit) {
			this.holdCommit = false;
			this.deferCommit = () => this.load(tabId, url);
			return;
		}
		const tab = this.tabs.get(tabId)!;
		tab.url = url;
		tab.title = url === "about:blank" ? "" : (PAGES[url]?.match(/<h1>(.*?)<\/h1>/)?.[1] ?? url);
		tab.status = "complete";
		document.body.innerHTML = PAGES[url] ?? "";
		this.injected.delete(tabId);
		this.registries.set(tabId, new RefRegistry());
		this.events.navCommitted.fire({ tabId, frameId: 0, url });
		this.events.navDomContentLoaded.fire({ tabId, frameId: 0, url });
		if (!this.holdLoad) setTimeout(() => this.events.navCompleted.fire({ tabId, frameId: 0, url }), 0);
	}

	api(): ChromeApi {
		const self = this;
		return {
			runtime: {
				id: "ext",
				connectNative: () => {
					throw new Error("unused");
				},
				getManifest: () => ({ version: "0.1.0" }),
				getURL: (path: string) => `chrome-extension://ext/${path}`,
				sendMessage: async () => undefined,
				onMessage: self.events.runtimeMessage,
				onStartup: event(),
				onInstalled: event(),
			},
			tabs: {
				create: async ({ url, windowId, active }) => {
					const id = self.nextTab++;
					const tab: Tab = { id, windowId: windowId ?? 1, groupId: -1, url: url ?? "about:blank", title: "", active: active ?? true, status: "complete" };
					self.tabs.set(id, tab);
					self.registries.set(id, new RefRegistry());
					return { ...tab };
				},
				update: async (tabId, props) => {
					const tab = self.tabs.get(tabId)!;
					if (props.active === true) self.activate(tabId);
					else if (props.active === false) tab.active = false;
					if (props.url) self.load(tabId, props.url);
					return { ...tab };
				},
				get: async (tabId) => {
					const tab = self.tabs.get(tabId);
					if (!tab) throw new Error(`No tab with id: ${tabId}`);
					return { ...tab };
				},
				remove: async (ids) => {
					for (const id of Array.isArray(ids) ? ids : [ids]) {
						self.tabs.delete(id);
						self.events.tabsRemoved.fire(id, { windowId: 1, isWindowClosing: false });
					}
				},
				query: async ({ windowId, active }) =>
					[...self.tabs.values()].filter((tab) => (windowId === undefined || tab.windowId === windowId) && (active === undefined || tab.active === active)),
				group: async ({ tabIds, groupId, createProperties }) => {
					const id = groupId ?? self.nextGroup++;
					if (!self.groups.has(id)) self.groups.set(id, { id, windowId: createProperties?.windowId ?? 1 });
					for (const tabId of Array.isArray(tabIds) ? tabIds : [tabIds]) {
						self.tabs.get(tabId)!.groupId = id;
						// Chrome reports the grouping as a tab update, possibly before
						// the caller has recorded the tab.
						self.events.tabsUpdated.fire(tabId, { groupId: id }, self.tabs.get(tabId));
					}
					return id;
				},
				ungroup: async (ids) => {
					for (const id of Array.isArray(ids) ? ids : [ids]) self.tabs.get(id)!.groupId = -1;
				},
				sendMessage: async (tabId, message, options) => {
					const answer = self.frameResponder?.(options?.frameId ?? 0, message as Record<string, unknown>);
					if (answer !== undefined) return { ok: true, value: answer };
					if (!self.injected.has(tabId)) throw new Error("Could not establish connection. Receiving end does not exist.");
					try {
						const value = await handleContentRequest(document, self.registries.get(tabId)!, message as never);
						return { ok: true, value };
					} catch (error) {
						const bridge = error as BridgeError;
						return { ok: false, error: { code: bridge.code ?? "X", message: bridge.message } };
					}
				},
				captureVisibleTab: async (windowId) => {
					await new Promise((resolve) => setTimeout(resolve, 5));
					self.duringCapture?.();
					const front = [...self.tabs.values()].find((tab) => tab.windowId === windowId && tab.active);
					self.captures.push(front?.id ?? -1);
					return CAPTURE_PNG;
				},
				goBack: async () => undefined,
				onRemoved: self.events.tabsRemoved,
				onUpdated: self.events.tabsUpdated,
				onActivated: self.events.tabsActivated,
			},
			tabGroups: {
				update: async (groupId, props) => Object.assign(self.groups.get(groupId)!, props) as never,
				get: async (groupId) => self.groups.get(groupId) as never,
				onRemoved: self.events.groupsRemoved,
			},
			windows: {
				create: async ({ url }) => {
					self.windowsCreated.push(url ?? "");
					return { id: self.nextWindow++, focused: true };
				},
				get: async (windowId) => ({ id: windowId, focused: false, state: self.windowStates.get(windowId) ?? "normal" }),
				getLastFocused: async () => ({ id: 1, focused: true }),
				update: async (windowId, props) => {
					self.windowUpdates.push({ windowId, ...props });
					if (props.state) self.windowStates.set(windowId, props.state);
					return { id: windowId, focused: false };
				},
				remove: async (windowId) => {
					self.windowsRemoved.push(windowId);
					for (const [id, tab] of self.tabs) if (tab.windowId === windowId) self.tabs.delete(id);
				},
				onRemoved: self.events.windowsRemoved,
			},
			scripting: {
				executeScript: async ({ target }) => {
					const url = self.tabs.get(target.tabId)?.url ?? "";
					if (url.startsWith("chrome://")) throw new Error("Cannot access a chrome:// URL");
					self.injected.add(target.tabId);
					return [];
				},
			},
			debugger: {
				attach: async () => void self.attached++,
				detach: async () => undefined,
				sendCommand: async (_target, method, params) => {
					self.debuggerCalls.push({ method, params });
					return {};
				},
				onEvent: self.events.debuggerEvent,
				onDetach: self.events.debuggerDetach,
			},
			permissions: {
				contains: async (request) => (request.permissions?.includes("debugger") ? self.granted.debugger : self.granted.origins),
				request: async () => true,
			},
			storage: { local: self.local, session: self.session },
			webNavigation: {
				getAllFrames: async () => self.frames,
				onCommitted: self.events.navCommitted,
				onReferenceFragmentUpdated: self.events.navFragment,
				onDOMContentLoaded: self.events.navDomContentLoaded,
				onCompleted: self.events.navCompleted,
				onErrorOccurred: self.events.navError,
			},
			webRequest: { onBeforeRequest: self.events.requestStarted, onCompleted: self.events.requestCompleted, onErrorOccurred: event() } as never,
			alarms: { create: async () => undefined, onAlarm: event() },
			action: { setBadgeText: async () => undefined, setBadgeBackgroundColor: async () => undefined, setTitle: async () => undefined },
		} as ChromeApi;
	}
}

function setup() {
	const fake = new FakeChrome();
	const chrome = fake.api();
	const sessions = new SessionTabRegistry(chrome);
	sessions.start();
	const consent = new ConsentManager(chrome);
	consent.start();
	const executor = new Executor({ chrome, sessions, consent, trusted: new TrustedInput(chrome), network: new NetworkCapture(chrome, sessions) });
	executor.start();
	const run = (action: string, args: Record<string, unknown> = {}, sessionId = "s1") =>
		executor.execute(sessionId, action, args, new AbortController().signal) as Promise<Record<string, unknown>>;
	const approve = async (sessionId = "s1", trusted = true) => {
		await consent.decide({ type: "ao-consent-decision", sessionId, allow: true, trusted });
	};
	return { fake, chrome, sessions, consent, executor, run, approve };
}

describe("Executor", () => {
	it("revoking approval stops a click waiting for geometry before trusted dispatch", async () => {
		const fake = new FakeChrome();
		const chrome = fake.api();
		const bridge = new Bridge(chrome);
		await bridge.consent.decide({ type: "ao-consent-decision", sessionId: "s1", allow: true, trusted: true });
		const runtime = bridge as unknown as {
			runCommand(message: unknown): Promise<void>;
			handleUi(message: unknown): Promise<unknown>;
			port: { postMessage: ReturnType<typeof vi.fn> };
		};
		runtime.port = { postMessage: vi.fn() };
		await runtime.runCommand({ requestId: "open", sessionId: "s1", action: "open", args: { url: "https://shop.test/" } });
		await runtime.runCommand({ requestId: "snapshot", sessionId: "s1", action: "snapshot", args: { interactive: true } });
		let release!: () => void;
		const gate = new Promise<void>((resolve) => { release = resolve; });
		const geometry = vi.fn();
		const original = chrome.tabs.sendMessage;
		vi.spyOn(chrome.tabs, "sendMessage").mockImplementation(async (tabId, message, options) => {
			const result = await original(tabId, message, options);
			if ((message as { op?: string }).op === "geometry") {
				geometry();
				await gate;
			}
			return result;
		});
		const pending = runtime.runCommand({ requestId: "click", sessionId: "s1", action: "click", args: { ref: "e1" } });
		await vi.waitFor(() => expect(geometry).toHaveBeenCalled());
		await runtime.handleUi({ type: "ao-ui-revoke", sessionId: "s1" });
		expect((await bridge.consent.approvals()).s1).toBeUndefined();
		release();
		await pending;
		expect(runtime.port.postMessage).toHaveBeenLastCalledWith(expect.objectContaining({ requestId: "click", ok: false, error: expect.objectContaining({ code: "BROWSER_COMMAND_CANCELED" }) }));
		expect(fake.debuggerCalls.filter((call) => call.method.startsWith("Input."))).toEqual([]);
	});
	it("stops capture immediately when the user reclaims a tab", async () => {
		const { fake, run, approve, sessions } = setup();
		await approve();
		await run("open", { url: "https://shop.test/" });
		await run("network-start", { durationSeconds: 30 });
		const tab = [...fake.tabs.values()][0]!;
		const request = { tabId: tab.id!, requestId: "one", url: "https://shop.test/public", method: "GET", type: "xmlhttprequest", timeStamp: 1, frameId: 0 };
		fake.events.requestStarted.fire(request);
		tab.groupId = -1;
		fake.events.tabsUpdated.fire(tab.id, { groupId: -1 }, tab);
		await vi.waitFor(() => expect(sessions.ownerOf(tab.id!)).toBeUndefined());
		fake.events.requestStarted.fire({ ...request, requestId: "two", url: "https://shop.test/private/records/123" });
		fake.events.requestCompleted.fire({ ...request, statusCode: 200, timeStamp: 2 });
		const result = await run("network-list");
		expect(result).toMatchObject({ active: false, stopReason: "ownership-lost", requestCount: 1 });
		expect(result.requests).toEqual([expect.objectContaining({ url: "https://shop.test/public" })]);
		expect((result.requests as Record<string, unknown>[])[0]).not.toHaveProperty("status");
	});

	it("does not navigate when cancellation happens during policy lookup", async () => {
		const { chrome, fake, executor, run, approve } = setup();
		await approve();
		await run("open", { url: "https://shop.test/" });
		const controller = new AbortController();
		const original = chrome.storage.local.get;
		let release!: () => void;
		const gate = new Promise<void>((resolve) => { release = resolve; });
		vi.spyOn(chrome.storage.local, "get").mockImplementation(async (keys) => {
			const result = await original(keys);
			if (keys === POLICY_KEY) await gate;
			return result;
		});
		const update = vi.spyOn(chrome.tabs, "update");
		const pending = executor.execute("s1", "open", { url: "https://shop.test/done" }, controller.signal);
		await vi.waitFor(() => expect(chrome.storage.local.get).toHaveBeenCalledWith(POLICY_KEY));
		controller.abort();
		release();
		await expect(pending).rejects.toMatchObject({ code: "BROWSER_COMMAND_CANCELED" });
		expect(update).not.toHaveBeenCalled();
		expect([...fake.tabs.values()][0]!.url).toBe("https://shop.test/");
	});

	it("selects a loaded srcdoc child with an empty element source", async () => {
		const { fake, run, approve } = setup();
		await approve();
		await run("open", { url: "https://shop.test/" });
		fake.frames = [{ frameId: 0, parentFrameId: -1, url: "https://shop.test/" }, { frameId: 1, parentFrameId: 0, url: "about:srcdoc" }];
		fake.frameResponder = (_frameId, message) => message.op === "frameOffset" ? { src: "", name: "Embedded form" } : undefined;
		await expect(run("frame", { target: "eFrame" })).resolves.toMatchObject({ frame: "eFrame", name: "Embedded form" });
	});
	beforeEach(() => {
		vi.spyOn(Element.prototype, "getBoundingClientRect").mockReturnValue({ left: 10, top: 20, width: 100, height: 30, right: 110, bottom: 50, x: 10, y: 20, toJSON: () => ({}) } as DOMRect);
	});
	afterEach(() => vi.restoreAllMocks());

	it("asks for consent before the first command and honours a denial", async () => {
		const { fake, consent, run } = setup();
		const pending = run("tabs");
		await vi.waitFor(() => expect(fake.windowsCreated).toHaveLength(1));
		expect(fake.windowsCreated[0]).toContain("consent.html?session=s1");
		await consent.decide({ type: "ao-consent-decision", sessionId: "s1", allow: false, trusted: false });
		await expect(pending).rejects.toMatchObject({ code: "BROWSER_CHROME_CONSENT_DENIED" });
		await expect(run("tabs")).rejects.toMatchObject({ code: "BROWSER_CHROME_CONSENT_DENIED" });
		expect(fake.windowsCreated).toHaveLength(1);
	});

	it("opens pages in the session's own AO tab group and snapshots them", async () => {
		const { fake, run, approve } = setup();
		await approve();
		const opened = await run("open", { url: "https://shop.test/" });
		expect(opened).toMatchObject({ url: "https://shop.test/", title: "Shop", isLoading: false, target: { tabId: "t1", origin: "https://shop.test" } });
		const [group] = [...fake.groups.values()];
		expect(group).toMatchObject({ title: "Orchestrator · s1" });
		const [tab] = [...fake.tabs.values()];
		expect(tab!.groupId).toBe(group!.id);
		expect(tab!.active).toBe(false);

		const snapshot = await run("snapshot", { interactive: true });
		expect(snapshot.text).toContain('- button "Buy now" [ref=');
		expect(snapshot).toMatchObject({ untrustedExternalContent: true, target: { tabId: "t1" } });

		const first = await run("snapshot", { interactive: true, delta: true });
		expect(first).toMatchObject({ kind: "full", revision: 1 });
		expect(await run("snapshot", { interactive: true, delta: true })).toMatchObject({ kind: "unchanged", revision: 2, baseRevision: 1 });
	});

	it("gives each session its own unfocused window instead of the user's", async () => {
		const { fake, run, approve } = setup();
		await approve("s1");
		await approve("s2");
		await run("open", { url: "https://shop.test/" }, "s1");
		await run("open", { url: "https://shop.test/done" }, "s2");
		const windowOf = (url: string) => [...fake.tabs.values()].find((tab) => tab.url === url)!.windowId;
		// Window 1 is the user's focused window in the fake.
		expect(windowOf("https://shop.test/")).not.toBe(1);
		expect(windowOf("https://shop.test/done")).not.toBe(1);
		expect(windowOf("https://shop.test/")).not.toBe(windowOf("https://shop.test/done"));
		// A second tab for a session joins that session's window.
		await run("tab-new", { url: "https://shop.test/more" }, "s1");
		expect(windowOf("https://shop.test/more")).toBe(windowOf("https://shop.test/"));
	});

	it("closes a finished session's window when everything in it was the session's", async () => {
		const { fake, run, approve } = setup();
		await approve("s1");
		await approve("s2");
		await run("open", { url: "https://shop.test/" }, "s1");
		await run("open", { url: "https://shop.test/done" }, "s2");
		await run("tab-new", { url: "https://shop.test/more" }, "s1");
		const windowOf = (url: string) => [...fake.tabs.values()].find((tab) => tab.url === url)!.windowId;
		const own = windowOf("https://shop.test/");
		const other = windowOf("https://shop.test/done");
		await run("__destroy-session", {}, "s1");
		expect(fake.windowsRemoved).toEqual([own]);
		expect(fake.windowsRemoved).not.toContain(other);
	});

	it("keeps the window and the pages when the user has their own tab in it", async () => {
		const { fake, run, approve } = setup();
		await approve();
		await run("open", { url: "https://shop.test/" });
		const page = [...fake.tabs.values()].find((tab) => tab.url === "https://shop.test/")!;
		const personal = await fake.api().tabs.create({ url: "https://personal.test/", active: true, windowId: page.windowId });
		await run("__destroy-session");
		expect(fake.windowsRemoved).toEqual([]);
		expect(fake.tabs.get(personal.id!)).toBeDefined();
		expect(fake.tabs.get(page.id!)?.groupId).toBe(-1);
	});

	it("keeps the window when the user handed the session one of their tabs", async () => {
		const { fake, run, approve, sessions } = setup();
		await approve();
		await run("open", { url: "https://shop.test/" });
		const page = [...fake.tabs.values()].find((tab) => tab.url === "https://shop.test/")!;
		const handed = await fake.api().tabs.create({ url: "https://personal.test/", active: false, windowId: page.windowId });
		const tab = fake.tabs.get(handed.id!)!;
		tab.groupId = page.groupId;
		fake.events.tabsUpdated.fire(handed.id, { groupId: page.groupId }, tab);
		await vi.waitFor(() => expect(sessions.ownerOf(handed.id!)).toBeDefined());
		await run("__destroy-session");
		expect(fake.windowsRemoved).toEqual([]);
		expect(fake.tabs.get(handed.id!)?.groupId).toBe(-1);
	});

	it("restores a minimized agent window for trusted input without focusing it", async () => {
		const { fake, run, approve } = setup();
		await approve();
		await run("open", { url: "https://shop.test/" });
		const sessionWindow = [...fake.tabs.values()].find((tab) => tab.url === "https://shop.test/")!.windowId;
		fake.windowStates.set(sessionWindow, "minimized");
		const { refs } = (await run("snapshot", { interactive: true })) as { refs: Record<string, { name: string }> };
		const buy = Object.entries(refs).find(([, info]) => info.name === "Buy now")![0];
		expect(await run("click", { ref: buy })).toMatchObject({ inputTier: "trusted" });
		expect(fake.windowUpdates).toEqual([{ windowId: sessionWindow, state: "normal" }]);
	});

	it("blocks denied sites and non-web pages", async () => {
		const { fake, run, approve } = setup();
		await approve();
		await expect(run("open", { url: "https://www.paypal.com/" })).rejects.toMatchObject({ code: "BROWSER_SITE_BLOCKED" });
		await expect(run("open", { url: "file:///etc/hosts" })).rejects.toMatchObject({ code: "BROWSER_URL_FORBIDDEN" });
		await run("open", { url: "https://shop.test/" });
		await fake.local.set({ [POLICY_KEY]: { ...DEFAULT_SITE_POLICY, deny: ["shop.test"] } });
		await expect(run("snapshot")).rejects.toMatchObject({ code: "BROWSER_SITE_BLOCKED" });
	});

	it("clicks with trusted input by default and synthetic input where the site requires", async () => {
		const { fake, run, approve } = setup();
		await approve();
		await run("open", { url: "https://shop.test/" });
		const { refs } = (await run("snapshot", { interactive: true })) as { refs: Record<string, { name: string }> };
		const buy = Object.entries(refs).find(([, info]) => info.name === "Buy now")![0];
		const clicked = await run("click", { ref: buy });
		expect(clicked).toMatchObject({ inputTier: "trusted" });
		const mouse = fake.debuggerCalls.filter((call) => call.method === "Input.dispatchMouseEvent");
		expect(mouse.map((call) => call.params?.type)).toEqual(["mouseMoved", "mousePressed", "mouseReleased"]);
		expect(mouse[1]!.params).toMatchObject({ x: 60, y: 35, button: "left" });
		expect(fake.debuggerCalls.some((call) => call.method.startsWith("Runtime."))).toBe(false);

		await fake.local.set({ [POLICY_KEY]: { ...DEFAULT_SITE_POLICY, syntheticInput: ["shop.test"] } });
		const attached = fake.attached;
		let synthetic = 0;
		document.getElementById("buy")!.addEventListener("click", () => synthetic++);
		expect(await run("click", { ref: buy })).toMatchObject({ inputTier: "synthetic" });
		expect(fake.attached).toBe(attached);
		expect(synthetic).toBe(1);
	});

	it("resolves act instructions and reports unmatched ones with the snapshot", async () => {
		const { run, approve } = setup();
		await approve("s1", false);
		await run("open", { url: "https://shop.test/" });
		const matched = await run("act", { instruction: "the buy now button" });
		expect(matched).toMatchObject({ outcome: "matched", candidate: { role: "button", name: "Buy now" }, inputDispatched: true, retried: false });
		const filled = await run("act", { instruction: "search", action: "fill", value: "socks" });
		expect(filled).toMatchObject({ outcome: "matched" });
		expect((document.getElementById("q") as HTMLInputElement).value).toBe("socks");
		const missing = await run("act", { instruction: "the checkout wizard" });
		expect(missing).toMatchObject({ outcome: "no-match" });
		expect(String(missing.snapshot)).toContain("Buy now");
	});

	it("keeps agents inside their tab group", async () => {
		const { fake, run, approve } = setup();
		await approve();
		await run("open", { url: "https://shop.test/" });
		const second = await run("tab-new", { url: "https://shop.test/done" });
		expect(second).toMatchObject({ id: "t2", active: true, title: "Thanks" });
		expect((await run("tabs")).tabs).toHaveLength(2);
		const t2 = [...fake.tabs.values()].find((tab) => tab.url === "https://shop.test/done")!;
		t2.groupId = -1; // The user drags it out of the AO group: the agent loses it.
		const tabs = (await run("tabs")).tabs as { id: string }[];
		expect(tabs.map((tab) => tab.id)).toEqual(["t1"]);
		expect(await run("snapshot")).toMatchObject({ target: { tabId: "t1" } });
		await expect(run("tab-select", { tabId: "t2" })).rejects.toMatchObject({ code: "TAB_NOT_FOUND" });
		await expect(run("tab-close", { tabId: "t1" })).rejects.toMatchObject({ code: "CANNOT_CLOSE_LAST_TAB" });
	});

	it("releases the session: closes its window and revokes consent", async () => {
		const { fake, consent, run, approve } = setup();
		await approve();
		await run("open", { url: "https://shop.test/" });
		await run("tab-new");
		await run("__destroy-session");
		expect([...fake.tabs.values()]).toHaveLength(0);
		expect(await consent.approvals()).toEqual({});
	});

	it("closes blank tabs and keeps pages when the window holds a tab of the user's", async () => {
		const { fake, run, approve } = setup();
		await approve();
		await run("open", { url: "https://shop.test/" });
		await run("tab-new");
		const windowId = [...fake.tabs.values()][0]!.windowId;
		await fake.api().tabs.create({ url: "https://personal.test/", active: true, windowId });
		await run("__destroy-session");
		expect([...fake.tabs.values()].map((tab) => tab.url).sort()).toEqual(["https://personal.test/", "https://shop.test/"]);
		expect([...fake.tabs.values()].find((tab) => tab.url === "https://shop.test/")).toMatchObject({ groupId: -1 });
	});

	it("adds every enclosing frame's offset to trusted pointer coordinates", async () => {
		const { fake, run, approve } = setup();
		await approve();
		await run("open", { url: "https://shop.test/" });
		fake.frames = [
			{ frameId: 0, parentFrameId: -1, url: "https://shop.test/" },
			{ frameId: 1, parentFrameId: 0, url: "https://shop.test/outer" },
			{ frameId: 2, parentFrameId: 1, url: "https://shop.test/inner" },
		];
		fake.frameResponder = (frameId, message) => {
			if (message.op === "frameOffset" && frameId === 0 && message.ref === "eOuter") return { x: 100, y: 200, src: "https://shop.test/outer", name: "Outer" };
			if (message.op === "frameOffset" && frameId === 1 && message.ref === "eInner") return { x: 20, y: 30, src: "https://shop.test/inner", name: "Inner" };
			if (message.op === "geometry" && frameId === 2) return { x: 10, y: 15, width: 4, height: 4, inViewport: true };
			if (message.op === "ping" && frameId === 0) return { visibility: "visible" };
			return undefined;
		};
		expect(await run("frame", { target: "eOuter" })).toMatchObject({ frame: "eOuter", name: "Outer" });
		expect(await run("frame", { target: "eInner" })).toMatchObject({ frame: "eInner", name: "Inner" });
		await run("click", { ref: "eButton" });
		const pressed = fake.debuggerCalls.find((call) => call.params?.type === "mousePressed");
		expect(pressed?.params).toMatchObject({ x: 130, y: 245 });

		// Back to the page clears the whole chain.
		await run("frame", { target: "main" });
		fake.debuggerCalls.length = 0;
		fake.frameResponder = (frameId, message) =>
			message.op === "geometry" && frameId === 0 ? { x: 10, y: 15, width: 4, height: 4, inViewport: true } : message.op === "ping" ? { visibility: "visible" } : undefined;
		await run("click", { ref: "eButton" });
		expect(fake.debuggerCalls.find((call) => call.params?.type === "mousePressed")?.params).toMatchObject({ x: 10, y: 15 });
	});

	it("never returns another session's tab when sessions capture the same window at once", async () => {
		const { fake, run, approve } = setup();
		await approve("s1");
		await approve("s2");
		await run("open", { url: "https://shop.test/" }, "s1");
		await run("open", { url: "https://shop.test/done" }, "s2");
		const tabOf = (url: string) => [...fake.tabs.values()].find((tab) => tab.url === url)!.id!;
		const [first, second] = await Promise.all([run("screenshot", {}, "s1"), run("screenshot", {}, "s2")]);
		expect(first).toMatchObject({ target: { url: "https://shop.test/" } });
		expect(second).toMatchObject({ target: { url: "https://shop.test/done" } });
		// Each capture happened while that session's own tab was in front.
		expect(fake.captures.sort()).toEqual([tabOf("https://shop.test/"), tabOf("https://shop.test/done")].sort());
	});

	it("refuses pixels captured after the window switched tabs", async () => {
		const { fake, run, approve } = setup();
		await approve();
		await run("open", { url: "https://shop.test/" });
		const sessionWindow = [...fake.tabs.values()].find((tab) => tab.url === "https://shop.test/")!.windowId;
		const personal = await fake.api().tabs.create({ url: "https://personal.test/", active: false, windowId: sessionWindow });
		// The user switches the session's window to another tab during the capture.
		fake.duringCapture = () => fake.activate(personal.id!);
		await expect(run("screenshot")).rejects.toMatchObject({ code: "BROWSER_TAB_BUSY" });
		expect(fake.captures).toHaveLength(1);
		expect(fake.tabs.get(personal.id!)!.active).toBe(true);
	});

	it("never switches away from a tab the user chose in the agent's window", async () => {
		const { fake, run, approve } = setup();
		await approve();
		await run("open", { url: "https://shop.test/" });
		const own = [...fake.tabs.values()].find((tab) => tab.url === "https://shop.test/")!;
		await run("screenshot");
		expect(own.active).toBe(true);
		const { refs } = (await run("snapshot", { interactive: true })) as { refs: Record<string, { name: string }> };
		const buy = Object.entries(refs).find(([, info]) => info.name === "Buy now")![0];

		const personal = await fake.api().tabs.create({ url: "https://personal.test/", active: false, windowId: own.windowId });
		fake.activate(personal.id!);
		const inputBefore = fake.debuggerCalls.length;

		// Input still works, without trusted input, which would need the tab in front.
		expect(await run("click", { ref: buy })).toMatchObject({ inputTier: "synthetic" });
		expect(await run("press", { key: "Enter" })).toBeTruthy();
		await expect(run("screenshot")).rejects.toMatchObject({ code: "BROWSER_TAB_BUSY" });
		expect(fake.tabs.get(personal.id!)!.active).toBe(true);
		expect(fake.debuggerCalls.slice(inputBefore).filter((call) => call.method.startsWith("Input."))).toEqual([]);

		// Back on the agent's tab, trusted input resumes.
		fake.activate(own.id!);
		expect(await run("click", { ref: buy })).toMatchObject({ inputTier: "trusted" });
	});

	it("still brings an agent's own tab forward when another agent tab is in front", async () => {
		const { fake, run, approve } = setup();
		await approve();
		await run("open", { url: "https://shop.test/" });
		await run("screenshot");
		await run("tab-new", { url: "https://shop.test/more" });
		const first = [...fake.tabs.values()].find((tab) => tab.url === "https://shop.test/more")!;
		await run("screenshot");
		expect(first.active).toBe(true);
	});

	it("returns from open once the page is usable, without waiting for every resource", async () => {
		const { fake, run, approve } = setup();
		await approve();
		fake.holdLoad = true;
		const started = Date.now();
		const opened = await run("open", { url: "https://shop.test/" });
		expect(opened).toMatchObject({ url: "https://shop.test/", isLoading: true });
		expect(Date.now() - started).toBeLessThan(5_000);
	});

	it("ignores load events from the page that was already loading when open started", async () => {
		const { fake, run, approve } = setup();
		await approve();
		await run("open", { url: "https://shop.test/" });
		const tab = [...fake.tabs.values()].find((candidate) => candidate.url === "https://shop.test/")!;
		fake.holdCommit = true;
		let settled = false;
		const opening = run("open", { url: "https://shop.test/done" }).finally(() => (settled = true));
		await vi.waitFor(() => expect(fake.deferCommit).toBeDefined());
		fake.events.navDomContentLoaded.fire({ tabId: tab.id, frameId: 0, url: "https://shop.test/" });
		fake.events.navCompleted.fire({ tabId: tab.id, frameId: 0, url: "https://shop.test/" });
		await new Promise((resolve) => setTimeout(resolve, 1_700));
		expect(settled).toBe(false);
		fake.deferCommit!();
		expect(await opening).toMatchObject({ url: "https://shop.test/done", isLoading: false });
	});

	it("finishes an open that only changes the URL fragment", async () => {
		const { fake, run, approve } = setup();
		await approve();
		await run("open", { url: "https://shop.test/" });
		const tab = [...fake.tabs.values()].find((candidate) => candidate.url === "https://shop.test/")!;
		fake.holdCommit = true;
		const opening = run("open", { url: "https://shop.test/#more" });
		await vi.waitFor(() => expect(fake.deferCommit).toBeDefined());
		tab.url = "https://shop.test/#more";
		const started = Date.now();
		// A same-document navigation fires no DOMContentLoaded or load event.
		fake.events.navFragment.fire({ tabId: tab.id, frameId: 0, url: tab.url });
		expect(await opening).toMatchObject({ isLoading: false, target: { tabId: "t1" } });
		expect(Date.now() - started).toBeLessThan(1_000);
	});

	it("reports a fully loaded page when load finishes within the grace period", async () => {
		const { run, approve } = setup();
		await approve();
		expect(await run("open", { url: "https://shop.test/" })).toMatchObject({ isLoading: false });
	});

	it("applies the site policy to frames, not just the tab", async () => {
		const { fake, run, approve } = setup();
		await approve();
		await run("open", { url: "https://shop.test/" });
		const contentOps: string[] = [];
		fake.frames = [
			{ frameId: 0, parentFrameId: -1, url: "https://shop.test/" },
			{ frameId: 1, parentFrameId: 0, url: "https://www.paypal.com/checkout" },
			{ frameId: 2, parentFrameId: 0, url: "https://widgets.test/form" },
		];
		fake.frameResponder = (frameId, message) => {
			if (frameId !== 0) contentOps.push(`${frameId}:${String(message.op)}`);
			if (message.op === "frameOffset" && frameId === 0 && message.ref === "ePay") return { x: 0, y: 0, src: "https://www.paypal.com/checkout", name: "Pay" };
			if (message.op === "frameOffset" && frameId === 0 && message.ref === "eWidget") return { x: 0, y: 0, src: "https://widgets.test/form", name: "Widget" };
			if (message.op === "snapshot") return { snapshot: '- textbox "Card" [ref=e1]', refs: { e1: { role: "textbox", name: "Card" } } };
			if (message.op === "fill") return { filled: true };
			return undefined;
		};

		// An allowed page embedding a denied site cannot select that frame.
		await expect(run("frame", { target: "ePay" })).rejects.toMatchObject({ code: "BROWSER_SITE_BLOCKED" });
		expect(contentOps).toEqual([]);

		// A frame that navigates to a denied site after selection is blocked.
		await run("frame", { target: "eWidget" });
		expect(await run("snapshot", { interactive: true })).toMatchObject({ text: '- textbox "Card" [ref=e1]' });
		fake.frames[2]!.url = "https://www.paypal.com/embedded";
		await expect(run("snapshot", { interactive: true })).rejects.toMatchObject({ code: "BROWSER_SITE_BLOCKED" });
		await expect(run("fill", { ref: "e1", text: "4111" })).rejects.toMatchObject({ code: "BROWSER_SITE_BLOCKED" });
		const dispatched = fake.debuggerCalls.length;
		await expect(run("press", { key: "Enter" })).rejects.toMatchObject({ code: "BROWSER_SITE_BLOCKED" });
		expect(fake.debuggerCalls).toHaveLength(dispatched);
		expect(contentOps).toEqual(["2:snapshot"]);
	});

	it("uses synthetic input when a frame on the way requires it", async () => {
		const { fake, run, approve } = setup();
		await approve();
		await run("open", { url: "https://shop.test/" });
		await fake.local.set({ [POLICY_KEY]: { ...DEFAULT_SITE_POLICY, syntheticInput: ["widgets.test"] } });
		fake.frames = [
			{ frameId: 0, parentFrameId: -1, url: "https://shop.test/" },
			{ frameId: 2, parentFrameId: 0, url: "https://widgets.test/form" },
		];
		fake.frameResponder = (frameId, message) => {
			if (message.op === "frameOffset" && frameId === 0) return { x: 0, y: 0, src: "https://widgets.test/form", name: "Widget" };
			if (message.op === "click" && frameId === 2) return { clicked: true };
			return undefined;
		};
		await run("frame", { target: "eWidget" });
		const attached = fake.attached;
		expect(await run("click", { ref: "e1" })).toMatchObject({ inputTier: "synthetic" });
		expect(fake.attached).toBe(attached);
	});

	it("refuses panel-only actions", async () => {
		const { run } = setup();
		await expect(run("devtools-open")).rejects.toMatchObject({ code: "BROWSER_ACTION_UNSUPPORTED_IN_TARGET" });
		await expect(run("__render-check")).rejects.toMatchObject({ code: "BROWSER_ACTION_UNSUPPORTED_IN_TARGET" });
	});

	it("captures screenshots of the session tab", async () => {
		const { fake, run, approve } = setup();
		await approve();
		await run("open", { url: "https://shop.test/" });
		const shot = await run("screenshot");
		expect(shot).toMatchObject({ width: 1, height: 1, target: { tabId: "t1" } });
		expect([...fake.tabs.values()][0]!.active).toBe(true);
	});
});

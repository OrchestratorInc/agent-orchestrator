import { expect, it, vi } from "vitest";
import { Bridge } from "./background";
import type { ChromeApi, Port } from "./chrome-api";

it("answers broker liveness pings without starting a session command", () => {
	const bridge = new Bridge({} as ChromeApi);
	const postMessage = vi.fn();
	const runtime = bridge as unknown as { port: Port; onHostMessage(message: unknown): void };
	runtime.port = { postMessage } as unknown as Port;
	runtime.onHostMessage({ type: "ping" });
	expect(postMessage).toHaveBeenCalledExactlyOnceWith({ type: "pong" });
});

it("aborts only the revoked session's admitted commands before removing approval", async () => {
	const bridge = new Bridge({} as ChromeApi);
	const canceled = new AbortController();
	const other = new AbortController();
	const runtime = bridge as unknown as {
		inFlight: Map<string, { controller: AbortController; sessionId: string }>;
		handleUi(message: unknown): Promise<unknown>;
	};
	runtime.inFlight.set("one", { controller: canceled, sessionId: "s1" });
	runtime.inFlight.set("two", { controller: other, sessionId: "s2" });
	const revoke = vi.spyOn(bridge.consent, "revoke").mockImplementation(async () => {
		expect(canceled.signal.aborted).toBe(true);
		expect(other.signal.aborted).toBe(false);
	});
	await expect(runtime.handleUi({ type: "ao-ui-revoke", sessionId: "s1" })).resolves.toEqual({ revoked: true });
	expect(revoke).toHaveBeenCalledWith("s1", true);
});

it("reconnects to AO when Chrome wakes the service worker with its alarm or at startup", () => {
	const listeners: Record<string, (...args: unknown[]) => void> = {};
	const listen = (name: string) => ({ addListener: (callback: (...args: unknown[]) => void) => (listeners[name] = callback), removeListener: vi.fn() });
	const ports: Array<{ onDisconnect: (() => void) | undefined }> = [];
	const connectNative = vi.fn(() => {
		const port = { onDisconnect: undefined as (() => void) | undefined };
		ports.push(port);
		return {
			postMessage: vi.fn(),
			disconnect: vi.fn(),
			onMessage: { addListener: vi.fn(), removeListener: vi.fn() },
			onDisconnect: { addListener: (callback: () => void) => (port.onDisconnect = callback), removeListener: vi.fn() },
		};
	});
	const createAlarm = vi.fn(async () => undefined);
	const chrome = {
		runtime: {
			id: "ext",
			connectNative,
			getManifest: () => ({ version: "0.1.0" }),
			onMessage: listen("message"),
			onStartup: listen("startup"),
			onInstalled: listen("installed"),
		},
		alarms: { create: createAlarm, onAlarm: listen("alarm") },
		tabs: { onRemoved: listen("tabRemoved"), onUpdated: listen("tabUpdated"), onActivated: listen("tabActivated") },
		webNavigation: { onCommitted: listen("navCommitted"), onDOMContentLoaded: listen("navDomContentLoaded"), onReferenceFragmentUpdated: listen("navFragment"), onCompleted: listen("navCompleted"), onErrorOccurred: listen("navError") },
		webRequest: { onBeforeRequest: listen("requestStarted"), onCompleted: listen("requestCompleted"), onErrorOccurred: listen("requestError") },
		tabGroups: { onRemoved: listen("groupRemoved") },
		windows: { onRemoved: listen("windowRemoved") },
		debugger: { onEvent: listen("debuggerEvent"), onDetach: listen("debuggerDetach") },
		action: { setBadgeText: async () => undefined, setBadgeBackgroundColor: async () => undefined, setTitle: async () => undefined },
	} as unknown as ChromeApi;
	vi.useFakeTimers();
	try {
		new Bridge(chrome).start();
		expect(createAlarm).toHaveBeenCalledWith("ao-reconnect", { periodInMinutes: 0.5 });
		expect(connectNative).toHaveBeenCalledTimes(1);

		// Connected: the alarm does not open a second port.
		listeners.alarm?.({ name: "ao-reconnect" });
		expect(connectNative).toHaveBeenCalledTimes(1);

		// AO went away and the worker's reconnect timer died with it.
		ports[0]!.onDisconnect?.();
		vi.clearAllTimers();
		listeners.alarm?.({ name: "ao-reconnect" });
		expect(connectNative).toHaveBeenCalledTimes(2);
		listeners.alarm?.({ name: "something-else" });
		expect(connectNative).toHaveBeenCalledTimes(2);

		ports[1]!.onDisconnect?.();
		listeners.startup?.();
		expect(connectNative).toHaveBeenCalledTimes(3);
	} finally {
		vi.useRealTimers();
	}
});

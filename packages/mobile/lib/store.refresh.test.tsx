import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { SessionsResponse } from "./api";

const state = vi.hoisted(() => ({
	onState: undefined as ((value: string) => void) | undefined,
	onNetwork: undefined as ((value: { isConnected?: boolean; isInternetReachable?: boolean; type?: string }) => void) | undefined,
	resolve: vi.fn(), sessions: vi.fn(), notifications: vi.fn(), selected: "a",
	endpoints: [] as unknown[],
	paired: [] as unknown[], others: {} as Record<string, unknown>,
}));
vi.mock("@react-native-async-storage/async-storage", () => ({ default: { getItem: async () => null, setItem: async () => {} } }));
vi.mock("expo-secure-store", () => ({ getItemAsync: vi.fn(), setItemAsync: vi.fn(), deleteItemAsync: vi.fn() }));
vi.mock("react-native", () => ({ AppState: { currentState: "active", addEventListener: (_: string, callback: (value: string) => void) => {
	state.onState = callback; return { remove: () => {} };
} } }));
vi.mock("expo-network", () => ({
	getNetworkStateAsync: async () => ({ isConnected: true, isInternetReachable: true, type: "WIFI" }),
	addNetworkStateListener: (callback: NonNullable<typeof state.onNetwork>) => { state.onNetwork = callback; return { remove: () => {} }; },
}));
vi.mock("./api", () => ({ ApiError: class extends Error {}, getSessions: state.sessions, getNotifications: state.notifications }));
vi.mock("./resolveConfig", () => ({ resolveActiveConfig: state.resolve, runtimeResolveDeps: () => ({}) }));
vi.mock("./race", () => ({ IncompatibleHostVersionError: class extends Error {} }));
vi.mock("./connectRuntime", () => ({ rejectedEndpointNeedsRace: () => false }));
vi.mock("./hosts", () => ({
	activeHost: async () => ({ id: state.selected, name: state.selected, endpoints: state.endpoints, token: "secret" }),
	loadHosts: async () => state.paired, sameHostConnections: (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b),
	setActiveHost: async (id: string) => { state.selected = id; },
}));
vi.mock("./account", () => ({ loadAccount: async () => null }));
vi.mock("./accountHosts", () => ({ syncAccountHosts: vi.fn(), cancelAccountHostSync: vi.fn() }));
vi.mock("./installId", () => ({ primeInstallId: vi.fn() }));
vi.mock("./telemetry/runtime", () => ({ mobileTelemetry: () => null, trackFeature: vi.fn() }));
vi.mock("./chat/conversationEvents", () => ({ useConversationEventTransport: () => {} }));
vi.mock("./otherHosts", async (importOriginal) => ({ ...(await importOriginal<typeof import("./otherHosts")>()), useOtherHosts: () => ({ snapshots: state.others, refreshHost: vi.fn() }) }));

import { UnreachableError } from "./connectionError";
import { IncompatibleHostVersionError } from "./race";
import { AppProvider, HostScope, useApp } from "./store";
import { EMPTY_LINK_TRACKER, recordPoll } from "./linkHealth";
let current!: ReturnType<typeof useApp>;
let renderer: ReactTestRenderer | undefined;
function Harness() { current = useApp(); return null; }
function board(title: string): SessionsResponse {
	return { projects: [], orchestrators: [], orchestratorId: null, stats: {}, sessions: [{
		id: "worker", projectId: "", status: "idle", mode: "chat", branch: null, issueId: null,
		issueTitle: null, userPrompt: null, displayName: title, summary: null,
		createdAt: "2026-10-11T00:00:00Z", lastActivityAt: "2026-10-11T00:00:00Z",
	}] };
}
function deferred() {
	let resolve!: (value: SessionsResponse) => void;
	const promise = new Promise<SessionsResponse>((done) => { resolve = done; });
	return { promise, resolve };
}
async function mount() { await act(async () => { renderer = create(<AppProvider><Harness /></AppProvider>); }); }
beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	vi.useFakeTimers(); vi.clearAllMocks(); state.selected = "a"; state.endpoints = []; state.paired = []; state.others = {};
	state.resolve.mockImplementation(async () => ({ hostId: state.selected, host: `${state.selected}.test`, httpPort: "3011", muxPort: "3011", password: "secret" }));
	state.sessions.mockResolvedValue(board("Fresh"));
	state.notifications.mockResolvedValue({ notifications: [], unreadCount: 0 });
});
afterEach(async () => {
	await act(async () => { renderer?.unmount(); }); renderer = undefined;
	vi.useRealTimers(); vi.unstubAllGlobals();
});

it("waits for a slow board load to complete before scheduling the next poll", async () => {
	const slow = deferred(); state.sessions.mockReturnValueOnce(slow.promise);
	await mount();
	await act(async () => { await vi.advanceTimersByTimeAsync(16_000); });
	expect(state.sessions).toHaveBeenCalledTimes(1);
	await act(async () => { slow.resolve(board("First")); });
	expect(current.sessions[0].displayName).toBe("First");
	await act(async () => { await vi.advanceTimersByTimeAsync(7_999); });
	expect(state.sessions).toHaveBeenCalledTimes(1);
	await act(async () => { await vi.advanceTimersByTimeAsync(1); });
	expect(current.sessions[0].displayName).toBe("Fresh");
});

it("coalesces manual refreshes into one follow-up so a completed mutation is not lost", async () => {
	const slow = deferred(); state.sessions.mockReturnValueOnce(slow.promise);
	await mount();
	let first!: Promise<void>; let second!: Promise<void>;
	await act(async () => { first = current.refresh(); second = current.refresh(); });
	expect(state.sessions).toHaveBeenCalledTimes(1);
	await act(async () => { slow.resolve(board("Before mutation")); await Promise.all([first, second]); });
	expect(state.sessions).toHaveBeenCalledTimes(2);
	expect(current.sessions[0].displayName).toBe("Fresh");
});

it("cancels the load on backgrounding and ignores its late response", async () => {
	const slow = deferred(); state.sessions.mockReturnValueOnce(slow.promise);
	await mount();
	const signal = state.sessions.mock.calls[0][2]?.signal as AbortSignal | undefined;
	await act(async () => { state.onState!("background"); });
	expect(signal?.aborted).toBe(true);
	await act(async () => { slow.resolve(board("Stale")); });
	expect(current.connection).toBe("closed");
	expect(state.notifications).not.toHaveBeenCalled();
});

it("ignores the old A load even after switching A to B to A", async () => {
	const slow = deferred(); state.sessions.mockReturnValueOnce(slow.promise);
	await mount();
	await act(async () => { await current.switchHost("b"); });
	await act(async () => { await current.switchHost("a"); });
	expect(current.sessions[0].displayName).toBe("Fresh");
	await act(async () => { slow.resolve(board("Old A")); });
	expect(current.sessions[0].displayName).toBe("Fresh");
});

it("does not start a retained refresh after the provider unmounts", async () => {
	await mount();
	const refresh = current.refresh;
	const calls = state.sessions.mock.calls.length;
	await act(async () => { renderer!.unmount(); }); renderer = undefined;
	await refresh();
	expect(state.sessions).toHaveBeenCalledTimes(calls);
});

// One poll that gets no answer used to put "Your machine is offline" on screen,
// and the next successful one quietly took it back.
it("reports a single unanswered poll as reconnecting, not as an offline machine", async () => {
	state.sessions.mockRejectedValueOnce(new UnreachableError("offline"));
	await mount();
	expect(current.connection).toBe("closed");
	expect(current.linkHealth).toBe("reconnecting");
	expect(current.unreachable).toBe(false);
	await act(async () => { await vi.advanceTimersByTimeAsync(8_000); });
	expect(current.linkHealth).toBe("ok");
	expect(current.connection).toBe("open");
});

it("calls the machine unreachable once it has stopped answering for a while", async () => {
	state.sessions.mockRejectedValue(new UnreachableError("offline"));
	await mount();
	await act(async () => { await vi.advanceTimersByTimeAsync(8_000); });
	expect(current.unreachable).toBe(false);
	await act(async () => { await vi.advanceTimersByTimeAsync(8_000); });
	expect(current.linkHealth).toBe("unreachable");
	expect(current.unreachable).toBe(true);
});

it("blames the phone's network, then races and polls as soon as it returns", async () => {
	state.sessions.mockRejectedValue(new UnreachableError("offline"));
	await mount();
	await act(async () => { state.onNetwork!({ isConnected: false, isInternetReachable: false, type: "NONE" }); });
	await act(async () => { await vi.advanceTimersByTimeAsync(60_000); });
	expect(current.phoneOffline).toBe(true);
	expect(current.linkHealth).toBe("phone-offline");
	expect(current.unreachable).toBe(false);

	state.sessions.mockResolvedValue(board("Back"));
	const races = state.resolve.mock.calls.length;
	await act(async () => { state.onNetwork!({ isConnected: true, isInternetReachable: true, type: "WIFI" }); });
	expect(state.resolve.mock.calls.length).toBe(races + 1);
	expect(current.phoneOffline).toBe(false);
	expect(current.connection).toBe("open");
	expect(current.linkHealth).toBe("ok");
	expect(current.sessions[0].displayName).toBe("Back");
});

// A board request still hanging on the link the phone just left must not hold
// recovery hostage: the race found a working endpoint (here the same URL), so a
// fresh request goes out at once, and the poll loop keeps running afterwards.
it("replaces a request stuck on the old link when the network returns", async () => {
	state.sessions.mockImplementationOnce((_config: unknown, _scope: unknown, options?: { signal?: AbortSignal }) =>
		new Promise((_, reject) => options?.signal?.addEventListener("abort", () => reject(new Error("aborted")))));
	await mount();
	expect(state.sessions).toHaveBeenCalledTimes(1);
	const stuck = state.sessions.mock.calls[0][2]?.signal as AbortSignal;
	await act(async () => { state.onNetwork!({ isConnected: false, isInternetReachable: false, type: "NONE" }); });
	state.sessions.mockResolvedValue(board("Recovered"));
	await act(async () => { state.onNetwork!({ isConnected: true, isInternetReachable: true, type: "WIFI" }); });
	expect(stuck.aborted).toBe(true);
	expect(state.sessions).toHaveBeenCalledTimes(2);
	expect(current.sessions[0].displayName).toBe("Recovered");
	expect(current.connection).toBe("open");
	// The poll loop was waiting on the stuck load; it must carry on, not stop.
	await act(async () => { await vi.advanceTimersByTimeAsync(8_000); });
	expect(state.sessions).toHaveBeenCalledTimes(3);
});

// The machine answered, just with a version this phone can't use. A link that
// was "reconnecting" must not keep saying so (and that it clears on its own)
// over the upgrade guidance the 426 deserves.
it("lets an incompatible-version race override reconnecting", async () => {
	state.sessions.mockRejectedValueOnce(new UnreachableError("offline"));
	await mount();
	expect(current.linkHealth).toBe("reconnecting");
	// The network comes back and the race now meets a machine this phone can't use.
	state.resolve.mockRejectedValueOnce(new IncompatibleHostVersionError("Update Orchestrator"));
	await act(async () => { state.onNetwork!({ isConnected: false, isInternetReachable: false, type: "NONE" }); });
	await act(async () => { state.onNetwork!({ isConnected: true, isInternetReachable: true, type: "WIFI" }); });
	expect(current.errorStatus).toBe(426);
	expect(current.config).toBeNull();
	expect(current.linkHealth).toBe("ok");
});

// On the tunnel with a better LAN address saved, the upgrade check races once a
// minute. With the phone offline that race can only fail, and a failed race
// clears the config and drops the rows that "You're offline" promised to keep.
it("does not race for a better endpoint while the phone is offline", async () => {
	state.endpoints = [{ kind: "lan", host: "192.168.1.5", port: 3011, secure: false }];
	state.resolve.mockImplementation(async () => ({ hostId: "a", host: "a.trycloudflare.com", httpPort: "443", muxPort: "443", password: "secret", endpointKind: "tunnel" }));
	await mount();
	expect(current.sessions[0].displayName).toBe("Fresh");
	const races = state.resolve.mock.calls.length;
	state.sessions.mockRejectedValue(new UnreachableError("offline"));
	state.resolve.mockResolvedValue(null);
	await act(async () => { state.onNetwork!({ isConnected: false, isInternetReachable: false, type: "NONE" }); });
	await act(async () => { await vi.advanceTimersByTimeAsync(65_000); });
	expect(state.resolve.mock.calls.length).toBe(races);
	expect(current.config).not.toBeNull();
	expect(current.sessions[0].displayName).toBe("Fresh");
	expect(current.linkHealth).toBe("phone-offline");
});

// Another paired machine's chat and project views go through HostScope. One
// unanswered poll there must read as reconnecting, as it does for the selected
// machine, not as "This machine is offline".
it("gives another machine the same reconnecting grace period", async () => {
	const hostB = { id: "b", name: "b", endpoints: [], token: "secret", lastConnected: 0 };
	state.paired = [{ id: "a", name: "a", endpoints: [], token: "secret", lastConnected: 0 }, hostB];
	const failedOnce = recordPoll(EMPTY_LINK_TRACKER, "no-answer", Date.now());
	state.others = { b: {
		hostId: "b", name: "b", appearance: {}, endpoints: [], config: null, connection: "closed", loading: false,
		error: "Couldn't reach your machine.", errorStatus: null, projects: [], projectsKnown: false, sessions: [], archive: null,
		orchestrators: [], orchestratorId: null, stats: {}, notificationsUnread: 0, lastSyncAt: 0, linkTracker: failedOnce,
	} };
	let scoped!: ReturnType<typeof useApp>;
	function ScopedHarness() { scoped = useApp(); return null; }
	await act(async () => { renderer = create(<AppProvider><HostScope hostId="b"><ScopedHarness /></HostScope></AppProvider>); });
	expect(scoped.currentHostId).toBe("b");
	expect(scoped.linkHealth).toBe("reconnecting");
	expect(scoped.unreachable).toBe(false);
});

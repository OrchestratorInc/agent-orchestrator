import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { SessionsResponse } from "./api";

const state = vi.hoisted(() => ({
	onState: undefined as ((value: string) => void) | undefined,
	resolve: vi.fn(), sessions: vi.fn(), notifications: vi.fn(), selected: "a",
}));
vi.mock("@react-native-async-storage/async-storage", () => ({ default: { getItem: async () => null, setItem: async () => {} } }));
vi.mock("expo-secure-store", () => ({ getItemAsync: vi.fn(), setItemAsync: vi.fn(), deleteItemAsync: vi.fn() }));
vi.mock("react-native", () => ({ AppState: { currentState: "active", addEventListener: (_: string, callback: (value: string) => void) => {
	state.onState = callback; return { remove: () => {} };
} } }));
vi.mock("./api", () => ({ ApiError: class extends Error {}, getSessions: state.sessions, getNotifications: state.notifications }));
vi.mock("./resolveConfig", () => ({ resolveActiveConfig: state.resolve, runtimeResolveDeps: () => ({}) }));
vi.mock("./race", () => ({ IncompatibleHostVersionError: class extends Error {} }));
vi.mock("./connectRuntime", () => ({ rejectedEndpointNeedsRace: () => false }));
vi.mock("./hosts", () => ({
	activeHost: async () => ({ id: state.selected, name: state.selected, endpoints: [], token: "secret" }),
	loadHosts: async () => [], sameHostConnections: () => true,
	setActiveHost: async (id: string) => { state.selected = id; },
}));
vi.mock("./account", () => ({ loadAccount: async () => null }));
vi.mock("./accountHosts", () => ({ syncAccountHosts: vi.fn(), cancelAccountHostSync: vi.fn() }));
vi.mock("./installId", () => ({ primeInstallId: vi.fn() }));
vi.mock("./telemetry/runtime", () => ({ mobileTelemetry: () => null, trackFeature: vi.fn() }));
vi.mock("./chat/conversationEvents", () => ({ useConversationEventTransport: () => {} }));
vi.mock("./otherHosts", () => ({ useOtherHosts: () => ({ snapshots: {}, refreshHost: vi.fn() }) }));

import { AppProvider, useApp } from "./store";
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
	vi.useFakeTimers(); vi.clearAllMocks(); state.selected = "a";
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

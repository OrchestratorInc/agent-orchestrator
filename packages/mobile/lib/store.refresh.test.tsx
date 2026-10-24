import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { DashboardSession, SessionsResponse } from "./api";
import type { SessionSource } from "./environment/types";
import type { HostSnapshot } from "./otherHosts";

const state = vi.hoisted(() => ({
	onState: undefined as ((value: string) => void) | undefined,
	resolve: vi.fn(), sessions: vi.fn(), notifications: vi.fn(), refreshOther: vi.fn(), selected: "a",
	cloud: undefined as SessionSource | undefined, cloudEpoch: 0,
	cloudOrgError: null as string | null, retryOrg: vi.fn(async () => {}),
	secondary: {} as Record<string, HostSnapshot>,
	adoptPrimary: vi.fn(), claimPrimary: vi.fn(),
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
	loadHosts: async () => ["a", "b"].map((id) => ({ id, name: id, platform: "darwin", endpoints: [], token: "secret", lastConnected: 0 })),
	sameHostConnections: (previous: unknown, next: unknown) => JSON.stringify(previous) === JSON.stringify(next),
	setActiveHost: async (id: string) => { state.selected = id; },
}));
vi.mock("./account", () => ({ loadAccount: async () => null }));
vi.mock("./accountHosts", () => ({ syncAccountHosts: vi.fn(), cancelAccountHostSync: vi.fn() }));
// These are paired-host tests; Cloud authentication is a separate native boundary.
vi.mock("./cloud/authStore", () => ({ useCloudAuth: () => ({ signedIn: !!state.cloud, orgId: state.cloud ? "org" : null, orgLoading: false, orgError: state.cloudOrgError, retryOrgResolution: state.retryOrg, sessionEpoch: state.cloudEpoch, client: state.cloud }) }));
vi.mock("./environment/resolve", async (importOriginal) => ({
	...await importOriginal<typeof import("./environment/resolve")>(),
	resolveCloudSource: () => state.cloud,
}));
vi.mock("expo/fetch", () => ({ fetch: vi.fn() }));
vi.mock("./installId", () => ({ primeInstallId: vi.fn() }));
vi.mock("./telemetry/runtime", () => ({ mobileTelemetry: () => null, trackFeature: vi.fn() }));
vi.mock("./chat/conversationEvents", () => ({ useConversationEventTransport: () => {} }));
vi.mock("./otherHosts", async (importOriginal) => ({
	...await importOriginal<typeof import("./otherHosts")>(),
	useOtherHosts: () => ({ snapshots: state.secondary, refreshHost: state.refreshOther, configForHost: () => null, adoptPrimary: state.adoptPrimary, claimPrimary: state.claimPrimary }),
}));

import { AppProvider, useApp } from "./store";
import { emptyHostSnapshot } from "./otherHosts";
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
	vi.useFakeTimers(); vi.clearAllMocks(); state.selected = "a"; state.cloud = undefined; state.cloudEpoch = 0; state.secondary = {};
	vi.setSystemTime(new Date("2026-10-21T10:00:00Z"));
	state.cloudOrgError = null; state.retryOrg.mockReset().mockImplementation(async () => {});
	state.resolve.mockImplementation(async () => ({ hostId: state.selected, host: `${state.selected}.test`, httpPort: "3011", muxPort: "3011", password: "secret" }));
	state.sessions.mockResolvedValue(board("Fresh"));
	state.notifications.mockResolvedValue({ notifications: [], unreadCount: 0 });
});
afterEach(async () => {
	await act(async () => { renderer?.unmount(); }); renderer = undefined;
	vi.useRealTimers(); vi.unstubAllGlobals();
});

it("refreshes an offline secondary pairing even without a verified configuration", async () => {
	await mount();
	expect(current.configForHost("b")).toBeNull();
	await act(async () => { await current.refreshAll(); });
	expect(state.refreshOther).toHaveBeenCalledWith("b");
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

function cloudBoard() {
	const listProjects = vi.fn(async () => []);
	const listSessions = vi.fn(async (): Promise<DashboardSession[]> => []);
	return { source: { kind: "cloud", listProjects, listSessions } as unknown as SessionSource, listProjects, listSessions };
}

it.each(["all", "source", "selected"])("retries failed workspace settings during an explicit %s Cloud refresh, not background polling", async (action) => {
	const cloud = cloudBoard(); state.cloud = cloud.source; state.cloudOrgError = "Workspace offline";
	state.retryOrg.mockImplementation(async () => { state.cloudOrgError = null; });
	await mount();
	await act(async () => { await vi.advanceTimersByTimeAsync(5_000); });
	expect(state.retryOrg).not.toHaveBeenCalled();
	expect(current.scopedBoard.sources['["cloud","org"]'].error).toBe("Workspace offline");
	await act(async () => { current.setEnvironment("cloud"); });
	await act(async () => {
		if (action === "all") await current.refreshAll();
		else if (action === "source") await current.refreshSource({ kind: "cloud", id: "org" });
		else await current.refresh();
	});
	expect(state.retryOrg).toHaveBeenCalledOnce();
	expect(current.scopedBoard.sources['["cloud","org"]'].error).toBeNull();
});

it.each(["background", "unmount"])("does not retry Cloud workspace settings from a retained refresh after %s", async (stage) => {
	const cloud = cloudBoard(); state.cloud = cloud.source; state.cloudOrgError = "Workspace offline";
	await mount();
	const refresh = current.refreshAll;
	if (stage === "background") await act(async () => { state.onState!("background"); });
	else { await act(async () => { renderer!.unmount(); }); renderer = undefined; }
	await refresh();
	expect(state.retryOrg).not.toHaveBeenCalled();
});

it.each([5_000, 1_000])("publishes slow Cloud loads without overlapping %i ms background polls", async (interval) => {
	const cloud = cloudBoard(); state.cloud = cloud.source;
	const session = board("Cloud worker").sessions[0];
	const rows = [{ ...session, cloud: { desiredState: "running", observedState: interval === 1_000 ? "provisioning" : "running" }, runtimeConnected: interval !== 1_000 }] as DashboardSession[];
	cloud.listSessions.mockResolvedValue(rows);
	await mount();
	const initialCalls = cloud.listSessions.mock.calls.length;
	let finish!: (rows: DashboardSession[]) => void;
	cloud.listSessions.mockImplementationOnce(() => new Promise((resolve) => { finish = resolve; }));
	await act(async () => { await vi.advanceTimersByTimeAsync(interval); });
	expect(cloud.listSessions).toHaveBeenCalledTimes(initialCalls + 1);
	await act(async () => { await vi.advanceTimersByTimeAsync(interval * 2); });
	expect(cloud.listSessions).toHaveBeenCalledTimes(initialCalls + 1);
	await act(async () => { finish(rows); });
	expect(current.cloudBoard.loading).toBe(false);
	expect(current.cloudBoard.sessions[0].displayName).toBe("Cloud worker");
	await act(async () => { await vi.advanceTimersByTimeAsync(interval - 1); });
	expect(cloud.listSessions).toHaveBeenCalledTimes(initialCalls + 1);
	await act(async () => { await vi.advanceTimersByTimeAsync(1); });
	expect(cloud.listSessions).toHaveBeenCalledTimes(initialCalls + 2);
});

it("does not schedule another Cloud poll after backgrounding or unmounting during a load", async () => {
	const cloud = cloudBoard(); state.cloud = cloud.source;
	let finish!: (rows: DashboardSession[]) => void;
	cloud.listSessions.mockImplementationOnce(() => new Promise((resolve) => { finish = resolve; }));
	await mount();
	const refresh = current.refreshSource;
	const sync = current.getSourceLastSyncAt;
	await act(async () => { state.onState!("background"); finish(board("Late").sessions); });
	expect(current.cloudBoard.sessions).toEqual([]);
	expect(sync('["cloud","org"]')).toBe(0);
	await act(async () => { await vi.advanceTimersByTimeAsync(20_000); });
	expect(cloud.listSessions).toHaveBeenCalledTimes(1);
	await act(async () => { state.onState!("active"); });
	expect(cloud.listSessions).toHaveBeenCalledTimes(2);
	await act(async () => { renderer!.unmount(); }); renderer = undefined;
	await refresh({ kind: "cloud", id: "org" });
	await vi.advanceTimersByTimeAsync(20_000);
	expect(cloud.listSessions).toHaveBeenCalledTimes(2);
});

it("does not supersede an explicit Cloud refresh when a background timer fires", async () => {
	const cloud = cloudBoard(); state.cloud = cloud.source;
	await mount();
	await act(async () => { await vi.advanceTimersByTimeAsync(4_999); });
	let finish!: (rows: DashboardSession[]) => void;
	cloud.listSessions.mockImplementationOnce(() => new Promise((resolve) => { finish = resolve; }));
	let refresh!: Promise<void>;
	await act(async () => { refresh = current.refreshSource({ kind: "cloud", id: "org" }); });
	await act(async () => { await vi.advanceTimersByTimeAsync(1); });
	expect(cloud.listSessions).toHaveBeenCalledTimes(2);
	await act(async () => { finish(board("After mutation").sessions); await refresh; });
	expect(current.cloudBoard.sessions[0].displayName).toBe("After mutation");
});

it("keeps live per-source sync clocks independent of the selected environment and memoized snapshots", async () => {
	const cloud = cloudBoard(); state.cloud = cloud.source;
	await mount();
	const getSync = current.getSourceLastSyncAt;
	const first = Date.now();
	expect(getSync('["local","a"]')).toBe(first);
	expect(getSync('["cloud","org"]')).toBe(first);
	expect(getSync('["local","b"]')).toBe(0);
	expect(getSync('["cloud","a"]')).toBe(0);
	vi.setSystemTime(first + 10_000);
	await act(async () => { await current.refreshSource({ kind: "cloud", id: "org" }); });
	expect(getSync('["cloud","org"]')).toBe(first + 10_000);
	expect(getSync('["local","a"]')).toBe(first);
	vi.setSystemTime(first + 20_000);
	await act(async () => { await current.refreshHost("a"); });
	expect(current.getSourceLastSyncAt).toBe(getSync);
	expect(getSync('["local","a"]')).toBe(first + 20_000);
	expect(getSync('["cloud","org"]')).toBe(first + 10_000);
	expect(getSync('["local","missing"]')).toBe(0);
});

it("does not freshen Cloud on failure or an older response superseded by a newer refresh", async () => {
	const cloud = cloudBoard(); state.cloud = cloud.source;
	await mount();
	const first = Date.now();
	cloud.listSessions.mockRejectedValueOnce(new Error("offline"));
	vi.setSystemTime(first + 10_000);
	await act(async () => { await current.refreshSource({ kind: "cloud", id: "org" }); });
	expect(current.getSourceLastSyncAt('["cloud","org"]')).toBe(first);
	let finishOld!: (rows: DashboardSession[]) => void;
	cloud.listSessions.mockImplementationOnce(() => new Promise((resolve) => { finishOld = resolve; }));
	let old!: Promise<void>;
	await act(async () => { old = current.refreshSource({ kind: "cloud", id: "org" }); });
	vi.setSystemTime(first + 20_000);
	await act(async () => { await current.refreshSource({ kind: "cloud", id: "org" }); });
	vi.setSystemTime(first + 30_000);
	await act(async () => { finishOld([]); await old; });
	expect(current.getSourceLastSyncAt('["cloud","org"]')).toBe(first + 20_000);
});

it("resets same-org Cloud freshness on account/source changes and rejects the old account's late success", async () => {
	const cloud = cloudBoard(); state.cloud = cloud.source;
	await mount();
	let finishOld!: (rows: DashboardSession[]) => void;
	cloud.listSessions.mockImplementationOnce(() => new Promise((resolve) => { finishOld = resolve; }));
	let old!: Promise<void>;
	await act(async () => { old = current.refreshSource({ kind: "cloud", id: "org" }); });
	const next = cloudBoard(); next.listSessions.mockImplementation(() => new Promise(() => {}));
	state.cloud = next.source; state.cloudEpoch++;
	await act(async () => { renderer!.update(<AppProvider><Harness /></AppProvider>); });
	expect(current.getSourceLastSyncAt('["cloud","org"]')).toBe(0);
	vi.setSystemTime(Date.now() + 60_000);
	await act(async () => { finishOld([]); await old; });
	expect(current.getSourceLastSyncAt('["cloud","org"]')).toBe(0);
});

it("reads each background machine's own successful sync and forgets removed snapshots", async () => {
	const host = { id: "b", name: "Build Mac", platform: "darwin", endpoints: [], token: "secret", lastConnected: 0 };
	state.secondary = { b: { ...emptyHostSnapshot(host), lastSyncAt: Date.now() - 90_000 } };
	await mount();
	const getSync = current.getSourceLastSyncAt;
	expect(getSync('["local","b"]')).toBe(Date.now() - 90_000);
	state.secondary = { b: { ...state.secondary.b, lastSyncAt: Date.now() - 5_000 } };
	await act(async () => { renderer!.update(<AppProvider><Harness /></AppProvider>); });
	expect(getSync('["local","b"]')).toBe(Date.now() - 5_000);
	state.secondary = {};
	await act(async () => { renderer!.update(<AppProvider><Harness /></AppProvider>); });
	expect(getSync('["local","b"]')).toBe(0);
	expect(getSync('["local","a"]')).toBe(Date.now());
});

it("transfers the live primary clock, not an older memoized timestamp, when promoting another host", async () => {
	const host = { id: "b", name: "Build Mac", platform: "darwin", endpoints: [], token: "secret", lastConnected: 0 };
	const snapshot: HostSnapshot = { ...emptyHostSnapshot(host), connection: "open", config: { hostId: "b", host: "b.test", httpPort: "3011", muxPort: "3011", password: "secret" }, lastSyncAt: Date.now() };
	state.secondary = { b: snapshot }; state.claimPrimary.mockReturnValue(snapshot);
	await mount();
	const firstSnapshot = current.hostStates.find((host) => host.hostId === "a");
	vi.setSystemTime(Date.now() + 10_000);
	await act(async () => { await current.refreshHost("a"); });
	expect(current.hostStates.find((host) => host.hostId === "a")).toBe(firstSnapshot);
	await act(async () => { await current.switchHost("b"); });
	expect(state.adoptPrimary).toHaveBeenCalledWith(expect.objectContaining({ hostId: "a", lastSyncAt: Date.now() }));
});

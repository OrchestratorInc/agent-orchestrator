import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { SessionsResponse } from "./api";
import type { ServerConfig } from "./config";

// The resume flicker, end to end through the real store: an open chat must stay
// on its screen while the app is in the background, while the endpoint race
// runs on return, when that race lands on a different endpoint, and through
// the first poll after it. Before the machine was kept apart from the verified
// connection, the session route swapped the chat for a loader at each of these.
const state = vi.hoisted(() => ({
	onState: undefined as ((value: string) => void) | undefined,
	resolve: vi.fn(), sessions: vi.fn(), notifications: vi.fn(),
}));
vi.mock("@react-native-async-storage/async-storage", () => ({ default: { getItem: async () => null, setItem: async () => {} } }));
vi.mock("expo-secure-store", () => ({ getItemAsync: vi.fn(), setItemAsync: vi.fn(), deleteItemAsync: vi.fn() }));
vi.mock("react-native", () => ({ AppState: { currentState: "active", addEventListener: (_: string, callback: (value: string) => void) => {
	state.onState = callback; return { remove: () => {} };
} } }));
vi.mock("expo-network", () => ({
	getNetworkStateAsync: async () => ({ isConnected: true, isInternetReachable: true, type: "WIFI" }),
	addNetworkStateListener: () => ({ remove: () => {} }),
}));
vi.mock("./api", () => ({ ApiError: class extends Error {}, getSessions: state.sessions, getNotifications: state.notifications }));
vi.mock("./resolveConfig", () => ({ resolveActiveConfig: state.resolve, runtimeResolveDeps: () => ({}) }));
vi.mock("./race", () => ({ IncompatibleHostVersionError: class extends Error {} }));
vi.mock("./connectRuntime", () => ({ rejectedEndpointNeedsRace: () => false }));
vi.mock("./hosts", () => ({
	activeHost: async () => ({ id: "a", name: "A", endpoints: [], token: "secret" }),
	loadHosts: async () => [], sameHostConnections: (x: unknown, y: unknown) => JSON.stringify(x) === JSON.stringify(y),
	setActiveHost: vi.fn(),
}));
vi.mock("./account", () => ({ loadAccount: async () => null }));
vi.mock("./cloud/CloudProvider", () => ({ useCloud: () => ({ org: null, projects: [], sessions: [], connections: [], chatHarnesses: [], client: {}, upsertSession: () => {}, refreshSessions: async () => {}, refresh: async () => {} }) }));
vi.mock("./accountHosts", () => ({ syncAccountHosts: vi.fn(), cancelAccountHostSync: vi.fn() }));
vi.mock("./installId", () => ({ primeInstallId: vi.fn() }));
vi.mock("./telemetry/runtime", () => ({ mobileTelemetry: () => null, trackFeature: vi.fn() }));
vi.mock("./chat/conversationEvents", () => ({ useConversationEventTransport: () => {} }));
vi.mock("./otherHosts", async (importOriginal) => ({ ...(await importOriginal<typeof import("./otherHosts")>()), useOtherHosts: () => ({ snapshots: {}, refreshHost: vi.fn() }) }));

import { sessionRouteView } from "./session/sessionRoute";
import { AppProvider, useApp } from "./store";

const lan: ServerConfig = { hostId: "a", host: "192.168.1.20", httpPort: "3011", muxPort: "3011", password: "secret", endpointKind: "lan" };
const tunnel: ServerConfig = { hostId: "a", host: "a.trycloudflare.com", httpPort: "443", muxPort: "443", secure: true, password: "secret", endpointKind: "tunnel" };
const board: SessionsResponse = { projects: [], orchestrators: [], orchestratorId: null, stats: {}, sessions: [{
	id: "chat", projectId: "", status: "idle", mode: "chat", branch: null, issueId: null,
	issueTitle: null, userPrompt: null, displayName: "Chat", summary: null,
	createdAt: "2026-11-03T00:00:00Z", lastActivityAt: "2026-11-03T00:00:00Z",
}] };

// What app/session/[id].tsx decides on each render, recorded with the inputs
// that matter for "is this still the same screen".
type Frame = { view: string; machine: string; config: string | null };
let frames: Frame[] = [];
let renderer: ReactTestRenderer | undefined;
function OpenChat() {
	const app = useApp();
	const listed = app.sessions.find((row) => row.id === "chat");
	const view = sessionRouteView({ listed, paired: app.paired, connection: app.connection, loading: app.loading, lookup: { state: "pending" }, routeHostId: "a", currentHostId: app.currentHostId });
	frames.push({ view: view.kind, machine: app.machine, config: app.config?.endpointKind ?? null });
	return null;
}
function deferred<T>() {
	let resolve!: (value: T) => void;
	const promise = new Promise<T>((done) => { resolve = done; });
	return { promise, resolve };
}
const flush = () => act(async () => { await vi.advanceTimersByTimeAsync(0); });

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	vi.useFakeTimers(); vi.clearAllMocks(); frames = [];
	state.resolve.mockResolvedValue(lan);
	state.sessions.mockResolvedValue(board);
	state.notifications.mockResolvedValue({ notifications: [], unreadCount: 0 });
});
afterEach(async () => {
	await act(async () => { renderer?.unmount(); }); renderer = undefined;
	vi.useRealTimers(); vi.unstubAllGlobals();
});

it("keeps an open chat on its screen from leaving the app to the first poll after returning", async () => {
	await act(async () => { renderer = create(<AppProvider><OpenChat /></AppProvider>); });
	await flush();
	expect(frames.at(-1)).toEqual({ view: "screen", machine: "host.a", config: "lan" });
	const opened = frames.length - 1;

	await act(async () => { state.onState!("background"); });
	// The verified connection is gone, as it must be: a LAN address can belong
	// to another machine once the phone wakes.
	expect(frames.at(-1)?.config).toBeNull();

	// Back in the app, with the endpoint race still running.
	const race = deferred<ServerConfig>();
	state.resolve.mockReturnValueOnce(race.promise);
	await act(async () => { state.onState!("active"); });
	await flush();
	expect(frames.at(-1)?.config).toBeNull();

	// The race lands on another of the same machine's endpoints, then polls.
	await act(async () => { race.resolve(tunnel); });
	await flush();
	expect(frames.at(-1)).toEqual({ view: "screen", machine: "host.a", config: "tunnel" });

	const after = frames.slice(opened);
	expect(after.filter((frame) => frame.view !== "screen")).toEqual([]);
	expect(new Set(after.map((frame) => frame.machine))).toEqual(new Set(["host.a"]));
});

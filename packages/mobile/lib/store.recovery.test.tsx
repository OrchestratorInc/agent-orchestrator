import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { SessionsResponse } from "./api";
import type { ServerConfig } from "./config";

// The selected machine going away while the app is open, end to end through
// the real store: the failed poll races, the race finds nothing, and the
// reconnect loop has to notice the machine coming back. Its tries used to be
// 2 s, 4 s, 8 s, then every 15 s from when the connection dropped, all spent
// before the board even called the machine unreachable: one restarted then
// waited up to 15 s for the phone.
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
vi.mock("./connectRuntime", () => ({ endpointStillAnswers: async () => false, rejectedEndpointNeedsRace: () => false }));
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

import { AppProvider, useApp } from "./store";

const lan: ServerConfig = { hostId: "a", host: "192.168.1.20", httpPort: "3011", muxPort: "3011", password: "secret", endpointKind: "lan" };
const board: SessionsResponse = { projects: [], orchestrators: [], orchestratorId: null, stats: {}, sessions: [] };

let renderer: ReactTestRenderer | undefined;
let connected = false;
function Probe() {
	connected = useApp().config !== null;
	return null;
}
const flush = () => act(async () => { await vi.advanceTimersByTimeAsync(0); });
const advance = (ms: number) => act(async () => { await vi.advanceTimersByTimeAsync(ms); });
let up = true;

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	vi.useFakeTimers(); vi.clearAllMocks(); up = true;
	state.resolve.mockImplementation(async () => up ? lan : null);
	state.sessions.mockImplementation(async () => {
		if (!up) throw new TypeError("Network request failed");
		return board;
	});
	state.notifications.mockResolvedValue({ notifications: [], unreadCount: 0 });
});
afterEach(async () => {
	await act(async () => { renderer?.unmount(); }); renderer = undefined;
	vi.useRealTimers(); vi.unstubAllGlobals();
});

/** Takes the machine down and steps until the app has given up its connection. */
async function dropMachine() {
	await act(async () => { renderer = create(<AppProvider><Probe /></AppProvider>); });
	await flush();
	expect(connected).toBe(true);
	up = false;
	for (let waited = 0; connected; waited += 500) {
		expect(waited).toBeLessThan(60_000);
		await advance(500);
	}
}

it("reconnects within 2 s to a machine that comes back 25 s into an outage", async () => {
	await dropMachine();
	await advance(25_000);
	up = true;
	await advance(2_000);
	expect(connected).toBe(true);
});

it("tries a machine that stays off less often as the outage goes on", async () => {
	await dropMachine();
	const triesDuring = async (ms: number) => {
		const before = state.resolve.mock.calls.length;
		await advance(ms);
		return state.resolve.mock.calls.length - before;
	};
	expect(await triesDuring(60_000)).toBeLessThanOrEqual(30);
	await triesDuring(240_000);
	expect(await triesDuring(600_000)).toBeLessThanOrEqual(40);
	expect(connected).toBe(false);
});

it("keeps an incompatible machine on the slow pace: it answered", async () => {
	const { IncompatibleHostVersionError } = await import("./race");
	state.resolve.mockImplementation(async () => { throw new IncompatibleHostVersionError("update"); });
	await act(async () => { renderer = create(<AppProvider><Probe /></AppProvider>); });
	await flush();
	const before = state.resolve.mock.calls.length;
	await advance(60_000);
	expect(state.resolve.mock.calls.length - before).toBeLessThanOrEqual(4);
});

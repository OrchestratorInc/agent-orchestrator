import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { SessionsResponse } from "./api";

// The saved board (see boardSnapshot) as the store writes and shows it.
const state = vi.hoisted(() => ({
	onState: undefined as ((value: string) => void) | undefined,
	resolve: vi.fn(), sessions: vi.fn(), notifications: vi.fn(), selected: "a",
	items: new Map<string, string>(),
}));
vi.mock("./cloud/CloudProvider", () => ({ useCloud: () => ({ org: null, projects: [], sessions: [], connections: [], chatHarnesses: [], client: {}, upsertSession: () => {}, refreshSessions: async () => {}, refresh: async () => {} }) }));
vi.mock("@react-native-async-storage/async-storage", () => ({ default: {
	getItem: async (key: string) => state.items.get(key) ?? null,
	setItem: async (key: string, value: string) => { state.items.set(key, value); },
	removeItem: async (key: string) => { state.items.delete(key); },
} }));
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
	activeHost: async () => ({ id: state.selected, name: state.selected, endpoints: [], token: "secret" }),
	loadHosts: async () => [], sameHostConnections: (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b),
	setActiveHost: async (id: string) => { state.selected = id; },
}));
vi.mock("./account", () => ({ loadAccount: async () => null }));
vi.mock("./accountHosts", () => ({ syncAccountHosts: vi.fn(), cancelAccountHostSync: vi.fn() }));
vi.mock("./installId", () => ({ primeInstallId: vi.fn() }));
vi.mock("./telemetry/runtime", () => ({ mobileTelemetry: () => null, trackFeature: vi.fn() }));
vi.mock("./chat/conversationEvents", () => ({ useConversationEventTransport: () => {} }));
vi.mock("./otherHosts", async (importOriginal) => ({ ...(await importOriginal<typeof import("./otherHosts")>()), useOtherHosts: () => ({ snapshots: {}, refreshHost: vi.fn() }) }));

import { boardKey, serializeBoard } from "./boardSnapshot";
import { AppProvider, useApp } from "./store";

let current!: ReturnType<typeof useApp>;
let renderer: ReactTestRenderer | undefined;
function Harness() { current = useApp(); return null; }
function board(title: string, projects: SessionsResponse["projects"] = []): SessionsResponse {
	return { projects, orchestrators: [], orchestratorId: null, stats: {}, sessions: [{
		id: `${title}-1`, projectId: "", status: "idle", mode: "chat", branch: null, issueId: null,
		issueTitle: null, userPrompt: null, displayName: title, summary: null,
		createdAt: "2026-10-11T00:00:00Z", lastActivityAt: "2026-10-11T00:00:00Z",
	}] };
}
function saveBoard(machine: string, answer: SessionsResponse) {
	state.items.set(boardKey(machine), serializeBoard({
		machine, savedAt: Date.now(), sessions: answer.sessions, orchestrators: answer.orchestrators,
		projects: answer.projects, archive: null, stats: {}, notificationsUnread: 0,
	})!);
}
async function mount() { await act(async () => { renderer = create(<AppProvider><Harness /></AppProvider>); }); }
// B is paired but never answers.
const configFor = (id: string) => id === "b" ? null : { hostId: id, host: `${id}.test`, httpPort: "3011", muxPort: "3011", password: "secret" };

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	vi.useFakeTimers(); vi.clearAllMocks(); state.selected = "a"; state.items.clear();
	state.resolve.mockImplementation(async () => configFor(state.selected));
	state.sessions.mockResolvedValue(board("Live A", [{ id: "a-app", name: "a-app" }]));
	state.notifications.mockResolvedValue({ notifications: [], unreadCount: 0 });
});
afterEach(async () => {
	await act(async () => { renderer?.unmount(); }); renderer = undefined;
	vi.useRealTimers(); vi.unstubAllGlobals();
});

it("saves the board before the unread count answers, so leaving the app mid-request still keeps it", async () => {
	state.notifications.mockReturnValue(new Promise(() => {}));
	await mount();
	expect(current.sessions[0]?.displayName).toBe("Live A");
	await act(async () => { state.onState!("background"); });
	await act(async () => { await vi.advanceTimersByTimeAsync(0); });
	expect(JSON.parse(state.items.get(boardKey("host.a")) ?? "null")?.sessions[0].displayName).toBe("Live A");
});

it("shows A's saved board again after visiting a machine that never answered", async () => {
	await mount();
	saveBoard("host.a", board("Saved A"));
	await act(async () => { await current.switchHost("b"); });
	expect(current.sessions).toEqual([]);
	// Back on A, whose poll has not answered yet: its saved board shows.
	state.sessions.mockReturnValue(new Promise(() => {}));
	await act(async () => { await current.switchHost("a"); });
	await act(async () => { await vi.advanceTimersByTimeAsync(0); });
	expect(current.sessions[0]?.displayName).toBe("Saved A");
});

it("shows another machine's saved projects rather than keeping the last machine's", async () => {
	saveBoard("host.b", board("Saved B", [{ id: "b-app", name: "b-app" }]));
	await mount();
	expect(current.projects.map((project) => project.id)).toEqual(["a-app"]);
	await act(async () => { await current.switchHost("b"); });
	await act(async () => { await vi.advanceTimersByTimeAsync(0); });
	expect(current.projects.map((project) => project.id)).toEqual(["b-app"]);
});

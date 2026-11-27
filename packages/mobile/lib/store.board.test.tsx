import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { SessionsResponse } from "./api";

// The saved board (see boardSnapshot) as the store writes and shows it.
const state = vi.hoisted(() => ({
	onState: undefined as ((value: string) => void) | undefined,
	resolve: vi.fn(), sessions: vi.fn(), notifications: vi.fn(), selected: "a",
	items: new Map<string, string>(),
	/** When set, the next read of a saved board waits until `releaseBoardRead` is called. */
	holdNextBoardRead: false,
	releaseBoardRead: null as null | (() => void),
	/** The machines paired on this phone, as loadHosts reads them. */
	paired: [] as Array<{ id: string; name: string; endpoints: never[]; token: string }>,
}));
vi.mock("./cloud/CloudProvider", () => ({ useCloud: () => ({ org: null, projects: [], sessions: [], connections: [], chatHarnesses: [], client: {}, upsertSession: () => {}, refreshSessions: async () => {}, refresh: async () => {} }) }));
vi.mock("@react-native-async-storage/async-storage", () => ({ default: {
	getItem: async (key: string) => {
		if (state.holdNextBoardRead && key.startsWith("ao.board.")) {
			state.holdNextBoardRead = false;
			await new Promise<void>((resolve) => { state.releaseBoardRead = resolve; });
		}
		return state.items.get(key) ?? null;
	},
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
	loadHosts: async () => state.paired, sameHostConnections: (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b),
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
// B is paired but never answers. C answers on a connection that names no machine.
const configFor = (id: string) => id === "b" ? null : { ...(id === "c" ? {} : { hostId: id }), host: `${id}.test`, httpPort: "3011", muxPort: "3011", password: "secret" };

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	vi.useFakeTimers(); vi.clearAllMocks(); state.selected = "a"; state.items.clear(); state.holdNextBoardRead = false; state.releaseBoardRead = null; state.paired = [];
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

// A poll whose /projects failed keeps the list it had on screen; the board
// saved for the next launch must keep it too, not null over it.
it("saves the projects it kept when a poll could not load them", async () => {
	await mount();
	expect(current.projects.map((project) => project.id)).toEqual(["a-app"]);
	state.sessions.mockResolvedValue({ ...board("Live A"), projects: null });
	await act(async () => { await current.refresh(); });
	expect(current.projects.map((project) => project.id)).toEqual(["a-app"]);
	await act(async () => { state.onState!("background"); });
	await act(async () => { await vi.advanceTimersByTimeAsync(0); });
	expect(JSON.parse(state.items.get(boardKey("host.a")) ?? "null")?.projects).toEqual([{ id: "a-app", name: "a-app" }]);
});

// The first poll since launch answered before the saved board was read, and
// its /projects failed: the saved list stays, on screen and on the phone,
// instead of a background flush saving null over it.
it("keeps the saved projects when the first poll could not load them", async () => {
	saveBoard("host.a", board("Saved A", [{ id: "a-app", name: "a-app" }]));
	state.holdNextBoardRead = true;
	state.sessions.mockResolvedValue({ ...board("Live A"), projects: null });
	await mount();
	await act(async () => { await vi.advanceTimersByTimeAsync(0); });
	expect(current.sessions[0]?.displayName).toBe("Live A");
	expect(current.projects.map((project) => project.id)).toEqual(["a-app"]);
	await act(async () => { state.onState!("background"); });
	await act(async () => { await vi.advanceTimersByTimeAsync(0); });
	expect(JSON.parse(state.items.get(boardKey("host.a")) ?? "null")?.projects).toEqual([{ id: "a-app", name: "a-app" }]);
	await act(async () => { state.releaseBoardRead?.(); });
});

// Shown and forgotten by the paired machine's id: one saved under its address
// instead would never be shown, nor deleted when the machine is forgotten.
it("keeps no board for a connection that names no machine", async () => {
	state.selected = "c";
	await mount();
	expect(current.sessions[0]?.displayName).toBe("Live A");
	await act(async () => { state.onState!("background"); });
	await act(async () => { await vi.advanceTimersByTimeAsync(0); });
	expect([...state.items.keys()].filter((key) => key.startsWith("ao.board."))).toEqual([]);
});

// The phone closed the app in the background, and it is launched again with
// the machine out of reach: the last board shows, dated, until a poll answers,
// so the chats on it can still be opened from what is saved.
it("shows the saved board on a cold launch with the machine unreachable", async () => {
	state.selected = "b";
	state.paired = [{ id: "b", name: "b", endpoints: [], token: "secret" }];
	const savedAt = Date.now() - 10 * 60_000;
	saveBoard("host.b", board("Saved B", [{ id: "b-app", name: "b-app" }]));
	state.items.set(boardKey("host.b"), JSON.stringify({ ...JSON.parse(state.items.get(boardKey("host.b"))!), savedAt }));
	await mount();
	await act(async () => { await vi.advanceTimersByTimeAsync(0); });
	expect(current.config).toBeNull();
	expect(current.sessions.map((session) => session.displayName)).toEqual(["Saved B"]);
	expect(current.allSessions.map((session) => session.displayName)).toEqual(["Saved B"]);
	expect(current.hostStates.map((host) => [host.hostId, host.sessions.length])).toEqual([["b", 1]]);
	expect(current.allProjects.map((project) => project.id)).toEqual(["b-app"]);
	// The freshness line says how old it is, for this machine only.
	expect(current.hostStates[0].lastSyncAt).toBe(savedAt);
	expect(current.getPrimaryLastSyncAt("b")).toBe(savedAt);
	expect(current.getPrimaryLastSyncAt("a")).toBeUndefined();
	// Still there while the app keeps racing for the machine and gives up on it.
	await act(async () => { await vi.advanceTimersByTimeAsync(3 * 60_000); });
	expect(state.resolve.mock.calls.length).toBeGreaterThan(1);
	expect(current.sessions.map((session) => session.displayName)).toEqual(["Saved B"]);
	expect(current.getPrimaryLastSyncAt("b")).toBe(savedAt);
	expect(state.sessions).not.toHaveBeenCalled();
});

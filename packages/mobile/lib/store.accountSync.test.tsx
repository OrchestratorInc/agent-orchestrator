import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

const state = vi.hoisted(() => ({
	resolveConfig: vi.fn(), sync: vi.fn(), cancel: vi.fn(),
	onState: undefined as ((state: string) => void) | undefined,
	finishSync: undefined as (() => void) | undefined,
	hosts: [] as import("./hosts").Host[],
}));
vi.mock("@react-native-async-storage/async-storage", () => ({ default: { getItem: async () => null, setItem: async () => {} } }));
vi.mock("react-native", () => ({ AppState: { currentState: "active", addEventListener: (_: string, callback: (value: string) => void) => {
	state.onState = callback; return { remove: () => {} };
} } }));
vi.mock("expo-network", () => ({ getNetworkStateAsync: async () => ({ isConnected: true, isInternetReachable: true, type: "WIFI" }), addNetworkStateListener: () => ({ remove: () => {} }) }));
vi.mock("./api", () => ({ ApiError: class extends Error {}, getSessions: vi.fn(), getNotifications: vi.fn() }));
vi.mock("./resolveConfig", () => ({ resolveActiveConfig: state.resolveConfig, runtimeResolveDeps: () => ({}) }));
vi.mock("./config", () => ({ isConfigured: () => false, machineIdentity: () => "" }));
vi.mock("./race", () => ({ IncompatibleHostVersionError: class extends Error {} }));
vi.mock("./connectRuntime", () => ({ rejectedEndpointNeedsRace: () => false }));
vi.mock("./hosts", () => ({ activeHost: async () => state.hosts[0] ?? null, loadHosts: async () => state.hosts, sameHostConnections: (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b), setActiveHost: vi.fn() }));
vi.mock("./account", () => ({ loadAccount: async () => ({ id: "user-a", email: "person@example.com" }) }));
vi.mock("./accountHosts", () => ({ syncAccountHosts: state.sync, cancelAccountHostSync: state.cancel }));
vi.mock("./installId", () => ({ primeInstallId: vi.fn() }));
vi.mock("./telemetry/runtime", () => ({ mobileTelemetry: { track: vi.fn() }, trackFeature: vi.fn() }));
vi.mock("./chat/conversationEvents", () => ({ useConversationEventTransport: () => {} }));
vi.mock("./otherHosts", () => ({ useOtherHosts: () => ({ snapshots: {}, refreshHost: vi.fn() }) }));

import { AppProvider } from "./store";
let renderer: ReactTestRenderer | undefined;
beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	vi.clearAllMocks();
	state.resolveConfig.mockResolvedValue(null);
	state.hosts = [];
	state.sync.mockImplementation(() => new Promise<void>((resolve) => { state.finishSync = resolve; }));
});
afterEach(async () => {
	await act(async () => { renderer?.unmount(); state.finishSync?.(); });
	vi.unstubAllGlobals();
});

it("loads saved pairings on launch without waiting for account network sync", async () => {
	await act(async () => { renderer = create(<AppProvider><></></AppProvider>); });
	expect(state.resolveConfig).toHaveBeenCalledTimes(1);
	expect(state.sync).toHaveBeenCalledTimes(1);
});

it("resolves saved pairings immediately on resume and cancels background account requests", async () => {
	await act(async () => { renderer = create(<AppProvider><></></AppProvider>); });
	await act(async () => { state.onState!("background"); });
	expect(state.cancel).toHaveBeenCalled();
	state.resolveConfig.mockClear();
	await act(async () => { state.onState!("active"); });
	expect(state.resolveConfig).toHaveBeenCalledTimes(1);
});

const host = { id: "h_one", name: "One", platform: "darwin", endpoints: [], token: "old", lastConnected: 1, accountUserId: "user-a" };
const config = { hostId: host.id, host: "host-one", password: "old", httpPort: "3011", muxPort: "3011" };

it("re-resolves the live connection when sync removes the selected host", async () => {
	state.hosts = [host];
	state.resolveConfig.mockResolvedValue(config);
	await act(async () => { renderer = create(<AppProvider><></></AppProvider>); });
	state.hosts = [];
	state.resolveConfig.mockResolvedValue(null);
	await act(async () => { state.finishSync!(); });
	expect(state.resolveConfig).toHaveBeenCalledTimes(2);
});

it("re-resolves rotated credentials without reconnecting unchanged syncs", async () => {
	state.hosts = [host];
	state.resolveConfig.mockResolvedValue(config);
	await act(async () => { renderer = create(<AppProvider><></></AppProvider>); });
	state.hosts = [{ ...host, token: "new" }];
	state.resolveConfig.mockResolvedValue({ ...config, password: "new" });
	await act(async () => { state.finishSync!(); });
	expect(state.resolveConfig).toHaveBeenCalledTimes(2);
});

it("keeps the live connection on an unchanged account sync", async () => {
	state.hosts = [host];
	state.resolveConfig.mockResolvedValue(config);
	await act(async () => { renderer = create(<AppProvider><></></AppProvider>); });
	await act(async () => { state.finishSync!(); });
	expect(state.resolveConfig).toHaveBeenCalledTimes(1);
});

it("does not restart a launch race that is still in flight when sync finishes", async () => {
	state.hosts = [host];
	let finishRace!: (value: typeof config) => void;
	state.resolveConfig.mockImplementation(() => new Promise((resolve) => { finishRace = resolve; }));
	await act(async () => { renderer = create(<AppProvider><></></AppProvider>); });
	await act(async () => { state.finishSync!(); });
	expect(state.resolveConfig).toHaveBeenCalledTimes(1);
	await act(async () => { finishRace(config); });
});

it("leaves an unreachable selected host to the reconnect timer on unchanged syncs", async () => {
	state.hosts = [host];
	state.resolveConfig.mockResolvedValue(null);
	await act(async () => { renderer = create(<AppProvider><></></AppProvider>); });
	await act(async () => { state.finishSync!(); });
	expect(state.resolveConfig).toHaveBeenCalledTimes(1);
});

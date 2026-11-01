import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({
	plain: new Map<string, string>(), secure: new Map<string, string>(),
	listeners: new Set<(state: string) => void>(), appState: { currentState: "active" },
}));
vi.mock("@react-native-async-storage/async-storage", () => ({ default: {
	getItem: async (key: string) => fixtures.plain.get(key) ?? null,
	setItem: async (key: string, value: string) => void fixtures.plain.set(key, value),
	removeItem: async (key: string) => void fixtures.plain.delete(key),
} }));
vi.mock("expo-secure-store", () => ({
	getItemAsync: async (key: string) => fixtures.secure.get(key) ?? null,
	setItemAsync: async (key: string, value: string) => void fixtures.secure.set(key, value),
	deleteItemAsync: async (key: string) => void fixtures.secure.delete(key),
}));
vi.mock("expo-constants", () => ({ default: {} }));
vi.mock("expo-device", () => ({ isDevice: true }));
vi.mock("expo-notifications", () => ({ setNotificationHandler: vi.fn() }));
vi.mock("expo-router", () => ({ useRouter: () => ({}), useRootNavigationState: () => null }));
vi.mock("./pageNavigation", () => ({ useOpenPage: () => vi.fn() }));
vi.mock("./store", () => ({ useApp: () => ({ hostStates: [] }) }));
vi.mock("./telemetry/runtime", () => ({ mobileTelemetry: () => null }));
vi.mock("./api", () => ({ ApiError: class extends Error {}, unpairFromDaemon: vi.fn(async () => {}) }));
vi.mock("react-native", () => ({ Platform: { OS: "ios" }, Linking: {}, AppState: {
	get currentState() { return fixtures.appState.currentState; },
	addEventListener: (_: string, listener: (state: string) => void) => {
		fixtures.listeners.add(listener);
		return { remove: () => fixtures.listeners.delete(listener) };
	},
} }));

import { PushManager } from "./PushManager";
import { unpairFromDaemon } from "./api";
import { queueMachineUnpair } from "./pendingUnpair";
import { retryPendingUnpairs } from "./push";

let renderer: ReactTestRenderer | undefined;
beforeEach(async () => {
	await retryPendingUnpairs();
	fixtures.plain.clear(); fixtures.secure.clear(); vi.clearAllMocks();
	fixtures.appState.currentState = "active";
	vi.useFakeTimers();
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	vi.stubGlobal("fetch", vi.fn(async () => ({ ok: true, json: async () => ({ hostId: "h_a", apiVersion: 1 }) })));
	const error = console.error;
	vi.spyOn(console, "error").mockImplementation((...args) => {
		if (!String(args[0]).startsWith("react-test-renderer is deprecated")) error(...args);
	});
	await queueMachineUnpair({ id: "h_a", name: "A", platform: "linux", token: "secret", lastConnected: 1,
		endpoints: [{ kind: "lan", host: "192.168.1.42", port: 3011, secure: false }] }, "phone-id");
});
afterEach(async () => {
	if (renderer) await act(async () => renderer?.unmount());
	renderer = undefined;
	await retryPendingUnpairs();
	vi.restoreAllMocks(); vi.unstubAllGlobals(); vi.useRealTimers();
});

describe("unpair retry lifecycle with zero paired machines", () => {
	it("resumes durable cleanup on launch, even on onboarding", async () => {
		await act(async () => { renderer = create(<PushManager />); await vi.advanceTimersByTimeAsync(0); });
		expect(unpairFromDaemon).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ hostId: "h_a", password: "secret" }), "phone-id");
		expect(fixtures.secure.size).toBe(0);
	});

	it.each(["foreground", "timer"])("retries failed cleanup on %s without a remaining host connection", async (trigger) => {
		vi.mocked(fetch).mockRejectedValue(new Error("offline"));
		await act(async () => { renderer = create(<PushManager />); await vi.advanceTimersByTimeAsync(0); });
		expect(fixtures.secure.size).toBeGreaterThan(0);
		vi.mocked(fetch).mockResolvedValue({ ok: true, json: async () => ({ hostId: "h_a", apiVersion: 1 }) } as Response);
		await act(async () => {
			if (trigger === "foreground") for (const listener of fixtures.listeners) listener("active");
			else await vi.advanceTimersByTimeAsync(30_000);
			await vi.advanceTimersByTimeAsync(0);
		});
		expect(unpairFromDaemon).toHaveBeenCalledOnce();
		expect(fixtures.secure.size).toBe(0);
	});

	it("does not start periodic cleanup while the app is backgrounded", async () => {
		fixtures.appState.currentState = "background";
		await act(async () => { renderer = create(<PushManager />); await vi.advanceTimersByTimeAsync(60_000); });
		expect(fetch).not.toHaveBeenCalled();
		expect(fixtures.secure.size).toBeGreaterThan(0);
	});
});

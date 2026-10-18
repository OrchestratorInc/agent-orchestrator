import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { ServerConfig } from "../config";

const fixture = vi.hoisted(() => ({
	focused: true, appState: "active", request: vi.fn(),
	listener: undefined as ((state: string) => void) | undefined,
}));
vi.mock("expo-router", async () => {
	const { useEffect } = await import("react");
	return { useFocusEffect: (effect: () => void) => useEffect(() => fixture.focused ? effect() : undefined, [effect, fixture.focused]) };
});
vi.mock("react-native", () => ({ AppState: {
	get currentState() { return fixture.appState; },
	addEventListener: (_: string, callback: (state: string) => void) => { fixture.listener = callback; return { remove: () => { fixture.listener = undefined; } }; },
} }));
vi.mock("../api", () => ({ apiRequest: fixture.request }));
import { useChatViewLease } from "./useChatViewLease";

const config = { hostId: "host-a", host: "a.test", httpPort: "3011", muxPort: "", password: "password" } satisfies ServerConfig;
let renderer: ReactTestRenderer | undefined;
function Harness({ cfg = config, id = "session/a" }: { cfg?: ServerConfig | null; id?: string }) { useChatViewLease(cfg, id); return null; }
function requests() { return fixture.request.mock.calls.map(([cfg, path, init]) => ({ host: cfg.hostId, path, ...JSON.parse(init.body) })); }
beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	vi.useFakeTimers();
	fixture.focused = true; fixture.appState = "active"; fixture.listener = undefined;
	fixture.request.mockReset().mockResolvedValue({ status: 204 });
});
afterEach(async () => { await act(async () => renderer?.unmount()); vi.useRealTimers(); vi.unstubAllGlobals(); });

it("registers a focused chat, renews its 30-second lease and releases it on blur", async () => {
	await act(async () => { renderer = create(<Harness />); });
	expect(requests()).toEqual([{ host: "host-a", path: "/api/v1/sessions/session%2Fa/chat-view", active: true, viewId: expect.any(String) }]);
	const viewId = requests()[0].viewId;
	await act(async () => { await vi.advanceTimersByTimeAsync(15_000); });
	expect(requests()).toHaveLength(2);
	expect(requests()[1].viewId).toBe(viewId);
	fixture.focused = false;
	await act(async () => { renderer!.update(<Harness />); });
	expect(requests().at(-1)).toMatchObject({ active: false, viewId });
	const count = requests().length;
	await act(async () => { await vi.advanceTimersByTimeAsync(45_000); });
	expect(requests()).toHaveLength(count);
});

it("releases the lease in background and renews it when the app becomes active", async () => {
	await act(async () => { renderer = create(<Harness />); });
	await act(async () => { fixture.listener?.("background"); });
	expect(requests().at(-1)).toMatchObject({ active: false });
	const count = requests().length;
	await act(async () => { await vi.advanceTimersByTimeAsync(45_000); });
	expect(requests()).toHaveLength(count);
	await act(async () => { fixture.listener?.("active"); });
	expect(requests().at(-1)).toMatchObject({ active: true });
});

it("does not register an unpaired chat", async () => {
	await act(async () => { renderer = create(<Harness cfg={null} />); });
	expect(requests()).toEqual([]);
});

it("releases the old host lease before registering a different host", async () => {
	await act(async () => { renderer = create(<Harness />); });
	await act(async () => { renderer!.update(<Harness cfg={{ ...config, hostId: "host-b", host: "b.test" }} />); });
	expect(requests()).toMatchObject([{ host: "host-a", active: true }, { host: "host-a", active: false }, { host: "host-b", active: true }]);
});

it("treats an older daemon without chat-view support as best-effort", async () => {
	fixture.request.mockRejectedValue(new Error("404 unsupported"));
	await act(async () => { renderer = create(<Harness />); });
	await act(async () => { await vi.advanceTimersByTimeAsync(15_000); });
	expect(requests()).toHaveLength(2);
});

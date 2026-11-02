import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { ServerConfig } from "../config";

const fixture = vi.hoisted(() => ({
	focused: true, appState: "active", request: vi.fn(),
	routes: [{ key: "chat", name: "session/[id]" }] as Array<{ key: string; name: string }>,
	listener: undefined as ((state: string) => void) | undefined,
	navigationListeners: new Set<() => void>(),
}));
vi.mock("expo-router", () => ({
	useIsFocused: () => fixture.focused,
	useRoute: () => ({ key: "chat" }),
	useNavigation: () => navigation,
}));
vi.mock("react-native", () => ({ AppState: {
	get currentState() { return fixture.appState; },
	addEventListener: (_: string, callback: (state: string) => void) => { fixture.listener = callback; return { remove: () => { fixture.listener = undefined; } }; },
} }));
vi.mock("../api", () => ({ apiRequest: fixture.request }));
import { coveredOnlyBySheets, useChatViewLease } from "./useChatViewLease";

const navigation = {
	getState: () => ({ routes: fixture.routes }),
	addListener: (_: string, callback: () => void) => {
		fixture.navigationListeners.add(callback);
		return () => { fixture.navigationListeners.delete(callback); };
	},
};
const config = { hostId: "host-a", host: "a.test", httpPort: "3011", muxPort: "", password: "password" } satisfies ServerConfig;
let renderer: ReactTestRenderer | undefined;
function Harness({ cfg = config, id = "session/a", onViewing }: { cfg?: ServerConfig | null; id?: string; onViewing?: () => void }) {
	useChatViewLease(cfg, id, onViewing);
	return null;
}
function requests() { return fixture.request.mock.calls.map(([cfg, path, init]) => ({ host: cfg.hostId, path, ...JSON.parse(init.body) })); }
/** Push or pop a route over the chat, the way a sheet or another screen arrives. */
async function navigate(routes: Array<{ key: string; name: string }>, focused: boolean) {
	fixture.routes = routes;
	fixture.focused = focused;
	await act(async () => {
		for (const listener of fixture.navigationListeners) listener();
		renderer!.update(<Harness />);
	});
}
beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	vi.useFakeTimers();
	fixture.focused = true; fixture.appState = "active"; fixture.listener = undefined;
	fixture.routes = [{ key: "chat", name: "session/[id]" }];
	fixture.navigationListeners.clear();
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
	await navigate([{ key: "chat", name: "session/[id]" }, { key: "other", name: "session/[id]" }], false);
	expect(requests().at(-1)).toMatchObject({ active: false, viewId });
	const count = requests().length;
	await act(async () => { await vi.advanceTimersByTimeAsync(45_000); });
	expect(requests()).toHaveLength(count);
});

it("keeps the lease while a chat sheet covers the chat, without registering it again", async () => {
	await act(async () => { renderer = create(<Harness />); });
	const viewId = requests()[0].viewId;
	await navigate([{ key: "chat", name: "session/[id]" }, { key: "sheet", name: "sheets/chat-settings" }], false);
	await act(async () => { await vi.advanceTimersByTimeAsync(45_000); });
	expect(requests().every((request) => request.active && request.viewId === viewId)).toBe(true);
	expect(requests().length).toBeGreaterThan(2);
	await navigate([{ key: "chat", name: "session/[id]" }], true);
	expect(requests().every((request) => request.active && request.viewId === viewId)).toBe(true);
});

it("keeps the lease when the chat renders blurred before the stack listeners run", async () => {
	await act(async () => { renderer = create(<Harness />); });
	fixture.routes = [{ key: "chat", name: "session/[id]" }, { key: "sheet", name: "sheets/chat-settings" }];
	fixture.focused = false;
	await act(async () => { renderer!.update(<Harness />); });
	expect(requests().map((request) => request.active)).toEqual([true]);
});

it("does not report a registration answered after the chat stopped being viewed", async () => {
	const onViewing = vi.fn();
	let answer!: () => void;
	fixture.request.mockImplementationOnce(() => new Promise((resolve) => { answer = () => resolve({ status: 204 }); }));
	await act(async () => { renderer = create(<Harness onViewing={onViewing} />); });
	await act(async () => renderer!.unmount());
	renderer = undefined;
	await act(async () => { answer(); });
	expect(requests().map((request) => request.active)).toEqual([true, false]);
	expect(onViewing).not.toHaveBeenCalled();
});

it("releases the lease when a screen opens from a sheet over the chat", async () => {
	await act(async () => { renderer = create(<Harness />); });
	await navigate([{ key: "chat", name: "session/[id]" }, { key: "sheet", name: "sheets/conversation-actions" }], false);
	await navigate([{ key: "chat", name: "session/[id]" }, { key: "sheet", name: "sheets/conversation-actions" }, { key: "shell", name: "shell/[handleId]" }], false);
	expect(requests().at(-1)).toMatchObject({ active: false });
});

it("reports each registration once it is answered, not each renewal", async () => {
	const onViewing = vi.fn();
	let answer!: () => void;
	fixture.request.mockImplementationOnce(() => new Promise((resolve) => { answer = () => resolve({ status: 204 }); }));
	await act(async () => { renderer = create(<Harness onViewing={onViewing} />); });
	expect(onViewing).not.toHaveBeenCalled();
	await act(async () => { answer(); });
	expect(onViewing).toHaveBeenCalledTimes(1);
	await act(async () => { await vi.advanceTimersByTimeAsync(30_000); });
	expect(onViewing).toHaveBeenCalledTimes(1);
	await act(async () => { fixture.listener?.("background"); });
	await act(async () => { fixture.listener?.("active"); });
	expect(onViewing).toHaveBeenCalledTimes(2);
});

it("reports the renewal that succeeds when the registration failed, once", async () => {
	const onViewing = vi.fn();
	fixture.request.mockRejectedValueOnce(new Error("request timed out"));
	await act(async () => { renderer = create(<Harness onViewing={onViewing} />); });
	expect(onViewing).not.toHaveBeenCalled();
	await act(async () => { await vi.advanceTimersByTimeAsync(15_000); });
	expect(requests()).toHaveLength(2);
	expect(onViewing).toHaveBeenCalledTimes(1);
	await act(async () => { await vi.advanceTimersByTimeAsync(30_000); });
	expect(onViewing).toHaveBeenCalledTimes(1);
});

it("does not report a slow answer from before the app went to the background", async () => {
	const onViewing = vi.fn();
	let answer!: () => void;
	fixture.request.mockImplementationOnce(() => new Promise((resolve) => { answer = () => resolve({ status: 204 }); }));
	await act(async () => { renderer = create(<Harness onViewing={onViewing} />); });
	await act(async () => { fixture.listener?.("background"); });
	await act(async () => { fixture.listener?.("active"); });
	fixture.request.mockRejectedValueOnce(new Error("release failed")).mockRejectedValueOnce(new Error("registration failed"));
	await act(async () => { answer(); });
	expect(requests().map((request) => request.active)).toEqual([true, false, true]);
	expect(onViewing).not.toHaveBeenCalled();
	await act(async () => { await vi.advanceTimersByTimeAsync(15_000); });
	expect(onViewing).toHaveBeenCalledTimes(1);
});

it("does not report a registration the daemon rejected", async () => {
	const onViewing = vi.fn();
	fixture.request.mockRejectedValue(new Error("404 unsupported"));
	await act(async () => { renderer = create(<Harness onViewing={onViewing} />); });
	expect(onViewing).not.toHaveBeenCalled();
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

it("counts the chat as covered only by sheets above it in its own stack", () => {
	const chat = { key: "chat", name: "session/[id]" };
	expect(coveredOnlyBySheets([chat], "chat")).toBe(true);
	expect(coveredOnlyBySheets([chat, { key: "s", name: "sheets/composer-picker" }], "chat")).toBe(true);
	expect(coveredOnlyBySheets([chat, { key: "p", name: "preview/[id]" }], "chat")).toBe(false);
	expect(coveredOnlyBySheets([{ key: "tabs", name: "(tabs)" }], "chat")).toBe(false);
	expect(coveredOnlyBySheets(undefined, "chat")).toBe(false);
});

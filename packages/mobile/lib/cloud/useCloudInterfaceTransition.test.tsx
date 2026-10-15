import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { SessionSource } from "../environment/types";
import type { CloudInterfaceStatus } from "./interfaceTransition";
const f = vi.hoisted(() => ({ focused: true, state: "active", listener: undefined as ((state: string) => void) | undefined }));
vi.mock("expo-router", async () => { const { useEffect } = await import("react"); return { useFocusEffect: (effect: () => void) => useEffect(() => f.focused ? effect() : undefined, [effect, f.focused]) }; });
vi.mock("react-native", () => ({ AppState: { get currentState() { return f.state; }, addEventListener: (_: string, callback: (state: string) => void) => { f.listener = callback; return { remove: () => {} }; } } }));
import { useCloudInterfaceTransition } from "./useCloudInterfaceTransition";
let renderer: ReactTestRenderer | undefined; let hook: ReturnType<typeof useCloudInterfaceTransition>;
const transition = { id: "h1", sessionId: "s1", sourceMode: "chat" as const, targetMode: "tui" as const, phase: "requested" as const, policy: "drain" as const, createdAt: "now", updatedAt: "now" };
function Harness({ source, sessionId = "s1", settled }: { source?: SessionSource; sessionId?: string; settled?: () => void }) { hook = useCloudInterfaceTransition(source, sessionId, settled); return null; }
beforeEach(() => { vi.useFakeTimers(); f.focused = true; f.state = "active"; vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true); vi.spyOn(console, "error").mockImplementation(() => {}); });
afterEach(async () => { await act(async () => renderer?.unmount()); renderer = undefined; vi.useRealTimers(); vi.restoreAllMocks(); vi.unstubAllGlobals(); });
it.each(["drain", "interrupt"] as const)("exposes the accepted %s policy until the start request settles", async (policy) => {
	let finish!: (result: { transition: typeof transition }) => void;
	const source = { kind: "cloud", scopeKey: "one", getInterfaceTransition: async () => ({ supported: true, targetMode: "tui" }), startInterfaceTransition: () => new Promise<{ transition: typeof transition }>((resolve) => { finish = resolve; }) } as unknown as SessionSource;
	await act(async () => { renderer = create(<Harness source={source} />); });
	let starting!: Promise<void>;
	await act(async () => { starting = hook.start("tui", policy); });
	expect(hook.starting).toBe(true); expect(hook.startingPolicy).toBe(policy);
	await act(async () => { await hook.start("tui", policy === "drain" ? "interrupt" : "drain"); });
	expect(hook.startingPolicy).toBe(policy);
	await act(async () => { finish({ transition }); await starting; });
	expect(hook.starting).toBe(false); expect(hook.startingPolicy).toBeUndefined();
});
it("clears the starting policy after a failed start", async () => {
	let reject!: (cause: Error) => void;
	const source = { kind: "cloud", scopeKey: "one", getInterfaceTransition: async () => ({ supported: true, targetMode: "tui" }), startInterfaceTransition: () => new Promise((_, fail) => { reject = fail; }) } as unknown as SessionSource;
	await act(async () => { renderer = create(<Harness source={source} />); });
	let failed!: Promise<unknown>;
	await act(async () => { failed = hook.start("tui", "drain").catch((cause: unknown) => cause); });
	expect(hook.startingPolicy).toBe("drain");
	const cause = new Error("Start failed");
	await act(async () => { reject(cause); expect(await failed).toBe(cause); });
	expect(hook.starting).toBe(false); expect(hook.startingPolicy).toBeUndefined(); expect(hook.error).toBeTruthy();
});
it.each(["background", "blur", "account", "session", "source", "unavailable"] as const)("clears the starting policy and ignores a late start after %s cleanup", async (cleanup) => {
	let finish!: (result: { transition: typeof transition }) => void;
	let signal!: AbortSignal;
	const source = { kind: "cloud", scopeKey: "one", getInterfaceTransition: async () => ({ supported: true, targetMode: "tui" }), startInterfaceTransition: (_: string, __: unknown, options: { signal: AbortSignal }) => { signal = options.signal; return new Promise<{ transition: typeof transition }>((resolve) => { finish = resolve; }); } } as unknown as SessionSource;
	await act(async () => { renderer = create(<Harness source={source} />); });
	let starting!: Promise<void>;
	await act(async () => { starting = hook.start("tui", "drain"); });
	expect(hook.startingPolicy).toBe("drain");
	await act(async () => {
		if (cleanup === "background") f.listener?.("background");
		else if (cleanup === "blur") { f.focused = false; renderer!.update(<Harness source={source} />); }
		else if (cleanup === "session") renderer!.update(<Harness source={source} sessionId="s2" />);
		else if (cleanup === "unavailable") renderer!.update(<Harness />);
		else renderer!.update(<Harness source={{ ...source, scopeKey: cleanup === "account" ? "two" : "one" }} />);
	});
	expect(signal.aborted).toBe(true); expect(hook.starting).toBe(false); expect(hook.startingPolicy).toBeUndefined();
	await act(async () => { finish({ transition }); await starting; });
	expect(hook.startingPolicy).toBeUndefined(); expect(hook.transition).toBeUndefined();
});
it("keeps ownership through early mode commit and pauses polling in background", async () => {
	let status: CloudInterfaceStatus = { supported: true, targetMode: "tui", currentMode: "chat", runtimeConnected: true };
	const getInterfaceTransition = vi.fn(async () => status);
	const startInterfaceTransition = vi.fn(async () => { status = { ...status, transition }; return { transition }; });
	const source = { kind: "cloud", scopeKey: "one", getInterfaceTransition, startInterfaceTransition } as unknown as SessionSource;
	const settled = vi.fn();
	await act(async () => { renderer = create(<Harness source={source} settled={settled} />); });
	await act(async () => { await hook.start("tui", "drain", { model: "chosen", reasoningEffort: "high" }); });
	expect(startInterfaceTransition).toHaveBeenCalledWith("s1", { targetMode: "tui", policy: "drain", model: "chosen", reasoningEffort: "high" }, expect.objectContaining({ signal: expect.any(AbortSignal) }));
	status = { ...status, currentMode: "tui", transition: { ...transition, phase: "target_starting" } };
	await act(async () => { await vi.advanceTimersByTimeAsync(1000); }); expect(settled).not.toHaveBeenCalled();
	await act(async () => { f.listener?.("background"); }); const count = getInterfaceTransition.mock.calls.length;
	await act(async () => { await vi.advanceTimersByTimeAsync(10_000); }); expect(getInterfaceTransition).toHaveBeenCalledTimes(count);
	status = { ...status, transition: { ...transition, phase: "completed" } };
	await act(async () => { f.listener?.("active"); }); expect(settled).toHaveBeenCalledOnce();
});
it("ignores a late previous-account status and forbids cancellation after source stopping", async () => {
	let resolve!: (status: CloudInterfaceStatus) => void;
	const old = { kind: "cloud", scopeKey: "old", getInterfaceTransition: () => new Promise<CloudInterfaceStatus>((done) => { resolve = done; }) } as unknown as SessionSource;
	await act(async () => { renderer = create(<Harness source={old} />); });
	const cancelInterfaceTransition = vi.fn();
	const next = { kind: "cloud", scopeKey: "new", getInterfaceTransition: async () => ({ supported: true, targetMode: "tui", transition: { ...transition, phase: "source_stopping" } }), cancelInterfaceTransition } as unknown as SessionSource;
	await act(async () => { renderer!.update(<Harness source={next} />); });
	await act(async () => { resolve({ supported: false, targetMode: "chat" }); });
	expect(hook.status?.supported).toBe(true);
	await act(async () => { await expect(hook.cancel()).rejects.toThrow(/can no longer be cancelled/i); }); expect(cancelInterfaceTransition).not.toHaveBeenCalled();
});
it("keeps a live handoff during network loss and retries without inventing a failure", async () => {
	let connected = true;
	let status: CloudInterfaceStatus = { supported: true, targetMode: "tui", currentMode: "chat", runtimeConnected: true, transition };
	const getInterfaceTransition = vi.fn(async () => { if (!connected) throw new Error("Network lost"); return status; });
	const source = { kind: "cloud", scopeKey: "one", getInterfaceTransition } as unknown as SessionSource;
	await act(async () => { renderer = create(<Harness source={source} />); });
	connected = false; await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
	expect(hook.transition?.phase).toBe("requested"); expect(hook.fetchFailed).toBe(true);
	const calls = getInterfaceTransition.mock.calls.length;
	connected = true; status = { ...status, currentMode: "tui", transition: { ...transition, phase: "completed" } };
	await act(async () => { await vi.advanceTimersByTimeAsync(999); }); expect(getInterfaceTransition).toHaveBeenCalledTimes(calls);
	await act(async () => { await vi.advanceTimersByTimeAsync(1); }); expect(hook.transition?.phase).toBe("completed"); expect(hook.fetchFailed).toBe(false);
});

import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { ServerConfig } from "../config";

const state = vi.hoisted(() => ({
	stream: vi.fn(),
	getItem: vi.fn(async () => "42"),
	setItem: vi.fn(async () => {}),
}));
vi.mock("./api", () => ({ streamGlobalConversationEvents: state.stream }));
vi.mock("../config", () => ({ isConfigured: () => true, machineIdentity: () => "qa-host" }));
vi.mock("@react-native-async-storage/async-storage", () => ({ default: { getItem: state.getItem, setItem: state.setItem, removeItem: vi.fn() } }));
import { subscribeConversationEvents, useConversationEventTransport } from "./conversationEvents";
import { HEAD_CURSOR } from "./eventCursor";
import { LISTENER_GRACE_MS } from "./sse";

const config = { host: "qa-host" } as ServerConfig;
let renderer: ReactTestRenderer | undefined;
function Harness() { useConversationEventTransport(config); return null; }

function streamUntilAborted() {
	return (_cfg: unknown, _after: number, signal: AbortSignal) =>
		new Promise((_resolve, reject) => signal.addEventListener("abort", () => reject(new Error("aborted"))));
}

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	vi.useFakeTimers();
	vi.clearAllMocks();
	state.stream.mockImplementation(streamUntilAborted());
});
afterEach(async () => {
	if (renderer) await act(async () => renderer!.unmount());
	renderer = undefined;
	// Let the registry's listener grace from the previous test run out.
	vi.advanceTimersByTime(LISTENER_GRACE_MS);
	vi.useRealTimers();
	vi.unstubAllGlobals();
});

it("starts at head while no chat is open, although a cursor is stored", async () => {
	await act(async () => { renderer = create(<Harness />); });
	expect(state.getItem).toHaveBeenCalled();
	expect(state.stream).toHaveBeenCalledTimes(1);
	expect(state.stream.mock.calls[0][1]).toBe(HEAD_CURSOR);
});

it("replays from the stored cursor while a chat is open, so the chat misses nothing", async () => {
	const unsubscribe = subscribeConversationEvents(config, "qa-session", () => {});
	try {
		await act(async () => { renderer = create(<Harness />); });
		expect(state.stream.mock.calls[0][1]).toBe(42);
	} finally { unsubscribe(); }
});

it("stores the head it started at, so the next start with a chat open replays only from there", async () => {
	state.stream.mockImplementationOnce((_cfg, _after, signal: AbortSignal, _onEvent, onReset: (cursor: number) => void) => {
		onReset(500);
		return streamUntilAborted()(_cfg, _after, signal);
	});
	await act(async () => { renderer = create(<Harness />); });
	expect(state.setItem).toHaveBeenCalledWith("ao.chat.events.qa-host", "500");
});

it("resumes a reconnect within one start from its cursor", async () => {
	state.stream.mockImplementationOnce(async (_cfg, _after, _signal, _onEvent, onReset: (cursor: number) => void) => {
		onReset(100);
		return 120;
	});
	await act(async () => { renderer = create(<Harness />); });
	await act(async () => { await vi.advanceTimersByTimeAsync(1_000); });
	expect(state.stream).toHaveBeenCalledTimes(2);
	expect(state.stream.mock.calls[1][1]).toBe(120);
});

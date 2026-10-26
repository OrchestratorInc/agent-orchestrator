import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { ServerConfig } from "../config";
import type { ConversationStreamOptions } from "./api";

const state = vi.hoisted(() => ({
	stream: vi.fn(),
	getItem: vi.fn(async () => "42"),
}));
vi.mock("./api", () => ({ streamGlobalConversationEvents: state.stream }));
vi.mock("../config", () => ({ isConfigured: () => true, machineIdentity: () => "qa-host" }));
vi.mock("@react-native-async-storage/async-storage", () => ({ default: { getItem: state.getItem, setItem: vi.fn(), removeItem: vi.fn() } }));
import { conversationStreamState, subscribeConversationEvents, useConversationEventTransport, watchConversationStream } from "./conversationEvents";
import { HEAD_CURSOR } from "./eventCursor";

const config = { host: "qa-host" } as ServerConfig;
let renderer: ReactTestRenderer | undefined;
function Harness() { useConversationEventTransport(config); return null; }

function untilAborted(signal: AbortSignal) {
	return new Promise((_resolve, reject) => signal.addEventListener("abort", () => reject(new Error("aborted"))));
}

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	vi.useFakeTimers();
	vi.clearAllMocks();
});
afterEach(async () => {
	if (renderer) await act(async () => renderer!.unmount());
	renderer = undefined;
	vi.useRealTimers();
	vi.unstubAllGlobals();
});

it("opens at head with a chat open, without reading a cursor an older build stored", async () => {
	state.stream.mockImplementation((_cfg, _after, signal: AbortSignal) => untilAborted(signal));
	const unsubscribe = subscribeConversationEvents(config, "qa-session", () => {});
	try {
		await act(async () => { renderer = create(<Harness />); });
		expect(state.stream.mock.calls[0][1]).toBe(HEAD_CURSOR);
		expect(state.getItem).not.toHaveBeenCalled();
	} finally { unsubscribe(); }
});

it("reports the stream open once the daemon accepts it, and clears it when it stops", async () => {
	let options!: ConversationStreamOptions;
	state.stream.mockImplementation((_cfg, _after, signal: AbortSignal, _onEvent, _onReset, opts: ConversationStreamOptions) => {
		options = opts;
		return untilAborted(signal);
	});
	const seen: Array<string | undefined> = [];
	const stop = watchConversationStream(config, (s) => seen.push(s));
	try {
		await act(async () => { renderer = create(<Harness />); });
		expect(conversationStreamState(config)).toBeUndefined();
		options.onOpen?.();
		expect(conversationStreamState(config)).toBe("open");
		await act(async () => renderer!.unmount());
		renderer = undefined;
		expect(seen).toEqual(["open", undefined]);
	} finally { stop(); }
});

it("reports a first attempt that fails, and stays open across reconnects after it opened", async () => {
	state.stream
		.mockImplementationOnce(async () => { throw new Error("401"); })
		.mockImplementationOnce(async (_cfg, _after, _signal, _onEvent, onReset: (cursor: number) => void, opts: ConversationStreamOptions) => {
			onReset(100);
			opts.onOpen?.();
			return 120;
		})
		.mockImplementation((_cfg, _after, signal: AbortSignal) => untilAborted(signal));
	await act(async () => { renderer = create(<Harness />); });
	expect(conversationStreamState(config)).toBe("failed");
	await act(async () => { await vi.advanceTimersByTimeAsync(1_000); });
	expect(conversationStreamState(config)).toBe("open");
	await act(async () => { await vi.advanceTimersByTimeAsync(2_000); });
	// A reconnect within one start resumes from its cursor; the state stays open.
	expect(state.stream).toHaveBeenCalledTimes(3);
	expect(state.stream.mock.calls[2][1]).toBe(120);
	expect(conversationStreamState(config)).toBe("open");
});

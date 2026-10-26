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
import { conversationStreamHealthy, STREAM_HEALTHY_WITHIN_MS, subscribeConversationEvents, useConversationEventTransport } from "./conversationEvents";
import { HEAD_CURSOR } from "./eventCursor";
import { STREAM_RESYNC_EVENT } from "./sse";

const config = { host: "qa-host" } as ServerConfig;
let renderer: ReactTestRenderer | undefined;
function Harness() { useConversationEventTransport(config); return null; }

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

it("opens at head, ignoring a cursor an older build stored, and asks listeners to reload once", async () => {
	let options!: ConversationStreamOptions;
	state.stream.mockImplementation((_cfg, _after, signal: AbortSignal, _onEvent, _onReset, opts: ConversationStreamOptions) => {
		options = opts;
		return new Promise((_resolve, reject) => signal.addEventListener("abort", () => reject(new Error("aborted"))));
	});
	const received: string[] = [];
	const unsubscribe = subscribeConversationEvents(config, "qa-session", (event) => received.push(event.type));
	try {
		await act(async () => { renderer = create(<Harness />); });
		expect(state.stream).toHaveBeenCalledTimes(1);
		expect(state.stream.mock.calls[0][1]).toBe(HEAD_CURSOR);
		options.onOpen?.();
		expect(received).toEqual([STREAM_RESYNC_EVENT]);
	} finally { unsubscribe(); }
});

it("resumes a reconnect within one start from its cursor without another reload", async () => {
	const received: string[] = [];
	const unsubscribe = subscribeConversationEvents(config, "qa-session", (event) => received.push(event.type));
	state.stream
		.mockImplementationOnce(async (_cfg, _after, _signal, _onEvent, onReset: (cursor: number) => void, opts: ConversationStreamOptions) => {
			onReset(100);
			opts.onOpen?.();
			return 120;
		})
		.mockImplementation((_cfg, _after, signal: AbortSignal, _onEvent, _onReset, opts: ConversationStreamOptions) => {
			opts.onOpen?.();
			return new Promise((_resolve, reject) => signal.addEventListener("abort", () => reject(new Error("aborted"))));
		});
	try {
		await act(async () => { renderer = create(<Harness />); });
		await act(async () => { await vi.advanceTimersByTimeAsync(1_000); });
		expect(state.stream).toHaveBeenCalledTimes(2);
		expect(state.stream.mock.calls[1][1]).toBe(120);
		expect(received).toEqual([STREAM_RESYNC_EVENT]);
	} finally { unsubscribe(); }
});

it("counts the stream healthy only while heartbeats arrive on time", async () => {
	let options!: ConversationStreamOptions;
	state.stream.mockImplementation((_cfg, _after, signal: AbortSignal, _onEvent, _onReset, opts: ConversationStreamOptions) => {
		options = opts;
		return new Promise((_resolve, reject) => signal.addEventListener("abort", () => reject(new Error("aborted"))));
	});
	await act(async () => { renderer = create(<Harness />); });
	expect(conversationStreamHealthy(config)).toBe(false);
	options.onHeartbeat?.();
	expect(conversationStreamHealthy(config)).toBe(true);
	vi.advanceTimersByTime(STREAM_HEALTHY_WITHIN_MS);
	expect(conversationStreamHealthy(config)).toBe(false);
	options.onHeartbeat?.();
	await act(async () => renderer!.unmount());
	renderer = undefined;
	expect(conversationStreamHealthy(config)).toBe(false);
});

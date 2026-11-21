import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { ServerConfig } from "../config";
import type { ConversationEvent } from "./sse";

// A streaming reply reaches the phone as one event per write, and the daemon
// writes a running reply about every 100 ms. A refresh that waited for the
// events to pause never came while the agent kept talking: the reply showed in
// a few jumps, or only once the turn ended.
const fixture = vi.hoisted(() => ({
	request: vi.fn(),
	listener: undefined as ((event: ConversationEvent) => void) | undefined,
}));
vi.mock("../api", () => ({ apiRequest: fixture.request, ApiError: class extends Error {}, ATTACHMENT_REQUEST_TIMEOUT_MS: 1 }));
vi.mock("../config", () => ({ machineIdentity: (cfg: { hostId?: string }) => `host.${cfg.hostId}`, authHeaders: () => ({}), httpBase: () => "http://qa-host" }));
vi.mock("expo/fetch", () => ({ fetch: vi.fn() }));
vi.mock("@react-native-async-storage/async-storage", () => ({ default: {
	getItem: async () => null,
	setItem: async () => {},
	removeItem: async () => {},
} }));
vi.mock("./conversationEvents", () => ({
	subscribeConversationEvents: (_cfg: unknown, _sessionId: string, listener: (event: ConversationEvent) => void) => {
		fixture.listener = listener;
		return () => { fixture.listener = undefined; };
	},
}));
vi.mock("./conversationPoll", () => ({ conversationPollIntervalFor: () => null }));
import { useMobileConversation } from "./useConversation";

const lan = { hostId: "a", host: "192.168.1.20" } as ServerConfig;
const tailnet = { hostId: "a", host: "100.64.0.20" } as ServerConfig;
let renderer: ReactTestRenderer | undefined;
function Harness({ cfg = lan }: { cfg?: ServerConfig }) {
	useMobileConversation(cfg, "host.a", "live");
	return null;
}
const conversationRequests = () => fixture.request.mock.calls.filter(([, path]) => /\/conversation(\?|$)/.test(String(path))).length;
let seq = 0;
const emit = () => fixture.listener?.({ seq: ++seq, projectId: "p", sessionId: "live", type: "session_updated", payload: { conversationId: "c" }, createdAt: "" });

/** Lets a pending request settle and React commit, without advancing the clock. */
const settle = () => act(async () => { for (let i = 0; i < 5; i++) await Promise.resolve(); });

beforeEach(() => {
	vi.useFakeTimers();
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	fixture.listener = undefined;
	seq = 0;
	fixture.request.mockReset().mockImplementation(async () => ({
		json: async () => ({ controller: "ready", capabilities: [], messages: [], conversationId: "c" }),
	}));
});
afterEach(async () => {
	if (renderer) await act(async () => renderer!.unmount());
	renderer = undefined;
	vi.useRealTimers();
	vi.unstubAllGlobals();
});

it("keeps refreshing while a reply streams without pause", async () => {
	await act(async () => { renderer = create(<Harness />); });
	await settle();
	expect(fixture.listener).toBeDefined();
	const opened = conversationRequests();

	// Three seconds of a steady stream: an event every 100 ms, never a pause.
	for (let i = 0; i < 30; i++) {
		await act(async () => { emit(); });
		await act(async () => { vi.advanceTimersByTime(100); });
		await settle();
	}

	// Roughly every 120 ms of coalescing plus the read; anything near zero is the bug.
	expect(conversationRequests() - opened).toBeGreaterThanOrEqual(10);
});

it("coalesces a burst into one read, and catches up once after a read in flight", async () => {
	await act(async () => { renderer = create(<Harness />); });
	await settle();
	const opened = conversationRequests();

	let finish!: () => void;
	fixture.request.mockImplementationOnce(() => new Promise((resolve) => {
		finish = () => resolve({ json: async () => ({ controller: "ready", capabilities: [], messages: [], conversationId: "c" }) });
	}));
	for (let i = 0; i < 5; i++) await act(async () => { emit(); });
	await act(async () => { vi.advanceTimersByTime(200); });
	expect(conversationRequests() - opened).toBe(1);

	// Events while that read is out wait for it rather than racing it.
	for (let i = 0; i < 5; i++) await act(async () => { emit(); });
	await act(async () => { vi.advanceTimersByTime(1_000); });
	expect(conversationRequests() - opened).toBe(1);

	await act(async () => { finish(); });
	await settle();
	await act(async () => { vi.advanceTimersByTime(200); });
	await settle();
	expect(conversationRequests() - opened).toBe(2);

	// Nothing more happened, so nothing more is read.
	await act(async () => { vi.advanceTimersByTime(2_000); });
	expect(conversationRequests() - opened).toBe(2);
});

// Switching away from a route that stopped answering leaves its read out until
// it times out. The new route's events must read without waiting for it.
it("reads for the new connection while a read on the old one is still out", async () => {
	await act(async () => { renderer = create(<Harness />); });
	await settle();
	const requestsTo = (host: string) => fixture.request.mock.calls
		.filter(([cfg, path]) => (cfg as ServerConfig).host === host && /\/conversation(\?|$)/.test(String(path))).length;

	fixture.request.mockImplementationOnce(() => new Promise(() => {})); // never answers
	await act(async () => { emit(); });
	await act(async () => { vi.advanceTimersByTime(200); });
	expect(requestsTo(lan.host)).toBe(2); // the open read, then the stuck one

	await act(async () => renderer!.update(<Harness cfg={tailnet} />));
	await settle();
	const reconnected = requestsTo(tailnet.host); // the reconnect's own read

	await act(async () => { emit(); });
	await act(async () => { vi.advanceTimersByTime(200); });
	await settle();
	expect(requestsTo(tailnet.host)).toBe(reconnected + 1);
});

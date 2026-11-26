import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { ServerConfig } from "../config";

// A reply streams as a change about every 100 ms. The transcript has to follow
// it: a debounce that restarted on every change held it back until the agent
// stopped, and on a slow link newer reads discarded every older answer, so the
// screen stood still for the whole reply.
const fixture = vi.hoisted(() => ({
	request: vi.fn(),
	listeners: new Set<(event: { payload?: { conversationId?: string } }) => void>(),
}));
vi.mock("../api", () => ({ apiRequest: fixture.request, ApiError: class extends Error {}, ATTACHMENT_REQUEST_TIMEOUT_MS: 1 }));
vi.mock("../config", () => ({ machineIdentity: (cfg: { hostId?: string }) => `host.${cfg.hostId}`, authHeaders: () => ({}), httpBase: () => "http://qa-host" }));
vi.mock("expo/fetch", () => ({ fetch: vi.fn() }));
vi.mock("@react-native-async-storage/async-storage", () => ({ default: { getItem: async () => null, setItem: async () => {}, removeItem: async () => {} } }));
vi.mock("./conversationEvents", () => ({
	subscribeConversationEvents: (_cfg: unknown, _session: string, listener: (event: { payload?: { conversationId?: string } }) => void) => {
		fixture.listeners.add(listener);
		return () => fixture.listeners.delete(listener);
	},
}));
vi.mock("./conversationPoll", () => ({ conversationPollIntervalFor: () => null }));
// Saving the transcript for after a restart is not under test here.
vi.mock("./savedConversations", () => ({ conversationSaver: { schedule: () => {}, flush: async () => {} }, loadSavedConversation: async () => null }));
import { REFRESH_INTERVAL_MS, useMobileConversation, type MobileConversation } from "./useConversation";
import { LiveRepliesContext, useLiveReplyText } from "./liveReplies";
import type { ConversationMessage } from "./types";

const lan = { hostId: "a", host: "192.168.1.20" } as ServerConfig;
const tailnet = { hostId: "a", host: "100.64.0.20" } as ServerConfig;
let renderer: ReactTestRenderer | undefined;
let conversation!: MobileConversation;
const shown: string[] = [];
function Harness({ id, cfg = lan }: { id: string; cfg?: ServerConfig }) {
	conversation = useMobileConversation(cfg, "host.a", id);
	const last = conversation.snapshot?.items.at(-1);
	// The reply reads its text as the timeline's does: newer text than the
	// snapshot's arrives through `liveReplies`, rendering the reply alone.
	return last?.kind === "message"
		? <LiveRepliesContext.Provider value={conversation.liveReplies}><Reply message={last} /></LiveRepliesContext.Provider>
		: null;
}
let renders = 0;
function Reply({ message }: { message: ConversationMessage }) {
	const latest = useLiveReplyText(message);
	if (shown.at(-1) !== latest) shown.push(latest);
	return null;
}
function Counted({ id }: { id: string }) {
	renders++;
	return <Harness id={id} />;
}

/** The machine's answer: an older settled message and the reply as it stands after `reads` reads. */
let reads = 0;
let finished = false;
let inFlight = 0;
let mostInFlight = 0;
function answerAfter(delayMs: number) {
	fixture.request.mockImplementation(() => {
		const words = ++reads;
		inFlight++;
		mostInFlight = Math.max(mostInFlight, inFlight);
		return new Promise((resolve) => setTimeout(() => {
			inFlight--;
			resolve({ json: async () => ({
				conversationId: "c-a",
				controller: "busy",
				capabilities: [],
				latestSequence: 2,
				turns: [],
				messages: [
					{ id: "m1", sequence: 1, revision: 1, role: "user", origin: "human", text: "Fix the test", streaming: false, createdAt: "2026-11-14T00:00:00Z" },
					{ id: "m2", sequence: 2, revision: words, role: "assistant", origin: "provider", text: `words ${words}`, streaming: !finished, createdAt: "2026-11-14T00:00:01Z" },
				],
			}) });
		}, delayMs));
	});
}

/** The daemon reporting a change every 100 ms for `ms`. */
async function stream(ms: number) {
	for (let elapsed = 0; elapsed < ms; elapsed += 100) {
		await act(async () => {
			for (const listener of fixture.listeners) listener({ payload: { conversationId: "c-a" } });
			await vi.advanceTimersByTimeAsync(100);
		});
	}
}

beforeEach(() => {
	vi.useFakeTimers();
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	fixture.listeners.clear();
	fixture.request.mockReset();
	reads = 0;
	finished = false;
	renders = 0;
	inFlight = 0;
	mostInFlight = 0;
	shown.length = 0;
});
afterEach(async () => {
	if (renderer) await act(async () => renderer!.unmount());
	renderer = undefined;
	vi.unstubAllGlobals();
	vi.useRealTimers();
});

it("shows a steady reply several times a second instead of waiting for it to stop", async () => {
	answerAfter(30);
	await act(async () => { renderer = create(<Harness id="steady" />); await vi.advanceTimersByTimeAsync(50); });
	const before = reads;
	await stream(3_000);
	const during = reads - before;
	// The last read lands.
	await act(async () => { await vi.advanceTimersByTimeAsync(100); });
	// One read per interval, give or take the first and last.
	expect(during).toBeGreaterThanOrEqual(Math.floor(3_000 / REFRESH_INTERVAL_MS) - 1);
	expect(during).toBeLessThanOrEqual(Math.ceil(3_000 / REFRESH_INTERVAL_MS) + 1);
	expect(shown.length).toBeGreaterThanOrEqual(during);
	expect(mostInFlight).toBe(1);
});

it("on a slow link runs one read at a time and shows every answer", async () => {
	answerAfter(1_500);
	await act(async () => { renderer = create(<Harness id="slow" />); await vi.advanceTimersByTimeAsync(1_600); });
	const before = shown.length;
	await stream(9_000);
	await act(async () => { await vi.advanceTimersByTimeAsync(4_000); });
	expect(mostInFlight).toBe(1);
	// Nine seconds of 1.5 s reads: about six answers, each one put on screen.
	expect(shown.length - before).toBeGreaterThanOrEqual(5);
	expect(shown.at(-1)).toBe(`words ${reads}`);
});

it("renders only the reply while nothing but its text grows, and the conversation once more changes", async () => {
	answerAfter(30);
	await act(async () => { renderer = create(<Counted id="identity" />); await vi.advanceTimersByTimeAsync(50); });
	await act(async () => { await vi.advanceTimersByTimeAsync(50); });
	expect(conversation.snapshot?.items).toHaveLength(2);
	const opened = conversation.snapshot;
	const settled = opened?.items[0];
	const screenRenders = renders;
	await stream(1_000);
	await act(async () => { await vi.advanceTimersByTimeAsync(100); });
	// The words still reach the reply, read by read; the screen's conversation stands.
	expect(shown.at(-1)).toBe(`words ${reads}`);
	expect(shown.length).toBeGreaterThan(3);
	expect(conversation.snapshot).toBe(opened);
	expect(renders).toBe(screenRenders);
	// The reply finishing is more than text: the conversation updates, its settled row kept as the same object.
	finished = true;
	await stream(300);
	// Past the read interval, so the last change's read has answered.
	await act(async () => { await vi.advanceTimersByTimeAsync(REFRESH_INTERVAL_MS * 2); });
	expect(conversation.snapshot).not.toBe(opened);
	expect(conversation.snapshot?.items[0]).toBe(settled);
	const reply = conversation.snapshot?.items[1];
	expect(reply?.kind === "message" && !reply.streaming && reply.text).toBe(`words ${reads}`);
});

// Switching away from a route that stopped answering leaves its read out until
// it times out. The new route's changes must read without waiting for it.
it("reads through the new connection while a read on the old one is still out", async () => {
	answerAfter(30);
	await act(async () => { renderer = create(<Harness id="switch" />); await vi.advanceTimersByTimeAsync(50); });
	const answer = fixture.request.getMockImplementation()!;
	fixture.request.mockImplementation((target: ServerConfig, ...rest: unknown[]) =>
		target.host === lan.host ? new Promise(() => {}) : answer(target, ...rest));
	const readsTo = (host: string) => fixture.request.mock.calls.filter(([target]) => (target as ServerConfig).host === host).length;

	await stream(300);
	const stuck = readsTo(lan.host);
	expect(stuck).toBeGreaterThanOrEqual(2); // the opening read, then the one that never answers

	await act(async () => { renderer!.update(<Harness id="switch" cfg={tailnet} />); await vi.advanceTimersByTimeAsync(50); });
	const reconnected = readsTo(tailnet.host);
	expect(reconnected).toBeGreaterThanOrEqual(1);

	await stream(1_000);
	expect(readsTo(tailnet.host)).toBeGreaterThan(reconnected + 1);
	expect(readsTo(lan.host)).toBe(stuck);
	expect(shown.at(-1)).toBe(`words ${reads}`);
});

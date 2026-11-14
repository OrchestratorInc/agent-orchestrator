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
import { REFRESH_INTERVAL_MS, useMobileConversation, type MobileConversation } from "./useConversation";

const cfg = { hostId: "a", host: "192.168.1.20" } as ServerConfig;
let renderer: ReactTestRenderer | undefined;
let conversation!: MobileConversation;
const shown: string[] = [];
function Harness({ id }: { id: string }) {
	conversation = useMobileConversation(cfg, "host.a", id);
	const text = conversation.snapshot?.items.at(-1);
	const latest = text && text.kind === "message" ? text.text : "";
	if (shown.at(-1) !== latest) shown.push(latest);
	return null;
}

/** The machine's answer: an older settled message and the reply as it stands after `reads` reads. */
let reads = 0;
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
					{ id: "m2", sequence: 2, revision: words, role: "assistant", origin: "provider", text: `words ${words}`, streaming: true, createdAt: "2026-11-14T00:00:01Z" },
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
	// One read per interval, give or take the first and last.
	expect(during).toBeGreaterThanOrEqual(Math.floor(3_000 / REFRESH_INTERVAL_MS) - 1);
	expect(during).toBeLessThanOrEqual(Math.ceil(3_000 / REFRESH_INTERVAL_MS) + 1);
	expect(shown.length).toBeGreaterThanOrEqual(during);
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

it("keeps rows that did not change as the same objects, so their views skip a render", async () => {
	answerAfter(30);
	await act(async () => { renderer = create(<Harness id="identity" />); await vi.advanceTimersByTimeAsync(50); });
	await act(async () => { await vi.advanceTimersByTimeAsync(50); });
	expect(conversation.snapshot?.items).toHaveLength(2);
	const settled = conversation.snapshot?.items[0];
	const streaming = conversation.snapshot?.items[1];
	await stream(500);
	expect(conversation.snapshot?.items[0]).toBe(settled);
	expect(conversation.snapshot?.items[1]).not.toBe(streaming);
});

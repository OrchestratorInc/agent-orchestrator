import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { forgetCloudAccountCaches } from "./accountCaches";
import type { ConversationSnapshot } from "../chat/types";
import { CLOUD_CHAT_NEWEST, CLOUD_CHAT_READ_BACK_PAGES } from "./chatHistory";
import type { CloudClient } from "./client";
import type { CloudChatEventsPage, CloudClientEvent, CloudSession } from "./types";
import { useCloudConversation, type CloudConversation } from "./useCloudConversation";

vi.mock("@react-native-async-storage/async-storage", () => ({ default: { getItem: async () => null, setItem: async () => undefined } }));
vi.mock("react-native", () => ({ AppState: { currentState: "active", addEventListener: () => ({ remove() {} }) } }));
const streams = vi.hoisted(() => ({ onEvents: undefined as undefined | ((events: unknown[]) => void) }));
vi.mock("./eventStream", () => ({
	followCloudSessionEvents: (input: { onEvents(events: unknown[]): void }) => {
		streams.onEvents = input.onEvents;
		return () => { streams.onEvents = undefined; };
	},
}));

// Cursor's worker lists no chat models, so no catalog is asked for.
const SESSION = {
	id: "s1", orgId: "org", projectId: "p", kind: "worker", harness: "cursor", displayName: "Long task", branch: "ao/long",
	mode: "trusted", interfaceMode: "chat", activityState: "active", status: "working", runtimeConnected: true, observedState: "running",
	isTerminated: false, createdAt: "2026-11-09T10:00:00Z", updatedAt: "2026-11-09T12:00:00Z",
} as unknown as CloudSession;

// One turn per 50 events, each opened by its prompt, as the control plane pages them.
const event = (sequence: number): CloudClientEvent => {
	const turnId = `t${Math.ceil(sequence / 50)}`;
	const opens = sequence % 50 === 1;
	return {
		sessionId: "s1", sequence, createdAt: new Date(Date.UTC(2026, 10, 9, 10, 0, sequence)).toISOString(),
		type: opens ? "chat.user_message" : sequence % 50 === 0 ? "chat.turn_completed" : "chat.assistant_delta",
		payload: opens ? { turnId, text: `Prompt ${turnId}` } : { turnId, itemId: "a", text: "." },
	};
};
const range = (from: number, to: number) => Array.from({ length: to - from + 1 }, (_, index) => event(from + index));
const page = (events: CloudClientEvent[], extra: Partial<CloudChatEventsPage> = {}): CloudChatEventsPage => ({ events, hasMore: false, nextAfter: events.at(-1)?.sequence ?? 0, ...extra });

type Query = { after?: number; before?: number; since?: string; limit?: number };
/** A control plane holding events 1–450 that reads back. */
function cloud(newest = 450) {
	const listChatEvents = vi.fn(async (_org: string, _session: string, query: Query): Promise<CloudChatEventsPage> => {
		if (query.after !== undefined) return page(range(query.after + 1, newest), { hasOlder: false, nextBefore: query.after + 1 });
		if (query.before === CLOUD_CHAT_NEWEST) return page(range(newest - 149, newest), { hasOlder: true, nextBefore: newest - 149 });
		if (query.since) return page(range(201, 300), { hasOlder: true, nextBefore: 201 });
		return page(range(1, 200), { hasOlder: false, nextBefore: 1 });
	});
	return {
		baseUrl: "https://cloud",
		listChatEvents,
		getSession: vi.fn(async () => ({ session: SESSION })),
		touchPresence: vi.fn(async () => undefined),
	} as unknown as CloudClient & { listChatEvents: typeof listChatEvents };
}

let renderer: ReactTestRenderer | undefined;
let chat: CloudConversation;
function Probe({ client }: { client: CloudClient }) {
	chat = useCloudConversation({ client, orgId: "org", sessionId: "s1", initial: SESSION, active: true });
	return null;
}
const settle = () => act(async () => { for (let tick = 0; tick < 6; tick++) await new Promise((resolve) => setTimeout(resolve, 0)); });
const open = async (client: CloudClient) => {
	act(() => { renderer = create(<Probe client={client} />); });
};
const close = () => { act(() => renderer?.unmount()); renderer = undefined; };

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	forgetCloudAccountCaches();
});
afterEach(() => { close(); vi.unstubAllGlobals(); });

// Seen on the desktop: a long Cloud chat showed nothing for seconds while its
// whole history was read from the start, though each read took milliseconds.
it("opens a long chat on its newest page and the half hour before it, and reads older pages on request", async () => {
	const client = cloud();
	await open(client);
	await settle();
	expect(client.listChatEvents.mock.calls[0]?.[2]).toEqual({ before: CLOUD_CHAT_NEWEST, limit: 150 });
	expect(client.listChatEvents.mock.calls[1]?.[2]).toMatchObject({ before: 301, limit: 500, since: expect.any(String) });
	expect(chat.snapshot).toMatchObject({ oldestSequence: 201, latestSequence: 450, hasMoreBefore: true });

	await act(async () => { await chat.loadOlder(); });
	expect(chat.snapshot).toMatchObject({ oldestSequence: 1, hasMoreBefore: false });
	expect(chat.snapshot?.turns.map((turn) => turn.id)).toEqual(["t1", "t2", "t3", "t4", "t5", "t6", "t7", "t8", "t9"]);
});

it("drops streamed events below a page that lacks older history", async () => {
	await open(cloud());
	await settle();
	act(() => streams.onEvents?.([event(100), event(451)]));
	expect(chat.snapshot).toMatchObject({ oldestSequence: 201, latestSequence: 451 });
});

it("reopens a chat from what it held and reads only what is newer, until the account changes", async () => {
	await open(cloud());
	await settle();
	close();

	const again = cloud(455);
	await open(again);
	// From the first frame, before any read.
	expect(chat.snapshot).toMatchObject({ oldestSequence: 201, latestSequence: 450 });
	await settle();
	expect(again.listChatEvents.mock.calls[0]?.[2]).toEqual({ after: 450, before: CLOUD_CHAT_NEWEST, limit: 500 });
	expect(chat.snapshot?.latestSequence).toBe(455);
	close();

	forgetCloudAccountCaches();
	await open(cloud());
	expect(chat.snapshot).toBeNull();
});

describe("a newest page of only pull request updates", () => {
	// A paused session's newest events can all be scm.updated: every push to the
	// repository refreshed its open pull request while nobody talked to it.
	const update = (sequence: number): CloudClientEvent => ({
		sessionId: "s1", sequence, type: "scm.updated", createdAt: new Date(Date.UTC(2026, 10, 9, 10, sequence)).toISOString(),
		payload: { pullRequestId: "pr-1", number: 7, repository: "octo/widgets" },
	});
	const chatThen = (chatThrough: number, total: number) => Array.from({ length: total }, (_, index) => {
		const sequence = index + 1;
		return sequence > chatThrough ? update(sequence) : { ...event(sequence), createdAt: new Date(Date.UTC(2026, 10, 9, 10, sequence)).toISOString() };
	});
	/** The control plane's read back: the newest limit before `before`, cut at since. */
	function serving(events: CloudClientEvent[]) {
		const client = cloud();
		client.listChatEvents.mockImplementation(async (_org, _session, query) => {
			let held = events.filter((item) => item.sequence > (query.after ?? 0) && item.sequence < (query.before ?? Number.MAX_SAFE_INTEGER)).slice(-(query.limit ?? 500));
			if (query.since) held = held.filter((item) => item.createdAt >= query.since!);
			const first = held[0]?.sequence ?? query.before ?? 0;
			return page(held, { hasOlder: events.some((item) => item.sequence < first && item.sequence > (query.after ?? 0)), nextBefore: first });
		});
		return client;
	}
	const readBacks = (client: ReturnType<typeof cloud>) => client.listChatEvents.mock.calls.filter(([, , query]) => query.limit === 200);
	const settleLong = () => act(async () => { for (let tick = 0; tick < 60; tick++) await new Promise((resolve) => setTimeout(resolve, 0)); });

	it("reads back to the conversation, loading until it does", async () => {
		const client = serving(chatThen(100, 1000));
		await open(client);
		const frames: (ConversationSnapshot | null)[] = [];
		for (let round = 0; round < 10 && !chat.snapshot; round++) {
			await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
			frames.push(chat.snapshot);
		}
		await settleLong();
		// No frame showed the chat without its conversation.
		expect(frames.every((frame) => frame === null || frame.turns.length > 0)).toBe(true);
		expect(chat.snapshot?.turns.length).toBeGreaterThan(0);
		expect(readBacks(client).length).toBeGreaterThan(0);
		expect(readBacks(client).length).toBeLessThanOrEqual(CLOUD_CHAT_READ_BACK_PAGES);
	});

	it("stops at the bound and offers the earlier messages", async () => {
		const client = serving(chatThen(0, 3000));
		await open(client);
		await settleLong();
		expect(readBacks(client)).toHaveLength(CLOUD_CHAT_READ_BACK_PAGES);
		expect(chat.snapshot).toMatchObject({ hasMoreBefore: true, turns: [] });
		await act(async () => { await chat.loadOlder(); });
		expect(readBacks(client)).toHaveLength(CLOUD_CHAT_READ_BACK_PAGES + 1);
	});

	it("does not read back a newest page that holds the conversation", async () => {
		const client = cloud();
		await open(client);
		await settle();
		expect(readBacks(client)).toHaveLength(0);
	});
});

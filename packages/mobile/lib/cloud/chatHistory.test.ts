import { beforeEach, describe, expect, it, vi } from "vitest";
import { forgetCloudAccountCaches } from "./accountCaches";
import {
	CLOUD_CHAT_NEWEST, cloudChatKey, prependOlderCloudEvents, readCloudChatEvents, readOlderCloudChatEvents,
	retainCloudChat, retainedCloudChat, streamedCloudChatEvents,
} from "./chatHistory";
import type { CloudChatEventsPage, CloudClientEvent } from "./types";

const at = (sequence: number) => new Date(Date.UTC(2026, 10, 9, 12, 0, sequence)).toISOString();
const event = (sequence: number): CloudClientEvent => ({ sequence, type: "chat.assistant_delta", createdAt: at(sequence), payload: { turnId: "t", text: `${sequence}` } } as CloudClientEvent);
const range = (from: number, to: number) => Array.from({ length: to - from + 1 }, (_, index) => event(from + index));
const page = (events: CloudClientEvent[], extra: Partial<CloudChatEventsPage> = {}): CloudChatEventsPage => ({ events, hasMore: false, nextAfter: events.at(-1)?.sequence ?? 0, ...extra });
const sequences = (events: readonly CloudClientEvent[]) => events.map((item) => item.sequence);

function client(...pages: CloudChatEventsPage[]) {
	const listChatEvents = vi.fn();
	for (const next of pages) listChatEvents.mockResolvedValueOnce(next);
	return { listChatEvents };
}

describe("a Cloud chat read newest first", () => {
	it("opens on the newest page and notes the older history it lacks", async () => {
		const cloud = client(page(range(301, 450), { hasOlder: true, nextBefore: 301 }));
		const read = await readCloudChatEvents(cloud, "org", "s1", []);
		expect(cloud.listChatEvents).toHaveBeenCalledWith("org", "s1", { before: CLOUD_CHAT_NEWEST, limit: 150 });
		expect(sequences(read.events)).toEqual(sequences(range(301, 450)));
		expect(read.startOver).toEqual({ history: { hasOlder: true, nextBefore: 301, recentPending: true } });
	});

	it("holds a short chat whole, with nothing older to read", async () => {
		const read = await readCloudChatEvents(client(page(range(1, 40), { hasOlder: false, nextBefore: 1 })), "org", "s1", []);
		expect(read.startOver).toEqual({ history: undefined });
	});

	// A control plane from before reading back ignores `before` and answers forward.
	it("reads an older control plane's whole history forward, as before", async () => {
		const cloud = client(page(range(1, 500), { hasMore: true, nextAfter: 500 }), page(range(501, 620)));
		const read = await readCloudChatEvents(cloud, "org", "s1", []);
		expect(cloud.listChatEvents).toHaveBeenLastCalledWith("org", "s1", { after: 500, limit: 500 });
		expect(read.events).toHaveLength(620);
		expect(read.startOver).toEqual({ history: undefined });
	});

	it("reads only what came after the newest held event", async () => {
		const cloud = client(page(range(451, 460), { hasOlder: false }));
		const read = await readCloudChatEvents(cloud, "org", "s1", range(301, 450));
		expect(cloud.listChatEvents).toHaveBeenCalledWith("org", "s1", { after: 450, before: CLOUD_CHAT_NEWEST, limit: 500 });
		expect(read.startOver).toBeUndefined();
		expect(sequences(read.events).slice(-2)).toEqual([459, 460]);
	});

	// More came than one read holds (a phone away a while): the events between
	// would be a hole no older page fills, so the list starts again at the newest.
	it("starts over at the newest page when more came than a read holds", async () => {
		const cloud = client(page(range(951, 1450), { hasOlder: true, nextBefore: 951 }), page(range(1301, 1450), { hasOlder: true, nextBefore: 1301 }));
		const read = await readCloudChatEvents(cloud, "org", "s1", range(301, 450));
		expect(cloud.listChatEvents).toHaveBeenLastCalledWith("org", "s1", { before: CLOUD_CHAT_NEWEST, limit: 150 });
		expect(sequences(read.events)[0]).toBe(1301);
		expect(read.startOver?.history).toMatchObject({ hasOlder: true, nextBefore: 1301 });
	});

	it("reads the half hour before the first page, then older pages on request", async () => {
		const held = range(301, 450);
		const recent = client(page(range(201, 300), { hasOlder: true, nextBefore: 201 }));
		const first = await readOlderCloudChatEvents(recent, "org", "s1", held, { hasOlder: true, nextBefore: 301, recentPending: true }, { recent: true });
		expect(recent.listChatEvents).toHaveBeenCalledWith("org", "s1", { before: 301, limit: 500, since: new Date(Date.parse(at(301)) - 30 * 60_000).toISOString() });
		expect(first).toEqual({ older: range(201, 300), history: { hasOlder: true, nextBefore: 201, recentPending: false } });

		const older = client(page(range(1, 200), { hasOlder: false, nextBefore: 1 }));
		const next = await readOlderCloudChatEvents(older, "org", "s1", held, { hasOlder: true, nextBefore: 201, recentPending: false });
		expect(older.listChatEvents).toHaveBeenCalledWith("org", "s1", { before: 201, limit: 200, since: undefined });
		expect(next).toMatchObject({ history: { hasOlder: false } });
	});

	it("asks for the whole history again when the control plane stops reading back", async () => {
		const read = await readOlderCloudChatEvents(client(page(range(1, 10))), "org", "s1", range(301, 450), { hasOlder: true, nextBefore: 301, recentPending: false });
		expect(read).toEqual({ restart: true });
	});

	it("puts an older page in front of the list as it is when the page lands", () => {
		// The stream added 451 while the page was read; a page overlapping the list adds only what is below it.
		const now = range(301, 451);
		expect(sequences(prependOlderCloudEvents(now, range(250, 305))).slice(0, 2)).toEqual([250, 251]);
		expect(prependOlderCloudEvents(now, range(320, 330))).toBe(now);
	});

	it("drops streamed events below a list that lacks older history, and keeps them for a whole one", () => {
		const held = range(301, 450);
		const incoming = [event(120), event(451)];
		expect(sequences(streamedCloudChatEvents(held, { hasOlder: true, nextBefore: 301, recentPending: false }, incoming))).toEqual([451]);
		expect(streamedCloudChatEvents(held, undefined, incoming)).toBe(incoming);
	});
});

describe("a Cloud chat kept for a reopen", () => {
	beforeEach(() => forgetCloudAccountCaches());

	it("keeps a few chats for an hour, and forgets them when the account changes", () => {
		vi.useFakeTimers();
		try {
			const key = cloudChatKey("https://cloud", "org", "s1");
			retainCloudChat(key, range(1, 3), undefined);
			expect(sequences(retainedCloudChat(key)!.events)).toEqual([1, 2, 3]);
			vi.advanceTimersByTime(61 * 60_000);
			expect(retainedCloudChat(key)).toBeUndefined();

			for (let index = 0; index < 9; index++) retainCloudChat(cloudChatKey("https://cloud", "org", `s${index}`), range(1, 1), undefined);
			expect(retainedCloudChat(cloudChatKey("https://cloud", "org", "s0"))).toBeUndefined();
			expect(retainedCloudChat(cloudChatKey("https://cloud", "org", "s8"))).toBeDefined();

			forgetCloudAccountCaches();
			expect(retainedCloudChat(cloudChatKey("https://cloud", "org", "s8"))).toBeUndefined();
		} finally {
			vi.useRealTimers();
		}
	});
});

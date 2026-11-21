import { QueryClient } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import type { CloudCpChatEventsResponse, CloudCpClientEvent } from "./cloud-cp";
import {
	CLOUD_CHAT_NEWEST,
	cloudChatEventsKey,
	cloudChatHistoryKey,
	cloudChatHistoryPartial,
	loadCloudChatHistory,
	loadOlderCloudChatEvents,
	readCloudChatEvents,
	streamedCloudChatEvents,
	type CloudChatHistory,
} from "./cloud-chat-history";

const ORG = "org-1";
const SESSION = "session-1";

const event = (sequence: number, createdAt = "2026-11-08T10:00:00.000Z"): CloudCpClientEvent => ({
	sessionId: SESSION, sequence, type: "chat.assistant_delta", payload: { text: String(sequence) }, createdAt,
});
const sequences = (events: readonly CloudCpClientEvent[] | undefined) => events?.map((item) => item.sequence);

/** A page read back from a sequence, as a control plane that supports it answers. */
const back = (events: CloudCpClientEvent[], hasOlder: boolean, nextBefore = events[0]?.sequence): CloudCpChatEventsResponse => ({
	events, hasMore: false, nextAfter: events.at(-1)?.sequence ?? 0, hasOlder, nextBefore,
});
/** A forward page, as an older control plane answers any read. */
const forward = (events: CloudCpClientEvent[], hasMore: boolean): CloudCpChatEventsResponse => ({
	events, hasMore, nextAfter: events.at(-1)?.sequence ?? 0,
});

function client(...pages: CloudCpChatEventsResponse[]) {
	const listChatEvents = vi.fn<(orgId: string, sessionId: string, query?: object) => Promise<CloudCpChatEventsResponse>>();
	for (const page of pages) listChatEvents.mockResolvedValueOnce(page);
	return { listChatEvents };
}

function history(queryClient: QueryClient) {
	return queryClient.getQueryData<CloudChatHistory>(cloudChatHistoryKey(ORG, SESSION));
}

describe("readCloudChatEvents", () => {
	it("reads the newest page first, turn aligned by the control plane", async () => {
		const cp = client(back([event(40), event(41), event(42)], true));
		const read = await readCloudChatEvents(cp, ORG, SESSION, undefined);
		expect(cp.listChatEvents).toHaveBeenCalledExactlyOnceWith(ORG, SESSION, { before: CLOUD_CHAT_NEWEST, limit: 150 });
		expect(sequences(read.events)).toEqual([40, 41, 42]);
		expect(read.startOver).toEqual({ history: { hasOlder: true, nextBefore: 40, recentPending: true } });
	});

	it("holds the whole history when the newest page reaches the session's start", async () => {
		const read = await readCloudChatEvents(client(back([event(1), event(2)], false)), ORG, SESSION, []);
		expect(read.startOver).toEqual({ history: undefined });
	});

	it("reads only what came after the newest cached event", async () => {
		const cp = client(back([event(43), event(44)], false));
		const read = await readCloudChatEvents(cp, ORG, SESSION, [event(40), event(42)]);
		expect(cp.listChatEvents).toHaveBeenCalledExactlyOnceWith(ORG, SESSION, { after: 42, before: CLOUD_CHAT_NEWEST, limit: 500 });
		expect(sequences(read.events)).toEqual([40, 42, 43, 44]);
		expect(read.startOver).toBeUndefined();
	});

	it("starts over at the newest page when more came than one read holds", async () => {
		const cp = client(back([event(900), event(901)], true), back([event(880), event(901)], true));
		const read = await readCloudChatEvents(cp, ORG, SESSION, [event(40), event(42)]);
		expect(cp.listChatEvents).toHaveBeenLastCalledWith(ORG, SESSION, { before: CLOUD_CHAT_NEWEST, limit: 150 });
		expect(sequences(read.events)).toEqual([880, 901]);
		expect(read.startOver?.history).toMatchObject({ hasOlder: true, nextBefore: 880 });
	});

	it("pages forward from the start on a control plane that cannot read back", async () => {
		const cp = client(forward([event(1), event(2)], true), forward([event(3)], false));
		const read = await readCloudChatEvents(cp, ORG, SESSION, undefined);
		expect(cp.listChatEvents.mock.calls.map((call) => call[2])).toEqual([{ before: CLOUD_CHAT_NEWEST, limit: 150 }, { after: 2, limit: 500 }]);
		expect(sequences(read.events)).toEqual([1, 2, 3]);
		expect(read.startOver).toEqual({ history: undefined });
	});

	it("pages forward from the newest cached event on a control plane that cannot read back", async () => {
		const cp = client(forward([event(43)], true), forward([event(44)], false));
		const read = await readCloudChatEvents(cp, ORG, SESSION, [event(42)]);
		expect(cp.listChatEvents.mock.calls.map((call) => call[2])).toEqual([{ after: 42, before: CLOUD_CHAT_NEWEST, limit: 500 }, { after: 43, limit: 500 }]);
		expect(sequences(read.events)).toEqual([42, 43, 44]);
		expect(read.startOver).toBeUndefined();
	});

	it("stops when the forward cursor does not advance", async () => {
		const cp = client(forward([], true));
		await expect(readCloudChatEvents(cp, ORG, SESSION, undefined)).rejects.toThrow("did not advance");
	});
});

describe("loadCloudChatHistory", () => {
	it("records what the newest page holds", async () => {
		const queryClient = new QueryClient();
		const events = await loadCloudChatHistory(queryClient, client(back([event(40), event(41)], true)), ORG, SESSION);
		expect(sequences(events)).toEqual([40, 41]);
		expect(history(queryClient)).toEqual({ hasOlder: true, nextBefore: 40, recentPending: true, loadingOlder: false, generation: 1 });
		expect(cloudChatHistoryPartial(queryClient, ORG, SESSION)).toBe(true);
	});

	it("keeps events the stream added above a list that starts over, and none below it", async () => {
		const queryClient = new QueryClient();
		queryClient.setQueryData(cloudChatEventsKey(ORG, SESSION), [event(40), event(42)]);
		queryClient.setQueryData<CloudChatHistory>(cloudChatHistoryKey(ORG, SESSION), { hasOlder: true, nextBefore: 40, recentPending: false, loadingOlder: true, generation: 3 });
		const cp = client();
		cp.listChatEvents.mockImplementationOnce(async () => {
			// The stream replays from the old list's end while the read runs.
			queryClient.setQueryData(cloudChatEventsKey(ORG, SESSION), [event(40), event(42), event(43), event(901)]);
			return back([event(900)], true);
		}).mockResolvedValueOnce(back([event(880), event(900)], true));
		const events = await loadCloudChatHistory(queryClient, cp, ORG, SESSION);
		expect(sequences(events)).toEqual([880, 900, 901]);
		expect(history(queryClient)).toEqual({ hasOlder: true, nextBefore: 880, recentPending: true, loadingOlder: false, generation: 4 });
	});

	it("merges what came after the newest cached event with what the stream added", async () => {
		const queryClient = new QueryClient();
		queryClient.setQueryData(cloudChatEventsKey(ORG, SESSION), [event(40), event(42)]);
		const cp = client();
		cp.listChatEvents.mockImplementationOnce(async () => {
			queryClient.setQueryData(cloudChatEventsKey(ORG, SESSION), [event(40), event(42), event(44)]);
			return back([event(43)], false);
		});
		expect(sequences(await loadCloudChatHistory(queryClient, cp, ORG, SESSION))).toEqual([40, 42, 43, 44]);
	});

	it("marks a list that starts over with the whole history as having nothing older", async () => {
		const queryClient = new QueryClient();
		queryClient.setQueryData<CloudChatHistory>(cloudChatHistoryKey(ORG, SESSION), { hasOlder: true, nextBefore: 40, recentPending: false, loadingOlder: false, generation: 1 });
		await loadCloudChatHistory(queryClient, client(forward([event(1)], false)), ORG, SESSION);
		expect(history(queryClient)).toMatchObject({ hasOlder: false, generation: 2 });
	});
});

describe("loadOlderCloudChatEvents", () => {
	function seeded(state: Partial<CloudChatHistory> = {}) {
		const queryClient = new QueryClient();
		queryClient.setQueryData(cloudChatEventsKey(ORG, SESSION), [event(40, "2026-11-08T10:00:00.000Z"), event(41)]);
		queryClient.setQueryData<CloudChatHistory>(cloudChatHistoryKey(ORG, SESSION), {
			hasOlder: true, nextBefore: 40, recentPending: true, loadingOlder: false, generation: 1, ...state,
		});
		return queryClient;
	}

	it("reads the half hour before the first page and adds it in front", async () => {
		const queryClient = seeded();
		const cp = client(back([event(30), event(35)], true));
		await loadOlderCloudChatEvents(queryClient, cp, ORG, SESSION, { recent: true });
		expect(cp.listChatEvents).toHaveBeenCalledExactlyOnceWith(ORG, SESSION, { before: 40, limit: 500, since: "2026-11-08T09:30:00.000Z" });
		expect(sequences(queryClient.getQueryData(cloudChatEventsKey(ORG, SESSION)))).toEqual([30, 35, 40, 41]);
		expect(history(queryClient)).toEqual({ hasOlder: true, nextBefore: 30, recentPending: false, loadingOlder: false, generation: 1 });
	});

	it("reads the next older page on demand, until none remain", async () => {
		const queryClient = seeded({ recentPending: false });
		const cp = client(back([event(1), event(20)], false));
		await loadOlderCloudChatEvents(queryClient, cp, ORG, SESSION);
		expect(cp.listChatEvents).toHaveBeenCalledExactlyOnceWith(ORG, SESSION, { before: 40, limit: 200, since: undefined });
		expect(sequences(queryClient.getQueryData(cloudChatEventsKey(ORG, SESSION)))).toEqual([1, 20, 40, 41]);
		expect(history(queryClient)).toMatchObject({ hasOlder: false, loadingOlder: false });
		await loadOlderCloudChatEvents(queryClient, cp, ORG, SESSION);
		expect(cp.listChatEvents).toHaveBeenCalledTimes(1);
	});

	it("reads one older page at a time", async () => {
		const queryClient = seeded({ recentPending: false });
		let answer!: (page: CloudCpChatEventsResponse) => void;
		const cp = { listChatEvents: vi.fn(() => new Promise<CloudCpChatEventsResponse>((resolve) => { answer = resolve; })) };
		const first = loadOlderCloudChatEvents(queryClient, cp, ORG, SESSION);
		expect(history(queryClient)?.loadingOlder).toBe(true);
		await loadOlderCloudChatEvents(queryClient, cp, ORG, SESSION);
		expect(cp.listChatEvents).toHaveBeenCalledTimes(1);
		answer(back([event(39)], true));
		await first;
		expect(history(queryClient)).toMatchObject({ loadingOlder: false, nextBefore: 39 });
	});

	it("drops a page read for a list that has since started over", async () => {
		const queryClient = seeded({ recentPending: false });
		const cp = client();
		cp.listChatEvents.mockImplementationOnce(async () => {
			queryClient.setQueryData(cloudChatEventsKey(ORG, SESSION), [event(900)]);
			queryClient.setQueryData<CloudChatHistory>(cloudChatHistoryKey(ORG, SESSION), { hasOlder: true, nextBefore: 900, recentPending: true, loadingOlder: false, generation: 2 });
			return back([event(39)], true);
		});
		await loadOlderCloudChatEvents(queryClient, cp, ORG, SESSION);
		expect(sequences(queryClient.getQueryData(cloudChatEventsKey(ORG, SESSION)))).toEqual([900]);
		expect(history(queryClient)).toMatchObject({ nextBefore: 900, generation: 2, recentPending: true });
	});

	it("only clears the pending half hour when nothing older remains", async () => {
		const queryClient = seeded({ hasOlder: false });
		const cp = client();
		await loadOlderCloudChatEvents(queryClient, cp, ORG, SESSION, { recent: true });
		expect(cp.listChatEvents).not.toHaveBeenCalled();
		expect(history(queryClient)?.recentPending).toBe(false);
	});

	it("frees the next read after a failed one", async () => {
		const queryClient = seeded({ recentPending: false });
		const cp = { listChatEvents: vi.fn().mockRejectedValueOnce(new Error("offline")) };
		await expect(loadOlderCloudChatEvents(queryClient, cp, ORG, SESSION)).rejects.toThrow("offline");
		expect(history(queryClient)).toMatchObject({ loadingOlder: false, hasOlder: true, nextBefore: 40 });
	});

	it("reads the whole history again when the control plane stops reading back", async () => {
		const queryClient = seeded({ recentPending: false });
		await loadOlderCloudChatEvents(queryClient, client(forward([event(1)], false)), ORG, SESSION);
		expect(queryClient.getQueryData(cloudChatEventsKey(ORG, SESSION))).toBeUndefined();
		expect(history(queryClient)?.hasOlder).toBe(false);
	});
});

describe("streamedCloudChatEvents", () => {
	it("drops streamed events below a list that lacks older history", () => {
		const queryClient = new QueryClient();
		const current = [event(880), event(900)];
		expect(sequences(streamedCloudChatEvents(queryClient, ORG, SESSION, current, [event(43), event(901)]))).toEqual([43, 901]);
		queryClient.setQueryData<CloudChatHistory>(cloudChatHistoryKey(ORG, SESSION), { hasOlder: true, nextBefore: 880, recentPending: false, loadingOlder: false, generation: 1 });
		expect(sequences(streamedCloudChatEvents(queryClient, ORG, SESSION, current, [event(43), event(900), event(901)]))).toEqual([900, 901]);
	});
});

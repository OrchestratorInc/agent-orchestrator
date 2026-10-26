import { QueryClient, QueryObserver } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { PERSISTED_CONVERSATION_LIMIT, startQueryPersistence, type CacheRecord, type QueryCacheStore } from "./query-persistence";
import { isRestoredQueryData } from "./restored-query";

function memoryStore(initial: Record<string, CacheRecord[]> = {}) {
	const hosts = new Map(Object.entries(initial).map(([host, records]) => [host, new Map(records.map((record) => [record.key, record]))]));
	const writes: Array<{ puts: string[]; deletes: string[] }> = [];
	const store: QueryCacheStore = {
		read: async (host, keys) => keys.flatMap((key) => hosts.get(host)?.get(key) ?? []),
		write: async (host, puts, deletes) => {
			writes.push({ puts: puts.map((record) => record.key), deletes });
			const records = hosts.get(host) ?? new Map<string, CacheRecord>();
			hosts.set(host, records);
			for (const key of deletes) records.delete(key);
			for (const record of puts) records.set(record.key, record);
		},
		clear: async (host) => {
			hosts.delete(host);
		},
	};
	return { store, writes, records: (host: string) => hosts.get(host) ?? new Map<string, CacheRecord>() };
}

const BUSTER = "1|1.2.3";
const page = (sessionId: string, latestSequence = 1) => ({ sessionId, conversationId: `c-${sessionId}`, latestSequence, items: [], turns: [] });
const record = (key: string, value: unknown, buster = BUSTER): CacheRecord => ({ key, buster, json: JSON.stringify(value) });

function observe(queryClient: QueryClient, sessionId: string) {
	const observer = new QueryObserver(queryClient, { queryKey: ["conversation", sessionId], queryFn: () => page(sessionId), enabled: false });
	return observer.subscribe(() => undefined);
}

let queryClient: QueryClient;
beforeEach(() => {
	vi.useFakeTimers();
	queryClient = new QueryClient();
});
afterEach(() => {
	vi.useRealTimers();
	queryClient.clear();
});

describe("query persistence", () => {
	it("restores this host's list and opened conversations as stale, never as live data", async () => {
		const { store } = memoryStore({
			"local:a": [
				record("workspaces", [{ id: "p1", sessions: [] }]),
				record("index", { order: ["s1"], sizes: {} }),
				record("conversation:s1", page("s1")),
			],
			"local:b": [record("workspaces", [{ id: "other", sessions: [] }])],
		});

		const persistence = await startQueryPersistence(queryClient, store, "local:a", "1.2.3");

		const workspaces = queryClient.getQueryState(["workspaces"]);
		expect(workspaces?.data).toEqual([{ id: "p1", sessions: [] }]);
		expect(isRestoredQueryData(workspaces)).toBe(true);
		expect(queryClient.getQueryCache().find({ queryKey: ["workspaces"] })?.isStaleByTime(60_000)).toBe(true);
		// A conversation is read only once its query exists.
		expect(queryClient.getQueryState(["conversation", "s1"])).toBeUndefined();
		const unsubscribe = observe(queryClient, "s1");
		await vi.waitFor(() => expect(queryClient.getQueryData(["conversation", "s1"])).toEqual({ pages: [page("s1")], pageParams: [undefined] }));
		expect(isRestoredQueryData(queryClient.getQueryState(["conversation", "s1"]))).toBe(true);
		unsubscribe();

		queryClient.setQueryData(["workspaces"], [{ id: "p1", sessions: [] }]);
		expect(isRestoredQueryData(queryClient.getQueryState(["workspaces"]))).toBe(false);
		persistence.stop();
	});

	it("drops a cache written by another app version", async () => {
		const { store, records } = memoryStore({ "local:a": [record("workspaces", [{ id: "p1" }], "1|0.9.0")] });

		const persistence = await startQueryPersistence(queryClient, store, "local:a", "1.2.3");

		expect(queryClient.getQueryData(["workspaces"])).toBeUndefined();
		expect(records("local:a").size).toBe(0);
		persistence.stop();
	});

	it("writes live data once per throttle window and skips unchanged results", async () => {
		const { store, writes, records } = memoryStore({ "local:a": [record("workspaces", [{ id: "old" }])] });
		const persistence = await startQueryPersistence(queryClient, store, "local:a", "1.2.3");

		const live = [{ id: "p1", sessions: [] }];
		queryClient.setQueryData(["workspaces"], live);
		queryClient.setQueryData(["workspaces"], [{ id: "p1", sessions: [] }]);
		expect(writes).toHaveLength(0);
		await vi.advanceTimersByTimeAsync(2_000);
		expect(writes).toEqual([{ puts: ["workspaces", "index"], deletes: [] }]);
		expect(JSON.parse(records("local:a").get("workspaces")!.json)).toEqual(live);

		// Structural sharing keeps the same reference, so nothing is rewritten.
		queryClient.setQueryData(["workspaces"], [{ id: "p1", sessions: [] }]);
		await vi.advanceTimersByTimeAsync(2_000);
		expect(writes).toHaveLength(1);
		persistence.stop();
	});

	it("keeps page 0 of the most recently opened conversations only", async () => {
		const { store, records } = memoryStore();
		const persistence = await startQueryPersistence(queryClient, store, "local:a", "1.2.3");

		const sessions = Array.from({ length: PERSISTED_CONVERSATION_LIMIT + 1 }, (_, index) => `s${index}`);
		for (const sessionId of sessions) {
			const unsubscribe = observe(queryClient, sessionId);
			queryClient.setQueryData(["conversation", sessionId], { pages: [page(sessionId), page(sessionId, 0)], pageParams: [undefined, 1] });
			unsubscribe();
		}
		// Hover prefetch has no observer, so it is not an opened conversation.
		queryClient.setQueryData(["conversation", "hovered"], { pages: [page("hovered")], pageParams: [undefined] });
		await persistence.flush();

		const saved = records("local:a");
		expect(JSON.parse(saved.get("index")!.json).order).toEqual([...sessions].reverse().slice(0, PERSISTED_CONVERSATION_LIMIT));
		expect(saved.has("conversation:s0")).toBe(false);
		expect(saved.has("conversation:hovered")).toBe(false);
		expect(JSON.parse(saved.get(`conversation:s${PERSISTED_CONVERSATION_LIMIT}`)!.json)).toEqual(page(`s${PERSISTED_CONVERSATION_LIMIT}`));
		persistence.stop();
	});
});

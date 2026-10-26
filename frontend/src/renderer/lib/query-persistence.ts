import type { InfiniteData, Query, QueryClient } from "@tanstack/react-query";
import { workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import { conversationQueryRoot } from "../hooks/useConversation";
import type { ConversationSnapshot } from "../types/conversation";
import { aoBridge } from "./bridge";

// Bump when the shape of WorkspaceSummary[] or ConversationSnapshot changes in
// a way an older cache cannot be read as.
const CACHE_SCHEMA_VERSION = 1;
export const PERSISTED_CONVERSATION_LIMIT = 10;
// Measured in JSON characters. The biggest real first page is about 1 MB.
const MAX_ENTRY_CHARS = 2_000_000;
const MAX_TOTAL_CHARS = 8_000_000;
const WRITE_THROTTLE_MS = 2_000;

const WORKSPACES = "workspaces";
// Most recently opened conversations first, with the size of every entry.
const INDEX = "index";
const CONVERSATION_PREFIX = "conversation:";

export type CacheRecord = { key: string; buster: string; json: string };
type CacheIndex = { order: string[]; sizes: Record<string, number> };

/** Records of one daemon, by entry key. */
export interface QueryCacheStore {
	read(host: string, keys: string[]): Promise<CacheRecord[]>;
	write(host: string, puts: CacheRecord[], deletes: string[]): Promise<void>;
	clear(host: string): Promise<void>;
}

function conversationSessionId(query: Query): string | undefined {
	const key = query.queryKey;
	return key.length === 2 && key[0] === conversationQueryRoot[0] && typeof key[1] === "string" ? key[1] : undefined;
}

function isWorkspaceQuery(query: Query): boolean {
	return query.queryKey.length === 1 && query.queryKey[0] === workspaceQueryKey[0];
}

function parse(json: string): unknown {
	try {
		return JSON.parse(json);
	} catch {
		return undefined;
	}
}

function parseIndex(record: CacheRecord | undefined): CacheIndex {
	const value = record ? (parse(record.json) as Partial<CacheIndex> | undefined) : undefined;
	const order = Array.isArray(value?.order) ? value.order.filter((id): id is string => typeof id === "string") : [];
	const sizes = value?.sizes && typeof value.sizes === "object" ? value.sizes : {};
	return { order, sizes };
}

export type QueryPersistence = {
	/** Writes pending changes now (page hide). */
	flush(): Promise<void>;
	stop(): void;
};

/**
 * Fills empty queries from this daemon's cache, then keeps the cache current:
 * the workspace list plus page 0 of the most recently opened conversations.
 * The list is restored before this resolves; a conversation is read when its
 * query is created, so a launch never parses transcripts it does not show.
 * Restored data is written with dataUpdatedAt 0 (see isRestoredQueryData).
 * `host` must identify the daemon and its data dir; nothing of another host is
 * read or written.
 */
export async function startQueryPersistence(
	queryClient: QueryClient,
	store: QueryCacheStore,
	host: string,
	buster: string,
): Promise<QueryPersistence> {
	const fullBuster = `${CACHE_SCHEMA_VERSION}|${buster}`;
	const startedAt = Date.now();
	const written = new Map<string, unknown>();
	const restore = (queryKey: readonly unknown[], data: unknown) => {
		if (data === undefined || queryClient.getQueryData(queryKey) !== undefined) return;
		queryClient.setQueryData(queryKey, data, { updatedAt: 0 });
	};

	const records = await store.read(host, [WORKSPACES, INDEX]).catch(() => [] as CacheRecord[]);
	let { order, sizes } = parseIndex(records.find((record) => record.key === INDEX));
	if (records.some((record) => record.buster !== fullBuster)) {
		await store.clear(host).catch(() => undefined);
		order = [];
		sizes = {};
	} else {
		const workspaces = records.find((record) => record.key === WORKSPACES);
		const data = workspaces ? parse(workspaces.json) : undefined;
		if (Array.isArray(data)) restore(workspaceQueryKey, data);
	}

	const restoreConversation = async (sessionId: string) => {
		const [record] = await store.read(host, [CONVERSATION_PREFIX + sessionId]).catch(() => [] as CacheRecord[]);
		if (!record || record.buster !== fullBuster) return;
		const page = parse(record.json);
		if (!page || typeof page !== "object") return;
		// Page 0 is the newest page; its param is always the initial undefined.
		restore([...conversationQueryRoot, sessionId], { pages: [page as ConversationSnapshot], pageParams: [undefined] } satisfies InfiniteData<ConversationSnapshot, number | undefined>);
	};

	const dirty = new Set<string>();
	const pendingDeletes = new Set<string>();
	let indexDirty = false;
	let timer: ReturnType<typeof setTimeout> | undefined;
	let writing = Promise.resolve();
	let stopped = false;

	const schedule = () => {
		if (timer || stopped) return;
		timer = setTimeout(() => {
			timer = undefined;
			void flush();
		}, WRITE_THROTTLE_MS);
	};

	const forget = (key: string) => {
		delete sizes[key];
		written.delete(key);
		dirty.delete(key);
		pendingDeletes.add(key);
		indexDirty = true;
	};

	// Only data fetched from this daemon while this persistence runs is written.
	// That keeps restored data, and data left in memory by a previous daemon,
	// out of the cache.
	const liveValue = (key: string): unknown => {
		const queryKey = key === WORKSPACES ? workspaceQueryKey : [...conversationQueryRoot, key.slice(CONVERSATION_PREFIX.length)];
		const state = queryClient.getQueryState(queryKey);
		if (!state || state.data === undefined || state.dataUpdatedAt < startedAt) return undefined;
		return key === WORKSPACES ? state.data : (state.data as InfiniteData<ConversationSnapshot>).pages?.[0];
	};

	const flush = (): Promise<void> => {
		if (timer) {
			clearTimeout(timer);
			timer = undefined;
		}
		const puts = new Map<string, CacheRecord>();
		for (const key of dirty) {
			const value = liveValue(key);
			// Skip unchanged writes: query results keep their identity when a
			// refetch brings nothing new.
			if (value === undefined || written.get(key) === value) continue;
			const json = JSON.stringify(value);
			written.set(key, value);
			if (json.length > MAX_ENTRY_CHARS) {
				if (key in sizes) forget(key);
				continue;
			}
			sizes[key] = json.length;
			indexDirty = true;
			puts.set(key, { key, buster: fullBuster, json });
		}
		dirty.clear();
		// Oldest conversations go first when the cache is over budget.
		let total = Object.values(sizes).reduce((sum, size) => sum + size, 0);
		while (total > MAX_TOTAL_CHARS && order.length > 0) {
			const key = CONVERSATION_PREFIX + order.pop()!;
			total -= sizes[key] ?? 0;
			forget(key);
			puts.delete(key);
		}
		if (indexDirty) {
			puts.set(INDEX, { key: INDEX, buster: fullBuster, json: JSON.stringify({ order, sizes } satisfies CacheIndex) });
			indexDirty = false;
		}
		const deletes = [...pendingDeletes];
		pendingDeletes.clear();
		if (puts.size === 0 && deletes.length === 0) return writing;
		writing = writing.then(() => store.write(host, [...puts.values()], deletes)).catch(() => undefined);
		return writing;
	};

	const opened = (sessionId: string) => {
		if (order[0] === sessionId) return;
		order = [sessionId, ...order.filter((id) => id !== sessionId)];
		for (const dropped of order.splice(PERSISTED_CONVERSATION_LIMIT)) forget(CONVERSATION_PREFIX + dropped);
		indexDirty = true;
		dirty.add(CONVERSATION_PREFIX + sessionId);
		schedule();
	};

	const unsubscribe = queryClient.getQueryCache().subscribe((event) => {
		const sessionId = conversationSessionId(event.query);
		if (event.type === "added" && sessionId && event.query.state.data === undefined && order.includes(sessionId)) {
			void restoreConversation(sessionId);
			return;
		}
		if (event.type === "observerAdded" && sessionId) {
			opened(sessionId);
			return;
		}
		if (event.type !== "updated" || event.action.type !== "success") return;
		if (isWorkspaceQuery(event.query)) dirty.add(WORKSPACES);
		else if (sessionId && order.includes(sessionId)) dirty.add(CONVERSATION_PREFIX + sessionId);
		else return;
		schedule();
	});

	const flushOnHide = () => {
		if (document.visibilityState === "hidden") void flush();
	};
	const flushNow = () => void flush();
	document.addEventListener("visibilitychange", flushOnHide);
	window.addEventListener("pagehide", flushNow);

	return {
		flush,
		stop: () => {
			stopped = true;
			if (timer) clearTimeout(timer);
			timer = undefined;
			unsubscribe();
			document.removeEventListener("visibilitychange", flushOnHide);
			window.removeEventListener("pagehide", flushNow);
		},
	};
}

const DB_NAME = "ao-query-cache";
const STORE_NAME = "entries";

function request<T>(req: IDBRequest<T>): Promise<T> {
	return new Promise((resolve, reject) => {
		req.onsuccess = () => resolve(req.result);
		req.onerror = () => reject(req.error);
	});
}

/** IndexedDB lives in Electron's userData, which main.ts pins under ~/.ao. */
export function indexedDbQueryCacheStore(): QueryCacheStore {
	const db = new Promise<IDBDatabase>((resolve, reject) => {
		const req = indexedDB.open(DB_NAME, 1);
		req.onupgradeneeded = () => req.result.createObjectStore(STORE_NAME);
		req.onsuccess = () => resolve(req.result);
		req.onerror = () => reject(req.error);
	});
	const id = (host: string, key: string) => `${host}\u0000${key}`;
	const transaction = async (mode: IDBTransactionMode, run: (store: IDBObjectStore) => void) => {
		const tx = (await db).transaction(STORE_NAME, mode);
		run(tx.objectStore(STORE_NAME));
		await new Promise<void>((resolve, reject) => {
			tx.oncomplete = () => resolve();
			tx.onerror = () => reject(tx.error);
			tx.onabort = () => reject(tx.error);
		});
	};
	return {
		read: async (host, keys) => {
			const entries = (await db).transaction(STORE_NAME, "readonly").objectStore(STORE_NAME);
			const records = await Promise.all(keys.map((key) => request(entries.get(id(host, key))) as Promise<CacheRecord | undefined>));
			return records.filter((record): record is CacheRecord => record !== undefined);
		},
		write: (host, puts, deletes) =>
			transaction("readwrite", (entries) => {
				for (const key of deletes) entries.delete(id(host, key));
				for (const record of puts) entries.put(record, id(host, record.key));
			}),
		clear: (host) => transaction("readwrite", (entries) => entries.delete(IDBKeyRange.bound(id(host, ""), `${host}\u0001`, false, true))),
	};
}

let sharedStore: QueryCacheStore | undefined;
let connected: { host: string; persistence: Promise<QueryPersistence> } | undefined;
const hostIds = new Map<string, Promise<string | undefined>>();
let buster: Promise<string> | undefined;

async function fetchHostId(baseUrl: string): Promise<string | undefined> {
	const response = await fetch(`${baseUrl}/api/v1/identity`);
	if (!response.ok) return undefined;
	const body = (await response.json()) as { hostId?: unknown };
	return typeof body.hostId === "string" && body.hostId ? body.hostId : undefined;
}

/**
 * Persists the local daemon's cache under its host id. The daemon keeps that id
 * in its data dir (mobile/identity.json), so another data dir or another
 * machine gets a separate cache. Without an id nothing is cached.
 */
export async function connectQueryPersistence(queryClient: QueryClient, baseUrl: string): Promise<void> {
	if (typeof indexedDB === "undefined") return;
	// Opening the database overlaps the identity request.
	const store = (sharedStore ??= indexedDbQueryCacheStore());
	let hostId = hostIds.get(baseUrl);
	if (!hostId) {
		// A daemon without an identity answers the same way every time; only a
		// failed request is asked again.
		hostId = fetchHostId(baseUrl).catch(() => {
			hostIds.delete(baseUrl);
			return undefined;
		});
		hostIds.set(baseUrl, hostId);
	}
	buster ??= aoBridge.app.getVersion().catch(() => "unknown").then((version) => `${version}|${import.meta.env.VITE_AO_GIT_COMMIT ?? ""}`);
	const [id, cacheBuster] = await Promise.all([hostId, buster]);
	if (!id) return;
	const host = `local:${id}`;
	if (connected?.host !== host) {
		void connected?.persistence.then((persistence) => persistence.stop());
		connected = { host, persistence: startQueryPersistence(queryClient, store, host, cacheBuster) };
	}
	await connected.persistence;
}

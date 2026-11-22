import type { InfiniteData, QueryClient, QueryKey } from "@tanstack/react-query";
import type { ConversationSnapshot } from "../types/conversation";

// What a remote host showed last: its project and session lists and the live page
// of each chat. It shows at once after a restart, also while the host connects or
// is offline, and fresh reads replace it. A local chat's live page is kept too and
// shows while its first read runs. Renderer IndexedDB is in Electron userData,
// which main.ts pins under ~/.ao/electron.
// ponytail: whole live page per chat, newest MAX_CHATS local and remote kept; trim items if pages grow large.

const DB_NAME = "ao-remote-chats";
const STORE = "pages";
const MAX_CHATS = 50;
const WRITE_DELAY_MS = 1_000;

type Entry = { key: string; hostId: string; queryKey: QueryKey; savedAt: number; data: unknown };

let database: Promise<IDBDatabase> | undefined;

function openDatabase(): Promise<IDBDatabase> {
	database ??= new Promise((resolve, reject) => {
		const request = indexedDB.open(DB_NAME, 1);
		request.onupgradeneeded = () => request.result.createObjectStore(STORE, { keyPath: "key" });
		request.onsuccess = () => resolve(request.result);
		request.onerror = () => reject(request.error);
	});
	return database;
}

function done<T>(request: IDBRequest<T>): Promise<T> {
	return new Promise((resolve, reject) => {
		request.onsuccess = () => resolve(request.result);
		request.onerror = () => reject(request.error);
	});
}

/** The host of a query worth keeping: a chat, or the host's project or session list. Empty for a local chat. */
function cachedHost(key: QueryKey): string | undefined {
	if (key[0] === "conversation" && key.length === 2 && typeof key[1] === "string") return "";
	if (typeof key[1] !== "string" || key.length !== 3) return undefined;
	if (key[0] === "remote-conversation" && typeof key[2] === "string") return key[1];
	if (key[0] === "remote-workspaces" && (key[2] === "projects" || key[2] === "sessions")) return key[1];
	return undefined;
}

const isChat = (key: QueryKey) => key[0] === "remote-conversation" || key[0] === "conversation";
const keyHost = (key: string) => cachedHost(JSON.parse(key) as QueryKey);

// Data read from disk or already written is not written again.
const saved = new WeakSet<object>();

let loaded = false;

/**
 * Keeps the cache to the configured hosts. The first call also shows what the disk
 * holds; a query already in memory keeps its own data.
 */
export async function syncRemoteCache(queryClient: QueryClient, hostIds: string[]): Promise<void> {
	const hosts = new Set(["", ...hostIds]);
	for (const query of queryClient.getQueryCache().findAll()) {
		const host = cachedHost(query.queryKey);
		if (host && !hosts.has(host)) queryClient.removeQueries({ queryKey: query.queryKey, exact: true });
	}
	if (typeof indexedDB === "undefined") return;
	try {
		const store = (await openDatabase()).transaction(STORE, "readwrite").objectStore(STORE);
		if (loaded) {
			for (const key of (await done(store.getAllKeys())) as string[]) {
				if (!hosts.has(keyHost(key) ?? "")) store.delete(key);
			}
			return;
		}
		loaded = true;
		const entries = (await done(store.getAll())) as Entry[];
		const oldChats = (local: boolean) => entries
			.filter((entry) => isChat(entry.queryKey) && !entry.hostId === local)
			.sort((a, b) => b.savedAt - a.savedAt)
			.slice(MAX_CHATS);
		const old = new Set([...oldChats(true), ...oldChats(false)]);
		for (const entry of entries) {
			if (!hosts.has(entry.hostId) || old.has(entry)) {
				store.delete(entry.key);
				continue;
			}
			if (!entry.hostId || queryClient.getQueryData(entry.queryKey) !== undefined) continue;
			saved.add(entry.data as object);
			const data = isChat(entry.queryKey) ? { pages: [entry.data], pageParams: [undefined] } : entry.data;
			queryClient.setQueryData(entry.queryKey, data, { updatedAt: entry.savedAt });
		}
	} catch {
		// A cache that cannot be read only costs the first read of each list and chat.
	}
}

/** Drops everything kept for a host, for one that refused this device's password. */
export async function forgetRemoteHost(queryClient: QueryClient, hostId: string): Promise<void> {
	queryClient.removeQueries({ queryKey: ["remote-conversation", hostId] });
	queryClient.removeQueries({ queryKey: ["remote-workspaces", hostId] });
	if (typeof indexedDB === "undefined") return;
	try {
		const store = (await openDatabase()).transaction(STORE, "readwrite").objectStore(STORE);
		for (const key of (await done(store.getAllKeys())) as string[]) {
			if (keyHost(key) === hostId) store.delete(key);
		}
	} catch {
		// Nothing more to drop.
	}
}

/** Saves each kept query shortly after its data changes. Returns the unsubscribe. */
export function persistRemoteCache(queryClient: QueryClient): () => void {
	if (typeof indexedDB === "undefined") return () => {};
	const timers = new Map<string, ReturnType<typeof setTimeout>>();
	const unsubscribe = queryClient.getQueryCache().subscribe((event) => {
		if (event.type !== "updated") return;
		const { queryKey } = event.query;
		const hostId = cachedHost(queryKey);
		if (hostId === undefined) return;
		const key = JSON.stringify(queryKey);
		if (hostId === "" && event.action.type === "fetch" && event.query.state.data === undefined) {
			void openDatabase()
				.then((db) => done(db.transaction(STORE, "readonly").objectStore(STORE).get(key)))
				.then((entry: Entry | undefined) => {
					const { state } = event.query;
					if (!entry || state.data !== undefined || state.fetchStatus !== "fetching") return;
					saved.add(entry.data as object);
					queryClient.setQueryData(queryKey, { pages: [entry.data], pageParams: [undefined] }, { updatedAt: entry.savedAt });
				})
				.catch(() => undefined);
		}
		clearTimeout(timers.get(key));
		timers.set(key, setTimeout(() => {
			timers.delete(key);
			const state = event.query.state.data;
			const data = isChat(queryKey) ? (state as InfiniteData<ConversationSnapshot> | undefined)?.pages[0] : state;
			if (!data || saved.has(data as object)) return;
			saved.add(data as object);
			const entry: Entry = { key, hostId, queryKey, savedAt: Date.now(), data };
			void openDatabase()
				.then((db) => done(db.transaction(STORE, "readwrite").objectStore(STORE).put(entry)))
				.catch(() => saved.delete(data as object));
		}, WRITE_DELAY_MS));
	});
	return () => {
		unsubscribe();
		for (const timer of timers.values()) clearTimeout(timer);
	};
}

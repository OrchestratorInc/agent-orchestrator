import type { InfiniteData, QueryClient, QueryKey } from "@tanstack/react-query";
import type { ConversationSnapshot } from "../types/conversation";
import { done } from "./remote-chat-cache";

const DB_NAME = "ao-local-chats";
const STORE = "pages";
const MAX_CHATS = 50;
const WRITE_DELAY_MS = 1_000;

type Entry = { sessionId: string; savedAt: number; page: ConversationSnapshot };

export const savedConversationQueryKey = (sessionId: string) => ["conversation-saved", sessionId] as const;

let database: Promise<IDBDatabase> | undefined;

function openDatabase(): Promise<IDBDatabase> {
	database ??= new Promise((resolve, reject) => {
		const request = indexedDB.open(DB_NAME, 1);
		request.onupgradeneeded = () => request.result.createObjectStore(STORE, { keyPath: "sessionId" }).createIndex("savedAt", "savedAt");
		request.onsuccess = () => resolve(request.result);
		request.onerror = () => {
			database = undefined;
			reject(request.error);
		};
	});
	return database;
}

const localChat = (key: QueryKey) => (key[0] === "conversation" && key.length === 2 && typeof key[1] === "string" ? key[1] : undefined);

async function pruneOldChats(): Promise<void> {
	const store = (await openDatabase()).transaction(STORE, "readwrite").objectStore(STORE);
	const cursor = store.index("savedAt").openKeyCursor(null, "prev");
	let kept = 0;
	cursor.onsuccess = () => {
		if (!cursor.result) return;
		if (++kept > MAX_CHATS) store.delete(cursor.result.primaryKey);
		cursor.result.continue();
	};
}

export function keepLocalChats(sessionIds: string[]): Promise<void> {
	if (typeof indexedDB === "undefined") return Promise.resolve();
	const keep = new Set(sessionIds);
	return openDatabase()
		.then(async (db) => {
			const store = db.transaction(STORE, "readwrite").objectStore(STORE);
			for (const key of (await done(store.getAllKeys())) as string[]) {
				if (!keep.has(key)) store.delete(key);
			}
		})
		.then(pruneOldChats)
		.catch(() => undefined);
}

export function persistLocalChats(queryClient: QueryClient): () => void {
	if (typeof indexedDB === "undefined") return () => {};
	void pruneOldChats().catch(() => undefined);
	const written = new WeakSet<object>();
	const timers = new Map<string, ReturnType<typeof setTimeout>>();
	const unsubscribe = queryClient.getQueryCache().subscribe((event) => {
		if (event.type !== "updated") return;
		const sessionId = localChat(event.query.queryKey);
		if (!sessionId) return;
		if (event.action.type === "fetch" && event.query.state.data === undefined) {
			void openDatabase()
				.then((db) => done(db.transaction(STORE).objectStore(STORE).get(sessionId)))
				.then((entry: Entry | undefined) => {
					if (entry && event.query.state.data === undefined) queryClient.setQueryData(savedConversationQueryKey(sessionId), entry.page);
				})
				.catch(() => undefined);
		}
		clearTimeout(timers.get(sessionId));
		timers.set(sessionId, setTimeout(() => {
			timers.delete(sessionId);
			const page = (event.query.state.data as InfiniteData<ConversationSnapshot> | undefined)?.pages[0];
			if (!page || written.has(page)) return;
			written.add(page);
			const entry: Entry = { sessionId, savedAt: Date.now(), page };
			void openDatabase()
				.then((db) => done(db.transaction(STORE, "readwrite").objectStore(STORE).put(entry)))
				.catch(() => written.delete(page));
		}, WRITE_DELAY_MS));
	});
	return () => {
		unsubscribe();
		for (const timer of timers.values()) clearTimeout(timer);
	};
}

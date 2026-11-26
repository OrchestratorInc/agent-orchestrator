import { hashKey, type InfiniteData, type QueryClient, type QueryKey } from "@tanstack/react-query";
import { hasOptimisticProjectRemovals } from "../hooks/optimistic-project-removals";
import { workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import type { ConversationSnapshot } from "../types/conversation";
import type { WorkspaceSummary } from "../types/workspace";
import { DEV_BUILD_INFO } from "./dev-build-info";
import { done } from "./remote-chat-cache";

const DB_NAME = "ao-local-chats";
const STORE = "pages";
const MAX_PAGE_CHARS = 4 * 1024 * 1024;
const WRITE_DELAY_MS = 1_000;

type Entry = { sessionId: string; createdAt: string; build: string; page: ConversationSnapshot };

export const savedConversationQueryKey = (sessionId: string) => ["conversation-saved", sessionId] as const;

let database: Promise<IDBDatabase> | undefined;

function openDatabase(): Promise<IDBDatabase> {
	database ??= new Promise((resolve, reject) => {
		const request = indexedDB.open(DB_NAME, 2);
		request.onupgradeneeded = () => {
			if (request.result.objectStoreNames.contains(STORE)) request.result.deleteObjectStore(STORE);
			request.result.createObjectStore(STORE, { keyPath: ["build", "sessionId"] });
		};
		request.onsuccess = () => resolve(request.result);
		request.onerror = () => {
			database = undefined;
			reject(request.error);
		};
	});
	return database;
}

function savableCreatedAt(queryClient: QueryClient, sessionId: string): string | undefined {
	const session = queryClient.getQueryData<WorkspaceSummary[]>(workspaceQueryKey)
		?.flatMap((workspace) => workspace.sessions)
		.find((candidate) => candidate.id === sessionId);
	return session?.kind === "orchestrator" ? undefined : session?.createdAt;
}

const localChat = (key: QueryKey) => (key[0] === "conversation" && key.length === 2 && typeof key[1] === "string" ? key[1] : undefined);

function keepLocalChats(sessionIds: string[]): Promise<void> {
	const keep = new Set(sessionIds);
	return openDatabase()
		.then(async (db) => {
			const store = db.transaction(STORE, "readwrite").objectStore(STORE);
			for (const key of (await done(store.getAllKeys())) as [string, string][]) {
				if (key[0] !== DEV_BUILD_INFO.commit || !keep.has(key[1])) store.delete(key);
			}
		})
		.catch(() => undefined);
}

export function persistLocalChats(queryClient: QueryClient): () => void {
	if (typeof indexedDB === "undefined") return () => {};
	const written = new WeakSet<object>();
	const timers = new Map<string, ReturnType<typeof setTimeout>>();
	const workspacesHash = hashKey(workspaceQueryKey);
	let listed: string | undefined;
	const unsubscribe = queryClient.getQueryCache().subscribe((event) => {
		if (event.type !== "updated") return;
		if (event.query.queryHash === workspacesHash) {
			const workspaces = event.query.state.data as WorkspaceSummary[] | undefined;
			if (event.action.type !== "success" || event.action.manual || !workspaces || hasOptimisticProjectRemovals()) return;
			const sessionIds = workspaces.flatMap((workspace) => workspace.sessions.map((session) => session.id));
			const key = sessionIds.join("\0");
			if (key === listed) return;
			listed = key;
			void keepLocalChats(sessionIds);
			return;
		}
		const sessionId = localChat(event.query.queryKey);
		if (!sessionId) return;
		if (event.action.type === "fetch" && event.query.state.data === undefined) {
			void openDatabase()
				.then((db) => done(db.transaction(STORE).objectStore(STORE).get([DEV_BUILD_INFO.commit, sessionId])))
				.then((entry: Entry | undefined) => {
					const { state } = event.query;
					if (!entry || state.data !== undefined || state.fetchStatus !== "fetching") return;
					if (entry.createdAt !== savableCreatedAt(queryClient, sessionId)) return;
					queryClient.setQueryData(savedConversationQueryKey(sessionId), entry.page);
				})
				.catch(() => undefined);
		}
		if (event.action.type === "success" || event.action.type === "error") {
			queryClient.removeQueries({ queryKey: savedConversationQueryKey(sessionId), exact: true });
		}
		clearTimeout(timers.get(sessionId));
		timers.set(sessionId, setTimeout(() => {
			timers.delete(sessionId);
			const page = queryClient.getQueryData<InfiniteData<ConversationSnapshot>>(event.query.queryKey)?.pages[0];
			const createdAt = savableCreatedAt(queryClient, sessionId);
			if (!page || page.controller.state === "busy" || createdAt === undefined || written.has(page)) return;
			written.add(page);
			const tooLarge = JSON.stringify(page).length > MAX_PAGE_CHARS;
			void openDatabase()
				.then((db) => {
					const store = db.transaction(STORE, "readwrite").objectStore(STORE);
					if (tooLarge) return done(store.delete([DEV_BUILD_INFO.commit, sessionId]));
					return done(store.put({ sessionId, createdAt, build: DEV_BUILD_INFO.commit, page } satisfies Entry));
				})
				.catch(() => written.delete(page));
		}, WRITE_DELAY_MS));
	});
	return () => {
		unsubscribe();
		for (const timer of timers.values()) clearTimeout(timer);
	};
}

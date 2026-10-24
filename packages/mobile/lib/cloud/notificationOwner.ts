import { CloudApiError, type CloudClient, type CloudNotification } from "@aoagents/cloud-client";
import { awaitCloudReplay } from "./conversationReplay";

export type CloudNotificationSnapshot = {
	items: CloudNotification[];
	unreadCount: number;
	loading: boolean;
	refreshing: boolean;
	loadingMore: boolean;
	hasMore: boolean;
	error: string | null;
};
type Clears = Record<string, number | null>;
type NotificationClient = Pick<CloudClient, "listNotifications" | "listNotificationEvents" | "markNotificationRead" | "markAllNotificationsRead">;
type OwnerOptions = {
	client: NotificationClient;
	orgId: string;
	userId: string;
	scopeKey: string;
	isCurrent(): boolean;
	storage: { getItem(key: string): Promise<string | null>; setItem(key: string, value: string): Promise<unknown> };
};

const PAGE_SIZE = 50;
const LIST_INTERVAL = 30_000;
const EVENT_INTERVAL = 2_000;
const emptySnapshot = (): CloudNotificationSnapshot => ({ items: [], unreadCount: 0, loading: true, refreshing: false, loadingMore: false, hasMore: false, error: null });
const timestamp = (value: string) => Date.parse(value) || 0;
const cancellation = () => Object.assign(new Error("Cloud notification request cancelled."), { name: "AbortError" });
const message = (error: unknown) => error instanceof Error ? error.message : "Could not load Cloud notifications.";
const terminal = (error: unknown) => error instanceof CloudApiError && [401, 403, 404, 410].includes(error.status);
const retryDelay = (error: unknown, failures: number) => error instanceof CloudApiError && error.status === 429
	? Math.max(1000, (error.retryAfterSeconds ?? 30) * 1000)
	: Math.min(30_000, 1000 * 2 ** Math.min(failures - 1, 5));

/** Owns one recipient's inbox. React only supplies lifecycle and account ownership. */
export function createCloudNotificationOwner(options: OwnerOptions) {
	let snapshot = emptySnapshot();
	let rows: CloudNotification[] = [];
	let clears: Clears = {};
	let active = false;
	let generation = 0;
	let listGeneration = 0;
	let pageDepth = 1;
	let nextCursor: string | undefined;
	let eventCursor = 0;
	let listController: AbortController | undefined;
	let listPromise: Promise<void> | undefined;
	let initialized = false;
	let initialization = Promise.resolve();
	let storageTail = Promise.resolve();
	let mutationTail = Promise.resolve();
	const listeners = new Set<() => void>();
	const controllers = new Set<AbortController>();
	const timers = new Set<ReturnType<typeof setTimeout>>();
	const storageKey = `ao.cloudNotifications.cleared:${options.scopeKey}`;
	const current = (captured = generation) => active && captured === generation && options.isCurrent();
	const visible = () => rows.filter((item) => !(typeof clears[item.id] === "number" && !(item.status === "unread" && timestamp(item.updatedAt) > clears[item.id]!)));
	const publish = (patch: Partial<CloudNotificationSnapshot> = {}) => {
		if (!current()) return;
		snapshot = { ...snapshot, ...patch, items: visible() };
		for (const listener of listeners) listener();
	};
	function requireCurrent() { if (!current()) throw cancellation(); }
	function persistClears() {
		// Each write captures its own value; completion order cannot restore an older map.
		const rank = (marker: number | null) => marker === null ? Number.POSITIVE_INFINITY : marker;
		const retained = Object.entries(clears).sort(([, left], [, right]) => rank(right) - rank(left)).slice(0, 200);
		const raw = JSON.stringify(Object.fromEntries(retained));
		const write = storageTail.then(() => options.storage.setItem(storageKey, raw));
		storageTail = write.then(() => undefined, () => undefined);
		return write;
	}
	function validateRow(item: CloudNotification) {
		if (item.source !== "cloud" || item.orgId !== options.orgId || item.recipientUserId !== options.userId) throw new Error("Cloud notification recipient changed. Refresh your account.");
	}
	function adoptRows(incoming: CloudNotification[]) {
		rows = incoming;
		let revived = false;
		for (const item of rows) {
			const marker = clears[item.id];
			if (typeof marker === "number" && item.status === "unread" && timestamp(item.updatedAt) > marker) {
				clears[item.id] = null;
				revived = true;
			}
		}
		if (revived) void persistClears().catch(() => {});
	}
	function later(run: () => void, delay: number, captured: number) {
		if (!current(captured)) return;
		const timer = setTimeout(() => { timers.delete(timer); if (current(captured)) run(); }, delay);
		timers.add(timer);
	}
	async function request<T>(run: (signal: AbortSignal) => Promise<T>): Promise<T> {
		requireCurrent();
		const captured = generation;
		const controller = new AbortController();
		controllers.add(controller);
		try {
			const answer = await awaitCloudReplay(run(controller.signal), controller.signal);
			if (!current(captured)) throw cancellation();
			return answer;
		} finally { controllers.delete(controller); }
	}
	function load(mode: "refresh" | "more"): Promise<void> {
		if (!current()) return Promise.resolve();
		if (mode === "more" && (!nextCursor || snapshot.loadingMore || snapshot.refreshing || snapshot.loading)) return Promise.resolve();
		if (mode === "refresh" && listPromise && !snapshot.loadingMore) return listPromise;
		listController?.abort();
		const controller = new AbortController();
		controllers.add(controller);
		listController = controller;
		const captured = generation;
		const sequence = ++listGeneration;
		const isCurrent = () => current(captured) && listGeneration === sequence && !controller.signal.aborted;
		publish(mode === "more" ? { loadingMore: true } : { refreshing: !snapshot.loading, loadingMore: false });
		let promise!: Promise<void>;
		promise = (async () => {
			try {
				let cursor = mode === "more" ? nextCursor : undefined;
				const incoming = mode === "more" ? [...rows] : [];
				const seenCursors = new Set<string>();
				const depth = mode === "more" ? 1 : pageDepth;
				const oldest = mode === "refresh" ? rows.at(-1) : undefined;
				let reachedBoundary = !oldest;
				let fetched = 0;
				let unreadCount = snapshot.unreadCount;
				for (; fetched < depth || !reachedBoundary;) {
					const page = await awaitCloudReplay(options.client.listNotifications(options.orgId, { status: "all", limit: PAGE_SIZE, cursor, signal: controller.signal }), controller.signal);
					if (!isCurrent()) return;
					for (const item of page.items) validateRow(item);
					if (!Number.isSafeInteger(page.unreadCount) || page.unreadCount < 0) throw new Error("Cloud unread count is invalid.");
					unreadCount = page.unreadCount;
					incoming.push(...page.items);
					// New rows can push the old tail into a later page. Keep the user's
					// loaded history window while replacing it with authoritative rows.
					if (oldest && page.items.some((item) => item.id === oldest.id || timestamp(item.createdAt) < timestamp(oldest.createdAt))) reachedBoundary = true;
					fetched++;
					const following = page.page.hasMore ? page.page.nextCursor : undefined;
					if (page.page.hasMore && (!following || following === cursor || seenCursors.has(following))) throw new Error("Cloud notification cursor did not advance.");
					if (following) seenCursors.add(following);
					cursor = following;
					if (!cursor) break;
				}
				if (!isCurrent()) return;
				const unique = new Map<string, CloudNotification>();
				for (const item of incoming) unique.set(item.id, item);
				adoptRows([...unique.values()].sort((a, b) => timestamp(b.createdAt) - timestamp(a.createdAt) || a.id.localeCompare(b.id)));
				pageDepth = mode === "more" ? pageDepth + fetched : fetched;
				nextCursor = cursor;
				publish({ unreadCount, hasMore: !!cursor, error: null });
			} catch (error) {
				if (!isCurrent()) return;
				publish({ error: message(error) });
				throw error;
			} finally {
				controllers.delete(controller);
				if (isCurrent()) publish({ loading: false, refreshing: false, loadingMore: false });
				if (listPromise === promise) listPromise = undefined;
			}
		})();
		listPromise = promise;
		return promise;
	}
	const refresh = () => initialized ? load("refresh") : initialization.then(() => load("refresh"));
	async function refreshAfterChange() {
		const captured = generation;
		// The existing request may have captured server state before a mutation/event.
		await listPromise?.catch(() => {});
		if (!current(captured)) throw cancellation();
		await refresh();
	}
	async function pollList(captured: number, failures = 0): Promise<void> {
		try {
			await refresh();
			if (current(captured)) later(() => void pollList(captured), LIST_INTERVAL, captured);
		} catch (error) {
			if (current(captured) && !terminal(error)) later(() => void pollList(captured, failures + 1), retryDelay(error, failures + 1), captured);
		}
	}
	async function pollEvents(captured: number, failures = 0): Promise<void> {
		let changed = false;
		try {
			for (;;) {
				const after = eventCursor;
				const page = await request((signal) => options.client.listNotificationEvents(options.orgId, { after, signal }));
				if (!current(captured)) return;
				for (const event of page.items) {
					if (!Number.isSafeInteger(event.sequence) || event.sequence < 1 || event.orgId !== options.orgId || event.recipientUserId !== options.userId) throw new Error("Cloud notification event is invalid.");
					validateRow(event.notification);
				}
				const next = Math.max(after, ...page.items.map((event) => event.sequence));
				if (page.hasMore && next <= after) throw new Error("Cloud notification event cursor did not advance.");
				changed ||= next > after;
				eventCursor = next;
				if (!page.hasMore) break;
			}
			if (changed) await refreshAfterChange();
			later(() => void pollEvents(captured), EVENT_INTERVAL, captured);
		} catch (error) {
			if (!current(captured)) return;
			publish({ error: message(error) });
			if (!terminal(error)) later(() => void pollEvents(captured, failures + 1), retryDelay(error, failures + 1), captured);
		}
	}
	function mutate(run: () => Promise<void>): Promise<void> {
		const captured = generation;
		const ready = initialization;
		const result = mutationTail.then(async () => { await ready; if (!current(captured)) throw cancellation(); await run(); });
		mutationTail = result.catch(() => {});
		return result;
	}
	async function acknowledge(ids: readonly string[]) {
		for (const id of new Set(ids)) {
			try { await request((signal) => options.client.markNotificationRead(options.orgId, id, { signal })); }
			catch (error) { if (!(error instanceof CloudApiError && error.status === 404)) throw error; }
		}
	}
	return {
		getSnapshot: () => snapshot,
		subscribe(listener: () => void) { listeners.add(listener); return () => { listeners.delete(listener); }; },
		start() {
			if (active || !options.isCurrent()) return;
			active = true;
			const captured = ++generation;
			publish({ refreshing: !snapshot.loading, loadingMore: false });
			initialization = (async () => {
				if (initialized) return;
				try {
					const raw = await request(() => options.storage.getItem(storageKey));
					if (!current(captured)) return;
					const parsed: unknown = raw ? JSON.parse(raw) : {};
					clears = parsed && typeof parsed === "object" && !Array.isArray(parsed)
						? Object.fromEntries(Object.entries(parsed).filter(([, value]) => value === null || (typeof value === "number" && Number.isFinite(value)))) : {};
				} catch { /* Storage failure must not make the inbox unavailable. */ }
				if (current(captured)) initialized = true;
			})();
			void initialization.then(() => {
				if (!current(captured)) return;
				publish();
				void pollList(captured);
				void pollEvents(captured);
			});
		},
		stop() {
			active = false;
			generation++;
			for (const controller of controllers) controller.abort();
			controllers.clear();
			for (const timer of timers) clearTimeout(timer);
			timers.clear();
			listPromise = undefined;
		},
		refresh,
		loadMore: () => load("more"),
		markRead(ids: readonly string[]) { return mutate(async () => { await acknowledge(ids); await refreshAfterChange().catch(() => {}); }); },
		markAllRead() { return mutate(async () => { await request((signal) => options.client.markAllNotificationsRead(options.orgId, { signal })); await refreshAfterChange().catch(() => {}); }); },
		clearOne(item: CloudNotification) { return mutate(async () => {
			validateRow(item);
			const marker = timestamp(item.updatedAt);
			const previous = clears[item.id];
			clears[item.id] = marker;
			publish();
			try {
				if (item.status === "unread") await acknowledge([item.id]);
				requireCurrent();
				await persistClears();
			} catch (error) {
				if (clears[item.id] === marker) {
					if (previous === undefined) delete clears[item.id]; else clears[item.id] = previous;
				}
				publish();
				throw error;
			}
			await refreshAfterChange().catch(() => {});
		}); },
	};
}

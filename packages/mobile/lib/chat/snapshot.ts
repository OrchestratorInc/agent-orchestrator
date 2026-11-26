import { sameJson } from "../keepUnchanged";
import type { ConversationItem, ConversationMessage, ConversationSnapshot, ConversationTurn } from "./types";

export type ConversationPage = ConversationSnapshot;

export type ConversationPageCache = {
	get(key: string): ConversationPage[] | undefined;
	set(key: string, pages: ConversationPage[]): void;
};

/**
 * Small memory-only cache for instant repeat opens. It deliberately retains
 * only the authoritative live page: user-requested historical pages can be
 * large and are cheap to request again when the user asks for them.
 */
export function createConversationPageCache(
	maxEntries: number,
	maxWeight = Number.POSITIVE_INFINITY,
	weigh: (page: ConversationPage) => number = estimateRetainedBytes,
): ConversationPageCache {
	const entries = new Map<string, { pages: ConversationPage[]; weight: number }>();
	const limit = Math.max(1, maxEntries);
	const weightLimit = Math.max(0, maxWeight);
	let retainedWeight = 0;
	return {
		get(key) {
			const entry = entries.get(key);
			if (!entry) return undefined;
			entries.delete(key);
			entries.set(key, entry);
			return [...entry.pages];
		},
		set(key, pages) {
			const live = pages[0];
			const previous = entries.get(key);
			if (previous) retainedWeight -= previous.weight;
			entries.delete(key);
			if (!live) {
				return;
			}
			const weight = Math.max(0, weigh(live));
			if (weight > weightLimit) return;
			entries.set(key, { pages: [live], weight });
			retainedWeight += weight;
			while (entries.size > limit || retainedWeight > weightLimit) {
				const oldest = entries.keys().next().value as string | undefined;
				if (oldest === undefined) break;
				retainedWeight -= entries.get(oldest)?.weight ?? 0;
				entries.delete(oldest);
			}
		},
	};
}

export function createMobileConversationPageCache(
	weigh: (page: ConversationPage) => number = estimatePageBytes,
): ConversationPageCache {
	return createConversationPageCache(16, 2 * 1024 * 1024, weigh);
}

const rowBytes = new WeakMap<object, number>();

/**
 * A page's estimated size, each row's weighed once: a refresh keeps every row
 * that did not change as the same object (reuseUnchangedRows), and a streaming
 * reply is read several times a second, each read walking the whole page.
 */
export function estimatePageBytes(page: ConversationPage): number {
	let bytes = estimateRetainedBytes({ ...page, items: [], turns: [] });
	for (const rows of [page.items, page.turns]) {
		for (const row of rows) {
			let size = rowBytes.get(row);
			if (size === undefined) {
				size = estimateRetainedBytes(row);
				rowBytes.set(row, size);
			}
			bytes += size;
		}
	}
	return bytes;
}

function estimateRetainedBytes(value: unknown): number {
	const pending: unknown[] = [value];
	const seen = new Set<object>();
	let bytes = 0;
	while (pending.length) {
		const current = pending.pop();
		if (typeof current === "string") {
			bytes += current.length * 2;
		} else if (typeof current === "number" || typeof current === "boolean") {
			bytes += 8;
		} else if (current && typeof current === "object" && !seen.has(current)) {
			seen.add(current);
			for (const [key, child] of Object.entries(current)) {
				bytes += key.length * 2 + 16;
				pending.push(child);
			}
		}
	}
	return bytes;
}

/**
 * Remembers `next` for `key` when it differs from what was shown, or when the
 * cache no longer holds its live page (another conversation evicted it while
 * this one sat unchanged). A refresh that changed nothing then skips the write
 * without leaving the cache short of this conversation.
 */
export function cacheConversationPages(cache: ConversationPageCache, key: string, previous: ConversationPage[], next: ConversationPage[]): void {
	if (next !== previous || cache.get(key)?.[0] !== next[0]) cache.set(key, next);
}

export function cachedConversationState(cache: ConversationPageCache, key: string): {
	pages: ConversationPage[];
	loading: boolean;
} {
	const pages = cache.get(key) ?? [];
	return { pages, loading: pages.length === 0 };
}

/**
 * A rollback rewrites the projection by removing rows. A live first page cannot
 * carry tombstones for rows cached in older pages, so those pages must be
 * discarded before the authoritative refresh and may be paged in again later.
 */
export function discardHistoricalPages(pages: ConversationPage[]): ConversationPage[] {
	return pages.length ? pages.slice(0, 1) : pages;
}

/**
 * Puts a freshly fetched live page in front of the loaded history. History is
 * kept only while it describes the same conversation and active branch: an edit
 * (even one whose delivery came back uncertain) or a branch switch keeps the
 * conversation id, so a changed branch is what says the older pages now hold
 * rows the replacement conversation does not contain.
 */
export function withLivePage(pages: ConversationPage[], live: ConversationPage): ConversationPage[] {
	const current = pages[0];
	if (!current) return [live];
	if (current.conversationId && current.conversationId !== live.conversationId) return [live];
	if (current.activeBranchId && live.activeBranchId && current.activeBranchId !== live.activeBranchId) return [live];
	const shared = reuseUnchangedRows(pages, live);
	// Nothing moved: the same pages, so the screen does not render again.
	if (shared === current) return pages;
	return [shared, ...withSlidRows(pages, live)];
}

/**
 * The history behind a new live page, holding on to the rows the last live page
 * had that this one does not because newer rows pushed them past its limit.
 *
 * The live page is the newest rows only, so once a conversation outgrew it,
 * every new row dropped the oldest from the top of the list. The list's rows
 * all moved up an index, its measurements no longer matched them, and it
 * mounted and dropped whole turns around the one on screen at every new turn.
 * The rows stay as history instead, as a loaded older page's do; the cursor
 * for loading older rows stays where it was.
 */
function withSlidRows(pages: ConversationPage[], live: ConversationPage): ConversationPage[] {
	const current = pages[0];
	const history = pages.slice(1);
	// Only a live page with rows before it has moved past any: one that holds the
	// whole conversation is authoritative about what is no longer in it.
	const floor = live.items[0]?.sequence;
	if (!current || !live.hasMoreBefore || floor === undefined) return history;
	const slid = current.items.filter((item) => item.sequence < floor);
	if (!slid.length) return history;
	const liveTurns = new Set(live.turns.map((turn) => turn.id));
	const slidTurns = current.turns.filter((turn) => !liveTurns.has(turn.id));
	const behind = history[0];
	if (!behind) return [{ ...current, items: slid, turns: slidTurns }];
	const held = new Set(behind.items.map(itemKey));
	const heldTurns = new Set(behind.turns.map((turn) => turn.id));
	return [{
		...behind,
		items: [...behind.items, ...slid.filter((item) => !held.has(itemKey(item)))],
		turns: [...behind.turns, ...slidTurns.filter((turn) => !heldTurns.has(turn.id))],
	}, ...history.slice(1)];
}

/**
 * The fresh live page, with every item and turn that has not changed replaced
 * by the object already on screen; the page already on screen when nothing at
 * all changed.
 *
 * Each refresh parses a new page, so every row used to arrive as a new object
 * and every row's memo missed: one streamed word re-rendered (and re-parsed
 * the Markdown of) the whole visible transcript. With the old objects kept,
 * only the rows that really changed render.
 */
export function reuseUnchangedRows(pages: ConversationPage[], live: ConversationPage): ConversationPage {
	const current = pages[0];
	if (!current) return live;
	const items = new Map<string, ConversationItem>();
	const turns = new Map<string, ConversationTurn>();
	for (const page of pages) {
		for (const item of page.items) if (!items.has(itemKey(item))) items.set(itemKey(item), item);
		for (const turn of page.turns) if (!turns.has(turn.id)) turns.set(turn.id, turn);
	}
	let changed = live.items.length !== current.items.length || live.turns.length !== current.turns.length;
	const nextItems = live.items.map((item, index) => {
		const previous = items.get(itemKey(item));
		const kept = previous && sameJson(previous, item) ? previous : item;
		if (kept !== current.items[index]) changed = true;
		return kept;
	});
	const nextTurns = live.turns.map((turn, index) => {
		const previous = turns.get(turn.id);
		const kept = previous && sameJson(previous, turn) ? previous : turn;
		if (kept !== current.turns[index]) changed = true;
		return kept;
	});
	if (!changed && sameJson({ ...live, items: [], turns: [] }, { ...current, items: [], turns: [] })) return current;
	return { ...live, items: nextItems, turns: nextTurns };
}

/**
 * The streaming replies whose text is all that changed from `previous` to
 * `next`, or null when anything else did: a row came or went, a turn moved, a
 * reply finished, settings or state changed. Only the newest sequence may move
 * alongside the text. Rows `next` shares with `previous` are the same objects
 * (see reuseUnchangedRows), so this is a pass over references, not a deep compare.
 */
export function streamingTextChanges(previous: ConversationPage, next: ConversationPage): ConversationMessage[] | null {
	if (previous.items.length !== next.items.length || previous.turns.length !== next.turns.length) return null;
	if (next.turns.some((turn, index) => turn !== previous.turns[index])) return null;
	const grown: ConversationMessage[] = [];
	for (let index = 0; index < next.items.length; index++) {
		const after = next.items[index];
		const before = previous.items[index];
		if (after === before) continue;
		if (after.kind !== "message" || before.kind !== "message" || after.id !== before.id || !after.streaming || !before.streaming) return null;
		if (!sameJson({ ...before, text: "", revision: 0 }, { ...after, text: "", revision: 0 })) return null;
		grown.push(after);
	}
	if (!grown.length) return null;
	const shell = (page: ConversationPage) => ({ ...page, items: [], turns: [], latestSequence: 0 });
	return sameJson(shell(previous), shell(next)) ? grown : null;
}

function itemKey(item: ConversationItem): string {
	return `${item.kind}:${item.id}`;
}

/** Whether an older page still belongs behind the live one; a branch switch while it loaded makes it stale. */
export function olderPageFits(pages: ConversationPage[], older: ConversationPage): boolean {
	const live = pages[0];
	return Boolean(live) && (!live.activeBranchId || !older.activeBranchId || live.activeBranchId === older.activeBranchId);
}

/** Merge historical pages behind the current live page without duplicating revisions. */
export function mergeConversationPages(pages: ConversationPage[]): ConversationSnapshot | undefined {
	const live = pages[0];
	if (!live) return undefined;
	const items = new Map<string, ConversationItem>();
	const turns = new Map<string, ConversationTurn>();
	// Oldest first, so newer pages replace the same item/turn with its latest
	// revision when a page boundary overlaps a streaming update.
	for (const page of [...pages].reverse()) {
		for (const item of page.items) items.set(`${item.kind}:${item.id}`, item);
		for (const turn of page.turns) turns.set(turn.id, turn);
	}
	const oldest = pages[pages.length - 1] ?? live;
	return {
		...live,
		oldestSequence: oldest.oldestSequence,
		hasMoreBefore: oldest.hasMoreBefore,
		items: [...items.values()].sort((a, b) => a.sequence - b.sequence),
		turns: [...turns.values()].sort((a, b) => a.requestedAt.localeCompare(b.requestedAt)),
	};
}

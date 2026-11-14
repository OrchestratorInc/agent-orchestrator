import AsyncStorage from "@react-native-async-storage/async-storage";
import type { ConversationPage } from "./snapshot";
import { DEFAULT_CONFIG, hostMachineIdentity, machineIdentity } from "../config";
import type { HostMetadata } from "../hosts";

// Each recent chat's newest page, saved on the phone. The in-memory page cache
// makes reopening a chat instant, but it dies with the app: after the phone
// closed the app in the background, every chat opened on "Loading
// conversation…" until the machine answered, which on a slow link takes
// seconds. A chat with nothing in memory now shows its saved page at once,
// and its first read replaces it.

const INDEX_KEY = "ao.chat.saved.v1";
const PAGE_PREFIX = "ao.chat.saved.v1.";
/** How many chats keep a saved page; the least recently saved go first. */
export const SAVED_CONVERSATIONS = 12;
/** A page larger than this many UTF-8 bytes is not saved (Android keeps one value under ~2 MB). */
export const SAVED_PAGE_MAX_BYTES = 400_000;
/**
 * All saved pages together stay under this many UTF-8 bytes; the least
 * recently saved chats go first. Android's AsyncStorage holds 6 MB in all,
 * shared with everything else the app keeps, and a write past it fails.
 */
export const SAVED_TOTAL_MAX_BYTES = 2_000_000;
/** A saved page older than this is not shown. */
export const SAVED_PAGE_KEEP_MS = 7 * 24 * 60 * 60 * 1000;

type Saved = { v: 1; savedAt: number; page: ConversationPage };
/** A saved chat in the index: its key and its page's size in UTF-8 bytes. */
export type SavedEntry = { key: string; bytes: number };

const FIRST_WRITE_DELAY_MS = 2_000;

export async function loadSavedConversation(key: string, now = Date.now()): Promise<ConversationPage | null> {
	try {
		const raw = await AsyncStorage.getItem(PAGE_PREFIX + key);
		if (!raw) return null;
		const saved = JSON.parse(raw) as Partial<Saved>;
		if (saved.v !== 1 || typeof saved.savedAt !== "number" || now - saved.savedAt >= SAVED_PAGE_KEEP_MS) return null;
		const page = saved.page;
		return page && Array.isArray(page.items) && Array.isArray(page.turns) ? page : null;
	} catch {
		return null;
	}
}

/**
 * The size of `text` in UTF-8, which is what storage holds: `length` counts
 * UTF-16 units, so a page of CJK text took three times the room it counted.
 */
export function utf8Bytes(text: string): number {
	let bytes = 0;
	for (let i = 0; i < text.length; i++) {
		const code = text.charCodeAt(i);
		if (code < 0x80) bytes += 1;
		else if (code < 0x800) bytes += 2;
		else if (code >= 0xd800 && code <= 0xdbff && i + 1 < text.length && (text.charCodeAt(i + 1) & 0xfc00) === 0xdc00) {
			bytes += 4;
			i++;
		} else bytes += 3;
	}
	return bytes;
}

/**
 * The entries to keep after saving `key` (`bytes` large), newest first, and
 * the keys to drop: at most `limit` chats and `budget` bytes in all, the
 * least recently saved going first.
 */
export function nextIndex(
	index: readonly SavedEntry[],
	key: string,
	bytes: number,
	limit = SAVED_CONVERSATIONS,
	budget = SAVED_TOTAL_MAX_BYTES,
): { keep: SavedEntry[]; drop: string[] } {
	const seen = new Set([key]);
	const ordered = [{ key, bytes }, ...index.filter((entry) => !seen.has(entry.key) && Boolean(seen.add(entry.key)))];
	const keep: SavedEntry[] = [];
	let total = 0;
	for (const entry of ordered) {
		if (keep.length >= limit || (keep.length > 0 && total + entry.bytes > budget)) break;
		keep.push(entry);
		total += entry.bytes;
	}
	return { keep, drop: ordered.slice(keep.length).map((entry) => entry.key) };
}

async function readIndex(): Promise<SavedEntry[]> {
	try {
		const raw = await AsyncStorage.getItem(INDEX_KEY);
		const parsed: unknown = raw ? JSON.parse(raw) : [];
		if (!Array.isArray(parsed)) return [];
		return parsed.flatMap((entry): SavedEntry[] => {
			if (Array.isArray(entry) && typeof entry[0] === "string" && typeof entry[1] === "number") return [{ key: entry[0], bytes: entry[1] }];
			// An entry with no size counts as the largest page there can be.
			if (typeof entry === "string") return [{ key: entry, bytes: SAVED_PAGE_MAX_BYTES }];
			return [];
		});
	} catch {
		return []; // a broken index starts over, and the sweep below clears what it named
	}
}

const writeIndex = (entries: readonly SavedEntry[]) => AsyncStorage.setItem(INDEX_KEY, JSON.stringify(entries.map((entry) => [entry.key, entry.bytes])));

/** The saved chats on the phone, by key, whether the index names them or not. */
async function savedKeysOnPhone(): Promise<string[]> {
	return (await AsyncStorage.getAllKeys()).filter((key) => key.startsWith(PAGE_PREFIX)).map((key) => key.slice(PAGE_PREFIX.length));
}

async function writeSaved(key: string, page: ConversationPage, now: number): Promise<void> {
	const text = JSON.stringify({ v: 1, savedAt: now, page } satisfies Saved);
	const bytes = utf8Bytes(text);
	if (bytes > SAVED_PAGE_MAX_BYTES) return;
	const { keep, drop } = nextIndex(await readIndex(), key, bytes);
	// Pages no kept entry names go too: the dropped ones, and any an interrupted
	// save or removal left behind with no entry, which nothing else would clear.
	const kept = new Set(keep.map((entry) => entry.key));
	let stale = drop;
	try {
		stale = [...new Set([...drop, ...(await savedKeysOnPhone())])].filter((saved) => !kept.has(saved));
	} catch { /* the dropped ones at least */ }
	// Room first: on a phone near its limit the page fits only once these are gone.
	if (stale.length) await AsyncStorage.multiRemove(stale.map((saved) => PAGE_PREFIX + saved));
	// The index before the page, so a save cut short leaves an entry with no
	// page (which loads as nothing), never a page outside the budget.
	await writeIndex(keep);
	await AsyncStorage.setItem(PAGE_PREFIX + key, text);
}

/** Removes every saved chat `matches` picks, listed in the index or not. */
async function removeSaved(matches: (key: string) => boolean): Promise<void> {
	const index = await readIndex();
	const keys = new Set(index.map((entry) => entry.key));
	try {
		for (const key of await savedKeysOnPhone()) keys.add(key);
	} catch { /* the ones the index names at least */ }
	const doomed = [...keys].filter(matches);
	if (doomed.length) await AsyncStorage.multiRemove(doomed.map((key) => PAGE_PREFIX + key));
	await writeIndex(index.filter((entry) => !matches(entry.key)));
}

/**
 * Saves each chat's newest page at most every `everyMs` (a streaming reply
 * changes it several times a second), and every waiting one at once on
 * `flush` (leaving the app). One write at a time, in order.
 */
export function createConversationSaver(
	write: (key: string, page: ConversationPage, now: number) => Promise<void> = writeSaved,
	everyMs = 5_000,
	remove: (matches: (key: string) => boolean) => Promise<void> = removeSaved,
) {
	const pending = new Map<string, ConversationPage>();
	const lastWrite = new Map<string, number>();
	const timers = new Map<string, ReturnType<typeof setTimeout>>();
	let chain = Promise.resolve();
	const flushOne = (key: string) => {
		clearTimeout(timers.get(key));
		timers.delete(key);
		const page = pending.get(key);
		if (!page) return;
		pending.delete(key);
		const now = Date.now();
		lastWrite.set(key, now);
		chain = chain.then(() => write(key, page, now)).catch(() => {});
	};
	return {
		schedule(key: string, page: ConversationPage) {
			if (!key) return;
			pending.set(key, page);
			if (timers.has(key)) return;
			// Never at once: a chat that just opened, or is streaming, settles first.
			timers.set(key, setTimeout(() => flushOne(key), Math.max(FIRST_WRITE_DELAY_MS, (lastWrite.get(key) ?? 0) + everyMs - Date.now())));
		},
		flush() {
			for (const key of [...pending.keys()]) flushOne(key);
			return chain;
		},
		/**
		 * Drops the saved chats of `machines` (every machine for null): the writes
		 * still waiting are cancelled, and the removal runs after any write already
		 * under way, so none of them lands after it.
		 */
		forget(machines: readonly string[] | null): Promise<void> {
			const matches = (key: string) => machines === null || machines.some((machine) => key.startsWith(`${machine}/`));
			for (const [key, timer] of [...timers]) {
				if (!matches(key)) continue;
				clearTimeout(timer);
				timers.delete(key);
			}
			for (const key of [...pending.keys()]) if (matches(key)) pending.delete(key);
			for (const key of [...lastWrite.keys()]) if (matches(key)) lastWrite.delete(key);
			const removal = chain.then(() => remove(matches));
			chain = removal.catch(() => {});
			return removal;
		},
	};
}

export const conversationSaver = createConversationSaver();

/** Every key a machine's chats may be saved under (see conversationPageCacheKey). */
export function savedConversationMachines(host: HostMetadata): string[] {
	const machines = new Set<string>();
	if (host.id) machines.add(hostMachineIdentity(host.id));
	for (const endpoint of host.endpoints) {
		machines.add(machineIdentity({ ...DEFAULT_CONFIG, host: endpoint.host, httpPort: String(endpoint.port), secure: endpoint.secure }));
	}
	return [...machines];
}

/** Removes a forgotten machine's chats from the phone; with no machine named, every saved chat. */
export function forgetSavedConversations(host: HostMetadata | null): Promise<void> {
	return conversationSaver.forget(host ? savedConversationMachines(host) : null);
}

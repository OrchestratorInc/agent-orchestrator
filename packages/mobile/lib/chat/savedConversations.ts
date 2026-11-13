import AsyncStorage from "@react-native-async-storage/async-storage";
import type { ConversationPage } from "./snapshot";

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
/** A page larger than this is not saved (Android keeps one value under ~2 MB). */
export const SAVED_PAGE_MAX_CHARS = 400_000;
/** A saved page older than this is not shown. */
export const SAVED_PAGE_KEEP_MS = 7 * 24 * 60 * 60 * 1000;

type Saved = { v: 1; savedAt: number; page: ConversationPage };

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

/** The keys to keep after saving `key`, newest first, and the ones to drop. */
export function nextIndex(index: readonly string[], key: string, limit = SAVED_CONVERSATIONS): { keep: string[]; drop: string[] } {
	const ordered = [key, ...index.filter((entry) => entry !== key)];
	return { keep: ordered.slice(0, limit), drop: ordered.slice(limit) };
}

async function writeSaved(key: string, page: ConversationPage, now: number): Promise<void> {
	const text = JSON.stringify({ v: 1, savedAt: now, page } satisfies Saved);
	if (text.length > SAVED_PAGE_MAX_CHARS) return;
	let index: string[] = [];
	try {
		const raw = await AsyncStorage.getItem(INDEX_KEY);
		const parsed: unknown = raw ? JSON.parse(raw) : [];
		if (Array.isArray(parsed)) index = parsed.filter((entry): entry is string => typeof entry === "string");
	} catch { /* a broken index starts over */ }
	const { keep, drop } = nextIndex(index, key);
	await AsyncStorage.setItem(PAGE_PREFIX + key, text);
	await AsyncStorage.setItem(INDEX_KEY, JSON.stringify(keep));
	if (drop.length) await AsyncStorage.multiRemove(drop.map((entry) => PAGE_PREFIX + entry));
}

/**
 * Saves each chat's newest page at most every `everyMs` (a streaming reply
 * changes it several times a second), and every waiting one at once on
 * `flush` (leaving the app). One write at a time, in order.
 */
export function createConversationSaver(write: (key: string, page: ConversationPage, now: number) => Promise<void> = writeSaved, everyMs = 5_000) {
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
	};
}

export const conversationSaver = createConversationSaver();

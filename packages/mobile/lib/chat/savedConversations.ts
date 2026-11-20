import AsyncStorage from "@react-native-async-storage/async-storage";
import { AESEncryptionKey, AESSealedData, aesDecryptAsync, aesEncryptAsync } from "expo-crypto";
import { Directory, File, Paths } from "expo-file-system";
import * as SecureStore from "expo-secure-store";
import type { ConversationPage } from "./snapshot";
import type { ConversationItem, ConversationTurn } from "./types";
import { DEFAULT_CONFIG, hostMachineIdentity, machineIdentity } from "../config";
import type { HostMetadata } from "../hosts";

// Each recent chat's newest page, saved on the phone. The in-memory page cache
// makes reopening a chat instant, but it dies with the app: after the phone
// closed the app in the background, every chat opened on "Loading
// conversation…" until the machine answered, which on a slow link takes
// seconds. A chat with nothing in memory now shows its saved page at once,
// and its first read replaces it.
//
// The pages are a cache, so they live in the OS cache directory, one file per
// chat under `saved-chats/` (as artifactDownload keeps downloads), not in
// AsyncStorage with the data the user keeps:
// - device backups skip the cache directory by OS rule (Android's cacheDir,
//   iOS's Library/Caches), so agent output, which can carry secrets, never
//   leaves the phone in one, and the rest of the app is backed up as before;
// - they share no storage limit with pairings, drafts or pending sends;
// - the OS may clear them when storage runs low; a missing file loads as
//   nothing saved.
// Each file is encrypted with AES-GCM under a key kept in the secure store and
// bound to its chat, so a copy taken off the phone cannot be read. A file that
// does not decrypt (cut short, another chat's, made under a key since deleted)
// loads as nothing saved. With no key to be had, nothing is saved.
//
// A page is replaced by writing it whole beside the old one (`.page.tmp`),
// then moving it over the old one. An in-place write would truncate the old
// page first (Android opens it for overwrite), so a kill mid-write would lose
// the last offline transcript. expo-file-system's move with `overwrite`
// deletes the destination just before renaming onto it, so a kill inside that
// one native call can leave only the `.tmp`, which is whole by then: a read
// falls back to it when the page is missing or does not open.

/** Where older builds kept the pages; cleared once a launch. */
const LEGACY_INDEX_KEY = "ao.chat.saved.v1";
const LEGACY_PAGE_PREFIX = `${LEGACY_INDEX_KEY}.`;
const DIRECTORY_NAME = "saved-chats";
const FILE_SUFFIX = ".page";
/** A replacement page being written, moved over its page once whole. */
const TMP_SUFFIX = `${FILE_SUFFIX}.tmp`;
const replacementOf = (name: string) => `${name}.tmp`;
/** The longest file name used; a chat whose key would need more is not saved. */
const MAX_FILE_NAME = 200;
const KEY_STORE_KEY = "ao.chat.savedPagesKey";
const keyStoreOptions = (): SecureStore.SecureStoreOptions => ({ keychainAccessible: SecureStore.WHEN_UNLOCKED_THIS_DEVICE_ONLY });

/** How many chats keep a saved page; the least recently saved go first. */
export const SAVED_CONVERSATIONS = 12;
/** A page larger than this many UTF-8 bytes is not saved. */
export const SAVED_PAGE_MAX_BYTES = 400_000;
/**
 * All saved pages together stay under this many bytes on disk; the least
 * recently saved chats go first. The OS may clear the cache directory too,
 * but is not relied on to.
 */
export const SAVED_TOTAL_MAX_BYTES = 2_000_000;
/** A saved page older than this is not shown; the next write deletes it. */
export const SAVED_PAGE_KEEP_MS = 7 * 24 * 60 * 60 * 1000;

type Saved = { v: 2; savedAt: number; page: ConversationPage };
/** A file in the saved-chats directory, as it is listed. */
export type SavedFile = { name: string; size: number; modified: number };

const FIRST_WRITE_DELAY_MS = 2_000;

const savedDirectory = () => new Directory(Paths.cache, DIRECTORY_NAME);

/**
 * A chat's file name: its key, escaped to be safe as one, so a machine's chats
 * are still found by its identity (see forget). Null when it would be too long.
 */
export function savedFileName(key: string): string | null {
	const base = encodeURIComponent(key);
	return base.length + TMP_SUFFIX.length <= MAX_FILE_NAME ? base + FILE_SUFFIX : null;
}

/** The chat a file holds (its page, or a replacement being written), or null for a file that is neither. */
export function savedFileKey(name: string): string | null {
	const suffix = name.endsWith(FILE_SUFFIX) ? FILE_SUFFIX : name.endsWith(TMP_SUFFIX) ? TMP_SUFFIX : null;
	if (!suffix) return null;
	try {
		return decodeURIComponent(name.slice(0, -suffix.length));
	} catch {
		return null;
	}
}

let legacyCleanup: Promise<void> | undefined;
/**
 * Removes the pages and index older builds kept in AsyncStorage. Runs once a
 * launch; one that fails runs again next time.
 */
export function clearLegacySavedPages(): Promise<void> {
	legacyCleanup ??= (async () => {
		const keys = (await AsyncStorage.getAllKeys()).filter((key) => key === LEGACY_INDEX_KEY || key.startsWith(LEGACY_PAGE_PREFIX));
		if (keys.length) await AsyncStorage.multiRemove(keys);
	})().catch(() => {
		legacyCleanup = undefined;
	});
	return legacyCleanup;
}

let pageKey: AESEncryptionKey | undefined;
let creatingKey: Promise<AESEncryptionKey> | undefined;
/** Moves on when the key is deleted, so a read under way then keeps no old key. */
let keyGeneration = 0;

/** The key pages are encrypted with; with `create`, made the first time a page is saved. */
async function savedPagesKey(create: boolean): Promise<AESEncryptionKey | null> {
	if (pageKey) return pageKey;
	const generation = keyGeneration;
	const stored = await SecureStore.getItemAsync(KEY_STORE_KEY, keyStoreOptions());
	let key: AESEncryptionKey | null = null;
	if (stored) key = await AESEncryptionKey.import(stored, "base64");
	else if (create) {
		creatingKey ??= (async () => {
			const made = await AESEncryptionKey.generate();
			await SecureStore.setItemAsync(KEY_STORE_KEY, await made.encoded("base64"), keyStoreOptions());
			return made;
		})().finally(() => {
			creatingKey = undefined;
		});
		key = await creatingKey;
	}
	if (key && generation === keyGeneration) pageKey = key;
	return key;
}

/** A chat's key as the encryption's additional data, so a file opens only as its own chat. */
const boundTo = (key: string) => ({ additionalData: new TextEncoder().encode(key) });

/** A saved file's page, or null when it does not open as a current page of `key`. */
async function readSavedFile(file: File, key: string, encryptionKey: AESEncryptionKey, now: number): Promise<ConversationPage | null> {
	try {
		const plain = await aesDecryptAsync(AESSealedData.fromCombined(await file.bytes()), encryptionKey, boundTo(key));
		const saved = JSON.parse(new TextDecoder().decode(plain)) as Partial<Saved>;
		const page = saved.page;
		return saved.v === 2 && typeof saved.savedAt === "number" && now - saved.savedAt < SAVED_PAGE_KEEP_MS
			&& page && Array.isArray(page.items) && Array.isArray(page.turns) ? page : null;
	} catch {
		return null;
	}
}

export async function loadSavedConversation(key: string, now = Date.now()): Promise<ConversationPage | null> {
	void clearLegacySavedPages();
	try {
		const name = savedFileName(key);
		if (!name) return null;
		const directory = savedDirectory();
		// The page, else a whole replacement a kill left before it was moved into place.
		const files = [new File(directory, name), new File(directory, replacementOf(name))].filter((file) => file.exists);
		if (!files.length) return null;
		const encryptionKey = await savedPagesKey(false);
		if (!encryptionKey) return null;
		// Unreadable or expired files are not deleted here, where a write may have
		// replaced them since they were read; the next write prunes or replaces them.
		for (const file of files) {
			const page = await readSavedFile(file, key, encryptionKey, now);
			if (page) return savedPageView(page);
		}
		return null;
	} catch {
		return null;
	}
}

/**
 * A saved page as it shows until the first live read replaces it: a record of
 * what was said, never a live one. A question the agent asked then may be
 * answered or gone, and a turn running then may have ended days ago, so the
 * page offers no Approve/Deny or answer form (an open request is left out) and
 * shows nothing as working: no running turn with its ticking timer, no running
 * step, no reply still streaming in (no cursor), no busy or starting agent.
 * The live read brings back whatever is still open. A page with none of these
 * is returned as it is.
 */
export function savedPageView(page: ConversationPage): ConversationPage {
	const openRequest = (item: ConversationItem) =>
		item.kind === "activity" && item.status === "pending" && (item.activityKind === "approval" || item.activityKind === "user_input");
	const runningStep = (item: ConversationItem) => item.kind === "activity" && item.status === "running";
	const streamingMessage = (item: ConversationItem) => item.kind === "message" && item.streaming;
	const live = (turn: ConversationTurn) => turn.state === "running" || turn.state === "queued";
	const state = page.controller?.state;
	const controllerLive = state === "busy" || state === "connecting" || state === "recovering";
	if (!controllerLive && !page.turns.some(live) && !page.items.some((item) => openRequest(item) || runningStep(item) || streamingMessage(item))) return page;
	return {
		...page,
		// What the agent is doing is not known until the machine answers; "ready" draws no banner or busy state.
		controller: controllerLive ? { ...page.controller, state: "ready" } : page.controller,
		// "recovered" is a turn whose outcome is not known: no timer, no working row.
		turns: page.turns.map((turn) => (live(turn) ? { ...turn, state: "recovered" } : turn)),
		items: page.items.flatMap((item): ConversationItem[] => {
			if (openRequest(item)) return [];
			// A settled step draws no spinner, and claims no outcome ("failed", "stopped").
			if (item.kind === "activity" && runningStep(item)) return [{ ...item, status: "completed" }];
			// The words saved are all that will come until the live read: drawn whole, with no cursor.
			if (item.kind === "message" && item.streaming) return [{ ...item, streaming: false }];
			return [item];
		}),
	};
}

/**
 * The files to delete once `keep` is written: anything that is not a saved
 * page, a `.tmp` beside its chat's page (a replacement cut short, or one the
 * page has since replaced), pages older than SAVED_PAGE_KEEP_MS, and the pages
 * past `limit` chats or `budget` bytes in all, the least recently saved (by
 * modification time) going first. A `.tmp` alone stands in for its page.
 * `keep` always stays.
 */
export function savedPageEvictions(
	files: readonly SavedFile[],
	keep: string,
	now: number,
	limit = SAVED_CONVERSATIONS,
	budget = SAVED_TOTAL_MAX_BYTES,
): string[] {
	const names = new Set(files.map((file) => file.name));
	const beside = (file: SavedFile) => file.name.endsWith(TMP_SUFFIX) && names.has(file.name.slice(0, FILE_SUFFIX.length - TMP_SUFFIX.length));
	const current = (file: SavedFile) => file.name === keep
		|| (savedFileKey(file.name) !== null && !beside(file) && now - file.modified < SAVED_PAGE_KEEP_MS);
	const evicted = files.filter((file) => !current(file)).map((file) => file.name);
	const newestFirst = files.filter(current).sort((a, b) => Number(b.name === keep) - Number(a.name === keep) || b.modified - a.modified);
	let kept = 0;
	let total = 0;
	for (const file of newestFirst) {
		if (file.name !== keep && (kept >= limit || total + file.size > budget)) {
			// This one and every older one go.
			evicted.push(...newestFirst.slice(kept).map((older) => older.name));
			break;
		}
		kept++;
		total += file.size;
	}
	return evicted;
}

async function writeSaved(key: string, page: ConversationPage, now: number): Promise<void> {
	await clearLegacySavedPages();
	const name = savedFileName(key);
	if (!name) return;
	const plain = new TextEncoder().encode(JSON.stringify({ v: 2, savedAt: now, page } satisfies Saved));
	if (plain.byteLength > SAVED_PAGE_MAX_BYTES) return;
	const encryptionKey = await savedPagesKey(true);
	if (!encryptionKey) return;
	const sealed = await aesEncryptAsync(plain, encryptionKey, boundTo(key));
	const directory = savedDirectory();
	if (!directory.exists) directory.create({ intermediates: true, idempotent: true });
	// Written whole beside the old page, then moved over it (see the top of this
	// file): a write cut short leaves the old page as it was.
	const replacement = new File(directory, replacementOf(name));
	replacement.write(await sealed.combined());
	replacement.moveSync(new File(directory, name), { overwrite: true });
	pruneSaved(directory, name, now);
}

/** Applies savedPageEvictions to the directory. Never fails the write it follows. */
function pruneSaved(directory: Directory, keep: string, now: number): void {
	try {
		const files = directory.list().filter((item): item is File => item instanceof File);
		const facts = files.map((file): SavedFile => {
			const info = file.info();
			return { name: file.name, size: info.size ?? 0, modified: info.modificationTime ?? 0 };
		});
		const evicted = new Set(savedPageEvictions(facts, keep, now));
		for (const file of files) if (evicted.has(file.name)) file.delete();
	} catch {
		// Trimmed after the next write.
	}
}

/**
 * Deletes every saved chat `matches` picks. With `all`, the whole directory
 * goes and the key with it, so anything a deletion missed can no longer be
 * read; the next save makes a new key.
 */
async function removeSaved(matches: (key: string) => boolean, all: boolean): Promise<void> {
	await clearLegacySavedPages();
	const directory = savedDirectory();
	if (all) {
		keyGeneration++;
		pageKey = undefined;
		try {
			if (directory.exists) directory.delete();
		} finally {
			await SecureStore.deleteItemAsync(KEY_STORE_KEY, keyStoreOptions());
		}
		return;
	}
	if (!directory.exists) return;
	const failures: unknown[] = [];
	for (const item of directory.list()) {
		if (!(item instanceof File)) continue;
		const key = savedFileKey(item.name);
		if (key === null || !matches(key)) continue;
		// One file that will not go does not keep the rest; the first failure is reported after.
		try {
			item.delete();
		} catch (error) {
			failures.push(error);
		}
	}
	if (failures.length) throw failures[0];
}

/**
 * Saves each chat's newest page at most every `everyMs` (a streaming reply
 * changes it several times a second). One write at a time, in order. Nothing
 * is written when the app goes to the background, deliberately: a kill loses
 * at most the last few seconds of a saved copy, which only shows until the
 * live read replaces it. `flush` writes every waiting page at once; the app
 * does not call it (tests do, to write without waiting on the timers).
 */
export function createConversationSaver(
	write: (key: string, page: ConversationPage, now: number) => Promise<void> = writeSaved,
	everyMs = 5_000,
	remove: (matches: (key: string) => boolean, all: boolean) => Promise<void> = removeSaved,
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
			const removal = chain.then(() => remove(matches, machines === null));
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

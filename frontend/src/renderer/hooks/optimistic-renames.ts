import { useSyncExternalStore } from "react";

/**
 * Titles the user just confirmed, keyed by what they name (a session, a shell
 * terminal, a side chat). A rename shows from the frame its editor closes, and
 * no older read or acknowledgement can put a previous title back:
 *
 * - While any request for a key is in flight its newest live title wins, both
 *   where lists are read (the hooks subscribe here, so they redraw in the same
 *   commit as the editor) and on every read that lands meanwhile.
 * - Once the newest request is acknowledged, a read that began before the
 *   acknowledgement still shows that title; a read begun after it is the
 *   server's answer and wins.
 * - A failed request drops only its own title: the newest one still in flight
 *   shows, else the last acknowledged one, else the title from before.
 * - Requests for one key go out one after another (see {@link queueRenameWrite}),
 *   so the server stores the renames in the order they were confirmed.
 */

type Attempt = { token: number; title: string };

type Entry = {
	/** The title the server is known to have: the one before the first attempt, or the last acknowledged. */
	base: string;
	/** In-flight attempts, oldest first. */
	attempts: Attempt[];
	/** Clock of the last acknowledgement; 0 when none in this entry. */
	ackedAt: number;
	/** When the last attempt settled (ms), for pruning. */
	settledAtMs: number;
};

/** How long an acknowledged title still covers reads begun before it. */
const SETTLED_RETENTION_MS = 120_000;

let clock = 0;
let version = 0;
const entries = new Map<string, Entry>();
const listeners = new Set<() => void>();
/** Per key, the last queued write, settled either way. */
const writes = new Map<string, Promise<void>>();

function notify() {
	version += 1;
	for (const listener of listeners) listener();
}

function pruneSettled(now = Date.now()) {
	for (const [key, entry] of entries) {
		if (entry.attempts.length === 0 && now - entry.settledAtMs > SETTLED_RETENTION_MS) entries.delete(key);
	}
}

export function subscribeOptimisticRenames(listener: () => void): () => void {
	listeners.add(listener);
	return () => listeners.delete(listener);
}

export function optimisticRenamesVersion(): number {
	return version;
}

/** Redraws the caller when a rename starts or settles anywhere. */
export function useOptimisticRenamesVersion(): number {
	return useSyncExternalStore(subscribeOptimisticRenames, optimisticRenamesVersion, optimisticRenamesVersion);
}

/** Call as a read starts; pass the result to {@link renamedTitle} when it lands. */
export function renameReadStarted(): number {
	clock += 1;
	return clock;
}

/** Whether any title is still in flight or covering older reads. */
export function hasOptimisticRenames(): boolean {
	return entries.size > 0;
}

/** Whether `key` has a title in flight or covering older reads. Expired entries are dropped first. */
export function isOptimisticRenameTracked(key: string): boolean {
	pruneSettled();
	return entries.has(key);
}

/**
 * The title to show for `key` instead of what was read, or undefined to keep
 * what was read. Without `readStartedAt`, only an in-flight title counts (what
 * the hooks overlay on cached data). With it, an acknowledged title also covers
 * a read that began before the acknowledgement.
 */
export function renamedTitle(key: string, readStartedAt?: number): string | undefined {
	const entry = entries.get(key);
	if (!entry) return undefined;
	const live = entry.attempts.at(-1);
	if (live) return live.title;
	if (readStartedAt === undefined) return undefined;
	if (readStartedAt < entry.ackedAt) return entry.base;
	if (Date.now() - entry.settledAtMs > SETTLED_RETENTION_MS) entries.delete(key);
	return undefined;
}

/**
 * Records a confirmed rename and returns its token. `currentTitle` is what
 * shows now; a failure with nothing else in flight rolls back to it.
 */
export function beginOptimisticRename(key: string, title: string, currentTitle: string): number {
	pruneSettled();
	clock += 1;
	const token = clock;
	const entry = entries.get(key);
	if (entry) {
		// Nothing in flight: what shows now is the server's latest word, which may
		// be a name given elsewhere since the last acknowledgement.
		if (entry.attempts.length === 0) entry.base = currentTitle;
		entry.attempts.push({ token, title });
	} else entries.set(key, { base: currentTitle, attempts: [{ token, title }], ackedAt: 0, settledAtMs: 0 });
	notify();
	return token;
}

export type RenameSettlement = {
	/** The title to show now for the key. */
	title: string;
	/** This attempt was the one showing, so the caches and the user need to hear about it. */
	latest: boolean;
};

/**
 * Settles one attempt. A late answer for a superseded attempt changes nothing
 * that shows. `storedTitle` is the name the server kept when it differs from
 * the one sent; it then covers older reads and is what a later failure rolls
 * back to.
 */
export function settleOptimisticRename(key: string, token: number, ok: boolean, storedTitle?: string): RenameSettlement | undefined {
	const entry = entries.get(key);
	const index = entry?.attempts.findIndex((attempt) => attempt.token === token) ?? -1;
	if (!entry || index < 0) return undefined;
	const latest = index === entry.attempts.length - 1;
	if (ok) {
		// The server has this title, so the attempts before it are moot.
		entry.base = storedTitle ?? entry.attempts[index].title;
		entry.attempts = entry.attempts.slice(index + 1);
		clock += 1;
		entry.ackedAt = clock;
	} else {
		entry.attempts.splice(index, 1);
	}
	entry.settledAtMs = Date.now();
	// A failure with no acknowledgement and nothing in flight leaves the server's
	// title unchanged; reads show it again as they are.
	if (entry.attempts.length === 0 && entry.ackedAt === 0) entries.delete(key);
	notify();
	return { title: entry.attempts.at(-1)?.title ?? entry.base, latest };
}

/**
 * Runs `write` once every earlier write for `key` has settled, so two renames
 * in quick succession reach the server in the order they were confirmed and
 * the newer one is the name it keeps. With nothing queued it runs at once,
 * synchronously up to its first await.
 */
export function queueRenameWrite<T>(key: string, write: () => Promise<T>): Promise<T> {
	const previous = writes.get(key);
	const run = previous ? previous.then(() => write()) : write();
	const settled = run.then(() => undefined, () => undefined);
	writes.set(key, settled);
	void settled.then(() => {
		if (writes.get(key) === settled) writes.delete(key);
	});
	return run;
}

export function resetOptimisticRenamesForTest(): void {
	entries.clear();
	writes.clear();
	clock = 0;
	notify();
}

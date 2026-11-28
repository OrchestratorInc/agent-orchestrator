import AsyncStorage from "@react-native-async-storage/async-storage";
import { useSyncExternalStore } from "react";

// The review comments sent to a worker by hand from its PR page, remembered so
// a reopened page still says "Sent to agent" and Send all never delivers one
// twice. Automatic delivery is the daemon's to report; this is only what the
// person sent. Kept per machine and session, bounded as the desktop's is, and
// forgotten with the machine or the signed-in account (forgetLocalState.ts).

export const REVIEW_SENT_STORAGE_KEY = "ao.reviews.sent.v1";
// The sessions touched most recently, and each one's newest sends.
const MAX_SCOPES = 200;
const MAX_KEYS = 500;

/** Scope → the comments sent from it by hand, oldest first. Insertion order is recency. */
export type SentReviews = Readonly<Record<string, readonly string[]>>;

/** One session on one machine (or Cloud). */
export function sentScope(hostId: string, sessionId: string): string {
	return `${hostId}\n${sessionId}`;
}

/** Saved sends as written, dropping anything that is not a list of keys. */
export function parseSentReviews(raw: string | null): SentReviews {
	if (!raw) return {};
	try {
		const saved: unknown = JSON.parse(raw);
		if (!saved || typeof saved !== "object" || Array.isArray(saved)) return {};
		return Object.fromEntries(Object.entries(saved).filter((entry): entry is [string, string[]] =>
			Array.isArray(entry[1]) && entry[1].every((key) => typeof key === "string")));
	} catch {
		return {};
	}
}

/** `keys` added to the scope, which becomes the most recent; the oldest scopes and keys past the bounds go. */
export function withSent(stored: SentReviews, scope: string, keys: readonly string[]): SentReviews {
	const previous = stored[scope] ?? [];
	const added = keys.filter((key) => !previous.includes(key));
	if (!added.length && scope in stored) return stored;
	const { [scope]: _moved, ...rest } = stored;
	const scopes = Object.entries(rest).slice(-(MAX_SCOPES - 1));
	return Object.fromEntries([...scopes, [scope, [...previous, ...added].slice(-MAX_KEYS)]]);
}

/** Every scope of one machine (or Cloud) removed. */
export function withoutHost(stored: SentReviews, hostId: string): SentReviews {
	const prefix = sentScope(hostId, "");
	return Object.fromEntries(Object.entries(stored).filter(([scope]) => !scope.startsWith(prefix)));
}

let state: SentReviews = {};
let loading: Promise<void> | null = null;
let writing: Promise<void> = Promise.resolve();
const listeners = new Set<() => void>();
const snapshots = new WeakMap<readonly string[], ReadonlySet<string>>();
const NONE: ReadonlySet<string> = new Set();

function set(next: SentReviews) {
	if (next === state) return;
	state = next;
	for (const listener of listeners) listener();
}

/** Reads the saved sends once, keeping any marked while the read was out. */
function load(): Promise<void> {
	loading ??= AsyncStorage.getItem(REVIEW_SENT_STORAGE_KEY).then(
		(raw) => {
			let next = parseSentReviews(raw);
			for (const [scope, keys] of Object.entries(state)) next = withSent(next, scope, keys);
			set(next);
		},
		() => {},
	);
	return loading;
}

/** Writes what is in memory, after the writes before it; rejects if it did not reach the phone. */
function persist(): Promise<void> {
	const result = writing.then(() => Object.keys(state).length
		? AsyncStorage.setItem(REVIEW_SENT_STORAGE_KEY, JSON.stringify(state))
		: AsyncStorage.removeItem(REVIEW_SENT_STORAGE_KEY));
	writing = result.catch(() => {});
	return result;
}

export function markReviewCommentsSent(scope: string, keys: readonly string[]): void {
	if (!keys.length) return;
	set(withSent(state, scope, keys));
	void load().then(persist).catch(() => {});
}

/** Forgets a machine's sends (or Cloud's, for a signed-out account). Rejects if they are still on the phone. */
export async function forgetSentReviewComments(hostId: string): Promise<void> {
	await load();
	set(withoutHost(state, hostId));
	await persist();
}

function subscribe(listener: () => void) {
	listeners.add(listener);
	void load();
	return () => { listeners.delete(listener); };
}

/** The comments sent by hand from this session's PR page, by `sentKey`. */
export function useSentReviewComments(scope: string): ReadonlySet<string> {
	return useSyncExternalStore(subscribe, () => {
		const keys = state[scope];
		if (!keys) return NONE;
		let snapshot = snapshots.get(keys);
		if (!snapshot) {
			snapshot = new Set(keys);
			snapshots.set(keys, snapshot);
		}
		return snapshot;
	});
}

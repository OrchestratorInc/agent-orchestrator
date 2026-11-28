import { useCallback, useSyncExternalStore } from "react";
import { pullRequestIdentity } from "./reviewView";

/**
 * Merges the phone has started, kept for the whole app rather than by the page
 * that started them.
 *
 * A merge call runs for seconds: the daemon re-reads the pull request, checks
 * its stack, then asks GitHub. A page that held the merge in its own state lost
 * it on the way back, so the PR list still said Ready to merge, the page opened
 * again on an empty slider, and the merge's success landed on a page no longer
 * shown: a haptic with nothing moving. Every screen reads the merge from here.
 *
 * A finished merge is held for a minute: the board shows it before its next
 * poll, and a daemon that never agrees is believed again after that.
 */
export type PRMergeEntry =
	| { status: "merging" }
	| { status: "merged"; at: number }
	| { status: "failed"; error: unknown; at: number };

export const PR_MERGE_HOLD_MS = 60_000;

let entries: ReadonlyMap<string, PRMergeEntry> = new Map();
const inFlight = new Map<string, Promise<void>>();
const expiries = new Map<string, ReturnType<typeof setTimeout>>();
const listeners = new Set<() => void>();

/**
 * One machine's pull request: the same one on two machines is two merges, and
 * so is the same repository and number on two forges. Read by forge,
 * repository and number, since the board and the PR page may spell its URL
 * differently.
 */
export function prMergeKey(hostId: string | null | undefined, prUrl: string): string {
	const identity = pullRequestIdentity(prUrl);
	return JSON.stringify([hostId ?? "", identity ? `${forgeHost(prUrl)}/${identity.repo}#${identity.number}` : prUrl]);
}

function forgeHost(prUrl: string): string {
	try { return new URL(prUrl).host; } catch { return ""; }
}

function set(key: string, entry: PRMergeEntry | undefined) {
	const next = new Map(entries);
	if (entry) next.set(key, entry);
	else next.delete(key);
	entries = next;
	clearTimeout(expiries.get(key));
	expiries.delete(key);
	if (entry && entry.status !== "merging") {
		expiries.set(key, setTimeout(() => {
			expiries.delete(key);
			if (entries.get(key) === entry) set(key, undefined);
		}, PR_MERGE_HOLD_MS));
	}
	for (const listener of listeners) listener();
}

/**
 * Runs `merge` for the pull request under `key`, recording it as merging, then
 * merged or failed. A second call while one runs joins the first. The returned
 * promise settles as the merge does, rejecting with its error.
 */
export function trackPRMerge(key: string, merge: () => Promise<void>): Promise<void> {
	const running = inFlight.get(key);
	if (running) return running;
	set(key, { status: "merging" });
	const run = (async () => {
		try {
			await merge();
			set(key, { status: "merged", at: Date.now() });
		} catch (error) {
			set(key, { status: "failed", error, at: Date.now() });
			throw error;
		} finally {
			inFlight.delete(key);
		}
	})();
	inFlight.set(key, run);
	return run;
}

export function prMergeEntry(key: string | undefined): PRMergeEntry | undefined {
	return key ? entries.get(key) : undefined;
}

export function subscribePRMerges(listener: () => void): () => void {
	listeners.add(listener);
	return () => { listeners.delete(listener); };
}

/** Every merge held, by key; a new map each time one changes. */
export function usePRMerges(): ReadonlyMap<string, PRMergeEntry> {
	return useSyncExternalStore(subscribePRMerges, () => entries);
}

/** The merge held for one pull request, if any. */
export function usePRMerge(key: string | undefined): PRMergeEntry | undefined {
	const read = useCallback(() => prMergeEntry(key), [key]);
	return useSyncExternalStore(subscribePRMerges, read);
}

/** Tests only: forgets every merge. */
export function resetPRMerges() {
	for (const timer of expiries.values()) clearTimeout(timer);
	expiries.clear();
	inFlight.clear();
	entries = new Map();
}

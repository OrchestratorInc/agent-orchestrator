/**
 * Which sessions the user has opened since they last changed: the sidebar's
 * filled (unread) and hollow (read) dots. The desktop's session-read-store,
 * kept on the phone: everything that existed before the first launch counts as
 * read (the baseline), and opening a session reads it up to its latest change.
 */
import AsyncStorage from "@react-native-async-storage/async-storage";
import { useSyncExternalStore } from "react";

const STORAGE_KEY = "ao.sidebar.readState.v1";

export type ReadState = { baseline: number; readAt: Record<string, number> };

let state: ReadState = { baseline: Date.now(), readAt: {} };
let loaded = false;
const listeners = new Set<() => void>();

function emit() {
	for (const listener of listeners) listener();
}

function persist() {
	void AsyncStorage.setItem(STORAGE_KEY, JSON.stringify(state)).catch(() => {});
}

/** Reads the stored state once; until then every session counts as read. */
export function loadReadState(): Promise<void> {
	if (loaded) return Promise.resolve();
	loaded = true;
	return AsyncStorage.getItem(STORAGE_KEY).then((raw) => {
		const stored = raw ? JSON.parse(raw) as Partial<ReadState> : null;
		if (stored && typeof stored.baseline === "number") {
			state = { baseline: stored.baseline, readAt: { ...stored.readAt, ...state.readAt } };
		} else {
			persist();
		}
		emit();
	}).catch(() => {});
}

/** The last change a person would notice, as the unread dot compares it. */
export function changedAt(session: { lastEventAt?: string | null; updatedAt?: string | null; lastActivityAt?: string | null }): number {
	return Date.parse(session.lastEventAt ?? session.updatedAt ?? session.lastActivityAt ?? "");
}

export function isUnread(read: ReadState, key: string, at: number): boolean {
	return Number.isFinite(at) && at > (read.readAt[key] ?? read.baseline);
}

/** The user has the session open: everything up to `at` is read. */
export function markRead(key: string, at: number) {
	if (!Number.isFinite(at) || (state.readAt[key] ?? 0) >= at) return;
	state = { baseline: state.baseline, readAt: { ...state.readAt, [key]: at } };
	persist();
	emit();
}

function subscribe(listener: () => void) {
	listeners.add(listener);
	return () => { listeners.delete(listener); };
}

export function useReadState(): ReadState {
	return useSyncExternalStore(subscribe, () => state, () => state);
}

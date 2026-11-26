import { useMemo } from "react";
import { create } from "zustand";

const STORAGE_KEY = "ao.reviews.sent";
// Bounded: the sessions touched most recently, and each one's newest sends.
const MAX_SESSIONS = 200;
const MAX_KEYS = 500;

/** Session key → the review comments sent to its worker by hand, oldest first. */
type Stored = Record<string, string[]>;

function load(): Stored {
	try {
		const saved: unknown = JSON.parse(window.localStorage.getItem(STORAGE_KEY) ?? "null");
		if (saved && typeof saved === "object" && !Array.isArray(saved)) {
			return Object.fromEntries(
				Object.entries(saved).filter((entry): entry is [string, string[]] =>
					Array.isArray(entry[1]) && entry[1].every((key) => typeof key === "string")),
			);
		}
	} catch {}
	return {};
}

function persist(state: Stored) {
	try {
		window.localStorage.setItem(STORAGE_KEY, JSON.stringify(state));
	} catch {}
}

type ReviewSentState = {
	sent: Stored;
	markSent: (sessionKey: string, keys: string[]) => void;
};

/**
 * Which review comments were sent to a session's worker from the Reviews tab.
 * Automatic delivery is recorded by the daemon; a send by hand is remembered
 * here, so it stays sent when the tab or the app reopens and Send all does
 * not deliver it again.
 */
export const useReviewSentStore = create<ReviewSentState>((set, get) => ({
	sent: load(),
	markSent: (sessionKey, keys) => {
		if (keys.length === 0) return;
		const { [sessionKey]: existing = [], ...others } = get().sent;
		const added = new Set(keys);
		const merged = [...existing.filter((key) => !added.has(key)), ...added].slice(-MAX_KEYS);
		// The session just touched moves to the end; the oldest ones fall off.
		const kept = Object.entries(others).slice(-(MAX_SESSIONS - 1));
		const next = { ...Object.fromEntries(kept), [sessionKey]: merged };
		persist(next);
		set({ sent: next });
	},
}));

const none: string[] = [];

/** The comment keys sent by hand to this session's worker. */
export function useReviewSentKeys(sessionKey: string): ReadonlySet<string> {
	const keys = useReviewSentStore((state) => state.sent[sessionKey] ?? none);
	return useMemo(() => new Set(keys), [keys]);
}

/** Tests: forget every send. */
export function resetReviewSentStore() {
	try {
		window.localStorage.removeItem(STORAGE_KEY);
	} catch {}
	useReviewSentStore.setState({ sent: {} });
}

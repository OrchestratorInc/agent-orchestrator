import { useCallback, useSyncExternalStore } from "react";
import type { SessionPRSummary } from "../hooks/useSessionScmSummary";

/**
 * Which PR's full description is open in a session's main area, keyed by the
 * session's UI key. The PR card opens it and keeps the snapshot current while
 * it is open; the session view draws it over the chat and file tabs.
 */
const openBySession = new Map<string, SessionPRSummary>();
const listeners = new Set<() => void>();
const notify = () => { for (const listener of listeners) listener(); };
const subscribe = (listener: () => void) => {
	listeners.add(listener);
	return () => { listeners.delete(listener); };
};

export function openPRDescription(sessionKey: string, pr: SessionPRSummary): void {
	openBySession.set(sessionKey, pr);
	notify();
}

export function closePRDescription(sessionKey: string): void {
	if (!openBySession.delete(sessionKey)) return;
	notify();
}

/** Refreshes the open snapshot when it is this PR, so new details reach the main area. */
export function refreshPRDescription(sessionKey: string, pr: SessionPRSummary): void {
	const open = openBySession.get(sessionKey);
	if (!open || open.url !== pr.url || open === pr) return;
	openBySession.set(sessionKey, pr);
	notify();
}

export function usePRDescriptionPane(sessionKey: string): SessionPRSummary | undefined {
	const getSnapshot = useCallback(() => openBySession.get(sessionKey), [sessionKey]);
	return useSyncExternalStore(subscribe, getSnapshot, () => undefined);
}

/** Test helper: closes every open description. */
export function resetPRDescriptionPanes(): void {
	openBySession.clear();
	notify();
}

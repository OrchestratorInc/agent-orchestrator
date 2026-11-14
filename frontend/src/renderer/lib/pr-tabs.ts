import { useCallback, useSyncExternalStore } from "react";

/** The part of a PR's tab to show: its top (the description), or its comments. */
export type PRTabSection = "summary" | "comments";

/**
 * A session's PR tabs, beside its chat and file tabs: the PRs open as tabs, the
 * one in front (none while another tab is), and the part each was last asked
 * to show. Keyed by the session's UI key. The inspector's PR card opens them;
 * the session view draws them.
 */
export type PRTabsState = {
	open: string[];
	active: string | null;
	/** What the last open asked to show, and a count so asking twice scrolls again. */
	request?: { url: string; section: PRTabSection; key: number };
};

const EMPTY: PRTabsState = { open: [], active: null };
const bySession = new Map<string, PRTabsState>();
const listeners = new Set<() => void>();
let requestKey = 0;
const notify = () => { for (const listener of listeners) listener(); };
const subscribe = (listener: () => void) => {
	listeners.add(listener);
	return () => { listeners.delete(listener); };
};
const update = (sessionKey: string, next: PRTabsState) => {
	if (next.open.length === 0 && !next.request) bySession.delete(sessionKey);
	else bySession.set(sessionKey, next);
	notify();
};

/** Opens a PR as a tab, or brings its tab forward, on the part asked for. */
export function openPRTab(sessionKey: string, url: string, section: PRTabSection = "summary"): void {
	const current = bySession.get(sessionKey) ?? EMPTY;
	update(sessionKey, {
		open: current.open.includes(url) ? current.open : [...current.open, url],
		active: url,
		request: { url, section, key: ++requestKey },
	});
}

export function activatePRTab(sessionKey: string, url: string): void {
	const current = bySession.get(sessionKey) ?? EMPTY;
	if (!current.open.includes(url) || current.active === url) return;
	update(sessionKey, { ...current, active: url });
}

/** Another tab came forward: the PR tabs stay open behind it. */
export function deactivatePRTab(sessionKey: string): void {
	const current = bySession.get(sessionKey);
	if (!current?.active) return;
	update(sessionKey, { ...current, active: null });
}

export function closePRTab(sessionKey: string, url: string): void {
	const current = bySession.get(sessionKey);
	if (!current?.open.includes(url)) return;
	update(sessionKey, {
		open: current.open.filter((candidate) => candidate !== url),
		active: current.active === url ? null : current.active,
		request: current.request?.url === url ? undefined : current.request,
	});
}

export function usePRTabs(sessionKey: string): PRTabsState {
	const getSnapshot = useCallback(() => bySession.get(sessionKey) ?? EMPTY, [sessionKey]);
	return useSyncExternalStore(subscribe, getSnapshot, () => EMPTY);
}

/** The tab strip's key for a PR's tab. */
export const prTabKey = (url: string) => `pr:${url}`;

export function prTabUrl(key: string | undefined): string | undefined {
	return key?.startsWith("pr:") ? key.slice("pr:".length) : undefined;
}

/** Test helper: closes every PR tab. */
export function resetPRTabs(): void {
	bySession.clear();
	notify();
}

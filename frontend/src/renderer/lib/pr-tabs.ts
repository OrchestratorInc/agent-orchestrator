import { useCallback, useSyncExternalStore } from "react";

export type PRTabSection = "summary" | "comments";

export type PRTabsState = {
	open: string[];
	active: string | null;
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

export const prTabKey = (url: string) => `pr:${url}`;

export function prTabUrl(key: string | undefined): string | undefined {
	return key?.startsWith("pr:") ? key.slice("pr:".length) : undefined;
}

export function resetPRTabs(): void {
	bySession.clear();
	notify();
}

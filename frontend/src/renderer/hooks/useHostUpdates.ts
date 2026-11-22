import { useEffect } from "react";
import { create } from "zustand";
import type { HostUpdateView } from "../../main/remote-request";
import { aoBridge } from "../lib/bridge";
import { isNightlyBuild } from "../lib/build-channel";
import { useAppVersion } from "./useCommandPaletteEnabled";
import { useUiStore } from "../stores/ui-store";
import { requestRemoteHostsRefresh, type RemoteHost } from "./useRemoteHosts";

const REFRESH_MS = 10 * 60_000;
const UPDATING_POLL_MS = 3_000;
const UPDATING_LIMIT_MS = 10 * 60_000;

type HostUpdateState = {
	view: HostUpdateView | null;
	updating?: { from: string; since: number; unconfirmed?: boolean };
	error?: string;
	unconfirmed?: boolean;
};

type Store = {
	byUrl: Record<string, HostUpdateState>;
	localVersion?: string;
	hostIds: Record<string, string>;
	patch: (url: string, change: Partial<HostUpdateState>) => void;
	retain: (urls: string[]) => void;
};

export const useHostUpdateStore = create<Store>((set) => ({
	byUrl: {},
	hostIds: {},
	patch: (url, change) => set((state) => ({ byUrl: { ...state.byUrl, [url]: { ...(state.byUrl[url] ?? { view: null }), ...change } } })),
	retain: (urls) => set((state) => ({ byUrl: Object.fromEntries(Object.entries(state.byUrl).filter(([url, entry]) => urls.includes(url) || entry.updating)) })),
}));

const PUBLISHED = /^(\d+)\.(\d+)\.(\d+)(?:-nightly\.(\d{12}))?$/;

export function compareVersions(a: string, b: string): number | null {
	const left = PUBLISHED.exec(a.replace(/^v/i, ""));
	const right = PUBLISHED.exec(b.replace(/^v/i, ""));
	if (!left || !right || isNightlyBuild(a) !== isNightlyBuild(b)) return null;
	for (let index = 1; index <= 4; index++) {
		const difference = Number(left[index] ?? 0) - Number(right[index] ?? 0);
		if (difference !== 0) return difference;
	}
	return 0;
}

export type HostUpdateAction =
	| { kind: "update"; version: string }
	| { kind: "updating" }
	| { kind: "failed"; error?: string; unconfirmed?: boolean }
	| { kind: "manual"; version: string };

export function hostUpdateAction(state: HostUpdateState | undefined, localVersion: string | undefined): HostUpdateAction | null {
	if (state?.updating) return { kind: "updating" };
	const view = state?.view;
	if (!view || !localVersion || (compareVersions(view.version, localVersion) ?? 0) >= 0) return null;
	if (state.unconfirmed) return { kind: "failed", unconfirmed: true };
	if (state.error !== undefined || view.state === "failed") return { kind: "failed", error: state.error || view.error };
	if (view.state === "updating") return { kind: "updating" };
	return view.supported ? { kind: "update", version: view.version } : { kind: "manual", version: view.version };
}

const generations = new Map<string, number>();
const starting = new Set<string>();

function settle(url: string, view: HostUpdateView | null) {
	const { byUrl, patch } = useHostUpdateStore.getState();
	const updating = byUrl[url]?.updating;
	if (!updating) {
		if (!view) return;
		const previous = byUrl[url];
		patch(url, { view, unconfirmed: previous?.unconfirmed && view.state === "idle" && view.version === previous.view?.version ? true : undefined, ...(byUrl[url]?.error === "" ? { error: undefined } : {}), ...(view?.state === "updating" && view.version ? { updating: { from: view.version, since: Date.now() } } : {}) });
		return;
	}
	if (view?.state === "updating") {
		patch(url, { view, updating: { ...updating, since: Date.now() } });
		return;
	}
	if (!view) {
		if (Date.now() - updating.since < UPDATING_LIMIT_MS) return;
		patch(url, { updating: undefined, error: "" });
		requestRemoteHostsRefresh();
		return;
	}
	patch(url, { view, updating: undefined, error: view.state === "failed" ? view.error ?? "" : undefined, unconfirmed: updating.unconfirmed && view.state === "idle" && view.version === updating.from });
	if (view.state === "idle" && view.version !== updating.from) requestRemoteHostsRefresh();
}

export async function refreshHostUpdate(url: string): Promise<void> {
	const generation = generations.get(url);
	const hostId = useHostUpdateStore.getState().hostIds[url];
	const view = await aoBridge.remotes.hostUpdate(url).catch(() => null);
	const { byUrl, hostIds } = useHostUpdateStore.getState();
	if (generation !== generations.get(url) || starting.has(url) || (hostIds[url] !== hostId && !byUrl[url]?.updating)) return;
	settle(url, view);
}

export async function startHostUpdate(url: string): Promise<void> {
	const { byUrl, patch } = useHostUpdateStore.getState();
	const updating = { from: byUrl[url]?.view?.version ?? "", since: Date.now() };
	patch(url, { updating, error: undefined, unconfirmed: undefined });
	starting.add(url);
	try {
		const view = await aoBridge.remotes.startHostUpdate(url);
		patch(url, { ...(view ? { view } : {}), updating: view ? updating : { ...updating, unconfirmed: true }, error: undefined });
	} catch (cause) {
		const view = await aoBridge.remotes.hostUpdate(url).catch(() => null);
		if (view?.state === "updating") patch(url, { view, updating, error: undefined });
		else if (view && (view.state === "failed" || view.version !== updating.from)) {
			patch(url, { updating });
			settle(url, view);
		} else patch(url, { updating: undefined, error: cause instanceof Error ? cause.message : String(cause) });
	} finally {
		starting.delete(url);
		generations.set(url, (generations.get(url) ?? 0) + 1);
	}
}

const AUTO_ATTEMPTS_KEY = "ao.autoUpdateRemoteHostAttempts";

export function autoUpdateHosts(): void {
	const { byUrl, hostIds, localVersion } = useHostUpdateStore.getState();
	let attempts: Record<string, string> = {};
	try { attempts = JSON.parse(localStorage.getItem(AUTO_ATTEMPTS_KEY) ?? "{}") ?? {}; } catch { attempts = {}; }
	for (const [url, state] of Object.entries(byUrl)) {
		const host = hostIds[url];
		if (!host || !localVersion || state.view?.method === "app" || hostUpdateAction(state, localVersion)?.kind !== "update" || attempts[host] === localVersion) continue;
		attempts[host] = localVersion;
		localStorage.setItem(AUTO_ATTEMPTS_KEY, JSON.stringify(attempts));
		void startHostUpdate(url);
	}
}

export function useHostUpdatePolling(hosts: RemoteHost[]): void {
	const localVersion = useAppVersion();
	useEffect(() => { useHostUpdateStore.setState({ localVersion }); }, [localVersion]);
	const readable = hosts.filter((host) => host.failureReason !== "unauthorized" && (host.status === "connected" || host.failureReason === "incompatible"));
	const key = readable.map((host) => `${host.url}\n${host.status}\n${host.hostId}`).join("\0");
	useEffect(() => {
		const urls = readable.map((host) => host.url);
		useHostUpdateStore.setState({ hostIds: Object.fromEntries(readable.map((host) => [host.url, host.hostId])) });
		useHostUpdateStore.getState().retain(urls);
		const readAll = () => { for (const url of urls) void refreshHostUpdate(url); };
		readAll();
		const timer = setInterval(() => { if (!document.hidden) readAll(); }, REFRESH_MS);
		return () => clearInterval(timer);
	}, [key]);
	const updatingKey = useHostUpdateStore((state) => Object.keys(state.byUrl).filter((url) => state.byUrl[url]?.updating).join("\0"));
	useEffect(() => {
		if (!updatingKey) return;
		const urls = updatingKey.split("\0");
		const timer = setInterval(() => { for (const url of urls) void refreshHostUpdate(url); }, UPDATING_POLL_MS);
		return () => clearInterval(timer);
	}, [updatingKey]);
	const autoUpdate = useUiStore((state) => state.autoUpdateRemoteHosts);
	const behindKey = useHostUpdateStore((state) => [state.localVersion, ...Object.keys(state.byUrl).filter((url) => hostUpdateAction(state.byUrl[url], state.localVersion)?.kind === "update")].join("\0"));
	useEffect(() => { if (autoUpdate) autoUpdateHosts(); }, [autoUpdate, behindKey]);
}

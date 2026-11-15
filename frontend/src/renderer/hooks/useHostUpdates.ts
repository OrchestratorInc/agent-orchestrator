import { useEffect } from "react";
import { create } from "zustand";
import type { HostUpdateView } from "../../main/remote-request";
import { aoBridge } from "../lib/bridge";
import { isNightlyBuild } from "../lib/build-channel";
import { useAppVersion } from "./useCommandPaletteEnabled";
import { requestRemoteHostsRefresh, type RemoteHost } from "./useRemoteHosts";

const REFRESH_MS = 10 * 60_000;
const UPDATING_POLL_MS = 3_000;
const UPDATING_LIMIT_MS = 10 * 60_000;

type HostUpdateState = {
	view: HostUpdateView | null;
	updating?: { from: string; since: number };
	error?: string;
};

type Store = {
	byUrl: Record<string, HostUpdateState>;
	urlByHost: Record<string, string>;
	localVersion?: string;
	patch: (url: string, change: Partial<HostUpdateState>) => void;
	retain: (urls: string[]) => void;
};

export const useHostUpdateStore = create<Store>((set) => ({
	byUrl: {},
	urlByHost: {},
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
	| { kind: "failed"; error?: string }
	| { kind: "manual"; version: string };

export function hostUpdateAction(state: HostUpdateState | undefined, localVersion: string | undefined): HostUpdateAction | null {
	if (state?.updating) return { kind: "updating" };
	const view = state?.view;
	if (!view || !localVersion || (compareVersions(view.version, localVersion) ?? 0) >= 0) return null;
	if (state.error || view.state === "failed") return { kind: "failed", error: state.error ?? view.error };
	if (view.state === "updating") return { kind: "updating" };
	return view.supported ? { kind: "update", version: view.version } : { kind: "manual", version: view.version };
}

function settle(url: string, view: HostUpdateView | null) {
	const { byUrl, patch } = useHostUpdateStore.getState();
	const updating = byUrl[url]?.updating;
	if (!updating) {
		if (!view) return;
		patch(url, { view, error: undefined, ...(view?.state === "updating" && view.version ? { updating: { from: view.version, since: Date.now() } } : {}) });
		return;
	}
	if (!view || view.state === "updating") {
		if (Date.now() - updating.since < UPDATING_LIMIT_MS) return;
		patch(url, { view: null, updating: undefined });
		requestRemoteHostsRefresh();
		return;
	}
	patch(url, { view, updating: undefined, error: view.state === "failed" ? view.error ?? "" : undefined });
	if (view.state === "idle" && view.version !== updating.from) requestRemoteHostsRefresh();
}

export async function refreshHostUpdate(url: string): Promise<void> {
	settle(url, await aoBridge.remotes.hostUpdate(url).catch(() => null));
}

export async function startHostUpdate(url: string): Promise<void> {
	const { byUrl, patch } = useHostUpdateStore.getState();
	const updating = { from: byUrl[url]?.view?.version ?? "", since: Date.now() };
	patch(url, { updating, error: undefined });
	try {
		patch(url, { view: await aoBridge.remotes.startHostUpdate(url), updating, error: undefined });
	} catch (cause) {
		const view = await aoBridge.remotes.hostUpdate(url).catch(() => null);
		if (view?.state === "updating") patch(url, { view, updating, error: undefined });
		else patch(url, { updating: undefined, error: cause instanceof Error ? cause.message : String(cause) });
	}
}

export function useHostVersion(hostId: string | undefined): string | undefined {
	return useHostUpdateStore((state) => (hostId ? state.byUrl[state.urlByHost[hostId] ?? ""]?.view?.version : undefined) || undefined);
}

export function useHostUpdatePolling(hosts: RemoteHost[]): void {
	const localVersion = useAppVersion();
	useEffect(() => { useHostUpdateStore.setState({ localVersion }); }, [localVersion]);
	const readable = hosts.filter((host) => host.failureReason !== "unauthorized" && (host.status === "connected" || host.failureReason === "incompatible"));
	const key = readable.map((host) => `${host.url}\n${host.status}`).join("\0");
	useEffect(() => {
		const urls = readable.map((host) => host.url);
		useHostUpdateStore.getState().retain(urls);
		useHostUpdateStore.setState({ urlByHost: Object.fromEntries(hosts.map((host) => [host.hostId, host.url])) });
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
}

import { useCallback, useEffect, useRef, useState } from "react";
import { aoBridge } from "../lib/bridge";
import { connectHost, connectedHosts, disconnectHost } from "../lib/host-clients";
import { useCloudSession } from "../lib/cloud-session";
import { useSettings } from "./useSettings";
import { useUiStore } from "../stores/ui-store";

const HOSTS_CHANGED_EVENT = "ao:remote-hosts-changed";
const HOST_CHECK_EVENT = "ao:remote-host-check";
const RETRY_TICK_MS = 500;
const RETRY_BASE_MS = 1_000;
const RETRY_MAX_MS = 10_000;
const HIDDEN_RETRY_MS = 30_000;
const ACCOUNT_SYNC_MS = 30_000;
const NO_HOSTS: RemoteHost[] = [];

function isOfflineError(error: unknown): boolean {
	return error instanceof Error && error.message.endsWith(" is offline");
}

function failureReasonOf(error: unknown): RemoteHost["failureReason"] {
	if (error instanceof Error && error.message.endsWith(" is unauthorized")) return "unauthorized";
	if (error instanceof Error && error.message.endsWith(" is incompatible")) return "incompatible";
	return undefined;
}

export function retryDelayMs(attempt: number, random = Math.random()): number {
	const ceiling = Math.min(RETRY_MAX_MS, RETRY_BASE_MS * 2 ** attempt);
	return Math.round(ceiling / 2 + (ceiling / 2) * random);
}

type RetrySchedule = Map<string, { attempt: number; at: number }>;

function scheduleRetry(retries: RetrySchedule, hostId: string): void {
	const attempt = (retries.get(hostId)?.attempt ?? -1) + 1;
	retries.set(hostId, { attempt, at: Date.now() + (document.hidden ? HIDDEN_RETRY_MS : retryDelayMs(attempt)) });
}

export function requestRemoteHostsRefresh(): void {
	window.dispatchEvent(new Event(HOSTS_CHANGED_EVENT));
}

export function requestRemoteHostCheck(hostId: string): void {
	window.dispatchEvent(new CustomEvent<string>(HOST_CHECK_EVENT, { detail: hostId }));
}

export type RemoteHost = {
	hostId: string;
	label: string;
	url: string;
	status: "connecting" | "connected" | "offline";
	failureReason?: "unauthorized" | "incompatible";
};

type SavedHost = Pick<RemoteHost, "hostId" | "url">;

/**
 * listed: hosts holds the saved list, not the empty one before the first read.
 * settled: the host-list attempt has concluded — disabled, signed out, or
 * listed. Consumers that must wait for remote workspace data (the session
 * deep-link gate) settle on this instead of restating the enable rule, so the
 * two can never drift apart.
 */
export function useRemoteHosts(): { hosts: RemoteHost[]; listed: boolean; settled: boolean; refresh: () => Promise<void>; retryNow: () => Promise<void> } {
	const remoteHostsEnabled = useUiStore((state) => state.remoteHosts);
	const { status, session } = useCloudSession();
	const accountId = session?.user.id ?? "";
	const { settings } = useSettings();
	const cloudBaseUrl = settings?.cloudControlPlaneUrl ?? "";
	const enabled = remoteHostsEnabled && status === "authenticated" && Boolean(accountId);
	const enabledRef = useRef(enabled);
	enabledRef.current = enabled;
	const accountRef = useRef(accountId);
	accountRef.current = accountId;
	const previousAccount = useRef(accountId);
	const [hosts, setHosts] = useState<RemoteHost[]>([]);
	const [listed, setListed] = useState(false);
	const hostsRef = useRef(hosts);
	hostsRef.current = hosts;
	const retries = useRef<RetrySchedule>(new Map());
	const inFlight = useRef(new Set<string>());
	const savedHostUrls = useRef(new Map<string, string>());
	const refreshGeneration = useRef(0);

	const attempt = useCallback(async (saved: SavedHost, generation: number, account: string) => {
		const current = () => enabledRef.current && accountRef.current === account && refreshGeneration.current === generation;
		const shouldAdopt = () => current() && savedHostUrls.current.get(saved.hostId) === saved.url;
		const settle = (change: Partial<RemoteHost>) => setHosts((list) => list.map((host) =>
			host.hostId === saved.hostId && host.url === saved.url ? { ...host, failureReason: undefined, ...change } : host));
		try {
			const connected = await connectHost(saved.url, saved.hostId, shouldAdopt);
			if (!shouldAdopt()) {
				if (savedHostUrls.current.get(saved.hostId) !== saved.url) await aoBridge.remotes.disconnect(saved.url);
				return;
			}
			retries.current.delete(saved.hostId);
			settle({ hostId: connected.hostId, status: "connected" });
		} catch (error) {
			if (!current()) return;
			if (isOfflineError(error)) scheduleRetry(retries.current, saved.hostId);
			else retries.current.delete(saved.hostId);
			// A failed reconnect must not leave the old proxy marked connected.
			try { await disconnectHost(saved.hostId); } catch { /* Keep the offline state visible. */ }
			if (!current()) return;
			const failureReason = failureReasonOf(error);
			settle({ status: "offline", ...(failureReason ? { failureReason } : {}) });
		}
	}, []);

	const attemptOnce = useCallback((host: SavedHost) => {
		if (inFlight.current.has(host.hostId)) return Promise.resolve();
		inFlight.current.add(host.hostId);
		return attempt(host, refreshGeneration.current, accountRef.current).finally(() => inFlight.current.delete(host.hostId));
	}, [attempt]);

	const refresh = useCallback(async () => {
		if (!enabledRef.current) return;
		const generation = ++refreshGeneration.current;
		const current = () => enabledRef.current && accountRef.current === accountId && refreshGeneration.current === generation;
		let saved = await aoBridge.remotes.list();
		if (cloudBaseUrl) {
			try {
				await aoBridge.remotes.syncAccountHosts(accountId);
				saved = await aoBridge.remotes.list();
			} catch { /* Cloud outage must not hide locally paired machines. */ }
		}
		if (!current()) return;
		await Promise.all(connectedHosts().filter((hostId) => !saved.some((host) => host.hostId === hostId)).map(disconnectHost));
		if (!current()) return;
		savedHostUrls.current = new Map(saved.map((host) => [host.hostId, host.url]));
		setListed(true);
		setHosts((previous) => saved.map((host) => ({
			...host,
			status: previous.some((entry) => entry.hostId === host.hostId && entry.url === host.url && entry.status === "connected") ? "connected" : "connecting",
		})));
		await Promise.all(saved.map((host) => attempt(host, generation, accountId)));
	}, [attempt, cloudBaseUrl, accountId]);

	const retryOfflineNow = useCallback(() => {
		const due = hostsRef.current.filter((host) => host.status === "offline" && retries.current.has(host.hostId));
		for (const host of due) retries.current.delete(host.hostId);
		return Promise.all(due.map(attemptOnce)).then(() => undefined);
	}, [attemptOnce]);
	const retryNow = useCallback(async () => {
		retries.current.clear();
		await refresh();
	}, [refresh]);

	useEffect(() => {
		if (previousAccount.current !== accountId) {
			previousAccount.current = accountId;
			refreshGeneration.current++;
			setHosts([]);
			setListed(false);
			for (const hostId of connectedHosts()) void disconnectHost(hostId);
		}
		if (enabled) {
			void refresh();
			return;
		}
		refreshGeneration.current++;
		setHosts([]);
		setListed(false);
		retries.current.clear();
		savedHostUrls.current.clear();
		for (const hostId of connectedHosts()) void disconnectHost(hostId);
	}, [enabled, refresh]);
	useEffect(() => {
		if (!enabled) return;
		const timer = setInterval(() => {
			const now = Date.now();
			for (const host of hostsRef.current) {
				const retry = retries.current.get(host.hostId);
				if (host.status === "offline" && retry && retry.at <= now) void attemptOnce(host);
			}
		}, RETRY_TICK_MS);
		const onVisible = () => { if (!document.hidden) void retryOfflineNow(); };
		const onOnline = () => { void retryOfflineNow(); };
		const onCheck = (event: Event) => {
			const host = hostsRef.current.find((entry) => entry.hostId === (event as CustomEvent<string>).detail);
			if (host?.status === "connected") void attemptOnce(host);
		};
		window.addEventListener("focus", onVisible);
		document.addEventListener("visibilitychange", onVisible);
		window.addEventListener("online", onOnline);
		window.addEventListener(HOST_CHECK_EVENT, onCheck);
		return () => {
			clearInterval(timer);
			window.removeEventListener("focus", onVisible);
			document.removeEventListener("visibilitychange", onVisible);
			window.removeEventListener("online", onOnline);
			window.removeEventListener(HOST_CHECK_EVENT, onCheck);
		};
	}, [enabled, attemptOnce, retryOfflineNow]);
	useEffect(() => {
		if (!enabled) return;
		const onChanged = () => { void refresh(); };
		window.addEventListener(HOSTS_CHANGED_EVENT, onChanged);
		return () => window.removeEventListener(HOSTS_CHANGED_EVENT, onChanged);
	}, [enabled, refresh]);
	useEffect(() => {
		if (!enabled || !cloudBaseUrl) return;
		const timer = setInterval(() => { if (!document.hidden) void refresh(); }, ACCOUNT_SYNC_MS);
		return () => clearInterval(timer);
	}, [enabled, cloudBaseUrl, refresh]);

	return {
		hosts: enabled ? hosts : NO_HOSTS,
		listed: enabled && listed,
		// Concluded, per the doc comment above. Deliberately mirrors the
		// `enabled` computation's inputs so the two cannot drift.
		settled: !remoteHostsEnabled || status === "unauthenticated" || listed,
		refresh,
		retryNow,
	};
}

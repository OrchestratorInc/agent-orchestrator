import * as Crypto from "expo-crypto";
import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { AppState } from "react-native";
import { getAccountAccessToken, loadAccount, onAccountChange, signInToAccount, signOutOfAccount, type Account } from "../account";
import { clearAccountHosts, syncAccountHosts } from "../accountHosts";
import { shouldPoll } from "../appStatePoll";
import { fetch as expoFetch } from "expo/fetch";
import { router } from "expo-router";
import { forgetCloudAccountCaches } from "./accountCaches";
import { forgetCloudDrafts } from "./drafts";
import { collectPages, createCloudClient, type CloudClient } from "./client";
import { CLOUD_API_BASE_URL } from "./config";
import { CloudAuthError, cloudErrorMessage } from "./errors";
import { resolveOrganization } from "./org";
import { presentPlanRefusal } from "./planRefusal";
import { registerCloudPush, unregisterCloudPush } from "./push";
import type { CloudMe, CloudOrganization, CloudProject, CloudProviderConnection, CloudSession } from "./types";

export type CloudStatus = "loading" | "signed-out" | "connecting" | "ready" | "error";

export type CloudState = {
	status: CloudStatus;
	account: Account | null;
	me: CloudMe | null;
	org: CloudOrganization | null;
	/** Why the workspace could not load; null while it is fine. */
	error: string | null;
	client: CloudClient;
	projects: CloudProject[];
	projectsLoaded: boolean;
	/** Why the last project read failed, apart from the workspace's error, which another read can clear. */
	projectsError: string | null;
	sessions: CloudSession[];
	sessionsLoaded: boolean;
	/** Why the last session read failed. */
	sessionsError: string | null;
	connections: CloudProviderConnection[];
	connectionsLoaded: boolean;
	notificationsUnread: number;
	/** Harnesses that can start in Chat; the rest start in the terminal. */
	chatHarnesses: readonly string[];
	signIn(): Promise<Account | null>;
	/**
	 * Stops Orchestrator Cloud pushing to this phone, then signs out. Throws
	 * CloudPushRemovalError, still signed in, when the push registration
	 * could not be removed, unless `keepPush` says to sign out anyway.
	 */
	signOut(options?: { keepPush?: boolean }): Promise<void>;
	refresh(): Promise<void>;
	refreshProjects(): Promise<void>;
	refreshSessions(): Promise<void>;
	refreshConnections(): Promise<void>;
	refreshNotifications(): Promise<void>;
	/** Put a session the phone just created or changed into the list without waiting for the poll. */
	upsertSession(session: CloudSession): void;
	/** A cloud screen is on view: poll at the live rate until the returned release runs. */
	watch(): () => void;
};

const CloudContext = createContext<CloudState | null>(null);

export function useCloud(): CloudState {
	const value = useContext(CloudContext);
	if (!value) throw new Error("useCloud must be used within <CloudProvider>");
	return value;
}

// While a cloud screen is open the board is live; otherwise a slow tick keeps
// the unread badge and the sidebar honest without spending battery.
const LIVE_POLL_MS = 5_000;
const IDLE_POLL_MS = 30_000;
// A control plane that doesn't say which harnesses run in Chat starts every
// session in the terminal, as the desktop reads it.
const NO_CHAT_HARNESSES: readonly string[] = [];

/** Sign-out stopped because Orchestrator Cloud would have kept pushing to this phone. */
export class CloudPushRemovalError extends Error {
	constructor() {
		super("Orchestrator Cloud couldn't be told to stop sending this account's notifications to this phone.");
		this.name = "CloudPushRemovalError";
	}
}

export function CloudProvider({ children }: { children: ReactNode }) {
	const [status, setStatus] = useState<CloudStatus>("loading");
	const [account, setAccount] = useState<Account | null>(null);
	const [me, setMe] = useState<CloudMe | null>(null);
	const [org, setOrg] = useState<CloudOrganization | null>(null);
	const [error, setError] = useState<string | null>(null);
	const [projects, setProjects] = useState<CloudProject[]>([]);
	const [projectsLoaded, setProjectsLoaded] = useState(false);
	const [projectsError, setProjectsError] = useState<string | null>(null);
	const [sessions, setSessions] = useState<CloudSession[]>([]);
	const [sessionsLoaded, setSessionsLoaded] = useState(false);
	const [sessionsError, setSessionsError] = useState<string | null>(null);
	const [connections, setConnections] = useState<CloudProviderConnection[]>([]);
	const [connectionsLoaded, setConnectionsLoaded] = useState(false);
	const [notificationsUnread, setNotificationsUnread] = useState(0);
	const [watchers, setWatchers] = useState(0);
	// Every async result checks it still belongs to the account and org that asked.
	const generation = useRef(0);
	const orgRef = useRef<CloudOrganization | null>(null);
	orgRef.current = org;
	const resolveRef = useRef<() => Promise<void>>(async () => {});
	// The push registration a connect started, unawaited: sign-out lets it land
	// before removing it, and starts no other while it runs, so a late PUT can't
	// register the phone again after its removal.
	const pushRegistration = useRef<Promise<unknown>>(Promise.resolve());
	const signingOut = useRef(false);

	const client = useMemo(() => createCloudClient({
		baseUrl: CLOUD_API_BASE_URL,
		getToken: getAccountAccessToken,
		// The session event stream reads its body as it arrives; uploads send raw bytes.
		nativeFetch: expoFetch as unknown as typeof fetch,
		newKey: () => Crypto.randomUUID(),
		// Membership changed under us: re-resolve the organization.
		onForbidden: () => { void resolveRef.current(); },
		// Whatever asked (New task, a paused session, a review, a terminal),
		// one sheet says why the plan refused it, over the plan's usage.
		onPlanRefused: (refusal) => {
			presentPlanRefusal(refusal, (params) => router.push({ pathname: "/sheets/cloud-plan", params }));
		},
	}), []);

	const reset = useCallback(() => {
		generation.current++;
		forgetCloudAccountCaches();
		setMe(null);
		setOrg(null);
		setProjects([]);
		setProjectsLoaded(false);
		setProjectsError(null);
		setSessions([]);
		setSessionsLoaded(false);
		setSessionsError(null);
		setConnections([]);
		setConnectionsLoaded(false);
		setNotificationsUnread(0);
		setError(null);
	}, []);

	const fail = useCallback((cause: unknown, ownGeneration: number) => {
		if (ownGeneration !== generation.current) return;
		if (cause instanceof CloudAuthError) {
			// The refresh token is gone; the account module already signed out.
			setError(cloudErrorMessage(cause));
			setStatus("error");
			return;
		}
		setError(cloudErrorMessage(cause));
	}, []);

	const refreshProjects = useCallback(async () => {
		const current = orgRef.current;
		const own = generation.current;
		if (!current) return;
		try {
			const items = await collectPages((cursor) => client.listProjects(current.id, { cursor }));
			if (own !== generation.current) return;
			setProjects(items);
			setProjectsLoaded(true);
			setProjectsError(null);
		} catch (cause) {
			if (own === generation.current) setProjectsError(cloudErrorMessage(cause));
			fail(cause, own);
		}
	}, [client, fail]);

	const refreshSessions = useCallback(async () => {
		const current = orgRef.current;
		const own = generation.current;
		if (!current) return;
		try {
			const items = await collectPages((cursor) => client.listSessions(current.id, { cursor }), 5);
			if (own !== generation.current) return;
			setSessions(items);
			setSessionsLoaded(true);
			setSessionsError(null);
			setError(null);
		} catch (cause) {
			if (own === generation.current) setSessionsError(cloudErrorMessage(cause));
			fail(cause, own);
		}
	}, [client, fail]);

	const refreshConnections = useCallback(async () => {
		const own = generation.current;
		if (!orgRef.current) return;
		try {
			const { providerConnections } = await client.listProviderConnections();
			if (own !== generation.current) return;
			setConnections(providerConnections ?? []);
			setConnectionsLoaded(true);
		} catch (cause) { fail(cause, own); }
	}, [client, fail]);

	const refreshNotifications = useCallback(async () => {
		const current = orgRef.current;
		const own = generation.current;
		if (!current) return;
		try {
			const page = await client.listNotifications(current.id, { status: "unread", limit: 1 });
			if (own !== generation.current) return;
			setNotificationsUnread(page.unreadCount ?? 0);
		} catch {
			// The badge is a convenience; a failed read keeps the last count.
		}
	}, [client]);

	const resolve = useCallback(async () => {
		let own = generation.current;
		setStatus((previous) => (previous === "ready" ? previous : "connecting"));
		try {
			const nextMe = await client.getMe();
			if (own !== generation.current) return;
			const nextOrg = await resolveOrganization(client, nextMe);
			if (own !== generation.current) return;
			await draftsCleared.current;
			if (own !== generation.current) return;
			// Another organization than the one shown (a 403 re-resolved it):
			// what was loaded for the old one goes, and its loads still out are dropped.
			if (orgRef.current && orgRef.current.id !== nextOrg.id) {
				own = ++generation.current;
				forgetCloudAccountCaches();
				setProjects([]);
				setProjectsLoaded(false);
				setSessions([]);
				setSessionsLoaded(false);
				setNotificationsUnread(0);
			}
			setMe(nextMe);
			setOrg(nextOrg);
			orgRef.current = nextOrg;
			setError(null);
			setStatus("ready");
			await Promise.all([refreshProjects(), refreshSessions(), refreshConnections(), refreshNotifications()]);
			if (!signingOut.current) pushRegistration.current = registerCloudPush(client, { ask: false }).catch(() => {});
		} catch (cause) {
			if (own !== generation.current) return;
			setError(cloudErrorMessage(cause));
			setStatus("error");
		}
	}, [client, refreshConnections, refreshNotifications, refreshProjects, refreshSessions]);
	resolveRef.current = resolve;

	// The account last adopted. Drafts outlive a restart, so they go only when
	// one account gives way to none or another, never when the app opens.
	const adopted = useRef<Account | null>(null);
	// The last account's drafts going: the next one's workspace waits for it,
	// so no composer (drawn only once Cloud is ready) reads one first.
	const draftsCleared = useRef<Promise<void>>(Promise.resolve());
	const adopt = useCallback((next: Account | null) => {
		const previous = adopted.current;
		adopted.current = next;
		if (previous && previous.id !== next?.id) draftsCleared.current = forgetCloudDrafts().catch(() => {});
		signingOut.current = false;
		reset();
		setAccount(next);
		if (!next) {
			setStatus("signed-out");
			return;
		}
		void resolve();
	}, [reset, resolve]);

	useEffect(() => {
		let mounted = true;
		void loadAccount().then((value) => { if (mounted) adopt(value); }).catch(() => { if (mounted) adopt(null); });
		const off = onAccountChange((value) => { if (mounted) adopt(value); });
		return () => { mounted = false; off(); };
	}, [adopt]);

	// A workspace that failed to load (offline at launch) tries again on its own.
	useEffect(() => {
		if (status !== "error" || !account) return;
		const retry = () => { if (shouldPoll(AppState.currentState)) void resolveRef.current(); };
		const timer = setInterval(retry, IDLE_POLL_MS);
		const sub = AppState.addEventListener("change", (next) => { if (shouldPoll(next)) retry(); });
		return () => { clearInterval(timer); sub.remove(); };
	}, [account, status]);

	// Poll while the app is in front: fast while a cloud screen watches, slow otherwise.
	useEffect(() => {
		if (status !== "ready") return;
		let timer: ReturnType<typeof setInterval> | undefined;
		const tick = () => {
			if (!shouldPoll(AppState.currentState)) return;
			void refreshSessions();
			void refreshNotifications();
		};
		const start = () => {
			if (timer) clearInterval(timer);
			timer = setInterval(tick, watchers > 0 ? LIVE_POLL_MS : IDLE_POLL_MS);
		};
		start();
		const sub = AppState.addEventListener("change", (next) => {
			if (!shouldPoll(next)) return;
			tick();
			start();
		});
		return () => { if (timer) clearInterval(timer); sub.remove(); };
	}, [refreshNotifications, refreshSessions, status, watchers]);

	const refreshSessionsRef = useRef(refreshSessions);
	refreshSessionsRef.current = refreshSessions;
	const watch = useCallback(() => {
		setWatchers((count) => count + 1);
		void refreshSessionsRef.current();
		let released = false;
		return () => {
			if (released) return;
			released = true;
			setWatchers((count) => Math.max(0, count - 1));
		};
	}, []);

	const signIn = useCallback(async () => {
		const value = await signInToAccount();
		// Same account sync Settings runs: machines linked to this account appear too.
		if (value) void syncAccountHosts(value).catch(() => {});
		return value;
	}, []);

	// Push devices belong to the account, not an organization, so this runs
	// whether or not one was resolved.
	const signOut = useCallback(async ({ keepPush = false }: { keepPush?: boolean } = {}) => {
		signingOut.current = true;
		await pushRegistration.current;
		const removed = await unregisterCloudPush(client);
		if (!removed && !keepPush) {
			// Still signed in: a later connect may register again.
			signingOut.current = false;
			throw new CloudPushRemovalError();
		}
		await signOutOfAccount();
		await clearAccountHosts().catch(() => {});
	}, [client]);

	const statusRef = useRef(status);
	statusRef.current = status;
	const refresh = useCallback(async () => {
		// A workspace in error resolves again: refreshing its lists alone never
		// brings it back to ready.
		if (!orgRef.current || statusRef.current === "error") {
			await resolve();
			return;
		}
		await Promise.all([refreshProjects(), refreshSessions(), refreshConnections(), refreshNotifications()]);
	}, [refreshConnections, refreshNotifications, refreshProjects, refreshSessions, resolve]);

	const upsertSession = useCallback((session: CloudSession) => {
		setSessions((current) => [session, ...current.filter((item) => item.id !== session.id)]);
	}, []);

	const value = useMemo<CloudState>(() => ({
		status, account, me, org, error, client,
		projects, projectsLoaded, projectsError, sessions, sessionsLoaded, sessionsError, connections, connectionsLoaded, notificationsUnread,
		chatHarnesses: me?.chatHarnesses ?? NO_CHAT_HARNESSES,
		signIn, signOut, refresh, refreshProjects, refreshSessions, refreshConnections, refreshNotifications, upsertSession, watch,
	}), [account, client, connections, connectionsLoaded, error, me, notificationsUnread, org, projects, projectsError, projectsLoaded, refresh, refreshConnections, refreshNotifications, refreshProjects, refreshSessions, sessions, sessionsError, sessionsLoaded, signIn, signOut, status, upsertSession, watch]);
	return <CloudContext.Provider value={value}>{children}</CloudContext.Provider>;
}

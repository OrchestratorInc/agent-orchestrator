// AO Cloud's approval gate for cloud agent browsers, fed into the same
// approval store as the local daemon's: cloud requests become
// PendingApprovals (marked `cloud`), so ApprovalHost and ApprovalPrompt show
// and decide them exactly like local ones. The control plane owns every
// decision; this module only reads requests and forwards the user's answer.
//
// The control plane lists approvals per session. Each running cloud session's
// list is read on a slow poll; a browser.approval_* event on any session
// stream already open (the session on screen, for one) rereads that session
// at once. Opening a stream per session just for approvals would spend the
// window's small stream budget.

import { useQueries, useQueryClient, type QueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore } from "react";
import { useCloudCp } from "../hooks/useCloudCp";
import { useWorkspaceQuery } from "../hooks/useWorkspaceQuery";
import type { CloudCpBrowserApproval, CloudCpClient } from "./cloud-cp";
import { CloudCpError } from "./cloud-cp/errors";
import { tapCloudSessionEvents } from "./cloud-cp/session-events-hub";
import { usesPreviewWorkspaceData } from "./preview-mode";
import type { ApprovalDecision, PendingApproval } from "./approvals";
import { retryUnlessHidden } from "./hidden-window";

export interface CloudApprovalTarget {
	baseUrl: string;
	orgId: string;
	sessionId: string;
}

/** Under ["approvals"], so every decision's invalidation rereads it too. */
export const cloudPendingApprovalsQueryKey = (target: CloudApprovalTarget) =>
	["approvals", "pending", "cloud", target.baseUrl.replace(/\/+$/, ""), target.orgId, target.sessionId] as const;

/** Reread of each running cloud session's requests between events. */
export const CLOUD_APPROVAL_POLL_MS = 15_000;
/** Reread of the session on screen, which the person is watching. */
export const CLOUD_APPROVAL_WATCHED_POLL_MS = 5_000;

// Cloud categories in the local prompt's vocabulary. "unknown" is a command
// the gate does not judge, so the person decides; it keeps its own label
// rather than passing for a page script.
const CATEGORIES: Record<string, string> = {
	pay: "payment",
	send: "message",
	post: "publish",
	delete: "delete",
	account: "account",
	script: "script",
	unknown: "unknown",
};

/** A cloud request in the local prompt's shape. */
export function toPendingApproval(request: CloudCpBrowserApproval, target: Omit<CloudApprovalTarget, "sessionId">): PendingApproval {
	return {
		id: request.id,
		sessionId: request.sessionId,
		kind: "browser-action",
		category: CATEGORIES[request.category] ?? "unknown",
		site: request.site,
		// The prompt's headline: what will happen, in one line.
		action: request.summary,
		reason: request.reason,
		createdAt: request.createdAt,
		expiresAt: request.expiresAt,
		cloud: { baseUrl: target.baseUrl.replace(/\/+$/, ""), orgId: target.orgId },
		// Absent from an older control plane, where anyone could decide.
		...(request.canDecide === false ? { waitingFor: { name: request.decider?.name || undefined } } : {}),
	};
}

export async function fetchCloudPendingApprovals(client: CloudCpClient, target: CloudApprovalTarget): Promise<PendingApproval[]> {
	const { pending } = await client.listBrowserApprovals(target.orgId, target.sessionId);
	return pending.map((request) => toPendingApproval(request, target));
}

/**
 * Sends the user's answer to the control plane. A request that already ended
 * (409: decided elsewhere or expired; 404: gone) is not an error the user can
 * act on: the list is reread and the prompt goes away.
 */
export async function decideCloudApproval(
	client: CloudCpClient,
	target: CloudApprovalTarget,
	id: string,
	decision: ApprovalDecision,
): Promise<void> {
	try {
		await client.decideBrowserApproval(target.orgId, target.sessionId, id, { decision });
	} catch (error) {
		if (error instanceof CloudCpError && (error.status === 409 || error.status === 404)) return;
		throw error;
	}
}

/**
 * Milliseconds until the next request expires by this machine's clock, or
 * undefined when none will. It only schedules a reread: expiresAt is the
 * control plane's time, so a request is never hidden by the local clock.
 */
export function nextExpiryIn(requests: PendingApproval[], now: number): number | undefined {
	let next: number | undefined;
	for (const request of requests) {
		const at = Date.parse(request.expiresAt);
		if (!Number.isFinite(at) || at <= now) continue;
		next = next === undefined ? at - now : Math.min(next, at - now);
	}
	return next;
}

const isApprovalEvent = (type: string | undefined) =>
	type === "browser.approval_requested" || type === "browser.approval_decided";

type SessionRef = { orgId: string; sessionId: string };
const refKey = (ref: SessionRef) => `${ref.orgId}/${ref.sessionId}`;

// Cloud sessions read whatever the session lists say: the one on screen
// (registered by its dock), and any that announced an approval on an open
// stream. A session the lists have not caught up with (just created, past
// the list's first page, or not yet "running" there) is otherwise never read.
let watched: SessionRef[] = [];
let heard: SessionRef[] = [];
const refListeners = new Set<() => void>();
const subscribeRefs = (listener: () => void) => {
	refListeners.add(listener);
	return () => {
		refListeners.delete(listener);
	};
};
const getWatched = () => watched;
const getHeard = () => heard;
const notifyRefs = () => {
	for (const listener of refListeners) listener();
};
function addHeard(ref: SessionRef) {
	if (heard.some((known) => refKey(known) === refKey(ref))) return;
	heard = [...heard, ref];
	notifyRefs();
}
/** A session the control plane no longer has stops being read. */
function dropHeard(ref: SessionRef) {
	const next = heard.filter((known) => refKey(known) !== refKey(ref));
	if (next.length === heard.length) return;
	heard = next;
	notifyRefs();
}

/** Keeps a cloud session's approvals read often while it is on screen. */
export function useWatchCloudApprovals(orgId: string | undefined, sessionId: string): void {
	useEffect(() => {
		if (!orgId || !sessionId) return;
		const ref = { orgId, sessionId };
		watched = [...watched, ref];
		notifyRefs();
		return () => {
			watched = watched.filter((candidate) => candidate !== ref);
			notifyRefs();
		};
	}, [orgId, sessionId]);
}

/**
 * Whether this person may decide a listed cloud session's approvals: only the
 * session's creator, or the person it was started for. A session with neither
 * on record (from before they were) is decided by org admins; it is kept,
 * since the list cannot say who those are. Everyone else's are not read
 * org-wide: they show only in that session's dock, read-only.
 */
export function mayDecide(cloud: { createdByUserId?: string; requestedBy?: { userId: string } }, userId: string): boolean {
	if (!cloud.createdByUserId && !cloud.requestedBy) return true;
	return cloud.createdByUserId === userId || cloud.requestedBy?.userId === userId;
}

/** Whether a listed cloud session can have an agent browser waiting. */
export function mayHaveApprovals(cloud: { desiredState?: string; observedState?: string }): boolean {
	return cloud.desiredState === "running" || cloud.observedState === "running";
}

// Approval reads can leave a short trace in the console when what they find
// changes, to follow a missing card: in dev builds, or in a packaged build
// with localStorage["ao.debug.approvals"] = "1".
function tracing(): boolean {
	if (import.meta.env.DEV) return true;
	try {
		return window.localStorage.getItem("ao.debug.approvals") === "1";
	} catch {
		return false;
	}
}
function trace(message: string) {
	if (tracing()) console.info(`[approvals] ${message}`);
}
const lastCounts = new Map<string, number>();
function traceRead(sessionId: string, count: number) {
	if (lastCounts.get(sessionId) === count) return;
	lastCounts.set(sessionId, count);
	trace(`cloud session ${sessionId}: ${count} pending`);
}

const CLOUD_PENDING_PREFIX = ["approvals", "pending", "cloud"] as const;

/**
 * The pending requests of every cloud session the live reader still reads, as
 * the control plane last listed them. A session it stopped reading (stopped,
 * gone, or another account's after a sign-out or org switch) keeps its query
 * in the cache for a while with no observer; its list is not shown.
 */
export function cachedCloudPendingApprovals(queryClient: QueryClient): PendingApproval[] {
	return queryClient
		.getQueriesData<PendingApproval[]>({ queryKey: CLOUD_PENDING_PREFIX, type: "active" })
		.flatMap(([, data]) => data ?? []);
}

/**
 * Pending approvals for cloud sessions, read straight from the query cache
 * the app shell's live reader fills. Reading them fetches nothing.
 */
export function useCloudPendingApprovals(): PendingApproval[] {
	const queryClient = useQueryClient();
	const last = useRef<PendingApproval[]>([]);
	const subscribe = useCallback((listener: () => void) => queryClient.getQueryCache().subscribe(listener), [queryClient]);
	const read = useCallback(() => {
		const next = cachedCloudPendingApprovals(queryClient);
		const same = next.length === last.current.length && next.every((request, index) => request === last.current[index]);
		if (!same) last.current = next;
		return last.current;
	}, [queryClient]);
	return useSyncExternalStore(subscribe, read, read);
}

/**
 * The app shell's one live reader: reads every running cloud session's
 * requests into the query cache and listens for approval events.
 */
export function useLiveCloudPendingApprovals(): PendingApproval[] {
	const queryClient = useQueryClient();
	const { client, ready, baseUrl, userId } = useCloudCp();
	const workspaces = useWorkspaceQuery();
	const base = baseUrl.replace(/\/+$/, "");
	const watchedRefs = useSyncExternalStore(subscribeRefs, getWatched, getWatched);
	const heardRefs = useSyncExternalStore(subscribeRefs, getHeard, getHeard);
	const targets = useMemo(() => {
		if (!ready || usesPreviewWorkspaceData) return [];
		const found = new Map<string, CloudApprovalTarget & { watched: boolean }>();
		const add = (ref: SessionRef, isWatched: boolean) => {
			const known = found.get(refKey(ref));
			found.set(refKey(ref), { baseUrl: base, ...ref, watched: isWatched || Boolean(known?.watched) });
		};
		for (const workspace of workspaces.data ?? []) {
			for (const session of workspace.sessions) {
				if (session.cloud?.orgId && mayHaveApprovals(session.cloud) && mayDecide(session.cloud, userId)) {
					add({ orgId: session.cloud.orgId, sessionId: session.id }, false);
				}
			}
		}
		for (const ref of heardRefs) add(ref, false);
		for (const ref of watchedRefs) add(ref, true);
		return [...found.values()];
	}, [base, heardRefs, ready, userId, watchedRefs, workspaces.data]);

	useEffect(() => {
		if (!ready) return;
		return tapCloudSessionEvents((target, event) => {
			if (!isApprovalEvent(event.type)) return;
			addHeard({ orgId: target.orgId, sessionId: target.sessionId });
			void queryClient.invalidateQueries({ queryKey: cloudPendingApprovalsQueryKey(target) });
		});
	}, [queryClient, ready]);

	const results = useQueries({
		queries: targets.map((target) => ({
			queryKey: cloudPendingApprovalsQueryKey(target),
			queryFn: () =>
				fetchCloudPendingApprovals(client, target).then((pending) => {
					traceRead(target.sessionId, pending.length);
					return pending;
				}, (error: unknown) => {
					if (error instanceof CloudCpError && error.status === 404) {
						// Gone (deleted, or never this person's): stop reading it.
						dropHeard(target);
						return [];
					}
					// A failed read must not pass for "nothing pending" without a trace.
					console.warn(`Could not read browser approvals for cloud session ${target.sessionId}`, error);
					throw error;
				}),
			refetchInterval: target.watched ? CLOUD_APPROVAL_WATCHED_POLL_MS : CLOUD_APPROVAL_POLL_MS,
			// People step away while an agent works: a request must reach the
			// app while its window is hidden or unfocused, not on its return.
			refetchIntervalInBackground: true,
			// A retry started while the window is hidden would pause until it is
			// shown; the poll above tries again instead.
			retry: retryUnlessHidden,
		})),
	});
	const all = results.flatMap((result) => result.data ?? []);

	// A request the user leaves alone ends at its expiry: reread then, so it
	// leaves once the control plane no longer lists it.
	const [now, setNow] = useState(() => Date.now());
	const wait = nextExpiryIn(all, now);
	useEffect(() => {
		if (wait === undefined) return;
		const timer = setTimeout(() => {
			setNow(Date.now());
			void queryClient.invalidateQueries({ queryKey: ["approvals", "pending", "cloud"] });
		}, wait + 50);
		return () => clearTimeout(timer);
	}, [queryClient, wait]);
	const targetList = targets.map((target) => target.sessionId).join(", ");
	useEffect(() => {
		trace(`reading browser approvals for ${targets.length} cloud session(s)${targetList ? `: ${targetList}` : ""}`);
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [targetList]);
	return all;
}

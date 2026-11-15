// AO Cloud's approval gate for cloud agent browsers, fed into the same
// approval store as the local daemon's: cloud requests become
// PendingApprovals (marked `cloud`), so ApprovalHost and ApprovalPrompt show
// and decide them exactly like local ones. The control plane owns every
// decision; this module only reads requests and forwards the user's answer.

import { useQuery, useQueryClient, type QueryClient } from "@tanstack/react-query";
import { useEffect, useSyncExternalStore } from "react";
import { useCloudCp } from "../hooks/useCloudCp";
import { useCloudOrg } from "../hooks/useCloudOrg";
import type { CloudCpBrowserApproval, CloudCpBrowserApprovalDecision, CloudCpClient } from "./cloud-cp";
import { CloudCpError } from "./cloud-cp/errors";
import { subscribeBrowserApprovalsBridged, type SubscribeBrowserApprovalsBridgedOptions } from "./cloud-cp/stream-bridge";
import { usesPreviewWorkspaceData } from "./preview-mode";
import { computeSseRetryDelayMs } from "./sse-backoff";
import type { ApprovalDecision, PendingApproval } from "./approvals";

/** Under ["approvals"], so every decision's invalidation rereads it too. */
export const cloudPendingApprovalsQueryKey = (baseUrl: string, orgId: string) =>
	["approvals", "pending", "cloud", baseUrl.replace(/\/+$/, ""), orgId] as const;

/** Reread while the stream is down. */
const CLOUD_FALLBACK_POLL_MS = 15_000;
// The control plane sends a keepalive every 15 s; a stream silent for longer
// than this is treated as dead and reopened, even if it never errored.
export const CLOUD_STREAM_SILENCE_MS = 35_000;

const CLOUD_DECISIONS: Record<ApprovalDecision, CloudCpBrowserApprovalDecision> = {
	"allow-once": "allow_once",
	"allow-site": "allow_site",
	deny: "deny",
};

/** A cloud request in the local prompt's shape. */
export function toPendingApproval(request: CloudCpBrowserApproval, cloud: { baseUrl: string; orgId: string }): PendingApproval {
	const savedLogin = request.kind === "saved_login";
	return {
		id: request.id,
		sessionId: request.sessionId,
		kind: savedLogin ? "credential-use" : "browser-action",
		category: request.category,
		site: request.site,
		url: request.url,
		pageTitle: request.pageTitle,
		action: request.action,
		reason: request.reason,
		// A saved login names the vault item it would use, never its secret.
		element: savedLogin ? request.credentialLabel ?? request.element : request.element,
		image: request.image,
		createdAt: request.createdAt,
		expiresAt: request.expiresAt,
		cloud: { baseUrl: cloud.baseUrl.replace(/\/+$/, ""), orgId: cloud.orgId },
	};
}

export async function fetchCloudPendingApprovals(client: CloudCpClient, baseUrl: string, orgId: string): Promise<PendingApproval[]> {
	const { approvals } = await client.listOrgBrowserApprovals(orgId);
	return approvals.map((request) => toPendingApproval(request, { baseUrl, orgId }));
}

/**
 * Sends the user's answer to the control plane. A request that already ended
 * (409, or gone) is not an error the user can act on: the list is reread and
 * the prompt goes away.
 */
export async function decideCloudApproval(client: CloudCpClient, orgId: string, id: string, decision: ApprovalDecision): Promise<void> {
	try {
		await client.decideBrowserApproval(orgId, id, { decision: CLOUD_DECISIONS[decision] });
	} catch (error) {
		if (error instanceof CloudCpError && (error.status === 409 || error.status === 404)) return;
		throw error;
	}
}

export interface CloudApprovalStreamDeps {
	subscribe: (options: Omit<SubscribeBrowserApprovalsBridgedOptions, "baseUrl" | "orgId">) => Promise<void>;
	/** Rereads the pending list. */
	refresh: () => void;
	/** Whether the stream is delivering right now; the poll runs while it is not. */
	setLive: (live: boolean) => void;
	now?: () => number;
	random?: () => number;
}

/**
 * Holds the org's approval stream open: every frame (and every reconnect)
 * rereads the pending list, and a stream that ends, errors or goes silent is
 * reopened with the same backoff as the local stream.
 */
export function connectCloudApprovalStream(deps: CloudApprovalStreamDeps): () => void {
	const now = deps.now ?? Date.now;
	const stop = new AbortController();
	let failures = 0;
	let lastSeen = now();
	let current: AbortController | undefined;
	const watchdog = setInterval(() => {
		if (current && now() - lastSeen > CLOUD_STREAM_SILENCE_MS) current.abort();
	}, 10_000);
	const wait = (ms: number) =>
		new Promise<void>((resolve) => {
			const timer = setTimeout(done, ms);
			function done() {
				clearTimeout(timer);
				stop.signal.removeEventListener("abort", done);
				resolve();
			}
			stop.signal.addEventListener("abort", done, { once: true });
		});
	void (async () => {
		while (!stop.signal.aborted) {
			const attempt = new AbortController();
			current = attempt;
			const abortAttempt = () => attempt.abort();
			stop.signal.addEventListener("abort", abortAttempt, { once: true });
			lastSeen = now();
			await deps.subscribe({
				signal: attempt.signal,
				onOpen: () => {
					lastSeen = now();
				},
				onActivity: () => {
					lastSeen = now();
				},
				onFrame: () => {
					// The first frame (the snapshot) proves the stream works.
					failures = 0;
					deps.setLive(true);
					deps.refresh();
				},
			});
			stop.signal.removeEventListener("abort", abortAttempt);
			current = undefined;
			if (stop.signal.aborted) break;
			deps.setLive(false);
			// The poll covers the gap; refresh now so nothing waits for it.
			deps.refresh();
			failures += 1;
			await wait(computeSseRetryDelayMs(failures, deps.random));
		}
		clearInterval(watchdog);
		deps.setLive(false);
	})();
	return () => {
		stop.abort();
		clearInterval(watchdog);
	};
}

let liveOrgs: string[] = [];
const liveListeners = new Set<() => void>();
const subscribeLive = (listener: () => void) => {
	liveListeners.add(listener);
	return () => liveListeners.delete(listener);
};
const getLiveOrgs = () => liveOrgs;
function setOrgLive(key: string, live: boolean) {
	if (liveOrgs.includes(key) === live) return;
	liveOrgs = live ? [...liveOrgs, key] : liveOrgs.filter((candidate) => candidate !== key);
	for (const listener of liveListeners) listener();
}

/**
 * Pending approvals for cloud sessions in the signed-in org. The app shell
 * passes `live` once: it fetches and holds the org's stream open. Other
 * readers (a cloud session's dock) share that cache and never fetch.
 */
export function useCloudPendingApprovals({ live = false }: { live?: boolean } = {}): PendingApproval[] {
	const queryClient = useQueryClient();
	const { client, ready, baseUrl } = useCloudCp();
	const { org } = useCloudOrg();
	const orgId = org?.id ?? "";
	const base = baseUrl.replace(/\/+$/, "");
	const key = cloudPendingApprovalsQueryKey(base, orgId);
	const streamKey = `${base}|${orgId}`;
	const enabled = live && ready && orgId !== "" && !usesPreviewWorkspaceData;
	const streaming = useSyncExternalStore(subscribeLive, getLiveOrgs, getLiveOrgs).includes(streamKey);
	useEffect(() => {
		if (!enabled) return;
		return retainCloudApprovalStream(queryClient, base, orgId);
	}, [base, enabled, orgId, queryClient]);
	const query = useQuery({
		queryKey: key,
		queryFn: () => fetchCloudPendingApprovals(client, base, orgId),
		enabled,
		refetchInterval: enabled && !streaming ? CLOUD_FALLBACK_POLL_MS : false,
	});
	return ready && orgId !== "" ? query.data ?? [] : [];
}

const streams = new Map<string, { refs: number; dispose: () => void }>();

function retainCloudApprovalStream(queryClient: QueryClient, baseUrl: string, orgId: string): () => void {
	const streamKey = `${baseUrl}|${orgId}`;
	let entry = streams.get(streamKey);
	if (!entry) {
		const dispose = connectCloudApprovalStream({
			subscribe: (options) => subscribeBrowserApprovalsBridged({ ...options, baseUrl, orgId }),
			refresh: () => void queryClient.invalidateQueries({ queryKey: cloudPendingApprovalsQueryKey(baseUrl, orgId) }),
			setLive: (live) => setOrgLive(streamKey, live),
		});
		entry = { refs: 0, dispose };
		streams.set(streamKey, entry);
	}
	entry.refs += 1;
	return () => {
		const held = streams.get(streamKey);
		if (!held) return;
		held.refs -= 1;
		if (held.refs === 0) {
			held.dispose();
			streams.delete(streamKey);
		}
	};
}

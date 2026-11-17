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

import { useQueries, useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo, useState, useSyncExternalStore } from "react";
import { useCloudCp } from "../hooks/useCloudCp";
import { useWorkspaceQuery } from "../hooks/useWorkspaceQuery";
import type { CloudCpBrowserApproval, CloudCpClient } from "./cloud-cp";
import { CloudCpError } from "./cloud-cp/errors";
import { tapCloudSessionEvents } from "./cloud-cp/session-events-hub";
import { usesPreviewWorkspaceData } from "./preview-mode";
import type { ApprovalDecision, PendingApproval } from "./approvals";

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

// Cloud categories in the local prompt's vocabulary. "unknown" is an action
// whose effect cannot be read in advance, which locally is "script".
const CATEGORIES: Record<string, string> = {
	pay: "payment",
	send: "message",
	post: "publish",
	delete: "delete",
	account: "account",
	script: "script",
	unknown: "script",
};

/** A cloud request in the local prompt's shape. */
export function toPendingApproval(request: CloudCpBrowserApproval, target: Omit<CloudApprovalTarget, "sessionId">): PendingApproval {
	return {
		id: request.id,
		sessionId: request.sessionId,
		kind: "browser-action",
		category: CATEGORIES[request.category] ?? "script",
		site: request.site,
		// The prompt's headline: what will happen, in one line.
		action: request.summary,
		reason: request.reason,
		createdAt: request.createdAt,
		expiresAt: request.expiresAt,
		cloud: { baseUrl: target.baseUrl.replace(/\/+$/, ""), orgId: target.orgId },
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

/** Requests still waiting at `now`: the worker refuses one once it expires. */
export function unexpired(requests: PendingApproval[], now: number): PendingApproval[] {
	return requests.filter((request) => !(Date.parse(request.expiresAt) <= now));
}

/** Milliseconds until the next request expires, or undefined when none will. */
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

// What the shell's live reader holds, for every other reader.
let published: PendingApproval[] = [];
const listeners = new Set<() => void>();
const subscribePublished = (listener: () => void) => {
	listeners.add(listener);
	return () => {
		listeners.delete(listener);
	};
};
const getPublished = () => published;
function publish(next: PendingApproval[]) {
	const same = next.length === published.length && next.every((request, index) => request === published[index]);
	if (same) return;
	published = next;
	for (const listener of listeners) listener();
}

/**
 * Pending approvals for cloud sessions, as the app shell's live reader last
 * saw them. Reading them fetches nothing.
 */
export function useCloudPendingApprovals(): PendingApproval[] {
	return useSyncExternalStore(subscribePublished, getPublished, getPublished);
}

/**
 * The app shell's one live reader: reads every running cloud session's
 * requests, listens for approval events, and publishes what it finds.
 */
export function useLiveCloudPendingApprovals(): PendingApproval[] {
	const queryClient = useQueryClient();
	const { client, ready, baseUrl } = useCloudCp();
	const workspaces = useWorkspaceQuery();
	const base = baseUrl.replace(/\/+$/, "");
	const targets = useMemo(() => {
		const found: CloudApprovalTarget[] = [];
		if (!ready || usesPreviewWorkspaceData) return found;
		for (const workspace of workspaces.data ?? []) {
			for (const session of workspace.sessions) {
				if (session.cloud?.orgId && session.cloud.observedState === "running") {
					found.push({ baseUrl: base, orgId: session.cloud.orgId, sessionId: session.id });
				}
			}
		}
		return found;
	}, [base, ready, workspaces.data]);

	useEffect(() => {
		if (!ready) return;
		return tapCloudSessionEvents((target, event) => {
			if (!isApprovalEvent(event.type)) return;
			void queryClient.invalidateQueries({ queryKey: cloudPendingApprovalsQueryKey(target) });
		});
	}, [queryClient, ready]);

	const results = useQueries({
		queries: targets.map((target) => ({
			queryKey: cloudPendingApprovalsQueryKey(target),
			queryFn: () => fetchCloudPendingApprovals(client, target),
			refetchInterval: CLOUD_APPROVAL_POLL_MS,
		})),
	});
	const all = results.flatMap((result) => result.data ?? []);

	// A request the user leaves alone ends at its expiry: drop it then, and
	// reread so the list agrees with the control plane.
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
	const current = unexpired(all, Math.max(now, Date.now()));
	useEffect(() => publish(current));
	useEffect(() => () => publish([]), []);
	return current;
}

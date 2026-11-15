// The approval gate as the renderer sees it: agent actions waiting for the
// user (a risky browser click, a saved login's first use on a site), their
// decisions, the per-session mode, remembered sites, and the audit log. The
// daemon owns every decision; this module only reads and forwards the user's.

import { useQueries, useQuery, useQueryClient, type QueryClient } from "@tanstack/react-query";
import { useEffect, useSyncExternalStore } from "react";
import type { components } from "../../api/schema";
import { apiErrorException } from "./api-error-exception";
import { apiClient, eventStreamBaseUrl, getApiBaseUrl, hasTrustedApiBaseUrl, subscribeApiBaseUrl } from "./api-client";
import { useConnectedHosts } from "../hooks/useHostConnection";
import { baseUrlForHost, clientForHost, connectedHosts, subscribeConnectedHosts } from "./host-clients";
import { probeRemoteSse } from "./remote-sse-probe";
import { usesPreviewWorkspaceData } from "./preview-mode";
import { computeSseRetryDelayMs } from "./sse-backoff";

export type PendingApproval = components["schemas"]["ApprovalPendingResponse"] & {
	/** The remote host whose daemon holds the request; absent for this machine. */
	hostId?: string;
};
export type ApprovalAuditEntry = components["schemas"]["ApprovalAuditEntryResponse"];
export type ApprovalPolicy = components["schemas"]["ApprovalPolicyResponse"];
export type ApprovalDecision = "allow-once" | "allow-site" | "deny";
export type ApprovalMode = ApprovalPolicy["mode"];

export const pendingApprovalsQueryKey = ["approvals", "pending"] as const;
export const approvalAuditQueryKey = (sessionId?: string) => ["approvals", "audit", sessionId ?? ""] as const;
export const approvalPolicyQueryKey = (sessionId?: string) => ["approvals", "policy", sessionId ?? ""] as const;
export const remotePendingApprovalsQueryKey = (hostId: string) => ["approvals", "pending", "host", hostId] as const;

/** Fallback reread of a remote host's requests while its stream is down. */
const REMOTE_FALLBACK_POLL_MS = 15_000;

const EVENTSOURCE_CLOSED = 2;

export async function fetchPendingApprovals(): Promise<PendingApproval[]> {
	if (usesPreviewWorkspaceData) return [];
	const { data, error } = await apiClient.GET("/api/v1/approvals");
	if (error || !data) throw apiErrorException(error, "Could not load approval requests");
	return data.pending;
}

export async function decideApproval(
	id: string,
	decision: ApprovalDecision,
	queryClient: QueryClient,
	hostId?: string,
): Promise<void> {
	const client = hostId ? clientForHost(hostId) : apiClient;
	const { error } = await client.POST("/api/v1/approvals/{id}/decision", {
		params: { path: { id } },
		body: { decision },
	});
	// The request may have ended (timed out, or the agent gave up) while the
	// prompt was open; either way the list is stale now.
	await queryClient.invalidateQueries({ queryKey: ["approvals"] });
	if (error) throw apiErrorException(error, "Could not send the decision");
}

export async function fetchApprovalAudit(sessionId?: string, limit = 100): Promise<ApprovalAuditEntry[]> {
	if (usesPreviewWorkspaceData) return [];
	const { data, error } = await apiClient.GET("/api/v1/approvals/audit", {
		params: { query: { limit, ...(sessionId ? { sessionId } : {}) } },
	});
	if (error || !data) throw apiErrorException(error, "Could not load the approval log");
	return data.entries;
}

export async function fetchApprovalPolicy(sessionId?: string): Promise<ApprovalPolicy> {
	if (usesPreviewWorkspaceData) return { mode: "risky", defaultMode: "risky", sites: [] };
	const { data, error } = await apiClient.GET("/api/v1/approvals/policy", {
		params: { query: sessionId ? { sessionId } : {} },
	});
	if (error || !data) throw apiErrorException(error, "Could not load approval settings");
	return data;
}

export async function setApprovalMode(sessionId: string, mode: ApprovalMode, queryClient?: QueryClient): Promise<ApprovalPolicy> {
	const { data, error } = await apiClient.PUT("/api/v1/approvals/policy", { body: { sessionId, mode } });
	await queryClient?.invalidateQueries({ queryKey: ["approvals", "policy"] });
	if (error || !data) throw apiErrorException(error, "Could not save approval settings");
	return data;
}

export async function forgetApprovalSite(site: string, category: string, queryClient: QueryClient): Promise<void> {
	const { error } = await apiClient.DELETE("/api/v1/approvals/sites", { params: { query: { site, category } } });
	await queryClient.invalidateQueries({ queryKey: ["approvals", "policy"] });
	if (error) throw apiErrorException(error, "Could not save approval settings");
}

let streamRefs = 0;
let disposeStream: (() => void) | undefined;

/**
 * One shared stream for the whole renderer. Every approval event (and every
 * reconnect) rereads the pending list and the log; the payload itself is not
 * trusted as state.
 */
function retainApprovalStream(queryClient: QueryClient): () => void {
	streamRefs += 1;
	if (!disposeStream) disposeStream = openApprovalStream(queryClient);
	return () => {
		streamRefs -= 1;
		if (streamRefs === 0) {
			disposeStream?.();
			disposeStream = undefined;
		}
	};
}

function openApprovalStream(queryClient: QueryClient): () => void {
	let source: EventSource | undefined;
	let sourceBaseUrl: string | undefined;
	let retry: ReturnType<typeof setTimeout> | undefined;
	let failures = 0;
	let disposed = false;
	const refresh = () => void queryClient.invalidateQueries({ queryKey: ["approvals"] });
	const close = () => {
		if (retry) clearTimeout(retry);
		retry = undefined;
		source?.close();
		source = undefined;
		sourceBaseUrl = undefined;
	};
	const connect = () => {
		if (disposed || !hasTrustedApiBaseUrl() || typeof EventSource === "undefined") return;
		const baseUrl = getApiBaseUrl();
		if (sourceBaseUrl === baseUrl && (source || retry)) return;
		close();
		sourceBaseUrl = baseUrl;
		const next = new EventSource(`${eventStreamBaseUrl(baseUrl)}/api/v1/approvals/stream`);
		source = next;
		next.addEventListener("ready", () => {
			failures = 0;
			refresh();
		});
		next.addEventListener("approval_requested", refresh);
		next.addEventListener("approval_resolved", refresh);
		next.onerror = () => {
			if (disposed || source !== next || next.readyState !== EVENTSOURCE_CLOSED) return;
			next.close();
			source = undefined;
			failures += 1;
			retry = setTimeout(() => {
				retry = undefined;
				sourceBaseUrl = undefined;
				connect();
			}, computeSseRetryDelayMs(failures));
		};
	};
	const unsubscribe = subscribeApiBaseUrl(connect);
	connect();
	return () => {
		disposed = true;
		unsubscribe();
		close();
	};
}

/**
 * Pending approval requests. The app shell passes `live` once: it fetches
 * and holds the event stream open. Every other reader (a session's dock)
 * shares that cached query and never fetches on its own.
 */
export function usePendingApprovals({ live = false }: { live?: boolean } = {}) {
	const queryClient = useQueryClient();
	useEffect(() => (live && !usesPreviewWorkspaceData ? retainApprovalStream(queryClient) : undefined), [live, queryClient]);
	return useQuery({
		queryKey: pendingApprovalsQueryKey,
		queryFn: fetchPendingApprovals,
		// Only the live reader fetches; the rest read its cache.
		enabled: live,
		// The stream drives updates; this only covers a stream that cannot connect.
		refetchInterval: live ? 15_000 : false,
	});
}

export function useApprovalAudit(sessionId?: string) {
	return useQuery({ queryKey: approvalAuditQueryKey(sessionId), queryFn: () => fetchApprovalAudit(sessionId) });
}

export function useApprovalPolicy(sessionId?: string) {
	return useQuery({ queryKey: approvalPolicyQueryKey(sessionId), queryFn: () => fetchApprovalPolicy(sessionId) });
}

async function fetchRemotePendingApprovals(hostId: string): Promise<PendingApproval[]> {
	const { data, error } = await clientForHost(hostId).GET("/api/v1/approvals");
	if (error || !data) throw apiErrorException(error, "Could not load approval requests");
	return data.pending.map((request) => ({ ...request, hostId }));
}

let streamingHosts: string[] = [];
const streamListeners = new Set<() => void>();
const subscribeStreamingHosts = (listener: () => void) => {
	streamListeners.add(listener);
	return () => streamListeners.delete(listener);
};
const getStreamingHosts = () => streamingHosts;
function setStreaming(hostId: string, streaming: boolean) {
	if (streamingHosts.includes(hostId) === streaming) return;
	streamingHosts = streaming ? [...streamingHosts, hostId] : streamingHosts.filter((id) => id !== hostId);
	for (const listener of streamListeners) listener();
}

/**
 * One approval stream per connected remote host. Each event rereads that
 * host's pending list; while a stream is down the query falls back to a slow
 * poll, so a healthy host adds no steady load.
 */
export function connectRemoteApprovalStreams(queryClient: QueryClient): () => void {
	const connections = new Map<string, { base: string; close: () => void }>();
	const refresh = (hostId: string) =>
		void queryClient.invalidateQueries({ queryKey: remotePendingApprovalsQueryKey(hostId) });
	const sync = () => {
		const active = new Set(connectedHosts());
		for (const [hostId, connection] of connections) {
			if (active.has(hostId) && baseUrlForHost(hostId) === connection.base) continue;
			connection.close();
			connections.delete(hostId);
			setStreaming(hostId, false);
		}
		for (const hostId of active) {
			const base = baseUrlForHost(hostId);
			if (!base || connections.has(hostId)) continue;
			const close = probeRemoteSse(
				`${base.replace(/\/+$/, "")}/api/v1/approvals/stream`,
				["approval_requested", "approval_resolved"],
				() => refresh(hostId),
				() => {
					setStreaming(hostId, true);
					refresh(hostId);
				},
				() => {
					setStreaming(hostId, false);
					refresh(hostId);
				},
			);
			connections.set(hostId, { base, close });
		}
	};
	const unsubscribe = subscribeConnectedHosts(sync);
	sync();
	return () => {
		unsubscribe();
		for (const [hostId, connection] of connections) {
			connection.close();
			setStreaming(hostId, false);
		}
		connections.clear();
	};
}

/**
 * Pending requests on connected remote hosts (self-hosted agent browsers).
 * The app shell passes `live` once: it holds each host's stream open and
 * fetches; other readers share the cache.
 */
export function useRemotePendingApprovals({ live = false }: { live?: boolean } = {}): PendingApproval[] {
	const queryClient = useQueryClient();
	const hosts = useConnectedHosts();
	const streaming = useSyncExternalStore(subscribeStreamingHosts, getStreamingHosts, getStreamingHosts);
	useEffect(
		() => (live && !usesPreviewWorkspaceData ? connectRemoteApprovalStreams(queryClient) : undefined),
		[live, queryClient],
	);
	const results = useQueries({
		queries: hosts.map((hostId) => ({
			queryKey: remotePendingApprovalsQueryKey(hostId),
			queryFn: () => fetchRemotePendingApprovals(hostId),
			enabled: live && !usesPreviewWorkspaceData,
			refetchInterval: live && !streaming.includes(hostId) ? REMOTE_FALLBACK_POLL_MS : (false as const),
		})),
	});
	return results.flatMap((result) => result.data ?? []);
}

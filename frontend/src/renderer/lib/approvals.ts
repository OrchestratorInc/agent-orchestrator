// The approval gate as the renderer sees it: agent actions waiting for the
// user (a risky browser click, a saved login's first use on a site), their
// decisions, the per-session mode, remembered sites, and the audit log. The
// daemon owns every decision; this module only reads and forwards the user's.

import { useQuery, useQueryClient, type QueryClient } from "@tanstack/react-query";
import { useEffect } from "react";
import type { components } from "../../api/schema";
import { apiErrorException } from "./api-error-exception";
import { apiClient, eventStreamBaseUrl, getApiBaseUrl, hasTrustedApiBaseUrl, subscribeApiBaseUrl } from "./api-client";
import { usesPreviewWorkspaceData } from "./preview-mode";
import { computeSseRetryDelayMs } from "./sse-backoff";

export type PendingApproval = components["schemas"]["ApprovalPendingResponse"];
export type ApprovalAuditEntry = components["schemas"]["ApprovalAuditEntryResponse"];
export type ApprovalPolicy = components["schemas"]["ApprovalPolicyResponse"];
export type ApprovalDecision = "allow-once" | "allow-site" | "deny";
export type ApprovalMode = ApprovalPolicy["mode"];

export const pendingApprovalsQueryKey = ["approvals", "pending"] as const;
export const approvalAuditQueryKey = (sessionId?: string) => ["approvals", "audit", sessionId ?? ""] as const;
export const approvalPolicyQueryKey = (sessionId?: string) => ["approvals", "policy", sessionId ?? ""] as const;

const EVENTSOURCE_CLOSED = 2;

export async function fetchPendingApprovals(): Promise<PendingApproval[]> {
	if (usesPreviewWorkspaceData) return [];
	const { data, error } = await apiClient.GET("/api/v1/approvals");
	if (error || !data) throw apiErrorException(error, "Could not load approval requests");
	return data.pending;
}

export async function decideApproval(id: string, decision: ApprovalDecision, queryClient: QueryClient): Promise<void> {
	const { error } = await apiClient.POST("/api/v1/approvals/{id}/decision", {
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

import type { PROpener } from "@aoagents/product-ui";
import { hashKey, useQuery, useQueryClient, type QueryClient, type QueryKey } from "@tanstack/react-query";
import { useMemo } from "react";
import type { components } from "../../api/schema";
import { apiClient } from "../lib/api-client";
import { clientForHost } from "../lib/host-clients";
import { LOCAL_HOST } from "../lib/hosts";
import { noPRDetails } from "../lib/pr-display";
import type { CloudCpPullRequestMergeAccess, CloudCpPullRequestSummary } from "../lib/cloud-cp";
import { createRendererCloudCpClient } from "../lib/cloud-cp/renderer-client";
import { useCloudSessionEventListener } from "./useCloudSessionEvents";
import { useSettings } from "./useSettings";

// openedBy only comes from AO Cloud, which names the person behind a PR its
// GitHub App opened for them; so does mergeAccess, who may merge it and where.
export type SessionPRSummary = components["schemas"]["SessionPRSummary"] & { openedBy?: PROpener; mergeAccess?: CloudCpPullRequestMergeAccess };
export type SessionPRReference = components["schemas"]["SessionPRReference"];

export const sessionScmSummaryQueryKey = (sessionId?: string, hostId?: string) =>
	sessionId ? (["session-scm-summary", hostId ?? LOCAL_HOST, sessionId] as const) : (["session-scm-summary"] as const);

export async function fetchSessionScmSummary(sessionId: string, hostId?: string) {
	const { data, error } = await (hostId ? clientForHost(hostId) : apiClient).GET("/api/v1/sessions/{sessionId}/pr", {
		params: { path: { sessionId } },
	});
	if (error) throw error;
	return { prs: data?.prs ?? [], linkedPrs: data?.linkedPrs ?? [] };
}

export function cloudPRSummaryToSessionPRSummary(
	pr: CloudCpPullRequestSummary,
	autoInjectCI: boolean,
): SessionPRSummary {
	return {
		...noPRDetails(),
		...pr,
		provider: pr.provider === "gitlab" ? "gitlab" : "github",
		repo: pr.repository,
		ci: { ...pr.ci, checks: [], autoInjectCI },
		review: pr.review,
		mergeability: {
			...pr.mergeability,
			prUrl: pr.mergeability.pullRequestUrl,
		},
	};
}

export function sessionScmSummaryQueryOptions(sessionId: string, hostId?: string) {
	return {
		queryKey: sessionScmSummaryQueryKey(sessionId, hostId),
		enabled: Boolean(sessionId),
		queryFn: () => fetchSessionScmSummary(sessionId, hostId),
		retry: 1,
		...(hostId ? { refetchInterval: 15_000 } : {}),
	};
}

/** How long the SCM events a Cloud session's stream delivers are folded into one refresh. */
export const CLOUD_SCM_REFRESH_DELAY_MS = 500;

interface CloudScmRefresh {
	timer?: ReturnType<typeof setTimeout>;
	running: boolean;
	again: boolean;
}

const cloudScmRefreshes = new WeakMap<QueryClient, Map<string, CloudScmRefresh>>();

/**
 * Re-reads a Cloud session's pull requests after SCM events on its stream,
 * once per burst rather than once per event. A stream that opens with nothing
 * to resume from replays the session's history, one scm.updated for every
 * refresh of every pull request it ever held, and every mounted view of the
 * session hears each one. Invalidating per event started a request per event
 * and view: invalidateQueries cancels a read in flight, but only in the
 * renderer; the main process completes every request (cloudCpFetch). On
 * 2026-11-16 one orchestrator view sent 1,873 reads in 3 s and stalled the
 * control plane. Events are folded for CLOUD_SCM_REFRESH_DELAY_MS, and reads
 * never overlap: one already under way finishes, then one more read starts,
 * so the result still reflects every event folded in.
 */
export function scheduleCloudScmRefresh(queryClient: QueryClient, queryKey: QueryKey): void {
	let refreshes = cloudScmRefreshes.get(queryClient);
	if (!refreshes) {
		refreshes = new Map();
		cloudScmRefreshes.set(queryClient, refreshes);
	}
	const id = hashKey(queryKey);
	let refresh = refreshes.get(id);
	if (!refresh) {
		refresh = { running: false, again: false };
		refreshes.set(id, refresh);
	}
	if (refresh.running) {
		refresh.again = true;
		return;
	}
	if (refresh.timer !== undefined) return;
	const pending = refresh;
	const all = refreshes;
	pending.timer = setTimeout(() => {
		pending.timer = undefined;
		pending.running = true;
		void runCloudScmRefresh(queryClient, queryKey).finally(() => {
			pending.running = false;
			if (pending.again) {
				pending.again = false;
				scheduleCloudScmRefresh(queryClient, queryKey);
			} else if (pending.timer === undefined) {
				all.delete(id);
			}
		});
	}, CLOUD_SCM_REFRESH_DELAY_MS);
}

async function runCloudScmRefresh(queryClient: QueryClient, queryKey: QueryKey): Promise<void> {
	// A read under way may predate the events; let it finish, then read again.
	if (queryClient.getQueryState(queryKey)?.fetchStatus === "fetching") {
		await queryClient.refetchQueries({ queryKey, exact: true }, { cancelRefetch: false }).catch(() => undefined);
	}
	await queryClient.invalidateQueries({ queryKey, exact: true }, { cancelRefetch: false });
}

export function useSessionScmSummary(
	sessionId?: string,
	enabled = true,
	cloudOrgId?: string,
	cloudAutoInjectCI = false,
	hostId?: string,
) {
	const { settings } = useSettings(undefined, Boolean(cloudOrgId));
	const baseUrl = settings?.cloudControlPlaneUrl ?? "";
	const cloudClient = useMemo(() => createRendererCloudCpClient(baseUrl), [baseUrl]);
	const cloud = Boolean(cloudOrgId);
	const queryClient = useQueryClient();
	const queryKey = useMemo(
		() => cloud
			? ["cloud-session-scm-summary", baseUrl, cloudOrgId, sessionId] as const
			: sessionScmSummaryQueryKey(sessionId, hostId),
		[baseUrl, cloud, cloudOrgId, hostId, sessionId],
	);
	// The session's shared event stream says when its pull requests change.
	useCloudSessionEventListener({
		baseUrl,
		orgId: cloudOrgId,
		sessionId,
		enabled,
		onEvent: (event) => {
			if (
				event.type === "scm.updated" ||
				event.type === "pull_request.created" ||
				event.type === "pull_request.claimed"
			) {
				scheduleCloudScmRefresh(queryClient, queryKey);
			}
		},
	});
	return useQuery({
		queryKey,
		enabled: enabled && Boolean(sessionId) && (!cloud || baseUrl !== ""),
		queryFn: async () => {
			if (!cloudOrgId) return fetchSessionScmSummary(sessionId!, hostId);
			const response = await cloudClient.listSessionPullRequests(cloudOrgId, sessionId!);
			return {
				prs: response.pullRequests.map((pr) => cloudPRSummaryToSessionPRSummary(pr, cloudAutoInjectCI)),
				linkedPrs: (response.linkedPullRequests ?? []).map((pr): SessionPRReference => ({
					host: "github.com", number: pr.number, provider: "github", repo: pr.repository, url: pr.url,
				})),
			};
		},
		retry: 1,
		...(hostId ? { refetchInterval: 15_000 } : {}),
	});
}

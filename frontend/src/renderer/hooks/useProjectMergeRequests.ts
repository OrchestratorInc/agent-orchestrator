import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo } from "react";
import type { CloudCpMergeRequest } from "../lib/cloud-cp";
import { useCloudCp } from "./useCloudCp";
import { useCloudSessionLive } from "./useCloudSessionLive";

/**
 * A fallback re-read for while a live stream is reconnecting; a request
 * asked for or decided arrives at once through the session's live state.
 */
export const MERGE_REQUESTS_REFRESH_MS = 60_000;

export function projectMergeRequestsQueryKey(baseUrl: string, orgId: string, projectId: string) {
	return ["cloud-merge-requests", baseUrl, orgId, projectId] as const;
}

/**
 * A shared cloud project's merge requests: all of them for its owner, a
 * guest's own for the guest. Pending ones come first. They are re-read the
 * moment one is asked for or decided, from the live state of liveSessionId,
 * the session on screen (its stream is already open, so watching it adds no
 * presence anywhere else).
 */
export function useProjectMergeRequests(orgId: string | undefined, projectId: string | undefined, enabled = true, liveSessionId?: string) {
	const { client, baseUrl, ready } = useCloudCp();
	const queryClient = useQueryClient();
	const queryKey = useMemo(() => projectMergeRequestsQueryKey(baseUrl, orgId ?? "", projectId ?? ""), [baseUrl, orgId, projectId]);
	const version = useCloudSessionLive(orgId, liveSessionId, enabled && Boolean(liveSessionId))?.mergeRequestsVersion;
	useEffect(() => {
		if (version === undefined) return;
		void queryClient.invalidateQueries({ queryKey });
	}, [queryClient, queryKey, version]);
	return useQuery({
		queryKey,
		enabled: enabled && ready && Boolean(orgId && projectId),
		refetchInterval: MERGE_REQUESTS_REFRESH_MS,
		retry: 1,
		queryFn: async ({ signal }) => (await client.listProjectMergeRequests(orgId!, projectId!, { signal })).mergeRequests,
	});
}

/** The pending request for a pull request, if any. */
export function pendingMergeRequestFor(requests: readonly CloudCpMergeRequest[] | undefined, prUrl: string): CloudCpMergeRequest | undefined {
	return requests?.find((request) => request.status === "pending" && request.pullRequest.url === prUrl);
}

/** Ask, approve and reject, each refreshing the project's requests afterwards. */
export function useMergeRequestActions(orgId: string | undefined, projectId: string | undefined) {
	const { client, baseUrl } = useCloudCp();
	const queryClient = useQueryClient();
	const refresh = () => queryClient.invalidateQueries({ queryKey: projectMergeRequestsQueryKey(baseUrl, orgId ?? "", projectId ?? "") });
	const request = useMutation({
		mutationFn: (input: { sessionId: string; number: number; prUrl: string }) =>
			client.requestPullRequestMerge(orgId!, input.sessionId, input.number, input.prUrl),
		onSuccess: refresh,
	});
	const approve = useMutation({
		mutationFn: (input: { requestId: string; expectedHeadSha: string }) =>
			client.approveMergeRequest(orgId!, input.requestId, input.expectedHeadSha),
		onSuccess: refresh,
	});
	const reject = useMutation({
		mutationFn: (requestId: string) => client.rejectMergeRequest(orgId!, requestId),
		onSuccess: refresh,
	});
	return { request, approve, reject };
}

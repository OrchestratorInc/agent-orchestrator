import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { CloudCpMergeRequest } from "../lib/cloud-cp";
import { useCloudCp } from "./useCloudCp";

/** How often a shared project's merge requests are re-read while shown. */
export const MERGE_REQUESTS_REFRESH_MS = 10_000;

export function projectMergeRequestsQueryKey(baseUrl: string, orgId: string, projectId: string) {
	return ["cloud-merge-requests", baseUrl, orgId, projectId] as const;
}

/**
 * A shared cloud project's merge requests: all of them for its owner, a
 * guest's own for the guest. Pending ones come first.
 */
export function useProjectMergeRequests(orgId: string | undefined, projectId: string | undefined, enabled = true) {
	const { client, baseUrl, ready } = useCloudCp();
	return useQuery({
		queryKey: projectMergeRequestsQueryKey(baseUrl, orgId ?? "", projectId ?? ""),
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

import { apiErrorException } from "../lib/api-error-exception";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { clientForSessionHost } from "../lib/host-clients";
import { usesPreviewWorkspaceData as usePreviewData } from "../lib/preview-mode";
import { sessionScmSummaryQueryKey, type SessionPRSummary } from "./useSessionScmSummary";

export const prDetailsLoadQueryKey = (url: string, hostId?: string) => ["pr-details-load", hostId ?? "local", url] as const;

/**
 * Asks the daemon, once, to read a PR's details when none are stored. The SCM
 * observer reads them only for the open PRs it follows, so a PR that merged or
 * closed before then would otherwise show them loading for good. Returns true
 * when that read failed, so the view can say so instead of waiting.
 */
export function usePRDetailsLoad(pr: SessionPRSummary | undefined, sessionId: string, hostId?: string, enabled = true): boolean {
	const queryClient = useQueryClient();
	const query = useQuery({
		queryKey: prDetailsLoadQueryKey(pr?.url ?? "", hostId),
		enabled: enabled && !usePreviewData && Boolean(pr?.url) && !pr?.detailsObservedAt,
		// One read per PR: the observer keeps an open PR's details current after it.
		staleTime: Infinity,
		retry: false,
		queryFn: async () => {
			const { error } = await clientForSessionHost(hostId).POST("/api/v1/prs/{id}/details", {
				params: { path: { id: String(pr!.number) } },
				body: { prUrl: pr!.url },
			});
			if (error) throw apiErrorException(error);
			// The details are stored apart from the PR's row, so no change event
			// follows: refresh every session on this host that shows the PR.
			const [scope, host] = sessionScmSummaryQueryKey(sessionId, hostId);
			await queryClient.invalidateQueries({
				predicate: ({ queryKey, state }) => queryKey[0] === scope && queryKey[1] === host && (queryKey[2] === sessionId || showsPR(state.data, pr!.url)),
			});
			return true;
		},
	});
	return query.isError && !pr?.detailsObservedAt;
}

function showsPR(summary: unknown, url: string): boolean {
	const { prs = [], linkedPrs = [] } = (summary ?? {}) as { prs?: { url?: string }[]; linkedPrs?: { url?: string }[] };
	return [...prs, ...linkedPrs].some((pr) => pr.url === url);
}

import { apiErrorException } from "../lib/api-error-exception";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { clientForSessionHost } from "../lib/host-clients";
import { usesPreviewWorkspaceData as usePreviewData } from "../lib/preview-mode";
import { sessionScmSummaryQueryKey, type SessionPRSummary } from "./useSessionScmSummary";

export const prDetailsLoadQueryKey = (url: string, hostId?: string) => ["pr-details-load", hostId ?? "local", url] as const;

export function usePRDetailsLoad(pr: SessionPRSummary | undefined, sessionId: string, hostId?: string, enabled = true): boolean {
	const queryClient = useQueryClient();
	const query = useQuery({
		queryKey: prDetailsLoadQueryKey(pr?.url ?? "", hostId),
		enabled: enabled && !usePreviewData && Boolean(pr?.url) && !pr?.detailsObservedAt,
		staleTime: Infinity,
		retry: false,
		queryFn: async () => {
			const { error } = await clientForSessionHost(hostId).POST("/api/v1/prs/{id}/details", {
				params: { path: { id: String(pr!.number) } },
				body: { prUrl: pr!.url },
			});
			if (error) throw apiErrorException(error);
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

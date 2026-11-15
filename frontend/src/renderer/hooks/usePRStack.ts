import { apiErrorException } from "../lib/api-error-exception";
import { useQuery } from "@tanstack/react-query";
import { clientForSessionHost } from "../lib/host-clients";
import { usesPreviewWorkspaceData as usePreviewData } from "../lib/preview-mode";
import type { PRStackInfo } from "../lib/pr-stack";
import type { SessionPRSummary } from "./useSessionScmSummary";

export const prStackQueryKey = (url: string, hostId?: string) => ["pr-stack", hostId ?? "local", url] as const;

export function usePRStack(pr: SessionPRSummary, hostId?: string, enabled = true) {
	return useQuery({
		queryKey: prStackQueryKey(pr.url, hostId),
		enabled: enabled && !usePreviewData && Boolean(pr.url) && pr.provider === "github",
		staleTime: 60_000,
		refetchInterval: 60_000,
		retry: false,
		queryFn: async (): Promise<PRStackInfo> => {
			const { data, error } = await clientForSessionHost(hostId).GET("/api/v1/prs/{id}/stack", {
				params: { path: { id: String(pr.number) }, query: { prUrl: pr.url } },
			});
			if (error) throw apiErrorException(error);
			return data ?? {};
		},
	});
}

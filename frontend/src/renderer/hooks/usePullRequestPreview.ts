import { useQuery } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient } from "../lib/api-client";
import { apiErrorException } from "../lib/api-error-exception";
import { queryClient } from "../lib/query-client";

export type PullRequestPreview = components["schemas"]["PRPreviewResponse"];

// The daemon keeps a read for two minutes; a chat open longer rereads on hover.
const PR_PREVIEW_STALE_MS = 2 * 60_000;
const PR_PREVIEW_CACHE_MS = 30 * 60_000;

export function pullRequestPreviewQueryKey(url: string) {
	return ["pr-preview", url] as const;
}

function pullRequestPreviewQueryOptions(url: string) {
	return {
		queryKey: pullRequestPreviewQueryKey(url),
		queryFn: async ({ signal }: { signal: AbortSignal }): Promise<PullRequestPreview> => {
			const { data, error } = await apiClient.GET("/api/v1/pr-preview", { params: { query: { url } }, signal });
			if (error) throw apiErrorException(error, "Could not read the pull request");
			return data;
		},
		staleTime: PR_PREVIEW_STALE_MS,
		gcTime: PR_PREVIEW_CACHE_MS,
		retry: false,
	};
}

/** Reads a pull request's state into the cache its chip and card read. Never rejects. */
export async function preloadPullRequestPreview(url: string): Promise<void> {
	await queryClient.prefetchQuery(pullRequestPreviewQueryOptions(url));
}

/**
 * A GitHub pull request's state and summary, read through the daemon. The
 * chip only watches the cache, which the on-screen preload fills; `fetch`
 * (an open hover card) reads it when it is missing or stale. Uses the shared
 * queryClient directly, so it works outside a QueryClientProvider.
 */
export function usePullRequestPreview(url: string, fetch: boolean) {
	return useQuery({ ...pullRequestPreviewQueryOptions(url), enabled: fetch }, queryClient);
}

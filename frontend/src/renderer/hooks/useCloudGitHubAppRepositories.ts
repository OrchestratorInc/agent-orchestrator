import { useQuery } from "@tanstack/react-query";
import type { CloudCpGitHubAppRepository } from "../lib/cloud-cp";
import { useCloudCp } from "./useCloudCp";

export const cloudGitHubAppReposQueryKey = (baseUrl: string, orgId: string | undefined) => ["cloud-github-app-repos", baseUrl, orgId] as const;

/**
 * Every repository the org's GitHub App installations grant AO, across all
 * pages, without revoked or archived ones. Cloud project setup picks a
 * project's repositories from this list.
 */
export function useCloudGitHubAppRepositories(orgId: string | undefined, enabled = true) {
	const { client, baseUrl } = useCloudCp();
	return useQuery({
		queryKey: cloudGitHubAppReposQueryKey(baseUrl, orgId),
		enabled: enabled && orgId !== undefined,
		staleTime: 30_000,
		queryFn: async ({ signal }) => {
			if (!orgId) return [];
			const repositories: CloudCpGitHubAppRepository[] = [];
			let cursor: string | undefined;
			for (;;) {
				const response = await client.listGitHubRepositories(orgId, { limit: 100, cursor }, { signal });
				repositories.push(...response.items.filter((repo) => repo.revokedAt === undefined && !repo.isArchived));
				if (!response.page.hasMore) return repositories;
				if (!response.page.nextCursor || response.page.nextCursor === cursor) throw new Error("GitHub repository pagination stopped unexpectedly");
				cursor = response.page.nextCursor;
			}
		},
	});
}

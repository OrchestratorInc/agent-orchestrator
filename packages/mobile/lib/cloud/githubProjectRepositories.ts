import type { CloudClient, GitHubRepository } from "@aoagents/cloud-client";

/** Fetch the complete grant set before allowing a repository to be selected. */
export async function loadGrantedRepositories(
	client: Pick<CloudClient, "listGitHubRepositories">,
	orgId: string,
	signal: AbortSignal,
): Promise<GitHubRepository[]> {
	const repositories = new Map<string, GitHubRepository>();
	let cursor: string | undefined;
	for (;;) {
		const response = await client.listGitHubRepositories(orgId, { cursor, limit: 100, signal });
		for (const repository of response.items) {
			if (repository.access === "active" && !repository.isArchived && !repository.revokedAt) {
				repositories.set(repository.githubRepositoryId, repository);
			}
		}
		if (!response.page.hasMore) return [...repositories.values()].sort((a, b) => a.fullName.localeCompare(b.fullName));
		if (!response.page.nextCursor || response.page.nextCursor === cursor) {
			throw new Error("GitHub repository pagination stopped unexpectedly.");
		}
		cursor = response.page.nextCursor;
	}
}

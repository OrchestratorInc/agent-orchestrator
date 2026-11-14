import { useEffect, useMemo, useRef, useState } from "react";
import { collectPages } from "../cloud/client";
import { useCloud } from "../cloud/CloudProvider";
import { hasActiveInstallation, matchRepositories, reachableRepositories } from "../cloud/newProject";
import type { CloudGitHubRepository } from "../cloud/types";

/** The URL a local clone of a GitHub repository uses: HTTPS, with the machine's own Git credentials. */
export function cloneUrlFor(repo: Pick<CloudGitHubRepository, "htmlUrl" | "fullName">): string {
	const base = repo.htmlUrl?.replace(/\/+$/, "") || `https://github.com/${repo.fullName}`;
	return base.endsWith(".git") ? base : `${base}.git`;
}

/**
 * The repositories the Orchestrator GitHub App reaches, searched to fill a
 * local clone's URL. Only when signed in to Orchestrator Cloud with the app
 * installed; otherwise the URL is typed. Read once, when `enabled` first
 * holds, and never asks GitHub for anything: installing the app belongs to
 * the cloud project's sheet.
 */
export function useGitHubRepositories(enabled: boolean) {
	const { status, client, org } = useCloud();
	const orgId = status === "ready" ? org?.id : undefined;
	const [result, setResult] = useState<{ orgId: string; items: CloudGitHubRepository[] } | null>(null);
	// Another organization's repositories are never shown, not even while its own load.
	const repos = result && result.orgId === orgId ? result.items : null;
	const [loading, setLoading] = useState(false);
	const [query, setQuery] = useState("");
	const readFor = useRef<string | null>(null);

	useEffect(() => {
		if (!enabled || !orgId || readFor.current === orgId) return;
		readFor.current = orgId;
		let cancelled = false;
		setLoading(true);
		void (async () => {
			try {
				const { installations } = await client.listGitHubInstallations(orgId);
				const items = hasActiveInstallation(installations)
					? reachableRepositories(await collectPages((cursor) => client.listGitHubRepositories(orgId, { cursor })))
					: [];
				if (!cancelled) setResult({ orgId, items });
			} catch {
				// No GitHub App on this deployment, or Cloud is unreachable: the URL is typed.
				if (!cancelled) setResult({ orgId, items: [] });
			} finally {
				if (!cancelled) setLoading(false);
			}
		})();
		return () => {
			cancelled = true;
			if (readFor.current === orgId) readFor.current = null;
		};
	}, [client, enabled, orgId]);

	const matches = useMemo(() => matchRepositories(repos, query, 8), [query, repos]);
	return {
		/** Something to search: signed in, the app installed, and a repository it reaches. */
		available: Boolean(orgId) && (repos?.length ?? 0) > 0,
		loading,
		count: repos?.length ?? 0,
		query,
		setQuery,
		searching: query.trim().length > 0,
		matches,
		/** The repository's clone URL, and the search cleared. */
		take(repo: CloudGitHubRepository): string {
			setQuery("");
			return cloneUrlFor(repo);
		},
	};
}

export type GitHubRepositories = ReturnType<typeof useGitHubRepositories>;

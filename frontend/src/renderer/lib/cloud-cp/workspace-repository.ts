// A control-plane client whose session workspace calls all target one of a
// multi-repository session's additional repositories (the `repository`
// parameter). Views take a client, so giving them a scoped one switches every
// tree, review, diff, file and revision call to that repository at once; the
// query-option helpers read workspaceRepositoryOf to keep each repository's
// cache entries apart.

import type { CloudCpClient } from "./client";

const scopedRepository = new WeakMap<CloudCpClient, string>();
const scopedClients = new WeakMap<CloudCpClient, Map<string, CloudCpClient>>();

/** The additional repository a scoped client targets; undefined for the main repository. */
export function workspaceRepositoryOf(client: CloudCpClient): string | undefined {
	return scopedRepository.get(client);
}

/** `client` scoped to `repository`; the same client for the main repository (empty or undefined). */
export function withWorkspaceRepository(client: CloudCpClient, repository: string | undefined): CloudCpClient {
	const name = repository?.trim() ?? "";
	if (name === "") return client;
	let byName = scopedClients.get(client);
	if (!byName) {
		byName = new Map();
		scopedClients.set(client, byName);
	}
	const cached = byName.get(name);
	if (cached) return cached;
	const scoped: CloudCpClient = {
		...client,
		getWorkspaceReview: (orgId, sessionId, options) => client.getWorkspaceReview(orgId, sessionId, { ...options, repository: name }),
		getWorkspaceReviewTree: (orgId, sessionId, path, options) => client.getWorkspaceReviewTree(orgId, sessionId, path, { ...options, repository: name }),
		searchWorkspaceReview: (orgId, sessionId, query, options) => client.searchWorkspaceReview(orgId, sessionId, query, { ...options, repository: name }),
		getWorkspaceReviewFile: (orgId, sessionId, query, options) => client.getWorkspaceReviewFile(orgId, sessionId, query, { ...options, repository: name }),
		getWorkspaceReviewDiffs: (orgId, sessionId, body, options) => client.getWorkspaceReviewDiffs(orgId, sessionId, body, { ...options, repository: name }),
		getWorkspaceReviewRevision: (orgId, sessionId, query, options) => client.getWorkspaceReviewRevision(orgId, sessionId, query, { ...options, repository: name }),
		updateWorkspaceReviewFile: (orgId, sessionId, body, options) => client.updateWorkspaceReviewFile(orgId, sessionId, body, { ...options, repository: name }),
	};
	scopedRepository.set(scoped, name);
	byName.set(name, scoped);
	return scoped;
}

/**
 * A file of a sibling repository is addressed as `../<repository>/<path>`,
 * where it really sits relative to the main checkout, so open file tabs of
 * different repositories never collide.
 */
export function joinWorkspaceRepositoryPath(repository: string | undefined, path: string): string {
	return repository ? `../${repository}/${path}` : path;
}

/** Splits a `../<repository>/<path>` file path; any other path is the main repository's. */
export function splitWorkspaceRepositoryPath(path: string): { repository?: string; path: string } {
	const match = /^\.\.\/([^/]+)\/(.+)$/.exec(path);
	return match ? { repository: match[1], path: match[2] } : { path };
}

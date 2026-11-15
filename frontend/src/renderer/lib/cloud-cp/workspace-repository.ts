import type { CloudCpClient } from "./client";

const scopedRepository = new WeakMap<CloudCpClient, string>();
const scopedClients = new WeakMap<CloudCpClient, Map<string, CloudCpClient>>();

export function workspaceRepositoryOf(client: CloudCpClient): string | undefined {
	return scopedRepository.get(client);
}

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

export function joinWorkspaceRepositoryPath(repository: string | undefined, path: string): string {
	return repository ? `../${repository}/${path}` : path;
}

export function splitWorkspaceRepositoryPath(path: string): { repository?: string; path: string } {
	const match = /^\.\.\/([^/]+)\/(.+)$/.exec(path);
	return match ? { repository: match[1], path: match[2] } : { path };
}

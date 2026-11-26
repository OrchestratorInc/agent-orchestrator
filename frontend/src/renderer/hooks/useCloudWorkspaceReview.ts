import { type QueryClient, useQueryClient } from "@tanstack/react-query";
import type {
	CloudCpClient,
	CloudCpClientEvent,
	CloudCpWorkspaceReviewDiffsRequest,
	CloudCpWorkspaceReviewFileQuery,
	CloudCpWorkspaceReviewRevisionQuery,
	CloudCpWorkspaceReviewScope,
	CloudCpWorkspaceReviewSearchQuery,
} from "../lib/cloud-cp";
import { CloudCpError, workspaceRepositoryOf } from "../lib/cloud-cp";
import { cloudWorkerReadRetry, cloudWorkerRequest, recordCloudWorkspaceStartupEvent } from "../lib/cloud-worker-starting";
import { useCloudSessionEventListener } from "./useCloudSessionEvents";

export const cloudWorkspaceReviewQueryKey = (baseUrl: string, orgId: string, sessionId: string) =>
	["cloud-workspace-review", baseUrl, orgId, sessionId] as const;

/**
 * The cache key prefix of one repository of a session: the session's own key
 * for the main repository, with the sibling's name added for a scoped client
 * (withWorkspaceRepository), so invalidating the session covers every repository.
 */
function repositoryKey(client: CloudCpClient, baseUrl: string, orgId: string, sessionId: string) {
	const repository = workspaceRepositoryOf(client);
	const key = cloudWorkspaceReviewQueryKey(baseUrl, orgId, sessionId);
	return repository ? [...key, `repository:${repository}`] as const : key;
}

export function cloudWorkspaceReviewSummaryQueryOptions(args: {
	client: CloudCpClient;
	baseUrl: string;
	orgId: string;
	sessionId: string;
	enabled?: boolean;
	visible?: boolean;
}) {
	const { client, baseUrl, orgId, sessionId } = args;
	return {
		queryKey: [...repositoryKey(client, baseUrl, orgId, sessionId), "summary"] as const,
		// The summary is the session's first workspace read, so its answer tells
		// every other read whether the worker is still preparing the workspace.
		queryFn: () => cloudWorkerRequest(orgId, sessionId, () => client.getWorkspaceReview(orgId, sessionId)),
		enabled: args.enabled ?? true,
		refetchInterval: args.visible === false ? false as const : 5_000,
		// A worker still preparing its workspace is asked again after its
		// Retry-After (or a growing wait) while the panel keeps loading, not
		// every poll, for as long as the session's starting budget lasts
		// (cloud-worker-starting).
		...cloudWorkerReadRetry(orgId, sessionId, cloudWorkspaceReviewRetry),
	};
}

export function cloudWorkspaceReviewTreeQueryOptions(args: {
	client: CloudCpClient;
	baseUrl: string;
	orgId: string;
	sessionId: string;
	path?: string;
	/** False while the session's workspace cannot answer (useCloudWorkspaceReady): what was read before stays. */
	enabled?: boolean;
}) {
	return {
		queryKey: [...repositoryKey(args.client, args.baseUrl, args.orgId, args.sessionId), "tree", args.path ?? ""] as const,
		queryFn: () => cloudWorkerRequest(args.orgId, args.sessionId, () => args.client.getWorkspaceReviewTree(args.orgId, args.sessionId, args.path)),
		enabled: args.enabled ?? true,
		...cloudWorkerReadRetry(args.orgId, args.sessionId, cloudWorkspaceReviewRetry),
	};
}

export function cloudWorkspaceReviewSearchQueryOptions(args: {
	client: CloudCpClient;
	baseUrl: string;
	orgId: string;
	sessionId: string;
	query: CloudCpWorkspaceReviewSearchQuery;
	/** False while the session's workspace cannot answer (useCloudWorkspaceReady): what was read before stays. */
	enabled?: boolean;
}) {
	return {
		queryKey: [...repositoryKey(args.client, args.baseUrl, args.orgId, args.sessionId), "search", args.query.query, args.query.cursor ?? "", args.query.limit ?? 50] as const,
		queryFn: ({ signal }: { signal?: AbortSignal } = {}) =>
			cloudWorkerRequest(args.orgId, args.sessionId, () => args.client.searchWorkspaceReview(args.orgId, args.sessionId, args.query, { signal })),
		enabled: args.enabled ?? true,
		...cloudWorkerReadRetry(args.orgId, args.sessionId, cloudWorkspaceReviewRetry),
	};
}

export function cloudWorkspaceReviewFileQueryOptions(args: {
	client: CloudCpClient;
	baseUrl: string;
	orgId: string;
	sessionId: string;
	path: string;
	scope?: CloudCpWorkspaceReviewScope;
	commitSha?: string;
	/** False while the session's workspace cannot answer (useCloudWorkspaceReady): what was read before stays. */
	enabled?: boolean;
}) {
	const query: CloudCpWorkspaceReviewFileQuery = { path: args.path, scope: args.scope, commitSha: args.commitSha };
	return {
		queryKey: [...repositoryKey(args.client, args.baseUrl, args.orgId, args.sessionId), "file", args.scope ?? "combined", args.commitSha ?? "", args.path] as const,
		queryFn: () => cloudWorkerRequest(args.orgId, args.sessionId, () => args.client.getWorkspaceReviewFile(args.orgId, args.sessionId, query)),
		enabled: args.enabled ?? true,
		...cloudWorkerReadRetry(args.orgId, args.sessionId, cloudWorkspaceReviewRetry),
	};
}

export function cloudWorkspaceReviewDiffsQueryOptions(args: {
	client: CloudCpClient;
	baseUrl: string;
	orgId: string;
	sessionId: string;
	paths: readonly string[];
	scope: CloudCpWorkspaceReviewScope;
	contextLines?: number;
	ignoreWhitespace?: boolean;
	workspaceVersion?: string;
	commitSha?: string;
	/** False while the session's workspace cannot answer (useCloudWorkspaceReady): what was read before stays. */
	enabled?: boolean;
}) {
	const body: CloudCpWorkspaceReviewDiffsRequest = {
		scope: args.scope,
		paths: [...args.paths],
		contextLines: args.contextLines ?? 3,
		ignoreWhitespace: args.ignoreWhitespace ?? false,
		workspaceVersion: args.workspaceVersion,
		commitSha: args.commitSha,
	};
	return {
		queryKey: [
			...repositoryKey(args.client, args.baseUrl, args.orgId, args.sessionId), "diffs", args.scope,
			args.commitSha ?? "", [...args.paths], body.contextLines, body.ignoreWhitespace, args.workspaceVersion ?? "",
		] as const,
		queryFn: () => cloudWorkerRequest(args.orgId, args.sessionId, () => args.client.getWorkspaceReviewDiffs(args.orgId, args.sessionId, body)),
		enabled: args.enabled ?? true,
		...cloudWorkerReadRetry(args.orgId, args.sessionId, cloudWorkspaceReviewRetry),
	};
}

export function cloudWorkspaceReviewRevisionQueryOptions(args: {
	client: CloudCpClient;
	baseUrl: string;
	orgId: string;
	sessionId: string;
	query: CloudCpWorkspaceReviewRevisionQuery;
	/** False while the session's workspace cannot answer (useCloudWorkspaceReady): what was read before stays. */
	enabled?: boolean;
}) {
	return {
		queryKey: [
			...repositoryKey(args.client, args.baseUrl, args.orgId, args.sessionId), "revision", args.query.scope ?? "combined",
			args.query.commitSha ?? "", args.query.side ?? "after", args.query.path, args.query.workspaceVersion ?? "", args.query.expectedRevision ?? "",
		] as const,
		queryFn: () => cloudWorkerRequest(args.orgId, args.sessionId, () => args.client.getWorkspaceReviewRevision(args.orgId, args.sessionId, args.query)),
		enabled: args.enabled ?? true,
		...cloudWorkerReadRetry(args.orgId, args.sessionId, cloudWorkspaceReviewRetry),
	};
}

export function cloudWorkspaceReviewRetry(failureCount: number, error: Error): boolean {
	if (error instanceof CloudCpError && error.status === 409) return false;
	return failureCount < 2;
}

export async function invalidateCloudWorkspaceReviewEvent(
	queryClient: QueryClient,
	queryKey: ReturnType<typeof cloudWorkspaceReviewQueryKey>,
	event: CloudCpClientEvent,
): Promise<void> {
	if (event.type !== "workspace.changed") return;
	await queryClient.invalidateQueries({ queryKey });
}

export function useCloudWorkspaceReviewEvents(args: {
	baseUrl: string;
	orgId?: string;
	sessionId: string;
	enabled: boolean;
}): void {
	const queryClient = useQueryClient();
	useCloudSessionEventListener({
		baseUrl: args.baseUrl,
		orgId: args.orgId,
		sessionId: args.sessionId,
		enabled: args.enabled,
		onEvent: (event) => {
			if (!args.orgId) return;
			recordCloudWorkspaceStartupEvent(args.orgId, args.sessionId, event);
			void invalidateCloudWorkspaceReviewEvent(queryClient, cloudWorkspaceReviewQueryKey(args.baseUrl, args.orgId, args.sessionId), event);
		},
	});
}

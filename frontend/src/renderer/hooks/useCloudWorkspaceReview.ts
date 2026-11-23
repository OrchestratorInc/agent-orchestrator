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
import { cloudWorkerRequest, cloudWorkerStartingDelay, cloudWorkerStartingRemaining, isCloudWorkerStarting, recordCloudWorkspaceStartupEvent } from "../lib/cloud-worker-starting";
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
		// A worker still preparing its workspace is asked again after a growing
		// wait (or its Retry-After) while the panel keeps loading, not every poll.
		retry: (failureCount: number, error: Error) =>
			isCloudWorkerStarting(error) ? failureCount < STARTING_RETRIES : cloudWorkspaceReviewRetry(failureCount, error),
		retryDelay: (failureCount: number, error: Error) =>
			isCloudWorkerStarting(error)
				? cloudWorkerStartingRemaining(orgId, sessionId) || cloudWorkerStartingDelay(failureCount + 1, error)
				: Math.min(1_000 * 2 ** failureCount, 30_000),
	};
}

/** How many "still starting" answers a summary waits through (about half an hour at the longest wait) before it shows the error. */
const STARTING_RETRIES = 30;

export function cloudWorkspaceReviewTreeQueryOptions(args: {
	client: CloudCpClient;
	baseUrl: string;
	orgId: string;
	sessionId: string;
	path?: string;
}) {
	return {
		queryKey: [...repositoryKey(args.client, args.baseUrl, args.orgId, args.sessionId), "tree", args.path ?? ""] as const,
		queryFn: () => args.client.getWorkspaceReviewTree(args.orgId, args.sessionId, args.path),
		retry: cloudWorkspaceReviewRetry,
	};
}

export function cloudWorkspaceReviewSearchQueryOptions(args: {
	client: CloudCpClient;
	baseUrl: string;
	orgId: string;
	sessionId: string;
	query: CloudCpWorkspaceReviewSearchQuery;
}) {
	return {
		queryKey: [...repositoryKey(args.client, args.baseUrl, args.orgId, args.sessionId), "search", args.query.query, args.query.cursor ?? "", args.query.limit ?? 50] as const,
		queryFn: ({ signal }: { signal?: AbortSignal } = {}) =>
			args.client.searchWorkspaceReview(args.orgId, args.sessionId, args.query, { signal }),
		retry: cloudWorkspaceReviewRetry,
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
}) {
	const query: CloudCpWorkspaceReviewFileQuery = { path: args.path, scope: args.scope, commitSha: args.commitSha };
	return {
		queryKey: [...repositoryKey(args.client, args.baseUrl, args.orgId, args.sessionId), "file", args.scope ?? "combined", args.commitSha ?? "", args.path] as const,
		queryFn: () => args.client.getWorkspaceReviewFile(args.orgId, args.sessionId, query),
		retry: cloudWorkspaceReviewRetry,
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
		queryFn: () => args.client.getWorkspaceReviewDiffs(args.orgId, args.sessionId, body),
		retry: cloudWorkspaceReviewRetry,
	};
}

export function cloudWorkspaceReviewRevisionQueryOptions(args: {
	client: CloudCpClient;
	baseUrl: string;
	orgId: string;
	sessionId: string;
	query: CloudCpWorkspaceReviewRevisionQuery;
}) {
	return {
		queryKey: [
			...repositoryKey(args.client, args.baseUrl, args.orgId, args.sessionId), "revision", args.query.scope ?? "combined",
			args.query.commitSha ?? "", args.query.side ?? "after", args.query.path, args.query.workspaceVersion ?? "", args.query.expectedRevision ?? "",
		] as const,
		queryFn: () => args.client.getWorkspaceReviewRevision(args.orgId, args.sessionId, args.query),
		retry: cloudWorkspaceReviewRetry,
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

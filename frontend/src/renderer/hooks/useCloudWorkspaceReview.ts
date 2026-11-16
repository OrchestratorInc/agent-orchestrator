import { type QueryClient, useQueryClient } from "@tanstack/react-query";
import type {
	CloudCpClient,
	CloudCpClientEvent,
	CloudCpFileReferenceResolveRequest,
	CloudCpWorkspaceCommitSelectionResponse,
	CloudCpWorkspaceHistoryResponse,
	CloudCpWorkspaceReviewDiffsRequest,
	CloudCpWorkspaceReviewFileQuery,
	CloudCpWorkspaceReviewResponse,
	CloudCpWorkspaceReviewRevisionQuery,
	CloudCpWorkspaceReviewScope,
	CloudCpWorkspaceReviewSearchQuery,
	CloudCpWorkspaceReviewView,
} from "../lib/cloud-cp";
import { CloudCpError, joinWorkspaceRepositoryPath, splitWorkspaceRepositoryPath, workspaceRepositoryOf } from "../lib/cloud-cp";
import { FILE_REFERENCE_RESOLVE_UNSUPPORTED, type ChatFileReference, type FileAnnotationResolution } from "../../shared/chat-file-reference";
import { workspaceSelectionKey } from "../lib/files-review-selection";
import { useCloudSessionEventListener } from "./useCloudSessionEvents";
import type { FilesReviewDataSource } from "./useFilesReviewSelection";
import {
	WorkspaceRequestError,
	type FilesBranchTarget,
	type WorkspaceBranches,
	type WorkspaceCommitSelection,
	type WorkspaceFilesResponse,
	type WorkspaceHistoryResponse,
	type WorkspaceViewParams,
} from "./useSessionWorkspaceFiles";

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

/** A view's part of a cache key, with its fence and picked SHAs; empty without a view. */
function viewKey(view: CloudCpWorkspaceReviewView | undefined): readonly string[] {
	return view && (view.branch || view.prNumber) ? ["view", workspaceSelectionKey(view, view.commitShas ?? [])] : [];
}

/** The view fields of a review request; none without a view. */
function viewFields(view: CloudCpWorkspaceReviewView | undefined): CloudCpWorkspaceReviewView {
	if (!view || !(view.branch || view.prNumber)) return {};
	return {
		branch: view.branch, prNumber: view.prNumber, sourceUrl: view.sourceUrl,
		expectedTipSha: view.expectedTipSha, expectedBaseSha: view.expectedBaseSha, commitShas: view.commitShas ? [...view.commitShas] : undefined,
	};
}

/**
 * A review view as the Cloud routes take it. The branch menu names a remote
 * branch `refs/remotes/origin/<name>`, as the daemon reads it; the Cloud routes
 * take `origin/<name>`.
 */
export function cloudWorkspaceView(view: WorkspaceViewParams | undefined, commitShas?: readonly string[]): CloudCpWorkspaceReviewView | undefined {
	if (!view || !(view.branch || view.prNumber)) return undefined;
	return {
		...(view.branch ? { branch: view.branch.replace(/^refs\/remotes\//, "") } : {}),
		...(view.prNumber ? { prNumber: view.prNumber, sourceUrl: view.sourceUrl } : {}),
		expectedTipSha: view.expectedTipSha,
		expectedBaseSha: view.expectedBaseSha,
		...(commitShas ? { commitShas: [...commitShas] } : {}),
	};
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
		queryFn: () => client.getWorkspaceReview(orgId, sessionId),
		enabled: args.enabled ?? true,
		refetchInterval: args.visible === false ? false as const : 5_000,
		retry: cloudWorkspaceReviewRetry,
	};
}

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
	/** Reads the file from another branch or a PR (and its picked commits) instead of the worktree. */
	view?: CloudCpWorkspaceReviewView;
}) {
	const query: CloudCpWorkspaceReviewFileQuery = { path: args.path, scope: args.scope, commitSha: args.commitSha, ...viewFields(args.view) };
	return {
		queryKey: [...repositoryKey(args.client, args.baseUrl, args.orgId, args.sessionId), "file", args.scope ?? "combined", args.commitSha ?? "", args.path, ...viewKey(args.view)] as const,
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
	/** Diffs another branch or a PR (and its picked commits) instead of the worktree. */
	view?: CloudCpWorkspaceReviewView;
}) {
	const body: CloudCpWorkspaceReviewDiffsRequest = {
		scope: args.scope,
		paths: [...args.paths],
		contextLines: args.contextLines ?? 3,
		ignoreWhitespace: args.ignoreWhitespace ?? false,
		workspaceVersion: args.workspaceVersion,
		commitSha: args.commitSha,
		...viewFields(args.view),
	};
	return {
		queryKey: [
			...repositoryKey(args.client, args.baseUrl, args.orgId, args.sessionId), "diffs", args.scope,
			args.commitSha ?? "", [...args.paths], body.contextLines, body.ignoreWhitespace, args.workspaceVersion ?? "", ...viewKey(args.view),
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
			...viewKey(args.query),
		] as const,
		queryFn: () => args.client.getWorkspaceReviewRevision(args.orgId, args.sessionId, args.query),
		retry: cloudWorkspaceReviewRetry,
	};
}

/** A worker that predates views (501), or a control plane without the route (404). */
export function isWorkspaceViewUnsupported(error: unknown): boolean {
	return error instanceof CloudCpError && (error.status === 501 || error.status === 404);
}

function workspaceRequestError(error: unknown, fallback: string): WorkspaceRequestError {
	if (error instanceof WorkspaceRequestError) return error;
	if (error instanceof CloudCpError) return new WorkspaceRequestError(error.message || fallback, error.code, error.status);
	return new WorkspaceRequestError(error instanceof Error && error.message ? error.message : fallback);
}

/** A view read answers the same again for any 4xx and for an unsupported worker, so only other failures retry. */
function cloudViewRetry(failureCount: number, error: Error): boolean {
	const status = error instanceof WorkspaceRequestError || error instanceof CloudCpError ? error.status : undefined;
	return failureCount < 2 && !(status !== undefined && ((status >= 400 && status < 500) || status === 501));
}

function cloudHistory(sessionId: string, data: CloudCpWorkspaceHistoryResponse): WorkspaceHistoryResponse {
	return {
		sessionId,
		// A Cloud file row's status is a superset of the daemon's; the Cloud review renders it as is.
		commits: data.commits as unknown as WorkspaceHistoryResponse["commits"],
		commitsTruncated: data.commitsTruncated,
		rangeSummary: data.rangeSummary,
		view: { kind: data.view.kind, name: data.view.name, tipSha: data.view.tipSha, baseSha: data.view.baseSha || undefined, baseRef: data.view.baseRef, rangeMode: data.view.rangeMode },
	};
}

function cloudCommitSelection(sessionId: string, data: CloudCpWorkspaceCommitSelectionResponse): WorkspaceCommitSelection {
	const conflict = typeof data.conflict === "string"
		? { commitSha: /^[0-9a-f]{7,64}$/i.test(data.conflict) ? data.conflict : "", paths: [], reason: "conflict" as const }
		: data.conflict;
	return {
		sessionId,
		tipSha: data.tipSha,
		baseSha: data.baseSha,
		contiguous: data.contiguous,
		combinable: data.combinable,
		// Picks that do not combine always name why, so the review explains it.
		conflict: conflict ?? (data.combinable ? undefined : { commitSha: "", paths: [], reason: "conflict" }),
		files: data.files as unknown as WorkspaceCommitSelection["files"],
		summary: data.summary,
		truncated: data.truncated,
	};
}

/**
 * A Cloud review summary as the Files review's checked-out manifest, which
 * the branch and changes selection reads. Uncommitted is the working scopes
 * together, as Cloud shows them.
 */
export function cloudFilesManifest(data: CloudCpWorkspaceReviewResponse, sessionId: string): WorkspaceFilesResponse {
	const seen = new Set<string>();
	const uncommitted = [...data.sections.unstaged, ...data.sections.staged, ...data.sections.untracked].filter((file) => !seen.has(file.path) && Boolean(seen.add(file.path)));
	// A Cloud file row's status is a superset of the daemon's; the Cloud review renders it as is.
	const rows = (files: readonly unknown[]) => files as WorkspaceFilesResponse["files"];
	return {
		sessionId,
		workspaceVersion: data.workspaceVersion,
		compareBaseRef: data.compareBaseRef,
		compareBaseSha: data.compareBaseSha,
		compareMode: data.compareMode,
		files: rows(data.files),
		commits: data.commits as unknown as WorkspaceFilesResponse["commits"],
		summary: data.summary,
		truncated: data.truncated,
		sections: { uncommitted: rows(uncommitted), committed: rows(data.sections.committed), staged: rows(data.sections.staged), unstaged: rows(data.sections.unstaged), untracked: rows(data.sections.untracked) },
	};
}

/** Reads another branch's or a PR's history again once its tip or base moved, as the branch list does. */
export function invalidateCloudWorkspaceViews(queryClient: QueryClient, client: CloudCpClient, baseUrl: string, orgId: string, sessionId: string): void {
	const key = repositoryKey(client, baseUrl, orgId, sessionId);
	void queryClient.invalidateQueries({ queryKey: [...key, "branches"] });
	void queryClient.invalidateQueries({ queryKey: [...key, "history"] });
}

/**
 * The Files review's branches, histories and commit selections from a Cloud
 * session (one repository of it: a scoped client reads a sibling's). A worker
 * without views lists no branches, so the branch menu stays hidden.
 */
export function cloudFilesReviewDataSource({ client, baseUrl, orgId, sessionId }: { client: CloudCpClient; baseUrl: string; orgId: string; sessionId: string }): FilesReviewDataSource {
	const key = repositoryKey(client, baseUrl, orgId, sessionId);
	const history = (errorMessage: string, branch: FilesBranchTarget) => {
		const query = branch.kind === "branch" ? { branch: branch.name.replace(/^refs\/remotes\//, "") }
			: branch.kind === "pull_request" ? { prNumber: branch.number, sourceUrl: branch.url || undefined }
			: undefined;
		return {
			queryKey: branch.kind === "branch" ? [...key, "history", "branch", branch.name, branch.tipSha ?? ""]
				: branch.kind === "pull_request" ? [...key, "history", "pr", branch.url || `#${branch.number}`, branch.tipSha ?? ""]
				: [...key, "history", "checked_out"],
			queryFn: async () => {
				// The checked-out branch's commits come with the review summary.
				if (!query) throw new WorkspaceRequestError(errorMessage);
				try {
					return cloudHistory(sessionId, await client.getWorkspaceHistory(orgId, sessionId, query));
				} catch (error) {
					throw workspaceRequestError(error, errorMessage);
				}
			},
			staleTime: Infinity,
			retry: cloudViewRetry,
		};
	};
	return {
		checkedOutHistory: false,
		branches: (errorMessage) => ({
			queryKey: [...key, "branches"],
			queryFn: async (): Promise<WorkspaceBranches> => {
				try {
					const data = await client.getWorkspaceBranches(orgId, sessionId);
					return { sessionId, supported: data.supported, truncated: data.truncated, defaultBranch: data.defaultBranch, branches: data.branches };
				} catch (error) {
					if (isWorkspaceViewUnsupported(error)) return { sessionId, supported: false, truncated: false, branches: [] };
					throw workspaceRequestError(error, errorMessage);
				}
			},
			staleTime: 15_000,
			retry: cloudViewRetry,
		}),
		history,
		commitSelection: ({ commitShas, errorMessage, selectionKey, view, workspaceVersion }) => ({
			queryKey: [...key, "history-selection", selectionKey, workspaceVersion ?? ""],
			queryFn: async () => {
				try {
					const body = { ...cloudWorkspaceView(view), commitShas: [...commitShas], workspaceVersion };
					return cloudCommitSelection(sessionId, await client.getWorkspaceCommitSelection(orgId, sessionId, body));
				} catch (error) {
					throw workspaceRequestError(error, errorMessage);
				}
			},
			staleTime: Infinity,
			retry: cloudViewRetry,
		}),
		invalidateViews: (queryClient) => invalidateCloudWorkspaceViews(queryClient, client, baseUrl, orgId, sessionId),
	};
}

/** The cache key prefix of a Cloud session's chat file reference resolutions. */
export const cloudFileReferenceQueryKey = (baseUrl: string, orgId: string, sessionId: string) =>
	["file-annotation-source", "cloud", baseUrl, orgId, sessionId] as const;

/**
 * Resolves a sent chat file reference through the control plane. A sibling
 * repository's `../<repository>/<path>` names that repository. The first 501
 * (or 404) marks the route unsupported for `unsupported`, so later chips do
 * not ask again.
 */
export function resolveCloudFileReference(client: CloudCpClient, orgId: string, unsupported: { current: boolean }) {
	return async (reference: ChatFileReference, signal?: AbortSignal): Promise<FileAnnotationResolution> => {
		if (unsupported.current) throw new Error(FILE_REFERENCE_RESOLVE_UNSUPPORTED);
		const target = splitWorkspaceRepositoryPath(reference.path);
		const previous = reference.previousPath ? splitWorkspaceRepositoryPath(reference.previousPath) : undefined;
		const { hostId: _hostId, ...portable } = reference;
		const body: CloudCpFileReferenceResolveRequest = {
			...portable,
			path: target.path,
			...(previous ? { previousPath: previous.path } : {}),
			...(target.repository ? { repository: target.repository } : {}),
		};
		try {
			const resolution = await client.resolveFileReference(orgId, reference.sessionId, body, { signal });
			// The answer names the path inside the repository; the views address it as the reference did.
			return target.repository && resolution.path ? { ...resolution, path: joinWorkspaceRepositoryPath(target.repository, resolution.path) } : resolution;
		} catch (error) {
			if (isWorkspaceViewUnsupported(error)) {
				unsupported.current = true;
				throw new Error(FILE_REFERENCE_RESOLVE_UNSUPPORTED);
			}
			if (error instanceof CloudCpError && error.status === 400) throw new Error("CHAT_FILE_REFERENCE_INVALID");
			throw new Error(error instanceof CloudCpError && error.code ? error.code : "FILE_REFERENCE_CHECK_FAILED");
		}
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

/** A changed workspace may move the lines a sent chat reference names. */
export function useCloudFileReferenceEvents(args: { baseUrl: string; orgId?: string; sessionId: string; enabled: boolean }): void {
	const queryClient = useQueryClient();
	useCloudSessionEventListener({
		baseUrl: args.baseUrl,
		orgId: args.orgId,
		sessionId: args.sessionId,
		enabled: args.enabled,
		onEvent: (event) => {
			if (!args.orgId || event.type !== "workspace.changed") return;
			void queryClient.invalidateQueries({ queryKey: cloudFileReferenceQueryKey(args.baseUrl, args.orgId, args.sessionId) }, { cancelRefetch: false });
		},
	});
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
			void invalidateCloudWorkspaceReviewEvent(queryClient, cloudWorkspaceReviewQueryKey(args.baseUrl, args.orgId, args.sessionId), event);
		},
	});
}

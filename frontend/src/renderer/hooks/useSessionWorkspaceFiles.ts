import { useQuery, useQueryClient } from "@tanstack/react-query";
import type { QueryClient, UseQueryOptions } from "@tanstack/react-query";
import { useCallback, useEffect, useSyncExternalStore } from "react";
import type { components } from "../../api/schema";
import { apiErrorCode, apiErrorMessage } from "../lib/api-client";
import { CHECKED_OUT_BRANCH, buildReviewTarget, defaultChanges, followsDefaultSelection, isLegacyManifest, workspaceSelectionKey } from "../lib/files-review-selection";
import { clientForSessionHost } from "../lib/host-clients";
import { sessionUiKey } from "../lib/hosts";
import { fileMediaKind } from "../lib/file-media";
import { artifactContentUrl } from "../lib/artifact-content-url";
import { WORKSPACE_REVIEW_BATCH_SIZE } from "../lib/workspace-review";
import { useUiStore } from "../stores/ui-store";
import {
	getWorkspaceFileConnectionState,
	subscribeWorkspaceFileChanges,
	subscribeWorkspaceFileConnectionState,
	type WorkspaceFileConnectionState,
} from "../lib/workspace-file-events";

export type WorkspaceCompareMode = "base" | "head_fallback";
export type WorkspaceFileSummary = Omit<components["schemas"]["WorkspaceFileSummary"], "editable" | "fileFingerprint"> & {
	editable?: boolean;
	previousPath?: string;
	fileFingerprint?: string;
};
export type WorkspaceFileSections = components["schemas"]["WorkspaceFileSections"];
export type WorkspaceCommitSummary = components["schemas"]["WorkspaceCommitSummary"];
export type WorkspaceViewInfo = components["schemas"]["WorkspaceViewResponse"];
export type WorkspaceHistoryResponse = Omit<components["schemas"]["WorkspaceHistoryResponse"], "view"> & {
	/** Absent from an older daemon. */
	view?: WorkspaceViewInfo;
};
export type WorkspaceSummary = components["schemas"]["WorkspaceSummary"];
export type WorkspaceBranches = components["schemas"]["WorkspaceBranchesResponse"];
export type WorkspaceBranch = components["schemas"]["WorkspaceBranchResponse"];
export type WorkspaceCommitConflict = components["schemas"]["WorkspaceCommitConflictResponse"];
export type WorkspaceCommitSelection = Omit<components["schemas"]["WorkspaceCommitSelectionResponse"], "files"> & {
	files: WorkspaceFileSummary[];
};
export type WorkspaceFilesResponse = Omit<components["schemas"]["ListWorkspaceFilesResponse"], "files" | "sections" | "workspaceVersion" | "degraded" | "degradedCode"> & {
	compareMode?: WorkspaceCompareMode;
	files: WorkspaceFileSummary[];
	sections: {
		/** Absent from an older daemon. */
		uncommitted?: WorkspaceFileSummary[];
		committed: WorkspaceFileSummary[];
		staged: WorkspaceFileSummary[];
		unstaged: WorkspaceFileSummary[];
		untracked: WorkspaceFileSummary[];
	};
	workspaceVersion?: string;
	degraded?: boolean;
	degradedCode?: string;
	stale?: boolean;
	refreshing?: boolean;
};
export type WorkspaceFileDetail = Omit<components["schemas"]["WorkspaceFileResponse"], "editable" | "fileFingerprint" | "workspaceVersion"> & {
	/** HTTP validator for streamed artifacts, so an unchanged refetch keeps playing. */
	mediaVersion?: string;
	editable?: boolean;
	previousPath?: string;
	compareMode?: WorkspaceCompareMode;
	fileFingerprint?: string;
	workspaceVersion?: string;
};
export type WorkspaceDiffScope = components["schemas"]["WorkspaceDiffRequest"]["scope"];
export type WorkspaceDiffsResponse = components["schemas"]["WorkspaceDiffsResponse"];
export type WorkspaceFileRevision = components["schemas"]["WorkspaceFileRevisionResponse"];
export type WorkspaceFileSearchResponse = components["schemas"]["WorkspaceFileSearchResponse"];
export type FilesSource =
	| { kind: "workspace" }
 | { kind: "snapshot"; view?: WorkspaceViewParams; commitShas?: readonly string[]; label: string }
	| { kind: "pull_request"; number: number; url: string; label: string; snapshot?: string }
	/** `readOnly` hides the edit action, as for a cloud session's artifacts. */
	| { kind: "artifact"; rawUrl?: string; readOnly?: boolean };

/** The branch the Files review reads. Only the checked-out branch reads the worktree; the others read revisions. */
export type FilesBranchTarget =
	| { kind: "checked_out" }
	/** `tipSha` is the tip the branch list showed when it was picked. */
	| { kind: "branch"; name: string; tipSha?: string }
	| { kind: "pull_request"; number: number; url: string; branch: string; tipSha?: string };
/**
 * The changes the Files review shows. `combined` is the "Changes" scope an
 * older daemon has instead of `uncommitted`. `commits` lists ticked SHAs newest
 * first; none is All commits.
 */
export type FilesChanges =
	| { kind: "uncommitted" | "staged" | "unstaged" | "combined" }
	| { kind: "commits"; shas: readonly string[] };
/** `changes: null` follows the default until the user picks. */
export type FilesSelection = { branch: FilesBranchTarget; changes: FilesChanges | null };
/** Selects another branch or a PR on a read; none reads the worktree. The expected SHAs fence it to what its history listed. */
export type WorkspaceViewParams = {
	branch?: string;
	prNumber?: number;
	sourceUrl?: string;
	expectedTipSha?: string;
	expectedBaseSha?: string;
};
/** What the review pane reads for the picked branch and changes. */
export type ReviewTarget = {
	/** Viewed marks are stored under it, and the pane resets when it changes. */
	key: string;
	branch: FilesBranchTarget;
	changes: FilesChanges;
	scope: WorkspaceDiffScope;
	/** Undefined reads the whole range (or a working scope). */
	commitShas?: readonly string[];
	/** Undefined reads the checked-out branch. */
	view?: WorkspaceViewParams;
	/** The manifest's version, which fences checked-out reads. */
	workspaceVersion?: string;
	/** "" for the worktree's scopes and its whole range; see workspaceSelectionKey. */
	selectionKey: string;
	/** Undefined until known. */
	files?: WorkspaceFileSummary[];
	/** Set when exactly one commit is picked. */
	sha?: string;
	subject?: string;
	/** Historical targets carry their source when opening a file. */
	openable: boolean;
	source?: FilesSource;
	state: "ready" | "pending" | "error" | "not_combinable";
	conflict?: WorkspaceCommitConflict & { subject?: string };
	error?: Error;
};

/** A failed workspace read that keeps the daemon's error code, for callers that act on it. */
export class WorkspaceRequestError extends Error {
	constructor(
		message: string,
		readonly code?: string,
		readonly status?: number,
	) {
		super(message);
		this.name = "WorkspaceRequestError";
	}
}

function workspaceRequestError(error: unknown, response: Response | undefined, fallback: string): WorkspaceRequestError {
	return new WorkspaceRequestError(apiErrorMessage(error, fallback), apiErrorCode(error), response?.status);
}

export function workspaceErrorCode(error: unknown): string | undefined {
	return error instanceof WorkspaceRequestError ? error.code : undefined;
}

/**
 * Retries a failed read up to three times, except a 4xx answer: it comes back
 * the same every time, and the recovery keyed on its code (a moved tip, a gone
 * branch) would wait out the backoff.
 */
export function retryWorkspaceRead(failureCount: number, error: Error): boolean {
	const status = error instanceof WorkspaceRequestError ? error.status : undefined;
	return failureCount < 3 && !(status !== undefined && status >= 400 && status < 500);
}

export const sessionWorkspaceFilesQueryKey = (sessionId: string, hostId?: string) =>
	hostId ? ["session-workspace-files", hostId, sessionId] as const : ["session-workspace-files", sessionId] as const;
const WORKSPACE_FILES_DEGRADED_REFETCH_MS = 30_000;
const MAX_ARTIFACT_TEXT_BYTES = 256 * 1024;

async function fetchSessionWorkspaceFiles(sessionId: string, errorMessage: string, hostId?: string): Promise<WorkspaceFilesResponse> {
	const { data, error } = await clientForSessionHost(hostId).GET("/api/v1/sessions/{sessionId}/workspace/manifest", {
		params: { path: { sessionId } },
	});
	if (error) throw new Error(apiErrorMessage(error, errorMessage));
	const response = (data ? { ...data, commits: [] } : {
		sessionId,
		files: [],
		truncated: false,
		sections: { staged: [], unstaged: [], untracked: [], committed: [] },
		commits: [],
		summary: { files: 0, additions: 0, deletions: 0 },
	}) as WorkspaceFilesResponse;
	return {
		...response,
		commits: (response.commits ?? []).map((commit) => ({ ...commit, files: commit.files ?? [] })),
		files: response.files ?? [],
		sections: response.sections ?? { staged: [], unstaged: [], untracked: [], committed: [] },
	};
}

async function fetchSessionWorkspaceHistory(sessionId: string, errorMessage: string, hostId?: string, branch: FilesBranchTarget = CHECKED_OUT_BRANCH): Promise<WorkspaceHistoryResponse> {
	const query = branch.kind === "branch" ? { branch: branch.name }
		: branch.kind === "pull_request" ? { prNumber: branch.number, sourceUrl: branch.url || undefined }
		: undefined;
	const { data, error, response } = await clientForSessionHost(hostId).GET("/api/v1/sessions/{sessionId}/workspace/history", {
		params: query ? { path: { sessionId }, query } : { path: { sessionId } },
	});
	if (error) throw workspaceRequestError(error, response, errorMessage);
	if (!data) throw new Error(errorMessage);
	return {
		...data,
		commits: (data.commits ?? []).map((commit) => ({ ...commit, files: commit.files ?? [] })),
	} as WorkspaceHistoryResponse;
}

async function fetchSessionWorkspaceBranches(sessionId: string, errorMessage: string, hostId?: string): Promise<WorkspaceBranches> {
	const { data, error, response } = await clientForSessionHost(hostId).GET("/api/v1/sessions/{sessionId}/workspace/branches", {
		params: { path: { sessionId } },
	});
	if (error) throw workspaceRequestError(error, response, errorMessage);
	if (!data) throw new Error(errorMessage);
	return { ...data, branches: data.branches ?? [] };
}

async function fetchWorkspaceCommitSelection({
	commitShas,
	errorMessage,
	hostId,
	sessionId,
	view,
	workspaceVersion,
}: {
	commitShas: readonly string[];
	errorMessage: string;
	hostId?: string;
	sessionId: string;
	view?: WorkspaceViewParams;
	workspaceVersion?: string;
}): Promise<WorkspaceCommitSelection> {
	const { data, error, response } = await clientForSessionHost(hostId).POST("/api/v1/sessions/{sessionId}/workspace/history/selection", {
		params: { path: { sessionId } },
		body: { ...view, commitShas: [...commitShas], workspaceVersion },
	});
	if (error) throw workspaceRequestError(error, response, errorMessage);
	if (!data) throw new Error(errorMessage);
	return { ...data, files: data.files ?? [] } as WorkspaceCommitSelection;
}

// One SHA travels as commitSha, which every daemon reads; several need commitShas.
function commitParams(commitSha: string | undefined, commitShas: readonly string[] | undefined): { commitSha?: string; commitShas?: string[] } {
	if (commitShas && commitShas.length > 1) return { commitShas: [...commitShas] };
	return { commitSha: commitShas?.[0] ?? commitSha };
}

function selectionKeyFor(view: WorkspaceViewParams | undefined, commitSha: string | undefined, commitShas: readonly string[] | undefined): string {
	return workspaceSelectionKey(view, commitShas ?? (commitSha ? [commitSha] : []));
}

export const sessionWorkspaceFileQueryKey = (sessionId: string, path: string, scope: WorkspaceDiffScope = "combined", commitSha?: string, hostId?: string) =>
	hostId ? ["session-workspace-file", hostId, sessionId, scope, commitSha ?? "", path] as const
		: ["session-workspace-file", sessionId, scope, commitSha ?? "", path] as const;

async function fetchSessionWorkspaceFile(sessionId: string, path: string, scope: WorkspaceDiffScope, errorMessage: string, commitSha?: string, hostId?: string): Promise<WorkspaceFileDetail> {
	const { data, error } = await clientForSessionHost(hostId).GET("/api/v1/sessions/{sessionId}/workspace/file", {
		params: { path: { sessionId }, query: { path, section: scope === "combined" ? undefined : scope, commitSha } },
	});
	if (error) throw new Error(apiErrorMessage(error, errorMessage));
	if (!data) throw new Error(errorMessage);
	return data as WorkspaceFileDetail;
}

export type WorkspacePathResolution = components["schemas"]["WorkspacePathResolutionResponse"];

/**
 * Ask the daemon which session root owns a path. Resolves undefined when the
 * daemon cannot say (network failure, or a remote host running a build without
 * this route), so a caller always has a defined fallback.
 */
export async function resolveSessionWorkspacePath(sessionId: string, path: string, hostId?: string): Promise<WorkspacePathResolution | undefined> {
	try {
		const { data, error } = await clientForSessionHost(hostId).GET("/api/v1/sessions/{sessionId}/workspace/resolve", {
			params: { path: { sessionId }, query: { path } },
		});
		return error ? undefined : data;
	} catch {
		return undefined;
	}
}

async function fetchSessionPRFile(sessionId: string, number: number, sourceUrl: string, path: string, previousPath: string, errorMessage: string, commitSha?: string, hostId?: string): Promise<WorkspaceFileDetail> {
	const { data, error } = await clientForSessionHost(hostId).GET("/api/v1/sessions/{sessionId}/pr/{prNumber}/file", {
		params: { path: { sessionId, prNumber: number }, query: { path, previousPath, sourceUrl, commitSha } },
	});
	if (error) throw new Error(apiErrorMessage(error, errorMessage));
	if (!data) throw new Error(errorMessage);
	return data as WorkspaceFileDetail;
}

// Artifact files live in the session's artifact directory, outside the git
// workspace, so they have no diff/status and aren't reachable through the
// workspace-files endpoint. Local preview hostnames can resolve to ::1, where
// another app may own the daemon's port. Read through the trusted daemon API
// instead, explicitly selecting the artifact root. Remote proxy and cloud URLs
// already identify their content endpoint and must keep their original routing.
async function fetchSessionArtifactFile(sessionId: string, path: string, rawUrl: string | undefined, errorMessage: string): Promise<WorkspaceFileDetail> {
	if (!rawUrl) throw new Error(errorMessage);
	const contentUrl = artifactContentUrl(sessionId, path, rawUrl);
	if (!contentUrl) throw new Error(errorMessage);
	const media = fileMediaKind(path);
	// The artifact route only supports GET; media elements stream their own body.
	const response = await fetch(contentUrl);
	if (media) void response.body?.cancel();
	if (!response.ok) throw new Error(errorMessage);
	const { binary, bytes, content, size, truncated } = media
		? { binary: true, bytes: new Uint8Array(), content: "", size: Number(response.headers.get("content-length")) || 0, truncated: false }
		: await readArtifactTextResponse(response);
	// Only a complete text file can be edited: the save sends it back whole.
	const editable = !binary && !truncated;
	return {
		additions: 0,
		binary,
		content: binary ? "" : content,
		contentTruncated: truncated,
		deleted: false,
		deletions: 0,
		diff: "",
		diffTruncated: false,
		editable,
		fileFingerprint: editable ? await artifactFingerprint(bytes) : "",
		imageMediaType: media === "image" ? response.headers.get("content-type") ?? "image" : undefined,
		mediaVersion: media ? response.headers.get("etag") ?? response.headers.get("last-modified") ?? undefined : undefined,
		path,
		sessionId,
		size,
		status: "unmodified",
		workspaceVersion: "",
	};
}

async function readArtifactTextResponse(response: Response): Promise<{ binary: boolean; bytes: Uint8Array; content: string; size: number; truncated: boolean }> {
	const headerSize = Number.parseInt(response.headers.get("content-length") ?? "", 10);
	if (!response.body && Number.isFinite(headerSize) && headerSize > MAX_ARTIFACT_TEXT_BYTES) {
		return { binary: false, bytes: new Uint8Array(), content: "", size: headerSize, truncated: true };
	}
	const { bytes, size, truncated } = await readBoundedResponseBytes(response, MAX_ARTIFACT_TEXT_BYTES);
	const decoded = decodeArtifactText(bytes, truncated);
	return { ...decoded, bytes, size, truncated };
}

/** The hex SHA-256 of an artifact's bytes, the fingerprint its write endpoint checks. */
async function artifactFingerprint(bytes: Uint8Array): Promise<string> {
	const digest = await crypto.subtle.digest("SHA-256", bytes as Uint8Array<ArrayBuffer>);
	return Array.from(new Uint8Array(digest), (byte) => byte.toString(16).padStart(2, "0")).join("");
}

async function readBoundedResponseBytes(response: Response, limit: number): Promise<{ bytes: Uint8Array; size: number; truncated: boolean }> {
	if (!response.body) {
		const buffer = await response.arrayBuffer();
		const bytes = new Uint8Array(buffer);
		return { bytes: bytes.slice(0, limit), size: bytes.byteLength, truncated: bytes.byteLength > limit };
	}

	const reader = response.body.getReader();
	const chunks: Uint8Array[] = [];
	let total = 0;
	let truncated = false;
	while (true) {
		const { done, value } = await reader.read();
		if (done) break;
		const remaining = limit - total;
		if (value.byteLength > remaining) {
			if (remaining > 0) chunks.push(value.slice(0, remaining));
			total += value.byteLength;
			truncated = true;
			await reader.cancel();
			break;
		}
		chunks.push(value);
		total += value.byteLength;
	}

	const kept = chunks.reduce((sum, chunk) => sum + chunk.byteLength, 0);
	const bytes = new Uint8Array(kept);
	let offset = 0;
	for (const chunk of chunks) {
		bytes.set(chunk, offset);
		offset += chunk.byteLength;
	}
	return { bytes, size: total, truncated };
}

function decodeArtifactText(bytes: Uint8Array, truncated: boolean): { binary: boolean; content: string } {
	if (bytes.includes(0)) return { binary: true, content: "" };
	try {
		return { binary: false, content: new TextDecoder("utf-8", { fatal: true }).decode(bytes) };
	} catch {
		if (truncated) {
			for (let trim = 1; trim < 4 && trim < bytes.byteLength; trim += 1) {
				try {
					return { binary: false, content: new TextDecoder("utf-8", { fatal: true }).decode(bytes.slice(0, bytes.byteLength - trim)) };
				} catch {
					// Invalid UTF-8 away from the bounded suffix is handled below as binary.
				}
			}
		}
		return { binary: true, content: "" };
	}
}

export function sessionArtifactFileQueryOptions(sessionId: string, path: string, rawUrl: string | undefined, errorMessage = "Unable to load artifact"): UseQueryOptions<WorkspaceFileDetail> {
	return {
		queryKey: ["session-artifact-file", rawUrl ?? "", sessionId, path],
		queryFn: () => fetchSessionArtifactFile(sessionId, path, rawUrl, errorMessage),
	};
}

// Shared so the diff view (expand-on-demand) and the plain read-only viewer
// always resolve to the same cache entry for a given (session, path).
export function sessionWorkspaceFileQueryOptions(sessionId: string, path: string, errorMessage = "Unable to load workspace file", scope: WorkspaceDiffScope = "combined", commitSha?: string, hostId?: string) {
	return {
		queryKey: sessionWorkspaceFileQueryKey(sessionId, path, scope, commitSha, hostId),
		queryFn: () => fetchSessionWorkspaceFile(sessionId, path, scope, errorMessage, commitSha, hostId),
		staleTime: 5_000,
	};
}

export function sessionSourceFileQueryOptions(
	sessionId: string,
	source: FilesSource,
	path: string,
	errorMessage = "Unable to load file",
	scope: WorkspaceDiffScope = "combined",
	commitSha?: string,
	previousPath = "",
	hostId?: string,
): UseQueryOptions<WorkspaceFileDetail> {
	if (source.kind === "snapshot")
		return {
			queryKey: ["session-snapshot-file", hostId ?? "local", sessionId, JSON.stringify(source), path],
			queryFn: async ({ signal }) => {
				const { data, error, response } = await clientForSessionHost(hostId).GET("/api/v1/sessions/{sessionId}/workspace/file", {
					signal,
					params: { path: { sessionId }, query: { path, ...snapshotQuery(source) } },
				});
				if (error) throw workspaceRequestError(error, response, errorMessage);
				if (!data) throw new Error(errorMessage);
    if (!data.historical) throw new WorkspaceRequestError("This host does not support snapshot file browsing. Update its daemon to open this revision.","WORKSPACE_SNAPSHOT_UNSUPPORTED",400);
				return data as WorkspaceFileDetail;
			},
			retry: retryWorkspaceRead,
			staleTime: Infinity,
		};
	if (source.kind === "workspace") return sessionWorkspaceFileQueryOptions(sessionId, path, errorMessage, scope, commitSha, hostId);
	if (source.kind === "artifact") return sessionArtifactFileQueryOptions(sessionId, path, source.rawUrl, errorMessage);
	return {
		queryKey: hostId
			? ["session-source-file", hostId, sessionId, "pull_request", source.url, source.snapshot ?? "", commitSha ?? "", path]
			: ["session-source-file", sessionId, "pull_request", source.url, source.snapshot ?? "", commitSha ?? "", path],
		queryFn: () => fetchSessionPRFile(sessionId, source.number, source.url, path, previousPath, errorMessage, commitSha, hostId),
	};
}

/** `selectionKey` names the view and commits read (workspaceSelectionKey); "" is the worktree's own scope or range. */

export const sessionWorkspaceDiffsQueryKey = (
	sessionId: string,
	scope: WorkspaceDiffScope,
	paths: readonly string[],
	contextLines: number,
	ignoreWhitespace: boolean,
	workspaceVersion?: string,
	selectionKey?: string,
	hostId?: string,
) => hostId ? ["session-workspace-diffs", hostId, sessionId, scope, selectionKey ?? "", paths, contextLines, ignoreWhitespace, workspaceVersion ?? ""] as const
	: ["session-workspace-diffs", sessionId, scope, selectionKey ?? "", paths, contextLines, ignoreWhitespace, workspaceVersion ?? ""] as const;

/**
 * Patches for `paths`. `view` reads another branch or a PR; `commitShas` (or
 * the single `commitSha`) narrows the committed scope to those commits.
 */
export function sessionWorkspaceDiffsQueryOptions({
	contextLines = 3,
	errorMessage = "Unable to load workspace changes",
	ignoreWhitespace = false,
	paths,
	scope,
	sessionId,
	workspaceVersion,
	commitSha,
	commitShas,
	view,
	hostId,
}: {
	contextLines?: number;
	errorMessage?: string;
	ignoreWhitespace?: boolean;
	paths: readonly string[];
	scope: WorkspaceDiffScope;
	sessionId: string;
	workspaceVersion?: string;
	commitSha?: string;
	commitShas?: readonly string[];
	view?: WorkspaceViewParams;
	hostId?: string;
}) {
	return {
		queryKey: sessionWorkspaceDiffsQueryKey(sessionId, scope, paths, contextLines, ignoreWhitespace, workspaceVersion, selectionKeyFor(view, commitSha, commitShas), hostId),
		queryFn: async (): Promise<WorkspaceDiffsResponse> => {
			const { data, error, response } = await clientForSessionHost(hostId).POST("/api/v1/sessions/{sessionId}/workspace/diffs", {
				params: { path: { sessionId } },
				body: { ...view, ...commitParams(commitSha, commitShas), contextLines, ignoreWhitespace, paths: [...paths], scope, workspaceVersion },
			});
			if (error) throw workspaceRequestError(error, response, errorMessage);
			if (!data) throw new Error(errorMessage);
			return data;
		},
		retry: retryWorkspaceRead,
	};
}

export async function fetchWorkspaceFileRevision({
	errorMessage = "Unable to load file revision",
	expectedRevision,
	path,
	scope,
	sessionId,
	side,
	workspaceVersion,
	commitSha,
	commitShas,
	view,
	hostId,
	signal,
}: {
	errorMessage?: string;
	expectedRevision?: string;
	path: string;
	scope: WorkspaceDiffScope;
	sessionId: string;
	side: "before" | "after";
	workspaceVersion?: string;
	commitSha?: string;
	commitShas?: readonly string[];
	view?: WorkspaceViewParams;
	hostId?: string;
	signal?: AbortSignal;
}): Promise<WorkspaceFileRevision> {
	const commits = commitParams(commitSha, commitShas);
	const { data, error, response } = await clientForSessionHost(hostId).GET("/api/v1/sessions/{sessionId}/workspace/file/revision", {
		signal,
		params: {
			path: { sessionId },
			query: { path, scope, side, workspaceVersion, expectedRevision, ...view, commitSha: commits.commitSha, commitShas: commits.commitShas?.join(","), snapshot: scope === "committed" && (view !== undefined || commitShas !== undefined) ? true : undefined },
		},
	});
	if (error) throw workspaceRequestError(error, response, errorMessage);
	if (!data) throw new Error(errorMessage);
	return data;
}

export async function fetchPRFileRevision(sessionId: string, number: number, sourceUrl: string, path: string, side: "before" | "after", commitSha?: string, hostId?: string): Promise<WorkspaceFileRevision> {
	const { data, error } = await clientForSessionHost(hostId).GET("/api/v1/sessions/{sessionId}/pr/{prNumber}/file/revision", {
		params: { path: { sessionId, prNumber: number }, query: { path, side, sourceUrl, commitSha } },
	});
	if (error || !data) throw new Error(apiErrorMessage(error, "Unable to load pull request file revision"));
	return data as WorkspaceFileRevision;
}

export function sessionWorkspaceFileRevisionQueryOptions({
	path,
	scope,
	sessionId,
	side,
	workspaceVersion,
	commitSha,
	commitShas,
	view,
	hostId,
}: {
	path: string;
	scope: WorkspaceDiffScope;
	sessionId: string;
	side: "before" | "after";
	workspaceVersion?: string;
	commitSha?: string;
	commitShas?: readonly string[];
	view?: WorkspaceViewParams;
	hostId?: string;
}) {
	const selectionKey = selectionKeyFor(view, commitSha, commitShas);
	return {
		queryKey: hostId ? ["session-workspace-file-revision", hostId, sessionId, scope, selectionKey, side, path, workspaceVersion ?? ""] as const
			: ["session-workspace-file-revision", sessionId, scope, selectionKey, side, path, workspaceVersion ?? ""] as const,
		queryFn: () => fetchWorkspaceFileRevision({ sessionId, path, scope, side, workspaceVersion, commitSha, commitShas, view, hostId }),
	};
}

/** The full contents the review pane loads up front for a file whose patch ends on its last hunk. */
export const filesReviewEndOfFileQueryKey = (
	sessionId: string,
	scope: WorkspaceDiffScope,
	selectionKey: string,
	path: string,
	fileFingerprint: string,
	patchIdentity: string,
	hostId?: string,
) => hostId ? ["files-review-end-of-file", hostId, sessionId, scope, selectionKey, path, fileFingerprint, patchIdentity] as const
	: ["files-review-end-of-file", sessionId, scope, selectionKey, path, fileFingerprint, patchIdentity] as const;

export function sessionSourceFileRevisionQueryOptions({
	path,
	scope,
	sessionId,
	side,
	source,
	workspaceVersion,
	commitSha,
	hostId,
}: {
	path: string;
	scope: WorkspaceDiffScope;
	sessionId: string;
	side: "before" | "after";
	source: FilesSource;
	workspaceVersion?: string;
	commitSha?: string;
	hostId?: string;
}): UseQueryOptions<WorkspaceFileRevision> {
 if(source.kind === "snapshot") return sessionWorkspaceFileRevisionQueryOptions({path,scope:"committed",sessionId,side,view:source.view,commitShas:source.commitShas,hostId});
	if (source.kind === "workspace") return sessionWorkspaceFileRevisionQueryOptions({ path, scope, sessionId, side, workspaceVersion, commitSha, hostId });
	if (source.kind === "pull_request") {
		return {
			queryKey: hostId ? ["session-source-file-revision", hostId, sessionId, "pull_request", source.url, source.snapshot ?? "", commitSha ?? "", side, path] as const
				: ["session-source-file-revision", sessionId, "pull_request", source.url, source.snapshot ?? "", commitSha ?? "", side, path] as const,
			queryFn: () => fetchPRFileRevision(sessionId, source.number, source.url, path, side, commitSha, hostId),
		};
	}
	// Artifacts have no split before/after comparison — they're not diffed
	// against anything, just standalone output files. Callers gate this query
	// with `enabled: detail.deleted || detail.contentTruncated`, neither of
	// which an artifact ever sets, so the queryFn below never actually runs;
	// it still needs to type-check and exist, since options are constructed
	// unconditionally before `enabled` is evaluated.
	return {
		queryKey: ["session-source-file-revision", sessionId, "artifact", side, path] as const,
		queryFn: (): Promise<WorkspaceFileRevision> => {
			throw new Error("Artifact sources do not support file revisions");
		},
	};
}

export async function updateSessionWorkspaceFile({
	content,
	expectedFileFingerprint,
	path,
	sessionId,
	hostId,
}: {
	content: string;
	expectedFileFingerprint: string;
	path: string;
	sessionId: string;
	hostId?: string;
}): Promise<WorkspaceFileDetail> {
	const { data, error } = await clientForSessionHost(hostId).PUT("/api/v1/sessions/{sessionId}/workspace/file", {
		params: { path: { sessionId } },
		body: { content, expectedFileFingerprint, path },
	});
	if (error) throw new Error(apiErrorMessage(error, "Unable to save workspace file"));
	if (!data) throw new Error("Unable to save workspace file");
	return data as WorkspaceFileDetail;
}

/**
 * Replaces the content of one listed artifact file, provided it still holds
 * the content the edit started from, and returns its new fingerprint.
 */
export async function updateSessionArtifactFile({
	content,
	expectedFileFingerprint,
	path,
	sessionId,
	hostId,
}: {
	content: string;
	expectedFileFingerprint: string;
	path: string;
	sessionId: string;
	hostId?: string;
}): Promise<components["schemas"]["ArtifactFileResponse"]> {
	const { data, error } = await clientForSessionHost(hostId).PUT("/api/v1/sessions/{sessionId}/artifact-files", {
		params: { path: { sessionId } },
		body: { content, expectedFileFingerprint, path },
	});
	if (error) throw new Error(apiErrorMessage(error, "Unable to save artifact"));
	if (!data) throw new Error("Unable to save artifact");
	return data;
}

/** Deletes one listed file from the session's artifact directory. */
export async function deleteSessionArtifactFile({ path, sessionId, hostId }: { path: string; sessionId: string; hostId?: string }): Promise<void> {
	const { error } = await clientForSessionHost(hostId).DELETE("/api/v1/sessions/{sessionId}/artifact-files", {
		params: { path: { sessionId }, query: { path } },
	});
	if (error) throw new Error(apiErrorMessage(error, "Unable to delete artifact"));
}

export function sessionWorkspaceSearchQueryOptions(
	sessionId: string,
	query: string,
	errorMessage = "Unable to search workspace files",
	hostId?: string,
	limit = 100,
) {
	return {
		queryKey: hostId ? ["session-workspace-search", hostId, sessionId, query, limit] as const : ["session-workspace-search", sessionId, query, limit] as const,
		queryFn: async ({ signal }: { signal?: AbortSignal } = {}): Promise<WorkspaceFileSearchResponse> => {
			const { data, error } = await clientForSessionHost(hostId).GET("/api/v1/sessions/{sessionId}/workspace/search", {
				params: { path: { sessionId }, query: { query, limit } },
				signal,
			});
			if (error) throw new Error(apiErrorMessage(error, errorMessage));
			if (!data) throw new Error(errorMessage);
			return data;
		},
	};
}

// Shared so SessionFileExplorer and SessionInspector resolve to the same cache
// entry while SSE invalidation remains the normal refresh path.
export function sessionWorkspaceFilesQueryOptions(sessionId: string, errorMessage = "Unable to load workspace files", hostId?: string) {
	return {
		queryKey: sessionWorkspaceFilesQueryKey(sessionId, hostId),
		queryFn: () => fetchSessionWorkspaceFiles(sessionId, errorMessage, hostId),
	};
}

const historyKeyPrefix = (sessionId: string, hostId?: string) =>
	hostId ? ["session-workspace-history", hostId, sessionId] as const : ["session-workspace-history", sessionId] as const;
const branchesQueryKey = (sessionId: string, hostId?: string) =>
	hostId ? ["session-workspace-branches", hostId, sessionId] as const : ["session-workspace-branches", sessionId] as const;

/**
 * The commits a branch lists. The checked-out branch keeps its original key;
 * another branch or a PR is keyed by the tip the branch list shows, so a moved
 * tip reads a new history.
 */
export function sessionWorkspaceHistoryQueryOptions(sessionId: string, errorMessage = "Unable to load workspace history", hostId?: string, branch: FilesBranchTarget = CHECKED_OUT_BRANCH) {
	const prefix = historyKeyPrefix(sessionId, hostId);
	return {
		queryKey: branch.kind === "branch" ? [...prefix, "branch", branch.name, branch.tipSha ?? ""] as const
			: branch.kind === "pull_request" ? [...prefix, "pr", branch.url, branch.tipSha ?? ""] as const
			: prefix,
		queryFn: () => fetchSessionWorkspaceHistory(sessionId, errorMessage, hostId, branch),
		staleTime: Infinity,
		retry: retryWorkspaceRead,
	};
}

/** The branches and PRs the review can read. Refetched when the branch menu opens, since nothing watches other branches. */
export function sessionWorkspaceBranchesQueryOptions(sessionId: string, errorMessage = "Unable to load branches", hostId?: string) {
	return {
		queryKey: branchesQueryKey(sessionId, hostId),
		queryFn: () => fetchSessionWorkspaceBranches(sessionId, errorMessage, hostId),
		staleTime: 15_000,
		retry: retryWorkspaceRead,
	};
}

/**
 * The files changed by ticked commits taken together, or by a view's whole
 * range. Its inputs are immutable SHAs (and the checked-out manifest version),
 * so a result never goes stale.
 */
export function sessionWorkspaceCommitSelectionQueryOptions({
	commitShas,
	errorMessage = "Unable to combine commits",
	hostId,
	selectionKey,
	sessionId,
	view,
	workspaceVersion,
}: {
	commitShas: readonly string[];
	errorMessage?: string;
	hostId?: string;
	selectionKey: string;
	sessionId: string;
	view?: WorkspaceViewParams;
	workspaceVersion?: string;
}) {
	return {
		queryKey: hostId ? ["session-workspace-commit-selection", hostId, sessionId, selectionKey, workspaceVersion ?? ""] as const
			: ["session-workspace-commit-selection", sessionId, selectionKey, workspaceVersion ?? ""] as const,
		queryFn: () => fetchWorkspaceCommitSelection({ commitShas, errorMessage, hostId, sessionId, view, workspaceVersion }),
		staleTime: Infinity,
		retry: retryWorkspaceRead,
	};
}

/** A moved tip or base: refetch the branch list and the views' histories, whose new tips key new reads. */
export function invalidateWorkspaceViews(queryClient: QueryClient, sessionId: string, hostId?: string) {
	const prefix = historyKeyPrefix(sessionId, hostId);
	void queryClient.invalidateQueries({ queryKey: branchesQueryKey(sessionId, hostId) });
	void queryClient.invalidateQueries({ queryKey: prefix, predicate: (query) => query.queryKey.length > prefix.length });
}

export function workspaceFilesRefetchInterval(state: WorkspaceFileConnectionState, degraded = false): false | number {
	return state === "degraded" || degraded ? WORKSPACE_FILES_DEGRADED_REFETCH_MS : false;
}

export function useWorkspaceFileConnectionState(sessionId: string, hostId?: string): WorkspaceFileConnectionState {
	const subscribe = useCallback(
		(listener: () => void) => subscribeWorkspaceFileConnectionState(sessionId, listener, hostId),
		[sessionId, hostId],
	);
	const getSnapshot = useCallback(() => getWorkspaceFileConnectionState(sessionId, hostId), [sessionId, hostId]);
	return useSyncExternalStore(subscribe, getSnapshot);
}

export function isChangedWorkspaceFile(file: WorkspaceFileSummary): boolean {
	return file.status !== "unmodified";
}

// Keep the lightweight summary query warm while the inspector is open. The
// Files view then mounts against current cache data instead of flashing a
// misleading zero while its first request starts. The same moment also
// preloads the default review's diffs, because that view is unmounted until
// the Files tab is selected.
export function useSessionWorkspaceFilesChangedCount(sessionId: string | undefined, hostId?: string): number | undefined {
	const queryClient = useQueryClient();
	const query = useQuery({
		...sessionWorkspaceFilesQueryOptions(sessionId ?? "", "Unable to load workspace files", hostId),
		enabled: Boolean(sessionId),
		// Live invalidations keep the inactive tab fresh; polling starts only
		// when the full Files view is visible.
		refetchInterval: false,
		select: (data: WorkspaceFilesResponse) => data.files.filter(isChangedWorkspaceFile).length,
	});
	useEffect(() => {
		if (!sessionId) return;
		return subscribeWorkspaceFileChanges(sessionId, queryClient, hostId);
	}, [queryClient, sessionId, hostId]);
	useEffect(() => {
		if (!sessionId || query.data === undefined) return;
		// A picked branch or set of changes is not what the default would warm.
		if (!followsDefaultSelection(useUiStore.getState().inspectorSessions[sessionUiKey(sessionId, hostId)]?.filesSelection)) return;
		const data = queryClient.getQueryData<WorkspaceFilesResponse>(sessionWorkspaceFilesQueryKey(sessionId, hostId));
		if (!data) return;
		void prefetchDefaultWorkspaceReviewDiffs(queryClient, sessionId, data, hostId).catch(() => {});
	}, [query.data, query.dataUpdatedAt, queryClient, sessionId, hostId]);
	return sessionId ? query.data : undefined;
}

// Must match WorkspaceReviewPane: the Files tab requests these batches, with
// this context size, and then full contents for files whose patch ends on the
// last hunk. A mismatch leaves the tab on "Loading diff…".
const REVIEW_PREFETCH_BATCHES = 4;
const REVIEW_PREFETCH_EOF_MAX = 40;
const REVIEW_PREFETCH_EOF_MAX_BYTES = 128 * 1024;
const lastPrefetchedReview = new Map<string, string>();

function isDeferredReviewFile(file: WorkspaceFileSummary) {
	const name = file.path.split("/").pop()?.toLowerCase() ?? "";
	return file.size > 512 * 1024 || /^(package-lock\.json|pnpm-lock\.yaml|yarn\.lock|bun\.lockb?|go\.sum|cargo\.lock)$/.test(name);
}

/**
 * What the Files review opens on while nothing is picked: the uncommitted
 * changes, else the branch's commits (Changes, on an older daemon). Built
 * through the same helper as the pane's own target, so the keys match.
 */
export function defaultWorkspaceReviewTarget(data: WorkspaceFilesResponse): ReviewTarget {
	const legacy = isLegacyManifest(data);
	return buildReviewTarget({ branch: CHECKED_OUT_BRANCH, changes: defaultChanges(CHECKED_OUT_BRANCH, data, legacy), manifest: data, legacy });
}

export async function prefetchDefaultWorkspaceReviewDiffs(queryClient: QueryClient, sessionId: string, data: WorkspaceFilesResponse, hostId?: string) {
	// The Files tab count can be seeded from a files array alone. Prefetch only
	// runs against a full workspace response.
	if (!data.sections) return;
	const target = defaultWorkspaceReviewTarget(data);
	const files = (target.files ?? []).filter((file) => !isDeferredReviewFile(file)).slice(0, WORKSPACE_REVIEW_BATCH_SIZE * REVIEW_PREFETCH_BATCHES);
	if (files.length === 0) return;
	const token = `${target.workspaceVersion ?? ""}:${target.scope}:${target.selectionKey}:${files.map((file) => file.path).join("\n")}`;

	try {
		const { REVIEW_CONTEXT_LINES, endsAtLastHunk, patchIdentity } = await import("../components/diffs/trailingContext");
		const { parsePatchFiles } = await import("@pierre/diffs");
		const batches: WorkspaceFileSummary[][] = [];
		for (let index = 0; index < files.length; index += WORKSPACE_REVIEW_BATCH_SIZE) batches.push(files.slice(index, index + WORKSPACE_REVIEW_BATCH_SIZE));
		const cacheKey = sessionUiKey(sessionId, hostId);
		// An existing entry can be invalidated by a file edit without its summary
		// version changing. Skip only if every batch is still present and fresh.
		if (lastPrefetchedReview.get(cacheKey) === token && batches.every((batch) => {
			const state = queryClient.getQueryState(sessionWorkspaceDiffsQueryKey(sessionId, target.scope, batch.map((file) => file.path), REVIEW_CONTEXT_LINES, false, target.workspaceVersion, target.selectionKey, hostId));
			return state?.data !== undefined && !state.isInvalidated;
		})) return;
		lastPrefetchedReview.set(cacheKey, token);
		const responses = await Promise.all(batches.map((batch) => queryClient.fetchQuery({
			...sessionWorkspaceDiffsQueryOptions({
				contextLines: REVIEW_CONTEXT_LINES,
				paths: batch.map((file) => file.path),
				scope: target.scope,
				sessionId,
				workspaceVersion: target.workspaceVersion,
				commitShas: target.commitShas,
				view: target.view,
				hostId,
			}),
			// A prefetch never retries, as fetchQuery's own default.
			retry: false,
			staleTime: Infinity,
			gcTime: 30 * 60 * 1000,
		})));

		const filesByPath = new Map(files.map((file) => [file.path, file]));
		const endOfFile: { file: WorkspaceFileSummary; identity: string }[] = [];
		for (const response of responses) {
			for (const group of response.groups) {
				if (group.truncated || endOfFile.length >= REVIEW_PREFETCH_EOF_MAX) continue;
				const prefix = group.repository ? `${group.repository}/` : "";
				const parsed = parsePatchFiles(group.patch, `${target.workspaceVersion ?? ""}:${target.scope}:${group.patch.length}`, true).flatMap((entry) => entry.files);
				for (const metadata of parsed) {
					if (endOfFile.length >= REVIEW_PREFETCH_EOF_MAX) break;
					if (prefix && !metadata.name.startsWith(prefix)) metadata.name = prefix + metadata.name;
					if (prefix && metadata.prevName && !metadata.prevName.startsWith(prefix)) metadata.prevName = prefix + metadata.prevName;
					const file = filesByPath.get(metadata.name);
					if (!file || file.binary || file.size > REVIEW_PREFETCH_EOF_MAX_BYTES || !endsAtLastHunk(metadata)) continue;
					endOfFile.push({ file, identity: patchIdentity(metadata) });
				}
			}
		}

		await Promise.all(endOfFile.map(async ({ file, identity }) => {
			const queryKey = filesReviewEndOfFileQueryKey(sessionId, target.scope, target.selectionKey, file.path, file.fileFingerprint ?? "", identity, hostId);
			const cached = queryClient.getQueryState(queryKey);
			if (cached?.data !== undefined && !cached.isInvalidated) return;
			try {
				const [before, after] = await Promise.all([
					fetchWorkspaceFileRevision({ commitShas: target.commitShas, view: target.view, sessionId, path: file.path, scope: target.scope, side: "before", workspaceVersion: target.workspaceVersion, hostId }),
					fetchWorkspaceFileRevision({ commitShas: target.commitShas, view: target.view, sessionId, path: file.path, scope: target.scope, side: "after", workspaceVersion: target.workspaceVersion, hostId }),
				]);
				if (before.binary || after.binary || before.truncated || after.truncated) return;
				const loaded = {
					oldFile: { name: file.previousPath || file.path, contents: before.content, cacheKey: before.revision },
					newFile: { name: file.path, contents: after.content, cacheKey: after.revision },
				};
				await queryClient.prefetchQuery({ queryKey, queryFn: () => loaded, retry: false, staleTime: Infinity, gcTime: 30 * 60 * 1000 });
			} catch {
				// Leave the cache empty so the review pane can fetch this file itself.
			}
		}));
	} catch {
		const cacheKey = sessionUiKey(sessionId, hostId);
		if (lastPrefetchedReview.get(cacheKey) === token) lastPrefetchedReview.delete(cacheKey);
	}
}

/** Shared by file, directory and passive-media reads: no live-worktree fallback. */
export function snapshotQuery(source: Extract<FilesSource, { kind: "snapshot" }>) {
	return { snapshot: true as const, ...source.view, commitShas: source.commitShas?.join(",") };
}

export function snapshotBlobUrl(base: string, sessionId: string, path: string, source: Extract<FilesSource, { kind: "snapshot" }>, side: "before" | "after" = "after") {
	const params = new URLSearchParams({ path, side });
	for (const [key, value] of Object.entries(snapshotQuery(source))) if (value !== undefined) params.set(key, String(value));
	return `${base.replace(/\/+$/, "")}/api/v1/sessions/${encodeURIComponent(sessionId)}/workspace/file/blob?${params}`;
}

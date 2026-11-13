import { ApiError, apiRequest } from "../api";
import { authHeaders, httpBase, type ServerConfig } from "../config";
import { formatFileAnnotationMessages, type FileAnnotationComment } from "../../../shared/files/file-annotations";
import { scopedFileDetail, sumFiles, type ScopedDiffs, type ScopedRevision } from "./model";

const API = "/api/v1";

export type WorkspaceFileStatus = "unmodified" | "modified" | "added" | "deleted" | "renamed";
export type WorkspaceSection = "committed" | "staged" | "unstaged" | "untracked";

/** One changed file, as the daemon's workspace manifest lists it. */
export type WorkspaceFileSummary = {
	path: string;
	previousPath?: string;
	status: WorkspaceFileStatus;
	additions: number;
	deletions: number;
	binary: boolean;
	size: number;
	fileFingerprint?: string;
};

/** Every uncommitted change, staged or not, as one diff against HEAD. */
export type FileSection = WorkspaceSection | "uncommitted";

export type WorkspaceManifest = {
	sessionId: string;
	files: WorkspaceFileSummary[];
	/** `uncommitted` is missing from an older daemon, which cannot read it as one section. */
	sections: Record<WorkspaceSection, WorkspaceFileSummary[]> & { uncommitted?: WorkspaceFileSummary[] };
	summary: { files: number; additions: number; deletions: number };
	truncated: boolean;
	workspaceVersion: string;
	compareBaseRef?: string;
	compareMode?: "base" | "head_fallback";
	ahead?: number | null;
	behind?: number | null;
	degraded: boolean;
};

/** One file's content and its git diff, in a single read. */
export type WorkspaceFileDetail = {
	path: string;
	previousPath?: string;
	status: WorkspaceFileStatus;
	additions: number;
	deletions: number;
	binary: boolean;
	deleted: boolean;
	size: number;
	content: string;
	contentTruncated: boolean;
	diff: string;
	diffTruncated: boolean;
	imageMediaType?: string;
	fileFingerprint: string;
	workspaceVersion: string;
};

export type WorkspaceTreeEntry = {
	name: string;
	path: string;
	type: "file" | "dir";
	size?: number;
	binary?: boolean;
	hasChanges?: boolean;
	status?: WorkspaceFileStatus;
};

export type WorkspaceTree = { path: string; entries: WorkspaceTreeEntry[]; truncated: boolean };

export type WorkspaceSearchResult = { path: string; status: WorkspaceFileStatus; binary: boolean; size: number };

/**
 * Another branch or the session's pull request, read in place of the worktree.
 * The daemon answers these from git snapshots, so they are read-only.
 */
export type WorkspaceView = { branch?: string; prNumber?: number; sourceUrl?: string };

/** Which of a file's diffs to read: one git section of the worktree, or one commit. Neither is base to worktree. */
export type FileRead = { section?: FileSection; commitSha?: string };

/** One row of the branch list: a branch, or a pull request whose branch is not local. */
export type WorkspaceBranch = {
	kind: "local" | "remote" | "pull_request";
	/** The branch, the pull request's source branch, or "" for a detached HEAD. */
	name: string;
	checkedOut: boolean;
	detached?: boolean;
	isDefault: boolean;
	prNumber?: number;
	prTitle?: string;
	prUrl?: string;
	tipSha?: string;
	updatedAt?: string | null;
};

/** The checked-out row first, then the rest newest first. */
export type WorkspaceBranches = { branches: WorkspaceBranch[]; supported: boolean; truncated: boolean };

export type WorkspaceCommit = { sha: string; subject: string; author: string; timestamp: string; files: WorkspaceFileSummary[] };

export type WorkspaceHistory = {
	/** Newest first. */
	commits: WorkspaceCommit[];
	commitsTruncated: boolean;
	view: {
		kind: "checked_out" | "branch" | "pull_request";
		name?: string;
		baseRef?: string;
		/** `recent` lists a default branch's newest commits, having no base to count from. */
		rangeMode: "since_base" | "recent";
	};
};

/** The files a range of commits changed, combined. */
export type WorkspaceCommitSelection = {
	files: WorkspaceFileSummary[];
	summary: { files: number; additions: number; deletions: number };
	truncated: boolean;
};

const EMPTY_SECTIONS: Record<WorkspaceSection, WorkspaceFileSummary[]> = { committed: [], staged: [], unstaged: [], untracked: [] };

function sessionPath(sessionId: string, rest: string): string {
	return `${API}/sessions/${encodeURIComponent(sessionId)}/${rest}`;
}

export async function getWorkspaceManifest(cfg: ServerConfig, sessionId: string, init?: Pick<RequestInit, "signal">): Promise<WorkspaceManifest> {
	const res = await apiRequest(cfg, sessionPath(sessionId, "workspace/manifest"), init);
	const body = (await res.json()) as Partial<WorkspaceManifest> | null;
	return {
		sessionId,
		files: body?.files ?? [],
		sections: { ...EMPTY_SECTIONS, ...body?.sections },
		summary: body?.summary ?? { files: 0, additions: 0, deletions: 0 },
		truncated: Boolean(body?.truncated),
		workspaceVersion: body?.workspaceVersion ?? "",
		compareBaseRef: body?.compareBaseRef,
		compareMode: body?.compareMode,
		ahead: body?.ahead,
		behind: body?.behind,
		degraded: Boolean(body?.degraded),
	};
}

/**
 * A view's query. `snapshot` keeps the daemon from ever answering it with the
 * live worktree, as the desktop asks.
 */
function setView(query: URLSearchParams, view: WorkspaceView | undefined): void {
	if (!view) return;
	query.set("snapshot", "true");
	if (view.branch) query.set("branch", view.branch);
	if (view.prNumber) query.set("prNumber", String(view.prNumber));
	if (view.sourceUrl) query.set("sourceUrl", view.sourceUrl);
}

export async function getWorkspaceFile(
	cfg: ServerConfig,
	sessionId: string,
	path: string,
	read: FileRead = {},
	view?: WorkspaceView,
	init?: Pick<RequestInit, "signal">,
): Promise<WorkspaceFileDetail> {
	// A worktree scope reads its own patch and after side, as desktop's review
	// does: the file route's `section` scopes only its diff, and keeps the
	// combined status and the live worktree's content.
	if (read.section && !read.commitSha && !view) return getScopedWorkspaceFile(cfg, sessionId, path, read.section, init);
	const query = new URLSearchParams({ path });
	setView(query, view);
	if (read.commitSha) query.set("commitSha", read.commitSha);
	const res = await apiRequest(cfg, `${sessionPath(sessionId, "workspace/file")}?${query}`, init);
	return (await res.json()) as WorkspaceFileDetail;
}

/** How many times a scoped read starts over when the worktree moves between its two halves. */
const SCOPED_READ_ATTEMPTS = 3;

/**
 * One file in a worktree scope: `workspace/diffs` for the scope's patch (Staged
 * is `--cached`, the branch's commits `<base> HEAD`), then `workspace/file/revision`
 * for its after side (the index, HEAD, or the worktree), fenced to the patch's
 * workspace version. If the agent changes the worktree in between, the daemon
 * refuses the fenced read and the pair is read again, so a patch is never shown
 * over another moment's content.
 */
export async function getScopedWorkspaceFile(cfg: ServerConfig, sessionId: string, path: string, scope: FileSection, init?: Pick<RequestInit, "signal">): Promise<WorkspaceFileDetail> {
	for (let attempt = 1; ; attempt += 1) {
		const diffs = await apiRequest(cfg, sessionPath(sessionId, "workspace/diffs"), {
			...init,
			method: "POST",
			body: JSON.stringify({ paths: [path], scope, contextLines: 3, ignoreWhitespace: false }),
		}).then((res) => res.json() as Promise<ScopedDiffs | null>);
		// A repository's git failure arrives inside a successful answer; it is not "no changes".
		const failure = (diffs?.groups ?? []).flatMap((group) => group.errors ?? [])[0];
		if (failure) throw new Error(failure.message || "Git couldn't read this file's changes.");
		const query = new URLSearchParams({ path, scope, side: "after" });
		if (diffs?.workspaceVersion) query.set("workspaceVersion", diffs.workspaceVersion);
		try {
			const revision = await apiRequest(cfg, `${sessionPath(sessionId, "workspace/file/revision")}?${query}`, init)
				.then((res) => res.json() as Promise<ScopedRevision | null>);
			return scopedFileDetail(path, diffs ?? {}, revision ?? {});
		} catch (cause) {
			const moved = cause instanceof ApiError && cause.code === "WORKSPACE_SNAPSHOT_STALE";
			if (!moved || attempt >= SCOPED_READ_ATTEMPTS) throw cause;
		}
	}
}

/** One folder, of the worktree or of a view's tip. */
export async function getWorkspaceTree(cfg: ServerConfig, sessionId: string, path = "", view?: WorkspaceView, init?: Pick<RequestInit, "signal">): Promise<WorkspaceTree> {
	const params = new URLSearchParams(path ? { path } : {});
	setView(params, view);
	const query = String(params) ? `?${params}` : "";
	const res = await apiRequest(cfg, `${sessionPath(sessionId, "workspace/tree")}${query}`, init);
	const body = (await res.json()) as Partial<WorkspaceTree> | null;
	return { path: body?.path ?? path, entries: body?.entries ?? [], truncated: Boolean(body?.truncated) };
}

const SEARCH_LIMIT = 50;

/**
 * Paths matching `query`. A view has no search route of its own: its tree,
 * filtered, searches the whole snapshot.
 */
export async function searchWorkspaceFiles(cfg: ServerConfig, sessionId: string, query: string, view?: WorkspaceView, init?: Pick<RequestInit, "signal">): Promise<{ results: WorkspaceSearchResult[]; truncated: boolean }> {
	if (view) {
		const params = new URLSearchParams({ filter: query });
		setView(params, view);
		const res = await apiRequest(cfg, `${sessionPath(sessionId, "workspace/tree")}?${params}`, init);
		const body = (await res.json()) as Partial<WorkspaceTree> | null;
		const files = (body?.entries ?? []).filter((entry) => entry.type === "file");
		return {
			results: files.slice(0, SEARCH_LIMIT).map((entry) => ({ path: entry.path, status: entry.status ?? "unmodified", binary: Boolean(entry.binary), size: entry.size ?? 0 })),
			truncated: files.length > SEARCH_LIMIT || Boolean(body?.truncated),
		};
	}
	const params = new URLSearchParams({ query, limit: String(SEARCH_LIMIT) });
	const res = await apiRequest(cfg, `${sessionPath(sessionId, "workspace/search")}?${params}`, init);
	const body = (await res.json()) as { results?: WorkspaceSearchResult[]; truncated?: boolean } | null;
	return { results: body?.results ?? [], truncated: Boolean(body?.truncated) };
}

/**
 * An authenticated image source for one side of a changed image. The route sets
 * no-store, so `version` (the workspace version) is what makes an edited image
 * reload; an unchanged URL is never refetched.
 */
export function workspaceImageSource(cfg: ServerConfig, sessionId: string, path: string, side: "before" | "after", version: string, read: FileRead = {}, view?: WorkspaceView): { uri: string; headers: Record<string, string> } {
	const query = new URLSearchParams({ path, side, v: version });
	setView(query, view);
	if (read.commitSha) query.set("commitSha", read.commitSha);
	return { uri: `${httpBase(cfg)}${sessionPath(sessionId, "workspace/file/blob")}?${query}`, headers: authHeaders(cfg) };
}

/** The branches and pull request the Files screen can compare. */
export async function getWorkspaceBranches(cfg: ServerConfig, sessionId: string, init?: Pick<RequestInit, "signal">): Promise<WorkspaceBranches> {
	const res = await apiRequest(cfg, sessionPath(sessionId, "workspace/branches"), init);
	const body = (await res.json()) as Partial<WorkspaceBranches> | null;
	return { branches: body?.branches ?? [], supported: body?.supported !== false, truncated: Boolean(body?.truncated) };
}

/** The commits on the checked-out branch, or on a view, since its base. */
export async function getWorkspaceHistory(cfg: ServerConfig, sessionId: string, view?: WorkspaceView, init?: Pick<RequestInit, "signal">): Promise<WorkspaceHistory> {
	const params = new URLSearchParams();
	if (view?.branch) params.set("branch", view.branch);
	if (view?.prNumber) params.set("prNumber", String(view.prNumber));
	if (view?.sourceUrl) params.set("sourceUrl", view.sourceUrl);
	const query = String(params) ? `?${params}` : "";
	const res = await apiRequest(cfg, `${sessionPath(sessionId, "workspace/history")}${query}`, init);
	const body = (await res.json()) as Partial<WorkspaceHistory> | null;
	return {
		commits: (body?.commits ?? []).map((commit) => ({ ...commit, files: commit.files ?? [] })),
		commitsTruncated: Boolean(body?.commitsTruncated),
		view: body?.view ?? { kind: view ? (view.prNumber ? "pull_request" : "branch") : "checked_out", rangeMode: "since_base" },
	};
}

/** What a view's whole range of commits changed. */
export async function getWorkspaceRangeFiles(cfg: ServerConfig, sessionId: string, view: WorkspaceView, init?: Pick<RequestInit, "signal">): Promise<WorkspaceCommitSelection> {
	const res = await apiRequest(cfg, sessionPath(sessionId, "workspace/history/selection"), {
		...init,
		method: "POST",
		body: JSON.stringify({ ...view, commitShas: [] }),
	});
	const body = (await res.json()) as Partial<WorkspaceCommitSelection> | null;
	const files = body?.files ?? [];
	return { files, summary: body?.summary ?? sumFiles(files), truncated: Boolean(body?.truncated) };
}

/**
 * Feedback the user typed while reviewing, relayed the way desktop does it:
 * `userAuthored` tells the daemon this is the user's own words, not automation.
 */
async function sendUserAuthored(cfg: ServerConfig, sessionId: string, message: string): Promise<void> {
	await apiRequest(cfg, sessionPath(sessionId, "send"), {
		method: "POST",
		body: JSON.stringify({ message, userAuthored: true }),
	});
}

/**
 * Delivers review comments the way the desktop file viewer does: the shared
 * formatter batches them into as few messages as fit the send limit. Resolves
 * with how many comments reached the agent, so a partial failure keeps the rest.
 */
export async function sendFileFeedback(
	cfg: ServerConfig,
	sessionId: string,
	comments: FileAnnotationComment[],
	onSent?: (count: number) => void,
): Promise<number> {
	let sent = 0;
	for (const { message, count } of formatFileAnnotationMessages(comments)) {
		await sendUserAuthored(cfg, sessionId, message);
		sent += count;
		onSent?.(count);
	}
	return sent;
}

import { apiRequest } from "../api";
import { authHeaders, httpBase, type ServerConfig } from "../config";
import { formatFileAnnotationMessages, type FileAnnotationComment } from "../../../shared/files/file-annotations";

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

export type WorkspaceManifest = {
	sessionId: string;
	files: WorkspaceFileSummary[];
	sections: Record<WorkspaceSection, WorkspaceFileSummary[]>;
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

export async function getWorkspaceFile(
	cfg: ServerConfig,
	sessionId: string,
	path: string,
	section?: WorkspaceSection,
	init?: Pick<RequestInit, "signal">,
): Promise<WorkspaceFileDetail> {
	const query = new URLSearchParams({ path });
	if (section) query.set("section", section);
	const res = await apiRequest(cfg, `${sessionPath(sessionId, "workspace/file")}?${query}`, init);
	return (await res.json()) as WorkspaceFileDetail;
}

export async function getWorkspaceTree(cfg: ServerConfig, sessionId: string, path = "", init?: Pick<RequestInit, "signal">): Promise<WorkspaceTree> {
	const query = path ? `?${new URLSearchParams({ path })}` : "";
	const res = await apiRequest(cfg, `${sessionPath(sessionId, "workspace/tree")}${query}`, init);
	const body = (await res.json()) as Partial<WorkspaceTree> | null;
	return { path: body?.path ?? path, entries: body?.entries ?? [], truncated: Boolean(body?.truncated) };
}

export async function searchWorkspaceFiles(cfg: ServerConfig, sessionId: string, query: string, init?: Pick<RequestInit, "signal">): Promise<{ results: WorkspaceSearchResult[]; truncated: boolean }> {
	const params = new URLSearchParams({ query, limit: "50" });
	const res = await apiRequest(cfg, `${sessionPath(sessionId, "workspace/search")}?${params}`, init);
	const body = (await res.json()) as { results?: WorkspaceSearchResult[]; truncated?: boolean } | null;
	return { results: body?.results ?? [], truncated: Boolean(body?.truncated) };
}

/**
 * An authenticated image source for one side of a changed image. The route sets
 * no-store, so `version` (the workspace version) is what makes an edited image
 * reload; an unchanged URL is never refetched.
 */
export function workspaceImageSource(cfg: ServerConfig, sessionId: string, path: string, side: "before" | "after", version: string): { uri: string; headers: Record<string, string> } {
	const query = new URLSearchParams({ path, side, v: version });
	return { uri: `${httpBase(cfg)}${sessionPath(sessionId, "workspace/file/blob")}?${query}`, headers: authHeaders(cfg) };
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

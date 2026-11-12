import {
	isOrchestratorSession,
	sessionDeliverables,
	STANDALONE_WORKSPACE_ID,
	type ArtifactKind,
	type WorkspaceSummary,
	type WorkspaceSession,
} from "../types/workspace";
import type { ArtifactBundle } from "../../../../packages/shared/artifacts/deliverables";
import { CloudCpError } from "./cloud-cp/errors";
import { fileMediaKind } from "./file-media";
import { isSvgPath } from "./image-files";
import { isArchivedSession } from "./standalone-archive";

/** One artifact file in the Artifacts list, with the session that wrote it. */
export type ArtifactRow = {
	/** The artifact's identity, kept when its file is renamed. */
	id?: string;
	path: string;
	name: string;
	kind: ArtifactKind;
	previewUrl?: string;
	inlineUrl?: string;
	rawUrl?: string;
	updatedAt: string;
	sessionId: string;
	sessionTitle: string;
	cloud?: WorkspaceSession["cloud"];
	/** The remote host the session runs on; undefined for this computer's own and Cloud sessions. */
	hostId?: string;
	/** Undefined for a Scratchpad session. */
	projectId?: string;
	/** Set on a row that stands for a bundle directory; `path` is a file inside it. */
	bundle?: ArtifactBundle;
	/** The file's public link while it is published; unset when it is not shared. */
	shareUrl?: string;
};

/**
 * Whether this person may change or take a copy of a session's artifact files:
 * edit, restore a version, delete, download, copy their contents or copy them
 * to this machine. A local session's files are always theirs. A cloud
 * session's belong to the person who started it, and every other member of
 * the organization only views them; the control plane refuses their edits,
 * restores and deletes. A control plane too old to name the creator leaves
 * the decision to the server. `cloudUserId` is the control-plane ID
 * (useCloudMeUserId), not the desktop's account ID, and undefined until known,
 * when the files stay view only.
 */
export function canChangeSessionArtifacts(cloud: WorkspaceSession["cloud"], cloudUserId: string | undefined): boolean {
	if (!cloud?.createdByUserId) return true;
	return cloud.createdByUserId === cloudUserId;
}

/**
 * What to show when the control plane refused a change to a session's artifact
 * file: its 403 artifact_not_owner reads as `viewOnly`, anything else as is.
 * canChangeSessionArtifacts offers the change on a session with no creator on
 * record, since that looks the same as an older control plane, so this is what
 * a member sees when they try it.
 */
export function artifactChangeError(error: unknown, viewOnly: string): unknown {
	return error instanceof CloudCpError && error.code === "artifact_not_owner" ? new Error(viewOnly) : error;
}

export function artifactKey(artifact: Pick<ArtifactRow, "sessionId" | "path">): string {
	return JSON.stringify([artifact.sessionId, artifact.path]);
}

/** One project's artifacts, newest first. */
export type ArtifactGroup = {
	/** The workspace id; STANDALONE_WORKSPACE_ID for the Scratchpad. */
	id: string;
	name: string;
	isScratchpad: boolean;
	/** A Cloud project's group. */
	cloud: boolean;
	rows: ArtifactRow[];
};

/**
 * Groups the listed artifacts of live local and cloud sessions by project,
 * orchestrators included: they get the same artifact directory and listing rule
 * as workers. Remote-host projects, archived sessions and projects without
 * artifacts are left out; rows sort newest first and groups by their newest
 * row. An orchestrator's rows carry `orchestratorTitle` as their session title
 * when given, since its own title is only its session ID.
 */
export function groupArtifactsByProject(workspaces: readonly WorkspaceSummary[], orchestratorTitle?: string): ArtifactGroup[] {
	const groups: ArtifactGroup[] = [];
	for (const workspace of workspaces) {
		if (workspace.hostId) continue;
		const isScratchpad = workspace.id === STANDALONE_WORKSPACE_ID;
		const rows = workspace.sessions
			.filter((session) => !isArchivedSession(session))
			.flatMap((session) =>
				sessionDeliverables(session).map((artifact): ArtifactRow => ({
					...(artifact.id ? { id: artifact.id } : {}),
					path: artifact.path,
					name: artifact.name,
					kind: artifact.kind,
					previewUrl: artifact.previewUrl,
					inlineUrl: artifact.inlineUrl,
					rawUrl: artifact.rawUrl,
					updatedAt: artifact.updatedAt,
					sessionId: session.id,
					sessionTitle: orchestratorTitle && isOrchestratorSession(session) ? orchestratorTitle : session.title,
					cloud: session.cloud,
					projectId: isScratchpad ? undefined : workspace.id,
					bundle: artifact.bundle,
					...(artifact.shareUrl ? { shareUrl: artifact.shareUrl } : {}),
				})),
			)
			.sort(newestFirst);
		if (rows.length === 0) continue;
		groups.push({ id: workspace.id, name: workspace.name, isScratchpad, cloud: workspace.kind === "cloud", rows });
	}
	return groups.sort((left, right) => newestFirst(left.rows[0], right.rows[0]));
}

/** Total artifacts across groups, for the sidebar badge. */
export function artifactCount(groups: readonly ArtifactGroup[]): number {
	return groups.reduce((total, group) => total + group.rows.length, 0);
}

/** Where an artifact lives: written on this machine or in a cloud session. */
export type ArtifactLocation = "local" | "cloud";

/** The file type the Artifacts page filters by; pages, Markdown, PDFs and other files are documents. */
export type ArtifactType = "image" | "video" | "document";

export const ARTIFACT_TYPES: readonly ArtifactType[] = ["image", "video", "document"];

/** An artifact's file type, from its extension. */
export function artifactType(row: Pick<ArtifactRow, "path">): ArtifactType {
	const media = fileMediaKind(row.path);
	if (media === "image" || isSvgPath(row.path)) return "image";
	if (media === "video") return "video";
	return "document";
}

/** Whether a row lives in a cloud session; a Cloud project's rows always do. */
export function artifactLocation(row: Pick<ArtifactRow, "cloud">, group: Pick<ArtifactGroup, "cloud">): ArtifactLocation {
	return group.cloud || row.cloud ? "cloud" : "local";
}

/**
 * What the Artifacts page shows. Each list keeps the rows matching any of its
 * values; an empty or missing list keeps every row. The lists narrow together.
 */
export type ArtifactFilters = {
	query: string;
	projectIds?: readonly string[];
	/** Undefined for both locations. */
	location?: ArtifactLocation;
	types?: readonly ArtifactType[];
	sharings?: readonly ArtifactSharing[];
};

/** Whether an artifact has a public link ("public") or none ("private"). */
export type ArtifactSharing = "public" | "private";

export const ARTIFACT_SHARINGS: readonly ArtifactSharing[] = ["public", "private"];

/**
 * Whether an artifact is known to be public or known not to be, or undefined
 * when it cannot be told. Only a local session's files carry their public
 * link (the daemon reads it from this computer's publish records); a cloud
 * session's file list says nothing about publishing, so its state is unknown
 * rather than "not shared".
 */
export function artifactSharing(row: Pick<ArtifactRow, "shareUrl" | "cloud">): ArtifactSharing | undefined {
	if (row.shareUrl) return "public";
	return row.cloud ? undefined : "private";
}

/**
 * Keeps groups matching the project filter, and rows matching the location
 * and type filters whose project, file name or session title contains the
 * query. Groups left without rows are dropped.
 */
export function filterArtifactGroups(groups: readonly ArtifactGroup[], filters: ArtifactFilters, scratchpadName?: string): ArtifactGroup[] {
	const { location } = filters;
	const projectIds = filters.projectIds ?? [];
	const types = filters.types ?? [];
	const sharings = filters.sharings ?? [];
	const needle = filters.query.trim().toLocaleLowerCase();
	return groups
		.filter((group) => projectIds.length === 0 || projectIds.includes(group.id))
		.map((group) => {
			const groupMatches = !needle || group.name.toLocaleLowerCase().includes(needle)
				|| Boolean(group.isScratchpad && scratchpadName?.toLocaleLowerCase().includes(needle));
			return {
				...group,
				rows: group.rows.filter((row) =>
					(!location || artifactLocation(row, group) === location)
					&& (types.length === 0 || types.includes(artifactType(row)))
					&& (sharings.length === 0 || matchesSharing(sharings, artifactSharing(row)))
					&& (groupMatches
						|| row.name.toLocaleLowerCase().includes(needle)
						|| row.sessionTitle.toLocaleLowerCase().includes(needle)),
				),
			};
		})
		.filter((group) => group.rows.length > 0);
}

/** A sharing filter keeps only rows whose state is known and chosen. */
function matchesSharing(sharings: readonly ArtifactSharing[], sharing: ArtifactSharing | undefined): boolean {
	return sharing !== undefined && sharings.includes(sharing);
}

function newestFirst(left: Pick<ArtifactRow, "updatedAt">, right: Pick<ArtifactRow, "updatedAt">): number {
	return timeOf(right.updatedAt) - timeOf(left.updatedAt);
}

function timeOf(value: string): number {
	const time = Date.parse(value);
	return Number.isNaN(time) ? 0 : time;
}

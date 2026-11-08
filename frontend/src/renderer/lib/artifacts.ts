import {
	sessionArtifacts,
	STANDALONE_WORKSPACE_ID,
	workerSessions,
	type ArtifactKind,
	type WorkspaceSummary,
	type WorkspaceSession,
} from "../types/workspace";
import { CloudCpError } from "./cloud-cp/errors";
import { isArchivedSession } from "./standalone-archive";

/** One artifact file in the Artifacts list, with the session that wrote it. */
export type ArtifactRow = {
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
	/** Undefined for a Scratchpad session. */
	projectId?: string;
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
 * Groups every artifact from live local and cloud worker sessions by project.
 * Remote-host projects, archived sessions and projects without artifacts are
 * left out; rows sort newest first and groups by their newest row.
 */
export function groupArtifactsByProject(workspaces: readonly WorkspaceSummary[]): ArtifactGroup[] {
	const groups: ArtifactGroup[] = [];
	for (const workspace of workspaces) {
		if (workspace.hostId) continue;
		const isScratchpad = workspace.id === STANDALONE_WORKSPACE_ID;
		const rows = workerSessions(workspace.sessions)
			.filter((session) => !isArchivedSession(session))
			.flatMap((session) =>
				sessionArtifacts(session).map((artifact): ArtifactRow => ({
					path: artifact.path,
					name: artifact.name,
					kind: artifact.kind,
					previewUrl: artifact.previewUrl,
					inlineUrl: artifact.inlineUrl,
					rawUrl: artifact.rawUrl,
					updatedAt: artifact.updatedAt,
					sessionId: session.id,
					sessionTitle: session.title,
					cloud: session.cloud,
					projectId: isScratchpad ? undefined : workspace.id,
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

/**
 * Keeps groups matching the project filter, and rows whose project, file name
 * or session title contains the query. Groups left without rows are dropped.
 */
export function filterArtifactGroups(groups: readonly ArtifactGroup[], query: string, projectId: string, scratchpadName?: string): ArtifactGroup[] {
	const needle = query.trim().toLocaleLowerCase();
	return groups
		.filter((group) => !projectId || group.id === projectId)
		.map((group) => ({
			...group,
			rows: needle && !group.name.toLocaleLowerCase().includes(needle)
				&& !(group.isScratchpad && scratchpadName?.toLocaleLowerCase().includes(needle))
				? group.rows.filter(
						(row) =>
							row.name.toLocaleLowerCase().includes(needle) ||
							row.sessionTitle.toLocaleLowerCase().includes(needle),
					)
				: group.rows,
		}))
		.filter((group) => group.rows.length > 0);
}

function newestFirst(left: Pick<ArtifactRow, "updatedAt">, right: Pick<ArtifactRow, "updatedAt">): number {
	return timeOf(right.updatedAt) - timeOf(left.updatedAt);
}

function timeOf(value: string): number {
	const time = Date.parse(value);
	return Number.isNaN(time) ? 0 : time;
}

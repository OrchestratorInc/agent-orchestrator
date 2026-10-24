import { isAgentActivityWorking } from "@aoagents/product-ui";
import {
	sessionArtifacts,
	STANDALONE_WORKSPACE_ID,
	workerSessions,
	type ArtifactKind,
	type WorkspaceSummary,
	type WorkspaceSession,
} from "../types/workspace";
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
	/** The writing session's agent is working, so the file may still change. */
	generating: boolean;
};

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
					generating: isAgentActivityWorking(session.activity),
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

import {
	isOrchestratorSession,
	sessionDeliverables,
	STANDALONE_WORKSPACE_ID,
	type ArtifactKind,
	type WorkspaceSummary,
	type WorkspaceSession,
} from "../types/workspace";
import type { ArtifactBundle } from "../../../../packages/shared/artifacts/deliverables";
import type { CloudShareAccess } from "../../shared/cloud-share";
import { CloudCpError } from "./cloud-cp/errors";
import { fileMediaKind } from "./file-media";
import { isSvgPath } from "./image-files";
import { isArchivedSession } from "./standalone-archive";

export type ArtifactRow = {
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
	hostId?: string;
	projectId?: string;
	bundle?: ArtifactBundle;
	sha256?: string;
	shareUrl?: string;
	shareAccess?: CloudShareAccess;
};

export function canChangeSessionArtifacts(cloud: WorkspaceSession["cloud"], cloudUserId: string | undefined): boolean {
	if (!cloud?.createdByUserId) return true;
	return cloud.createdByUserId === cloudUserId;
}

export function artifactChangeError(error: unknown, viewOnly: string): unknown {
	return error instanceof CloudCpError && error.code === "artifact_not_owner" ? new Error(viewOnly) : error;
}

export function artifactKey(artifact: Pick<ArtifactRow, "sessionId" | "path">): string {
	return JSON.stringify([artifact.sessionId, artifact.path]);
}

export type ArtifactGroup = {
	id: string;
	name: string;
	isScratchpad: boolean;
	cloud: boolean;
	rows: ArtifactRow[];
};

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
					...(artifact.shareUrl && artifact.shareAccess ? { shareAccess: artifact.shareAccess } : {}),
					...(artifact.sha256 ? { sha256: artifact.sha256 } : {}),
				})),
			)
			.sort(newestFirst);
		if (rows.length === 0) continue;
		groups.push({ id: workspace.id, name: workspace.name, isScratchpad, cloud: workspace.kind === "cloud", rows });
	}
	return groups.sort((left, right) => newestFirst(left.rows[0], right.rows[0]));
}

export function artifactCount(groups: readonly ArtifactGroup[]): number {
	return groups.reduce((total, group) => total + group.rows.length, 0);
}

export type ArtifactLocation = "local" | "cloud";

export type ArtifactType = "image" | "video" | "document";

export const ARTIFACT_TYPES: readonly ArtifactType[] = ["image", "video", "document"];

export function artifactType(row: Pick<ArtifactRow, "path">): ArtifactType {
	const media = fileMediaKind(row.path);
	if (media === "image" || isSvgPath(row.path)) return "image";
	if (media === "video") return "video";
	return "document";
}

export function artifactLocation(row: Pick<ArtifactRow, "cloud">, group: Pick<ArtifactGroup, "cloud">): ArtifactLocation {
	return group.cloud || row.cloud ? "cloud" : "local";
}

export type ArtifactFilters = {
	query: string;
	projectIds?: readonly string[];
	location?: ArtifactLocation;
	types?: readonly ArtifactType[];
	sharings?: readonly ArtifactSharing[];
};

export type ArtifactSharing = "public" | "shared" | "private";

export const ARTIFACT_SHARINGS: readonly ArtifactSharing[] = ["public", "shared", "private"];

export function artifactSharing(row: Pick<ArtifactRow, "shareUrl" | "shareAccess" | "cloud">): ArtifactSharing | undefined {
	if (row.shareUrl) return row.shareAccess === "restricted" ? "shared" : row.shareAccess === "anyone" ? "public" : undefined;
	return row.cloud ? undefined : "private";
}

export function withShareAccess(groups: readonly ArtifactGroup[], accessByUrl: ReadonlyMap<string, CloudShareAccess>): ArtifactGroup[] {
	if (accessByUrl.size === 0) return [...groups];
	return groups.map((group) => ({
		...group,
		rows: group.rows.map((row) => {
			const access = row.shareUrl ? accessByUrl.get(row.shareUrl) : undefined;
			return access ? { ...row, shareAccess: access } : row;
		}),
	}));
}

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

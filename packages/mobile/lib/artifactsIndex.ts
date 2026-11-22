import { deliverableArtifacts } from "../../shared/artifacts/deliverables";
import type { ArtifactKind, SessionArtifact } from "./artifacts";

/** One artifact on the Artifacts page, with the session and project it came from. */
export type ArtifactIndexRow = {
	key: string;
	sessionId: string;
	hostId: string;
	path: string;
	name: string;
	kind: ArtifactKind;
	updatedAt: string;
	sessionTitle: string;
	projectName?: string;
};

type Source = { id: string; hostId: string; projectId?: string | null; artifacts?: SessionArtifact[]; outputType?: string; title: string };

/**
 * Whether a session's files are artifacts at all: only a session started to
 * deliver them, exactly as the desktop's sessionArtifacts decides, so the two
 * Artifacts pages list the same files.
 */
export function deliversArtifacts(outputType: string | undefined): boolean {
	return outputType === "artifact" || outputType === "pr_artifact";
}

/**
 * Every session's deliverables in one list, newest change first, as the
 * desktop's Artifacts page lists them: from sessions that deliver artifacts,
 * the files a person opens, not the images a page links to
 * (deliverableArtifacts).
 */
export function artifactIndexRows(sessions: readonly Source[], projectName: (hostId: string, projectId: string) => string | undefined): ArtifactIndexRow[] {
	const rows: ArtifactIndexRow[] = [];
	for (const session of sessions) {
		if (!session.artifacts?.length || !deliversArtifacts(session.outputType)) continue;
		for (const artifact of deliverableArtifacts(session.artifacts)) {
			rows.push({
				key: `${session.hostId}:${session.id}:${artifact.path}`,
				sessionId: session.id,
				hostId: session.hostId,
				path: artifact.path,
				name: artifact.name,
				kind: artifact.kind,
				updatedAt: artifact.updatedAt,
				sessionTitle: session.title,
				projectName: session.projectId ? projectName(session.hostId, session.projectId) : undefined,
			});
		}
	}
	return rows.sort((a, b) => (Date.parse(b.updatedAt) || 0) - (Date.parse(a.updatedAt) || 0));
}

/** A search over the list: the file's name, its session or its project. */
export function filterArtifactRows(rows: readonly ArtifactIndexRow[], query: string): ArtifactIndexRow[] {
	const needle = query.trim().toLowerCase();
	if (!needle) return [...rows];
	return rows.filter((row) => [row.name, row.sessionTitle, row.projectName ?? ""].some((text) => text.toLowerCase().includes(needle)));
}

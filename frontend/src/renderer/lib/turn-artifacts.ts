import { artifactAtPath, deliverableArtifacts, type Deliverable } from "../../../../packages/shared/artifacts/deliverables";
import type { DiffFile, DiffStatus, TurnArtifactChange, TurnDiff } from "../types/conversation";
import type { SessionArtifact } from "../types/workspace";

/** A deliverable this turn created or edited, with how the turn changed it. */
export type TurnArtifact = {
	artifact: Deliverable<SessionArtifact>;
	status: DiffStatus;
};

/**
 * Where a diff path sits inside the session's artifact directory, or undefined
 * when it lies elsewhere. The renderer is not told where that directory is,
 * only that the daemon keeps it at `<data dir>/artifacts/<session id>/`, so an
 * absolute path is matched on that suffix. A relative path is a worktree file.
 */
export function artifactRelativePath(path: string, sessionId: string): string | undefined {
	const normalized = path.replace(/\\/g, "/");
	if (!normalized.startsWith("/") && !normalized.startsWith("~") && !/^[A-Za-z]:\//.test(normalized)) return undefined;
	const marker = `/artifacts/${sessionId}/`;
	const at = normalized.lastIndexOf(marker);
	if (at < 0) return undefined;
	const relative = normalized.slice(at + marker.length);
	return relative || undefined;
}

function deliverableFor(
	deliverables: readonly Deliverable<SessionArtifact>[],
	relative: string,
): Deliverable<SessionArtifact> | undefined {
	const direct = artifactAtPath(deliverables, relative);
	if (direct) return direct;
	// A bundle stands for every file in its directory.
	return deliverables.find((entry) => entry.bundle && relative.startsWith(`${entry.bundle.path}/`));
}

/** How a turn changed one of its artifacts: "added" only when it made every file of it. */
function cardStatus(statuses: readonly DiffStatus[]): DiffStatus {
	return statuses.every((status) => status === "added") ? "added" : "modified";
}

/**
 * Splits what a turn changed into the deliverables it touched and every other
 * changed file. Deliverables come from two places: the provider's diff, matched
 * by path, and the daemon's own record of the artifact directory (`reported`),
 * which also covers files an agent wrote through a shell command.
 *
 * Only files the session lists as deliverables move out of the diff: a deleted
 * artifact, a supporting file, or one written after the last session refresh
 * stays a plain changed file, so nothing the turn changed goes unshown.
 *
 * Several changed files can make one card (the files of a bundle, or a file
 * both sources report). The card reads Created only when every one of them was
 * added, so adding a file to an existing bundle reads Edited, in any order.
 *
 * `resolve` maps a diff path to the absolute path the same turn reported, for
 * agents whose diffs carry bare names.
 */
export function splitTurnArtifacts(
	diff: TurnDiff | undefined,
	sessionId: string | undefined,
	artifacts: readonly SessionArtifact[] | undefined,
	resolve: (path: string) => string = (path) => path,
	reported: readonly TurnArtifactChange[] = [],
): { artifacts: TurnArtifact[]; files: DiffFile[] } {
	const diffFiles = diff?.files ?? [];
	if (!sessionId || !artifacts?.length) return { artifacts: [], files: diffFiles };
	const deliverables = deliverableArtifacts(artifacts);
	// Insertion order is card order: the diff's, then the daemon's.
	const touched = new Map<string, { artifact: Deliverable<SessionArtifact>; statuses: DiffStatus[] }>();
	const note = (artifact: Deliverable<SessionArtifact>, status: DiffStatus) => {
		const entry = touched.get(artifact.path);
		if (entry) entry.statuses.push(status);
		else touched.set(artifact.path, { artifact, statuses: [status] });
	};
	const files: DiffFile[] = [];
	for (const file of diffFiles) {
		const relative = file.status === "deleted" ? undefined : artifactRelativePath(resolve(file.path), sessionId);
		const artifact = relative ? deliverableFor(deliverables, relative) : undefined;
		if (artifact) note(artifact, file.status);
		else files.push(file);
	}
	for (const change of reported) {
		const artifact = deliverableFor(deliverables, change.path);
		if (artifact) note(artifact, change.status);
	}
	return {
		artifacts: [...touched.values()].map(({ artifact, statuses }) => ({ artifact, status: cardStatus(statuses) })),
		files,
	};
}

import { artifactAtPath, deliverableArtifacts, type ArtifactFacts, type Deliverable } from "./deliverables";

/**
 * Which of a turn's changed files are session deliverables, shared so the
 * desktop and the phone show the same cards for the same turn.
 */

/** How a file changed. The daemon's neutral names, not a provider's. */
export type TurnFileStatus = "added" | "modified" | "deleted" | "renamed";

/** One session artifact a turn changed, by its path in the artifact directory. */
export type TurnArtifactChange = { path: string; status: "added" | "modified" };

type TurnArtifactFile = ArtifactFacts & { previousPaths?: readonly string[] };

/** A deliverable this turn created or edited, with how the turn changed it. */
export type TurnArtifact<T extends TurnArtifactFile> = {
	artifact: Deliverable<T>;
	status: TurnFileStatus;
};

/**
 * Where a diff path sits inside the session's artifact directory, or undefined
 * when it lies elsewhere. The client is not told where that directory is, only
 * that the daemon keeps it at `<data dir>/artifacts/<session id>/`, so an
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

function deliverableFor<T extends TurnArtifactFile>(
	deliverables: readonly Deliverable<T>[],
	relative: string,
): Deliverable<T> | undefined {
	const direct = artifactAtPath(deliverables, relative);
	if (direct) return direct;
	// A bundle stands for every file in its directory.
	return deliverables.find((entry) => entry.bundle && relative.startsWith(`${entry.bundle.path}/`));
}

/** How a turn changed one of its artifacts: "added" only when it made every file of it. */
function cardStatus(statuses: readonly TurnFileStatus[]): TurnFileStatus {
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
export function splitTurnArtifacts<T extends TurnArtifactFile, F extends { path: string; status: TurnFileStatus }>(
	diffFiles: F[] | undefined,
	sessionId: string | undefined,
	artifacts: readonly T[] | undefined,
	resolve: (path: string) => string = (path) => path,
	reported: readonly TurnArtifactChange[] = [],
): { artifacts: TurnArtifact<T>[]; files: F[] } {
	const changed = diffFiles ?? [];
	if (!sessionId || !artifacts?.length) return { artifacts: [], files: changed };
	const deliverables = deliverableArtifacts(artifacts);
	// Insertion order is card order: the diff's, then the daemon's.
	const touched = new Map<string, { artifact: Deliverable<T>; statuses: TurnFileStatus[] }>();
	const note = (artifact: Deliverable<T>, status: TurnFileStatus) => {
		const entry = touched.get(artifact.path);
		if (entry) entry.statuses.push(status);
		else touched.set(artifact.path, { artifact, statuses: [status] });
	};
	const files: F[] = [];
	for (const file of changed) {
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

/** The line under a turn's artifact card: how the turn changed it. */
export function turnArtifactVerb(status: TurnFileStatus): "Created" | "Edited" {
	return status === "added" ? "Created" : "Edited";
}

import { artifactAtPath, deliverableArtifacts, type Deliverable } from "../../../../packages/shared/artifacts/deliverables";
import type { DiffFile, DiffStatus, TurnDiff } from "../types/conversation";
import type { SessionArtifact } from "../types/workspace";

/** A deliverable this turn created or edited, with how the turn changed it. */
export type TurnArtifact = {
	artifact: Deliverable<SessionArtifact>;
	status: DiffStatus;
	/** The path the turn diff reported, which opens the file like a Files row does. */
	diffPath: string;
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

/**
 * Splits a turn diff into the deliverables it touched and every other changed
 * file. Only files the session lists as deliverables move out of the diff: a
 * deleted artifact, a supporting file, or one written after the last session
 * refresh stays a plain changed file, so nothing the turn changed goes unshown.
 *
 * `resolve` maps a diff path to the absolute path the same turn reported, for
 * agents whose diffs carry bare names.
 */
export function splitTurnArtifacts(
	diff: TurnDiff,
	sessionId: string | undefined,
	artifacts: readonly SessionArtifact[] | undefined,
	resolve: (path: string) => string = (path) => path,
): { artifacts: TurnArtifact[]; files: DiffFile[] } {
	if (!sessionId || !artifacts?.length) return { artifacts: [], files: diff.files };
	const deliverables = deliverableArtifacts(artifacts);
	const touched: TurnArtifact[] = [];
	const seen = new Set<string>();
	const files: DiffFile[] = [];
	for (const file of diff.files) {
		const relative = file.status === "deleted" ? undefined : artifactRelativePath(resolve(file.path), sessionId);
		const artifact = relative ? deliverableFor(deliverables, relative) : undefined;
		if (!artifact) {
			files.push(file);
			continue;
		}
		// Several files of one bundle make one card.
		if (seen.has(artifact.path)) continue;
		seen.add(artifact.path);
		touched.push({ artifact, status: file.status, diffPath: resolve(file.path) });
	}
	return { artifacts: touched, files };
}

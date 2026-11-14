import { hydratePartialDiff, type FileDiffLoadedFiles, type FileDiffMetadata } from "@pierre/diffs";

export const REVIEW_CONTEXT_LINES = 3;

export function endsAtLastHunk(fileDiff: FileDiffMetadata): boolean {
	if (!fileDiff.isPartial || (fileDiff.type !== "change" && fileDiff.type !== "rename-changed")) return false;
	const lastHunk = fileDiff.hunks[fileDiff.hunks.length - 1];
	if (!lastHunk) return false;
	if (lastHunk.noEOFCRAdditions || lastHunk.noEOFCRDeletions) return true;
	const tail = lastHunk.hunkContent[lastHunk.hunkContent.length - 1];
	return tail?.type === "change" || (tail?.type === "context" && tail.lines < REVIEW_CONTEXT_LINES);
}

function hashLines(lines: readonly string[]): number {
	let hash = 0x811c9dc5;
	for (const line of lines) {
		for (let index = 0; index < line.length; index++) hash = Math.imul(hash ^ line.charCodeAt(index), 0x01000193);
		hash = Math.imul(hash ^ 0x0a, 0x01000193);
	}
	return hash >>> 0;
}

const identities = new WeakMap<FileDiffMetadata, string>();

export function patchIdentity(fileDiff: FileDiffMetadata): string {
	const cached = identities.get(fileDiff);
	if (cached) return cached;
	const hunks = fileDiff.hunks.map((hunk) => `${hunk.deletionStart},${hunk.deletionCount},${hunk.additionStart},${hunk.additionCount}`).join(";");
	const lines = hashLines([...fileDiff.deletionLines, "\u0000", ...fileDiff.additionLines]);
	const identity = `${fileDiff.type}|${fileDiff.prevName ?? ""}|${hunks}|${lines.toString(36)}`;
	identities.set(fileDiff, identity);
	return identity;
}

export function diffContentVersion(fileDiff: FileDiffMetadata): number {
	return hashLines([fileDiff.isPartial ? "patch" : "full", patchIdentity(fileDiff)]);
}

const hydratedCopies = new WeakMap<FileDiffMetadata, { files: FileDiffLoadedFiles; diff: FileDiffMetadata | null }>();

const diffsByCacheKey = new Map<string, FileDiffMetadata>();

export function stableFileDiff(fileDiff: FileDiffMetadata): FileDiffMetadata {
	const key = fileDiff.cacheKey;
	if (key == null) return fileDiff;
	const existing = diffsByCacheKey.get(key);
	if (existing == null) {
		diffsByCacheKey.set(key, fileDiff);
		return fileDiff;
	}
	if (existing === fileDiff || (existing.isPartial === fileDiff.isPartial && patchIdentity(existing) === patchIdentity(fileDiff))) return existing;
	fileDiff.cacheKey = `${key}\u0000${patchIdentity(fileDiff)}`;
	diffsByCacheKey.set(fileDiff.cacheKey, fileDiff);
	return fileDiff;
}

export function hydratedCopy(fileDiff: FileDiffMetadata, files: FileDiffLoadedFiles): FileDiffMetadata | null {
	const cached = hydratedCopies.get(fileDiff);
	if (cached?.files === files) return cached.diff;
	let diff: FileDiffMetadata | null = null;
	try {
		if (fileDiff.isPartial) diff = hydratePartialDiff("clone", fileDiff, files);
	} catch {
		diff = null;
	}
	if (diff) diff = stableFileDiff(diff);
	hydratedCopies.set(fileDiff, { files, diff });
	return diff;
}

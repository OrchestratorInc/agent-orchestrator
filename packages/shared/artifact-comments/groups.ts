// Element groups for the comments panel (design §7). Threads on the same
// anchor path form one group; groups follow the element's position on the
// page as the bridge reports it, which is document order; threads in a group
// are newest first (the panel opens the newest and folds the rest); a
// marker's count is the group's open threads.
// Imports only sibling files so ao-landing can vendor the folder.

import type { ArtifactCommentAnchor, ArtifactCommentThread } from "./types";
import type { BridgeAnchorMarker, BridgeAnchorPosition } from "./bridge-protocol";

export type ArtifactCommentGroup = {
	path: string;
	/** The oldest thread's anchor. */
	anchor: ArtifactCommentAnchor;
	/** Newest first. */
	threads: ArtifactCommentThread[];
	openCount: number;
	/** From the bridge; undefined until it reports (or when it cannot run). */
	top?: number;
};

export type ArtifactCommentGroups = {
	/** Groups with open threads, on the page top to bottom; the main list. */
	open: ArtifactCommentGroup[];
	/** Groups of resolved threads ("Resolved (N)"), ordered the same way. */
	resolved: ArtifactCommentGroup[];
	/** Open threads whose element the bridge reported found:false ("Couldn't find this part of the page"). */
	unanchored: ArtifactCommentGroup[];
	/** Open threads on the version: the comment icon's count. */
	openCount: number;
	resolvedCount: number;
};

function byCreated(a: ArtifactCommentThread, b: ArtifactCommentThread): number {
	return a.createdAt < b.createdAt ? -1 : a.createdAt > b.createdAt ? 1 : a.id < b.id ? -1 : a.id > b.id ? 1 : 0;
}

function build(threads: ArtifactCommentThread[], positions: ReadonlyMap<string, BridgeAnchorPosition>): ArtifactCommentGroup[] {
	const byPath = new Map<string, ArtifactCommentThread[]>();
	for (const thread of [...threads].sort(byCreated)) {
		const list = byPath.get(thread.anchor.path);
		if (list === undefined) byPath.set(thread.anchor.path, [thread]);
		else list.push(thread);
	}
	const groups: (ArtifactCommentGroup & { oldest: ArtifactCommentThread })[] = [];
	for (const [path, list] of byPath) {
		const position = positions.get(path);
		groups.push({
			path,
			anchor: list[0].anchor,
			oldest: list[0],
			threads: [...list].sort((a, b) => byCreated(b, a)),
			openCount: list.filter((thread) => thread.status === "open").length,
			top: position?.found === true ? position.top : undefined,
		});
	}
	// Positioned groups top to bottom; groups not positioned yet (no bridge, or
	// it has not reported) after them, by their oldest thread.
	return groups
		.sort((a, b) => {
			if (a.top !== undefined && b.top !== undefined && a.top !== b.top) return a.top - b.top;
			if (a.top !== undefined && b.top === undefined) return -1;
			if (a.top === undefined && b.top !== undefined) return 1;
			return byCreated(a.oldest, b.oldest);
		})
		.map(({ oldest: _oldest, ...group }) => group);
}

/**
 * Splits a version's threads into the panel's sections. `positions` is the
 * latest `positions` message from the bridge; pass an empty list before it
 * reports or when it cannot run.
 */
export function groupArtifactCommentThreads(
	threads: readonly ArtifactCommentThread[],
	positions: readonly BridgeAnchorPosition[] = [],
): ArtifactCommentGroups {
	const positionByPath = new Map(positions.map((position) => [position.path, position]));
	const open = threads.filter((thread) => thread.status === "open");
	const resolved = threads.filter((thread) => thread.status === "resolved");
	const isMissing = (thread: ArtifactCommentThread) => positionByPath.get(thread.anchor.path)?.found === false;
	return {
		open: build(open.filter((thread) => !isMissing(thread)), positionByPath),
		resolved: build(resolved, positionByPath),
		unanchored: build(open.filter(isMissing), positionByPath),
		openCount: open.length,
		resolvedCount: resolved.length,
	};
}

/**
 * The bridge's `anchors` payload: one entry per element with threads, open or
 * resolved, with its open-thread count (0 draws no bubble but still gets a
 * position back). The textHash is the oldest thread's.
 */
export function artifactCommentMarkers(threads: readonly ArtifactCommentThread[]): BridgeAnchorMarker[] {
	const markers = new Map<string, BridgeAnchorMarker>();
	for (const thread of [...threads].sort(byCreated)) {
		const marker = markers.get(thread.anchor.path);
		const open = thread.status === "open" ? 1 : 0;
		if (marker === undefined) markers.set(thread.anchor.path, { path: thread.anchor.path, textHash: thread.anchor.textHash, count: open });
		else marker.count += open;
	}
	return [...markers.values()];
}

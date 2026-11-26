// Snapshot deltas for `ao browser snapshot --delta`, in the shape the CLI
// renders for AO's Browser panel: full, unchanged, or the changed refs plus
// one contiguous replacement of tree lines.

import type { RefInfo } from "./snapshot";

export type SnapshotState = { revision: number; text: string; refs: Record<string, RefInfo> };

export type RefChange =
	| { op: "removed"; ref: string }
	| { op: "added" | "changed"; ref: string; node: RefInfo };

export type TreeChange = { startLine: number; deleteCount: number; lines: string[] };

export type SnapshotDelta =
	| { kind: "full"; revision: number; text: string; refs: Record<string, RefInfo> }
	| { kind: "unchanged"; revision: number; baseRevision: number }
	| { kind: "delta"; revision: number; baseRevision: number; changes: RefChange[]; treeChange?: TreeChange };

/**
 * Compare a new snapshot with the base the agent holds. A missing base, or an
 * explicit full request, returns the whole tree.
 */
export function snapshotDelta(
	base: SnapshotState | undefined,
	current: { text: string; refs: Record<string, RefInfo> },
	revision: number,
	full: boolean,
): SnapshotDelta {
	if (!base || full) return { kind: "full", revision, text: current.text, refs: current.refs };
	if (base.text === current.text && sameRefs(base.refs, current.refs)) {
		return { kind: "unchanged", revision, baseRevision: base.revision };
	}
	const changes: RefChange[] = [];
	for (const ref of Object.keys(base.refs)) {
		if (!(ref in current.refs)) changes.push({ op: "removed", ref });
	}
	for (const [ref, node] of Object.entries(current.refs)) {
		const previous = base.refs[ref];
		if (!previous) changes.push({ op: "added", ref, node });
		else if (previous.role !== node.role || previous.name !== node.name) changes.push({ op: "changed", ref, node });
	}
	changes.sort((a, b) => Number(a.ref.slice(1)) - Number(b.ref.slice(1)));
	const before = base.text.split("\n");
	const after = current.text.split("\n");
	let start = 0;
	while (start < before.length && start < after.length && before[start] === after[start]) start++;
	let endBefore = before.length;
	let endAfter = after.length;
	while (endBefore > start && endAfter > start && before[endBefore - 1] === after[endAfter - 1]) {
		endBefore--;
		endAfter--;
	}
	const treeChange =
		endBefore === start && endAfter === start
			? undefined
			: { startLine: start, deleteCount: endBefore - start, lines: after.slice(start, endAfter) };
	return { kind: "delta", revision, baseRevision: base.revision, changes, ...(treeChange ? { treeChange } : {}) };
}

function sameRefs(a: Record<string, RefInfo>, b: Record<string, RefInfo>): boolean {
	const keys = Object.keys(a);
	if (keys.length !== Object.keys(b).length) return false;
	return keys.every((key) => b[key]?.role === a[key]?.role && b[key]?.name === a[key]?.name);
}

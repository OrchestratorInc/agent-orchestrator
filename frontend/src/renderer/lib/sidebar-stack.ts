import type { WorkspaceSession } from "../types/workspace";

/** Where a session sits in a stack of PRs drawn in the sidebar. */
export type SidebarStackPlacement = {
	/** 0 for a session at the top level, 1 or more for one nested under the session of the PR below it. */
	depth: number;
	/** The PR below this one, for a nested session. */
	onNumber?: number;
	/** True for the bottom session of a stack: nothing nests it, and something nests under it. */
	base: boolean;
};

function openPR(session: WorkspaceSession) {
	return session.prs.find((pr) => pr.state === "open" || pr.state === "draft");
}

/**
 * Nests each session whose open PR is based on another listed session's open PR
 * directly under that session, keeping the given order everywhere else. Only
 * sessions in the same list nest: a PR stacked on one nobody here owns stays
 * at the top level.
 */
export function nestStackedSessions(sessions: WorkspaceSession[]): {
	ordered: WorkspaceSession[];
	placement: ReadonlyMap<string, SidebarStackPlacement>;
} {
	const byNumber = new Map<number, WorkspaceSession>();
	for (const session of sessions) {
		const pr = openPR(session);
		if (pr && !byNumber.has(pr.number)) byNumber.set(pr.number, session);
	}
	const parentOf = new Map<string, WorkspaceSession>();
	for (const session of sessions) {
		const below = openPR(session)?.stackParent;
		const parent = below ? byNumber.get(below) : undefined;
		if (parent && parent.id !== session.id) parentOf.set(session.id, parent);
	}
	// A cycle (two PRs based on each other) has no bottom: leave its sessions flat.
	for (const session of sessions) {
		const seen = new Set<string>([session.id]);
		for (let p = parentOf.get(session.id); p; p = parentOf.get(p.id)) {
			if (seen.has(p.id)) {
				for (const id of seen) parentOf.delete(id);
				break;
			}
			seen.add(p.id);
		}
	}
	if (parentOf.size === 0) return { ordered: sessions, placement: new Map() };

	const children = new Map<string, WorkspaceSession[]>();
	for (const session of sessions) {
		const parent = parentOf.get(session.id);
		if (parent) children.set(parent.id, [...(children.get(parent.id) ?? []), session]);
	}
	const ordered: WorkspaceSession[] = [];
	const placement = new Map<string, SidebarStackPlacement>();
	const visit = (session: WorkspaceSession, depth: number) => {
		ordered.push(session);
		const kids = children.get(session.id) ?? [];
		const parent = parentOf.get(session.id);
		if (depth > 0 || kids.length > 0) {
			placement.set(session.id, { depth, base: depth === 0, onNumber: parent ? openPR(parent)?.number : undefined });
		}
		for (const kid of kids) visit(kid, depth + 1);
	};
	for (const session of sessions) if (!parentOf.has(session.id)) visit(session, 0);
	return { ordered, placement };
}

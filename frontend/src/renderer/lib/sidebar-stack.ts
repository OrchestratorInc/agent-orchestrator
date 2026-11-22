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

function openPRs(session: WorkspaceSession) {
	return session.prs.filter((pr) => pr.state === "open" || pr.state === "draft");
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
	// Every open PR, not just the first: one session can own several layers.
	const byNumber = new Map<number, WorkspaceSession>();
	for (const session of sessions) {
		for (const pr of openPRs(session)) if (!byNumber.has(pr.number)) byNumber.set(pr.number, session);
	}
	// A session nests under the owner of the first PR below one of its own
	// that another listed session owns.
	const parentOf = new Map<string, WorkspaceSession>();
	const onNumberOf = new Map<string, number>();
	for (const session of sessions) {
		for (const pr of openPRs(session)) {
			const parent = pr.stackParent ? byNumber.get(pr.stackParent) : undefined;
			if (parent && parent.id !== session.id) {
				parentOf.set(session.id, parent);
				onNumberOf.set(session.id, pr.stackParent!);
				break;
			}
		}
	}
	// A cycle (PRs stacked on each other) has no bottom: leave its members flat.
	// A session that only leads into one keeps its link.
	for (const session of sessions) {
		const path: string[] = [session.id];
		for (let p = parentOf.get(session.id); p; p = parentOf.get(p.id)) {
			const at = path.indexOf(p.id);
			if (at >= 0) {
				for (const id of path.slice(at)) {
					parentOf.delete(id);
					onNumberOf.delete(id);
				}
				break;
			}
			path.push(p.id);
		}
	}
	if (parentOf.size === 0) return { ordered: sessions, placement: new Map() };

	const children = new Map<string, WorkspaceSession[]>();
	for (const session of sessions) {
		const parent = parentOf.get(session.id);
		if (parent) children.set(parent.id, [...(children.get(parent.id) ?? []), session]);
	}
	// A stack takes the place of its highest member, so a layer the caller
	// floated up (one that needs attention) lifts its whole stack with it.
	const index = new Map(sessions.map((session, i) => [session.id, i]));
	const rootOf = (session: WorkspaceSession): WorkspaceSession => {
		let root = session;
		for (let p = parentOf.get(root.id); p; p = parentOf.get(root.id)) root = p;
		return root;
	};
	const rank = new Map<string, number>();
	for (const session of sessions) {
		const root = rootOf(session).id;
		rank.set(root, Math.min(rank.get(root) ?? Infinity, index.get(session.id)!));
	}
	const roots = sessions.filter((session) => !parentOf.has(session.id)).sort((a, b) => rank.get(a.id)! - rank.get(b.id)!);

	const ordered: WorkspaceSession[] = [];
	const placement = new Map<string, SidebarStackPlacement>();
	const visit = (session: WorkspaceSession, depth: number) => {
		ordered.push(session);
		const kids = children.get(session.id) ?? [];
		if (depth > 0 || kids.length > 0) {
			placement.set(session.id, { depth, base: depth === 0, onNumber: onNumberOf.get(session.id) });
		}
		for (const kid of kids) visit(kid, depth + 1);
	};
	for (const root of roots) visit(root, 0);
	return { ordered, placement };
}

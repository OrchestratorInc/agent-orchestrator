import {
	attentionZone,
	isOrchestratorSession,
	sessionNeedsAttention,
	sortedProjectSessions,
	STANDALONE_PROJECT_KIND,
	type WorkspaceSession,
	type WorkspaceSummary,
} from "../types/workspace";
import { sessionUiKey } from "./hosts";

/**
 * Items listed in `order` keep that order; items it has never seen are placed
 * before (`"start"`) or after (`"end"`) the ones it has. The sidebar uses this
 * for drag-reordered projects and sessions.
 */
export function applyOrder<T>(
	items: readonly T[],
	idOf: (item: T) => string,
	order: readonly string[],
	unplaced: "start" | "end",
): T[] {
	if (order.length === 0) return [...items];
	const byId = new Map(items.map((item) => [idOf(item), item]));
	const placed = order.flatMap((id) => {
		const item = byId.get(id);
		return item ? [item] : [];
	});
	const placedIds = new Set(order);
	const rest = items.filter((item) => !placedIds.has(idOf(item)));
	return unplaced === "start" ? [...rest, ...placed] : [...placed, ...rest];
}

/** 只按同项目、同主机的真实编排归属分组；缺失父级的 worker 仍可独立打开。 */
export function groupSidebarSessions(sessions: readonly WorkspaceSession[]) {
	const byId = new Map(sessions.map((session) => [session.id, session]));
	const roots: WorkspaceSession[] = [];
	const childrenByParent = new Map<string, WorkspaceSession[]>();
	for (const session of sessions) {
		const parent = session.parentOrchestratorId ? byId.get(session.parentOrchestratorId) : undefined;
		if (!isOrchestratorSession(session) && parent && isOrchestratorSession(parent)
			&& parent.isTerminated !== true && parent.workspaceId === session.workspaceId
			&& (parent.hostId ?? "") === (session.hostId ?? "")) {
			const children = childrenByParent.get(parent.id) ?? [];
			children.push(session);
			childrenByParent.set(parent.id, children);
		} else {
			roots.push(session);
		}
	}
	return { roots, childrenByParent };
}

export type SidebarNavSession = {
	sessionId: string;
	projectId: string;
	hostId?: string;
	session: WorkspaceSession;
};

export type SidebarOrderInput = {
	workspaces: readonly WorkspaceSummary[];
	remoteWorkspaces?: readonly WorkspaceSummary[];
	/** Saved drag order of the project list. */
	projectOrder?: readonly string[];
	/** Drag order of each project's sessions, keyed by `sessionUiKey(projectId, hostId)`. */
	sessionOrders?: Readonly<Record<string, readonly string[]>>;
};

/** 快捷键遍历与侧栏一致的会话，包含独立编排器并排除已归档会话。 */
function listedSessions(
	workspace: WorkspaceSummary,
	sessionOrders: SidebarOrderInput["sessionOrders"],
): WorkspaceSession[] {
	const visible = sortedProjectSessions(workspace.sessions).filter((session) => session.isTerminated !== true);
	const ordered = applyOrder(visible, (session) => session.id, sessionOrders?.[sessionUiKey(workspace.id, workspace.hostId)] ?? [], "start");
	const { roots, childrenByParent } = groupSidebarSessions(ordered);
	return roots.flatMap((session) => [session, ...(childrenByParent.get(session.id) ?? [])]);
}

/**
 * Every session reachable from the sidebar, in the order the sidebar shows
 * them: pinned sessions, then projects in the user's order, then remote hosts,
 * then the Scratchpad. A session that appears twice (pinned and under its
 * project) counts once, at its first position. Keyboard navigation and the
 * ⌘1–9 jump both walk this list so they always agree with what is on screen.
 */
export function buildSidebarSessionOrder({
	workspaces,
	remoteWorkspaces = [],
	projectOrder = [],
	sessionOrders,
}: SidebarOrderInput): SidebarNavSession[] {
	const entries: SidebarNavSession[] = [];
	const seen = new Set<string>();
	const add = (session: WorkspaceSession, workspace: WorkspaceSummary) => {
		const key = sessionUiKey(session.id, session.hostId ?? workspace.hostId);
		if (seen.has(key)) return;
		seen.add(key);
		entries.push({
			sessionId: session.id,
			projectId: workspace.id,
			hostId: session.hostId ?? workspace.hostId,
			session,
		});
	};

	const pinned = [...workspaces, ...remoteWorkspaces]
		.flatMap((workspace) => sortedProjectSessions(workspace.sessions).map((session) => ({ workspace, session })))
		.filter(({ session }) => session.isPinned && session.isTerminated !== true)
		.sort((a, b) => pinnedTime(b.session) - pinnedTime(a.session));
	for (const { workspace, session } of pinned) add(session, workspace);

	const projects = workspaces.filter((workspace) => workspace.kind !== STANDALONE_PROJECT_KIND);
	for (const workspace of applyOrder(projects, (item) => item.id, projectOrder, "start")) {
		for (const session of listedSessions(workspace, sessionOrders)) add(session, workspace);
	}
	for (const workspace of remoteWorkspaces) {
		for (const session of listedSessions(workspace, sessionOrders)) add(session, workspace);
	}
	for (const workspace of workspaces.filter((item) => item.kind === STANDALONE_PROJECT_KIND)) {
		for (const session of listedSessions(workspace, sessionOrders)) add(session, workspace);
	}
	return entries;
}

function pinnedTime(session: WorkspaceSession): number {
	return session.pinnedAt ? new Date(session.pinnedAt).getTime() || 0 : 0;
}

export function sessionWantsAttention(session: WorkspaceSession): boolean {
	return attentionZone(session) === "merge" || sessionNeedsAttention(session);
}

type NavCursor = { sessionId?: string; hostId?: string };

function indexOfCurrent<T extends { sessionId: string; hostId?: string }>(list: readonly T[], current: NavCursor): number {
	if (!current.sessionId) return -1;
	return list.findIndex((entry) => entry.sessionId === current.sessionId && (entry.hostId ?? "") === (current.hostId ?? ""));
}

/**
 * The entry `direction` steps from the current session, wrapping at the ends,
 * optionally skipping entries that fail `accept`. With no current session
 * (home, a project board) forward starts at the first entry and backward at
 * the last. The current session itself is never returned.
 */
export function stepSessionNav<T extends { sessionId: string; hostId?: string }>(
	list: readonly T[],
	current: NavCursor,
	direction: -1 | 1,
	accept: (entry: T) => boolean = () => true,
): T | undefined {
	const currentIndex = indexOfCurrent(list, current);
	const start = currentIndex === -1 ? (direction === 1 ? -1 : list.length) : currentIndex;
	for (let step = 1; step <= list.length; step += 1) {
		const index = (((start + direction * step) % list.length) + list.length) % list.length;
		if (index === currentIndex) return undefined;
		if (accept(list[index])) return list[index];
	}
	return undefined;
}

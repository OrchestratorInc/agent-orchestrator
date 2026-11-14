import {
	attentionZone,
	sessionNeedsAttention,
	sortedWorkerSessions,
	workerSessions,
	ownScratchpadWorkspaces,
	STANDALONE_PROJECT_KIND,
	STANDALONE_WORKSPACE_ID,
	type WorkspaceSession,
	type WorkspaceSummary,
} from "../types/workspace";
import { sessionUiKey } from "./hosts";

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

export function pinnedFirst<T>(items: readonly T[], idOf: (item: T) => string, pinnedIds: readonly string[]): T[] {
	if (pinnedIds.length === 0) return [...items];
	const pinned = new Set(pinnedIds);
	return [...items.filter((item) => pinned.has(idOf(item))), ...items.filter((item) => !pinned.has(idOf(item)))];
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
	projectOrder?: readonly string[];
	pinnedProjectIds?: readonly string[];
	sessionOrders?: Readonly<Record<string, readonly string[]>>;
};

function listedSessions(
	workspace: WorkspaceSummary,
	sessionOrders: SidebarOrderInput["sessionOrders"],
): WorkspaceSession[] {
	const visible = sortedWorkerSessions(workspace.sessions).filter((session) => session.isTerminated !== true);
	return applyOrder(visible, (session) => session.id, sessionOrders?.[sessionUiKey(workspace.id, workspace.hostId)] ?? [], "start");
}

export function buildSidebarSessionOrder({
	workspaces,
	remoteWorkspaces = [],
	projectOrder = [],
	pinnedProjectIds = [],
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
		.flatMap((workspace) => workerSessions(workspace.sessions).map((session) => ({ workspace, session })))
		.filter(({ session }) => session.isPinned && session.isTerminated !== true)
		.sort((a, b) => pinnedTime(b.session) - pinnedTime(a.session));
	for (const { workspace, session } of pinned) add(session, workspace);

	const scratchpads = ownScratchpadWorkspaces(workspaces);
	const scratchpadOf = new Map(scratchpads.flatMap((workspace) => workspace.sessions.map((session) => [session.id, workspace] as const)));
	const scratchpad: WorkspaceSummary = {
		...(scratchpads.find((workspace) => workspace.kind === STANDALONE_PROJECT_KIND) ??
			{ id: STANDALONE_WORKSPACE_ID, name: "", kind: STANDALONE_PROJECT_KIND, path: "" }),
		sessions: scratchpads.flatMap((workspace) => workspace.sessions),
	};
	for (const session of listedSessions(scratchpad, sessionOrders)) add(session, scratchpadOf.get(session.id) ?? scratchpad);
	const projects = workspaces.filter((workspace) => !scratchpads.includes(workspace));
	const orderedProjects = pinnedFirst(applyOrder(projects, (item) => item.id, projectOrder, "start"), (item) => item.id, pinnedProjectIds);
	for (const workspace of orderedProjects) {
		for (const session of listedSessions(workspace, sessionOrders)) add(session, workspace);
	}
	for (const workspace of remoteWorkspaces) {
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

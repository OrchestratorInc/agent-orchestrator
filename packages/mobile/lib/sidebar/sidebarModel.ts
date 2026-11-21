/**
 * The sidebar's tree, built from the store's rows: Pinned, Sessions (agents
 * with no project) and Projects with their sessions, in the desktop sidebar's
 * order and with its rules (frontend/src/renderer/components/Sidebar.tsx).
 *
 * Pure, no React Native imports, so the rules are unit-tested under Node. The
 * row indicator is the desktop's own code, shared through product-ui, so a row
 * reads the same on both apps.
 */
import type {
	KanbanColumn as ProductKanbanColumn,
	SessionActivity,
	SessionActivityState,
	SessionStatus,
} from "../../../product-ui/src/session-models";
import { getSessionRowIndicator, type SessionRowMarker } from "../../../product-ui/src/session-presentation";
import type { DashboardSession, ProjectInfo } from "../api";
import { isArchived } from "../agentsView";
import { sessionTitle } from "../sessionStatus";

/** Initial projects listed before the section's Show more (desktop: 10). */
export const SIDEBAR_INITIAL_SECTION_LIMIT = 10;
/** Initial sessions listed under an expanded project (desktop: 6). */
export const SIDEBAR_PROJECT_SESSION_LIMIT = 6;
/** Initial sessions listed in the Sessions section (desktop: 5). */
export const SIDEBAR_SESSIONS_LIMIT = 5;

export type SidebarIndicator = "working" | SessionRowMarker | null;

export type SidebarSessionInput = DashboardSession & { hostId?: string; hostName?: string };
export type SidebarProjectInput = ProjectInfo & { hostId?: string; hostName?: string };

export type SidebarSessionRow = {
	/** Unique across machines. */
	key: string;
	session: SidebarSessionInput;
	title: string;
	indicator: SidebarIndicator;
	/** The changed-since-opened dot; only drawn when there is no indicator. */
	unread: boolean;
	/** When the row's terse age counts from. */
	at: string | undefined;
	/** The machine's name, shown only when several environments are listed. */
	hostLabel?: string;
	cloud: boolean;
};

/** What a collapsed project row flags in place of its sessions (desktop's projectActivity). */
export type SidebarProjectActivity = "attention" | "working" | "unread" | null;

export type SidebarProjectNode = {
	key: string;
	project: SidebarProjectInput;
	hostLabel?: string;
	cloud: boolean;
	sessions: SidebarSessionRow[];
	activity: SidebarProjectActivity;
};

export type SidebarTree = {
	pinned: SidebarSessionRow[];
	standalone: SidebarSessionRow[];
	projects: SidebarProjectNode[];
};

export type SidebarTreeInput = {
	sessions: readonly SidebarSessionInput[];
	projects: readonly SidebarProjectInput[];
	/** Label sessions and projects with their machine (more than one environment listed). */
	showHosts: boolean;
	/** The session on screen: opening a session reads it. */
	activeKey?: string;
	isUnread(session: SidebarSessionInput): boolean;
	isCloudHost(hostId: string | undefined): boolean;
	/** Lower-cased filter text; empty lists everything. */
	query?: string;
};

export function sidebarSessionKey(session: { id: string; hostId?: string }): string {
	return `${session.hostId ?? ""}:${session.id}`;
}

export function sidebarProjectKey(project: { id: string; hostId?: string }): string {
	return `${project.hostId ?? ""}:${project.id}`;
}

const ACTIVITY_STATES = new Set<SessionActivityState>(["active", "idle", "waiting_input", "blocked", "exited", "unknown"]);

/** The desktop's row indicator for a mobile session row. */
export function sidebarIndicator(session: DashboardSession): SidebarIndicator {
	const state = session.activity as SessionActivityState | null | undefined;
	const activity: SessionActivity | null = state && ACTIVITY_STATES.has(state)
		? { state, lastActivityAt: session.lastActivityAt }
		: null;
	return getSessionRowIndicator({
		activity,
		displayStatus: session.displayStatus ?? undefined,
		kanbanColumn: (session.kanbanColumn ?? undefined) as ProductKanbanColumn | undefined,
		// An old daemon without a status reads as idle, which flags nothing.
		status: (session.status ?? "idle") as SessionStatus,
		provisionState: session.provisionState,
	});
}

/** Orchestrators live behind their project's button, never in a list. */
export function isSidebarWorker(session: DashboardSession): boolean {
	return session.kind !== "orchestrator" && !isArchived(session);
}

/** Desktop floats the sessions that need you to the top of a project's list. */
export function floatAttention(rows: readonly SidebarSessionRow[]): SidebarSessionRow[] {
	const flagged = rows.filter((row) => row.indicator === "attention");
	if (flagged.length === 0 || flagged.length === rows.length) return [...rows];
	return [...flagged, ...rows.filter((row) => row.indicator !== "attention")];
}

function newestFirst(a: SidebarSessionInput, b: SidebarSessionInput): number {
	return Date.parse(b.createdAt || "") - Date.parse(a.createdAt || "") || a.id.localeCompare(b.id);
}

function matches(text: string, query: string): boolean {
	return text.toLowerCase().includes(query);
}

export function buildSidebarTree(input: SidebarTreeInput): SidebarTree {
	const query = input.query?.trim().toLowerCase() ?? "";
	const row = (session: SidebarSessionInput): SidebarSessionRow => {
		const key = sidebarSessionKey(session);
		const indicator = sidebarIndicator(session);
		return {
			key,
			session,
			title: sessionTitle(session),
			indicator,
			// The open row is being read; anything else that changed since it was last opened is unread.
			unread: indicator === null && key !== input.activeKey && input.isUnread(session),
			at: session.lastEventAt ?? session.updatedAt ?? session.lastActivityAt,
			hostLabel: input.showHosts && !input.isCloudHost(session.hostId) ? session.hostName : undefined,
			cloud: input.isCloudHost(session.hostId),
		};
	};

	const workers = input.sessions.filter(isSidebarWorker).slice().sort(newestFirst);
	const rows = workers.map(row);
	const visible = (item: SidebarSessionRow) => !query || matches(item.title, query);

	// Pinned: the order they were pinned in, oldest first, as desktop lists them.
	const pinned = rows
		.filter((item) => item.session.isPinned && visible(item))
		.sort((a, b) => Date.parse(a.session.pinnedAt ?? "") - Date.parse(b.session.pinnedAt ?? ""));

	const standalone = floatAttention(rows.filter((item) => !item.session.projectId && visible(item)));

	const projects: SidebarProjectNode[] = [];
	for (const project of input.projects) {
		if (project.kind === "scratch") continue;
		const own = rows.filter((item) => item.session.projectId === project.id && (item.session.hostId ?? "") === (project.hostId ?? ""));
		const projectMatches = !query || matches(project.name, query);
		const listed = projectMatches ? own : own.filter(visible);
		if (query && !projectMatches && listed.length === 0) continue;
		const sessions = floatAttention(listed);
		projects.push({
			key: sidebarProjectKey(project),
			project,
			hostLabel: input.showHosts && !input.isCloudHost(project.hostId) ? project.hostName : undefined,
			cloud: input.isCloudHost(project.hostId),
			sessions,
			activity: projectActivity(own),
		});
	}
	return { pinned, standalone, projects };
}

/**
 * What a collapsed project shows on its icon: red when a session needs you,
 * otherwise the orchestrator button's corner dot for work in progress or a
 * finished turn nobody has opened (DESIGN.md decisions log, 2026-11-19).
 */
export function projectActivity(rows: readonly SidebarSessionRow[]): SidebarProjectActivity {
	if (rows.some((item) => item.indicator === "attention")) return "attention";
	if (rows.some((item) => item.indicator === "working")) return "working";
	if (rows.some((item) => item.unread)) return "unread";
	return null;
}

/** Desktop's Show more cap: the cap lifts by itself when the active row sits past it. */
export function capRows<T extends { key: string }>(items: readonly T[], limit: number, showAll: boolean, activeKey?: string): { listed: T[]; hiddenCount: number } {
	const hiddenCount = Math.max(0, items.length - limit);
	if (showAll || items.length <= limit) return { listed: [...items], hiddenCount };
	const activeIndex = activeKey ? items.findIndex((item) => item.key === activeKey) : -1;
	if (activeIndex >= limit) return { listed: [...items], hiddenCount };
	return { listed: items.slice(0, limit), hiddenCount };
}

/** The colour-coded first-letter tile desktop draws for a project without an avatar. */
export const PROJECT_TILE_COLORS = [
	"rgba(248,113,113,0.2)", // red-400
	"rgba(251,146,60,0.2)", // orange-400
	"rgba(251,191,36,0.2)", // amber-400
	"rgba(163,230,53,0.2)", // lime-400
	"rgba(52,211,153,0.2)", // emerald-400
	"rgba(45,212,191,0.2)", // teal-400
	"rgba(56,189,248,0.2)", // sky-400
	"rgba(96,165,250,0.2)", // blue-400
	"rgba(129,140,248,0.2)", // indigo-400
	"rgba(167,139,250,0.2)", // violet-400
	"rgba(232,121,249,0.2)", // fuchsia-400
	"rgba(244,114,182,0.2)", // pink-400
] as const;

/** Same hash as desktop's ProjectIcon, so a project keeps its colour across apps. */
export function projectTileColor(seed: string): string {
	let hash = 0;
	for (let index = 0; index < seed.length; index++) hash = (hash * 31 + seed.charCodeAt(index)) | 0;
	return PROJECT_TILE_COLORS[Math.abs(hash) % PROJECT_TILE_COLORS.length];
}

export function projectLetter(name: string): string {
	return name.match(/[\p{L}\p{N}]/u)?.[0]?.toUpperCase() ?? "?";
}

import { boardZoneOf } from "./agentsView";
import type { DashboardSession } from "./api";
import type { EnvironmentKind } from "./environment/types";
import type { Scoped } from "./environment/scopedBoard";

type SidebarSessionHealth = {
	stale: boolean;
	label: "DISCONNECTED" | "REFRESH FAILED" | null;
	lampStatus: "closed" | "connecting" | "open";
};

export function sidebarSessionHealth(input: {
	environment: EnvironmentKind | null;
	configured: boolean;
	connection: "closed" | "connecting" | "open";
	error: string | null;
}): SidebarSessionHealth {
	if (input.environment === "cloud") {
		return input.error
			? { stale: true, label: "REFRESH FAILED", lampStatus: "closed" }
			: { stale: false, label: null, lampStatus: input.configured ? "open" : "closed" };
	}
	return input.environment === "local" && input.connection !== "open"
		? { stale: true, label: "DISCONNECTED", lampStatus: input.connection }
		: { stale: false, label: null, lampStatus: input.connection };
}

export type SidebarSessionListPresentation =
	| { kind: "loading"; label: string }
	| { kind: "empty"; label: "No active sessions" }
	| { kind: "list" };

/** Distinguishes an empty board from the first request after an environment switch. */
export function sidebarSessionListPresentation(
	environment: EnvironmentKind | null,
	loading: boolean,
	sessionCount: number,
): SidebarSessionListPresentation {
	if (sessionCount > 0) return { kind: "list" };
	if (loading) {
		return {
			kind: "loading",
			label: environment === "cloud" ? "Loading Cloud workers…" : "Loading workers…",
		};
	}
	return { kind: "empty", label: "No active sessions" };
}

export function sidebarSessionRoute(entry: Scoped<Pick<DashboardSession, "id" | "projectId">>): {
	pathname: "/session/[id]"; params: { id: string; projectId: string; source: EnvironmentKind; sourceId: string };
};
export function sidebarSessionRoute(environment: EnvironmentKind | null, session: Pick<DashboardSession, "id" | "projectId">): {
	pathname: "/session/[id]"; params: { id: string; projectId: string };
} | undefined;
export function sidebarSessionRoute(
	entryOrEnvironment: Scoped<Pick<DashboardSession, "id" | "projectId">> | EnvironmentKind | null,
	session?: Pick<DashboardSession, "id" | "projectId">,
) {
	if (entryOrEnvironment && typeof entryOrEnvironment === "object") {
		return { pathname: "/session/[id]" as const, params: {
			id: entryOrEnvironment.value.id,
			projectId: entryOrEnvironment.value.projectId,
			source: entryOrEnvironment.source.kind,
			sourceId: entryOrEnvironment.source.id,
		} };
	}
	const environment = entryOrEnvironment;
	if (environment === null) return undefined;
	return { pathname: "/session/[id]" as const, params: { id: session!.id, projectId: session!.projectId } };
}

export type SidebarDestinationId = "projects" | "agents" | "prs" | "settings";
export type PrimarySidebarDestinationId = Exclude<SidebarDestinationId, "settings">;

export type SidebarDestination = {
	id: SidebarDestinationId;
	label: string;
	href: "/projects" | "/" | "/prs" | "/settings";
};

export const RECENT_WORKERS_LABEL = "Recent Workers";

export const sidebarDestinations: readonly SidebarDestination[] = [
	{ id: "projects", label: "Projects", href: "/projects" },
	{ id: "agents", label: "Workers", href: "/" },
	{ id: "prs", label: "Pull Requests", href: "/prs" },
	{ id: "settings", label: "Settings", href: "/settings" },
];

/**
 * The count a destination is worth badging.
 *
 * Only the number someone would open the app for: workers waiting on a person.
 * A total would be decoration — the board already says how many sessions exist,
 * and a badge that never drops to zero stops being read.
 */
export function sidebarDestinationBadge(
	id: SidebarDestinationId,
	sessions: readonly DashboardSession[],
): number | undefined {
	if (id !== "agents") return undefined;
	const waiting = sidebarSessions(sessions).filter((session) => boardZoneOf(session) === "needs_you").length;
	return waiting || undefined;
}

export function sidebarSessions(sessions: readonly DashboardSession[]): DashboardSession[] {
	return sessions
		.filter((session) => !session.isTerminated && session.status !== "terminated")
		.sort((a, b) => {
			const pinnedOrder = Number(Boolean(b.isPinned)) - Number(Boolean(a.isPinned));
			if (pinnedOrder !== 0) return pinnedOrder;
			return b.lastActivityAt.localeCompare(a.lastActivityAt);
		});
}

export function scopedSidebarSessions(sessions: readonly Scoped<DashboardSession>[]): Scoped<DashboardSession>[] {
	return sessions
		.filter((entry) => !entry.value.isTerminated && entry.value.status !== "terminated")
		.sort((a, b) => {
			const pinnedOrder = Number(Boolean(b.value.isPinned)) - Number(Boolean(a.value.isPinned));
			if (pinnedOrder !== 0) return pinnedOrder;
			return b.value.lastActivityAt.localeCompare(a.value.lastActivityAt);
		});
}

export function activeSidebarDestination(pathname: string): SidebarDestinationId {
	const withoutGroup = pathname.replace(/^\/\(tabs\)/, "") || "/";
	return sidebarDestinations.find(({ href }) => href === withoutGroup)?.id ?? "agents";
}

export function selectedPrimarySidebarDestination(
	pathname: string,
	previous: PrimarySidebarDestinationId,
): PrimarySidebarDestinationId {
	const active = activeSidebarDestination(pathname);
	return active === "settings" ? previous : active;
}

function normalizedPath(pathname: string): string {
	const path = pathname.replace(/^\/\(tabs\)/, "") || "/";
	return path.length > 1 ? path.replace(/\/$/, "") : path;
}

export function sidebarNavigationSettled(pendingPath: string | null, pathname: string): boolean {
	return pendingPath !== null && normalizedPath(pendingPath) === normalizedPath(pathname);
}

type ScrollableSidebarRef = {
	scrollTo?: (options: { y: number; animated: boolean }) => void;
	getScrollResponder?: () => {
		scrollTo?: (options: { y: number; animated: boolean }) => void;
	} | null | undefined;
};

export function scrollSidebarRefToTop(ref: ScrollableSidebarRef | null | undefined) {
	if (ref?.scrollTo) {
		ref.scrollTo({ y: 0, animated: true });
		return;
	}
	if (ref?.getScrollResponder) {
		ref.getScrollResponder()?.scrollTo?.({ y: 0, animated: true });
	}
}

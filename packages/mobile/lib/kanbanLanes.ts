/**
 * The desktop board's lanes on the phone: one lane at a time, chosen from a
 * row of lane chips with their counts (see kanban-lane-switcher.tsx).
 *
 * Lane order and names are the desktop's own, from product-ui: Building,
 * Validating, In review, Ready. Archived sessions are never a lane; the
 * board's Archive keeps them, as the desktop's archive sheet does.
 *
 * Pure, so the rules are tested under Node.
 */
import { boardKanbanColumnOrder, getKanbanColumnView } from "../../product-ui/src/session-presentation";
import type { KanbanColumn as Lane } from "../../product-ui/src/session-models";
import type { DashboardSession } from "./api";
import { isArchived, kanbanColumnOf } from "./agentsView";
import { sidebarIndicator } from "./sidebar/sidebarModel";

export type KanbanLane = Lane;

export const KANBAN_LANES: readonly KanbanLane[] = boardKanbanColumnOrder;

export function laneLabel(lane: KanbanLane): string {
	return getKanbanColumnView(lane).label;
}

/** The lane a live session sits in; archived sessions are in none. */
export function laneOf(session: DashboardSession): KanbanLane | null {
	if (isArchived(session)) return null;
	const column = kanbanColumnOf(session) as KanbanLane;
	return KANBAN_LANES.includes(column) ? column : null;
}

export function laneCounts(sessions: readonly DashboardSession[]): Record<KanbanLane, number> {
	const counts = Object.fromEntries(KANBAN_LANES.map((lane) => [lane, 0])) as Record<KanbanLane, number>;
	for (const session of sessions) {
		const lane = laneOf(session);
		if (lane) counts[lane] += 1;
	}
	return counts;
}

/**
 * The lane a board opens on: the first holding a session that needs you,
 * otherwise the first with anything in it, otherwise Building.
 */
export function initialLane(sessions: readonly DashboardSession[]): KanbanLane {
	const live = sessions.filter((session) => laneOf(session));
	const urgent = KANBAN_LANES.find((lane) => live.some((session) => laneOf(session) === lane && sidebarIndicator(session) === "attention"));
	if (urgent) return urgent;
	return KANBAN_LANES.find((lane) => live.some((session) => laneOf(session) === lane)) ?? KANBAN_LANES[0];
}

/** One lane's live sessions, plus every archived one for the board's Archive. */
export function sessionsForLane<T extends DashboardSession>(sessions: readonly T[], lane: KanbanLane): T[] {
	return sessions.filter((session) => isArchived(session) || laneOf(session) === lane);
}

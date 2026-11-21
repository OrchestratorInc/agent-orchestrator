import { boardZoneOf } from "./agentsView";
import type { DashboardSession } from "./api";

/**
 * The board's name. The board is the Workers page; the route name argument is
 * kept so a restored route that still names the old Projects tab reads sanely.
 */
export function boardTitle(_route?: string): string {
	return "Workers";
}

/**
 * The sidebar button's badge: how many workers are waiting on a person.
 *
 * Only the number someone would open the app for. A total would be decoration,
 * since the board already says how many workers exist, and a badge that never
 * drops to zero stops being read. Ended workers are not waiting on anyone.
 */
export function workersNeedingYou(sessions: readonly DashboardSession[]): number {
	return sessions.filter((session) => !session.isTerminated && session.status !== "terminated" && boardZoneOf(session) === "needs_you").length;
}

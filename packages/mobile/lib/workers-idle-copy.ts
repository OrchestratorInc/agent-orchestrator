import { relativeTime } from "./notificationView";
import type { ConnStatus } from "./store";

export type WorkersIdleInput = {
	/** Connected machines in scope, by name. Empty when the app knows none by name. */
	machines: string[];
	connection: ConnStatus;
	/** When the newest worker in scope last did anything; absent with no history. */
	lastFinishedAt?: string;
	now?: number;
};

/**
 * What the board says when nothing is running.
 *
 * A status report, not a pitch: which machine is quiet, and how long since
 * anything happened. "Spawn a worker to get started" told a returning user
 * nothing they didn't know.
 */
export function workersIdleCopy({ machines, connection, lastFinishedAt, now = Date.now() }: WorkersIdleInput) {
	const status = connection === "connecting"
		? machines.length === 1 ? `Connecting to ${machines[0]}…` : "Connecting…"
		: machines.length === 1 ? `${machines[0]} is idle.`
			: machines.length > 1 ? `All ${machines.length} machines are idle.`
				: "Your machine is idle.";
	const since = lastFinishedAt ? relativeTime(lastFinishedAt, now) : "";
	const history = !since ? "New workers will show up here."
		: since === "now" ? "Last worker finished just now."
			: `Last worker finished ${since} ago.`;
	return { title: "Nothing running", status, history };
}

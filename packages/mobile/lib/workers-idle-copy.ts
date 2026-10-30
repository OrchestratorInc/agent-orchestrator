import { relativeTime } from "./notificationView";
import type { ConnStatus } from "./store";

export type WorkersIdleInput = {
	/** Machines in scope that are `connection`, by name (see idleFleet). Empty when none is known by name. */
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

/**
 * Which machines the idle state may call idle, and the lamp it shows.
 *
 * Only an open machine has sent its workers, so it is the only kind that can be
 * called idle: with one open and another still connecting, the open one is named
 * and the other is not counted yet. With none open, the connecting ones are
 * named instead. `hosts` is the scope the board shows (the selected machine, or
 * the whole fleet); an empty list falls back to the single-host `connection`.
 */
export function idleFleet(
	hosts: readonly { name: string; connection: ConnStatus }[],
	connection: ConnStatus,
): Pick<WorkersIdleInput, "machines" | "connection"> {
	if (hosts.length === 0) return { machines: [], connection };
	const fleet: ConnStatus = hosts.some((host) => host.connection === "open") ? "open"
		: hosts.some((host) => host.connection === "connecting") ? "connecting" : "closed";
	return { machines: hosts.filter((host) => host.connection === fleet).map((host) => host.name), connection: fleet };
}

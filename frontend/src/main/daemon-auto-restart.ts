// Bounded auto-restart policy for the app-owned daemon. When the daemon child
// exits unexpectedly while the app is still running, the supervisor respawns it
// with capped exponential backoff so sessions recover without the user clicking
// the failure banner. A sliding window of recent unexpected exits bounds the
// retries: a daemon that keeps dying past the window is left on the manual
// banner instead of being restarted forever. Because the window is time-based,
// a daemon that stays up long enough has its older exits age out, so retries
// resume on a later crash rather than staying disabled for the session.

/** Unexpected exits allowed within one window before auto-restart gives up. */
export const DAEMON_AUTO_RESTART_MAX_ATTEMPTS = 6;
/** Window over which recent unexpected exits are counted, in milliseconds. */
export const DAEMON_AUTO_RESTART_WINDOW_MS = 5 * 60_000;
const BASE_DELAY_MS = 1_000;
const MAX_DELAY_MS = 30_000;

export type DaemonAutoRestartPlan =
	| { action: "restart"; attempt: number; delayMs: number }
	| { action: "give_up" };

/** Exit facts used to tell a crash from a deliberate stop. */
export type DaemonExitFacts = {
	/** Whether the liveness run-file is still on disk after the child exited. */
	runFilePresent: boolean;
	code: number | null;
	signal: string | null;
};

/**
 * Whether an unexpected daemon exit should be treated as ungraceful — and so
 * respawned. A graceful shutdown removes the run-file and exits 0; a crash,
 * force-kill, or panic instead leaves the run-file behind, exits non-zero, or
 * dies from a signal, and any one of those is enough. This keeps auto-restart
 * from immediately undoing a deliberate `ao stop`.
 */
export function daemonExitWasUngraceful(facts: DaemonExitFacts): boolean {
	return facts.runFilePresent || facts.signal !== null || (facts.code !== null && facts.code !== 0);
}

/**
 * Decide whether to respawn after an unexpected daemon exit at `now`, given the
 * unexpected-exit timestamps already recorded. Returns the plan plus the
 * timestamps to carry forward — the window is pruned on every call, and an exit
 * is only recorded when a restart will actually be scheduled.
 */
export function planDaemonAutoRestart(
	recentExits: readonly number[],
	now: number,
): { plan: DaemonAutoRestartPlan; recentExits: number[] } {
	const inWindow = recentExits.filter((at) => now - at <= DAEMON_AUTO_RESTART_WINDOW_MS);
	if (inWindow.length >= DAEMON_AUTO_RESTART_MAX_ATTEMPTS) {
		return { plan: { action: "give_up" }, recentExits: inWindow };
	}
	const attempt = inWindow.length + 1;
	const delayMs = Math.min(BASE_DELAY_MS * 2 ** (attempt - 1), MAX_DELAY_MS);
	return { plan: { action: "restart", attempt, delayMs }, recentExits: [...inWindow, now] };
}

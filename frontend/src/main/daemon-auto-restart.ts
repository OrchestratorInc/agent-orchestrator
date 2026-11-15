export const DAEMON_AUTO_RESTART_MAX_ATTEMPTS = 6;
export const DAEMON_AUTO_RESTART_WINDOW_MS = 5 * 60_000;
const BASE_DELAY_MS = 1_000;
const MAX_DELAY_MS = 30_000;

export type DaemonAutoRestartPlan =
	| { action: "restart"; attempt: number; delayMs: number }
	| { action: "give_up" };

export type DaemonExitFacts = {
	runFilePresent: boolean;
	runFilePid: number | null;
	childPid: number | null;
	code: number | null;
	signal: string | null;
};

export function daemonExitWasUngraceful(facts: DaemonExitFacts): boolean {
	if (facts.childPid === null || !Number.isSafeInteger(facts.childPid) || facts.childPid <= 1) return false;
	if (facts.runFilePresent) {
		return facts.runFilePid === facts.childPid;
	}
	return facts.signal !== null;
}

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

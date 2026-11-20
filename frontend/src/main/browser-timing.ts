/**
 * AO_BROWSER_TIMING=1 logs where each `ao browser` command spends its time on
 * the desktop side. The daemon's broker honors the same variable
 * (backend/internal/browserruntime/broker.go) for the end-to-end view.
 */
export const BROWSER_TIMING_ENV = "AO_BROWSER_TIMING";

export function browserTimingEnabled(env: NodeJS.ProcessEnv = process.env): boolean {
	return env[BROWSER_TIMING_ENV] === "1";
}

/** Milliseconds since `start` (a performance.now() reading), rounded. */
export function elapsedMs(start: number): number {
	return Math.round(performance.now() - start);
}

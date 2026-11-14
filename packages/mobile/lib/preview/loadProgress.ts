/** Where an estimate starts, so the bar shows at once that something began. */
const ESTIMATE_START = 0.1;
/** An estimate never claims the end: only the load finishing does. */
const ESTIMATE_CEILING = 0.9;
/** How quickly an estimate closes on its ceiling. */
const ESTIMATE_PACE_MS = 1_500;

/**
 * A stand-in for a load that reports no progress of its own: it moves quickly
 * at first and slows as it nears the end, the way Safari's bar does, so a slow
 * page still looks alive without the bar ever pretending to be done.
 */
export function estimatedLoadProgress(elapsedMs: number): number {
	const elapsed = Math.max(0, elapsedMs);
	return ESTIMATE_START + (ESTIMATE_CEILING - ESTIMATE_START) * (1 - Math.exp(-elapsed / ESTIMATE_PACE_MS));
}

/** A load's progress as a screen reader's percentage. */
export function loadProgressPercent(progress: number): number {
	return Math.round(Math.min(1, Math.max(0, progress)) * 100);
}

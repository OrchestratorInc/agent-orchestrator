export const SSE_BACKOFF_INIT_MS = 5_000;
export const SSE_BACKOFF_MAX_MS = 60_000;

const MAX_EXPONENT = 31;

export function computeSseRetryDelayMs(failures: number, random: () => number = Math.random): number {
	const attempt = Number.isFinite(failures)
		? Math.min(Math.max(Math.floor(failures), 1), MAX_EXPONENT)
		: 1;
	const step = Math.min(SSE_BACKOFF_INIT_MS * 2 ** (attempt - 1), SSE_BACKOFF_MAX_MS);
	const jitter = 0.5 + Math.min(Math.max(random(), 0), 0.999_999) * 0.5;
	return Math.round(step * jitter);
}

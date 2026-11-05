const CODEX_UNAVAILABLE_BACKOFF_MS = [30_000, 120_000, 600_000];
let codexAccountsPausedUntil = 0;
let codexBackoffStep = 0;

export function codexAccountsPollPaused(now = Date.now()): boolean {
	return now < codexAccountsPausedUntil;
}

export function noteCodexAccountsAvailable(): void {
	codexAccountsPausedUntil = 0;
	codexBackoffStep = 0;
}

export function noteCodexAccountsUnavailable(now = Date.now()): void {
	const delay = CODEX_UNAVAILABLE_BACKOFF_MS[Math.min(codexBackoffStep, CODEX_UNAVAILABLE_BACKOFF_MS.length - 1)];
	codexBackoffStep += 1;
	codexAccountsPausedUntil = now + delay;
}

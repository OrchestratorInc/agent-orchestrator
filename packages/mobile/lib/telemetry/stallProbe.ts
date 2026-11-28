// Notices the JS thread being busy: a timer asked to fire every `intervalMs`
// reports how late it ran. Pure (clock and timers injected) so it is tested
// without a device; TelemetryManager runs it only while the app is in front,
// since a suspended app's next timer is late by however long it was away.

/** Later than this the app was suspended or the clock jumped: not a stall. */
export const SUSPENDED_MS = 10_000;

export type StallProbe = { start(): void; stop(): void };

export function createStallProbe<H>(opts: {
	intervalMs: number;
	now: () => number;
	setTimer: (fn: () => void, ms: number) => H;
	clearTimer: (handle: H) => void;
	onLate: (lateMs: number) => void;
}): StallProbe {
	let handle: H | null = null;
	let expected = 0;
	const schedule = () => {
		expected = opts.now() + opts.intervalMs;
		handle = opts.setTimer(tick, opts.intervalMs);
	};
	const tick = () => {
		const late = opts.now() - expected;
		if (late > 0 && late < SUSPENDED_MS) opts.onLate(late);
		schedule();
	};
	return {
		start() {
			if (handle === null) schedule();
		},
		stop() {
			if (handle === null) return;
			opts.clearTimer(handle);
			handle = null;
		},
	};
}

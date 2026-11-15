/** Launch timestamps in ms since the process started, reported as one log line. */
export function createLaunchTiming(startedAt: number, now: () => number = Date.now) {
	const marks = new Map<string, number>();
	return {
		/** Keeps the first time a name is marked. */
		mark(name: string): void {
			if (!marks.has(name)) marks.set(name, Math.round(now() - startedAt));
		},
		line(): string {
			return `launch timing: ${[...marks].map(([name, ms]) => `${name}=${ms}ms`).join(" ")}`;
		},
	};
}

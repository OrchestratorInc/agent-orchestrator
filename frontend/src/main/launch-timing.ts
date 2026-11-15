export function createLaunchTiming(startedAt: number, now: () => number = Date.now) {
	const marks = new Map<string, number>();
	return {
		mark(name: string): void {
			if (!marks.has(name)) marks.set(name, Math.round(now() - startedAt));
		},
		line(): string {
			return `launch timing: ${[...marks].map(([name, ms]) => `${name}=${ms}ms`).join(" ")}`;
		},
	};
}

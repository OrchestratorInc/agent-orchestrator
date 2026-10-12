export function createGitHubConnectionAttempt(deps: {
	refresh(signal: AbortSignal): Promise<void>;
	onError(error: unknown): void;
	isCurrent(): boolean;
	now?: () => number;
}): { refresh(): Promise<void>; start(): void; stop(): void } {
	let generation = 0;
	let active = false;
	let controller: AbortController | null = null;
	let inFlight: Promise<void> | null = null;
	let timer: ReturnType<typeof setTimeout> | undefined;
	let deadline: ReturnType<typeof setTimeout> | undefined;
	const now = deps.now ?? Date.now;
	let startedAt = 0;
	function stop() {
		generation += 1; active = false;
		controller?.abort(); controller = null; inFlight = null;
		clearTimeout(timer); clearTimeout(deadline);
	}
	function refresh(): Promise<void> {
		if (!deps.isCurrent()) return Promise.resolve();
		if (inFlight) return inFlight;
		const attempt = generation;
		const current = new AbortController();
		controller = current;
		const pending = (async () => {
			try { await deps.refresh(current.signal); }
			catch (error) {
				if (!current.signal.aborted && attempt === generation && deps.isCurrent()) {
					stop(); deps.onError(error);
				}
			}
		})();
		inFlight = pending;
		void pending.finally(() => {
			if (inFlight === pending) inFlight = null;
			if (controller === current) controller = null;
		});
		return pending;
	}
	async function runNext(attempt: number): Promise<void> {
		await refresh();
		if (!active || attempt !== generation || !deps.isCurrent()) return;
		if (now() - startedAt >= 5 * 60_000) return;
		timer = setTimeout(() => { void runNext(attempt); }, 2500);
	}
	return {
		refresh, stop,
		start() {
			if (active || !deps.isCurrent()) return;
			stop(); active = true; startedAt = now();
			const attempt = generation;
			deadline = setTimeout(() => {
				if (!active || attempt !== generation || !deps.isCurrent()) return;
				stop(); deps.onError(new Error("GitHub did not finish connecting. Try again, or set up manually."));
			}, 5 * 60_000);
			void runNext(attempt);
		},
	};
}

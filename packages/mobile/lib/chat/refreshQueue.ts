/**
 * One refresh at a time, and at most one more waiting behind it.
 *
 * The chat used to start a new download for every change and keep only the
 * newest answer. On a slow link every download was overtaken before it landed,
 * so all of them were thrown away: the transcript stood still for the whole
 * reply while the downloads piled up and slowed the link further. Here a
 * refresh asked for while one runs waits for it, every answer is shown (each
 * one is newer than the last, because they run in order), and any number of
 * requests made meanwhile share the one queued run.
 *
 * `invalidate` ends a generation: the conversation or connection changed, so
 * a run still going must not publish, and nothing waits on it.
 */
export type RefreshQueue = {
	/** Runs `task` now, or once the running one ends. Resolves when a run that started after this call has finished. */
	run(task: (isCurrent: () => boolean) => Promise<void>): Promise<void>;
	/** Whether a run is going or waiting. */
	busy(): boolean;
	invalidate(): void;
};

export function createRefreshQueue(): RefreshQueue {
	let generation = 0;
	let running: { generation: number; done: Promise<void> } | null = null;
	let queued: { generation: number; done: Promise<void> } | null = null;

	const start = (task: (isCurrent: () => boolean) => Promise<void>): Promise<void> => {
		const mine = generation;
		const done = task(() => mine === generation).finally(() => {
			if (running?.done === done) running = null;
		});
		running = { generation: mine, done };
		return done;
	};

	return {
		run(task) {
			// The queued run is checked first: once the running one settles there is
			// a moment where `running` is already cleared but the queued run has not
			// started yet. A caller that awaited the running one and asks again
			// right away must join the queued run, not start a second one beside it.
			if (queued && queued.generation === generation) return queued.done;
			if (!running || running.generation !== generation) return start(task);
			const mine = generation;
			const done = running.done.catch(() => {}).then(() => {
				if (queued?.done === done) queued = null;
				// A newer generation runs its own refresh; this one has nothing to show.
				if (mine !== generation) return;
				return start(task);
			});
			queued = { generation: mine, done };
			return done;
		},
		busy() {
			return Boolean((running && running.generation === generation) || (queued && queued.generation === generation));
		},
		invalidate() {
			generation += 1;
			running = null;
			queued = null;
		},
	};
}

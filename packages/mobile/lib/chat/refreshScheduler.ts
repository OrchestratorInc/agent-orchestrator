export type RefreshScheduler = {
	/** Something changed: read again soon. */
	request(): void;
	/** Drop a read that has not started, and any catch-up owed by one that has. */
	cancel(): void;
};

/**
 * Paces the reads that follow change events.
 *
 * The first request starts a short window that later requests join, so a burst
 * costs one read. Later requests never push that window back: the daemon writes
 * a streaming reply about every 100 ms, so a window that restarted on each event
 * never closed while the agent kept talking, and the reply showed in a few jumps
 * or only once the turn ended. One read runs at a time; requests made while it
 * runs earn exactly one catch-up read after it (as desktop's
 * refreshRemoteConversationOnce), instead of a second read that races the first.
 */
export function createRefreshScheduler(run: () => Promise<void>, windowMs: number): RefreshScheduler {
	let timer: ReturnType<typeof setTimeout> | undefined;
	let running = false;
	let dirty = false;

	const schedule = () => {
		if (timer) return;
		timer = setTimeout(() => {
			timer = undefined;
			void fire();
		}, windowMs);
	};
	const fire = async () => {
		running = true;
		dirty = false;
		try {
			await run();
		} catch {
			// The next event, or the next read the screen makes, tries again.
		} finally {
			running = false;
			if (dirty) {
				dirty = false;
				schedule();
			}
		}
	};

	return {
		request() {
			if (running) dirty = true;
			else schedule();
		},
		cancel() {
			if (timer) clearTimeout(timer);
			timer = undefined;
			dirty = false;
		},
	};
}

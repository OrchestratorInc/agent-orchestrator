/**
 * Run `read` now and again `everyMs` after each read settles, until stopped.
 *
 * Paced from completion, never from a fixed interval: the conversation refresh
 * keeps only its newest request, so a timer that fires faster than a read
 * returns supersedes every read before it lands and nothing ever applies. A
 * failed read is followed by the next one, so one dropped request cannot strand
 * the caller. Returns the stop function.
 */
export function startSerialPoll(read: () => Promise<unknown>, everyMs: number): () => void {
	let stopped = false;
	let timer: ReturnType<typeof setTimeout> | undefined;
	const tick = async () => {
		try {
			await read();
		} catch {
			// The next read retries it.
		}
		if (!stopped) timer = setTimeout(() => void tick(), everyMs);
	};
	void tick();
	return () => {
		stopped = true;
		if (timer !== undefined) clearTimeout(timer);
	};
}

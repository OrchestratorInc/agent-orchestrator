/**
 * `read()` once it returns a config: at once, or as soon as it does, checked
 * every `everyMs`. Null after `timeoutMs`, or once `signal` aborts.
 *
 * The app drops a machine's verified connection on every app switch and while
 * it races the machine's endpoints, so "no config right now" is usually a
 * reconnect, not a machine that is down. A page fetched in that moment (the
 * archive paging on as someone scrolls) waits for it rather than failing.
 */
export function waitForConfig<T>(read: () => T | null, timeoutMs: number, signal?: AbortSignal, everyMs = 250): Promise<T | null> {
	const now = read();
	if (now || signal?.aborted) return Promise.resolve(now);
	return new Promise((resolve) => {
		const started = Date.now();
		const timer = setInterval(() => {
			const config = read();
			if (config || Date.now() - started >= timeoutMs) finish(config);
		}, everyMs);
		const onAbort = () => finish(null);
		function finish(config: T | null) {
			clearInterval(timer);
			signal?.removeEventListener("abort", onAbort);
			resolve(config);
		}
		signal?.addEventListener("abort", onAbort, { once: true });
	});
}

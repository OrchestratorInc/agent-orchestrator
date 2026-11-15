export function scheduleIdle(task: () => void, timeoutMs = 2_000): () => void {
	if (typeof window !== "undefined" && typeof window.requestIdleCallback === "function") {
		const handle = window.requestIdleCallback(() => task(), { timeout: timeoutMs });
		return () => window.cancelIdleCallback(handle);
	}
	const handle = setTimeout(task, Math.min(timeoutMs, 200));
	return () => clearTimeout(handle);
}

import type { BrowserDownloadsState } from "../shared/browser-downloads";

// Only typed AO download events count as completion evidence. A silent file or
// a renderer tab disappearing never supplies a positive download observation.
export function createAwakeDownloadBridge(options: {
	baseURL: () => string | null;
	request: (url: string, init: RequestInit) => Promise<unknown>;
}) {
	let activeIds: string[] = [];
	let timer: ReturnType<typeof setInterval> | undefined;
	let inFlight = false;
	let dirty = false;
	let disposed = false;
	const publish = async () => {
		const base = options.baseURL();
		if (disposed || !base) return;
		if (inFlight) {
			dirty = true;
			return;
		}
		inFlight = true;
		try {
			await options.request(`${base}/api/v1/awake/downloads`, {
				method: "POST",
				headers: { "Content-Type": "application/json" },
				body: JSON.stringify({ activeIds }),
				signal: AbortSignal.timeout(3000),
			});
		} catch {
			/* Wake policy must not interrupt Chromium's transfer. */
		} finally {
			inFlight = false;
			if (dirty && !disposed) {
				dirty = false;
				void publish();
			}
		}
	};
	return {
		update(state: BrowserDownloadsState) {
			if (disposed) return;
			const next = state.downloads
				.filter(
					(download) =>
						download.active &&
						(download.status === "progressing" || download.status === "paused"),
				)
				.map((download) => download.id)
				.sort();
			if (next.join("\0") !== activeIds.join("\0")) {
				activeIds = next;
				void publish();
			}
			if (activeIds.length && !timer) {
				timer = setInterval(() => {
					void publish();
				}, 5000);
				timer.unref?.();
			}
			if (!activeIds.length && timer) {
				clearInterval(timer);
				timer = undefined;
			}
		},
		dispose() {
			disposed = true;
			if (timer) clearInterval(timer);
			timer = undefined;
		},
	};
}

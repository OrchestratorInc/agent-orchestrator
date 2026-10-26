import type { ProfilerOnRenderCallback } from "react";

/** Lab instrumentation only: never imported by the application. No message text is retained. */
export function measureChatOpen() {
	const startedAt = performance.now();
	const longTasks: { startMs: number; durationMs: number }[] = [];
	const commits: {
		id: string;
		phase: string;
		actualDurationMs: number;
		baseDurationMs: number;
		startMs: number;
		commitMs: number;
	}[] = [];
	const collect = (entries: PerformanceEntry[]) => {
		for (const entry of entries) {
			// Navigation/render often begins inside a task that has already started.
			// Keep that overlapping task rather than hiding the initial mount cost.
			if (entry.startTime + entry.duration > startedAt) longTasks.push({
				startMs: entry.startTime - startedAt,
				durationMs: entry.duration,
			});
		}
	};
	const longTasksSupported = typeof PerformanceObserver !== "undefined"
		&& PerformanceObserver.supportedEntryTypes.includes("longtask");
	const observer = longTasksSupported
		? new PerformanceObserver((list) => collect(list.getEntries()))
		: undefined;
	observer?.observe({ type: "longtask" });
	const onRender: ProfilerOnRenderCallback = (id, phase, actualDuration, baseDuration, startTime, commitTime) => {
		commits.push({ id, phase, actualDurationMs: actualDuration, baseDurationMs: baseDuration,
			startMs: startTime - startedAt, commitMs: commitTime - startedAt });
	};
	const stop = () => {
		if (observer) collect(observer.takeRecords());
		observer?.disconnect();
	};
	return {
		onRender,
		cancel: stop,
		/** Call after starting navigation/render. Selector must identify the final durable message. */
		async finish(lastMessage: () => HTMLElement | null, timeoutMs = 15_000) {
			try {
				let visibleFrames = 0;
				while (performance.now() - startedAt < timeoutMs) {
					// An occluded Electron window may stop producing animation frames.
					await new Promise<void>((resolve, reject) => {
						const timer = setTimeout(() => {
							cancelAnimationFrame(frameId);
								reject(new Error(`Final message did not become visible within ${timeoutMs}ms; measurement incomplete`));
						}, Math.max(0, timeoutMs - (performance.now() - startedAt)));
						const frameId = requestAnimationFrame(() => {
							clearTimeout(timer);
							resolve();
						});
					});
					const node = lastMessage();
					const viewport = node?.closest<HTMLElement>('[role="log"]');
					const rect = node?.getBoundingClientRect();
					const clip = viewport?.getBoundingClientRect();
					const visible = node && rect && clip && node.checkVisibility({ checkOpacity: true, checkVisibilityCSS: true })
						&& rect.width > 0 && rect.height > 0
						&& rect.bottom > Math.max(0, clip.top) && rect.top < Math.min(innerHeight, clip.bottom)
						&& rect.right > Math.max(0, clip.left) && rect.left < Math.min(innerWidth, clip.right);
					visibleFrames = visible ? visibleFrames + 1 : 0;
					if (visibleFrames === 2) {
						const lastMessageVisibleMs = performance.now() - startedAt;
						// Observer delivery occurs after a task ends. Let that task finish,
						// then filter out entries belonging to the post-visibility drain.
						await new Promise<void>((resolve) => setTimeout(resolve, 0));
						stop();
						const tasks = longTasks.filter((entry) => entry.startMs < lastMessageVisibleMs);
						const reactCommits = commits.filter((entry) => entry.commitMs <= lastMessageVisibleMs);
						const memory = (performance as Performance & { memory?: { usedJSHeapSize: number } }).memory;
						return {
							lastMessageVisibleMs,
							longTasksSupported,
							longTasks: longTasksSupported ? {
								count: tasks.length,
								maxMs: Math.max(0, ...tasks.map((task) => task.durationMs)),
								totalMs: tasks.reduce((sum, task) => sum + task.durationMs, 0),
								entries: tasks,
							} : null,
							// actualDuration is React render work per commit, not commit-phase wall time.
							reactProfilerSamplesAvailable: reactCommits.length > 0,
							reactCommits,
							reactActualDurationTotalMs: reactCommits.length > 0
								? reactCommits.reduce((sum, entry) => sum + entry.actualDurationMs, 0) : null,
							jsHeapUsedBytes: memory?.usedJSHeapSize ?? null,
						};
					}
				}
				throw new Error(`Final message did not become visible within ${timeoutMs}ms; measurement incomplete`);
			} finally {
				stop();
			}
		},
	};
}

import type { ProfilerOnRenderCallback } from "react";

type CaptureStage = "fetch" | "json" | "conversion" | "grouping" | "renderSubmission";

/** Lab instrumentation only: never imported by the application. No message text is retained. */
export function measureChatOpen({ traceMarks = false }: { traceMarks?: boolean } = {}) {
	const startedAt = performance.now();
	const traceMarkPrefix = traceMarks ? `ao-chat-open-${Math.round(startedAt * 1000)}` : null;
	const mark = (label: string, at = performance.now()) => {
		if (traceMarkPrefix) performance.mark(`${traceMarkPrefix}:${label}`, { startTime: at });
	};
	mark("start", startedAt);
	const stages: { name: CaptureStage; startMs: number; durationMs: number; failed: boolean }[] = [];
	const startStage = (name: CaptureStage) => {
		const start = performance.now();
		mark(`${name}:start`, start);
		let ended = false;
		return (failed = false) => {
			if (ended) return;
			ended = true;
			mark(`${name}:end`);
			stages.push({ name, startMs: start - startedAt, durationMs: performance.now() - start, failed });
		};
	};
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
		mark("react-commit", commitTime);
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
		startStage,
		measureSync<T>(name: CaptureStage, work: () => T): T {
			const end = startStage(name);
			try { const value = work(); end(); return value; }
			catch (error) { end(true); throw error; }
		},
		async measureAsync<T>(name: CaptureStage, work: () => Promise<T>): Promise<T> {
			const end = startStage(name);
			try { const value = await work(); end(); return value; }
			catch (error) { end(true); throw error; }
		},
		/** Call after starting navigation/render. Selector must identify the final durable message. */
		async finish(lastMessage: () => HTMLElement | null, timeoutMs = 15_000) {
			try {
				let visibleFrames = 0;
				let firstVisibleFrameMs: number | null = null;
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
					if (visible && firstVisibleFrameMs === null) {
						firstVisibleFrameMs = performance.now() - startedAt;
						mark("first-visible-frame");
					}
					if (visibleFrames === 2) {
						const lastMessageVisibleMs = performance.now() - startedAt;
						mark("two-frame-visible");
						// Observer delivery occurs after a task ends. Let that task finish,
						// then filter out entries belonging to the post-visibility drain.
						await new Promise<void>((resolve) => setTimeout(resolve, 0));
						stop();
						const tasks = longTasks.filter((entry) => entry.startMs < lastMessageVisibleMs);
						const reactCommits = commits.filter((entry) => entry.commitMs <= lastMessageVisibleMs);
						const memory = (performance as Performance & { memory?: { usedJSHeapSize: number } }).memory;
						const firstPaint = performance.getEntriesByType("paint")
							.find((entry) => entry.name === "first-paint" && entry.startTime >= startedAt);
						return {
							traceMarkPrefix,
							lastMessageVisibleMs,
							firstVisibleFrameMs,
							// Two visible frames allow a paint opportunity; they do not prove presentation.
							firstPaintOpportunityMs: lastMessageVisibleMs,
							// PaintTiming describes document load, not a route's chat-content paint.
							documentFirstPaintMs: firstPaint ? firstPaint.startTime - startedAt : null,
							stages: Object.fromEntries((["fetch", "json", "conversion", "grouping", "renderSubmission"] as const)
								.map((name) => [name, stages.some((stage) => stage.name === name)
									? stages.filter((stage) => stage.name === name) : null])),
							longTasksSupported,
							longTasks: longTasksSupported ? {
								count: tasks.length,
								maxMs: Math.max(0, ...tasks.map((task) => task.durationMs)),
								totalMs: tasks.reduce((sum, task) => sum + task.durationMs, 0),
								entries: tasks,
							} : null,
							// actualDuration is React render work per commit, not commit-phase wall time.
							reactProfilerSamplesAvailable: reactCommits.length > 0,
							reactCommitPhaseWallMs: null,
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

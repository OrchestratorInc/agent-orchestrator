import type { ResourceSummary } from "../shared/performance-telemetry";

/** The fields of Electron's ProcessMetric the sampler reads. */
export type ProcessMetricLike = {
	pid: number;
	cpu: { percentCPUUsage: number };
	memory: { workingSetSize: number };
};

/**
 * Keeps peak memory and time-weighted CPU across samples for
 * ao.ui.performance_summary. Electron reports CPU as the average since the
 * previous getAppMetrics() call, so the first sample only sets the baseline.
 */
export function createResourceSampler(
	getMetrics: () => ProcessMetricLike[],
	shellPid: () => number | undefined,
	now: () => number = Date.now,
) {
	let rendererPeakKb = 0;
	let appPeakKb = 0;
	let cpuWeighted = 0;
	let cpuElapsedMs = 0;
	let lastSampleAt: number | undefined;

	function sample(): void {
		let metrics: ProcessMetricLike[];
		try {
			metrics = getMetrics();
		} catch {
			return;
		}
		const at = now();
		const pid = shellPid();
		let appKb = 0;
		let cpu = 0;
		for (const metric of metrics) {
			appKb += metric.memory.workingSetSize;
			cpu += metric.cpu.percentCPUUsage;
			if (metric.pid === pid) rendererPeakKb = Math.max(rendererPeakKb, metric.memory.workingSetSize);
		}
		appPeakKb = Math.max(appPeakKb, appKb);
		if (lastSampleAt !== undefined && at > lastSampleAt && Number.isFinite(cpu)) {
			cpuWeighted += cpu * (at - lastSampleAt);
			cpuElapsedMs += at - lastSampleAt;
		}
		lastSampleAt = at;
	}

	function reset(): void {
		rendererPeakKb = 0;
		appPeakKb = 0;
		cpuWeighted = 0;
		cpuElapsedMs = 0;
	}

	return {
		sample,
		/** Drops what was kept and the CPU baseline, so the next sample starts over. */
		clear(): void {
			reset();
			lastSampleAt = undefined;
		},
		/** Samples once more, returns what was kept since the previous take, and starts over. */
		take(): ResourceSummary | null {
			sample();
			const summary = appPeakKb > 0
				? {
					rendererPeakMb: Math.round(rendererPeakKb / 1024),
					appPeakMb: Math.round(appPeakKb / 1024),
					cpuAvgPct: cpuElapsedMs > 0 ? Math.round((cpuWeighted / cpuElapsedMs) * 10) / 10 : 0,
				}
				: null;
			reset();
			return summary;
		},
	};
}

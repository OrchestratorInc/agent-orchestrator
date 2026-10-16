import path from "node:path";
import type { ProcessMetric } from "electron";

export const PROCESS_METRICS_INTERVAL_MS = 5 * 60_000;

/** Crash dumps live inside the Electron profile, which main.ts pins under the AO data dir. */
export function crashDumpsDir(userDataDir: string): string {
	return path.join(userDataDir, "Crashpad");
}

type MetricSample = Pick<ProcessMetric, "pid" | "type" | "memory"> & Partial<Pick<ProcessMetric, "name" | "serviceName">>;

const mb = (kilobytes: number) => `${Math.round(kilobytes / 1024)}MB`;

/**
 * One launcher-log line per sample, largest process first, so a renderer that
 * grows between samples is easy to spot after the fact. Electron reports
 * memory in kilobytes; privateBytes exists only on Windows.
 */
export function formatAppMetrics(metrics: readonly MetricSample[], shellPid?: number): string {
	const rows = [...metrics]
		.sort((left, right) => right.memory.workingSetSize - left.memory.workingSetSize)
		.map((metric) => {
			const label = metric.pid === shellPid ? "shell" : (metric.serviceName ?? metric.name ?? metric.type);
			const privateBytes = metric.memory.privateBytes === undefined ? "" : ` private=${mb(metric.memory.privateBytes)}`;
			return `${metric.type}:${metric.pid}(${label}) ws=${mb(metric.memory.workingSetSize)} peak=${mb(metric.memory.peakWorkingSetSize)}${privateBytes}`;
		});
	const total = metrics.reduce((sum, metric) => sum + metric.memory.workingSetSize, 0);
	return `AO: process metrics total_ws=${mb(total)} | ${rows.join(" | ")}`;
}

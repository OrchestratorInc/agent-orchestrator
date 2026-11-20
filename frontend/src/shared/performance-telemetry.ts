/**
 * Where a shell page's launch started, in ms since the epoch. "cold" starts at
 * process creation, "warm" when a window was reopened in a running process.
 * Main hands this to each shell page once, so a reload gets null.
 */
export type LaunchTimingStart = { kind: "cold" | "warm"; startedAtMs: number };

/** App process resource use since the previous read. Memory is the peak sample. */
export type ResourceSummary = {
	/** Peak working set of the shell renderer process, in MB. */
	rendererPeakMb: number;
	/** Peak working set summed over every Electron process, in MB. */
	appPeakMb: number;
	/** CPU summed over every Electron process, averaged over the time between samples. Can exceed 100 on multi-core machines. */
	cpuAvgPct: number;
};

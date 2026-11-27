/**
 * Where a shell page's launch started, in ms since the epoch. "cold" starts at
 * process creation, "warm" when a window was reopened in a running process.
 * Main hands this to each shell page once, so a reload gets null.
 */
export type LaunchTimingStart = {
	kind: "cold" | "warm";
	startedAtMs: number;
	/** A cold launch that found no database: the first run on this data directory. */
	firstRun?: boolean;
};

/**
 * Main-process launch steps, in ms since the process started: Electron ready,
 * the window shown, the daemon spawned and the daemon ready. A step that has
 * not happened is absent. serverReused is true when the app attached to a
 * daemon that was already running instead of spawning one.
 */
export type LaunchSteps = {
	appReadyMs?: number;
	windowShownMs?: number;
	serverSpawnMs?: number;
	serverReadyMs?: number;
	serverReused?: boolean;
};

/** App process resource use since the previous read. Memory is the peak sample. */
export type ResourceSummary = {
	/** Peak working set of the shell renderer process, in MB. */
	rendererPeakMb: number;
	/** Peak working set summed over every Electron process, in MB. */
	appPeakMb: number;
	/** CPU summed over every Electron process, averaged over the time between samples. Can exceed 100 on multi-core machines. */
	cpuAvgPct: number;
};

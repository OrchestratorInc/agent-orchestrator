import { describe, expect, it } from "vitest";
import { createResourceSampler, type ProcessMetricLike } from "./resource-sampler";

const metric = (pid: number, workingSetMb: number, cpu: number): ProcessMetricLike => ({
	pid,
	cpu: { percentCPUUsage: cpu },
	memory: { workingSetSize: workingSetMb * 1024 },
});

describe("createResourceSampler", () => {
	it("keeps peak memory and time-weighted CPU, then starts over", () => {
		let now = 0;
		let metrics: ProcessMetricLike[] = [metric(1, 100, 0), metric(2, 300, 0)];
		const sampler = createResourceSampler(() => metrics, () => 2, () => now);

		// The first sample only sets the CPU baseline.
		sampler.sample();
		now = 10 * 60_000;
		metrics = [metric(1, 150, 1), metric(2, 500, 3)];
		sampler.sample();
		now = 20 * 60_000;
		metrics = [metric(1, 120, 0.5), metric(2, 400, 1.5)];

		expect(sampler.take()).toEqual({ rendererPeakMb: 500, appPeakMb: 650, cpuAvgPct: 3 });
		now = 30 * 60_000;
		metrics = [metric(1, 50, 1), metric(2, 60, 1)];
		expect(sampler.take()).toEqual({ rendererPeakMb: 60, appPeakMb: 110, cpuAvgPct: 2 });
	});

	it("reports no renderer peak when the shell process is gone", () => {
		const sampler = createResourceSampler(() => [metric(1, 100, 0)], () => undefined, () => 0);
		expect(sampler.take()).toEqual({ rendererPeakMb: 0, appPeakMb: 100, cpuAvgPct: 0 });
	});

	it("returns null when metrics cannot be read", () => {
		const sampler = createResourceSampler(() => {
			throw new Error("not ready");
		}, () => undefined);
		expect(sampler.take()).toBeNull();
	});
});

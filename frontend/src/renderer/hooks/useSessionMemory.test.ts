import { describe, expect, it } from "vitest";
import { formatCPU, formatMemory, memoryPressure, memoryTone } from "./useSessionMemory";

describe("formatMemory", () => {
	it("uses binary units like btop", () => {
		expect(formatMemory(512)).toBe("1 KB");
		expect(formatMemory(641_728_512)).toBe("612 MB");
		expect(formatMemory(2_254_857_830)).toBe("2.1 GB");
	});
});

describe("formatCPU", () => {
	it("rounds to whole percent above one", () => {
		expect(formatCPU(0)).toBe("0%");
		expect(formatCPU(0.34)).toBe("0.3%");
		expect(formatCPU(82.4)).toBe("82%");
	});
});

describe("memoryTone", () => {
	it("escalates at one and two gigabytes", () => {
		expect(memoryTone(900 * 1024 ** 2)).toBe("default");
		expect(memoryTone(1024 ** 3)).toBe("warning");
		expect(memoryTone(2 * 1024 ** 3)).toBe("critical");
	});
});

describe("memoryPressure", () => {
	const GIB = 1024 ** 3;
	const host = (totalGiB: number, availableGiB: number, extra: Partial<Parameters<typeof memoryPressure>[0]> = {}) => ({
		totalBytes: totalGiB * GIB,
		availableBytes: availableGiB * GIB,
		swapTotalBytes: 8 * GIB,
		swapUsedBytes: 0,
		swapBytesPerSec: 0,
		cpuCount: 8,
		load1: 0,
		...extra,
	});

	it("colours by how much of the machine is still free, not by AO's share", () => {
		expect(memoryPressure(host(16, 8))).toMatchObject({ freePct: 50, tone: "default", reason: "memory" });
		expect(memoryPressure(host(16, 3))).toMatchObject({ freePct: 19, tone: "warning" });
		expect(memoryPressure(host(16, 1))).toMatchObject({ freePct: 6, tone: "critical" });
	});

	it("is red the moment the host is actively swapping, whatever the percent says", () => {
		expect(memoryPressure(host(16, 8, { swapBytesPerSec: 4 * 1024 ** 2 }))).toMatchObject({ tone: "critical", reason: "swap", swapping: true });
		expect(memoryPressure(host(16, 8, { swapBytesPerSec: 4096 })).swapping).toBe(false);
	});

	it("goes yellow, never red, when the cores are pinned", () => {
		expect(memoryPressure(host(16, 8, { load1: 9 }))).toMatchObject({ tone: "warning", reason: "cpu", cpuLoad: 1.125 });
		expect(memoryPressure(host(16, 1, { load1: 9 }))).toMatchObject({ tone: "critical", reason: "memory" });
	});

	it("flags when free RAM sits under the user's reserve", () => {
		expect(memoryPressure(host(16, 8), 2 * GIB).belowReserve).toBe(false);
		expect(memoryPressure(host(16, 1.5), 2 * GIB).belowReserve).toBe(true);
		expect(memoryPressure(host(16, 1.5)).belowReserve).toBe(false);
	});

	it("treats an unreadable host as roomy", () => {
		expect(memoryPressure(host(0, 0))).toMatchObject({ freePct: 100, tone: "default" });
	});
});

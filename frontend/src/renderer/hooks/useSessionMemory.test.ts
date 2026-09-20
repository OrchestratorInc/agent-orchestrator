import { describe, expect, it } from "vitest";
import { formatMemory, memoryPressure, memoryTone } from "./useSessionMemory";

describe("formatMemory", () => {
	it("uses binary units like btop", () => {
		expect(formatMemory(512)).toBe("1 KB");
		expect(formatMemory(641_728_512)).toBe("612 MB");
		expect(formatMemory(2_254_857_830)).toBe("2.1 GB");
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
	const host = (totalGiB: number, availableGiB: number) => ({ totalBytes: totalGiB * GIB, availableBytes: availableGiB * GIB });

	it("colours by AO's share of its budget: three quarters is a glance, over budget a problem", () => {
		expect(memoryPressure(2 * GIB, host(32, 20), 8 * GIB)).toMatchObject({ pct: 25, tone: "default" });
		expect(memoryPressure(6 * GIB, host(32, 20), 8 * GIB)).toMatchObject({ pct: 75, tone: "warning" });
		expect(memoryPressure(9 * GIB, host(32, 20), 8 * GIB)).toMatchObject({ pct: 113, tone: "critical" });
	});

	it("does not care about machine size when the budget is the same", () => {
		expect(memoryPressure(3 * GIB, host(8, 3), 4 * GIB).tone).toBe("warning");
		expect(memoryPressure(3 * GIB, host(64, 50), 4 * GIB).tone).toBe("warning");
	});

	it("escalates when the host is nearly full even if AO is within budget", () => {
		expect(memoryPressure(1 * GIB, host(16, 2), 4 * GIB)).toMatchObject({ pct: 25, freePct: 13, tone: "warning" });
		expect(memoryPressure(1 * GIB, host(16, 1), 4 * GIB)).toMatchObject({ pct: 25, freePct: 6, tone: "critical" });
	});

	it("reports zero without a budget", () => {
		expect(memoryPressure(1 * GIB, host(0, 0), 0)).toMatchObject({ pct: 0, tone: "default" });
	});
});

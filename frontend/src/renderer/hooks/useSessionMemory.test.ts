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

	it("colours by AO's share of the machine: a tenth is a glance, a quarter a problem", () => {
		expect(memoryPressure(1 * GIB, host(16, 10))).toMatchObject({ pct: 6, tone: "default" });
		expect(memoryPressure(1.6 * GIB, host(16, 10))).toMatchObject({ pct: 10, tone: "warning" });
		expect(memoryPressure(4 * GIB, host(16, 10))).toMatchObject({ pct: 25, tone: "critical" });
	});

	it("escalates when the host is nearly full even if AO's share is small", () => {
		expect(memoryPressure(1 * GIB, host(16, 2))).toMatchObject({ pct: 6, freePct: 13, tone: "warning" });
		expect(memoryPressure(1 * GIB, host(16, 1))).toMatchObject({ pct: 6, freePct: 6, tone: "critical" });
	});

	it("reports zero without a readable total", () => {
		expect(memoryPressure(1 * GIB, host(0, 0))).toMatchObject({ pct: 0, tone: "default" });
	});
});

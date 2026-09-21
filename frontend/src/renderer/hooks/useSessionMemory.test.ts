import { describe, expect, it } from "vitest";
import { formatCPU, formatMemory } from "./useSessionMemory";

describe("formatMemory", () => {
	it("rounds to ten megabytes and switches to GB at a thousand", () => {
		expect(formatMemory(641_728_512)).toBe("640 MB");
		expect(formatMemory(2_254_857_830)).toBe("2.3 GB");
	});
});

describe("formatCPU", () => {
	it("is a whole percent of one core", () => {
		expect(formatCPU(82.4)).toBe("82%");
		expect(formatCPU(0.34)).toBe("0%");
	});
});

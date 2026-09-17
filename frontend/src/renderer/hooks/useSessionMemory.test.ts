import { describe, expect, it } from "vitest";
import { formatMemory, memoryTone } from "./useSessionMemory";

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

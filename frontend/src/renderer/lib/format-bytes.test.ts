import { describe, expect, it } from "vitest";
import { formatBytes } from "./format-bytes";

describe("formatBytes", () => {
	it("formats raw bytes", () => {
		expect(formatBytes(0)).toBe("0 B");
		expect(formatBytes(512)).toBe("512 B");
	});
	it("formats kilobytes and megabytes without a trailing .0", () => {
		expect(formatBytes(1024)).toBe("1 KB");
		expect(formatBytes(45 * 1024 * 1024)).toBe("45 MB");
	});
	it("keeps one decimal below 100", () => {
		expect(formatBytes(1536 * 1024)).toBe("1.5 MB");
		expect(formatBytes(2576980378)).toBe("2.4 GB");
	});
	it("caps at terabytes", () => {
		expect(formatBytes(1536 * 1024 ** 3)).toBe("1.5 TB");
	});
});

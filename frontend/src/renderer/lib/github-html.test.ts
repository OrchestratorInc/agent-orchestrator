import { describe, expect, it } from "vitest";
import { isDecorativeIcon } from "./github-html";

describe("isDecorativeIcon", () => {
	it("treats an untitled icon-sized image as decoration", () => {
		expect(isDecorativeIcon("", 16, 16)).toBe(true);
		expect(isDecorativeIcon(undefined, "12", undefined)).toBe(true);
	});

	it("keeps an image with alt text, no size, or any side past icon size", () => {
		expect(isDecorativeIcon("Graph", 16, 16)).toBe(false);
		expect(isDecorativeIcon("", undefined, undefined)).toBe(false);
		expect(isDecorativeIcon("", 600, 32)).toBe(false);
		expect(isDecorativeIcon("", "32", "600")).toBe(false);
	});
});

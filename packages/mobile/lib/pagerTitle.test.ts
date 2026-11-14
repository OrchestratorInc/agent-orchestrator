import { describe, expect, it } from "vitest";
import { headerTitleWidth } from "./pagerTitle";

describe("headerTitleWidth", () => {
	it("leaves room for two actions on a phone", () => {
		// iPhone 17 Pro: back button to x 60, history and ⋯ from x 282.
		expect(headerTitleWidth(402, 2)).toBe(154);
	});

	it("is wider with one action, up to the cap", () => {
		expect(headerTitleWidth(402, 1)).toBe(240);
		expect(headerTitleWidth(320, 1)).toBe(192);
	});

	it("caps on a wide screen and keeps a floor on a narrow one", () => {
		expect(headerTitleWidth(1024, 2)).toBe(240);
		expect(headerTitleWidth(280, 2)).toBe(120);
	});
});

import { describe, expect, it } from "vitest";
import { syncEdgeFade } from "./useEdgeFade";

function scroller(layout: { scrollTop?: number; scrollLeft?: number; scrollHeight?: number; clientHeight?: number; scrollWidth?: number; clientWidth?: number }) {
	const element = document.createElement("div");
	for (const [key, value] of Object.entries(layout)) Object.defineProperty(element, key, { configurable: true, value });
	return element;
}

describe("syncEdgeFade", () => {
	it("fades no edge of a scroller that fits", () => {
		const element = scroller({ scrollTop: 0, scrollHeight: 100, clientHeight: 100 });
		syncEdgeFade(element);
		expect(element).not.toHaveAttribute("data-fade-start");
		expect(element).not.toHaveAttribute("data-fade-end");
	});

	it("fades only the edges with content past them", () => {
		const top = scroller({ scrollTop: 0, scrollHeight: 300, clientHeight: 100 });
		syncEdgeFade(top);
		expect(top).not.toHaveAttribute("data-fade-start");
		expect(top).toHaveAttribute("data-fade-end");

		const end = scroller({ scrollTop: 200, scrollHeight: 300, clientHeight: 100 });
		syncEdgeFade(end);
		expect(end).toHaveAttribute("data-fade-start");
		expect(end).not.toHaveAttribute("data-fade-end");
	});

	it("reads the horizontal axis and can hold the end fade off", () => {
		const strip = scroller({ scrollLeft: 50, scrollWidth: 400, clientWidth: 200 });
		syncEdgeFade(strip, "x");
		expect(strip).toHaveAttribute("data-fade-start");
		expect(strip).toHaveAttribute("data-fade-end");

		syncEdgeFade(strip, "x", true);
		expect(strip).not.toHaveAttribute("data-fade-end");
	});
});

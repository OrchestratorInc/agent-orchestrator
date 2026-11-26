import { describe, expect, it } from "vitest";
import { harnessUpdateNoticeIntrusion } from "./useHarnessUpdateNoticeClearance";

describe("harnessUpdateNoticeIntrusion", () => {
	// A 300px notice pinned 16px from the right edge of a 1320px window, below the top bar.
	const notice = { left: 1004, top: 72, right: 1304, bottom: 140 };

	it("is 0 without a notice or when the content is clear of it", () => {
		expect(harnessUpdateNoticeIntrusion({ left: 300, top: 80, right: 1170, bottom: 200 }, null)).toBe(0);
		// Beside it, gap included.
		expect(harnessUpdateNoticeIntrusion({ left: 300, top: 80, right: 992, bottom: 200 }, notice)).toBe(0);
		// Scrolled below it.
		expect(harnessUpdateNoticeIntrusion({ left: 300, top: 150, right: 1170, bottom: 300 }, notice)).toBe(0);
	});

	it("is how far the content reaches under the notice, counting the gap", () => {
		expect(harnessUpdateNoticeIntrusion({ left: 404, top: 80, right: 1170, bottom: 200 }, notice)).toBe(178);
		expect(harnessUpdateNoticeIntrusion({ left: 260, top: 0, right: 1316, bottom: 900 }, notice)).toBe(324);
	});

	it("never takes more than half the content", () => {
		expect(harnessUpdateNoticeIntrusion({ left: 900, top: 80, right: 1300, bottom: 200 }, notice)).toBe(200);
	});
});

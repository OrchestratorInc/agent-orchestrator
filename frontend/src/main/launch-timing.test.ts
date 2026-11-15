import { describe, expect, it } from "vitest";
import { createLaunchTiming } from "./launch-timing";

describe("createLaunchTiming", () => {
	it("reports each mark once, in ms since the process started", () => {
		let now = 1_000;
		const timing = createLaunchTiming(1_000, () => now);
		now = 1_312.4;
		timing.mark("appReady");
		now = 1_900;
		timing.mark("windowShown");
		now = 5_000;
		timing.mark("appReady");

		expect(timing.line()).toBe("launch timing: appReady=312ms windowShown=900ms");
	});
});

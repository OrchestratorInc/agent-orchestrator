import { describe, expect, it } from "vitest";
import { parkSheetLink, takeSheetLink } from "./sheetLink";

describe("sheet links", () => {
	it("hands a parked link over exactly once", () => {
		expect(takeSheetLink()).toBeUndefined();
		parkSheetLink("https://github.com/acme/repo/pull/1#discussion_r1");
		expect(takeSheetLink()).toBe("https://github.com/acme/repo/pull/1#discussion_r1");
		expect(takeSheetLink()).toBeUndefined();
	});
});

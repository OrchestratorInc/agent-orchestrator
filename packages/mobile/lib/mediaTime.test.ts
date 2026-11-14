import { describe, expect, it } from "vitest";
import { formatMediaTime } from "./mediaTime";

describe("formatMediaTime", () => {
	it("writes minutes and seconds, as a player does", () => {
		expect(formatMediaTime(0)).toBe("0:00");
		expect(formatMediaTime(6.9)).toBe("0:06");
		expect(formatMediaTime(65)).toBe("1:05");
	});

	it("adds hours past an hour", () => {
		expect(formatMediaTime(3725)).toBe("1:02:05");
	});

	it("reads a length not known yet as zero", () => {
		expect(formatMediaTime(Number.NaN)).toBe("0:00");
		expect(formatMediaTime(-1)).toBe("0:00");
	});
});

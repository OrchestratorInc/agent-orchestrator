import { describe, expect, it } from "vitest";
import { truncateMiddle } from "./utils";

describe("truncateMiddle", () => {
	it("keeps the start and end of a long version", () => {
		expect(truncateMiddle("v0.0.1792022479-ge66e24")).toBe("v0.0.179…6e24");
	});

	it("leaves text that already fits alone", () => {
		expect(truncateMiddle("v2.1.300")).toBe("v2.1.300");
		expect(truncateMiddle("2026.10.14-0f9")).toBe("2026.10.14-0f9");
	});

	it("takes custom head and tail lengths", () => {
		expect(truncateMiddle("abcdefghijklmnop", 3, 2)).toBe("abc…op");
	});
});

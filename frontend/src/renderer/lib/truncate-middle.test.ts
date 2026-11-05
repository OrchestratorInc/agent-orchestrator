import { describe, expect, it } from "vitest";
import { truncateMiddle } from "./utils";

describe("truncateMiddle", () => {
	it("keeps the start and end of a long version", () => {
		expect(truncateMiddle("v0.0.1792022479-ge66e24")).toBe("v0.0.179…6e24");
	});

	it("leaves text that already fits alone", () => {
		expect(truncateMiddle("v2.1.300")).toBe("v2.1.300");
		// Head 8 + "…" + tail 4 is 13 characters: 13 characters fit as they are.
		expect(truncateMiddle("v0.0.179-6e24")).toBe("v0.0.179-6e24");
		expect(truncateMiddle("v0.0.179-6e245")).toBe("v0.0.179…e245");
	});

	it("takes custom head and tail lengths", () => {
		expect(truncateMiddle("abcdefghijklmnop", 3, 2)).toBe("abc…op");
		expect(truncateMiddle("abcdefghijklmnop", 3, 0)).toBe("abc…");
	});
});
